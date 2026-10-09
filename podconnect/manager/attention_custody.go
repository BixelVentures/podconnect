package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// Custody is retirement authority, never authority to replay a BEGIN or adopt a
// lease from another daemon. Every child retains the endpoint and original BEGIN.
type nativeAttentionOrigin struct {
	Process   string `json:"process"`
	RequestID string `json:"request_id"`
}
type nativeRecoveryObservation struct {
	DrainedOperation string `json:"last_drained_operation"`
	DrainedRequestID string `json:"last_drained_request_id"`
	nativeAttentionObservation
	Idle *bool `json:"recovery_idle,omitempty"`
}
type nativeRecoveryCommand struct {
	nativeAttentionCommand
	Origin nativeAttentionOrigin `json:"origin"`
}
type nativeRecoveryTerminal struct {
	Identity     nativeAttentionIdentity `json:"identity"`
	RequestID    string                  `json:"request_id"`
	Operation    string                  `json:"operation"`
	Action       string                  `json:"action"`
	ResponseCode int                     `json:"response_code"`
	CSeq         int                     `json:"request_cseq"`
}
type nativeRecoveryProof struct {
	Database       string                  `json:"database"`
	LedgerComplete bool                    `json:"ledger_complete"`
	Terminal       *nativeRecoveryTerminal `json:"terminal,omitempty"`
}
type nativeRecoveryResult struct {
	nativeAttentionResult
	Origin nativeAttentionOrigin `json:"origin"`
	State  string                `json:"recovery_state"`
	Proof  *nativeRecoveryProof  `json:"recovery_proof,omitempty"`
}
type custodyOutput struct {
	ConsumedDrains map[string]bool         `json:"consumed_drains,omitempty"`
	NoSubmission   bool                    `json:"no_submission,omitempty"`
	Release        *nativeAttentionResult  `json:"release,omitempty"`
	Original       nativeAttentionIdentity `json:"original"`
	Base           string                  `json:"endpoint"`
	DeviceID       string                  `json:"device_id"`
	Origin         nativeAttentionOrigin   `json:"origin"`
	Retired        bool                    `json:"retired"`
	Recovery       *nativeRecoveryCommand  `json:"recovery,omitempty"`
	Result         *nativeRecoveryResult   `json:"result,omitempty"`
}
type custodySession struct {
	Process  string                    `json:"process"`
	Revision string                    `json:"revision"`
	Nonce    string                    `json:"session"`
	Retired  bool                      `json:"retired"`
	Outputs  map[string]*custodyOutput `json:"outputs"`
}
type attentionCustody struct {
	Version  int               `json:"version"`
	Room     string            `json:"room"`
	Sessions []*custodySession `json:"sessions"`
	path     string
	fault    error
	imported bool
}

