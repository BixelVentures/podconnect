package main

import (
	"context"
	"math"
	"os"
	"time"
)

// This new one-shot intent may shorten recovery eligibility, never extend any
// existing transport proof/lease. A reconnect/poll cannot create it.
const connectRecoveryWindow = 5 * time.Second

type connectRecoveryIntent struct {
	startOwned                  bool
	loadOrdinal                 uint64
	resumeOrdinal               uint64
	awaitResume                 bool
	awaitInitial, initialPaused bool
	transport                   glTransportIntent
	run, phase, aliasRevision   uint64
	ordinal                     uint64
	alias                       int
	binding                     *aliasBinding
	until                       time.Time
}

func (l *glLive) acceptConnectRecoveryLocked(data map[string]any) {
	id, ok := numField(data, "id")
	ordinal, valid := numField(data, "ordinal")
	binding, present := eventAliasBinding(data)
	if !ok || !valid || !present || id < 1 || id != math.Trunc(id) || id > float64(^uint32(0)) ||
		ordinal < 1 || ordinal > 9007199254740991 || ordinal != math.Trunc(ordinal) ||
		l.st.SelAlias != int(id) || binding == nil || !binding.valid() || !sameAliasBinding(binding, l.st.AliasBinding) {
		log.Printf("connect-recovery manager reason=event_rejected")
		return
	}
	kind, kindPresent := data["kind"]
	if kindPresent && kind != "resume" {
		log.Printf("connect-recovery manager reason=event_rejected")
		return
	}
	resumeOrdinal, resumePresent := numField(data, "resume_ordinal")
	_, resumeField := data["resume_ordinal"]
	primaryLoad, primaryPresent := numField(data, "primary_load_ordinal")
	_, primaryField := data["primary_load_ordinal"]
	if kind == "resume" && (!resumePresent || resumeOrdinal != ordinal || !primaryPresent ||
		primaryLoad < 1 || primaryLoad > 9007199254740991 || primaryLoad != math.Trunc(primaryLoad)) {
		log.Printf("connect-recovery manager reason=event_rejected")
		return
	}
	if kind != "resume" && (resumeField || primaryField) {
		log.Printf("connect-recovery manager reason=event_rejected")
		return
	}
	loadOrdinal, loadPresent := numField(data, "initial_load_ordinal")
	_, loadField := data["initial_load_ordinal"]
	initialPaused, pausedPresent := data["initial_paused"].(bool)
	if kind == "resume" && loadField || loadField && (!loadPresent || loadOrdinal < 1 || loadOrdinal > 9007199254740991 || loadOrdinal != math.Trunc(loadOrdinal) || !pausedPresent) {
		log.Printf("connect-recovery manager reason=event_rejected")
		return
	}
	if l.connectRecoverySeen == nil {
		l.connectRecoverySeen = make(map[string]uint64)
	}
	if uint64(ordinal) <= l.connectRecoverySeen[binding.Incarnation] || (l.connectRecoverySeen[binding.Incarnation] == 0 && len(l.connectRecoverySeen) >= 32) {
		log.Printf("connect-recovery manager reason=replay_or_capacity")
		return
	}
	l.connectRecoverySeen[binding.Incarnation] = uint64(ordinal)
	l.connectRecovery = &connectRecoveryIntent{run: l.runEpoch, phase: l.phaseEpoch, aliasRevision: l.aliasRevision, alias: int(id), ordinal: uint64(ordinal), binding: copyAliasBinding(binding), until: time.Now().Add(connectRecoveryWindow)}
	if kind == "resume" {
		l.connectRecovery.loadOrdinal = uint64(primaryLoad)
		l.connectRecovery.resumeOrdinal = uint64(resumeOrdinal)
		l.connectRecovery.awaitResume = true
		l.st.Paused = false
		l.wireRevision[0]++
		l.transportRevision++
	} else if loadPresent {
		l.connectRecovery.loadOrdinal = uint64(loadOrdinal)
		l.connectRecovery.awaitInitial = true
		l.connectRecovery.initialPaused = initialPaused
		l.st.Paused = initialPaused
		l.wireRevision[0]++
		l.transportRevision++
	}
	// This accepted transfer owns the following released bridge ticks too.
	// Spending/expiring the credit cannot turn a later status poll into authority.
	l.connectRecoveryFence = l.connectRecovery
	log.Printf("connect-recovery manager reason=event_accepted")
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
		log.Printf("connect-recovery manager reason=stale_intent")
		l.connectRecovery = nil
		return nil, false
	}
	if c.awaitInitial || c.awaitResume || !l.st.Active || l.st.Paused || l.st.Stopped {
		return nil, false
	}
	current := glTransportIntent{l.runEpoch, l.phaseEpoch, l.aliasRevision, l.transportRevision, l.st.SelAlias}
	if c.transport.run != 0 && c.transport != current {
		l.connectRecovery = nil
		return nil, false
	}
	c.transport = current
	l.connectRecovery = nil // Consume before any catalogue read, prime or route send.
	log.Printf("connect-recovery manager reason=intent_consumed")
	return c, true
}

