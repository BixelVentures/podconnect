package main

import (
	"context"
	"errors"
)

const primaryRadioProtocol = "primary"

var errRadioPrimaryUnsupported = errors.New("native primary radio protocol unsupported")
var errRadioPrimaryAdvertised = errors.New("native primary radio endpoint advertised")

// A primary advertisement is sticky throughout startup. Losing that endpoint
// cannot switch custody to the separate legacy sender. A legacy Native process
// may still use its existing readiness check after an explicit endpoint 404.
func awaitConfiguredRadioBootstrap(ctx context.Context, o radioProducerBootstrapOptions, pause radioReadinessPause) (*radioProducerNativeClient, bool, error) {
	var selected *radioProducerNativeClient
	var primary, advertised bool
	legacyProbe := radioLegacyBootstrapProbe()
	err := waitRadioPassiveReadiness(ctx, func(readCtx context.Context) (bool, bool, error) {
		n, err := newRadioProducerNativeClient(o.NativeURL, o.NativeToken)
		if err != nil {
			return false, false, err
		}
		state, err := n.primaryProtocolState(readCtx)
		advertised = advertised || errors.Is(err, errRadioPrimaryAdvertised)
		olderC := err == nil && state.Schema == primaryRadioSchema && state.Protocol == "v1" &&
			producerHex(state.Process, 64) && producerCounter(state.Revision, true) &&
			state.Observation == nil && state.Session == nil
		if !advertised && (errors.Is(err, errRadioPrimaryUnsupported) || olderC) {
			expectedProcess := ""
			if olderC {
				expectedProcess = state.Process
			}
			ready, readErr := legacyProbe(readCtx, n, expectedProcess)
			if ready {
				selected = n
			}
			return ready, !ready, readErr
		}
		if err != nil {
			return false, true, err
		}
		advertised = true
		// Standard worker activation proves runtime readiness only. Keep
		// physical acceptance separate, as for the legacy worker backend.
		if !radioWorkerConfigured(o) && !o.Physical {
			return false, false, errRadioUnavailable
		}
		if !primaryBootstrapStateReady(state) {
			return false, true, errRadioUnavailable
		}
		n.primaryBootstrapProcess = state.Process
		selected, primary = n, true
		return true, false, nil
	}, pause)
	return selected, primary, err
}

// Native owns this fresh proof; config IDs and an available status alone can
// never stand in for a receiver/source observation or legacy pairing material.
func primaryBootstrapStateReady(s radioPrimaryState) bool {
	if s.Schema != primaryRadioSchema || s.Protocol != primaryRadioProtocol || !producerHex(s.Process, 64) || !producerCounter(s.Revision, true) ||
		s.Missing == nil || len(s.Missing) != 0 || s.Session != nil || s.IsolatedSession != nil || s.Observation == nil ||
		!validRadioObservation(*s.Observation) || s.Observation.StationID != "" {
		return false
	}
	switch s.Observation.SourceKind {
	case "none":
		if s.Observation.State != "idle" && s.Observation.State != "stopped" {
			return false
		}
	case "spotify":
		if (s.Observation.State != "playing" && s.Observation.State != "stopped") || s.Observation.TargetID == "" {
			return false
		}
	default:
		return false // No implicit adoption of already live radio or unknown custody.
	}
	ids, devices := map[string]bool{}, map[string]bool{}
	available, observedTarget := false, false
	for _, t := range s.Targets {
		if t.ID == "" || !producerCounter(t.HomePodID, false) || ids[t.ID] || devices[t.HomePodID] || t.ReceiverDigest != "" {
			return false
		}
		ids[t.ID], devices[t.HomePodID] = true, true
		available = available || t.Selectable && t.Availability == "available"
		observedTarget = observedTarget || t.ID == s.Observation.TargetID
	}
	return available && (s.Observation.TargetID == "" || observedTarget)
}
