package main

import (
	"math"
	"os"
	"time"
)

// This new one-shot intent may shorten recovery eligibility, never extend any
// existing transport proof/lease. A reconnect/poll cannot create it.
const connectRecoveryWindow = 5 * time.Second

type connectRecoveryIntent struct {
	transport                 glTransportIntent
	run, phase, aliasRevision uint64
	alias                     int
	binding                   *aliasBinding
	until                     time.Time
}

func (l *glLive) acceptConnectRecoveryLocked(data map[string]any) {
	id, ok := numField(data, "id")
	ordinal, valid := numField(data, "ordinal")
	binding, present := eventAliasBinding(data)
	if !ok || !valid || !present || id < 1 || id != math.Trunc(id) || id > float64(^uint32(0)) ||
		ordinal < 1 || ordinal > 9007199254740991 || ordinal != math.Trunc(ordinal) ||
		l.st.SelAlias != int(id) || binding == nil || !binding.valid() || !sameAliasBinding(binding, l.st.AliasBinding) {
		return
	}
	if l.connectRecoverySeen == nil {
		l.connectRecoverySeen = make(map[string]uint64)
	}
	if uint64(ordinal) <= l.connectRecoverySeen[binding.Incarnation] || (l.connectRecoverySeen[binding.Incarnation] == 0 && len(l.connectRecoverySeen) >= 32) {
		return
	}
	l.connectRecoverySeen[binding.Incarnation] = uint64(ordinal)
	l.connectRecovery = &connectRecoveryIntent{run: l.runEpoch, phase: l.phaseEpoch, aliasRevision: l.aliasRevision, alias: int(id), binding: copyAliasBinding(binding), until: time.Now().Add(connectRecoveryWindow)}
}

func (l *glLive) consumeConnectRecovery() (*connectRecoveryIntent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	c := l.connectRecovery
	if c == nil {
		return nil, false
	}
	// Expiry/changed source or alias consumes stale work, never revives it.
	if l.sourceRetired || c.run != l.runEpoch || c.phase != l.phaseEpoch || c.aliasRevision != l.aliasRevision ||
		c.alias != l.st.SelAlias || !sameAliasBinding(c.binding, l.st.AliasBinding) || !time.Now().Before(c.until) {
		l.connectRecovery = nil
		return nil, false
	}
	if !l.st.Active || l.st.Paused || l.st.Stopped {
		return nil, false
	}
	c.transport = glTransportIntent{l.runEpoch, l.phaseEpoch, l.aliasRevision, l.transportRevision, l.st.SelAlias}
	l.connectRecovery = nil // Consume before any catalogue read, prime or route send.
	return c, true
}

func tryExplicitConnectRecovery(room *Room, live *glLive, att *attention) bool {
	if room == nil || room.Idx != 0 || !fileExists(releasedPath(room)) {
		return false
	}
	credit, ok := live.consumeConnectRecovery()
	if !ok {
		return false
	}
	// Do not cross local attention's pending/unknown owner with desired-state calls.
	if att != nil {
		att.mu.Lock()
		blocked := att.active || att.nativeBusy || att.nativePending || att.pendingRelease || att.custodyBlockedLocked() || len(att.nativeGrants) > 0
		att.mu.Unlock()
		if blocked {
			return true
		}
	}
	intent := credit.transport
	guard := func() bool {
		return time.Now().Before(credit.until) && live.startIntentCurrent(intent) && fileExists(releasedPath(room))
	}
	accepted, current := routeAliasOutputWithGuard(room.OwnTone, credit.alias, live, credit.aliasRevision, nil, guard, true, true)
	if accepted && current && guard() && routeIntentCurrent(live, credit.alias, credit.aliasRevision) {
		// Every C volume/selection command independently rejected busy/unknown START.
		// A failure (including timeout) leaves release intact and cannot retry this credit.
		_ = os.Remove(releasedPath(room))
	}
	return true
}
