package main

// Historical absence evolution is not a source observation or a grant. Only
// the separate, short-lived target continuity can support an established lease.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"time"
)

type radioCustodyIdentity struct {
	Process     string `json:"process"`
	Device      string `json:"device_id"`
	Incarnation string `json:"device_incarnation"`
	Session     string `json:"session_epoch"`
	Route       string `json:"route_revision"`
	Base        string `json:"base_revision"`
	Attention   string `json:"attention_revision"`
}
type radioCustodyHold struct {
	Schema     string               `json:"schema"`
	Source     json.RawMessage      `json:"source_absence"`
	Output     radioCustodyIdentity `json:"output_identity"`
	Handoff    string               `json:"handoff_id"`
	Token      string               `json:"token"`
	Generation string               `json:"generation"`
	Revision   string               `json:"native_revision"`
	Peer       string               `json:"sender_peer"`
	Process    string               `json:"radio_process"`
	Epoch      string               `json:"radio_owner_epoch"`
	Digest     string               `json:"receiver_key_digest"`
	Volume     int                  `json:"initial_volume_percent"`
	Provenance string               `json:"volume_provenance"`
}
type radioCustodyEvolution struct {
	Serial      string          `json:"serial"`
	Process     string          `json:"c_process"`
	Future      json.RawMessage `json:"start_future"`
	Token       string          `json:"start_token"`
	Reservation string          `json:"native_reservation_id"`
	Members     string          `json:"member_set_digest"`
	Effect      string          `json:"effect_generation"`
	Completion  json.RawMessage `json:"completion"`
}
type radioTargetContinuity struct {
	Identity   radioCustodyIdentity `json:"identity"`
	Digest     string               `json:"receiver_key_digest"`
	Handoff    string               `json:"handoff_id"`
	Generation string               `json:"generation"`
	Serial     string               `json:"evolution_serial"`
	Effect     string               `json:"effect_generation"`
	ObservedAt string               `json:"observed_at"`
	ValidUntil string               `json:"valid_until"`
}
type radioCustodyProof struct {
	Schema            string                 `json:"schema"`
	OK                bool                   `json:"ok"`
	Purpose           string                 `json:"purpose"`
	Hold              json.RawMessage        `json:"hold"`
	OriginalPhase     string                 `json:"original_phase"`
	OriginalSignature *string                `json:"original_suspended_signature"`
	Phase             string                 `json:"phase"`
	Evolution         radioCustodyEvolution  `json:"evolution"`
	Continuity        *radioTargetContinuity `json:"target_continuity"`
	Physical          bool                   `json:"physical_result_verified"`
}
type radioProducerNativeCustody struct {
	radioProducerNativeRequest
	Phase             string            `json:"phase"`
	Terminal          bool              `json:"terminal"`
	CustodyOnly       bool              `json:"custody_only"`
	Source            json.RawMessage   `json:"source_proof"`
	OriginalSignature *string           `json:"original_suspended_signature"`
	Proof             radioCustodyProof `json:"custody_proof"`
	Binding           radioCustodyHold  `json:"-"`
}
type radioCustodyFuture struct {
	Process     json.RawMessage `json:"native_process"`
	Offer       string          `json:"offer_id"`
	Binding     string          `json:"binding_id"`
	Digest      string          `json:"content_digest"`
	Incarnation string          `json:"native_incarnation"`
	Revision    string          `json:"native_revision"`
	Old         json.RawMessage `json:"old_native_owner"`
	Intended    json.RawMessage `json:"intended_native_owner"`
	Path        string          `json:"expected_input_path"`
	PathDigest  string          `json:"expected_input_path_digest"`
	Policy      string          `json:"policy_revision"`
	Percent     int             `json:"start_volume_percent"`
}

