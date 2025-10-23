package kademlia

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// checkNodeActivity keeps track of active nodes: ping periodically, mark inactive on sustained failure,
// unban/re-add on success. Uses bounded concurrency and short per-ping timeouts.
func (s *DHT) checkNodeActivity(ctx context.Context) {
	ticker := time.NewTicker(checkNodeActivityInterval)
	defer ticker.Stop()

	const maxInflight = 32
	sem := make(chan struct{}, maxInflight)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !utils.CheckInternetConnectivity() {
				logtrace.Debug(ctx, "no internet connectivity, not checking node activity", logtrace.Fields{})
				continue
			}

			repInfo, err := s.store.GetAllReplicationInfo(ctx)
			if err != nil {
				logtrace.Error(ctx, "get all replicationInfo failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
				})
				continue
			}

			rand.Shuffle(len(repInfo), func(i, j int) { repInfo[i], repInfo[j] = repInfo[j], repInfo[i] })

			var wg sync.WaitGroup
			for _, info := range repInfo {

				wg.Add(1)
				sem <- struct{}{} // acquire
				go func() {
					defer wg.Done()
					defer func() { <-sem }()

					node := s.makeNode([]byte(info.ID), info.IP, info.Port)

					// Per-ping timeout
					if err := s.pingNode(ctx, node, 5*time.Second); err != nil {
						s.handlePingFailure(ctx, info.Active, node, err)
						return
					}
					s.handlePingSuccess(ctx, info.Active, node)
				}()
			}
			wg.Wait()
		}
	}
}

// ------------- Helper Funcs -----------------------//

func (s *DHT) makeNode(id []byte, ip string, port uint16) *Node {
	n := &Node{ID: id, IP: ip, Port: port}
	n.SetHashedID()
	return n
}

func (s *DHT) pingNode(ctx context.Context, n *Node, timeout time.Duration) error {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := s.newMessage(Ping, n, nil)
	resp, err := s.network.Call(pctx, req, false)
	if err != nil {
		return err
	}
	// Capture remote version from response sender for later gating
	if resp != nil && resp.Sender != nil {
		n.Version = resp.Sender.Version
	}
	return nil
}

func (s *DHT) handlePingFailure(ctx context.Context, wasActive bool, n *Node, err error) {
	logtrace.Debug(ctx, "failed to ping node", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		logtrace.FieldError:  err.Error(),
		"ip":                 n.IP,
		"node_id":            string(n.ID),
	})

	// increment soft-fail counter; only evict when past threshold
	s.ignorelist.IncrementCount(n)
	if wasActive && s.ignorelist.Banned(n) {
		logtrace.Error(ctx, "setting node to inactive", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
			"ip":                 n.IP,
			"node_id":            string(n.ID),
		})

		s.removeNode(ctx, n) // uses HashedID internally
		if uerr := s.store.UpdateIsActive(ctx, string(n.ID), false, false); uerr != nil {
			logtrace.Error(ctx, "failed to update replication info, node is inactive", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  uerr.Error(),
				"ip":                 n.IP,
				"node_id":            string(n.ID),
			})
		}
	}
}

func (s *DHT) handlePingSuccess(ctx context.Context, wasActive bool, n *Node) {
	// clear from ignorelist and ensure presence in routing
	s.ignorelist.Delete(n)

	if !wasActive {
		logtrace.Debug(ctx, "node found to be active again", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"ip":                 n.IP,
			"node_id":            string(n.ID),
		})
		s.addNode(ctx, n)
		if uerr := s.store.UpdateIsActive(ctx, string(n.ID), true, false); uerr != nil {
			logtrace.Error(ctx, "failed to update replication info, node is active", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  uerr.Error(),
				"ip":                 n.IP,
				"node_id":            string(n.ID),
			})
		}
	}

	if uerr := s.store.UpdateLastSeen(ctx, string(n.ID)); uerr != nil {
		logtrace.Error(ctx, "failed to update last seen", logtrace.Fields{
			logtrace.FieldError: uerr.Error(),
			"ip":                n.IP,
			"node_id":           string(n.ID),
		})
	}
}
