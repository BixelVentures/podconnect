package main

// Opt-in startup preparation only. Reads never launch a sender, mutate old
// Spotify config, select a receiver, or replay a prior playback operation.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type radioProducerBootstrapOptions struct {
	Enabled       bool   `json:"radio_enabled"`
	Mode          string `json:"radio_owner_mode"`
	Physical      bool   `json:"radio_physical_accepted"`
	NativeURL     string `json:"radio_native_url"`
	NativeToken   string `json:"radio_native_token"`
	Secret        string `json:"radio_handoff_secret"`
	AdminPassword string `json:"radio_sender_admin_password"`
	API           int    `json:"radio_sender_api_port"`
	WS            int    `json:"radio_sender_ws_port"`
	Cgroup        string `json:"radio_cgroup_parent"`
	Containment   string `json:"radio_containment"`
	stage         string
	controller    string
}

func (o *radioProducerBootstrapOptions) generalConfig() string {
	if o == nil {
		return ""
	}
	return fmt.Sprintf(" admin_password = \"%s\"\n trusted_networks = {}\n bind_address = \"127.0.0.1\"\n radio_controller_id = \"%s\"\n radio_handoff_secret = \"%s\"\n radio_pairing_import_path = \"%s\"\n", o.AdminPassword, o.controller, o.Secret, o.stage)
}
func readRadioProducerBootstrapOptions() (radioProducerBootstrapOptions, error) {
	o, e := readRadioOptions()
	if e != nil {
		return o, e
	}
	worker := o.Containment == "worker"
	if worker {
		if !o.Enabled || o.Mode != "builtin" || !producerHex(o.NativeToken, len(o.NativeToken)) || len(o.NativeToken) < 32 || len(o.NativeToken) > 256 || !producerHex(o.Secret, 64) || !producerHex(o.AdminPassword, len(o.AdminPassword)) || len(o.AdminPassword) < 32 || len(o.AdminPassword) > 256 || o.Cgroup != "" {
			return o, errRadioUnavailable
		}
		if o.API == 0 {
			o.API = 3989
		}
		if o.WS == 0 {
			o.WS = 3990
		}
		return o, nil
	}
	optionsPath := filepath.Join(dataDir, "options.json")
	info, e := os.Lstat(optionsPath)
	if e != nil {
		return o, e
	}
	raw, e := os.ReadFile(optionsPath)
	if e != nil {
		return o, e
	}
	if json.Unmarshal(raw, &o) != nil {
		return o, errRadioUnavailable
	}
	if !o.Enabled || o.Mode != "builtin" || !o.Physical || len(o.NativeToken) < 32 || len(o.NativeToken) > 256 || !producerHex(o.NativeToken, len(o.NativeToken)) || !producerHex(o.Secret, 64) || len(o.AdminPassword) < 32 || len(o.AdminPassword) > 256 || !producerHex(o.AdminPassword, len(o.AdminPassword)) || o.Cgroup == "" {
		return o, errRadioUnavailable
	}
	// The radio child's permanent uid must not be able to read the Native
	// control token. Refuse unsafe opt-in storage; never chmod or rewrite the
	// shared Spotify/HA options file as a side effect of radio startup.
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		return o, errRadioUnavailable
	}
	if o.API == 0 {
		o.API = 3989
	}
	if o.WS == 0 {
		o.WS = 3990
	}
	return o, nil
}
func radioRoleEnvironment(initialize bool) []string {
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "PODCONNECT_RADIO_") {
			env = append(env, v)
		}
	}
	env = append(env, "PODCONNECT_RADIO_SENDER=1")
	if initialize {
		env = append(env, "PODCONNECT_RADIO_INITIALIZE_ONLY=1")
	}
	return env
}

