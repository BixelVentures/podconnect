package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in: drives the actual patched OwnTone binary configured as the radio
// player (PODCONNECT_RADIO_OWNTONE_URL) with a local stream
// (PODCONNECT_RADIO_STREAM_URL) on an output named PODCONNECT_RADIO_OUTPUT.
func TestRadioPlainStreamOnActualOwnTone(t *testing.T) {
	base, stream, output := os.Getenv("PODCONNECT_RADIO_OWNTONE_URL"), os.Getenv("PODCONNECT_RADIO_STREAM_URL"), os.Getenv("PODCONNECT_RADIO_OUTPUT")
	if base == "" || stream == "" || output == "" {
		t.Skip("explicit actual OwnTone radio fixture only")
	}
	old := radioStreamURLs["p3"]
	radioStreamURLs["p3"] = stream
	t.Cleanup(func() { radioStreamURLs["p3"] = old })
	p := newRadioPlayer(radioPlayerConfig{Enabled: true})
	p.base = base
	p.rooms = func() []*Room { return []*Room{{ID: "r7", Idx: 7, Name: "Fridas", HomepodName: output}} }
	p.speakerVolume = func(*Room) int { return -1 }
	spotify := &fakeSpotify{route: "r0"}
	p.spotify = spotify
	ctx := context.Background()
	ten := 10
	if _, err := p.execute(ctx, radioCommand{ActionID: "real-start", Action: "start", StationID: "p3", TargetID: "r7", VolumePercent: &ten}); err != nil {
		t.Fatal("actual OwnTone did not play the plain stream:", err)
	}
	time.Sleep(2 * time.Second)
	status, err := p.player(ctx)
	outputs, _ := p.playerOutputs(ctx)
	if err != nil || status.State != "play" || len(outputs) != 1 || !outputs[0].Selected || outputs[0].Volume != 10 {
		t.Fatalf("actual player %+v %+v %v", status, outputs, err)
	}
	if !p.yield(ctx, "r7") {
		t.Fatal("Spotify takeover did not stop radio")
	}
	status, _ = p.player(ctx)
	outputs, _ = p.playerOutputs(ctx)
	if status.State == "play" || outputs[0].Selected {
		t.Fatalf("radio still holds the output: %+v %+v", status, outputs)
	}
	t.Log("PASS actual OwnTone plain radio stream start, volume and Spotify takeover release")
}
