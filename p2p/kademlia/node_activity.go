package kademlia

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

// checkNodeActivity keeps track of active nodes - the idea here is to ping nodes periodically and mark them as inactive if they don't respond
func (s *DHT) checkNodeActivity(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkNodeActivityInterval): // Adjust the interval as needed
			if !utils.CheckInternetConnectivity() {
				logtrace.Info(ctx, "no internet connectivity, not checking node activity", logtrace.Fields{})
			} else {
				repInfo, err := s.store.GetAllReplicationInfo(ctx)
				if err != nil {
					logtrace.Error(ctx, "get all replicationInfo failed", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						logtrace.FieldError:  err.Error(),
					})
				}

				for _, info := range repInfo {
					// new a ping request message
					node := &Node{
						ID:   []byte(info.ID),
						IP:   info.IP,
						Port: info.Port,
					}

					request := s.newMessage(Ping, node, nil)

					// invoke the request and handle the response
					_, err := s.network.Call(ctx, request, false)
					if err != nil {
						logtrace.Debug(ctx, "failed to ping node", logtrace.Fields{
							logtrace.FieldModule: "p2p",
							logtrace.FieldError:  err.Error(),
							"ip":                 info.IP,
							"node_id":            string(info.ID),
						})
						if info.Active {
							logtrace.Error(ctx, "setting node to inactive", logtrace.Fields{
								logtrace.FieldModule: "p2p",
								logtrace.FieldError:  err.Error(),
								"ip":                 info.IP,
								"node_id":            string(info.ID),
							})

							// add node to ignore list
							// we maintain this list to avoid pinging nodes that are not responding
							s.ignorelist.IncrementCount(node)
							// remove from route table
							s.removeNode(ctx, node)

							// mark node as inactive in database
							if err := s.store.UpdateIsActive(ctx, string(info.ID), false, false); err != nil {
								logtrace.Error(ctx, "failed to update replication info, node is inactive", logtrace.Fields{
									logtrace.FieldModule: "p2p",
									logtrace.FieldError:  err.Error(),
									"ip":                 info.IP,
									"node_id":            string(info.ID),
								})
							}
						}

					} else if err == nil {
						// remove node from ignore list
						s.ignorelist.Delete(node)

						if !info.Active {
							logtrace.Info(ctx, "node found to be active again", logtrace.Fields{
								logtrace.FieldModule: "p2p",
								"ip":                 info.IP,
								"node_id":            string(info.ID),
							})
							// add node adds in the route table
							s.addNode(ctx, node)
							if err := s.store.UpdateIsActive(ctx, string(info.ID), true, false); err != nil {
								logtrace.Error(ctx, "failed to update replication info, node is inactive", logtrace.Fields{
									logtrace.FieldModule: "p2p",
									logtrace.FieldError:  err.Error(),
									"ip":                 info.IP,
									"node_id":            string(info.ID),
								})
							}
						}

						if err := s.store.UpdateLastSeen(ctx, string(info.ID)); err != nil {
							logtrace.Error(ctx, "failed to update last seen", logtrace.Fields{
								logtrace.FieldError: err.Error(),
								"ip":                info.IP,
								"node_id":           string(info.ID),
							})
						}
					}
				}
			}
		}
	}
}
