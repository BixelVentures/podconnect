package main

// Concrete transport for the native radio-owner protocol. This is a NEW protocol;
// current receiver binaries do not implement it. There is intentionally no
// compatibility fallback to generic OwnTone/Spotify endpoints.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var radioStreamURLs = map[string]string{"p3": "https://live-icy.dr.dk/A/A05H.mp3", "p1": "https://live-icy.dr.dk/A/A03H.mp3", "p4-ostjylland": "https://live-icy.dr.dk/A/A14H.mp3", "p6-beat": "https://live-icy.dr.dk/A/A29H.mp3", "p8-jazz": "https://live-icy.dr.dk/A/A22H.mp3"}
var radioRequiredProofs = []string{"independent_sender_identity_and_pairing", "native_all_effect_path_fences", "exact_spotify_pause_observation_and_receiver_release", "external_spotify_start_event_including_same_track", "durable_unknown_effect_custody"}

type radioNative struct {
	base, token, process string
	client               *http.Client
	physicalAccepted     bool
}
type radioNativeReply struct {
	OwnerGeneration uint64                  `json:"owner_generation"`
	Terminal        bool                    `json:"terminal"`
	ActionID        string                  `json:"action_id"`
	Outcome         string                  `json:"outcome"`
	Observation     *radioNativeObservation `json:"observation,omitempty"`
	Schema          string                  `json:"schema"`
	Process         string                  `json:"process_id"`
	OK              bool                    `json:"ok"`
	Proofs          []string                `json:"proofs"`
	Targets         []radioTarget           `json:"targets"`
	Token           string                  `json:"token"`
	Generation      uint64                  `json:"generation"`
	Observed        bool                    `json:"observed"`
}

