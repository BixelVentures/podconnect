package main

// This is an opt-in Linux composition runner, NOT a replacement for packaging
// or household acceptance. Run it alone after the external Native fixture and
// old-C real HTTP owner have started in the SAME inert network namespace:
//
//   go test -count=1 -run '^TestRadioProductionPortsExternalChain$' -v
//
// Required PC_RADIO_CHAIN_* inputs: RUN=1, FIXTURE_ROOT (searchable private ephemeral
// directory, mode0711 with private files), CGROUP_PARENT (already delegated private cpu/memory/pids subtree),
// NATIVE_URL, NATIVE_TOKEN, TARGET_ID, RECEIVER_PORT, SENDER_BINARY, PAIRING_STAGE (actual v2
// export), HANDOFF_SECRET, ADMIN_PASSWORD, API_PORT, WS_PORT, MANAGER_PORT.
// The C fixture's --radio-http-owner reads PC_RADIO_HTTP_CONFIG and uses real
// configuration, DB/import, HTTP/auth/parser/player/output paths. Only the
// provider/receiver edges may be inert fixtures; no owner, receipt, capability,
// kernel custody, or READY override exists here. Its finite DR-catalog source
// substitution must be confined to the separately compiled C test artifact.
//
// Ordinary HA add-on packaging currently does not provision delegated cgroups.
// Ordinary safe Docker here mounts cgroup2 read-only. Those are deployment
// blockers, distinct from physical acceptance: do not add privilege/host-cgroup
// workarounds, silently use fake containment, or report a skipped run as proof.
import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Production main enters this same exact inherited-FD guardian before normal
// startup. A test executable needs the entry before testing parses its flags.
func init() { radioGuardianEntry() }

func radioChainEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv("PC_RADIO_CHAIN_" + key)
	if value == "" {
		t.Skip("NOT RUN: explicit inert chain prerequisite missing: " + key)
	}
	return value
}
func radioChainPort(t *testing.T, key string) int {
	t.Helper()
	value, e := strconv.Atoi(radioChainEnv(t, key))
	if e != nil || value < 1024 || value > 65535 {
		t.Fatalf("invalid isolated chain port: %s", key)
	}
	return value
}
func radioChainPath(t *testing.T, path string) string {
	t.Helper()
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || canonical != path {
		t.Fatal("chain path must be absolute and contain no symlinks")
	}
	return canonical
}
func radioChainInertNamespace(t *testing.T) {
	t.Helper()
	interfaces, e := net.Interfaces()
	if e != nil {
		t.Fatal(e)
	}
	for _, device := range interfaces {
		if device.Flags&net.FlagUp != 0 && device.Flags&net.FlagLoopback == 0 {
			t.Skip("NOT RUN: chain requires an isolated network namespace with only loopback active")
		}
	}
	raw, e := os.ReadFile("/proc/self/status")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			bits, e := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
			if e != nil {
				t.Fatal(e)
			}
			if bits&(1<<12|1<<21) != 0 {
				t.Skip("NOT RUN: chain requires no CAP_NET_ADMIN or CAP_SYS_ADMIN")
			}
			return
		}
	}
	t.Fatal("kernel capability information unavailable")
}
func radioChainPrivateFile(t *testing.T, root, path string) []byte {
	t.Helper()
	path = radioChainPath(t, path)
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		t.Fatal("fixture input must belong to its private ephemeral root")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 || info.Mode().Perm()&0077 != 0 {
		t.Fatal("fixture input is not a bounded private regular file")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestRadioProductionPortsExternalChain(t *testing.T) { radioProductionExternalChain(t, false) }

func TestRadioProductionFactoryExternalChain(t *testing.T) { radioProductionExternalChain(t, true) }

func radioProductionExternalChain(t *testing.T, factory bool) {
	if os.Getenv("PC_RADIO_CHAIN_RUN") != "1" {
		t.Skip("NOT RUN: opt-in inert Native/manager/old-C/radio-C chain environment not supplied")
	}
	if runtime.GOOS != "linux" {
		t.Skip("NOT RUN: real production cgroup/PID/guardian attestation requires Linux")
	}
	radioChainInertNamespace(t)
	fixtureRoot := radioChainPath(t, radioChainEnv(t, "FIXTURE_ROOT"))
	info, e := os.Stat(fixtureRoot)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0066 != 0 || info.Mode().Perm()&0001 == 0 {
		t.Skip("NOT RUN: fixture root must allow service-uid traversal but no group/other listing or writing (0711)")
	}
	parent := radioChainPath(t, radioChainEnv(t, "CGROUP_PARENT"))
	// This creates only one fresh child in the EXPLICITLY supplied delegated test
	// parent. It never enables controllers or writes the shared cgroup root.
	probe, e := newRadioCgroup(parent, defaultRadioResourceLimits())
	if e != nil {
		t.Skipf("NOT RUN: actual isolated delegated cgroup prerequisite unavailable: %v", e)
	}
	if e = probe.Close(); e != nil {
		t.Fatal("cannot retire empty cgroup probe:", e)
	}
	nativeURL := radioChainEnv(t, "NATIVE_URL")
	nativeToken := radioChainEnv(t, "NATIVE_TOKEN")
	target := radioChainEnv(t, "TARGET_ID")
	binary := radioChainPath(t, radioChainEnv(t, "SENDER_BINARY"))
	if !strings.HasPrefix(binary, fixtureRoot+string(os.PathSeparator)) {
		t.Fatal("sender test artifact must belong to private fixture root")
	}
	stage := radioChainPrivateFile(t, fixtureRoot, radioChainEnv(t, "PAIRING_STAGE"))
	secret := radioChainEnv(t, "HANDOFF_SECRET")
	admin := radioChainEnv(t, "ADMIN_PASSWORD")
	if !producerHex(secret, 64) || !producerHex(admin, len(admin)) || len(admin) < 32 || len(admin) > 256 {
		t.Fatal("invalid private radio fixture credentials")
	}
	api, ws, managerPort := radioChainPort(t, "API_PORT"), radioChainPort(t, "WS_PORT"), radioChainPort(t, "MANAGER_PORT")
	receiverPort := radioChainPort(t, "RECEIVER_PORT")
	if api == ws || api == managerPort || ws == managerPort || receiverPort == api || receiverPort == ws || receiverPort == managerPort {
		t.Fatal("chain ports must be distinct")
	}
	root := fixtureRoot
	if !factory {
		root, e = os.MkdirTemp(fixtureRoot, "manager-chain-")
		if e != nil {
			t.Fatal(e)
		}
		defer os.RemoveAll(root)
	}
	if e = os.Chmod(root, 0711); e != nil {
		t.Fatal(e)
	} // C may traverse only its private child, not list manager files.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	native, e := newRadioProducerNativeClient(nativeURL, nativeToken)
	if e != nil {
		t.Fatal(e)
	}
	evidence, e := native.Evidence(ctx)
	if e != nil || len(evidence.Missing) != 0 {
		t.Fatalf("actual Native prerequisites unavailable: %v %v", e, evidence.Missing)
	}
	native.mu.Lock()
	controller := native.state.ControllerID
	actualTargets := append([]radioProducerTarget{}, native.state.Targets...)
	physicalTarget := ""
	for _, configured := range native.state.Targets {
		if configured.ID == target {
			physicalTarget = configured.HomePodID
		}
	}
	native.mu.Unlock()
	if !producerCounter(physicalTarget, false) {
		t.Fatal("actual Native target has no exact physical receiver identity")
	}

	var pair struct {
		Schema     string            `json:"schema"`
		Controller string            `json:"controller_id"`
		Speakers   []json.RawMessage `json:"speakers"`
	}
	if json.Unmarshal(stage, &pair) != nil || pair.Schema != "podconnect.radio-pairing-stage.v2" || pair.Controller != controller || len(pair.Speakers) == 0 {
		t.Fatal("actual stage does not match actual Native/C registered controller")
	}
	var sender *radioSender
	var producer *radioProducer
	var factoryOptions radioProducerBootstrapOptions
	oldDir, oldOwner, oldStore := dataDir, radioOwner, store
	dataDir = root
	defer func() { dataDir, radioOwner, store = oldDir, oldOwner, oldStore }()
	if factory {
		// Only the explicit inert fixture owns these fresh data paths. The real
		// factory exports/imports pairing itself and creates the real child cage.
		store = newRoomStore()
		rooms := []*Room{}
		for _, actual := range actualTargets {
			rooms = append(rooms, &Room{ID: actual.ID, Name: actual.Name, HomepodID: actual.HomePodID})
		}
		// Reproduce the canonical primary path for the actual configured r0 when
		// present. Never invent a selectable room or physical identity.
		for i, room := range rooms {
			if room.ID == "r0" {
				rooms[0], rooms[i] = rooms[i], rooms[0]
				break
			}
		}
		for i, room := range rooms {
			room.Idx = i
		}
		roomBytes, _ := json.Marshal(roomsFile{NextIdx: len(rooms), Rooms: rooms})
		if e = os.WriteFile(filepath.Join(root, "rooms.json"), roomBytes, 0600); e != nil {
			t.Fatal(e)
		}
		opts := radioProducerBootstrapOptions{Enabled: true, Mode: "builtin", Physical: true, NativeURL: nativeURL, NativeToken: nativeToken, Secret: secret, AdminPassword: admin, API: api, WS: ws, Cgroup: parent}
		optsBytes, _ := json.Marshal(opts)
		if e = os.WriteFile(filepath.Join(root, "options.json"), optsBytes, 0600); e != nil {
			t.Fatal(e)
		}
		opts, e = readRadioProducerBootstrapOptions()
		if e != nil {
			t.Fatal("actual option admission failed:", e)
		}
		factoryOptions = opts
		// A well-formed but foreign-owned retained receipt must be rejected before
		// any private sender configuration, export/initializer or process launch.
		foreignDirectory := filepath.Join(root, "radio-owner-journal")
		if e = os.Mkdir(foreignDirectory, 0700); e != nil {
			t.Fatal(e)
		}
		foreignRecord := radioProducerRecord{Fence: radioFence{Token: "foreign-ledger", ActionID: "foreign-ledger", Generation: 1}, Terminal: true}
		foreignBytes, _ := json.Marshal(foreignRecord)
		foreignFile := filepath.Join(foreignDirectory, producerRecordName(foreignRecord.Fence.Token))
		if e = os.WriteFile(foreignFile, foreignBytes, 0600); e != nil {
			t.Fatal(e)
		}
		actualCredential, err := radioServiceCredential()
		if err != nil {
			t.Fatal(err)
		}
		if e = os.Chown(foreignFile, int(actualCredential.Uid), int(actualCredential.Gid)); e != nil {
			t.Fatal(e)
		}
		beforeForeign, _ := os.Stat(foreignFile)
		if _, err = prepareConfiguredRadioProducer(ctx, opts, native); err == nil {
			t.Fatal("foreign-owned ledger enabled factory")
		}
		afterForeign, _ := os.Stat(foreignFile)
		afterBytes, err := os.ReadFile(foreignFile)
		if err != nil || string(afterBytes) != string(foreignBytes) || !os.SameFile(beforeForeign, afterForeign) || beforeForeign.Mode() != afterForeign.Mode() {
			t.Fatal("foreign ledger refusal changed storage")
		}
		if _, err = os.Stat(filepath.Join(root, "radio-sender")); !os.IsNotExist(err) {
			t.Fatal("unsafe ledger reached sender configuration/launch")
		}
		if e = os.Remove(foreignFile); e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(foreignDirectory); e != nil {
			t.Fatal(e)
		}
		t.Log("VERIFIED actual foreign-owned valid journal rejected unchanged before any sender configuration/export/launch")
		producer, e = prepareConfiguredRadioProducer(ctx, opts, native)
		if e != nil {
			t.Fatal("actual factory/export/init/supervisor failed:", e)
		}
		client, ok := producer.sender.(*radioProducerSenderClient)
		if !ok || client.supervisor == nil {
			t.Fatal("factory did not create concrete sender")
		}
		sender = client.supervisor
	} else {
		sender, e = newRadioSender(root, api, ws, nil)
		if e != nil {
			t.Fatal(e)
		}
		sender.radioConfig = &radioProducerBootstrapOptions{AdminPassword: admin, Secret: secret, controller: controller, stage: filepath.Join(sender.root, "pairing-stage.json")}
		sender.newContainment = func() (radioProcessContainment, error) { return newRadioCgroup(parent, defaultRadioResourceLimits()) }
		credential, err := radioServiceCredential()
		if err != nil {
			t.Fatal(err)
		}
		sender.makeCommand = func(child context.Context, config string) *exec.Cmd {
			cmd := exec.CommandContext(child, binary, "--radio-http-owner")
			copy := *credential
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &copy}
			cmd.Env = append(radioRoleEnvironment(false), "PC_RADIO_HTTP_CONFIG="+config, "PC_RADIO_HTTP_RECEIVER_PORT="+strconv.Itoa(receiverPort), "PC_RADIO_HTTP_TARGET_ID="+physicalTarget)
			return cmd
		}
		if _, e = sender.prepare(); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(sender.radioConfig.stage, stage, 0600); e != nil {
			t.Fatal(e)
		}
		if e = sender.startIdleProcess(ctx); e != nil {
			if errors.Is(e, syscall.EPERM) || errors.Is(e, syscall.ENOSYS) || errors.Is(e, syscall.EROFS) {
				t.Skipf("NOT RUN: kernel cannot start exact delegated-cgroup sender: %v", e)
			}
			t.Fatal("actual private C owner failed to start:", e)
		}
		client, e := newRadioProducerSenderClient("http://127.0.0.1:"+strconv.Itoa(api), admin, native, sender)
		if e != nil {
			t.Fatal(e)
		}
		readyDeadline := time.Now().Add(5 * time.Second)
		for {
			_, e = client.Evidence(ctx, evidence)
			if e == nil {
				break
			}
			if time.Now().After(readyDeadline) {
				t.Fatal("actual C HTTP/identity/kernel evidence unavailable:", e)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		// true admits ONLY this explicitly isolated inert composition. Production
		// physical-false gate tests remain separate; no household acceptance is asserted.
		producer, e = newRadioProducer(native, client, filepath.Join(root, "owner-journal"), true)
		if e != nil {
			t.Fatal(e)
		}
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if e := sender.stop(cleanup); e != nil {
			t.Errorf("radio child cleanup failed: %v", e)
		}
	}()
	if factory {
		credential, err := radioServiceCredential()
		if err != nil {
			t.Fatal(err)
		}
		probe := exec.Command("/usr/bin/test", "-r", filepath.Join(root, "radio-owner-journal"))
		probe.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		if probe.Run() == nil {
			t.Fatal("radio uid can read manager custody journal")
		}
		tokenProbe := exec.Command("/usr/bin/test", "-r", filepath.Join(root, "options.json"))
		tokenProbe.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		if tokenProbe.Run() == nil {
			t.Fatal("radio uid can read Native control token")
		}

		client := producer.sender.(*radioProducerSenderClient)
		if e = sender.productionEvidence(sender.cmd.Process.Pid); e != nil {
			t.Fatal("actual permanent uid/caps/kernel proof failed:", e)
		}
		if client.supervisor != sender {
			t.Fatal("factory changed supervised source")
		}
		budgetPath := radioChainBudgetPath(sender)
		before, _ := os.ReadFile(budgetPath)
		writeProbe := exec.Command("/bin/sh", "-c", `printf max > "$1"`, "radio-budget-probe", budgetPath)
		writeProbe.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		if writeProbe.Run() == nil {
			t.Fatal("radio uid can change kernel budget")
		}
		after, _ := os.ReadFile(budgetPath)
		if string(before) != string(after) {
			t.Fatal("radio uid changed actual memory limit")
		}
		t.Log("VERIFIED actual permanent radio service uid, zero capabilities, private token/ledger read denied and cgroup budget write denied")
	}
	coordinator := newRadioCoordinator(producer)
	if e = coordinator.enable(ctx); e != nil {
		t.Fatal("real producer did not establish readiness:", e)
	}
	radioOwner = coordinator
	attention := "inert-" + attentionUUID()
	options, _ := json.Marshal(map[string]any{"attention_token": attention, "radio_owner_mode": "builtin", "radio_native_token": nativeToken})
	if e = os.WriteFile(filepath.Join(root, "options.json"), options, 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(managerPort))
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/radio/state", radioStateHandler)
	mux.HandleFunc("/api/radio/action", radioActionHandler)
	mux.HandleFunc("/api/radio/result", radioResultHandler)
	mux.HandleFunc("/api/radio/native-preempt", radioNativePreemptHandler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	managerRPC, e := newRadioProducerHTTP("http://127.0.0.1:"+strconv.Itoa(managerPort), attention)
	if e != nil {
		t.Fatal(e)
	}
	managerRPC.client.Timeout = 15 * time.Second
	call := func(path string, body any, out any) {
		t.Helper()
		if e := managerRPC.call(ctx, path, body, out); e != nil {
			t.Fatalf("actual manager %s unresolved (%v); no command is retried", path, e)
		}
	}
	read := func() radioState {
		t.Helper()
		var state radioState
		call("/api/radio/state", nil, &state)
		return state
	}
	action := func(state radioState, kind, station, destination string, volume ...int) string {
		t.Helper()
		id := attentionUUID()
		q := radioCommand{ActionID: id, Action: kind, StationID: station, TargetID: destination, ExpectedRevision: state.Revision, ExpectedProcessID: state.ProcessID, ExpectedOwnerGeneration: state.OwnerGeneration, ExpectedGeneration: state.Generation}
		if len(volume) == 1 {
			q.VolumePercent = &volume[0]
		}
		var reply struct {
			Schema string `json:"schema"`
			Action string `json:"action_id"`
			Status string `json:"status"`
			Retry  bool   `json:"retry_allowed"`
		}
		if err := managerRPC.call(ctx, "/api/radio/action", q, &reply); err != nil {
			snapshot := radioOwner.snapshot()
			until := int64(0)
			if snapshot.ValidUntil != nil {
				until = *snapshot.ValidUntil
			}
			t.Fatalf("actual action %s rejected/unresolved before retry: expected revision=%d generation=%d owner=%d; current revision=%d generation=%d owner=%d state=%s actions=%v valid_until=%v now=%d; %v", kind, q.ExpectedRevision, q.ExpectedGeneration, q.ExpectedOwnerGeneration, snapshot.Revision, snapshot.Generation, snapshot.OwnerGeneration, snapshot.State, snapshot.Actions, until, time.Now().Unix(), err)
		}
		if reply.Schema != "podconnect.radio-action.v1" || reply.Action != id || reply.Status != "accepted" || reply.Retry {
			t.Fatal("actual manager did not complete exact once-only command")
		}
		return id
	}
	initial := read()
	if !initial.Enabled || initial.SourceKind != "none" || initial.State != "idle" {
		t.Fatal("actual independent sender has not proven clean idle")
	}
	radioAssertActualEvolutionBoot(t, initial)
	startAction := action(initial, "start", "p3", target)
	playing := read()
	if playing.State != "playing" || playing.SourceKind != "radio" || playing.TargetID != target || playing.StationID != "p3" || playing.OwnerGeneration == 0 || playing.PhysicalVerified {
		t.Fatal("actual correlated decoder/RTP source was not observed")
	}
	// Evidence file is orchestration output only, never an admission input.
	observed, _ := json.Marshal(playing)
	if e = os.WriteFile(filepath.Join(root, "observed-playing.json"), observed, 0600); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("PC_RADIO_CHAIN_CONTROLS") == "1" {
		if playing.VolumePercent == nil {
			t.Fatal("actual startup ACK failed to expose source-owned volume")
		}
		_ = action(playing, "volume", "", "", 41)
		playing = read()
		if playing.VolumePercent == nil || *playing.VolumePercent != 41 {
			t.Fatal("manual radio volume was not actually ACKed")
		}
		firstGeneration := playing.OwnerGeneration
		_ = action(playing, "start", "p1", target)
		playing = read()
		if playing.StationID != "p1" || playing.OwnerGeneration <= firstGeneration || playing.VolumePercent == nil || *playing.VolumePercent != 41 {
			t.Fatal("station change lost actual source/volume continuity")
		}
		_ = action(playing, "start", "p3", target)
		playing = read()
		if playing.StationID != "p3" || playing.VolumePercent == nil || *playing.VolumePercent != 41 {
			t.Fatal("return station lost observed volume")
		}
		t.Log("VERIFIED actual manager offers volume after startup ACK, manual41 and P3/P1/P3 retain source-owned41")
	}
	playing = radioExerciseActualDiscovery(t, ctx, producer, read)
	var stopped radioState
	if os.Getenv("PC_RADIO_CHAIN_EVOLUTION") == "1" {
		stopped = radioExerciseActualEvolution(t, ctx, producer, read, target, nativeURL, os.Getenv("PC_RADIO_CHAIN_PEER_METRICS"))
	} else if os.Getenv("PC_RADIO_CHAIN_SELECTED") == "1" {
		stopped = radioExerciseActualSelected(t, ctx, producer, read, target, nativeURL, os.Getenv("PC_RADIO_CHAIN_PEER_METRICS"))
	} else {
		playing = radioExerciseActualMove(t, ctx, producer, read, action, target, nativeURL, os.Getenv("PC_RADIO_CHAIN_PEER_METRICS"))
		_ = action(playing, "stop", "", "")
		stopped = read()
		if stopped.State != "stopped" || stopped.SourceKind != "none" || stopped.OwnerGeneration != playing.OwnerGeneration || stopped.PhysicalVerified {
			t.Fatal("actual exact source retirement was not observed")
		}
		if os.Getenv("PC_RADIO_CHAIN_MOVE") != "coexist" {
			producer.mu.Lock()
			var original radioProducerRecord
			for _, record := range producer.records {
				if record.Fence.ActionID == startAction {
					original = record
				}
			}
			producer.mu.Unlock()
			retained, e := native.Result(ctx, original.Request, original.Native)
			if e != nil || !retained.Terminal || retained.Outcome != "committed" || retained.Hold == nil || retained.Hold.Token != original.Native.Token {
				t.Fatal("radio stop failed to preserve original paused Native custody")
			}
			if os.Getenv("PC_RADIO_CHAIN_RESTART") == "1" {
				restored, err := newRadioProducer(native, producer.sender, producer.journal.directory, true)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = restored.Ready(ctx); err == nil || restored.active != "" || len(restored.retained) != 0 {
					t.Fatal("STOP-only restart adopted retained Spotify/radio custody")
				}
				if factory {
					oldPID := sender.cmd.Process.Pid
					if _, err = prepareConfiguredRadioProducer(ctx, factoryOptions, native); err == nil || sender.cmd.Process.Pid != oldPID {
						t.Fatal("STOP-only factory replaced retained custody")
					}
				}
				t.Log("VERIFIED actual STOP-only restored manager stays blocked with no launch or source adoption")
				restartedID := action(stopped, "start", "p1", target)
				restarted := read()
				if restarted.State != "playing" || restarted.StationID != "p1" || restarted.TargetID != target || restarted.OwnerGeneration <= playing.OwnerGeneration {
					t.Fatal("stopped radio failed to start a new actual source generation")
				}
				producer.mu.Lock()
				var restartedRecord radioProducerRecord
				for _, record := range producer.records {
					if record.Fence.ActionID == restartedID {
						restartedRecord = record
					}
				}
				producer.mu.Unlock()
				if restartedRecord.Native.Handoff != original.Native.Handoff || restartedRecord.Native.Token != original.Native.Token {
					t.Fatal("Stop then Start replaced original suspended Spotify hold")
				}
				_ = action(restarted, "stop", "", "")
				stopped = read()
				if stopped.State != "stopped" || stopped.SourceKind != "none" || stopped.OwnerGeneration != restarted.OwnerGeneration {
					t.Fatal("restarted radio did not drain")
				}
				t.Log("VERIFIED actual Stop/Start reuses exact immutable Native hold without Spotify resume or second pause")
			}
			if os.Getenv("PC_RADIO_CHAIN_RESUME") == "1" {
				type peerMetric struct {
					Schema   string `json:"schema"`
					Audio    uint64 `json:"audio_packets"`
					Packets  uint64 `json:"packets"`
					Verifies uint64 `json:"verifies"`
					Selected bool   `json:"selected"`
					Session  bool   `json:"session_present"`
					Item     uint64 `json:"source_item"`
					Player   int    `json:"player_state"`
				}
				metricsFile := radioChainEnv(t, "PEER_METRICS")
				readPeer := func() peerMetric {
					var value peerMetric
					raw := radioChainPrivateFile(t, fixtureRoot, metricsFile)
					if json.Unmarshal(raw, &value) != nil || value.Schema != "podconnect.inert_receiver_metrics.v1" {
						t.Fatal("actual inert receiver metric malformed")
					}
					return value
				}
				resumeOnce := func() {
					// The actual radio source has drained. Allow its loopback receive queue to
					// settle, then baseline the independent receiver before explicit Resume.
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(250 * time.Millisecond):
					}
					before := readPeer()
					if before.Selected || before.Session || before.Item == 0 {
						t.Fatal("old source was not genuinely parked before Resume")
					}
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, nativeURL+"/player/resume", nil)
					if err != nil {
						t.Fatal(err)
					}
					response, err := (&http.Client{Timeout: 12 * time.Second}).Do(request)
					if err != nil {
						t.Fatal("actual explicit Native Resume failed:", err)
					}
					_ = response.Body.Close()
					if response.StatusCode != http.StatusOK {
						t.Fatal("actual explicit Native Resume status", response.StatusCode)
					}
					producer.mu.Lock()
					_, held := producer.retained[target]
					producer.mu.Unlock()
					if held {
						t.Fatal("confirmed Native return retained stale manager target hold")
					}
					_ = read()
					until := time.Now().Add(5 * time.Second)
					for {
						after := readPeer()
						if after.Audio > before.Audio+2 && after.Selected && after.Session && after.Item == before.Item {
							break
						}
						if time.Now().After(until) {
							t.Fatalf("actual retained source did not emit new RTP after explicit Resume: before=%+v after=%+v", before, after)
						}
						select {
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(50 * time.Millisecond):
						}
					}
					if _, _, err := producer.Ready(ctx); err != nil {
						t.Fatal("completed return fresh readiness:", err)
					}
					t.Log("VERIFIED actual explicit Native Resume/authenticated producer callback and fresh retained-source RTP after radio drain")
				}
				resumeOnce()
				if os.Getenv("PC_RADIO_CHAIN_CYCLES") == "1" {
					fresh := read()
					if fresh.State == "unknown" {
						t.Fatal("confirmed Spotify return left presentation unresolved")
					}
					freshAction := action(fresh, "start", "p3", target)
					playing = read()
					if playing.State != "playing" || playing.SourceKind != "radio" || playing.TargetID != target {
						t.Fatal("second actual radio cycle did not play")
					}
					producer.mu.Lock()
					var next radioProducerRecord
					for _, record := range producer.records {
						if record.Fence.ActionID == freshAction {
							next = record
						}
					}
					producer.mu.Unlock()
					if next.Native.Handoff == "" || next.Native.Handoff == original.Native.Handoff || next.Native.Token == original.Native.Token {
						t.Fatal("fresh radio cycle borrowed released Native custody")
					}
					_ = action(playing, "stop", "", "")
					stopped = read()
					resumeOnce()

					if os.Getenv("PC_RADIO_CHAIN_FACTORY_RESTART") == "1" {
						oldSender := sender
						if err := oldSender.stop(ctx); err != nil {
							t.Fatal("clean sender shutdown:", err)
						}
						next, err := prepareConfiguredRadioProducer(ctx, factoryOptions, native)
						if err != nil {
							t.Fatal("clean completed return did not permit fresh factory:", err)
						}
						producer = next
						sender = next.sender.(*radioProducerSenderClient).supervisor
						if sender == oldSender {
							t.Fatal("factory restart reused old supervisor")
						}
						coordinator = newRadioCoordinator(producer)
						if err := coordinator.enable(ctx); err != nil {
							t.Fatal("fresh factory enable:", err)
						}
						radioOwner = coordinator
						fresh := read()
						if fresh.SourceKind != "none" || fresh.State != "idle" {
							t.Fatal("fresh factory adopted history:", fresh)
						}
						_ = action(fresh, "start", "p1", target)
						playing = read()
						if playing.SourceKind != "radio" || playing.State != "playing" {
							t.Fatal("fresh factory new radio start failed")
						}
						_ = action(playing, "stop", "", "")
						stopped = read()
						resumeOnce()
						t.Log("VERIFIED actual clean completed return → new configured factory/same private DB+manager journal → fresh idle/new source/no adoption")
					}
					t.Log("VERIFIED two actual Radio/Spotify cycles with fresh Native hold, original retained source/session/queue, no replay")
				}

			}
		}
	}
	observed, _ = json.Marshal(stopped)
	if e = os.WriteFile(filepath.Join(root, "observed-stopped.json"), observed, 0600); e != nil {
		t.Fatal(e)
	}
	resultFile := filepath.Join(fixtureRoot, "manager-chain-result.json")
	resultBytes, _ := json.Marshal(struct{ Playing, Stopped radioState }{playing, stopped})
	resultHandle, e := os.OpenFile(resultFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("cannot record fresh inert chain result:", e)
	}
	if _, e = resultHandle.Write(resultBytes); e != nil {
		_ = resultHandle.Close()
		t.Fatal(e)
	}
	if e = resultHandle.Close(); e != nil {
		t.Fatal(e)
	}
	if factory {
		t.Log("VERIFIED actual configured factory/export/initialize/uid/HTTP Native/manager/two-C producer with real cgroup/PID/guardian and decoder/PairVerify/RTP; NOT household or HA-Supervisor deployment acceptance")
	} else {
		t.Log("VERIFIED actual HTTP Native/manager/two-C direct ports; NOT factory or physical acceptance")
	}
}