// The independent sender never needs the legacy C process's saved-root
// /proc scope. A permanent kernel uid transition prevents its decoder from
// regaining root and changing its own delegated budget or manager journal.
func radioServiceCredential() (*syscall.Credential, error) {
	return radioServiceCredentialNamed("owntone")
}
func radioServiceCredentialNamed(name string) (*syscall.Credential, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return nil, errRadioUnavailable
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 || uid == uint64(os.Geteuid()) {
		return nil, errRadioUnavailable
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil || gid == 0 {
		return nil, errRadioUnavailable
	}
	if name == "podradio" {
		spotify, err := user.Lookup("owntone")
		if err != nil || spotify.Uid == account.Uid || spotify.Gid == account.Gid {
			return nil, errRadioUnavailable
		}
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}

// The fixed early-exit export reads local SQLite only; no network decoder runs.
// Its root-owned staging directory keeps Spotify's DB inaccessible to podradio.
func runRadioTrustedPairingExport(ctx context.Context, args []string) error {
	if len(args) < 5 || len(args) > 20 || args[0] != "--radio-pairing-export-known" || !filepath.IsAbs(args[1]) || !filepath.IsAbs(args[2]) || !producerControllerID(args[3]) {
		return errRadioUnavailable
	}
	for _, id := range args[4:] {
		if !producerCounter(id, false) {
			return errRadioUnavailable
		}
	}
	directory, err := os.MkdirTemp(dataDir, ".radio-pair-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	destination := args[2]
	commandArgs := append([]string{}, args...)
	commandArgs[2] = filepath.Join(directory, "pairing.json")
	// Match the original exporter's immutable-stage semantics. Its existing
	// identity check validates controller/receiver keys against the live source
	// and keeps the original bytes when only manual volume has changed. Starting
	// from an empty temp file would incorrectly replace that stage after a clean
	// Spotify return, making the radio DB reject its own stored import digest.
	old, openErr := os.OpenFile(destination, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if openErr == nil {
		st, statErr := old.Stat()
		if statErr != nil || !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > 64*1024 {
			old.Close()
			return errRadioUnavailable
		}
		identity, ok := st.Sys().(*syscall.Stat_t)
		if !ok || identity.Nlink != 1 {
			old.Close()
			return errRadioUnavailable
		}
		raw, readErr := io.ReadAll(io.LimitReader(old, 64*1024+1))
		closeErr := old.Close()
		if readErr != nil || closeErr != nil || len(raw) > 64*1024 || writeRadioBootConfig(commandArgs[2], raw) != nil {
			return errRadioUnavailable
		}
	} else if !os.IsNotExist(openErr) {
		return errRadioUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Apply limits before the trusted export entry. Never invoke a shell or log
	// the bounded pairing result; it is transferred with a private atomic write.
	launch := append([]string{"--as=536870912", "--cpu=3", "--nofile=64", "--", "/usr/sbin/owntone"}, commandArgs...)
	cmd := exec.CommandContext(bounded, "/usr/bin/prlimit", launch...)
	cmd.Env = radioRoleEnvironment(false)
	setProcGroup(cmd)
	if err = cmd.Run(); err != nil {
		return errRadioUnavailable
	}
	st, err := os.Lstat(commandArgs[2])
	if err != nil || !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > 64*1024 {
		return errRadioUnavailable
	}
	raw, err := os.ReadFile(commandArgs[2])
	if err != nil {
		return errRadioUnavailable
	}
	return writeRadioBootConfig(destination, raw)
}

func runRadioBootstrapCommand(ctx context.Context, parent string, args []string, initialize bool) error {
	cage, e := radioConfiguredContainment(parent)
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, "/usr/sbin/owntone", args...)
	cmd.Env = radioRoleEnvironment(initialize)
	// No secret arguments, output or logged pairing material. The actual C early
	// helper opens the original SQLite READONLY and exports exact receiver keys.
	serviceUser := "owntone"
	if parent == "" {
		serviceUser = "podradio"
	}
	credential, err := radioServiceCredentialNamed(serviceUser)
	if err != nil {
		_ = cage.Close()
		return errRadioUnavailable
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	if e = cage.Configure(cmd); e != nil {
		_ = cage.Close()
		return e
	}
	cmd.Cancel = func() error { return cage.Kill() }
	cmd.WaitDelay = 2 * time.Second
	var failure <-chan struct{}
	if health, ok := cage.(interface{ Failure() <-chan struct{} }); ok {
		failure = health.Failure()
	}
	select {
	case <-failure:
		_ = cage.Close()
		return errRadioUnavailable
	default:
	}
	if e = cmd.Start(); e != nil {
		_ = cage.Close()
		return e
	}
	if e = radioContainmentAfterStartOneShot(cmd, cage); e != nil {
		_ = cage.Kill()
		_ = cmd.Wait()
		_ = cage.Close()
		return e
	}
	done := make(chan struct{})
	if failure != nil {
		go func() {
			select {
			case <-failure:
				_ = cage.Kill()
			case <-done:
			}
		}()
	}
	runErr := cmd.Wait()
	close(done)
	killErr := cage.Kill()
	closeErr := cage.Close()
	return errors.Join(runErr, killErr, closeErr)
}
func prepareConfiguredRadioProducer(ctx context.Context, o radioProducerBootstrapOptions, n *radioProducerNativeClient) (*radioProducer, error) {
	worker := o.Containment == "worker"
	if !o.Enabled || o.Mode != "builtin" || !o.Physical && !worker {
		return nil, errRadioUnavailable
	}
	evidence, e := n.Evidence(ctx)
	if e != nil || len(evidence.Missing) != 0 {
		return nil, errRadioUnavailable
	}
	n.mu.Lock()
	state := n.state
	n.mu.Unlock()
	if !producerControllerID(state.ControllerID) {
		return nil, errRadioUnavailable
	}
	o.controller = state.ControllerID
	primary := primaryRoom()
	if primary == nil || primary.Idx != 0 {
		return nil, errRadioUnavailable
	}
	rooms := loadRooms()
	mapping := map[string]string{}
	for _, room := range rooms {
		if room != nil {
			mapping[room.ID] = room.HomepodID
		}
	}
	ids := map[string]bool{}
	for _, target := range state.Targets {
		if !producerCounter(target.HomePodID, false) || mapping[target.ID] != target.HomePodID {
			return nil, errRadioUnavailable
		}
		ids[target.HomePodID] = true
	}
	if len(ids) == 0 {
		return nil, errRadioUnavailable
	}
	sender, e := newRadioSender(dataDir, o.API, o.WS, rooms)
	if e != nil {
		return nil, e
	}
	// An earlier uncertain/live source is never replaced by startup preparation.
	_, records, err := openRadioProducerJournal(filepath.Join(filepath.Dir(sender.root), "radio-owner-journal"))
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if !record.Terminal || record.Observation.SourceKind == "radio" && record.Observation.State == "playing" && !producerHistoryReleased(records, record) {
			return nil, errRadioUnknown
		}
	}

	o.stage = filepath.Join(sender.root, "pairing-stage.json")
	sender.radioConfig = &o
	sender.newContainment = func() (radioProcessContainment, error) { return radioConfiguredContainment(o.Cgroup) }
	serviceUser := "owntone"
	if worker {
		serviceUser = "podradio"
		sender.serviceUser = serviceUser
		sender.prepareOwnership = func(path string) error { return exec.Command("chown", "-R", "podradio:podradio", path).Run() }
	}
	credential, err := radioServiceCredentialNamed(serviceUser)
	if err != nil {
		return nil, err
	}
	sender.makeCommand = func(ctx context.Context, config string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "/usr/sbin/owntone", "-f", "-c", config)
		cmd.Env = radioRoleEnvironment(false)
		copy := *credential
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &copy}
		return cmd
	}
	config, e := sender.prepare()
	if e != nil {
		return nil, e
	}
	source := filepath.Join(primary.OwnDir, "songs3.db")
	if !filepath.IsAbs(source) || source == filepath.Join(sender.root, "radio-songs.db") {
		return nil, errRadioUnavailable
	}
	sorted := []string{}
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	args := append([]string{"--radio-pairing-export-known", source, o.stage, o.controller}, sorted...)
	// The stage and private DB use the same actual service uid across export,
	// initialize-only and normal runtime. Original Spotify files are never chowned.
	if worker {
		// This trusted, early-exit helper only reads the original SQLite database
		// and exports the closed receiver list. No DR decoder or network starts.
		e = runRadioTrustedPairingExport(ctx, args)
	} else {
		if e = sender.prepareOwnership(sender.root); e == nil {
			e = runRadioBootstrapCommand(ctx, o.Cgroup, args, false)
		}
	}
	if e != nil {
		return nil, e
	}
	if e = sender.prepareOwnership(sender.root); e != nil {
		return nil, e
	}
	if e = runRadioBootstrapCommand(ctx, o.Cgroup, []string{"-f", "-c", config}, true); e != nil {
		return nil, e
	}
	if e = radioSenderTreeSafe(sender.root); e != nil {
		return nil, e
	}
	// The long-lived process is idle and has no selected output; all subsequent
	// receiver effects require the canonical producer's real signed hold.
	if e = sender.startIdleProcess(ctx); e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = sender.stop(cleanup)
		}
	}()
	client, e := newRadioProducerSenderClient("http://127.0.0.1:"+strconv.Itoa(o.API), o.AdminPassword, n, sender)
	if e != nil {
		return nil, e
	}
	// The one supervised idle process may discover its configured receivers
	// later. Only a validated idle/discovery-pending reply can extend this wait;
	// death, malformed replies and uncertain custody never trigger a relaunch.
	if e = awaitRadioIdleEvidence(ctx, client, evidence, sender); e != nil {
		return nil, e
	}
	producer, e := newRadioProducer(n, client, filepath.Join(filepath.Dir(sender.root), "radio-owner-journal"), o.Physical)
	if e != nil {
		return nil, e
	}
	producer.runtimeReadinessOnly = worker
	// No implicit adoption of a prior session. Durable unknown blocks enable.
	if _, _, e = producer.Ready(ctx); e != nil {
		return nil, e
	}
	success = true
	return producer, nil
}

