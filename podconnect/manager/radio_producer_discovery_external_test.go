package main

// Actual optional discovery-edge fixture. The markers cause the existing C
// fixture to publish its registered receiver, never a manager availability DTO.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func radioExerciseActualDiscovery(t *testing.T, ctx context.Context, p *radioProducer, read func() radioState) radioState {
	t.Helper()
	state := read()
	if os.Getenv("PC_RADIO_CHAIN_DISCOVERY") != "1" {
		return state
	}
	root := os.Getenv("PC_RADIO_CHAIN_FIXTURE_ROOT")
	metricsPath := os.Getenv("PC_RADIO_CHAIN_PEER_METRICS")
	type target struct {
		Device     string `json:"device_id"`
		Audio      uint64 `json:"audio_packets"`
		Discovered bool   `json:"discovered"`
	}
	metric := func() map[string]target {
		var raw struct {
			Schema  string   `json:"schema"`
			Targets []target `json:"targets"`
		}
		if err := json.Unmarshal(radioChainPrivateFile(t, root, metricsPath), &raw); err != nil || raw.Schema != "podconnect.inert_receiver_metrics.v1" || len(raw.Targets) != 2 {
			t.Fatal("real delayed discovery metrics invalid", err)
		}
		rows := map[string]target{}
		for _, row := range raw.Targets {
			rows[row.Device] = row
		}
		return rows
	}
	before := metric()
	if !before["1"].Discovered || before["2"].Discovered || before["2"].Audio != 0 {
		t.Fatal("second receiver was not genuinely undiscovered", before)
	}
	unavailable := false
	for _, row := range state.Targets {
		if row.ID == "r0" {
			unavailable = !row.Selectable && row.Availability == "unavailable"
		}
	}
	if !unavailable {
		t.Fatal("undiscovered configured B was offered", state.Targets)
	}
	sender := p.sender.(*radioProducerSenderClient).supervisor
	sender.mu.Lock()
	pid := sender.cmd.Process.Pid
	sender.mu.Unlock()
	credential, err := radioServiceCredential()
	if err != nil {
		t.Fatal(err)
	}
	oldPID, err := strconv.Atoi(os.Getenv("PC_RADIO_CHAIN_OLD_PID"))
	if err != nil || oldPID <= 1 {
		t.Fatal("old fixture pid missing")
	}
	status, err := os.ReadFile("/proc/" + strconv.Itoa(oldPID) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	oldUID := uint64(0)
	uidSeen := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) != 5 {
				t.Fatal("kernel uid malformed")
			}
			oldUID, err = strconv.ParseUint(fields[2], 10, 32)
			uidSeen = err == nil
		}
	}
	if !uidSeen {
		t.Fatal("old fixture effective uid unavailable")
	}
	for _, entry := range []struct {
		key string
		uid uint32
	}{{"OLD", uint32(oldUID)}, {"SENDER", credential.Uid}} {
		path := os.Getenv("PC_RADIO_CHAIN_SECOND_DISCOVERY_" + entry.key)
		if filepath.Dir(path) != root || path == "" {
			t.Fatal("discovery marker escaped fresh fixture")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("discovery marker already exists", err)
		}
		file, err := os.CreateTemp(root, ".discovery-marker-")
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Chown(int(entry.uid), -1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		temporary := file.Name()
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(temporary, path); err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().Add(8 * time.Second)
	for {
		current := read()
		rows := metric()
		available := false
		for _, row := range current.Targets {
			if row.ID == "r0" {
				available = row.Selectable && row.Availability == "available"
			}
		}
		sender.mu.Lock()
		samePID := sender.cmd != nil && sender.cmd.Process.Pid == pid
		sender.mu.Unlock()
		if !samePID || current.TargetID != state.TargetID || current.OwnerGeneration != state.OwnerGeneration || current.StationID != state.StationID || current.SourceKind != "radio" || current.State != "playing" || rows["2"].Audio != 0 {
			t.Fatal("discovery changed source/relaunched/autoplayed", current, rows)
		}
		if available && rows["2"].Discovered && rows["1"].Audio > before["1"].Audio+2 {
			t.Log("VERIFIED configured paired B discovery-unavailable → actual device discovery/fresh Native+C availability while radio A continues; same sender PID/source, zero B audio, no launch/replay")
			return current
		}
		if time.Now().After(until) {
			t.Fatal("real discovery did not become available", current.Targets, rows)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
