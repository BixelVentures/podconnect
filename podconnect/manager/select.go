// Per-room HomePod selection — the manager's port of the old select-homepod bash service. For a
// room, find its bound AirPlay output (by stable OwnTone output id first, then by HomepodName as a
// fallback), self-heal the binding when the HomePod was renamed or re-ID'd, honor the released flag,
// warn on needs-auth, and activate it via PUT /api/outputs/set. Runs on room start + a slow reconcile
// tick inside the supervise loop, so picking/renaming takes effect without an add-on restart.
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

// selectHomePod locks the room's OwnTone onto its bound HomePod and heals drift. Self-healing id+name
// binding:
//   - skip while the room is released (don't re-grab a freed HomePod)
//   - match by HomepodID first, then by HomepodName (case-insensitive) — surviving Apple-Home renames
//   - if matched by name (id was empty/stale) -> persist the output's current id (self-populates the
//     migrated r0 and heals id changes)
//   - if the output's Name differs from HomepodName -> the HomePod was renamed: persist the new name,
//     and unless the room's name is user-pinned (NameManual) adopt it as the room Name too, then
//     re-render + restart that room's go-librespot so the Connect device + HA entity follow
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
	idx, matchedByName := matchOutput(devs, r.HomepodID, r.HomepodName)
	if idx < 0 {
		return // not found by id or name — leave the room as-is
	}
	target := &devs[idx]

	// Heal drift. Persist the current id when we fell back to a name match (id empty/stale), and the
	// current name when the HomePod was renamed. syncName mirrors a rename onto the room Name unless
	// the user pinned it. healBinding is no-op when nothing actually changed (quiet + idempotent).
	newID := ""
	if matchedByName {
		newID = target.ID
	}
	newName := ""
	if r.HomepodName != "" && !strings.EqualFold(target.Name, r.HomepodName) {
		newName = target.Name // the HomePod was renamed in Apple Home
	}
	if newID != "" || newName != "" {
		nameChanged := store.healBinding(r.ID, newID, newName, true)
		if newID != "" && r.HomepodID == "" {
			r.HomepodID = newID
		}
		if newName != "" {
			log.Printf("rooms[%s]: HomePod renamed -> %q (healed binding)", r.ID, newName)
			r.HomepodName = newName
		}
		if nameChanged {
			r.Name = newName
			if setGLDeviceName(r, newName) {
				log.Printf("rooms[%s]: room name follows HomePod -> %q (restarting go-librespot)", r.ID, newName)
				if rt := mgr.runtime(r.ID); rt != nil {
					go rt.restartGL()
				}
			}
		}
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
