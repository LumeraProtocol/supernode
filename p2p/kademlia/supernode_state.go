package kademlia

import (
	"context"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// Supernode p2p eligibility policy.
//
// Chain is the single source of truth for state values; we re-use
// sntypes.SuperNodeState ordinals to avoid drift.
//
// Two orthogonal gates govern p2p behavior:
//
//   - Routing / read eligibility: peer may sit in the routing table and
//     answer FIND_NODE, FIND_VALUE, BatchRetrieve.
//   - Store / write eligibility:  peer may receive STORE / batch-store
//     and be targeted by replication pushes.
//
// Post-LEP-5 / Everlight (lumera #113) policy:
//
//   routing = {ACTIVE, POSTPONED, STORAGE_FULL}
//   store   = {ACTIVE}
//
// POSTPONED: probation; still holds data, must keep serving reads, no writes.
// STORAGE_FULL: disk-full; still holds data, still payout-eligible, no writes.
//
// All other states (UNSPECIFIED, DISABLED, STOPPED, PENALIZED) are excluded
// from both gates.

// snStateInt normalizes a chain SuperNodeState to int32 for comparison.
func snStateInt(s sntypes.SuperNodeState) int32 { return int32(s) }

// isRoutingEligibleState reports whether the given chain supernode state is
// eligible for participation in routing/read paths.
func isRoutingEligibleState(s int32) bool {
	return s == snStateInt(sntypes.SuperNodeStateActive) ||
		s == snStateInt(sntypes.SuperNodeStatePostponed) ||
		s == snStateInt(sntypes.SuperNodeStateStorageFull)
}

// isStoreEligibleState reports whether the given chain supernode state is
// eligible for write/replication targeting.
func isStoreEligibleState(s int32) bool {
	return s == snStateInt(sntypes.SuperNodeStateActive)
}

// shouldRejectStore reports whether an incoming STORE/BatchStore request
// should be rejected because the self-node is not currently store-eligible
// AND the request contains genuinely new keys (not just replication of
// already-held data).
//
//   newKeys > 0 && !selfStoreEligible()  =>  reject
//
// When newKeys == 0 (all keys already held) replication is always allowed.
// When self is store-eligible (ACTIVE, or pre-bootstrap), always allow.
func (s *DHT) shouldRejectStore(newKeys int) bool {
	if s == nil {
		return false
	}
	if newKeys <= 0 {
		return false
	}
	return !s.selfStoreEligible()
}

// setSelfState caches the latest known chain state for this node. Safe for
// concurrent callers. Called by the bootstrap refresher.
func (s *DHT) setSelfState(state int32) {
	if s == nil {
		return
	}
	s.selfState.Store(state)
	s.selfStateReady.Store(true)
}

// selfStoreEligible reports whether this node is currently permitted to accept
// new-key STORE writes. Returns true when self-state is unknown (pre-bootstrap)
// to avoid lockout; returns true in integration-test mode.
func (s *DHT) selfStoreEligible() bool {
	if s == nil {
		return false
	}
	if integrationTestEnabled() {
		return true
	}
	if !s.selfStateReady.Load() {
		return true
	}
	return isStoreEligibleState(s.selfState.Load())
}

// pruneIneligibleStorePeers clears replication_info.Active for peers no longer
// in the store allowlist. Keeps the replication worker from pushing writes to
// STORAGE_FULL / POSTPONED / evicted peers between ping cycles.
//
// Integration tests bypass this via integrationTestEnabled() check in the
// allowlist setter (empty allowlist => ready=false => no-op here).
func (s *DHT) pruneIneligibleStorePeers(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	if !s.storeAllowReady.Load() {
		return
	}
	infos, err := s.store.GetAllReplicationInfo(ctx)
	if err != nil {
		logtrace.Warn(ctx, "pruneIneligibleStorePeers: list replication info failed", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
		return
	}
	cleared := 0
	for _, info := range infos {
		if !info.Active {
			continue
		}
		// Derive the same key used by the allowlist.
		node := &Node{ID: info.ID}
		node.SetHashedID()
		if len(node.HashedID) != 32 {
			continue
		}
		var key [32]byte
		copy(key[:], node.HashedID)

		s.storeAllowMu.RLock()
		_, ok := s.storeAllow[key]
		s.storeAllowMu.RUnlock()
		if ok {
			continue
		}
		if uerr := s.store.UpdateIsActive(ctx, string(info.ID), false, false); uerr != nil {
			logtrace.Warn(ctx, "pruneIneligibleStorePeers: UpdateIsActive failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  uerr.Error(),
				"node_id":            string(info.ID),
			})
			continue
		}
		cleared++
	}
	if cleared > 0 {
		logtrace.Info(ctx, "pruneIneligibleStorePeers: cleared replication_info.Active for ineligible peers", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"cleared":            cleared,
		})
	}
}