// Called once after the existing primary startup path. Default/off takes no
// worker and changes no original process/config behavior. An opt-in worker may
// wait for Native reads, but it never repeats a launch or command after failure.
func startConfiguredRadioBuiltin(ctx context.Context, c *radioCoordinator) {
	o, e := readRadioProducerBootstrapOptions()
	if e != nil {
		return
	}
	if o.NativeURL == "" {
		r := primaryRoom()
		if r == nil {
			return
		}
		o.NativeURL = "http://127.0.0.1:" + strconv.Itoa(r.GLPort)
	}
	if _, e := newRadioProducerNativeClient(o.NativeURL, o.NativeToken); e != nil {
		return
	}
	go func() {
		// Before preparation there is no sender, lease or effect to replay. Slow
		// startup/offline receivers therefore keep receiving bounded passive
		// reads instead of requiring a restart of the Spotify manager.
		n, err := awaitRadioNativeBootstrap(ctx, o.NativeURL, o.NativeToken, radioReadinessSleep)
		if err != nil {
			return
		}
		producer, err := prepareConfiguredRadioProducer(ctx, o, n)
		if err != nil {
			c.mu.Lock()
			c.state.Error = "Built-in radio bootstrap unavailable"
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		c.backend = producer
		c.state.Error = ""
		c.mu.Unlock()
		enableCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err = c.enable(enableCtx); err != nil {
			c.mu.Lock()
			c.state.Error = "Native radio safety proof unavailable"
			c.mu.Unlock()
		}
	}()
}

// A pending discovery is deliberately narrower than unavailable/unknown. It is
// emitted only after actual kernel custody, protocol and idle proof succeeded.
var errRadioSenderDiscoveryPending = errors.New("radio sender discovery pending")

type radioReadinessPause func(context.Context, time.Duration) error

func radioReadinessSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Every probe is bounded; backoff is capped. This helper can only perform its
// caller's reads and returns after readiness. No factory/effect is retried here.
func waitRadioPassiveReadiness(ctx context.Context, probe func(context.Context) (bool, bool, error), pause radioReadinessPause) error {
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		ready, retry, err := probe(readCtx)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		if ready && err == nil {
			return nil
		}
		if !retry {
			return errors.Join(errRadioUnavailable, err)
		}
		if err := pause(ctx, delay); err != nil {
			return err
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
}

// The Native payload is private. Only protocol-owned codes may reach startup logs.
func radioBootstrapMissingProofs(proofs []string) []string {
	seen := make(map[string]bool)
	for _, proof := range proofs {
		switch proof {
		case "actual_registered_radio_controller_binding", "conflicting_c_controller_identity",
			"fresh_receiver_source_proof", "fresh_explicit_intent_reacquire_ticket",
			"global_clean_absence_without_spotify_writer", "linux_native_process_identity",
			"radio_handoff_configuration":
		default:
			proof = "other"
		}
		seen[proof] = true
	}
	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func awaitRadioNativeBootstrap(ctx context.Context, base, token string, pause radioReadinessPause) (*radioProducerNativeClient, error) {
	var readyClient *radioProducerNativeClient
	lastReadiness := ""
	report := func(readiness string) {
		if readiness != lastReadiness {
			lastReadiness = readiness
			log.Printf("radio: bootstrap phase=native_wait %s", readiness)
		}
	}
	err := waitRadioPassiveReadiness(ctx, func(readCtx context.Context) (bool, bool, error) {
		// Pre-bootstrap reads carry no admitted source/operation. A fresh client
		// allows a cold Native process to be replaced before the ONE preparation
		// attempt. Once returned, its normal immutable process checks apply.
		n, err := newRadioProducerNativeClient(base, token)
		if err != nil {
			return false, false, err
		}
		evidence, err := n.Evidence(readCtx)
		if err != nil {
			report("result=read_failed")
			return false, true, err
		}
		available := 0
		for _, target := range evidence.Targets {
			if target.Selectable && target.Availability == "available" {
				available++
			}
		}
		ready := len(evidence.Missing) == 0 && available > 0
		result := "waiting"
		if ready {
			result = "ready"
		}
		n.mu.Lock()
		controllerRegistered := producerControllerID(n.state.ControllerID)
		n.mu.Unlock()
		report(fmt.Sprintf("result=%s controller_registered=%t configured_targets=%d selectable_available=%d missing_proofs=[%s]",
			result, controllerRegistered, len(evidence.Targets), available,
			strings.Join(radioBootstrapMissingProofs(evidence.Missing), ",")))
		// Held-only/controller metadata is not fresh initial acquisition
		// readiness. Keep reading until an actual source/absence path is offered
		// rather than consuming the one preparation attempt prematurely.
		if ready {
			readyClient = n
			return true, false, nil
		}
		return false, true, err
	}, pause)
	return readyClient, err
}

func awaitRadioIdleEvidence(ctx context.Context, client *radioProducerSenderClient, native radioProducerEvidence, sender *radioSender) error {
	parsed, err := url.Parse(client.http.base)
	if err != nil {
		return errRadioUnavailable
	}
	listenUntil := time.Now().Add(5 * time.Second)
	return waitRadioPassiveReadiness(ctx, func(readCtx context.Context) (bool, bool, error) {
		sender.mu.Lock()
		pid := 0
		if sender.cmd != nil && sender.cmd.Process != nil {
			pid = sender.cmd.Process.Pid
		}
		sender.mu.Unlock()
		if err := sender.productionEvidence(pid); err != nil {
			return false, false, fmt.Errorf("radio custody before readiness: %w", err)
		}
		// Only a socket not yet listening gets the bounded original warmup.
		// Once HTTP is reachable, a bad/auth/unknown response is fatal; it must
		// not be confused with the explicit validated discovery-pending state.
		dialer := net.Dialer{Timeout: 500 * time.Millisecond}
		connection, err := dialer.DialContext(readCtx, "tcp", parsed.Host)
		if err != nil {
			return false, time.Now().Before(listenUntil), err
		}
		connection.Close()
		evidence, err := client.Evidence(readCtx, native)
		if err == nil && len(evidence.Missing) == 0 {
			return true, false, nil
		}
		if errors.Is(err, errRadioSenderDiscoveryPending) {
			// Recheck custody after the response: death cannot become an
			// indefinitely retryable discovery result through a response race.
			if err := sender.productionEvidence(pid); err != nil {
				return false, false, fmt.Errorf("radio custody during discovery: %w", err)
			}
			return false, true, err
		}
		return false, false, fmt.Errorf("radio sender capability response: %w", err)
	}, radioReadinessSleep)
}