// File and containing directory are synced before admission/effects. A failed
// durability boundary fences the owner; it must not ACK or infer retirement.
func (c *attentionCustody) save() error {
	if c.fault != nil {
		return c.fault
	}
	data, err := json.Marshal(c)
	if err != nil {
		c.fault = err
		return err
	}
	if len(data) > 16*1024*1024 {
		c.fault = errors.New("custody journal capacity exceeded")
		return c.fault
	}
	dir := filepath.Dir(c.path)
	missing := []string{}
	for cursor := dir; ; cursor = filepath.Dir(cursor) {
		if _, e := os.Stat(cursor); e == nil {
			break
		} else if !os.IsNotExist(e) {
			c.fault = e
			return e
		}
		missing = append(missing, cursor)
		if filepath.Dir(cursor) == cursor {
			c.fault = errors.New("missing custody filesystem root")
			return c.fault
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err = os.Mkdir(missing[i], 0700); err != nil && !os.IsExist(err) {
			c.fault = err
			return err
		}
		parent, e := os.Open(filepath.Dir(missing[i]))
		if e != nil {
			c.fault = e
			return e
		}
		e = parent.Sync()
		closeErr := parent.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			c.fault = e
			return e
		}
	}
	f, err := os.CreateTemp(dir, ".attention-custody-*")
	if err != nil {
		c.fault = err
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), c.path)
	}
	if err == nil {
		var d *os.File
		d, err = os.Open(dir)
		if err == nil {
			err = d.Sync()
			closeErr = d.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		c.fault = err
	}
	return err
}
func (a *attention) loadCustody(path, room string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := &attentionCustody{Version: 1, Room: room, path: path}
	a.custody = c
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		c.fault = err
		return
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 16*1024*1024))
	dec.DisallowUnknownFields()
	if err = dec.Decode(c); err == nil {
		var extra any
		if dec.Decode(&extra) != io.EOF {
			err = errors.New("extra custody data")
		}
	}
	if err != nil || c.Version != 1 || c.Room != room {
		c.fault = errors.New("invalid attention custody journal")
		return
	}
	seen := map[string]bool{}
	for _, s := range c.Sessions {
		if s == nil || !validAttentionNonce(s.Process) || !validAttentionNonce(s.Nonce) || !nativeCurrency(s.Revision) || s.Revision == "0" || seen[s.Process+":"+s.Nonce] {
			c.fault = errors.New("invalid custody session")
			return
		}
		seen[s.Process+":"+s.Nonce] = true
		for key, o := range s.Outputs {
			if o == nil || key != o.Origin.RequestID || !validAttentionNonce(key) || !validAttentionNonce(o.Origin.Process) || !nativeCurrency(o.DeviceID) || !o.Original.valid() || o.Original.DeviceID != o.DeviceID || o.Original.Process != o.Origin.Process || o.Base == "" || s.Retired && !o.Retired {
				c.fault = errors.New("invalid custody output")
				return
			}
			if o.Retired && !o.NoSubmission && !validCustodyRelease(o) && (o.Recovery == nil || o.Result == nil || !recoveryReceipt(*o.Recovery, o.Original, o.DeviceID, *o.Result)) {
				c.fault = errors.New("custody retirement lacks exact proof")
				return
			}
			if o.Recovery != nil && (!o.Recovery.Expected.valid() || o.Recovery.Expected.DeviceID != o.DeviceID || !validAttentionNonce(o.Recovery.RequestID) || o.Recovery.Action != "recover" || o.Recovery.Origin != o.Origin || o.Recovery.Cap != 0 || o.Recovery.TTL != 0) {
				c.fault = errors.New("invalid custody recovery")
				return
			}
		}
		if !s.Retired {
			c.imported = true
		}
	}
}
func (a *attention) custodyBlockedLocked() bool {
	return a.deleting || a.custody != nil && (a.custody.fault != nil || a.custody.imported)
}
func (a *attention) custodyBlocked() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.custodyBlockedLocked()
}
func (a *attention) custodySessionLocked() *custodySession {
	if a.custody == nil {
		return nil
	}
	for _, s := range a.custody.Sessions {
		if s.Process == attentionManagerProcess() && s.Nonce == a.sessionNonce && s.Revision == strconv.FormatUint(a.admissionRevision, 10) {
			return s
		}
	}
	return nil
}
func (a *attention) custodyAdmitLocked(nonce string) bool {
	if a.custody == nil {
		return true
	}
	s := &custodySession{Process: attentionManagerProcess(), Revision: strconv.FormatUint(a.admissionRevision+1, 10), Nonce: nonce, Outputs: map[string]*custodyOutput{}}
	a.custody.Sessions = append(a.custody.Sessions, s)
	return a.custody.save() == nil
}
func (a *attention) custodyBeginLocked(base, id string, g *nativeAttentionGrant) bool {
	if a.custody == nil {
		return true
	}
	s := a.custodySessionLocked()
	if s == nil {
		a.custody.fault = errors.New("missing session custody")
		return false
	}
	origin := nativeAttentionOrigin{g.Command.Expected.Process, g.Command.RequestID}
	s.Outputs[origin.RequestID] = &custodyOutput{Base: base, DeviceID: id, Origin: origin, Original: g.Command.Expected}
	g.Origin = origin
	return a.custody.save() == nil
}
func (a *attention) custodyRetireLocked(g *nativeAttentionGrant) bool {
	if a.custody == nil {
		return true
	}
	s := a.custodySessionLocked()
	if s == nil || s.Outputs[g.Origin.RequestID] == nil {
		a.custody.fault = errors.New("missing output custody")
		return false
	}
	o := s.Outputs[g.Origin.RequestID]
	if !g.Submitted {
		o.NoSubmission = true
	} else {
		receipt := g.Result
		o.Release = &receipt
		if !validCustodyRelease(o) {
			return false
		}
	}
	o.Retired = true
	return a.custody.save() == nil
}
func (a *attention) retireNativeGrant(output string, g *nativeAttentionGrant) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.custodyRetireLocked(g) {
		return false
	}
	delete(a.nativeGrants, output)
	return true
}
func (a *attention) custodyFinishLocked() bool {
	if a.custody == nil || a.sessionNonce == "" {
		return true
	}
	s := a.custodySessionLocked()
	if s == nil {
		return false
	}
	if s.Retired {
		return true
	}
	for _, o := range s.Outputs {
		if !o.Retired {
			return false
		}
	}
	s.Retired = true
	return a.custody.save() == nil
}

