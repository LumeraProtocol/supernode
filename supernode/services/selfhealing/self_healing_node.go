package selfhealing

import (
	"github.com/LumeraProtocol/supernode/supernode/node"
)

// SHNodeMaker makes concrete instance of SelfHealingNode
type SHNodeMaker struct {
	node.NodeMaker
}

// MakeNode makes concrete instance of SelfHealingNode
func (maker SHNodeMaker) MakeNode(conn node.ConnectionInterface) node.SuperNodePeerAPIInterface {
	return &SHNode{SelfHealingChallengeInterface: conn.SelfHealingChallenge()}
}

// SHNode (for Self Healing Node) represent supernode connection.
type SHNode struct {
	node.SelfHealingChallengeInterface
}
