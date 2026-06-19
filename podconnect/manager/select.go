// Per-room HomePod selection — the manager's port of the old select-homepod bash service. For a
// room, find the AirPlay output whose name matches room.HomepodName (case-insensitive; type prefix
// "airplay" since HomePods report "AirPlay 2"), honor the released flag, warn on needs-auth, and
// activate it via PUT /api/outputs/set. Runs on room start + a slow reconcile tick inside the
// supervise loop, so picking in the panel takes effect without an add-on restart.
package main

import "strings"

// selectHomePod locks the room's OwnTone onto its target HomePod. Mirrors select-homepod's logic:
//   - skip while the room is released (don't re-grab a freed HomePod)
//   - match HomepodName case-insensitively; if unset, auto-select the sole AirPlay device
//   - warn (don't select) if the device needs AirPlay verification
//   - only PUT when it isn't already selected (idempotent, quiet)
func selectHomePod(r *Room) {
	if fileExists(releasedPath(r)) {
		return
	}
	devs, ok := fetchOutputsFrom(r.OwnTone)
	if !ok || len(devs) == 0 {
		return
	}
	var target *device
	if r.HomepodName != "" {
		for i := range devs {
			if strings.EqualFold(devs[i].Name, r.HomepodName) {
				target = &devs[i]
				break
			}
		}
	} else if len(devs) == 1 {
		target = &devs[0]
	}
	if target == nil {
		return
	}
	if target.NeedsAuth {
		log.Printf("rooms[%s]: HomePod %q needs AirPlay verification — Apple Home app > this HomePod > 'Allow Speaker & Display Access' > 'Anyone on the Same Network'.", r.ID, target.Name)
		return
	}
	if target.Selected {
		return
	}
	selectOnOwntoneAt(r.OwnTone, target.ID)
	log.Printf("rooms[%s]: selected HomePod %q", r.ID, target.Name)
}