// A typed pre-effect refusal may keep only this same unexpired transfer credit.
// No event, deadline or transport owner is reconstructed from a later poll.
func (l *glLive) retainConnectRecovery(c *connectRecoveryIntent) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	current := glTransportIntent{l.runEpoch, l.phaseEpoch, l.aliasRevision, l.transportRevision, l.st.SelAlias}
	if c == nil || l.connectRecovery != nil || l.sourceRetired || c.transport != current ||
		c.run != l.runEpoch || c.phase != l.phaseEpoch || c.aliasRevision != l.aliasRevision ||
		c.alias != l.st.SelAlias || !sameAliasBinding(c.binding, l.st.AliasBinding) ||
		l.connectRecoverySeen[c.binding.Incarnation] != c.ordinal || !time.Now().Before(c.until) ||
		!l.st.Active || l.st.Paused || l.st.Stopped {
		return false
	}
	l.connectRecovery = c
	return true
}

// The transfer identity, unlike transportRevision, survives pause/resume and
// expiry. A status poll cannot revive an UNKNOWN or spent Connect transfer.
func (l *glLive) holdsConnectRecoveryFence() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeStopLocked()
	c := l.connectRecoveryFence
	return c != nil && c.run == l.runEpoch && c.phase == l.phaseEpoch &&
		c.alias == l.st.SelAlias && sameAliasBinding(c.binding, l.st.AliasBinding)
}

func tryExplicitConnectRecovery(room *Room, live *glLive, att *attention) bool {
	handled, _ := tryExplicitConnectRecoveryOutcome(room, live, att)
	return handled
}

// The completed credit is a bookkeeping receipt for the alias actually routed,
// never a new admission based on a later bridge snapshot.
func tryExplicitConnectRecoveryOutcome(room *Room, live *glLive, att *attention) (bool, *connectRecoveryIntent) {
	if room == nil || room.Idx != 0 {
		return false, nil
	}
	credit, ok := live.consumeConnectRecovery()
	if !ok {
		return false, nil
	}
	// Do not cross local attention's pending/unknown owner at any awaited boundary.
	attentionBlocked := func() bool {
		if att == nil {
			return false
		}
		att.mu.Lock()
		defer att.mu.Unlock()
		return att.active || att.nativeBusy || att.nativePending || att.pendingRelease || att.custodyBlockedLocked() || len(att.nativeGrants) > 0
	}
	if attentionBlocked() {
		log.Printf("connect-recovery manager reason=attention_owner")
		return true, nil
	}
	intent := credit.transport
	guard := func() bool {
		return time.Now().Before(credit.until) && live.startIntentCurrent(intent) && !attentionBlocked()
	}
	status, capturedIntent := live.transportSnapshot()
	if capturedIntent != intent || !guard() {
		return true, nil
	}
	released := fileExists(releasedPath(room))
	ctx, cancel := context.WithDeadline(context.Background(), credit.until)
	state := bridgeStartRouteFact(ctx, room, status, intent, released)
	cancel()
	if !guard() || fileExists(releasedPath(room)) != released {
		return true, nil
	}
	if state == startRouteOwned {
		if !commitConnectRecoveryReceipt(room, live, att, credit, released) {
			return true, nil
		}
		credit.startOwned = true
		log.Printf("connect-recovery manager reason=completed_start_observed")
		return true, credit
	}
	// This read proves only OWNED. Absence admission belongs exclusively to
	// C's synchronous recovery=connect guard immediately before each effect.
	primeOutcome := startPrimeRejected
	accepted, current := routeAliasOutputWithGuardOutcome(room.OwnTone, credit.alias, live, credit.aliasRevision, nil, guard, &primeOutcome, true, true)
	if primeOutcome == startPrimeTemporaryBusy && !accepted && current && guard() && live.retainConnectRecovery(credit) {
		log.Printf("connect-recovery manager reason=temporary_busy_pending")
		return true, nil
	}
	if accepted && current && guard() && routeIntentCurrent(live, credit.alias, credit.aliasRevision) {
		// Every C volume/selection command independently rejected busy/unknown START.
		// A failure (including timeout) leaves release intact and cannot retry this credit.
		if commitConnectRecoveryReceipt(room, live, att, credit, released) {
			log.Printf("connect-recovery manager reason=route_accepted")
			log.Printf("connect-recovery manager reason=attempt_finished")
			return true, credit
		}
		log.Printf("connect-recovery manager reason=route_not_committed")
	} else {
		log.Printf("connect-recovery manager reason=route_not_committed")
	}
	log.Printf("connect-recovery manager reason=attempt_finished")
	return true, nil
}

// Only the captured source/credit may commit bookkeeping after awaited route reads.
// This point performs no network action and cannot consume a successor's marker.
func commitConnectRecoveryReceipt(room *Room, live *glLive, att *attention, credit *connectRecoveryIntent, released bool) bool {
	if att != nil {
		att.mu.Lock()
		defer att.mu.Unlock()
		if att.active || att.nativeBusy || att.nativePending || att.pendingRelease || att.custodyBlockedLocked() || len(att.nativeGrants) > 0 {
			return false
		}
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	live.observeStopLocked()
	current := glTransportIntent{live.runEpoch, live.phaseEpoch, live.aliasRevision, live.transportRevision, live.st.SelAlias}
	if live.sourceRetired || live.connectRecoveryFence != credit || credit.transport != current || !time.Now().Before(credit.until) || !sameAliasBinding(credit.binding, live.st.AliasBinding) || !live.st.Active || live.st.Paused || live.st.Stopped || fileExists(releasedPath(room)) != released {
		return false
	}
	if err := os.Remove(releasedPath(room)); err != nil && !os.IsNotExist(err) {
		return false
	}
	live.connectRecoveryFence = nil
	return true
}
