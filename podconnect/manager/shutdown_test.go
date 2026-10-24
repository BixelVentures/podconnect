package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Real child processes, signals and the existing keepAlive/Wait owner. The
// Native child holds Close until released; C must remain alive throughout.
func TestOrderlyShutdownChild(t *testing.T) {
	role := os.Getenv("PODCONNECT_SHUTDOWN_CHILD")
	if role == "" {
		return
	}
	events := os.NewFile(3, "events")
	release := os.NewFile(4, "release")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	_, _ = events.Write([]byte(role))
	<-signals
	if role == "N" {
		_, _ = events.Write([]byte("P"))
		var b [1]byte
		_, _ = release.Read(b[:])
		_, _ = events.Write([]byte("J"))
	} else {
		_, _ = events.Write([]byte("C"))
		var b [1]byte
		_, _ = release.Read(b[:])
	}
	os.Exit(0)
}

func TestOrderlyRoomStopKeepsCAliveUntilNativeJoin(t *testing.T) {
	for _, scenario := range []string{"stop", "forced", "pair"} {
		t.Run(scenario, func(t *testing.T) {
			eventsR, eventsW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			releaseR, releaseW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer eventsR.Close()
			defer eventsW.Close()
			defer releaseR.Close()
			defer releaseW.Close()
			rt := &roomRuntime{room: &Room{ID: "shutdown-fixture"}, stop: make(chan struct{})}
			child := func(role string) *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=^TestOrderlyShutdownChild$")
				cmd.Env = append(os.Environ(), "PODCONNECT_SHUTDOWN_CHILD="+role)
				cmd.ExtraFiles = []*os.File{eventsW, releaseR}
				return cmd
			}
			native, renderer := child("N"), child("O")
			attempted := make(chan *exec.Cmd, 1)
			firstNative := true
			nativeBuild := func() *exec.Cmd {
				if firstNative {
					firstNative = false
					return native
				}
				next := child("N")
				attempted <- next
				return next
			}
			var workers sync.WaitGroup
			workers.Add(2)
			go func() { defer workers.Done(); rt.keepAlive("native", &rt.glCmd, nativeBuild) }()
			go func() { defer workers.Done(); rt.keepAlive("C", &rt.otCmd, func() *exec.Cmd { return renderer }) }()
			defer func() { rt.requestStop(context.Background()); killGroup(native); killGroup(renderer); workers.Wait() }()
			seen := make(chan byte, 8)
			go func() {
				var b [1]byte
				for {
					if _, err := io.ReadFull(eventsR, b[:]); err != nil {
						return
					}
					seen <- b[0]
				}
			}()
			next := func() byte {
				t.Helper()
				select {
				case b := <-seen:
					return b
				case <-time.After(5 * time.Second):
					t.Fatal("child signal/join event missing")
					return 0
				}
			}
			a, b := next(), next()
			if !((a == 'N' && b == 'O') || (a == 'O' && b == 'N')) {
				t.Fatalf("readiness %c %c", a, b)
			}
			for {
				rt.mu.Lock()
				published := rt.glCmd == native && rt.otCmd == renderer
				rt.mu.Unlock()
				if published {
					break
				}
				runtime.Gosched()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopped := make(chan struct{})
			if scenario == "pair" {
				go func() {
					if !rt.restartOT() {
						t.Error("pair replacement did not join")
					}
					close(stopped)
				}()
			} else {
				rt.requestStop(ctx)
				go func() { rt.killAll(); close(stopped) }()
			}
			if got := next(); got != 'P' {
				t.Fatalf("first shutdown event=%c; Native must receive TERM while C stays alive", got)
			}
			if err := renderer.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatalf("C died before Native close joined: %v", err)
			}
			select {
			case <-stopped:
				t.Fatal("shutdown returned before Native close joined")
			default:
			}
			if scenario == "forced" {
				cancel()
			} else {
				_, _ = releaseW.Write([]byte("x"))
				if got := next(); got != 'J' {
					t.Fatalf("Native close completion=%c", got)
				}
			}
			if scenario != "forced" {
				if got := next(); got != 'C' {
					t.Fatalf("C stopped before Native join: %c", got)
				}
				if scenario == "pair" {
					// Wait for the EXISTING two-second respawn loop to reach its next
					// admission, while the original C process still holds its join.
					select {
					case successor := <-attempted:
						rt.mu.Lock()
						started := successor.Process != nil
						held := rt.replacing != nil
						rt.mu.Unlock()
						if started || !held {
							t.Fatal("Native respawn crossed pending C join")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("existing respawn did not reach replacement fence")
					}
				}
				select {
				case <-stopped:
					t.Fatal("shutdown returned before C join")
				default:
				}
				if scenario == "pair" {
					rt.requestStop(context.Background())
				}
				_, _ = releaseW.Write([]byte("x"))
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("ordered shutdown did not join")
			}
			rt.requestStop(context.Background())
			workers.Wait()
		})
	}
}
