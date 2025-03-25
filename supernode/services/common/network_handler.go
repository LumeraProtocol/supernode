package common

import (
	"context"
	"fmt"
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	supernode "github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/types"
	node "github.com/LumeraProtocol/supernode/supernode/node/supernode"
)

// NetworkHandler common functionality related for SNs Mesh and other interconnections
type NetworkHandler struct {
	task          *SuperNodeTask
	lumeraHandler lumera.Client

	nodeMaker  node.NodeMaker
	NodeClient node.ClientInterface

	acceptedMu sync.Mutex
	Accepted   SuperNodePeerList

	meshedNodes []types.MeshedSuperNode
	// valid only for secondary node
	ConnectedTo *SuperNodePeer

	superNodeAccAddress     string
	minNumberConnectedNodes int
}

// NewNetworkHandler creates instance of NetworkHandler
func NewNetworkHandler(task *SuperNodeTask,
	nodeClient node.ClientInterface,
	nodeMaker node.NodeMaker,
	lc lumera.Client,
	minNumberConnectedNodes int,
) *NetworkHandler {
	return &NetworkHandler{
		task:                    task,
		nodeMaker:               nodeMaker,
		lumeraHandler:           lc,
		NodeClient:              nodeClient,
		minNumberConnectedNodes: minNumberConnectedNodes,
	}
}

// MeshedNodes return SupernodeAccountAddresses of meshed nodes
func (h *NetworkHandler) MeshedNodes() []string {
	var ids []string
	for _, peer := range h.meshedNodes {
		ids = append(ids, peer.NodeID)
	}
	return ids
}

// Session is handshake wallet to supernode
func (h *NetworkHandler) Session(ctx context.Context, isPrimary bool) error {
	if err := h.task.RequiredStatus(StatusTaskStarted); err != nil {
		return err
	}

	<-h.task.NewAction(func(ctx context.Context) error {
		if isPrimary {
			log.WithContext(ctx).Debug("Acts as primary node")
			h.task.UpdateStatus(StatusPrimaryMode)
			return nil
		}

		log.WithContext(ctx).Debug("Acts as secondary node")
		h.task.UpdateStatus(StatusSecondaryMode)

		return nil
	})
	return nil
}

// AcceptedNodes waits for connection supernodes, as soon as there is the required amount returns them.
func (h *NetworkHandler) AcceptedNodes(serverCtx context.Context) (SuperNodePeerList, error) {
	if err := h.task.RequiredStatus(StatusPrimaryMode); err != nil {
		return nil, fmt.Errorf("AcceptedNodes: %w", err)
	}

	<-h.task.NewAction(func(ctx context.Context) error {
		log.WithContext(ctx).Debug("Waiting for supernodes to connect")

		sub := h.task.SubscribeStatus()
		for {
			select {
			case <-serverCtx.Done():
				return nil
			case <-ctx.Done():
				return nil
			case status := <-sub():
				if status.Is(StatusConnected) {
					return nil
				}
			}
		}
	})
	return h.Accepted, nil
}

// SessionNode accepts secondary node
func (h *NetworkHandler) SessionNode(_ context.Context, nodeID string) error {
	h.acceptedMu.Lock()
	defer h.acceptedMu.Unlock()

	if err := h.task.RequiredStatus(StatusPrimaryMode); err != nil {
		return fmt.Errorf("SessionNode: %w", err)
	}

	var err error

	<-h.task.NewAction(func(ctx context.Context) error {
		if node := h.Accepted.ByID(nodeID); node != nil {
			log.WithContext(ctx).WithField("nodeID", nodeID).Errorf("node is already registered")
			err = errors.Errorf("node %q is already registered", nodeID)
			return nil
		}

		var someNode *SuperNodePeer
		someNode, err = h.toSupernodePeer(ctx, nodeID)
		if err != nil {
			log.WithContext(ctx).WithField("nodeID", nodeID).WithError(err).Errorf("get node by extID")
			err = errors.Errorf("get node by extID %s: %w", nodeID, err)
			return nil
		}
		h.Accepted.Add(someNode)

		log.WithContext(ctx).WithField("nodeID", nodeID).Debug("Accept secondary node")

		if len(h.Accepted) >= h.minNumberConnectedNodes {
			h.task.UpdateStatus(StatusConnected)
		}
		return nil
	})
	return err
}