func newRadioNative(base, token string, physical bool) (*radioNative, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || token == "" {
		return nil, errRadioUnavailable
	}
	return &radioNative{base: base, token: token, physicalAccepted: physical, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("radio redirect refused") }}}, nil
}
func (n *radioNative) rpc(ctx context.Context, path string, args any) (radioNativeReply, error) {
	var reply radioNativeReply
	body, e := json.Marshal(args)
	if e != nil {
		return reply, e
	}
	method := http.MethodPost
	if args == nil {
		method = http.MethodGet
		body = nil
	}
	req, e := http.NewRequestWithContext(ctx, method, n.base+"/api/radio-owner/"+path, bytes.NewReader(body))
	if e != nil {
		return reply, e
	}
	req.Header.Set("X-PodConnect-Token", n.token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := n.client.Do(req)
	if e != nil {
		return reply, errRadioUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return reply, errRadioUnknown
	}
	// Check the complete bounded body before decoding: decoding through a
	// LimitReader alone can accept a valid prefix followed by excess whitespace
	// and silently ignore an invalid or oversized suffix beyond the limit.
	payload, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(payload) > 65536 {
		return reply, errRadioUnknown
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&reply) != nil || d.Decode(new(any)) != io.EOF || reply.Schema != "podconnect.radio-owner.v1" || reply.Process == "" || !reply.OK {
		return reply, errRadioUnknown
	}
	if n.process != "" && reply.Process != n.process {
		return reply, errRadioUnknown
	}
	return reply, nil
}
func (n *radioNative) Ready(ctx context.Context) ([]radioTarget, []string, error) {
	r, e := n.rpc(ctx, "capabilities", nil)
	if e != nil {
		return nil, nil, e
	}
	var missing []string
	for _, required := range radioRequiredProofs {
		found := false
		for _, p := range r.Proofs {
			found = found || p == required
		}
		if !found {
			missing = append(missing, required)
		}
	}
	if !n.physicalAccepted {
		missing = append(missing, "physical_homepod_acceptance")
	}
	if len(missing) == 0 {
		n.process = r.Process
	}
	return r.Targets, missing, nil
}
func (n *radioNative) Preflight(ctx context.Context, station, target string) error {
	stream, ok := radioStreamURLs[station]
	if !ok || target == "" {
		return errRadioUnavailable
	}
	r, e := n.rpc(ctx, "preflight", map[string]any{"process_id": n.process, "station_id": station, "stream_url": stream, "target_id": target})
	if e == nil && !r.Observed {
		return errRadioUnavailable
	}
	return e
}
func (n *radioNative) Reserve(ctx context.Context, from, to, actionID string, g uint64) (radioFence, error) {
	r, e := n.rpc(ctx, "reserve", map[string]any{"process_id": n.process, "source_target_id": from, "action_id": actionID, "target_id": to, "generation": g})
	if e != nil || r.Token == "" || r.Generation != g || r.OwnerGeneration == 0 {
		return radioFence{ActionID: actionID, Generation: g}, errRadioUnknown
	}
	return radioFence{Token: r.Token, ActionID: actionID, Generation: g, OwnerGeneration: r.OwnerGeneration}, nil
}
func (n *radioNative) effect(ctx context.Context, path string, f radioFence, args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	args["process_id"] = n.process
	args["token"] = f.Token
	args["generation"] = f.Generation
	r, e := n.rpc(ctx, path, args)
	if e != nil {
		return e
	}
	if !r.Observed || r.Token != f.Token || r.Generation != f.Generation {
		return errRadioUnknown
	}
	return nil
}
func (n *radioNative) PauseAndRelease(ctx context.Context, f radioFence, t string) error {
	return n.effect(ctx, "pause-release", f, map[string]any{"target_id": t})
}
func (n *radioNative) Start(ctx context.Context, f radioFence, s, t string) error {
	r, e := n.rpc(ctx, "start", map[string]any{"process_id": n.process, "token": f.Token, "generation": f.Generation, "action_id": f.ActionID, "station_id": s, "target_id": t})
	if e != nil || !r.Observed || r.Token != f.Token || r.Generation != f.Generation || r.Observation == nil {
		return errRadioUnknown
	}
	o := *r.Observation
	if !validRadioObservation(o) || o.State != "playing" || o.SourceKind != "radio" || o.StationID != s || o.TargetID != t || o.OwnerGeneration != f.OwnerGeneration {
		return errRadioUnknown
	}
	return nil
}
func (n *radioNative) Stop(ctx context.Context, f radioFence, t string) error {
	return n.effect(ctx, "stop", f, map[string]any{"target_id": t})
}
func (n *radioNative) Volume(ctx context.Context, f radioFence, t string, v int) error {
	return n.effect(ctx, "volume", f, map[string]any{"target_id": t, "volume_percent": v})
}
func (n *radioNative) Finish(ctx context.Context, f radioFence) (radioNativeObservation, error) {
	r, e := n.rpc(ctx, "finish", map[string]any{"process_id": n.process, "token": f.Token, "generation": f.Generation, "action_id": f.ActionID})
	if e != nil || !r.Terminal || !r.Observed || r.Token != f.Token || r.Generation != f.Generation || r.ActionID != f.ActionID || r.Observation == nil || !validRadioObservation(*r.Observation) {
		return radioNativeObservation{}, errRadioUnknown
	}
	return *r.Observation, nil
}

// Configuration never launches or restarts the existing Spotify processes. An
// enabled request still cannot pass without the native proof and physical gates.
func configuredRadioOwner() *radioCoordinator {
	c := newRadioCoordinator(nil)
	b, e := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if e != nil {
		return c
	}
	var options struct {
		Enabled  bool   `json:"radio_enabled"`
		Mode     string `json:"radio_owner_mode"`
		URL      string `json:"radio_native_url"`
		Token    string `json:"attention_token"`
		Physical bool   `json:"radio_physical_accepted"`
	}
	if json.Unmarshal(b, &options) != nil || !options.Enabled {
		return c
	}
	if options.Mode == "builtin" {
		// Its explicit startup worker runs after the existing primary engine.
		// Passive reads never launch or configure an independent sender.
		return c
	}
	if options.Mode != "" && options.Mode != "external" {
		return c
	}
	n, e := newRadioNative(strings.TrimRight(options.URL, "/"), options.Token, options.Physical)
	if e != nil {
		return c
	}
	c.backend = n
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = c.enable(ctx); e != nil {
		c.state.Error = "Native radio safety proof unavailable"
	}
	return c
}

// Observations are produced by native custody after receiver acknowledgement.
// Manager read time never replaces source observation time.
type radioNativeObservation struct {
	State           string `json:"state"`
	StationID       string `json:"station_id"`
	TargetID        string `json:"target_id"`
	SourceKind      string `json:"source_kind"`
	OwnerGeneration uint64 `json:"owner_generation"`
	ObservedAt      int64  `json:"observed_at"`
	ValidUntil      int64  `json:"valid_until"`
	VolumePercent   *int   `json:"volume_percent"`
}

func (n *radioNative) Observe(ctx context.Context) (radioNativeObservation, error) {
	r, e := n.rpc(ctx, "state", nil)
	if e != nil || r.Observation == nil {
		return radioNativeObservation{}, errRadioUnknown
	}
	o := *r.Observation
	if !validRadioObservation(o) {
		return radioNativeObservation{}, errRadioUnknown
	}
	return o, nil
}
func validRadioObservation(o radioNativeObservation) bool {
	now := time.Now().Unix()
	if o.ObservedAt <= 0 || o.ObservedAt > now || o.ValidUntil <= now || o.ValidUntil < o.ObservedAt || (o.SourceKind != "radio" && o.SourceKind != "spotify" && o.SourceKind != "none" && o.SourceKind != "unknown") || (o.State != "playing" && o.State != "stopped" && o.State != "unknown" && o.State != "idle") {
		return false
	}
	if o.SourceKind == "radio" {
		if _, ok := radioStreamURLs[o.StationID]; !ok || o.TargetID == "" {
			return false
		}
	}
	return o.VolumePercent == nil || (*o.VolumePercent >= 0 && *o.VolumePercent <= 100)
}
func applyRadioObservation(s *radioState, o radioNativeObservation) {
	s.State = o.State
	s.SourceKind = o.SourceKind
	s.OwnerGeneration = o.OwnerGeneration
	s.StationID = o.StationID
	s.TargetID = o.TargetID
	s.ObservedAt = &o.ObservedAt
	s.ValidUntil = &o.ValidUntil
	retrieved := time.Now().Unix()
	s.RetrievedAt = &retrieved
	s.VolumePercent = o.VolumePercent
}

type radioReconciliation struct {
	ActionID    string
	Generation  uint64
	Token       string
	Terminal    bool
	Outcome     string
	Observation radioNativeObservation
}

func (n *radioNative) Reconcile(ctx context.Context, f radioFence) (radioReconciliation, error) {
	path := "result?action_id=" + url.QueryEscape(f.ActionID) + "&generation=" + fmt.Sprint(f.Generation)
	r, e := n.rpc(ctx, path, nil)
	if e != nil || !r.Terminal || r.ActionID != f.ActionID || r.Generation != f.Generation || (f.Token != "" && r.Token != f.Token) || r.Observation == nil || !validRadioObservation(*r.Observation) || (r.Outcome != "committed" && r.Outcome != "rejected" && r.Outcome != "preempted") {
		return radioReconciliation{}, errRadioUnknown
	}
	return radioReconciliation{r.ActionID, r.Generation, r.Token, r.Terminal, r.Outcome, *r.Observation}, nil
}

func (c *radioCoordinator) refresh(ctx context.Context) {
	c.mu.Lock()
	g := c.state.Generation
	allowed := c.state.Enabled && !c.pending
	unknown := c.state.State == "unknown"
	f := c.fence
	operation := c.operation
	b := c.backend
	c.mu.Unlock()
	if !allowed {
		return
	}
	if targets, ok := b.(interface {
		CurrentTargets(context.Context) ([]radioTarget, error)
	}); ok {
		fresh, err := targets.CurrentTargets(ctx)
		if !errors.Is(err, errRadioTargetRefreshUnsupported) {
			c.mu.Lock()
			if !c.pending && c.state.Generation == g {
				if err != nil || !sameRadioTargetIDs(c.state.Targets, fresh) {
					fresh = closedRadioTargets(c.state.Targets)
				}
				if !sameRadioTargets(c.state.Targets, fresh) {
					c.state.Targets = append([]radioTarget{}, fresh...)
					c.state.Revision++ // Target metadata never invents a source epoch.
				}
			}
			c.mu.Unlock()
		}
	}
	var o radioNativeObservation
	if unknown {
		var isolationConfirmed bool
		if isolated, ok := b.(interface {
			observePrimaryIsolation(context.Context, radioFence) (radioNativeObservation, error)
		}); ok {
			var err error
			o, err = isolated.observePrimaryIsolation(ctx, f)
			isolationConfirmed = err == nil
		}
		if !isolationConfirmed {
			reconciler, ok := b.(interface {
				Reconcile(context.Context, radioFence) (radioReconciliation, error)
			})
			if !ok {
				return
			}
			r, e := reconciler.Reconcile(ctx, f)
			if e != nil || !validRadioReconciliation(r, f, operation) {
				return
			}
			o = r.Observation
		}
	} else {
		observer, ok := b.(interface {
			Observe(context.Context) (radioNativeObservation, error)
		})
		if !ok {
			return
		}
		var e error
		o, e = observer.Observe(ctx)
		if e != nil {
			// A failed read cannot keep offering controls against an allegedly
			// playing radio source. Preserve identity/fence for passive result
			// reconciliation, but immediately withdraw its effect authority.
			// Ordinary Spotify and an honestly empty radio source remain with
			// their existing owners and last-known presentation.
			c.mu.Lock()
			if !c.pending && c.state.Generation == g && c.state.SourceKind == "radio" && c.state.State != "unknown" {
				c.state.State = "unknown"
				c.state.Error = "Radio source could not be verified"
				c.state.ObservedAt = nil
				c.state.ValidUntil = nil
				c.state.VolumePercent = nil
				c.state.Generation++
				c.state.Revision++
			}
			c.mu.Unlock()
			return
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending || c.state.Generation != g || (!unknown && (o.OwnerGeneration < c.state.OwnerGeneration || (o.OwnerGeneration == c.state.OwnerGeneration && c.state.ObservedAt != nil && o.ObservedAt <= *c.state.ObservedAt))) {
		return
	}
	if unknown || o.State != c.state.State || o.StationID != c.state.StationID || o.TargetID != c.state.TargetID || o.OwnerGeneration != c.state.OwnerGeneration || o.SourceKind != c.state.SourceKind {
		c.state.Revision++
		c.state.Generation++
	}
	applyRadioObservation(&c.state, o)
	if unknown {
		c.state.Error = ""
	}
}
