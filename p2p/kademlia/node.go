package kademlia

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

// Node is the over-the-wire representation of a node
type Node struct {
	// id is a 32 byte unique identifier
	ID []byte `json:"id,omitempty"`

	// ip address of the node
	IP string `json:"ip,omitempty"`

	// port of the node
	Port uint16 `json:"port,omitempty"`

	HashedID []byte
}

// SetHashedID sets hash of ID
func (s *Node) SetHashedID() {
	if len(s.HashedID) == 0 {
		s.HashedID, _ = utils.Blake3Hash(s.ID)
	}
}

// String returns string format
func (s *Node) String() string {
	return fmt.Sprintf("%v-%v:%d", string(s.ID), s.IP, s.Port)
}

// NodeList is used in order to sort a list of nodes
type NodeList struct {
	Nodes []*Node

	// Comparator is the id to compare to
	Comparator []byte

	Mux sync.RWMutex

	debug bool
}

// String returns the dump information for node list
func (s *NodeList) String() string {
	s.Mux.RLock()
	defer s.Mux.RUnlock()

	var nodes []string
	for _, node := range s.Nodes {
		nodes = append(nodes, node.String())
	}
	return strings.Join(nodes, ",")
}

// DelNode deletes a node from list
func (s *NodeList) DelNode(node *Node) {
	s.Mux.Lock()
	defer s.Mux.Unlock()

	for i := 0; i < s.Len(); i++ {
		if bytes.Equal(s.Nodes[i].ID, node.ID) {
			newList := s.Nodes[:i]
			if i+1 < s.Len() {
				newList = append(newList, s.Nodes[i+1:]...)
			}

			s.Nodes = newList

			return
		}
	}
}

func haveAllNodes(nodesA []*Node, nodesB []*Node) bool {
	for _, nodeA := range nodesA {
		found := false
		for _, nodeB := range nodesB {
			if bytes.Equal(nodeA.ID, nodeB.ID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// Exists return true if the node is already there
func (s *NodeList) Exists(node *Node) bool {
	s.Mux.RLock()
	defer s.Mux.RUnlock()

	return s.exists(node)
}

func (s *NodeList) exists(node *Node) bool {
	for i := 0; i < len(s.Nodes); i++ {
		if bytes.Equal(s.Nodes[i].ID, node.ID) {
			return true
		}
	}
	return false
}

// AddNodes appends the nodes to node list if it's not existed
func (s *NodeList) AddNodes(nodes []*Node) {
	s.Mux.Lock()
	defer s.Mux.Unlock()

	for _, node := range nodes {
		if !s.exists(node) {
			s.Nodes = append(s.Nodes, node)
		}
	}
}

// Len check length
func (s *NodeList) Len() int {
	return len(s.Nodes)
}

// AddFirst adds a node to the first position of the list.
func (s *NodeList) AddFirst(node *Node) {
	s.Mux.Lock()         // lock for writing
	defer s.Mux.Unlock() // ensure the lock is released no matter what

	// Prepend the node to the slice
	s.Nodes = append([]*Node{node}, s.Nodes...)
}

// TopN keeps top N nodes
func (s *NodeList) TopN(n int) {
	s.Mux.Lock()
	defer s.Mux.Unlock()

	if s.Len() > n {
		s.Nodes = s.Nodes[:n]
	} else {
		s.Nodes = s.Nodes[:s.Len()]
	}
}

func (s *NodeList) distance(id1, id2 []byte) *big.Int {
	o1 := new(big.Int).SetBytes(id1)
	o2 := new(big.Int).SetBytes(id2)

	return new(big.Int).Xor(o1, o2)
}

// Sort sorts nodes
func (s *NodeList) Sort() {
	if len(s.Comparator) == 0 {
		logtrace.Warn(context.Background(), "sort without comparator", logtrace.Fields{logtrace.FieldModule: "p2p"})
	}

	s.Mux.Lock()
	defer s.Mux.Unlock()
	// Sort using the precomputed distances
	sort.Sort(s)
}

// Swap swap two nodes
func (s *NodeList) Swap(i, j int) {
	if i >= 0 && i < s.Len() && j >= 0 && j < s.Len() {
		s.Nodes[i], s.Nodes[j] = s.Nodes[j], s.Nodes[i]
	}
}

// Less compare two nodes
func (s *NodeList) Less(i, j int) bool {
	if i >= 0 && i < s.Len() && j >= 0 && j < s.Len() {
		id := s.distance(s.Nodes[i].HashedID, s.Comparator)
		jd := s.distance(s.Nodes[j].HashedID, s.Comparator)

		return id.Cmp(jd) == -1
	}

	return false
}

// NodeIDs returns the dump information for node list
func (s *NodeList) NodeIDs() [][]byte {
	s.Mux.RLock()
	defer s.Mux.RUnlock()

	toRet := make([][]byte, len(s.Nodes))
	for i := 0; i < len(s.Nodes); i++ {
		toRet = append(toRet, s.Nodes[i].ID)
	}

	return toRet
}

// NodeIPs returns the dump information for node list
func (s *NodeList) NodeIPs() []string {
	s.Mux.RLock()
	defer s.Mux.RUnlock()

	toRet := make([]string, len(s.Nodes))
	for i := 0; i < len(s.Nodes); i++ {
		toRet = append(toRet, s.Nodes[i].IP)
	}

	return toRet
}
