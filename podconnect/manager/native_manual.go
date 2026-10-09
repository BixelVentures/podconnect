package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// This is an observation of one atomic native speaker snapshot. It is published
// only by the existing catalog read; events cannot create or rebase a challenge.
type nativeManualObservation struct {
	Identity                  nativeAttentionIdentity
	Binding                   aliasBinding
	Alias                     int
	AliasRevision, Run, Phase uint64
	HomepodID, HomepodName    string
}
type nativeManualAction struct {
	ID                        string       `json:"action_id"`
	Origin                    string       `json:"origin"`
	Ordinal                   string       `json:"ordinal"`
	Binding                   aliasBinding `json:"alias_binding"`
	Alias                     int          `json:"alias_id"`
	Value                     int          `json:"value"`
	AliasRevision, Run, Phase uint64
	Observation               *nativeManualObservation
}
type nativeManualAttempt struct {
	Action nativeManualAction
	Grant  nativeAttentionGrant
	Fault  string
}
type nativeManualReport struct {
	ActionID string                `json:"action_id"`
	Origin   string                `json:"origin"`
	Ordinal  string                `json:"ordinal"`
	Fault    string                `json:"fault,omitempty"`
	Result   nativeAttentionResult `json:"result"`
}

func (l *glLive) observeNativeCatalog(rows []map[string]any, request *nativeManualObservation) {
	devices := make([]device, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row["attention_identity"])
		if err != nil {
			continue
		}
		var state nativeAttentionIdentity
		if json.Unmarshal(encoded, &state) != nil || !state.valid() || state.DeviceID != fmt.Sprint(row["id"]) {
			continue
		}
		name, _ := row["name"].(string)
		devices = append(devices, device{ID: state.DeviceID, Name: name, Selected: asBool(row["selected"]), AttentionIdentity: &state})
	}
	l.observeNativeDevices(devices, request)
}