// No duplicate keys, depth bombs, omitted fields, or arbitrary nullable fields
// are admitted by the new union. Existing DTO decoders remain unchanged.
func custodyUniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errRadioUnknown
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if x, ok := t.(json.Delim); ok {
			switch x {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || seen[s] {
						return errRadioUnknown
					}
					seen[s] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return errRadioUnknown
				}
			case '[':
				for d.More() {
					if e := walk(depth + 1); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return errRadioUnknown
				}
			default:
				return errRadioUnknown
			}
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errRadioUnknown
	}
	return nil
}
func custodyObject(raw []byte, out any, names []string, nullable ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != len(names) {
		return nil, errRadioUnknown
	}
	nulls := map[string]bool{}
	for _, s := range nullable {
		nulls[s] = true
	}
	for _, s := range names {
		v, ok := fields[s]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) && !nulls[s] {
			return nil, errRadioUnknown
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errRadioUnknown
	}
	return fields, nil
}

var custodyIdentityKeys = []string{"process", "device_id", "device_incarnation", "session_epoch", "route_revision", "base_revision", "attention_revision"}

func custodyIdentityValid(i radioCustodyIdentity) bool {
	return producerUUID(i.Process) && producerCounter(i.Device, false) && producerCounter(i.Incarnation, false) && producerCounter(i.Session, true) && producerCounter(i.Route, true) && producerCounter(i.Base, true) && producerCounter(i.Attention, true)
}
func custodyProcess(raw []byte) (string, error) {
	var p struct {
		Boot  string `json:"boot_id"`
		PID   string `json:"pid"`
		Ticks string `json:"proc_start_ticks"`
	}
	if _, e := custodyObject(raw, &p, []string{"boot_id", "pid", "proc_start_ticks"}); e != nil || !producerUUID(p.Boot) || !producerCounter(p.PID, false) || !producerCounter(p.Ticks, false) {
		return "", errRadioUnknown
	}
	canonical, _ := json.Marshal(p)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
func custodyOwner(raw []byte) error {
	var o map[string]json.RawMessage
	keys := []string{"account_user_id", "target_id", "room_id", "homepod_id", "connection_id", "session_id", "alias_incarnation", "alias_registry", "alias_revision"}
	if _, e := custodyObject(raw, &o, keys); e != nil {
		return e
	}
	for _, k := range keys {
		if k == "alias_revision" {
			var revision int64
			if json.Unmarshal(o[k], &revision) != nil || revision < 0 || revision > 9007199254740991 {
				return errRadioUnknown
			}
			continue
		}
		var s string
		if json.Unmarshal(o[k], &s) != nil || s == "" || len(s) > 512 {
			return errRadioUnknown
		}
		if k == "homepod_id" && !producerCounter(s, false) || (k == "alias_incarnation" || k == "alias_registry") && !custodyAliasASCII(s) {
			return errRadioUnknown
		}
	}
	return nil
}

// Historical C owner aliases are bounded printable identifiers, not key hashes.
// This does not alter the Native first-grant owner validator.
func custodyAliasASCII(s string) bool {
	if len(s) == 0 || len(s) > 255 {
		return false
	}
	for _, b := range []byte(s) {
		if b < 0x20 || b > 0x7e || b == '"' || b == '\\' {
			return false
		}
	}
	return true
}
func custodyPipe(raw []byte) error {
	var p struct {
		Device string `json:"device"`
		Inode  string `json:"inode"`
		Type   string `json:"type"`
	}
	if _, e := custodyObject(raw, &p, []string{"device", "inode", "type"}); e != nil || p.Type != "fifo" || !producerCounter(p.Device, false) || !producerCounter(p.Inode, false) {
		return errRadioUnknown
	}
	return nil
}
func decodeProducerNativeCustody(raw []byte, q radioProducerNativeRequest) (*radioProducerNativeCustody, error) {
	if q.Schema != "podconnect.native_radio_handoff.v1" || !producerHex(q.Process, 64) || q.Target == "" || len(q.Target) > 255 || !producerUUID(q.Handoff) || !producerUUID(q.Action) || !producerCounter(q.Generation, false) || !producerCounter(q.ExpectedRevision, true) || !producerUUID(q.Peer.Process) || !producerCounter(q.Peer.Epoch, false) || !producerHex(q.Peer.Digest, 64) || !producerHex(q.Token, 32) || custodyUniqueJSON(raw) != nil {
		return nil, errRadioUnknown
	}
	var c radioProducerNativeCustody
	fields, e := custodyObject(raw, &c, []string{"schema", "process_id", "target_id", "handoff_id", "generation", "action_id", "sender_peer", "token", "expected_native_revision", "phase", "terminal", "custody_only", "source_proof", "original_suspended_signature", "custody_proof"}, "original_suspended_signature")
	expected := q
	expected.Schema = "podconnect.native_radio_handoff_custody.v1"
	if e != nil || c.radioProducerNativeRequest != expected || c.Terminal || !c.CustodyOnly {
		return nil, errRadioUnknown
	}
	if _, e = custodyObject(fields["sender_peer"], new(radioProducerNativePeer), []string{"radio_process", "radio_owner_epoch", "receiver_key_digest"}); e != nil {
		return nil, e
	}
	kfields, e := custodyObject(fields["custody_proof"], &c.Proof, []string{"schema", "ok", "purpose", "hold", "original_phase", "original_suspended_signature", "phase", "evolution", "target_continuity", "physical_result_verified"}, "original_suspended_signature", "target_continuity")
	k := &c.Proof
	if e != nil || k.Schema != "podconnect.output_radio_absence_custody.v1" || !k.OK || k.Purpose != "radio_absence_custody_v1" || k.Physical || k.Phase != c.Phase || !producerJSONEqual(c.Source, k.Hold) {
		return nil, errRadioUnknown
	}
	if (c.OriginalSignature == nil) != (k.OriginalSignature == nil) || c.OriginalSignature != nil && *c.OriginalSignature != *k.OriginalSignature {
		return nil, errRadioUnknown
	}
	switch k.OriginalPhase {
	case "prepared":
		if k.OriginalSignature != nil {
			return nil, errRadioUnknown
		}
	case "suspended":
		if k.OriginalSignature == nil || !producerHex(*k.OriginalSignature, 64) {
			return nil, errRadioUnknown
		}
	default:
		return nil, errRadioUnknown
	}
	hfields, e := custodyObject(c.Source, &c.Binding, []string{"schema", "source_absence", "output_identity", "handoff_id", "token", "generation", "native_revision", "sender_peer", "radio_process", "radio_owner_epoch", "receiver_key_digest", "initial_volume_percent", "volume_provenance"})
	h := &c.Binding
	if e != nil || h.Schema != "podconnect.output_radio_absence.v1" || h.Handoff != q.Handoff || h.Token != q.Token || h.Generation != q.Generation || !producerCounter(h.Revision, false) || h.Peer != q.Peer.Process || h.Process != q.Peer.Process || h.Epoch != q.Peer.Epoch || h.Digest != q.Peer.Digest || h.Volume < 0 || h.Volume > 100 || h.Provenance != "stored_manual_base" {
		return nil, errRadioUnknown
	}
	if _, e = custodyObject(hfields["output_identity"], &h.Output, custodyIdentityKeys); e != nil || !custodyIdentityValid(h.Output) {
		return nil, errRadioUnknown
	}
	var source struct {
		Kind     string          `json:"kind"`
		Process  json.RawMessage `json:"native_process"`
		Registry string          `json:"registry_digest"`
		Path     string          `json:"input_path"`
		Digest   string          `json:"input_path_digest"`
		Pipe     json.RawMessage `json:"pipe_identity"`
	}
	if _, e = custodyObject(h.Source, &source, []string{"kind", "native_process", "registry_digest", "input_path", "input_path_digest", "pipe_identity"}); e != nil || source.Kind != "global" || !producerHex(source.Registry, 64) || !producerHex(source.Digest, 64) || !filepath.IsAbs(source.Path) || custodyPipe(source.Pipe) != nil {
		return nil, errRadioUnknown
	}
	process, e := custodyProcess(source.Process)
	if e != nil || process != q.Process {
		return nil, errRadioUnknown
	}
	if _, e = custodyObject(kfields["evolution"], &k.Evolution, []string{"serial", "c_process", "start_future", "start_token", "native_reservation_id", "member_set_digest", "effect_generation", "completion"}, "completion"); e != nil {
		return nil, e
	}
	x := &k.Evolution
	if !producerCounter(x.Serial, false) || x.Process != h.Output.Process || !producerHex(x.Token, 32) || !producerHex(x.Reservation, 32) || !producerHex(x.Members, 64) || !producerCounter(x.Effect, false) {
		return nil, errRadioUnknown
	}
	var f radioCustodyFuture
	if _, e = custodyObject(x.Future, &f, []string{"native_process", "offer_id", "binding_id", "content_digest", "native_incarnation", "native_revision", "old_native_owner", "intended_native_owner", "expected_input_path", "expected_input_path_digest", "policy_revision", "start_volume_percent"}, "old_native_owner"); e != nil || !producerJSONEqual(f.Process, source.Process) || !producerHex(f.Offer, 32) || !producerHex(f.Binding, 64) || !producerHex(f.Digest, 64) || !producerHex(f.Incarnation, 32) || !producerCounter(f.Revision, false) || f.Path != source.Path || f.PathDigest != source.Digest || f.Policy != "7fc3239b1c5e65cdb49cca308ad9368c50ef0a90d4cd5fd537a9f97de488625d" || f.Percent != 35 || custodyOwner(f.Intended) != nil {
		return nil, errRadioUnknown
	}
	if !bytes.Equal(bytes.TrimSpace(f.Old), []byte("null")) && custodyOwner(f.Old) != nil {
		return nil, errRadioUnknown
	}
	var intended struct {
		Device string `json:"homepod_id"`
	}
	_ = json.Unmarshal(f.Intended, &intended)
	if intended.Device == h.Output.Device {
		return nil, errRadioUnknown
	}
	isNull := bytes.Equal(bytes.TrimSpace(x.Completion), []byte("null"))
	switch c.Phase {
	case "evolution_pending":
		if !isNull || k.Continuity != nil {
			return nil, errRadioUnknown
		}
	case "source_evolved":
		if isNull || custodyCompletion(x.Completion, f, *x, source.Pipe, source.Digest) != nil {
			return nil, errRadioUnknown
		}
	case "evolution_unknown":
		if k.Continuity != nil || !isNull && custodyCompletion(x.Completion, f, *x, source.Pipe, source.Digest) != nil {
			return nil, errRadioUnknown
		}
	default:
		return nil, errRadioUnknown
	}
	if k.Continuity != nil {
		if k.OriginalPhase != "suspended" || c.Phase != "source_evolved" {
			return nil, errRadioUnknown
		}
		if _, e = custodyObject(kfields["target_continuity"], k.Continuity, []string{"identity", "receiver_key_digest", "handoff_id", "generation", "evolution_serial", "effect_generation", "observed_at", "valid_until"}); e != nil {
			return nil, e
		}
		var tcfields map[string]json.RawMessage
		_ = json.Unmarshal(kfields["target_continuity"], &tcfields)
		if _, e = custodyObject(tcfields["identity"], &k.Continuity.Identity, custodyIdentityKeys); e != nil || !c.targetCurrent(time.Now()) {
			return nil, errRadioUnknown
		}
	}
	return &c, nil
}
func custodyCompletion(raw []byte, f radioCustodyFuture, e radioCustodyEvolution, pipe json.RawMessage, digest string) error {
	var p map[string]json.RawMessage
	keys := []string{"schema", "ok", "token", "offer_id", "binding_id", "content_digest", "native_incarnation", "native_revision", "native_reservation_id", "policy_revision", "start_volume_percent", "intended_native_owner", "actual_native_owner", "target_identity", "route_identity", "input_path_digest", "pipe_identity", "input_epoch", "writer_source_epoch", "phase", "possible_effect", "contexts_drained", "gate_closed", "deadline_retired", "pin_remaining_ms", "expires_after_ms", "physical_result_verified"}
	if _, err := custodyObject(raw, &p, keys); err != nil {
		return err
	}
	stringsExpected := map[string]string{"token": e.Token, "offer_id": f.Offer, "binding_id": f.Binding, "content_digest": f.Digest, "native_incarnation": f.Incarnation, "native_revision": f.Revision, "native_reservation_id": e.Reservation, "policy_revision": f.Policy, "input_path_digest": digest, "phase": "completed"}
	for key, want := range stringsExpected {
		var got string
		if json.Unmarshal(p[key], &got) != nil || got != want {
			return errRadioUnknown
		}
	}
	var schema string
	_ = json.Unmarshal(p["schema"], &schema)
	if schema != "podconnect.output_start_complete.v1" {
		return errRadioUnknown
	}
	if !producerJSONEqual(p["intended_native_owner"], f.Intended) || !producerJSONEqual(p["actual_native_owner"], f.Intended) || !producerJSONEqual(p["pipe_identity"], pipe) {
		return errRadioUnknown
	}
	for _, key := range []string{"ok", "possible_effect", "contexts_drained", "gate_closed", "deadline_retired", "physical_result_verified"} {
		var b bool
		if json.Unmarshal(p[key], &b) != nil || (key == "ok" || key == "possible_effect" || key == "contexts_drained") && !b || (key == "gate_closed" || key == "deadline_retired" || key == "physical_result_verified") && b {
			return errRadioUnknown
		}
	}
	for _, key := range []string{"start_volume_percent", "pin_remaining_ms", "expires_after_ms"} {
		var n int
		if json.Unmarshal(p[key], &n) != nil || n < 0 || key == "start_volume_percent" && n != 35 || key == "pin_remaining_ms" && n > 15000 || key == "expires_after_ms" && n != 8000 {
			return errRadioUnknown
		}
	}
	for _, key := range []string{"input_epoch", "writer_source_epoch"} {
		var s string
		if json.Unmarshal(p[key], &s) != nil || !producerCounter(s, false) {
			return errRadioUnknown
		}
	}
	var target struct {
		Process     string `json:"process"`
		Device      string `json:"device_id"`
		Incarnation string `json:"device_incarnation"`
	}
	if _, err := custodyObject(p["target_identity"], &target, []string{"process", "device_id", "device_incarnation"}); err != nil || target.Process != e.Process || !producerCounter(target.Device, false) || !producerCounter(target.Incarnation, false) {
		return errRadioUnknown
	}
	var owner struct {
		Device string `json:"homepod_id"`
	}
	_ = json.Unmarshal(f.Intended, &owner)
	if target.Device != owner.Device {
		return errRadioUnknown
	}
	var route map[string]json.RawMessage
	if _, err := custodyObject(p["route_identity"], &route, []string{"process", "device_id", "device_incarnation", "session_epoch", "route_revision", "selection_revision", "input_generation", "input_item_id", "input_path_digest"}); err != nil {
		return err
	}
	for _, key := range []string{"process", "device_id", "device_incarnation", "input_path_digest"} {
		var got string
		_ = json.Unmarshal(route[key], &got)
		want := map[string]string{"process": target.Process, "device_id": target.Device, "device_incarnation": target.Incarnation, "input_path_digest": digest}[key]
		if got != want {
			return errRadioUnknown
		}
	}
	for _, key := range []string{"session_epoch", "route_revision", "selection_revision", "input_generation", "input_item_id"} {
		var got string
		if json.Unmarshal(route[key], &got) != nil || !producerCounter(got, key == "route_revision" || key == "selection_revision") {
			return errRadioUnknown
		}
	}
	return nil
}
func (c *radioProducerNativeCustody) targetCurrent(now time.Time) bool {
	t := c.Proof.Continuity
	h := c.Binding
	if t == nil || c.Phase != "source_evolved" || c.Proof.OriginalPhase != "suspended" || c.OriginalSignature == nil || !producerHex(*c.OriginalSignature, 64) || !custodyIdentityValid(t.Identity) || t.Identity.Process != h.Output.Process || t.Identity.Device != h.Output.Device || t.Identity.Incarnation != h.Output.Incarnation || t.Digest != h.Digest || t.Handoff != h.Handoff || t.Generation != h.Generation || t.Serial != c.Proof.Evolution.Serial || !producerCounter(t.Effect, false) {
		return false
	}
	at, a := time.Parse(time.RFC3339Nano, t.ObservedAt)
	until, b := time.Parse(time.RFC3339Nano, t.ValidUntil)
	return a == nil && b == nil && !at.After(now) && until.After(now) && until.After(at) && !until.After(at.Add(2*time.Second))
}

// This map is populated only by an actual normal signed suspension receipt in
// this live Native client. A custody-only read or restored manager cannot adopt it.
func (n *radioProducerNativeClient) rememberSuspended(h radioProducerHold, r radioProducerNativeReply) error {
	env, e := nativeProducerEnvelope(h)
	if e != nil || r.Custody != nil || r.Phase != "suspended" || r.radioProducerNativeRequest != env.Request || !producerJSONEqual(r.Proof, env.Binding) || !producerHex(r.SuspendedSignature, 64) {
		return errRadioUnknown
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if previous, ok := n.established[h.Handoff]; ok && (previous.radioProducerNativeRequest != r.radioProducerNativeRequest || !producerJSONEqual(previous.Proof, r.Proof) || previous.SuspendedSignature != r.SuspendedSignature) {
		return errRadioUnknown
	}
	if n.established == nil {
		n.established = map[string]radioProducerNativeReply{}
	}
	if n.holds == nil {
		n.holds = map[string]radioProducerNativeReply{}
	}
	n.established[h.Handoff] = r
	n.holds[h.Handoff] = r
	return nil
}
func (n *radioProducerNativeClient) retainedSuspended(ctx context.Context, h radioProducerHold, c *radioProducerNativeCustody) (string, error) {
	env, e := nativeProducerEnvelope(h)
	if e != nil || c == nil || !producerJSONEqual(c.Source, env.Binding) || !c.targetCurrent(time.Now()) {
		return "", errRadioUnknown
	}
	n.mu.Lock()
	original, ok := n.established[h.Handoff]
	n.mu.Unlock()
	if !ok || original.radioProducerNativeRequest != env.Request || !producerJSONEqual(original.Proof, env.Binding) || c.OriginalSignature == nil || original.SuspendedSignature != *c.OriginalSignature {
		return "", errRadioUnknown
	}
	s, e := n.readState(ctx)
	if e != nil || s.ProcessID != h.Process || !c.targetCurrent(time.Now()) {
		return "", errRadioUnknown
	}
	return original.SuspendedSignature, nil
}

type radioSenderOwnership struct {
	Native    radioProducerHold
	Sender    radioProducerHold
	Operation radioProducerIdentity
	Phase     string
}

func (s *radioProducerSenderClient) rememberOwned(h radioProducerHold, op radioProducerIdentity, r radioProducerSenderReply) {
	env, e := senderProducerEnvelope(h)
	if e != nil || r.Token != h.Token || r.Process != h.Process || r.Epoch != strconv.FormatUint(h.OwnerGeneration, 10) || r.SourceGeneration != strconv.FormatUint(h.SourceGeneration, 10) || r.Signature != "" || r.Challenge != "" {
		return
	}
	native, e := nativeProducerEnvelope(env.Native)
	if e != nil {
		return
	}
	s.native.mu.Lock()
	established, establishedOK := s.native.established[env.Native.Handoff]
	s.native.mu.Unlock()
	if !establishedOK || established.radioProducerNativeRequest != native.Request || !producerJSONEqual(established.Proof, native.Binding) || !producerHex(established.SuspendedSignature, 64) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owned == nil {
		s.owned = map[string]radioSenderOwnership{}
	}
	prior, known := s.owned[env.Native.Handoff]
	if r.Phase == "playing" && r.Observed {
		if admitted, ok := s.admitted[op.Handoff]; !ok || admitted != op {
			return
		}
		s.owned[env.Native.Handoff] = radioSenderOwnership{env.Native, h, op, "playing"}
	} else if r.Phase == "drained" && known && prior.Sender.Token == h.Token && prior.Sender.Process == h.Process && prior.Sender.SourceGeneration == h.SourceGeneration {
		prior.Operation = op
		prior.Phase = "drained"
		s.owned[env.Native.Handoff] = prior
	}
}
func (s *radioProducerSenderClient) ValidateRetainedTarget(ctx context.Context, n radioProducerHold, c *radioProducerNativeCustody) (string, error) {
	signature, e := s.native.retainedSuspended(ctx, n, c)
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	prior, ok := s.owned[n.Handoff]
	process := s.process
	s.mu.Unlock()
	if !ok || !retainedOwnershipMatches(n, prior, process) {
		return "", errRadioUnknown
	}
	q, e := senderOperationBody(prior.Sender, prior.Operation)
	if e != nil {
		return "", e
	}
	state := map[string]any{}
	for _, key := range []string{"schema", "device_id", "operation_id", "operation_generation", "radio_process", "radio_owner_epoch", "sender_token"} {
		state[key] = q[key]
	}
	r, e := s.call(ctx, "state", state)
	if e != nil || !retainedSenderCurrent(prior, r, c, time.Now()) {
		return "", errRadioUnknown
	}
	return signature, nil
}

func retainedOwnershipMatches(n radioProducerHold, prior radioSenderOwnership, process string) bool {
	return prior.Native.radioProducerIdentity == n.radioProducerIdentity && prior.Native.Token == n.Token && producerJSONEqual(prior.Native.Proof, n.Proof) && prior.Sender.Process == process && (prior.Phase == "playing" || prior.Phase == "drained")
}
func retainedSenderCurrent(prior radioSenderOwnership, r radioProducerSenderReply, c *radioProducerNativeCustody, now time.Time) bool {
	return c != nil && r.Process == prior.Sender.Process && r.Token == prior.Sender.Token && r.Epoch == strconv.FormatUint(prior.Sender.OwnerGeneration, 10) && r.SourceGeneration == strconv.FormatUint(prior.Sender.SourceGeneration, 10) && r.Digest == c.Binding.Digest && r.Device == c.Binding.Output.Device && r.Operation == prior.Operation.Handoff && r.Generation == strconv.FormatUint(prior.Operation.Generation, 10) && r.Signature == "" && r.Challenge == "" && ((r.Phase == "playing" && r.Observed) || r.Phase == "drained") && c.targetCurrent(now)
}

func (p *radioProducer) retainedResult(ctx context.Context, r radioProducerRecord, h radioProducerHold) error {
	result, e := p.native.Result(ctx, r.Request, h)
	if e != nil {
		return errRadioUnknown
	}
	if result.Identity != h.radioProducerIdentity {
		return errRadioUnknown
	}
	if result.Custody != nil {
		v, ok := p.sender.(interface {
			ValidateRetainedTarget(context.Context, radioProducerHold, *radioProducerNativeCustody) (string, error)
		})
		if !ok {
			return errRadioUnknown
		}
		_, e = v.ValidateRetainedTarget(ctx, h, result.Custody)
		return e
	}
	if !result.Terminal || result.Outcome != "committed" || result.Identity != h.radioProducerIdentity {
		return errRadioUnknown
	}
	return nil
}

// The third reuse boundary returns only the original signature, after a fresh
// retained-target check. It cannot turn a custody result into a normal grant.
func producerSenderStartSignature(reply radioProducerNativeReply, err error, retained func(*radioProducerNativeCustody) (string, error)) (string, error) {
	if reply.Custody != nil {
		if !errors.Is(err, errRadioUnknown) || reply.Phase != "" || len(reply.Proof) != 0 || reply.SuspendedSignature != "" || retained == nil {
			return "", errRadioUnknown
		}
		signature, e := retained(reply.Custody)
		if e != nil || reply.Custody.OriginalSignature == nil || signature != *reply.Custody.OriginalSignature || !producerHex(signature, 64) {
			return "", errRadioUnknown
		}
		return signature, nil
	}
	if err != nil {
		return "", err
	}
	if reply.Phase != "suspended" || !producerHex(reply.SuspendedSignature, 64) {
		return "", errRadioUnknown
	}
	return reply.SuspendedSignature, nil
}