func validCustodyRelease(o *custodyOutput) bool {
	r := o.Release
	return r != nil && r.Identity.valid() && r.Identity.DeviceID == o.DeviceID && r.Identity.Process == o.Original.Process && r.Identity.Incarnation == o.Original.Incarnation && r.Identity.Lease != "0" && validAttentionNonce(r.RequestID) && nativeCurrency(r.Operation) && r.Operation != "0" && r.Action == "release" && r.Current && (r.Outcome == "protocol_accepted" && r.ResponseCode == 200 && r.CSeq > 0)
}

func validRecoveryObservation(command nativeRecoveryCommand, id string, r nativeRecoveryResult) bool {
	if r.Origin != command.Origin || !r.Identity.valid() || r.Identity.DeviceID != id || r.Identity.Process != command.Expected.Process || r.Identity.Incarnation != command.Expected.Incarnation || r.RequestID != command.RequestID || r.Action != "recover" || !nativeCurrency(r.Operation) || r.Operation == "0" {
		return false
	}
	switch r.Outcome {
	case "pending", "protocol_accepted", "no_effect", "failed", "unknown":
	default:
		return false
	}
	switch r.State {
	case "pending", "restored", "already_retired", "never_admitted", "unknown":
	default:
		return false
	}
	return true
}

func recoveryReceipt(command nativeRecoveryCommand, original nativeAttentionIdentity, id string, r nativeRecoveryResult) bool {
	if r.Origin != command.Origin || !r.Identity.valid() || r.Identity.DeviceID != id || r.Identity.Lease != "0" || r.Identity.Restoring || r.Identity.Process != command.Expected.Process || r.Identity.Incarnation != command.Expected.Incarnation || r.RequestID != command.RequestID || r.Action != "recover" || !nativeCurrency(r.Operation) || r.Operation == "0" || !r.Current || r.Proof == nil || !validAttentionNonce(r.Proof.Database) || !r.Proof.LedgerComplete {
		return false
	}
	switch r.State {
	case "never_admitted":
		return r.Outcome == "no_effect" && r.Proof.Terminal == nil && r.ResponseCode == 0 && r.CSeq == 0
	case "restored", "already_retired":
		terminal := r.Proof.Terminal
		if terminal == nil || !terminal.Identity.valid() || terminal.Identity.DeviceID != id || !validAttentionNonce(terminal.RequestID) || !nativeCurrency(terminal.Operation) || terminal.Operation == "0" || terminal.ResponseCode != 200 || terminal.CSeq <= 0 {
			return false
		}
		if terminal.Action == "release" {
			if terminal.Identity.Process != original.Process || terminal.Identity.Incarnation != original.Incarnation || terminal.Identity.Lease == "0" {
				return false
			}
		} else if terminal.Action != "recover" {
			return false
		}
		if r.State == "already_retired" {
			return r.Outcome == "no_effect" && r.ResponseCode == 0 && r.CSeq == 0
		}
		return r.Outcome == "protocol_accepted" && r.ResponseCode == 200 && r.CSeq > 0 && terminal.Identity == r.Identity && terminal.RequestID == r.RequestID && terminal.Operation == r.Operation && terminal.Action == "recover" && terminal.CSeq == r.CSeq
	default:
		return false
	}
}

