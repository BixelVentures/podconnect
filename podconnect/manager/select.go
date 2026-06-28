// HomePod output matching. matchOutput resolves a room's bound AirPlay output among an OwnTone
// instance's discovered devices — by stable output id first, then by name. It's used by the
// device-aliases router (routeAliasOutput) + reclaim to point the single OwnTone at the right HomePod.
//
// (The old per-room selectHomePod "pin + self-heal-on-rename" was removed with the per-room
// multi-engine model in 0.25.0 — in single-engine alias mode the router owns output selection, so a
// per-room pin would fight it. If rename-healing is ever wanted in alias mode it must heal against the
// PRIMARY OwnTone for all rooms; routeAliasOutput's id-match already survives most renames.)
package main

import "strings"

// matchOutput finds the room's target output among devs. It prefers the stable HomepodID (so a
// renamed HomePod is still matched); failing that it falls back to a case-insensitive HomepodName
// match; failing that, the sole AirPlay device if the room has no binding yet. Pure -> unit-tested.
// matchedByName reports the name-fallback path fired (the id was empty/stale), so the caller knows to
// persist the output's current id.
func matchOutput(devs []device, homepodID, homepodName string) (idx int, matchedByName bool) {
	if homepodID != "" {
		for i := range devs {
			if devs[i].ID == homepodID {
				return i, false
			}
		}
	}
	if homepodName != "" {
		for i := range devs {
			if strings.EqualFold(devs[i].Name, homepodName) {
				return i, true
			}
		}
		return -1, false
	}
	if homepodID == "" && len(devs) == 1 {
		return 0, true // unbound room with a single AirPlay device — adopt it (and persist its id)
	}
	return -1, false
}