// ConnectTo connects to primary node
func (h *NetworkHandler) ConnectTo(_ context.Context, nodeID, sessID string) error {
	if err := h.task.RequiredStatus(StatusSecondaryMode); err != nil {
		return err
	}

	var err error

	<-h.task.NewAction(func(ctx context.Context) error {
		var someNode *SuperNodePeer
		someNode, err = h.toSupernodePeer(ctx, nodeID)
		if err != nil {
			log.WithContext(ctx).WithField("nodeID", nodeID).WithError(err).Errorf("get node by extID")
			return nil
		}

		if err := someNode.Connect(ctx); err != nil {
			log.WithContext(ctx).WithField("nodeID", nodeID).WithError(err).Errorf("connect to node")
			return nil
		}

		if err = someNode.Session(ctx, h.superNodeAccAddress, sessID); err != nil {
			log.WithContext(ctx).WithField("sessID", sessID).WithField("sn-acc-address", h.superNodeAccAddress).WithError(err).Errorf("handshake with peer")
			return nil
		}

		h.ConnectedTo = someNode
		h.task.UpdateStatus(StatusConnected)
		return nil
	})
	return err
}

// MeshNodes to set info of all meshed supernodes - that will be to send
func (h *NetworkHandler) MeshNodes(_ context.Context, meshedNodes []types.MeshedSuperNode) error {
	if err := h.task.RequiredStatus(StatusConnected); err != nil {
		return err
	}
	h.meshedNodes = meshedNodes

	return nil
}

// CheckNodeInMeshedNodes checks if the node is in the active mesh (by nodeID)
func (h *NetworkHandler) CheckNodeInMeshedNodes(nodeID string) error {
	if h.meshedNodes == nil {
		return errors.New("nil meshedNodes")
	}

	for _, node := range h.meshedNodes {
		if node.NodeID == nodeID {
			return nil
		}
	}

	return errors.New("nodeID not found")
}

// toSupernodePeer returns information about SN by its account-address
func (h *NetworkHandler) toSupernodePeer(ctx context.Context, supernodeAccountAddress string) (*SuperNodePeer, error) {
	sn, err := h.lumeraHandler.SuperNode().GetSupernodeBySupernodeAddress(ctx, supernodeAccountAddress)
	if err != nil {
		return nil, err
	}

	supernodeIP, err := supernode.GetLatestIP(sn)
	if err != nil {
		return nil, err
	}

	someNode := NewSuperNode(h.NodeClient, supernodeIP, supernodeAccountAddress, h.nodeMaker)
	return someNode, nil
}

// Connect connects to grpc Server and setup pointer to concrete client wrapper
func (node *SuperNodePeer) Connect(ctx context.Context) error {
	connCtx, connCancel := context.WithTimeout(ctx, defaultConnectToNodeTimeout)
	defer connCancel()

	conn, err := node.ClientInterface.Connect(connCtx, node.Address)
	if err != nil {
		return err
	}

	node.ConnectionInterface = conn
	node.SuperNodePeerAPIInterface = node.MakeNode(conn)
	return nil
}

func (h *NetworkHandler) CloseSNsConnections(ctx context.Context) error {
	for _, node := range h.Accepted {
		if node.ConnectionInterface != nil {
			if err := node.Close(); err != nil {
				log.WithContext(ctx).WithError(err).Errorf("close connection to node %s", node.ID)
			}
		} else {
			log.WithContext(ctx).Errorf("node %s has no connection", node.ID)
		}

	}

	if h.ConnectedTo != nil {
		if err := h.ConnectedTo.Close(); err != nil {
			log.WithContext(ctx).WithError(err).Errorf("close connection to node %s", h.ConnectedTo.ID)
		}
	}

	return nil
}

func (h *NetworkHandler) IsPrimary() bool {
	return h.ConnectedTo == nil
}
