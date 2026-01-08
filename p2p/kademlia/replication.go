package kademlia

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"encoding/hex"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cenkalti/backoff/v4"
)

const replicateKeysRequestBatchSize = 5000

var (
	// defaultReplicationInterval is the default interval for replication.
	defaultReplicationInterval = time.Minute * 10

	// nodeShowUpDeadline is the time after which the node will considered to be permeant offline
	// we'll adjust the keys once the node is permeant offline
	nodeShowUpDeadline = time.Minute * 35

	// check for active & inactive nodes after this interval
	checkNodeActivityInterval = time.Minute * 5

	defaultFetchAndStoreInterval = time.Minute * 10

	defaultBatchFetchAndStoreInterval = time.Minute * 5

	maxBackOff = 45 * time.Second
)

func splitReplicateKeys(keys []string) [][]string {
	if len(keys) == 0 {
		return nil
	}

	batchSize := replicateKeysRequestBatchSize
	if batchSize <= 0 {
		batchSize = 1
	}

	out := make([][]string, 0, (len(keys)+batchSize-1)/batchSize)
	for i := 0; i < len(keys); i += batchSize {
		j := i + batchSize
		if j > len(keys) {
			j = len(keys)
		}
		out = append(out, keys[i:j])
	}
	return out
}

// StartReplicationWorker starts replication
func (s *DHT) StartReplicationWorker(ctx context.Context) error {
	logtrace.Debug(ctx, "replication worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	go s.checkNodeActivity(ctx)
	go s.StartBatchFetchAndStoreWorker(ctx)
	go s.StartFailedFetchAndStoreWorker(ctx)

	for {
		select {
		case <-time.After(defaultReplicationInterval):
			//log.WithContext(ctx).Info("replication worker disabled")
			s.Replicate(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "closing replication worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

// StartBatchFetchAndStoreWorker starts replication
func (s *DHT) StartBatchFetchAndStoreWorker(ctx context.Context) error {
	logtrace.Debug(ctx, "batch fetch and store worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultBatchFetchAndStoreInterval):
			s.BatchFetchAndStore(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "closing batch fetch & store worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

// StartFailedFetchAndStoreWorker starts replication
func (s *DHT) StartFailedFetchAndStoreWorker(ctx context.Context) error {
	logtrace.Debug(ctx, "fetch and store worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultFetchAndStoreInterval):
			s.BatchFetchAndStoreFailedKeys(ctx)
		case <-ctx.Done():
			logtrace.Info(ctx, "closing fetch & store worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

func (s *DHT) updateReplicationNode(ctx context.Context, nodeID []byte, ip string, port uint16, isActive bool) error {
	// check if record exists
	ok, err := s.store.RecordExists(string(nodeID))
	if err != nil {
		return fmt.Errorf("err checking if replication info record exists: %w", err)
	}

	now := time.Now().UTC()
	info := domain.NodeReplicationInfo{
		UpdatedAt: time.Now().UTC(),
		Active:    isActive,
		IP:        ip,
		Port:      port,
		ID:        nodeID,
		LastSeen:  &now,
	}

	if ok {
		if err := s.store.UpdateReplicationInfo(ctx, info); err != nil {
			logtrace.Error(ctx, "failed to update replication info", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "node_id": string(nodeID), "ip": ip})
			return err
		}
	} else {
		if err := s.store.AddReplicationInfo(ctx, info); err != nil {
			logtrace.Error(ctx, "failed to add replication info", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "node_id": string(nodeID), "ip": ip})
			return err
		}
	}

	return nil
}

func (s *DHT) updateLastReplicated(ctx context.Context, nodeID []byte, timestamp time.Time) error {
	// Important: callers must be able to detect failures here; advancing lastReplicatedAt incorrectly can
	// permanently skip keys in subsequent runs.
	if err := s.store.UpdateLastReplicated(ctx, string(nodeID), timestamp); err != nil {
		logtrace.Error(ctx, "failed to update replication info last replicated", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "node_id": string(nodeID)})
		return err
	}

	return nil
}

// Replicate is called periodically by the replication worker to replicate the data across the network by refreshing the buckets
// it iterates over the node replication Info map and replicates the data to the nodes that are active
func (s *DHT) Replicate(ctx context.Context) {
	historicStart, err := s.store.GetOwnCreatedAt(ctx)
	if err != nil {
		logtrace.Error(ctx, "unable to get own createdAt", logtrace.Fields{logtrace.FieldError: err.Error()})
		historicStart = time.Now().UTC().Add(-24 * time.Hour * 180)
	}

	logtrace.Debug(ctx, "replicating data", logtrace.Fields{logtrace.FieldModule: "p2p", "historic-start": historicStart})

	for i := 0; i < B; i++ {
		if time.Since(s.ht.refreshTime(i)) > defaultRefreshTime {
			// refresh the bucket by iterative find node
			id := s.ht.randomIDFromBucket(i)
			if _, err := s.iterate(ctx, IterateFindNode, id, nil, 0); err != nil {
				logtrace.Error(ctx, "replicate iterate find node failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
			}
		}
	}

	repInfo, err := s.store.GetAllReplicationInfo(ctx)
	if err != nil {
		logtrace.Error(ctx, "get all replicationInfo failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
		return
	}

	if len(repInfo) == 0 {
		logtrace.Debug(ctx, "no replication info found", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return
	}

	// Important: compute a global window based on ACTIVE peers only.
	// - Avoids an inactive/uninitialized node forcing perpetual historic scans.
	// - Still includes keys needed by the most "behind" active peer (min lastReplicatedAt).
	// - Does not advance any cursor when the underlying key scan fails (handled below).
	var globalFrom time.Time
	haveActivePeer := false
	for _, info := range repInfo {
		if !info.Active {
			continue
		}
		start := historicStart
		if info.LastReplicatedAt != nil {
			start = *info.LastReplicatedAt
		}
		if !haveActivePeer {
			globalFrom = start
			haveActivePeer = true
			continue
		}
		if start.Before(globalFrom) {
			globalFrom = start
		}
	}

	if !haveActivePeer {
		// Important: even when there are no active peers to replicate TO, we still want to run
		// the offline-node adjustment logic (it uses last_seen and is_adjusted).
		logtrace.Debug(ctx, "no active replication peers; skipping replicate send phase", logtrace.Fields{logtrace.FieldModule: "p2p"})
		for _, info := range repInfo {
			if !info.Active {
				s.checkAndAdjustNode(ctx, info, historicStart)
			}
		}
		return
	}

	logtrace.Debug(ctx, "getting all possible replication keys", logtrace.Fields{logtrace.FieldModule: "p2p", "from": globalFrom})
	to := time.Now().UTC()
	replicationKeys := s.store.GetKeysForReplication(ctx, globalFrom, to)
	if replicationKeys == nil {
		// Important: treat nil as "DB error" (store returns non-nil empty slice on success).
		// Never advance lastReplicatedAt in this case, otherwise we can skip keys permanently.
		// Still run offline-node adjustment.
		logtrace.Error(ctx, "get keys for replication failed (nil); skipping replicate send phase", logtrace.Fields{logtrace.FieldModule: "p2p"})
		for _, info := range repInfo {
			if !info.Active {
				s.checkAndAdjustNode(ctx, info, historicStart)
			}
		}
		return
	}

	ignores := s.ignorelist.ToNodeList()
	closestContactsMap := make(map[string][][]byte)

	supernodeAddr, _ := s.getSupernodeAddress(ctx)
	hostIP := parseSupernodeAddress(supernodeAddr)
	self := &Node{ID: s.ht.self.ID, IP: hostIP, Port: s.ht.self.Port}
	self.SetHashedID()

	for i := 0; i < len(replicationKeys); i++ {
		decKey, _ := hex.DecodeString(replicationKeys[i].Key)
		closestContactsMap[replicationKeys[i].Key] = s.ht.closestContactsWithIncludingNode(Alpha, decKey, ignores, self).NodeIDs()
	}

	for _, info := range repInfo {
		if !info.Active {
			s.checkAndAdjustNode(ctx, info, historicStart)
			continue
		}

		start := historicStart
		if info.LastReplicatedAt != nil {
			start = *info.LastReplicatedAt
		}

		idx := replicationKeys.FindFirstAfter(start)
		if idx == -1 {
			// Now closestContactKeys contains all the keys that are in the closest contacts.
			if err := s.updateLastReplicated(ctx, info.ID, to); err != nil {
				logtrace.Error(ctx, "replicate update lastReplicated failed", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
			} else {
				logtrace.Debug(ctx, "no replication keys - replicate update lastReplicated success", logtrace.Fields{logtrace.FieldModule: "p2p", "node": info.IP, "to": to.String(), "fetch-keys": 0})
			}

			continue
		}
		countToSendKeys := len(replicationKeys) - idx
		logtrace.Debug(ctx, "count of replication keys to be checked", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID), "len-rep-keys": countToSendKeys})
		preAlloc := countToSendKeys
		if preAlloc > replicateKeysRequestBatchSize {
			preAlloc = replicateKeysRequestBatchSize
		}
		closestContactKeys := make([]string, 0, preAlloc)

		for i := idx; i < len(replicationKeys); i++ {
			for j := 0; j < len(closestContactsMap[replicationKeys[i].Key]); j++ {
				if bytes.Equal(closestContactsMap[replicationKeys[i].Key][j], info.ID) {
					// the node is supposed to hold this key as it's in the 6 closest contacts
					closestContactKeys = append(closestContactKeys, replicationKeys[i].Key)
				}
			}
		}

		batches := splitReplicateKeys(closestContactKeys)
		if len(batches) > 1 {
			keysBytes := 0
			for _, k := range closestContactKeys {
				keysBytes += len(k)
			}
			logtrace.Info(ctx, "replicate batching keys", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"rep-ip":             info.IP,
				"rep-id":             string(info.ID),
				"keys":               len(closestContactKeys),
				"batches":            len(batches),
				"batch_size":         replicateKeysRequestBatchSize,
				"keys_mb_lower":      utils.BytesIntToMB(keysBytes),
			})
		}

		logtrace.Debug(ctx, "closest contact keys count", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID), "len-rep-keys": len(closestContactKeys), "batches": len(batches)})

		if len(closestContactKeys) == 0 {
			if err := s.updateLastReplicated(ctx, info.ID, to); err != nil {
				logtrace.Error(ctx, "replicate update lastReplicated failed", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
			} else {
				logtrace.Debug(ctx, "no closest keys found - replicate update lastReplicated success", logtrace.Fields{logtrace.FieldModule: "p2p", "node": info.IP, "to": to.String(), "closest-contact-keys": 0})
			}

			continue
		}

		n := &Node{ID: info.ID, IP: info.IP, Port: info.Port}
		sendErr := error(nil)
		for batchIdx, batchKeys := range batches {
			batchNum := batchIdx + 1
			request := &ReplicateDataRequest{Keys: batchKeys}

			b := backoff.NewExponentialBackOff()
			b.MaxElapsedTime = maxBackOff

			logtrace.Debug(ctx, "sending replicate batch", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"rep-ip":             info.IP,
				"rep-id":             string(info.ID),
				"batch":              batchNum,
				"batches":            len(batches),
				"batch_keys":         len(batchKeys),
			})

			sendErr = backoff.RetryNotify(func() error {
				response, err := s.sendReplicateData(ctx, n, request)
				if err != nil {
					return err
				}

				if response.Status.Result != ResultOk {
					return errors.New(response.Status.ErrMsg)
				}

				return nil
			}, b, func(err error, duration time.Duration) {
				logtrace.Error(ctx, "retrying send replicate data", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"rep-ip":             info.IP,
					"rep-id":             string(info.ID),
					"batch":              batchNum,
					"batches":            len(batches),
					"duration":           duration,
				})
			})

			if sendErr != nil {
				logtrace.Error(ctx, "send replicate batch failed after retries", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  sendErr.Error(),
					"rep-ip":             info.IP,
					"rep-id":             string(info.ID),
					"batch":              batchNum,
					"batches":            len(batches),
				})
				break
			}
		}

		if sendErr != nil {
			continue
		}

		// Now closestContactKeys contains all the keys that are in the closest contacts.
		if err := s.updateLastReplicated(ctx, info.ID, to); err != nil {
			logtrace.Error(ctx, "replicate update lastReplicated failed", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
		} else {
			logtrace.Debug(ctx, "replicate update lastReplicated success", logtrace.Fields{logtrace.FieldModule: "p2p", "node": info.IP, "to": to.String(), "expected-rep-keys": len(closestContactKeys), "batches": len(batches)})
		}
	}

	logtrace.Debug(ctx, "Replication done", logtrace.Fields{logtrace.FieldModule: "p2p"})
}

func (s *DHT) adjustNodeKeys(ctx context.Context, from time.Time, info domain.NodeReplicationInfo) error {
	replicationKeys := s.store.GetKeysForReplication(ctx, from, time.Now().UTC())

	logtrace.Debug(ctx, "begin adjusting node keys process for offline node", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID), "total-rep-keys": len(replicationKeys), "from": from.String()})

	// prepare ignored nodes list but remove the node we are adjusting
	// because we want to find if this node was supposed to hold this key
	ignores := s.ignorelist.ToNodeList()
	var updatedIgnored []*Node
	for _, ignore := range ignores {
		if !bytes.Equal(ignore.ID, info.ID) {
			updatedIgnored = append(updatedIgnored, ignore)
		}
	}

	nodeKeysMap := make(map[string][]string)
	for i := 0; i < len(replicationKeys); i++ {

		offNode := &Node{ID: []byte(info.ID), IP: info.IP, Port: info.Port}
		offNode.SetHashedID()

		// get closest contacts to the key
		key, _ := hex.DecodeString(replicationKeys[i].Key)
		nodeList := s.ht.closestContactsWithIncludingNode(Alpha+1, key, updatedIgnored, offNode) // +1 because we want to include the node we are adjusting
		// check if the node that is gone was supposed to hold the key
		if !nodeList.Exists(offNode) {
			// the node is not supposed to hold this key as its not in 6 closest contacts
			continue
		}

		for i := 0; i < len(nodeList.Nodes); i++ {
			if nodeList.Nodes[i].IP == info.IP {
				continue // because we do not want to send request to the server that's already offline
			}

			// If the node is supposed to hold the key, we map the node's info to the key
			nodeInfo := generateKeyFromNode(nodeList.Nodes[i])

			// append the key to the list of keys that the node is supposed to have
			nodeKeysMap[nodeInfo] = append(nodeKeysMap[nodeInfo], replicationKeys[i].Key)

		}
	}

	// iterate over the map and send the keys to the node
	// Loop over the map
	successCount := 0
	failureCount := 0

	for nodeInfoKey, keys := range nodeKeysMap {
		logtrace.Debug(ctx, "sending adjusted replication keys to node", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID), "adjust-to-node": nodeInfoKey, "to-adjust-keys-len": len(keys)})
		// Retrieve the node object from the key
		node, err := getNodeFromKey(nodeInfoKey)
		if err != nil {
			logtrace.Error(ctx, "Failed to parse node info from key", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "offline-node-ip": info.IP, "offline-node-id": string(info.ID)})
			return fmt.Errorf("failed to parse node info from key: %w", err)
		}

		batches := splitReplicateKeys(keys)
		if len(batches) > 1 {
			keysBytes := 0
			for _, k := range keys {
				keysBytes += len(k)
			}
			logtrace.Info(ctx, "adjust batching keys", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"offline-node-ip":    info.IP,
				"offline-node-id":    string(info.ID),
				"adjust-to-node":     nodeInfoKey,
				"keys":               len(keys),
				"batches":            len(batches),
				"batch_size":         replicateKeysRequestBatchSize,
				"keys_mb_lower":      utils.BytesIntToMB(keysBytes),
			})
		}

		nodeSuccess := true
		for batchIdx, batchKeys := range batches {
			batchNum := batchIdx + 1
			request := &ReplicateDataRequest{Keys: batchKeys}

			b := backoff.NewExponentialBackOff()
			b.MaxElapsedTime = maxBackOff

			logtrace.Debug(ctx, "sending adjusted replicate batch", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"offline-node-ip":    info.IP,
				"offline-node-id":    string(info.ID),
				"adjust-to-node":     nodeInfoKey,
				"batch":              batchNum,
				"batches":            len(batches),
				"batch_keys":         len(batchKeys),
			})

			err = backoff.RetryNotify(func() error {
				response, err := s.sendReplicateData(ctx, node, request)
				if err != nil {
					return err
				}

				if response.Status.Result != ResultOk {
					return errors.New(response.Status.ErrMsg)
				}

				return nil
			}, b, func(err error, duration time.Duration) {
				logtrace.Error(ctx, "retrying send replicate data", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"offline-node-ip":    info.IP,
					"offline-node-id":    string(info.ID),
					"adjust-to-node":     nodeInfoKey,
					"batch":              batchNum,
					"batches":            len(batches),
					"duration":           duration,
				})
			})

			if err != nil {
				logtrace.Error(ctx, "send adjusted replicate batch failed after retries", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"offline-node-ip":    info.IP,
					"offline-node-id":    string(info.ID),
					"adjust-to-node":     nodeInfoKey,
					"batch":              batchNum,
					"batches":            len(batches),
				})
				nodeSuccess = false
				break
			}
		}

		if nodeSuccess {
			successCount++
		} else {
			failureCount++
		}
	}

	totalCount := successCount + failureCount

	if totalCount > 0 { // Prevent division by zero
		successRate := float64(successCount) / float64(totalCount) * 100

		if successRate < 75 {
			// Success rate is less than 75%
			return fmt.Errorf("adjust keys success rate is less than 75%%: %v", successRate)
		}
	} else {
		return fmt.Errorf("adjust keys totalCount is 0")
	}

	if err := s.store.UpdateIsAdjusted(ctx, string(info.ID), true); err != nil {
		return fmt.Errorf("replicate update isAdjusted failed: %v", err)
	}

	logtrace.Debug(ctx, "offline node was successfully adjusted", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID)})

	return nil
}

