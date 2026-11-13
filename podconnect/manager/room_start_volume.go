package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A room's start level is the volume its first sound plays at. It is the shared
// never-loud level (startVolumePercent) unless the room was given a lower one:
// the last explicit volume set for the room through /api/room-control, also
// while the room is quiet. A remembered level above the shared one never raises
// a start (never loud at start); one below it is honoured, so "set 10 %, then
// play" starts at 10 %.

func roomStartVolumePath(roomID string) string {
	return filepath.Join(dataDir, "rooms", roomID, "start_volume")
}

// rememberRoomVolume stores the room's last explicit volume (0-100).
func rememberRoomVolume(roomID string, pct int) error {
	path := roomStartVolumePath(roomID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(clampPct(pct))), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// startLevelForRoom is the level a new start in the room plays at.
func startLevelForRoom(roomID string) int {
	raw, err := os.ReadFile(roomStartVolumePath(roomID))
	if err != nil {
		return startVolumePercent
	}
	pct, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pct < 0 || pct > 100 {
		return startVolumePercent
	}
	return min(pct, startVolumePercent)
}

// startLevelForOutput is the start level of the room bound to this HomePod output.
func startLevelForOutput(outputID string) int {
	if outputID == "" {
		return startVolumePercent
	}
	for _, r := range loadRooms() {
		if r.HomepodID == outputID {
			return startLevelForRoom(r.ID)
		}
	}
	return startVolumePercent
}

// startLevelAccepted reports whether an observed output level is the start level
// (at most the level, within the bridge's rounding tolerance below it).
func startLevelAccepted(volume, level int) bool {
	return volume <= level && volume >= level-volTol
}

// setQuietRoomVolume puts the room's start level on its HomePod output now, so
// the slider shows it and nothing louder is waiting there. The bridge and every
// start path seat the same level again before the first sound.
func setQuietRoomVolume(primary, room *Room) error {
	if room.HomepodID == "" {
		return fmt.Errorf("room has no HomePod output")
	}
	query := url.Values{"volume": {strconv.Itoa(startLevelForRoom(room.ID))}, "output_id": {room.HomepodID}}
	req, err := http.NewRequest(http.MethodPut, primary.OwnTone+"/api/player/volume?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("owntone volume returned HTTP %d", resp.StatusCode)
	}
	return nil
}
