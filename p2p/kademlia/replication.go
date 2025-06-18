package kademlia

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"encoding/hex"

	"github.com/LumeraProtocol/supernode/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/cenkalti/backoff/v4"
)

var (
	// defaultReplicationInterval is the default interval for replication.
	defaultReplicationInterval = time.Minute * 10

	// nodeShowUpDeadline is the time after which the node will considered to be permeant offline
	// we'll adjust the keys once the node is permeant offline
	nodeShowUpDeadline = time.Minute * 35

	// check for active & inactive nodes after this interval
	checkNodeActivityInterval = time.Minute * 2

	defaultFetchAndStoreInterval = time.Minute * 10

	defaultBatchFetchAndStoreInterval = time.Minute * 5

	maxBackOff = 45 * time.Second
)

// StartReplicationWorker starts replication
func (s *DHT) StartReplicationWorker(ctx context.Context) error {
	logtrace.Info(ctx, "replication worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	go s.checkNodeActivity(ctx)
	go s.StartBatchFetchAndStoreWorker(ctx)
	go s.StartFailedFetchAndStoreWorker(ctx)

	for {
		select {
		case <-time.After(defaultReplicationInterval):
			//log.WithContext(ctx).Info("replication worker disabled")
			s.Replicate(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing replication worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

// StartBatchFetchAndStoreWorker starts replication
func (s *DHT) StartBatchFetchAndStoreWorker(ctx context.Context) error {
	logtrace.Info(ctx, "batch fetch and store worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultBatchFetchAndStoreInterval):
			s.BatchFetchAndStore(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing batch fetch & store worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

// StartFailedFetchAndStoreWorker starts replication
func (s *DHT) StartFailedFetchAndStoreWorker(ctx context.Context) error {
	logtrace.Info(ctx, "fetch and store worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultFetchAndStoreInterval):
			s.BatchFetchAndStoreFailedKeys(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing fetch & store worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
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
	if err := s.store.UpdateLastReplicated(ctx, string(nodeID), timestamp); err != nil {
		logtrace.Error(ctx, "failed to update replication info last replicated", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "node_id": string(nodeID)})
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

	logtrace.Info(ctx, "replicating data", logtrace.Fields{logtrace.FieldModule: "p2p", "historic-start": historicStart})

	for i := 0; i < B; i++ {
		if time.Since(s.ht.refreshTime(i)) > defaultRefreshTime {
			// refresh the bucket by iterative find node
			id := s.ht.randomIDFromBucket(K)
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
		logtrace.Info(ctx, "no replication info found", logtrace.Fields{logtrace.FieldModule: "p2p"})
		return
	}

	from := historicStart
	if repInfo[0].LastReplicatedAt != nil {
		from = *repInfo[0].LastReplicatedAt
	}

	logtrace.Info(ctx, "getting all possible replication keys", logtrace.Fields{logtrace.FieldModule: "p2p", "from": from})
	to := time.Now().UTC()
	replicationKeys := s.store.GetKeysForReplication(ctx, from, to)

	ignores := s.ignorelist.ToNodeList()
	closestContactsMap := make(map[string][][]byte)

	self := &Node{ID: s.ht.self.ID, IP: s.externalIP, Port: s.ht.self.Port}
	self.SetHashedID()

	for i := 0; i < len(replicationKeys); i++ {
		decKey, _ := hex.DecodeString(replicationKeys[i].Key)
		closestContactsMap[replicationKeys[i].Key] = s.ht.closestContactsWithInlcudingNode(Alpha, decKey, ignores, self).NodeIDs()
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
		logtrace.Info(ctx, "count of replication keys to be checked", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID), "len-rep-keys": countToSendKeys})
		// Preallocate a slice with a capacity equal to the number of keys.
		closestContactKeys := make([]string, 0, countToSendKeys)

		for i := idx; i < len(replicationKeys); i++ {
			for j := 0; j < len(closestContactsMap[replicationKeys[i].Key]); j++ {
				if bytes.Equal(closestContactsMap[replicationKeys[i].Key][j], info.ID) {
					// the node is supposed to hold this key as it's in the 6 closest contacts
					closestContactKeys = append(closestContactKeys, replicationKeys[i].Key)
				}
			}
		}

		logtrace.Info(ctx, "closest contact keys count", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID), "len-rep-keys": len(closestContactKeys)})

		if len(closestContactKeys) == 0 {
			if err := s.updateLastReplicated(ctx, info.ID, to); err != nil {
				logtrace.Error(ctx, "replicate update lastReplicated failed", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
			} else {
				logtrace.Info(ctx, "no closest keys found - replicate update lastReplicated success", logtrace.Fields{logtrace.FieldModule: "p2p", "node": info.IP, "to": to.String(), "closest-contact-keys": 0})
			}

			continue
		}

		// TODO: Check if data size is bigger than 32 MB
		request := &ReplicateDataRequest{
			Keys: closestContactKeys,
		}

		n := &Node{ID: info.ID, IP: info.IP, Port: info.Port}

		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = maxBackOff

		err = backoff.RetryNotify(func() error {
			response, err := s.sendReplicateData(ctx, n, request)
			if err != nil {
				return err
			}

			if response.Status.Result != ResultOk {
				return errors.New(response.Status.ErrMsg)
			}

			return nil
		}, b, func(err error, duration time.Duration) {
			logtrace.Error(ctx, "retrying send replicate data", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "rep-ip": info.IP, "rep-id": string(info.ID), "duration": duration})
		})

		if err != nil {
			logtrace.Error(ctx, "send replicate data failed after retries", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "rep-ip": info.IP, "rep-id": string(info.ID)})
			continue
		}

		// Now closestContactKeys contains all the keys that are in the closest contacts.
		if err := s.updateLastReplicated(ctx, info.ID, to); err != nil {
			logtrace.Error(ctx, "replicate update lastReplicated failed", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
		} else {
			logtrace.Info(ctx, "replicate update lastReplicated success", logtrace.Fields{logtrace.FieldModule: "p2p", "node": info.IP, "to": to.String(), "expected-rep-keys": len(closestContactKeys)})
		}
	}

	logtrace.Info(ctx, "Replication done", logtrace.Fields{logtrace.FieldModule: "p2p"})
}

func (s *DHT) adjustNodeKeys(ctx context.Context, from time.Time, info domain.NodeReplicationInfo) error {
	replicationKeys := s.store.GetKeysForReplication(ctx, from, time.Now().UTC())

	logtrace.Info(ctx, "begin adjusting node keys process for offline node", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID), "total-rep-keys": len(replicationKeys), "from": from.String()})

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
		nodeList := s.ht.closestContactsWithInlcudingNode(Alpha+1, key, updatedIgnored, offNode) // +1 because we want to include the node we are adjusting
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
		logtrace.Info(ctx, "sending adjusted replication keys to node", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID), "adjust-to-node": nodeInfoKey, "to-adjust-keys-len": len(keys)})
		// Retrieve the node object from the key
		node, err := getNodeFromKey(nodeInfoKey)
		if err != nil {
			logtrace.Error(ctx, "Failed to parse node info from key", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "offline-node-ip": info.IP, "offline-node-id": string(info.ID)})
			return fmt.Errorf("failed to parse node info from key: %w", err)
		}

		// TODO: Check if data size is bigger than 32 MB
		request := &ReplicateDataRequest{
			Keys: keys,
		}

		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = maxBackOff

		err = backoff.RetryNotify(func() error {
			response, err := s.sendReplicateData(ctx, node, request)
			if err != nil {
				return err
			}

			if response.Status.Result != ResultOk {
				return errors.New(response.Status.ErrMsg)
			}

			successCount++
			return nil
		}, b, func(err error, duration time.Duration) {
			logtrace.Error(ctx, "retrying send replicate data", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "offline-node-ip": info.IP, "offline-node-id": string(info.ID), "duration": duration})
		})

		if err != nil {
			logtrace.Error(ctx, "send replicate data failed after retries", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "offline-node-ip": info.IP, "offline-node-id": string(info.ID)})
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

	logtrace.Info(ctx, "offline node was successfully adjusted", logtrace.Fields{logtrace.FieldModule: "p2p", "offline-node-ip": info.IP, "offline-node-id": string(info.ID)})

	return nil
}

func isNodeGoneAndShouldBeAdjusted(lastSeen *time.Time, isAlreadyAdjusted bool) bool {
	if lastSeen == nil {
		logtrace.Info(context.Background(), "lastSeen is nil - aborting node adjustment", logtrace.Fields{})
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
				logtrace.Info(ctx, "set isAdjusted to true", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
			}
		}
	}

	logtrace.Info(ctx, "replication node not active, skipping over it.", logtrace.Fields{logtrace.FieldModule: "p2p", "rep-ip": info.IP, "rep-id": string(info.ID)})
}
