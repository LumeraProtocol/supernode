// Package lep6 owns in-process observability counters for the off-chain LEP-6 stack.
//
// The supernode repo does not expose service-specific Prometheus collectors today;
// comparable subsystems use structured logtrace calls plus typed in-process snapshots
// (for example p2p/kademlia handler counters surfaced through status). Keep LEP-6
// aligned with that pattern: hot paths increment cheap atomic counters/gauges and
// tests/status/debug callers can inspect Snapshot().
package lep6

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsSnapshot is a point-in-time copy of LEP-6 off-chain observability signals.
// Counter maps use stable label keys in the form documented on each field.
type MetricsSnapshot struct {
	// Storage challenge / dispatcher — LEP-6 §§9-12.
	DispatchResultsTotal             map[string]uint64 // result_class
	DispatchThrottledTotal           map[string]uint64 // policy
	DispatchEpochDurationMillisTotal map[string]uint64 // role
	DispatchEpochDurationMillisMax   map[string]uint64 // role
	DispatchEpochDurationCount       map[string]uint64 // role
	TicketDiscoveryTotal             map[string]uint64 // result
	NoTicketProviderActive           int64

	// Self-healing — LEP-6 §§18-22.
	HealClaimsSubmittedTotal            map[string]uint64 // outcome
	HealClaimsReconciledTotal           uint64
	HealVerificationsSubmittedTotal     map[string]uint64 // verified=<positive|negative>,result=<outcome>
	HealVerificationsAlreadyExistsTotal uint64
	HealFinalizePublishesTotal          uint64
	HealFinalizeCleanupsTotal           map[string]uint64 // status
	SelfHealingPendingClaims            int64
	SelfHealingStagingBytes             int64

	// Recheck — LEP-6 §12.3 and §15.1.
	RecheckCandidatesFoundTotal          uint64
	RecheckEvidenceSubmittedTotal        map[string]uint64 // class=<result_class>,outcome=<outcome>
	RecheckEvidenceAlreadySubmittedTotal uint64
	RecheckExecutionFailuresTotal        map[string]uint64 // reason
	RecheckPendingCandidates             int64
}

type counterMap struct {
	mu sync.RWMutex
	m  map[string]*atomic.Uint64
}

func (c *counterMap) inc(key string, delta uint64) {
	key = normalizeLabel(key)
	c.mu.RLock()
	v := c.m[key]
	c.mu.RUnlock()
	if v == nil {
		c.mu.Lock()
		if c.m == nil {
			c.m = make(map[string]*atomic.Uint64)
		}
		v = c.m[key]
		if v == nil {
			v = &atomic.Uint64{}
			c.m[key] = v
		}
		c.mu.Unlock()
	}
	v.Add(delta)
}

func (c *counterMap) setMax(key string, value uint64) {
	key = normalizeLabel(key)
	c.mu.RLock()
	v := c.m[key]
	c.mu.RUnlock()
	if v == nil {
		c.mu.Lock()
		if c.m == nil {
			c.m = make(map[string]*atomic.Uint64)
		}
		v = c.m[key]
		if v == nil {
			v = &atomic.Uint64{}
			c.m[key] = v
		}
		c.mu.Unlock()
	}
	for {
		old := v.Load()
		if value <= old || v.CompareAndSwap(old, value) {
			return
		}
	}
}

func (c *counterMap) snapshot() map[string]uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]uint64, len(c.m))
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = c.m[k].Load()
	}
	return out
}

func (c *counterMap) reset() {
	c.mu.Lock()
	c.m = make(map[string]*atomic.Uint64)
	c.mu.Unlock()
}

var metrics = struct {
	dispatchResults          counterMap
	dispatchThrottled        counterMap
	dispatchEpochMillisTotal counterMap
	dispatchEpochMillisMax   counterMap
	dispatchEpochCount       counterMap
	ticketDiscovery          counterMap
	noTicketProviderActive   atomic.Int64

	healClaimsSubmitted           counterMap
	healClaimsReconciled          atomic.Uint64
	healVerificationsSubmitted    counterMap
	healVerificationsAlreadyExist atomic.Uint64
	healFinalizePublishes         atomic.Uint64
	healFinalizeCleanups          counterMap
	selfHealingPendingClaims      atomic.Int64
	selfHealingStagingBytes       atomic.Int64

	recheckCandidatesFound          atomic.Uint64
	recheckEvidenceSubmitted        counterMap
	recheckEvidenceAlreadySubmitted atomic.Uint64
	recheckExecutionFailures        counterMap
	recheckPendingCandidates        atomic.Int64
}{}

// Reset clears all counters/gauges. It is intended for tests.
func Reset() {
	metrics.dispatchResults.reset()
	metrics.dispatchThrottled.reset()
	metrics.dispatchEpochMillisTotal.reset()
	metrics.dispatchEpochMillisMax.reset()
	metrics.dispatchEpochCount.reset()
	metrics.ticketDiscovery.reset()
	metrics.noTicketProviderActive.Store(0)
	metrics.healClaimsSubmitted.reset()
	metrics.healClaimsReconciled.Store(0)
	metrics.healVerificationsSubmitted.reset()
	metrics.healVerificationsAlreadyExist.Store(0)
	metrics.healFinalizePublishes.Store(0)
	metrics.healFinalizeCleanups.reset()
	metrics.selfHealingPendingClaims.Store(0)
	metrics.selfHealingStagingBytes.Store(0)
	metrics.recheckCandidatesFound.Store(0)
	metrics.recheckEvidenceSubmitted.reset()
	metrics.recheckEvidenceAlreadySubmitted.Store(0)
	metrics.recheckExecutionFailures.reset()
	metrics.recheckPendingCandidates.Store(0)
}

