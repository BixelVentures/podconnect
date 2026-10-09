package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The manager and native daemon each keep their existing owner. A challenge is
// observation, never native lease adoption. Only the bridge executes commands.
var attentionProcessOnce sync.Once
var attentionProcess string

func attentionUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	text := hex.EncodeToString(raw[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}
func attentionManagerProcess() string {
	attentionProcessOnce.Do(func() { attentionProcess = attentionUUID() })
	return attentionProcess
}

type attentionChallenge struct {
	Process  string `json:"process"`
	Revision string `json:"revision"`
}

func (a *attention) challenge() attentionChallenge {
	a.mu.Lock()
	defer a.mu.Unlock()
	return attentionChallenge{attentionManagerProcess(), strconv.FormatUint(a.admissionRevision, 10)}
}
func validAttentionNonce(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (a *attention) engageOwned(nonce string, expected attentionChallenge, begin bool, level int, owner string, ttl time.Duration, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked(now)
	if !validAttentionNonce(nonce) || expected.Process == "" || expected.Process != attentionManagerProcess() || a.nativeRevision == math.MaxUint64 {
		return false
	}
	if begin {
		// Retried lost response: same already-admitted nonce, never a second claim.
		if a.active && nonce == a.sessionNonce {
			return a.admissionRevision > 0 && expected.Revision == strconv.FormatUint(a.admissionRevision-1, 10)
		}
		if expected.Revision != strconv.FormatUint(a.admissionRevision, 10) || a.active || a.pendingRelease || a.nativePending || a.nativeBusy || nonce == a.sessionNonce || a.admissionRevision == math.MaxUint64 {
			return false
		}
		a.admissionRevision++
		a.sessionNonce = nonce
	} else if !a.active || nonce != a.sessionNonce || expected.Revision != strconv.FormatUint(a.admissionRevision, 10) {
		return false
	}
	a.nativeRevision++
	a.active = true
	a.pendingRelease = false
	a.level = clampPct(level)
	a.owner = owner
	a.deadline = now.Add(ttl)
	return true
}
func (a *attention) releaseOwned(nonce string, expected attentionChallenge) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if nonce == "" || nonce != a.sessionNonce || expected.Process != attentionManagerProcess() || expected.Revision != strconv.FormatUint(a.admissionRevision, 10) || a.nativeRevision == math.MaxUint64 || a.restoreRevision == math.MaxUint64 {
		return false
	}
	a.restoreRevision++
	a.nativeRevision++
	a.active = false
	a.pendingRelease = true
	return true
}

type nativeAttentionIdentity struct {
	Process           string `json:"process"`
	DeviceID          string `json:"device_id"`
	Incarnation       string `json:"device_incarnation"`
	Session           string `json:"session_epoch"`
	BaseRevision      string `json:"base_revision"`
	AttentionRevision string `json:"attention_revision"`
	RouteRevision     string `json:"route_revision"`
	Lease             string `json:"lease"`
	Base              int    `json:"base_volume"`
	Effective         int    `json:"effective_volume"`
	Restoring         bool   `json:"restoring"`
}
type nativeAttentionObservation struct {
	nativeAttentionIdentity
	LastRelease string `json:"last_release_operation"`
}
type nativeAttentionCommand struct {
	Expected  nativeAttentionIdentity `json:"expected"`
	RequestID string                  `json:"request_id"`
	Action    string                  `json:"action"`
	Cap       int                     `json:"cap_volume"`
	TTL       int                     `json:"ttl_ms"`
}
type nativeAttentionResult struct {
	Identity     nativeAttentionIdentity `json:"identity"`
	RequestID    string                  `json:"request_id"`
	Operation    string                  `json:"operation"`
	Action       string                  `json:"action"`
	Outcome      string                  `json:"outcome"`
	ResponseCode int                     `json:"response_code"`
	CSeq         int                     `json:"request_cseq"`
	Current      bool                    `json:"current"`
}
type nativeAttentionGrant struct {
	// Original native lease/process/device incarnation never replaced by a GET.
	Identity            nativeAttentionIdentity
	Command             nativeAttentionCommand
	Result              nativeAttentionResult
	Uncertain           bool
	Submitted           bool // One first submission; later observations are GET only.
	Revision            uint64
	LastRestoreRevision uint64
}

func nativeCurrency(v string) bool {
	n, e := strconv.ParseUint(v, 10, 64)
	return e == nil && strconv.FormatUint(n, 10) == v
}
func (s nativeAttentionIdentity) valid() bool {
	return validAttentionNonce(s.Process) && nativeCurrency(s.DeviceID) && nativeCurrency(s.Incarnation) && s.Incarnation != "0" && nativeCurrency(s.Session) && nativeCurrency(s.BaseRevision) && nativeCurrency(s.AttentionRevision) && nativeCurrency(s.RouteRevision) && nativeCurrency(s.Lease) && s.Base >= 0 && s.Base <= 100 && s.Effective >= 0 && s.Effective <= 100
}

type nativeAttentionHTTPStatus int

func (s nativeAttentionHTTPStatus) Error() string {
	return fmt.Sprintf("native attention HTTP %d", int(s))
}
func nativeAttentionHTTP(base, id, tail, method string, command *nativeAttentionCommand, out any) error {
	if !nativeCurrency(id) {
		return errors.New("invalid native output identity")
	}
	var body io.Reader
	if command != nil {
		data, e := json.Marshal(command)
		if e != nil {
			return e
		}
		body = bytes.NewReader(data)
	}
	request, e := http.NewRequest(method, base+"/api/outputs/"+id+"/attention"+tail, body)
	if e != nil {
		return e
	}
	if command != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := (&http.Client{Timeout: 4 * time.Second}).Do(request)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nativeAttentionHTTPStatus(response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 65537))
	if e = decoder.Decode(out); e != nil {
		return e
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		return errors.New("extra native attention response")
	}
	return nil
}
func (g *nativeAttentionGrant) submit(base, id string) error {
	if g.Submitted {
		return errors.New("native command already submitted; observe its original receipt")
	}
	g.Submitted = true
	g.Uncertain = true
	var observed nativeAttentionResult
	if err := nativeAttentionHTTP(base, id, "", http.MethodPut, &g.Command, &observed); err != nil {
		return err
	}
	return g.foldObserved(id, observed)
}
func (g *nativeAttentionGrant) observe(base, id string) error {
	var observed nativeAttentionResult
	tail := "/" + g.Result.Operation + "?process=" + url.QueryEscape(g.Result.Identity.Process)
	if g.Uncertain {
		tail = "?process=" + url.QueryEscape(g.Command.Expected.Process) + "&request_id=" + url.QueryEscape(g.Command.RequestID)
	}
	if err := nativeAttentionHTTP(base, id, tail, http.MethodGet, nil, &observed); err != nil {
		return err
	}
	return g.foldObserved(id, observed)
}
func (g *nativeAttentionGrant) foldObserved(id string, observed nativeAttentionResult) error {
	if !observed.Identity.valid() || observed.Identity.DeviceID != id || observed.RequestID != g.Command.RequestID || observed.Action != g.Command.Action || !nativeCurrency(observed.Operation) || observed.Operation == "0" {
		return errors.New("native command receipt identity mismatch")
	}
	if g.Identity.Lease != "" && (observed.Identity.Process != g.Identity.Process || observed.Identity.Incarnation != g.Identity.Incarnation || observed.Identity.Lease != g.Identity.Lease) {
		return errors.New("native lease receipt replacement refused")
	}
	if g.Command.Action == "manual" {
		before := g.Command.Expected
		revision, err := strconv.ParseUint(before.BaseRevision, 10, 64)
		if err != nil || revision >= math.MaxUint64-1 || observed.Identity.BaseRevision != strconv.FormatUint(revision+1, 10) || observed.Identity.Base != g.Command.Cap || observed.Identity.Session != before.Session || observed.Identity.RouteRevision != before.RouteRevision || observed.Identity.AttentionRevision != before.AttentionRevision || observed.Identity.Restoring != before.Restoring {
			return errors.New("native manual CAS receipt mismatch")
		}
	}
	switch observed.Outcome {
	case "pending", "desired_only", "protocol_accepted", "failed", "unknown":
	default:
		return errors.New("unknown native outcome")
	}
	if g.Result.Outcome != "" && g.Result.Outcome != "pending" && observed.Outcome != g.Result.Outcome {
		return errors.New("native terminal outcome changed")
	}
	g.Result = observed
	g.Uncertain = false
	if g.Identity.Lease == "" {
		g.Identity = observed.Identity
	}
	return nil
}
func (a *attention) submitNative(base, id string, g *nativeAttentionGrant) error {
	err := g.submit(base, id)
	if err == nil {
		a.mu.Lock()
		if a.nativeResults == nil {
			a.nativeResults = map[string]nativeAttentionResult{}
		}
		a.nativeResults[id] = g.Result
		a.mu.Unlock()
	}
	return err
}
func (a *attention) observeNative(base, id string, g *nativeAttentionGrant) error {
	err := g.observe(base, id)
	if err == nil {
		a.mu.Lock()
		if a.nativeResults == nil {
			a.nativeResults = map[string]nativeAttentionResult{}
		}
		a.nativeResults[id] = g.Result
		a.mu.Unlock()
	}
	return err
}
func (a *attention) nativeTarget(base, id string) bool {
	return a.nativeTargetForRoute(base, id, nil)
}
func (a *attention) nativeTargetForRoute(base, id string, admitRoute func() bool) bool {
	return a.nativeReconcileWithAdmission(base, id, true, admitRoute)
}
func (a *attention) nativeReconcile(base, id string, targetOnly bool) bool {
	return a.nativeReconcileWithAdmission(base, id, targetOnly, nil)
}
func (a *attention) nativeReconcileWithAdmission(base, id string, targetOnly bool, admitRoute func() bool) bool {
	a.mu.Lock()
	a.expireLocked(time.Now())
	nonce, rev, restoreRev, active, level := a.sessionNonce, a.nativeRevision, a.restoreRevision, a.active, a.level
	if a.nativeBusy {
		a.mu.Unlock()
		return false
	}
	a.nativeBusy = true
	if a.nativeGrants == nil {
		a.nativeGrants = map[string]*nativeAttentionGrant{}
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.nativeBusy = false
		a.nativePending = len(a.nativeGrants) > 0
		if !a.active && !a.nativePending {
			a.pendingRelease = false
		}
		a.mu.Unlock()
	}()
	if nonce == "" {
		return !active
	} // Legacy request is desired-only, never native apply.
	// Poll exact pending/lost receipts; each terminal failure remains immutable.
	for output, g := range a.nativeGrants {
		if g.Uncertain || g.Result.Outcome == "pending" {
			if a.observeNative(base, output, g) != nil {
				return false
			}
		}
	}
	if active && id != "" {
		g := a.nativeGrants[id]
		if g == nil {
			var state nativeAttentionObservation
			if nativeAttentionHTTP(base, id, "", http.MethodGet, nil, &state) != nil || !state.valid() || state.DeviceID != id || state.Lease != "0" {
				return false
			}
			// Observation may outlive the prepared room/alias. Claim this native
			// effect before reserving its lease, without holding attention locks.
			if admitRoute != nil && !admitRoute() {
				return false
			}
			a.mu.Lock()
			now := time.Now()
			a.expireLocked(now)
			current := a.active && a.sessionNonce == nonce && a.nativeRevision == rev && a.deadline.After(now)
			remaining := a.deadline.Sub(now)
			if !current || remaining/time.Millisecond <= 0 {
				a.mu.Unlock()
				return false // ttl_ms=0 means default renewal, never residual permission.
			}
			commandID := attentionUUID()
			if commandID == "" {
				a.mu.Unlock()
				return false
			}
			g = &nativeAttentionGrant{Revision: rev, Uncertain: true}
			g.Command = nativeAttentionCommand{state.nativeAttentionIdentity, commandID, "begin", level, int(clampAttentionTTL(remaining) / time.Millisecond)}
			// Reserve cleanup custody and exact residual TTL in the same admission.
			a.nativeGrants[id] = g
			a.nativePending = true
			a.mu.Unlock()
			if a.submitNative(base, id, g) != nil {
				return false
			}
		} else if g.Result.Outcome != "pending" && g.Revision != rev && !g.Identity.Restoring {
			var state nativeAttentionObservation
			if nativeAttentionHTTP(base, id, "", http.MethodGet, nil, &state) != nil || !state.valid() || state.Process != g.Identity.Process || state.Incarnation != g.Identity.Incarnation || state.Lease != g.Identity.Lease || state.Restoring {
				return false
			}
			// Claim and replace only while the same conversation/update is still current.
			// An old state GET has no authority to mint an UPDATE after release/retarget.
			// Observation may outlive the prepared room/alias. Claim this native
			// effect before reserving its lease, without holding attention locks.
			if admitRoute != nil && !admitRoute() {
				return false
			}
			a.mu.Lock()
			now := time.Now()
			a.expireLocked(now)
			current := a.active && a.sessionNonce == nonce && a.nativeRevision == rev && a.deadline.After(now)
			remaining := a.deadline.Sub(now)
			if !current || remaining/time.Millisecond <= 0 {
				a.mu.Unlock()
				return false // Refusal preserves the exact original grant and receipt.
			}
			commandID := attentionUUID()
			if commandID == "" {
				a.mu.Unlock()
				return false
			}
			g.Command = nativeAttentionCommand{state.nativeAttentionIdentity, commandID, "update", level, int(clampAttentionTTL(remaining) / time.Millisecond)}
			g.Result = nativeAttentionResult{}
			g.Uncertain = true
			g.Submitted = false
			g.Revision = rev
			a.mu.Unlock()
			if a.submitNative(base, id, g) != nil {
				return false
			}
		}
		if g.Uncertain || !g.Result.Current || (g.Result.Outcome != "protocol_accepted" && g.Result.Outcome != "desired_only") {
			return false
		}
	}
	if targetOnly {
		return true
	}
	// A handoff restores every ORIGINAL lease except the actual selected output.
	for output, g := range a.nativeGrants {
		if active && output == id {
			continue
		}
		if g.Uncertain || g.Result.Outcome == "pending" {
			return false
		}
		if g.Command.Action == "release" && (g.Result.Outcome == "protocol_accepted" || g.Result.Outcome == "desired_only") && g.Result.Current {
			delete(a.nativeGrants, output)
			continue
		}
		var state nativeAttentionObservation
		if nativeAttentionHTTP(base, output, "", http.MethodGet, nil, &state) != nil || !state.valid() || state.Process != g.Identity.Process || state.Incarnation != g.Identity.Incarnation {
			return false
		}
		if state.Lease == "0" {
			// Native TTL may have restored this exact lease while manager was blocked.
			var terminal nativeAttentionResult
			if !nativeCurrency(state.LastRelease) || state.LastRelease == "0" || nativeAttentionHTTP(base, output, "/"+state.LastRelease+"?process="+url.QueryEscape(g.Identity.Process), http.MethodGet, nil, &terminal) != nil || !terminal.Identity.valid() || terminal.Identity.Process != g.Identity.Process || terminal.Identity.DeviceID != output || terminal.Operation != state.LastRelease || terminal.Identity.Lease != g.Identity.Lease || terminal.Identity.Incarnation != g.Identity.Incarnation || terminal.Action != "release" || !terminal.Current || (terminal.Outcome != "protocol_accepted" && terminal.Outcome != "desired_only") {
				return false
			}
			a.mu.Lock()
			a.nativeResults[output] = terminal
			a.mu.Unlock()
			delete(a.nativeGrants, output)
			continue
		}
		if state.Lease != g.Identity.Lease {
			return false
		}
		// Retrying FAILED/UNKNOWN needs a new explicit retained-owner release or
		// changed manual/session input. Same failed command is never blindly resent.
		if g.Command.Action == "release" && g.LastRestoreRevision == restoreRev && state.BaseRevision == g.Command.Expected.BaseRevision && state.Session == g.Command.Expected.Session {
			return false
		}
		commandID := attentionUUID()
		if commandID == "" {
			return false
		}
		g.Command = nativeAttentionCommand{state.nativeAttentionIdentity, commandID, "release", 0, 0}
		g.Result = nativeAttentionResult{}
		g.Uncertain = true
		g.Submitted = false
		g.LastRestoreRevision = restoreRev
		if a.submitNative(base, output, g) != nil {
			return false
		}
		if g.Result.Current && (g.Result.Outcome == "protocol_accepted" || g.Result.Outcome == "desired_only") {
			delete(a.nativeGrants, output)
		} else {
			return false
		}
	}
	return true
}