// A new native process, or a positively idle fresh device incarnation, can
// import original cleanup custody, never prove physical restoration.
func (a *attention) detectNativeReplacement(base, id string, g *nativeAttentionGrant) {
	a.mu.Lock()
	supported := a.custody != nil
	a.mu.Unlock()
	if !supported {
		return
	}
	var state nativeRecoveryObservation
	if nativeAttentionHTTP(base, id, "", http.MethodGet, nil, &state) != nil || !state.valid() || state.DeviceID != id {
		return
	}
	idle := state.Idle != nil && *state.Idle && state.Lease == "0" && !state.Restoring
	freshIncarnation := idle && state.Incarnation != g.Command.Expected.Incarnation
	ownedDrain := idle && g.Command.Action == "release" && (g.Result.Outcome == "failed" || g.Result.Outcome == "unknown") && state.DrainedOperation == g.Result.Operation && state.DrainedRequestID == g.Command.RequestID
	if state.Process == g.Command.Expected.Process && !freshIncarnation && !ownedDrain {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.custody == nil || a.custody.fault != nil {
		return
	}
	if a.nativeGrants[id] != g {
		return // An old callback cannot import custody over a newer admission.
	}
	s := a.custodySessionLocked()
	if s == nil || s.Outputs[g.Origin.RequestID] == nil {
		a.custody.fault = errors.New("native replacement without original custody")
		return
	}
	o := s.Outputs[g.Origin.RequestID]
	if o.Retired || o.Origin != g.Origin || o.DeviceID != id || o.Base != base || o.Original.Incarnation != g.Command.Expected.Incarnation {
		return
	}
	a.active = false
	a.pendingRelease = true
	a.custody.imported = true
	// Pending API updates cannot retain their old admission after daemon death.
	if a.nativeRevision < ^uint64(0) {
		a.nativeRevision++
	}
}

// Existing bridge is the only HTTP owner. Imported custody cannot route audio,
// renew old attention, or select a baseline; native owns persisted BASE and device.
func (a *attention) reconcileCustody() bool {
	a.mu.Lock()
	c := a.custody
	if c == nil || !a.custodyBlockedLocked() {
		a.mu.Unlock()
		return true
	}
	if c.fault != nil || a.nativeBusy || a.directEffects > 0 {
		a.mu.Unlock()
		return false
	}
	a.nativeBusy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.nativeBusy = false; a.mu.Unlock() }()
	for _, s := range c.Sessions {
		if s.Retired {
			continue
		}
		for _, o := range s.Outputs {
			if o.Retired {
				continue
			}
			var state nativeRecoveryObservation
			if nativeAttentionHTTP(o.Base, o.DeviceID, "", http.MethodGet, nil, &state) != nil || !state.valid() || state.DeviceID != o.DeviceID {
				return false
			}
			// Native process replacement invalidates the fresh attempt, not the durable
			// original obligation. Only recover can act; old BEGIN/UPDATE are never sent.
			fresh := o.Recovery == nil || o.Recovery.Expected.Process != state.Process
			drainKey := state.Process + ":" + state.DrainedOperation + ":" + state.DrainedRequestID
			ownedDrain := false
			if o.Recovery != nil && o.Result != nil && (o.Result.Outcome == "failed" || o.Result.Outcome == "unknown") && validRecoveryObservation(*o.Recovery, o.DeviceID, *o.Result) {
				ownedDrain = state.DrainedOperation == o.Result.Operation && state.DrainedRequestID == o.Recovery.RequestID && !o.ConsumedDrains[drainKey]
				fresh = fresh || state.Incarnation != o.Recovery.Expected.Incarnation || state.Session != o.Recovery.Expected.Session || ownedDrain
			}
			if fresh {
				if state.Lease != "0" || state.Restoring || state.Idle == nil || !*state.Idle {
					return false
				}
				requestID := attentionUUID()
				if requestID == "" {
					return false
				}
				command := nativeRecoveryCommand{nativeAttentionCommand{state.nativeAttentionIdentity, requestID, "recover", 0, 0}, o.Origin}
				a.mu.Lock()
				o.Recovery = &command
				o.Result = nil
				if ownedDrain {
					if o.ConsumedDrains == nil {
						o.ConsumedDrains = map[string]bool{}
					}
					o.ConsumedDrains[drainKey] = true
				}
				err := c.save()
				a.mu.Unlock()
				if err != nil {
					return false
				}
				var result nativeRecoveryResult
				if nativeAttentionHTTP(o.Base, o.DeviceID, "", http.MethodPut, &command, &result) != nil {
					return false
				}
				if !validRecoveryObservation(*o.Recovery, o.DeviceID, result) {
					return false
				}
				a.mu.Lock()
				o.Result = &result
				err = c.save()
				a.mu.Unlock()
				if err != nil {
					return false
				}
			} else if o.Result == nil || o.Result.Outcome == "pending" {
				var result nativeRecoveryResult
				tail := "?process=" + url.QueryEscape(o.Recovery.Expected.Process) + "&request_id=" + url.QueryEscape(o.Recovery.RequestID)
				if nativeAttentionHTTP(o.Base, o.DeviceID, tail, http.MethodGet, nil, &result) != nil {
					return false
				}
				if !validRecoveryObservation(*o.Recovery, o.DeviceID, result) {
					return false
				}
				a.mu.Lock()
				o.Result = &result
				err := c.save()
				a.mu.Unlock()
				if err != nil {
					return false
				}
			}
			if o.Result == nil || !recoveryReceipt(*o.Recovery, o.Original, o.DeviceID, *o.Result) {
				return false
			}
			a.mu.Lock()
			o.Retired = true
			err := c.save()
			a.mu.Unlock()
			if err != nil {
				return false
			}
		}
		a.mu.Lock()
		s.Retired = true
		err := c.save()
		a.mu.Unlock()
		if err != nil {
			return false
		}
	}
	a.mu.Lock()
	c.imported = false
	// Original volatile grants are retired only after all durable child proofs.
	a.nativeGrants = map[string]*nativeAttentionGrant{}
	a.nativePending = false
	a.pendingRelease = false
	a.active = false
	a.mu.Unlock()
	return true
}

