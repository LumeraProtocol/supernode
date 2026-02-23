package kademlia

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

const (
	defaultRebalanceInterval       = 10 * time.Minute
	rebalanceScanPageSize          = 500
	rebalanceMaxKeysPerCycle       = 2000
	rebalanceProbeFanout           = Alpha
	rebalanceDeleteConfirmCycles   = 2
	rebalanceMaxDeletesPerCycle    = 100
	rebalanceMaxHealsPerCycle      = 200
	rebalanceMaxConfirmEntries     = 200000
	rebalanceProbeRequestTimeout   = 8 * time.Second
	rebalanceStoreAllowSkipLogStep = 25
)

func (s *DHT) startRebalanceWorker(ctx context.Context) {
	if s == nil {
		return
	}
	logtrace.Debug(ctx, "rebalance worker started", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"interval":           defaultRebalanceInterval.String(),
		"page_size":          rebalanceScanPageSize,
		"max_keys_cycle":     rebalanceMaxKeysPerCycle,
		"probe_fanout":       rebalanceProbeFanout,
	})

	ticker := time.NewTicker(defaultRebalanceInterval)
	defer ticker.Stop()

	deleteConfirm := make(map[string]int, rebalanceScanPageSize)
	cursor := ""
	storeAllowNotReadySkips := 0

	for {
		select {
		case <-ctx.Done():
			logtrace.Info(ctx, "closing rebalance worker", logtrace.Fields{
				logtrace.FieldModule: "p2p",
			})
			return
		case <-ticker.C:
			if !s.storeAllowReady.Load() || s.storeAllowCount.Load() == 0 {
				storeAllowNotReadySkips++
				// Avoid noisy logs while still leaving breadcrumbs during prolonged bootstrap/chain issues.
				if storeAllowNotReadySkips == 1 || storeAllowNotReadySkips%rebalanceStoreAllowSkipLogStep == 0 {
					logtrace.Debug(ctx, "rebalance skipped: store allowlist not ready", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						"store_allow_ready":  s.storeAllowReady.Load(),
						"store_allow_count":  s.storeAllowCount.Load(),
						"skips":              storeAllowNotReadySkips,
					})
				}
				continue
			}
			storeAllowNotReadySkips = 0

			if len(deleteConfirm) > rebalanceMaxConfirmEntries {
				deleteConfirm = make(map[string]int, rebalanceScanPageSize)
				logtrace.Warn(ctx, "rebalance confirmation cache reset due to size", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"max_entries":        rebalanceMaxConfirmEntries,
				})
			}

			cursor = s.rebalanceOnce(ctx, cursor, deleteConfirm)
		}
	}
}

func (s *DHT) rebalanceOnce(ctx context.Context, startCursor string, deleteConfirm map[string]int) string {
	if s == nil || s.ht == nil || s.store == nil {
		return startCursor
	}
	if integrationTestEnabled() {
		return startCursor
	}

	cursor := startCursor
	processed := 0
	underReplicated := 0
	healed := 0
	deleted := 0
	decodeErrors := 0

	for processed < rebalanceMaxKeysPerCycle {
		keys, err := s.store.ListLocalKeysPage(ctx, cursor, rebalanceScanPageSize)
		if err != nil {
			logtrace.Error(ctx, "rebalance: list local keys failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
			return cursor
		}
		if len(keys) == 0 {
			// Wrap around at end of keyspace.
			cursor = ""
			break
		}

		for _, keyHex := range keys {
			cursor = keyHex
			if processed >= rebalanceMaxKeysPerCycle {
				break
			}
			processed++

			keyBytes, err := hex.DecodeString(keyHex)
			if err != nil {
				decodeErrors++
				delete(deleteConfirm, keyHex)
				continue
			}

			candidates := s.storeEligibleCandidatesForKey(keyBytes, rebalanceProbeFanout)
			if len(candidates) == 0 {
				delete(deleteConfirm, keyHex)
				continue
			}

			ownerN := Alpha
			if ownerN > len(candidates) {
				ownerN = len(candidates)
			}
			owners := candidates[:ownerN]
			isOwner := containsNodeID(owners, s.ht.self.ID)

			probeStatuses, holders := s.probeKeyAcrossCandidates(ctx, keyHex, candidates)
			selfID := string(s.ht.self.ID)
			selfStatus, ok := probeStatuses[selfID]
			if !ok {
				local, lerr := s.store.RetrieveBatchLocalStatus(ctx, []string{keyHex})
				if lerr != nil {
					delete(deleteConfirm, keyHex)
					continue
				}
				selfStatus = local[keyHex]
				probeStatuses[selfID] = selfStatus
				if selfStatus.Exists && selfStatus.HasLocalBlob {
					holders++
				}
			}
			if !selfStatus.Exists || !selfStatus.HasLocalBlob {
				// This worker only manages local key copies; skip cloud-only placeholders or stale rows.
				delete(deleteConfirm, keyHex)
				continue
			}

			if holders < Alpha {
				underReplicated++
				delete(deleteConfirm, keyHex)

				if isOwner && healed < rebalanceMaxHealsPerCycle {
					before := holders
					holders = s.healKeyToMinimumReplicas(ctx, keyHex, keyBytes, candidates, probeStatuses, holders, selfStatus.Datatype)
					if holders > before {
						healed += holders - before
					}
				}
				continue
			}

			if !isOwner && holders >= Alpha {
				deleteConfirm[keyHex]++
				if deleteConfirm[keyHex] >= rebalanceDeleteConfirmCycles && deleted < rebalanceMaxDeletesPerCycle {
					if err := s.store.BatchDeleteRecords([]string{keyHex}); err != nil {
						logtrace.Error(ctx, "rebalance: local delete failed", logtrace.Fields{
							logtrace.FieldModule: "p2p",
							logtrace.FieldError:  err.Error(),
							"key":                keyHex,
						})
						continue
					}
					deleted++
					delete(deleteConfirm, keyHex)
				}
			} else {
				delete(deleteConfirm, keyHex)
			}
		}

		if len(keys) < rebalanceScanPageSize {
			// Reached end of keyspace in this cycle.
			cursor = ""
			break
		}
	}

	logtrace.Info(ctx, "rebalance cycle complete", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"cursor":             cursor,
		"processed":          processed,
		"under_replicated":   underReplicated,
		"healed":             healed,
		"deleted":            deleted,
		"decode_errors":      decodeErrors,
		"confirm_entries":    len(deleteConfirm),
	})

	return cursor
}