// Capture this before issuing the existing catalog HTTP request. A response
// cannot be relabeled with a source/alias born while that request was pending.
func (l *glLive) nativeCatalogRequest() *nativeManualObservation {
	snapshot, revision := l.routeSnapshot()
	if snapshot.AliasBinding == nil || !snapshot.AliasBinding.valid() {
		return nil
	}
	var room *Room
	for _, candidate := range loadRooms() {
		if candidate.ID == snapshot.AliasBinding.RoomID {
			room = candidate
			break
		}
	}
	if room == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.sourceRetired || l.aliasRevision != revision || l.st.SelAlias != snapshot.SelAlias || !sameAliasBinding(l.st.AliasBinding, snapshot.AliasBinding) {
		return nil
	}
	return &nativeManualObservation{Binding: *snapshot.AliasBinding, Alias: snapshot.SelAlias, AliasRevision: revision, Run: l.runEpoch, Phase: l.phaseEpoch, HomepodID: room.HomepodID, HomepodName: room.HomepodName}
}
func (l *glLive) observeNativeDevices(devices []device, request *nativeManualObservation) {
	if request == nil {
		return
	}
	index, _ := matchOutput(devices, request.HomepodID, request.HomepodName)
	if index < 0 || !devices[index].Selected || devices[index].AttentionIdentity == nil {
		return
	}
	native := *devices[index].AttentionIdentity
	if !native.valid() || native.DeviceID != devices[index].ID {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.sourceRetired || l.runEpoch != request.Run || l.phaseEpoch != request.Phase || l.aliasRevision != request.AliasRevision || l.st.SelAlias != request.Alias || !sameAliasBinding(l.st.AliasBinding, &request.Binding) {
		return
	}
	observed := *request
	observed.Identity = native
	l.manualObservation = &observed
}
func (l *glLive) nativeManualSupported() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.manualObservation != nil
}
func (l *glLive) admitManualEventLocked(data map[string]any) {
	l.observeStopLocked()
	if l.sourceRetired || l.st.AliasBinding == nil || !l.st.AliasBinding.valid() {
		return
	}
	origin, _ := data["origin"].(string)
	if origin != "spotify_connect" && origin != "api" {
		return
	}
	// Any declared automation identity has no external manual authority, including
	// another manager process's late echo. No unbounded echo ledger is necessary.
	process, _ := data["manager_process"].(string)
	request, _ := data["request_id"].(string)
	if process != "" || request != "" {
		return
	}
	if _, present := data["manager_process"]; present && data["manager_process"] != "" {
		return
	}
	if _, present := data["request_id"]; present && data["request_id"] != "" {
		return
	}
	binding, present := eventAliasBinding(data)
	if !present || binding == nil || !binding.valid() || !sameAliasBinding(binding, l.st.AliasBinding) {
		return
	}
	alias, ok := numField(data, "alias_id")
	if !ok || alias != float64(l.st.SelAlias) || alias != math.Trunc(alias) {
		return
	}
	value, ok := numField(data, "value")
	if !ok || value < 0 || value > 100 || value != math.Trunc(value) {
		return
	}
	ordinal, ok := data["ordinal"].(string)
	if !ok || !nativeCurrency(ordinal) {
		return
	}
	sequence, err := strconv.ParseUint(ordinal, 10, 64)
	if err != nil || sequence == 0 || sequence == math.MaxUint64 {
		return
	}
	if l.manualIncarnation == binding.Incarnation && sequence <= l.manualOrdinal {
		return
	}
	id := attentionUUID()
	if id == "" {
		return
	}
	l.manualIncarnation = binding.Incarnation
	l.manualOrdinal = sequence
	action := &nativeManualAction{ID: id, Origin: origin, Ordinal: ordinal, Binding: *binding, Alias: int(alias), Value: int(value), AliasRevision: l.aliasRevision, Run: l.runEpoch, Phase: l.phaseEpoch}
	observed := l.manualObservation
	if observed != nil && observed.AliasRevision == action.AliasRevision && observed.Run == action.Run && observed.Phase == action.Phase && observed.Binding == action.Binding && observed.Alias == action.Alias {
		copy := *observed
		action.Observation = &copy
	}
	l.manualAction = action
}
func (l *glLive) manualSnapshot() *nativeManualAction {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	if l.manualAction == nil {
		return nil
	}
	copy := *l.manualAction
	return &copy
}
func (l *glLive) claimManual(action nativeManualAction) bool {
	// Same existing room-store -> live lock order as route admission. Configuration
	// changes cannot admit a prepared action for an obsolete catalog target.
	if action.Observation == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	rooms, err := store.loadLocked()
	if err != nil {
		return false
	}
	entries, ok := aliasEntriesForRooms(rooms.Rooms, connectAliasesForRooms(rooms.Rooms))
	if !ok || aliasRegistryHash(entries) != action.Binding.Registry {
		return false
	}
	matched := false
	for _, entry := range entries {
		if entry.RoomID == action.Binding.RoomID && entry.ID == uint32(action.Alias) {
			matched = true
		}
	}
	if !matched {
		return false
	}
	matched = false
	for _, room := range rooms.Rooms {
		if room.ID == action.Binding.RoomID && room.HomepodID == action.Observation.HomepodID && room.HomepodName == action.Observation.HomepodName {
			matched = true
		}
	}
	if !matched {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	return !l.sourceRetired && l.manualAction != nil && l.manualAction.ID == action.ID && l.runEpoch == action.Run && l.phaseEpoch == action.Phase && l.aliasRevision == action.AliasRevision && l.st.SelAlias == action.Alias && sameAliasBinding(l.st.AliasBinding, &action.Binding)
}

func (a *attention) nativeManualReconcile(base string, live *glLive) bool {
	action := live.manualSnapshot()
	a.mu.Lock()
	if a.nativeBusy || a.custodyBlockedLocked() {
		a.mu.Unlock()
		return false
	}
	a.nativeBusy = true
	attempt := a.nativeManualAttempt
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.nativeBusy = false
		if attempt != nil {
			a.nativeManualReport = &nativeManualReport{attempt.Action.ID, attempt.Action.Origin, attempt.Action.Ordinal, attempt.Fault, attempt.Grant.Result}
		}
		a.mu.Unlock()
	}()
	// An old uncertain/pending command may only resolve its original UUID receipt.
	// A new action cannot cause a second application or borrow another challenge.
	if attempt != nil && attempt.Fault == "" && (attempt.Grant.Uncertain || attempt.Grant.Result.Outcome == "pending") {
		if err := attempt.Grant.observe(base, attempt.Grant.Command.Expected.DeviceID); err != nil {
			if status, ok := err.(nativeAttentionHTTPStatus); ok {
				attempt.Fault = fmt.Sprintf("unknown: native HTTP %d resolving original manual receipt; no replay authority", status)
			} else {
				return false
			}
		}
		if attempt.Fault == "" && (attempt.Grant.Uncertain || attempt.Grant.Result.Outcome == "pending") {
			return false
		}
	}
	if action == nil || attempt != nil && attempt.Action.ID == action.ID {
		return true
	}
	attempt = &nativeManualAttempt{Action: *action}
	a.mu.Lock()
	a.nativeManualAttempt = attempt
	a.mu.Unlock()
	if action.Observation == nil {
		attempt.Fault = "unknown: no native challenge observed before this action"
		return true
	}
	command := nativeAttentionCommand{Expected: action.Observation.Identity, RequestID: action.ID, Action: "manual", Cap: action.Value}
	// This local claim is admission before I/O, not proof of wire time or playback.
	if !live.claimManual(*action) {
		attempt.Fault = "retired: source, alias or latest action changed before admission"
		return true
	}
	attempt.Grant = nativeAttentionGrant{Identity: command.Expected, Command: command, Uncertain: true}
	if err := attempt.Grant.submit(base, command.Expected.DeviceID); err != nil {
		if status, ok := err.(nativeAttentionHTTPStatus); ok {
			attempt.Fault = fmt.Sprintf("native HTTP %d: original manual command rejected", status)
			return true
		}
		return false
	}
	return !attempt.Grant.Uncertain && attempt.Grant.Result.Outcome != "pending"
}