// Snapshot returns a consistent copy of current LEP-6 metrics.
func Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		DispatchResultsTotal:                 metrics.dispatchResults.snapshot(),
		DispatchThrottledTotal:               metrics.dispatchThrottled.snapshot(),
		DispatchEpochDurationMillisTotal:     metrics.dispatchEpochMillisTotal.snapshot(),
		DispatchEpochDurationMillisMax:       metrics.dispatchEpochMillisMax.snapshot(),
		DispatchEpochDurationCount:           metrics.dispatchEpochCount.snapshot(),
		TicketDiscoveryTotal:                 metrics.ticketDiscovery.snapshot(),
		NoTicketProviderActive:               metrics.noTicketProviderActive.Load(),
		HealClaimsSubmittedTotal:             metrics.healClaimsSubmitted.snapshot(),
		HealClaimsReconciledTotal:            metrics.healClaimsReconciled.Load(),
		HealVerificationsSubmittedTotal:      metrics.healVerificationsSubmitted.snapshot(),
		HealVerificationsAlreadyExistsTotal:  metrics.healVerificationsAlreadyExist.Load(),
		HealFinalizePublishesTotal:           metrics.healFinalizePublishes.Load(),
		HealFinalizeCleanupsTotal:            metrics.healFinalizeCleanups.snapshot(),
		SelfHealingPendingClaims:             metrics.selfHealingPendingClaims.Load(),
		SelfHealingStagingBytes:              metrics.selfHealingStagingBytes.Load(),
		RecheckCandidatesFoundTotal:          metrics.recheckCandidatesFound.Load(),
		RecheckEvidenceSubmittedTotal:        metrics.recheckEvidenceSubmitted.snapshot(),
		RecheckEvidenceAlreadySubmittedTotal: metrics.recheckEvidenceAlreadySubmitted.Load(),
		RecheckExecutionFailuresTotal:        metrics.recheckExecutionFailures.snapshot(),
		RecheckPendingCandidates:             metrics.recheckPendingCandidates.Load(),
	}
}

func IncDispatchResult(resultClass string) { metrics.dispatchResults.inc(resultClass, 1) }
func IncDispatchThrottled(policy string, dropped int) {
	if dropped > 0 {
		metrics.dispatchThrottled.inc(policy, uint64(dropped))
	}
}
func ObserveDispatchEpochDuration(role string, duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	millis := uint64(duration.Milliseconds())
	metrics.dispatchEpochMillisTotal.inc(role, millis)
	metrics.dispatchEpochMillisMax.setMax(role, millis)
	metrics.dispatchEpochCount.inc(role, 1)
}
func IncTicketDiscovery(result string) { metrics.ticketDiscovery.inc(result, 1) }
func SetNoTicketProviderActive(active bool) {
	if active {
		metrics.noTicketProviderActive.Store(1)
	} else {
		metrics.noTicketProviderActive.Store(0)
	}
}

func IncHealClaim(outcome string) { metrics.healClaimsSubmitted.inc(outcome, 1) }
func IncHealClaimReconciled()     { metrics.healClaimsReconciled.Add(1) }
func IncHealVerification(outcome string, verified bool) {
	vote := "negative"
	if verified {
		vote = "positive"
	}
	metrics.healVerificationsSubmitted.inc("verified="+vote+",result="+normalizeLabel(outcome), 1)
}
func IncHealVerificationAlreadyExists()    { metrics.healVerificationsAlreadyExist.Add(1) }
func IncHealFinalizePublish()              { metrics.healFinalizePublishes.Add(1) }
func IncHealFinalizeCleanup(status string) { metrics.healFinalizeCleanups.inc(status, 1) }
func SetSelfHealingPendingClaims(count int) {
	metrics.selfHealingPendingClaims.Store(nonNegativeInt64(count))
}
func SetSelfHealingStagingBytes(bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	metrics.selfHealingStagingBytes.Store(bytes)
}

func IncRecheckCandidateFound() { metrics.recheckCandidatesFound.Add(1) }
func IncRecheckSubmission(resultClass, outcome string) {
	metrics.recheckEvidenceSubmitted.inc("class="+normalizeLabel(resultClass)+",outcome="+normalizeLabel(outcome), 1)
}
func IncRecheckAlreadySubmitted()     { metrics.recheckEvidenceAlreadySubmitted.Add(1) }
func IncRecheckFailure(reason string) { metrics.recheckExecutionFailures.inc(reason, 1) }
func SetRecheckPendingCandidates(count int) {
	metrics.recheckPendingCandidates.Store(nonNegativeInt64(count))
}

func normalizeLabel(label string) string {
	label = strings.TrimSpace(strings.ToLower(label))
	if label == "" {
		return "unknown"
	}
	return label
}

func nonNegativeInt64(v int) int64 {
	if v < 0 {
		return 0
	}
	return int64(v)
}
