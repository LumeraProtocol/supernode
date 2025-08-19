package kademlia

import (
	"bytes"
	"context"
	"encoding/hex"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

func (s *DHT) startDisabledKeysCleanupWorker(ctx context.Context) error {
	logtrace.Info(ctx, "disabled keys cleanup worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultCleanupInterval):
			s.cleanupDisabledKeys(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing disabled keys cleanup worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return nil
		}
	}
}

func (s *DHT) cleanupDisabledKeys(ctx context.Context) error {
	if s.metaStore == nil {
		return nil
	}

	from := time.Now().UTC().Add(-1 * defaultDisabledKeyExpirationInterval)
	disabledKeys, err := s.metaStore.GetDisabledKeys(from)
	if err != nil {
		return errors.Errorf("get disabled keys: %w", err)
	}

	for i := 0; i < len(disabledKeys); i++ {
		dec, err := hex.DecodeString(disabledKeys[i].Key)
		if err != nil {
			logtrace.Error(ctx, "decode disabled key failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
			continue
		}
		s.metaStore.Delete(ctx, dec)
	}

	return nil
}

func (s *DHT) startCleanupRedundantDataWorker(ctx context.Context) {
	logtrace.Info(ctx, "redundant data cleanup worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultRedundantDataCleanupInterval):
			s.cleanupRedundantDataWorker(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing disabled keys cleanup worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return
		}
	}
}

func (s *DHT) cleanupRedundantDataWorker(ctx context.Context) {
	from := time.Now().AddDate(-5, 0, 0) // 5 years ago

	logtrace.Info(ctx, "getting all possible replication keys past five years", logtrace.Fields{logtrace.FieldModule: "p2p", "from": from})
	to := time.Now().UTC()
	replicationKeys := s.store.GetKeysForReplication(ctx, from, to)

	ignores := s.ignorelist.ToNodeList()
	self := &Node{ID: s.ht.self.ID, IP: s.externalIP, Port: s.ht.self.Port}
	self.SetHashedID()

	closestContactsMap := make(map[string][][]byte)

	for i := 0; i < len(replicationKeys); i++ {
		decKey, _ := hex.DecodeString(replicationKeys[i].Key)
		nodes := s.ht.closestContactsWithInlcudingNode(Alpha, decKey, ignores, self)
		closestContactsMap[replicationKeys[i].Key] = nodes.NodeIDs()
	}

	insertKeys := make([]domain.DelKey, 0)
	removeKeys := make([]domain.DelKey, 0)
	for key, closestContacts := range closestContactsMap {
		if len(closestContacts) < Alpha {
			logtrace.Info(ctx, "not enough contacts to replicate", logtrace.Fields{logtrace.FieldModule: "p2p", "key": key, "closest contacts": closestContacts})
			continue
		}

		found := false
		for _, contact := range closestContacts {
			if bytes.Equal(contact, self.ID) {
				found = true
			}
		}

		delKey := domain.DelKey{
			Key:       key,
			CreatedAt: time.Now(),
			Count:     1,
		}

		if !found {
			insertKeys = append(insertKeys, delKey)
		} else {
			removeKeys = append(removeKeys, delKey)
		}
	}

	if len(insertKeys) > 0 {
		if err := s.metaStore.BatchInsertDelKeys(ctx, insertKeys); err != nil {
			logtrace.Error(ctx, "insert keys failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
			return
		}

		logtrace.Info(ctx, "insert del keys success", logtrace.Fields{logtrace.FieldModule: "p2p", "count-del-keys": len(insertKeys)})
	} else {
		logtrace.Info(ctx, "No redundant key found to be stored in the storage", logtrace.Fields{logtrace.FieldModule: "p2p"})
	}

	if len(removeKeys) > 0 {
		if err := s.metaStore.BatchDeleteDelKeys(ctx, removeKeys); err != nil {
			logtrace.Error(ctx, "batch delete del-keys failed", logtrace.Fields{logtrace.FieldError: err.Error()})
			return
		}
	}

}

func (s *DHT) startDeleteDataWorker(ctx context.Context) {
	logtrace.Info(ctx, "start delete data worker", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultDeleteDataInterval):
			s.deleteRedundantData(ctx)
		case <-ctx.Done():
			logtrace.Error(ctx, "closing delete data worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return
		}
	}
}

func (s *DHT) deleteRedundantData(ctx context.Context) {
	const batchSize = 100

	delKeys, err := s.metaStore.GetAllToDelKeys(delKeysCountThreshold)
	if err != nil {
		logtrace.Error(ctx, "get all to delete keys failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
		return
	}

	for len(delKeys) > 0 {
		// Check the available disk space
		isLow, err := utils.CheckDiskSpace(lowSpaceThreshold)
		if err != nil {
			logtrace.Error(ctx, "check disk space failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
			break
		}

		if !isLow {
			// Disk space is sufficient, stop deletion
			break
		}

		// Determine the size of the next batch
		batchEnd := batchSize
		if len(delKeys) < batchSize {
			batchEnd = len(delKeys)
		}

		// Prepare the batch for deletion
		keysToDelete := make([]string, 0, batchEnd)
		for _, delKey := range delKeys[:batchEnd] {
			keysToDelete = append(keysToDelete, delKey.Key)
		}

		// Perform the deletion
		if err := s.store.BatchDeleteRecords(keysToDelete); err != nil {
			logtrace.Error(ctx, "batch delete records failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error()})
			break
		}

		// Update the remaining keys to be deleted
		delKeys = delKeys[batchEnd:]
	}
}
