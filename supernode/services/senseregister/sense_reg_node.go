package senseregister

import (
	"github.com/LumeraProtocol/supernode/supernode/node"
)

// RegisterSenseNodeMaker makes concrete instance of SenseRegistrationNode
type RegisterSenseNodeMaker struct {
	node.NodeMaker
}

// MakeNode makes concrete instance of SenseRegistrationNode
func (maker RegisterSenseNodeMaker) MakeNode(conn node.ConnectionInterface) node.SuperNodePeerAPIInterface {
	return &SenseRegistrationNode{RegisterSenseInterface: conn.RegisterSense()}
}

// SenseRegistrationNode represent supernode connection.
type SenseRegistrationNode struct {
	node.RegisterSenseInterface
}
