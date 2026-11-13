package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// A room's start level has one source: its HomePod output's own volume in
// OwnTone, never above the shared start ceiling (startVolumePercent, 35 %).
// A room set to 8 % starts at 8 %; one left at 60 % starts at 35 %. OwnTone's
// start proof (policy v2) reports the same level to the Native engine, so the
// queue, Find musik, play_media, a Spotify app transfer, radio and
// connect-recovery all start at it.

// startLevelFor caps an output volume at the start ceiling.
func startLevelFor(volume int) int {
	return min(clampPct(volume), startVolumePercent)
}

// startLevelForOutput is the start level of this HomePod output. When OwnTone
// cannot be read the ceiling applies, never anything louder.
func startLevelForOutput(outputID string) int {
	primary := primaryRoom()
	if outputID == "" || primary == nil {
		return startVolumePercent
	}
	devs, _ := fetchOutputsFrom(primary.OwnTone)
	for _, d := range devs {
		if d.ID == outputID && d.hasVolume {
			return startLevelFor(d.volume)
		}
	}
	return startVolumePercent
}

// startLevelForRoom is the start level of the room's HomePod output.
func startLevelForRoom(roomID string) int {
	room := roomByID(roomID)
	if room == nil {
		return startVolumePercent
	}
	return startLevelForOutput(room.HomepodID)
}

// startLevelAccepted reports whether an observed output level is the start level
// (at most the level, within the bridge's rounding tolerance below it).
func startLevelAccepted(volume, level int) bool {
	return volume <= level && volume >= level-volTol
}

// setQuietRoomVolume puts the chosen volume on the quiet room's HomePod output.
// That output volume is the room's start level (capped at 35 % by every start).
func setQuietRoomVolume(primary, room *Room, pct int) error {
	if room.HomepodID == "" {
		return fmt.Errorf("room has no HomePod output")
	}
	query := url.Values{"volume": {strconv.Itoa(clampPct(pct))}, "output_id": {room.HomepodID}}
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
