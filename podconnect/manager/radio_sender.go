package main

// Dedicated radio sender lifecycle. It never opens Spotify's database/config,
// invokes a Spotify process, or restarts after exit. Native receiver custody must
// be established separately before any output is selected or stream is started.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type radioSender struct {
	mu               sync.Mutex
	root             string
	radioConfig      *radioProducerBootstrapOptions
	api, ws          int
	cmd              *exec.Cmd
	done             chan struct{}
	exit             error
	containment      radioProcessContainment
	newContainment   func() (radioProcessContainment, error)
	makeCommand      func(context.Context, string) *exec.Cmd
	prepareOwnership func(string) error
	serviceUser      string
}

func newRadioSender(dataRoot string, api, ws int, rooms []*Room) (*radioSender, error) {
	if !filepath.IsAbs(dataRoot) || strings.ContainsAny(dataRoot, "\"\n\r") || api < 1024 || api > 65535 || ws < 1024 || ws > 65535 || api == ws {
		return nil, errors.New("invalid independent radio sender configuration")
	}
	for _, room := range rooms {
		if room != nil && (api == room.GLPort || api == room.OTPort || api == room.OTWSPort || ws == room.GLPort || ws == room.OTPort || ws == room.OTWSPort) {
			return nil, errors.New("radio sender port collides with Spotify")
		}
	}
	canonical, e := filepath.EvalSymlinks(dataRoot)
	if e != nil || strings.ContainsAny(canonical, "\"\n\r") {
		return nil, errRadioUnavailable
	}
	root := filepath.Join(canonical, "radio-sender")
	return &radioSender{root: root, api: api, ws: ws, newContainment: func() (radioProcessContainment, error) { return nil, errRadioUnavailable }, prepareOwnership: func(path string) error { return exec.Command("chown", "-R", "owntone:owntone", path).Run() }, makeCommand: func(ctx context.Context, config string) *exec.Cmd {
		return exec.CommandContext(ctx, "owntone", "-f", "-c", config)
	}}, nil
}
func (s *radioSender) prepare() (string, error) {
	// Refuse symlink components: a radio path must never alias Spotify state.
	for p := s.root; p != filepath.Dir(p); p = filepath.Dir(p) {
		if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("radio sender path contains symlink")
		}
	}
	if e := radioSenderTreeSafe(s.root); e != nil {
		return "", e
	}
	if e := os.MkdirAll(filepath.Join(s.root, "media"), 0700); e != nil {
		return "", e
	}
	serviceUser := s.serviceUser
	if serviceUser == "" {
		serviceUser = "owntone"
	}
	config := fmt.Sprintf("general {\n%s uid = \"%s\"\n db_path = \"%s/radio-songs.db\"\n cache_dir = \"%s\"\n logfile = \"%s/radio.log\"\n websocket_port = %d\n}\nlibrary {\n port = %d\n name = \"PodView DR Radio\"\n directories = { \"%s/media\" }\n pipe_autostart = false\n}\naudio { type = \"disabled\" }\nmpd { port = 0 }\n", s.radioConfig.generalConfig(), serviceUser, s.root, s.root, s.root, s.ws, s.api, s.root)
	path := filepath.Join(s.root, "owntone.conf")
	// O_EXCL creation never follows an existing config symlink or truncates
	// any existing inode. Existing config is immutable: require the exact bytes.
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		if !errors.Is(e, os.ErrExist) {
			return "", e
		}
		if e = radioSenderTreeSafe(s.root); e != nil {
			return "", e
		}
		old, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(old, []byte(config)) {
			return "", errors.New("existing radio sender config differs")
		}
	} else {
		_, writeErr := file.Write([]byte(config))
		closeErr := file.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if e = radioSenderTreeSafe(s.root); e != nil {
		return "", e
	}

	return path, nil
}
func (s *radioSender) startIdle(ctx context.Context, proofs []string) error {
	// Even idle process startup is unreachable until every native custody proof is
	// present; no hidden fallback launch occurs in configuredRadioOwner.
	for _, need := range radioRequiredProofs {
		found := false
		for _, p := range proofs {
			found = found || p == need
		}
		if !found {
			return errRadioUnavailable
		}
	}
	return s.startIdleProcess(ctx)
}

// Startup-only lifecycle primitive. It grants no receiver/audio authority;
// the builtin factory validates real prerequisites before this idle launch.
func (s *radioSender) startIdleProcess(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return errors.New("radio sender already supervised")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.newContainment == nil {
		return errRadioUnavailable
	}
	cage, e := s.newContainment()
	if e != nil {
		return e
	}
	launched := false
	defer func() {
		if !launched {
			_ = cage.Close()
		}
	}()
	config, e := s.prepare()
	if e != nil {
		return e
	}
	if e = s.prepareOwnership(s.root); e != nil {
		return e
	}
	if e = radioSenderTreeSafe(s.root); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cmd := s.makeCommand(ctx, config)
	if cmd == nil {
		cage.Close()
		return errRadioUnavailable
	}
	if e = cage.Configure(cmd); e != nil {
		cage.Close()
		return e
	}
	// Override CommandContext's process-only cancellation: all descendants belong
	// to the same isolated kernel custody, including after the leader exits.
	cmd.Cancel = func() error { return cage.Kill() }
	cmd.WaitDelay = 2 * time.Second
	var custodyEnded <-chan struct{}
	if health, ok := cage.(interface{ Failure() <-chan struct{} }); ok {
		custodyEnded = health.Failure()
		select {
		case <-custodyEnded:
			return errRadioUnavailable
		default:
		}
	}
	if e = cmd.Start(); e != nil {
		cage.Close()
		return e
	}
	if e = radioContainmentAfterStart(cmd, cage); e != nil {
		_ = cage.Kill()
		_ = cmd.Wait()
		_ = cage.Close()
		return e
	}
	launched = true
	s.cmd = cmd
	s.containment = cage
	s.done = make(chan struct{})
	s.exit = nil
	done := s.done
	if custodyEnded != nil {
		go func() {
			select {
			case <-custodyEnded:
				_ = cage.Kill()
			case <-done:
			}
		}()
	}
	go func() {
		waitErr := cmd.Wait()
		killErr := cage.Kill()
		closeErr := cage.Close()
		s.mu.Lock()
		s.exit = errors.Join(waitErr, killErr, closeErr)
		s.mu.Unlock()
		close(done)
	}()
	return nil
}
func (s *radioSender) stop(ctx context.Context) error {
	s.mu.Lock()
	cmd, done, cage := s.cmd, s.done, s.containment
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	// Do not allow caller cancellation to suppress cleanup of our own subtree.
	if e := cage.Kill(); e != nil {
		return e
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == cmd {
		// A failed containment cleanup cannot be converted into a fresh launch.
		if e := cage.Close(); e != nil {
			return e
		}
		s.cmd = nil
		s.containment = nil
	}
	return nil
}

func radioSenderTreeSafe(root string) error {
	if _, e := os.Lstat(root); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("radio sender descendant contains symlink")
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return errors.New("radio sender descendant is not a regular file")
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Nlink != 1 {
				return errors.New("radio sender file has aliased inode")
			}
		}
		return nil
	})
}