func isNodeGoneAndShouldBeAdjusted(lastSeen *time.Time, isAlreadyAdjusted bool) bool {
	if lastSeen == nil {
		logtrace.Debug(context.Background(), "lastSeen is nil - aborting node adjustment", logtrace.Fields{})
		return false
	}

	return time.Since(*lastSeen) > nodeShowUpDeadline && !isAlreadyAdjusted
}

func (s *DHT) checkAndAdjustNode(ctx context.Context, info domain.NodeReplicationInfo, start time.Time) {
	adjustNodeKeys := isNodeGoneAndShouldBeAdjusted(info.LastSeen, info.IsAdjusted)
	if adjustNodeKeys {
		if err := s.adjustNodeKeys(ctx, start, info); err != nil {
			logtrace.Error(ctx, "failed to adjust node keys", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "rep-ip": info.IP, "rep-id": string(info.ID)})
		} else {
			info.IsAdjusted = true
			info.UpdatedAt = time.Now().UTC()

			if err := s.store.UpdateIsAdjusted(ctx, string(info.ID), true); err != nil {
				logtrace.Error(ctx, "failed to update replication info, set isAdjusted to true", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "rep-ip": info.IP, "rep-id": string(info.ID)})
			} else {
				logtrace.Debug(ctx, "set isAdjusted to true", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
			}
		}
	}

	logtrace.Debug(ctx, "replication node not active, skipping over it.", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
}