// Exact old session release is a query of durable retirement, not a claim in the
// new manager. Its challenge always identifies the actual current manager.
func (a *attention) custodyRelease(nonce string, expected attentionChallenge) (attSnapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.custody == nil || a.custody.fault != nil {
		return attSnapshot{}, false
	}
	for _, s := range a.custody.Sessions {
		if s.Nonce != nonce || s.Process != expected.Process || s.Revision != expected.Revision {
			continue
		}
		if !a.custody.imported && !s.Retired && s.Process == attentionManagerProcess() && s.Nonce == a.sessionNonce {
			return attSnapshot{}, false
		}
		snap := attSnapshot{Challenge: attentionChallenge{attentionManagerProcess(), strconv.FormatUint(a.admissionRevision, 10)}, Contract: "native_attention_v1", Outcome: "pending", NativeResults: map[string]nativeAttentionResult{}, RecoveryResults: map[string]nativeRecoveryResult{}}
		if s.Retired {
			snap.Outcome = "released"
		}
		for key, o := range s.Outputs {
			if o.Result != nil {
				snap.RecoveryResults[key] = *o.Result
			}
		}
		return snap, true
	}
	return attSnapshot{}, false
}

// Commit admission follows all catalog I/O. Cleanup may start only after already
// admitted direct native/config owners finish; no mutex spans HTTP or disk work.
func custodyMutationAdmission(w http.ResponseWriter) (func(), bool) {
	room := primaryRoom()
	if room == nil {
		return func() {}, true
	}
	a := attentionFor(room.ID)
	if a == nil {
		return func() {}, true
	}
	a.mu.Lock()
	if a.custodyBlockedLocked() {
		a.mu.Unlock()
		http.Error(w, "original attention restoration pending", http.StatusConflict)
		return nil, false
	}
	a.directEffects++
	a.mu.Unlock()
	return func() { a.mu.Lock(); a.directEffects--; a.mu.Unlock() }, true
}
func (a *attention) nativeReleaseAccepted(r nativeAttentionResult) bool {
	a.mu.Lock()
	legacy := a.custody == nil
	a.mu.Unlock()
	return r.Current && (r.Outcome == "protocol_accepted" && (legacy || r.ResponseCode == 200 && r.CSeq > 0) || legacy && r.Outcome == "desired_only")
}

func custodyDeleteAdmission(w http.ResponseWriter, id string) (func(), bool) {
	a := attentionFor(id)
	if a == nil {
		return custodyMutationAdmission(w)
	}
	a.mu.Lock()
	pending := a.custodyBlockedLocked() || a.nativeBusy
	if a.custody != nil {
		for _, s := range a.custody.Sessions {
			if !s.Retired {
				pending = true
				break
			}
		}
	}
	if pending {
		a.mu.Unlock()
		http.Error(w, "original attention cleanup pending", http.StatusConflict)
		return nil, false
	}
	a.deleting = true
	a.directEffects++
	a.mu.Unlock()
	return func() { a.mu.Lock(); a.directEffects--; a.deleting = false; a.mu.Unlock() }, true
}