func (s *DHT) storeEligibleCandidatesForKey(target []byte, want int) []*Node {
	if s == nil || s.ht == nil || len(target) == 0 {
		return nil
	}

	candidateLimit := len(s.ht.nodes()) + 1
	if candidateLimit < want {
		candidateLimit = want
	}
	ignoredSet := hashedIDSetFromNodes(s.ignorelist.ToNodeList())
	self := &Node{ID: s.ht.self.ID, IP: s.ht.self.IP, Port: s.ht.self.Port}
	self.SetHashedID()
	nl := s.ht.closestContactsWithIncludingNodeWithIgnoredSet(candidateLimit, target, ignoredSet, self)

	out := make([]*Node, 0, want)
	seen := make(map[string]struct{}, len(nl.Nodes))
	for _, n := range nl.Nodes {
		if n == nil || len(n.ID) == 0 {
			continue
		}
		id := string(n.ID)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if s.eligibleForStore(n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *DHT) probeKeyAcrossCandidates(ctx context.Context, keyHex string, candidates []*Node) (map[string]LocalKeyStatus, int) {
	statuses := make(map[string]LocalKeyStatus, len(candidates))
	holders := 0

	for i, n := range candidates {
		if i >= rebalanceProbeFanout {
			break
		}
		if n == nil || len(n.ID) == 0 {
			continue
		}

		var st LocalKeyStatus
		var err error
		if bytes.Equal(n.ID, s.ht.self.ID) {
			local, lerr := s.store.RetrieveBatchLocalStatus(ctx, []string{keyHex})
			if lerr != nil {
				err = lerr
			} else {
				st = local[keyHex]
			}
		} else {
			st, err = s.probeKeyOnNode(ctx, n, keyHex)
		}
		if err != nil {
			continue
		}

		statuses[string(n.ID)] = st
		if st.Exists && st.HasLocalBlob {
			holders++
		}
	}

	return statuses, holders
}

func (s *DHT) probeKeyOnNode(ctx context.Context, n *Node, keyHex string) (LocalKeyStatus, error) {
	pctx, cancel := context.WithTimeout(ctx, rebalanceProbeRequestTimeout)
	defer cancel()

	req := &BatchProbeKeysRequest{Keys: []string{keyHex}}
	resp, err := s.sendBatchProbeKeys(pctx, n, req)
	if err != nil {
		return LocalKeyStatus{}, err
	}
	st, ok := resp.Data[keyHex]
	if !ok {
		return LocalKeyStatus{}, nil
	}
	return st, nil
}

func (s *DHT) sendBatchProbeKeys(ctx context.Context, n *Node, request *BatchProbeKeysRequest) (*BatchProbeKeysResponse, error) {
	reqMsg := s.newMessage(BatchProbeKeys, n, request)
	rspMsg, err := s.network.Call(ctx, reqMsg, false)
	if err != nil {
		return nil, fmt.Errorf("probe network call: %w", err)
	}

	response, ok := rspMsg.Data.(*BatchProbeKeysResponse)
	if !ok {
		return nil, fmt.Errorf("invalid BatchProbeKeysResponse")
	}
	if response.Status.Result != ResultOk {
		return nil, fmt.Errorf("probe failed: %s", response.Status.ErrMsg)
	}
	if response.Data == nil {
		response.Data = map[string]LocalKeyStatus{}
	}
	return response, nil
}

func (s *DHT) healKeyToMinimumReplicas(
	ctx context.Context,
	keyHex string,
	key []byte,
	candidates []*Node,
	probeStatuses map[string]LocalKeyStatus,
	holders int,
	datatype int,
) int {
	if holders >= Alpha {
		return holders
	}

	value, err := s.store.Retrieve(ctx, key)
	if err != nil || len(value) == 0 {
		return holders
	}

	for _, n := range candidates {
		if holders >= Alpha {
			break
		}
		if n == nil || len(n.ID) == 0 || bytes.Equal(n.ID, s.ht.self.ID) {
			continue
		}
		id := string(n.ID)
		if st, ok := probeStatuses[id]; ok && st.Exists && st.HasLocalBlob {
			continue
		}

		response, err := s.sendStoreData(ctx, n, &StoreDataRequest{Data: value, Type: datatype})
		if err != nil || response == nil || response.Status.Result != ResultOk {
			continue
		}

		probeStatuses[id] = LocalKeyStatus{
			Exists:       true,
			HasLocalBlob: true,
			DataLen:      len(value),
			Datatype:     datatype,
		}
		holders++

		logtrace.Debug(ctx, "rebalance healed key replica", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"key":                keyHex,
			"node":               n.String(),
			"holders":            holders,
		})
	}

	return holders
}

func containsNodeID(nodes []*Node, id []byte) bool {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if bytes.Equal(n.ID, id) {
			return true
		}
	}
	return false
}
