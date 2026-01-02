package kademlia

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	// IterateStore - iterative store the data
	IterateStore = iota
	// IterateFindNode - iterative find node
	IterateFindNode
	// IterateFindValue - iterative find value
	IterateFindValue
)

const (
	// Alpha - a small number representing the degree of parallelism in network calls
	Alpha = 6

	// B - the size in bits of the keys used to identify nodes and store and
	// retrieve data; in basic Kademlia this is 256, the length of a SHA3-256
	B = 256

	// K - the maximum number of contacts stored in a bucket
	K = 20
)

// HashTable represents the hashtable state
type HashTable struct {
	// The ID of the queries node
	self *Node

	// Route table a list of all known nodes in the network
	// Nodes within buckets are sorted by least recently seen e.g.
	// [ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ]
	//  ^                                                           ^
	//  └ Least recently seen                    Most recently seen ┘
	routeTable [][]*Node // 256x20

	// mutex for route table
	mutex    sync.RWMutex
	refMutex sync.RWMutex

	// refresh time for every bucket
	refreshers []time.Time
}

// resetRefreshTime - reset the refresh time
func (ht *HashTable) resetRefreshTime(bucket int) {
	ht.refMutex.Lock()
	defer ht.refMutex.Unlock()
	ht.refreshers[bucket] = time.Now().UTC()
}

// refreshNode makes the node to the end
func (ht *HashTable) refreshNode(id []byte) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	// bucket index of the node
	index := ht.bucketIndex(ht.self.HashedID, id)
	// point to the bucket
	bucket := ht.routeTable[index]

	var offset int
	found := false
	// find the position of the node
	for i, v := range bucket {
		if bytes.Equal(v.HashedID, id) {
			offset = i
			found = true
			break
		}
	}

	// Safety check: only rotate if node was actually found
	if !found {
		// Node not in bucket, nothing to refresh
		return
	}

	// makes the node to the end

	if offset < 0 {
		return
	} // nothing to refresh

	current := bucket[offset]
	bucket = append(bucket[:offset], bucket[offset+1:]...)
	bucket = append(bucket, current)
	ht.routeTable[index] = bucket
}

// refreshTime returns the refresh time for bucket
func (ht *HashTable) refreshTime(bucket int) time.Time {
	ht.refMutex.RLock()
	defer ht.refMutex.RUnlock()

	return ht.refreshers[bucket]
}

// randomIDFromBucket returns a random id based on the bucket index and self id
func (ht *HashTable) randomIDFromBucket(bucket int) []byte {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()

	// set the new ID to to be equal in every byte up to
	// the byte of the first different bit in the bucket
	index := bucket / 8
	var id []byte
	for i := 0; i < index; i++ {
		id = append(id, ht.self.HashedID[i])
	}
	start := bucket % 8

	var first byte
	// check each bit from left to right in order
	for i := 0; i < 8; i++ {
		var bit bool
		if i < start {
			bit = hasBit(ht.self.HashedID[index], uint(i))
		} else {
			nBig, _ := rand.Int(rand.Reader, big.NewInt(2))
			n := nBig.Int64()

			bit = n == 1
		}
		if bit {
			first |= 1 << (7 - i)
		}
	}
	id = append(id, first)

	// randomize each remaining byte
	for i := index + 1; i < B/8; i++ {
		nBig, _ := rand.Int(rand.Reader, big.NewInt(256))
		n := nBig.Int64()

		id = append(id, byte(n))
	}

	return id
}

// Count returns the number of nodes in route table
func (ht *HashTable) totalCount() int {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()

	var num int
	for _, v := range ht.routeTable {
		num += len(v)
	}
	return num
}

// Simple helper function to determine the value of a particular
// bit in a byte by index
//
// number:  1
// bits:    00000001
// pos:     01234567
func hasBit(n byte, pos uint) bool {
	val := n & (1 << (7 - pos)) // check bits from left to right (7 - pos)
	return (val > 0)
}

// NewHashTable returns a new hashtable
func NewHashTable(options *Options) (*HashTable, error) {
	ht := &HashTable{
		self:       &Node{IP: options.IP, Port: options.Port},
		refreshers: make([]time.Time, B),
		routeTable: make([][]*Node, B),
	}
	if options.ID != nil {
		ht.self.ID = options.ID
	} else {
		return nil, errors.New("id is nil")
	}
	ht.self.SetHashedID()

	// init buckets with capacity K and refresh times
	for i := 0; i < B; i++ {
		ht.routeTable[i] = make([]*Node, 0, K)
		ht.resetRefreshTime(i)
	}
	return ht, nil
}

// --- identity normalization for routing distance
func ensureHashedTarget(target []byte) []byte {
	if len(target) != 32 {
		h, _ := utils.Blake3Hash(target)
		return h
	}
	return target
}

type hashedIDKey [32]byte

func hashedKeyFromNode(node *Node) (hashedIDKey, bool) {
	var k hashedIDKey
	if node == nil {
		return k, false
	}

	hashedID := node.HashedID
	if len(hashedID) != 32 {
		if len(node.ID) == 0 {
			return k, false
		}
		h, err := utils.Blake3Hash(node.ID)
		if err != nil || len(h) != 32 {
			return k, false
		}
		hashedID = h
	}

	copy(k[:], hashedID)
	return k, true
}

type hashedIDSet map[hashedIDKey]struct{}

func hashedIDSetFromNodes(nodes []*Node) hashedIDSet {
	if len(nodes) == 0 {
		return nil
	}
	set := make(hashedIDSet, len(nodes))
	for _, n := range nodes {
		if k, ok := hashedKeyFromNode(n); ok {
			set[k] = struct{}{}
		}
	}
	return set
}

func (s hashedIDSet) contains(hashedID []byte) bool {
	if len(s) == 0 || len(hashedID) != 32 {
		return false
	}
	var k hashedIDKey
	copy(k[:], hashedID)
	_, ok := s[k]
	return ok
}

func hashedKeysFromNodes(nodes []*Node) []hashedIDKey {
	if len(nodes) == 0 {
		return nil
	}
	keys := make([]hashedIDKey, 0, len(nodes))
	for _, n := range nodes {
		if k, ok := hashedKeyFromNode(n); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func hasHashedID(hashedID []byte, keys []hashedIDKey) bool {
	if len(keys) == 0 || len(hashedID) != 32 {
		return false
	}
	for i := range keys {
		if bytes.Equal(hashedID, keys[i][:]) {
			return true
		}
	}
	return false
}

func hasNodeWithHashedID(nodes []*Node, hashedID []byte) bool {
	if len(hashedID) != 32 {
		return false
	}
	for _, n := range nodes {
		if n == nil || len(n.HashedID) != 32 {
			continue
		}
		if bytes.Equal(n.HashedID, hashedID) {
			return true
		}
	}
	return false
}

// hasBucketNode: compare on HashedID
func (ht *HashTable) hasBucketNode(bucket int, hashedID []byte) bool {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	for _, node := range ht.routeTable[bucket] {
		if bytes.Equal(node.HashedID, hashedID) {
			return true
		}
	}
	return false
}

// hasNode: compare on HashedID
func (ht *HashTable) hasNode(hashedID []byte) bool {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	for _, bucket := range ht.routeTable {
		for _, node := range bucket {
			if bytes.Equal(node.HashedID, hashedID) {
				return true
			}
		}
	}
	return false
}

// RemoveNode: compare on HashedID
func (ht *HashTable) RemoveNode(index int, hashedID []byte) bool {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	bucket := ht.routeTable[index]
	for i, node := range bucket {
		if bytes.Equal(node.HashedID, hashedID) {
			if i+1 < len(bucket) {
				bucket = append(bucket[:i], bucket[i+1:]...)
			} else {
				bucket = bucket[:i]
			}
			ht.routeTable[index] = bucket
			return true
		}
	}
	return false
}

// closestContacts: use HashedID in ignored-map
func (ht *HashTable) closestContacts(num int, target []byte, ignoredNodes []*Node) (*NodeList, int) {
	hashedTarget := ensureHashedTarget(target)
	ignoredKeys := hashedKeysFromNodes(ignoredNodes)

	ht.mutex.RLock()
	total := 0
	for _, bucket := range ht.routeTable {
		total += len(bucket)
	}
	nodes := make([]*Node, 0, total)
	counter := 0
	for _, bucket := range ht.routeTable {
		for _, node := range bucket {
			counter++
			if node == nil {
				continue
			}
			if hasHashedID(node.HashedID, ignoredKeys) {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	ht.mutex.RUnlock()

	nl := &NodeList{Comparator: hashedTarget, Nodes: nodes}
	nl.Sort()
	nl.TopN(num)
	return nl, counter
}

// keep an alias for old callers; fix typo in new name
func (ht *HashTable) closestContactsWithIncludingNode(num int, target []byte, ignoredNodes []*Node, includeNode *Node) *NodeList {
	hashedTarget := ensureHashedTarget(target)
	ignoredKeys := hashedKeysFromNodes(ignoredNodes)

	ht.mutex.RLock()
	total := 0
	for _, bucket := range ht.routeTable {
		total += len(bucket)
	}
	nodes := make([]*Node, 0, total+1)
	for _, bucket := range ht.routeTable {
		for _, node := range bucket {
			if node == nil {
				continue
			}
			if hasHashedID(node.HashedID, ignoredKeys) {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	ht.mutex.RUnlock()

	if includeNode != nil {
		includeNode.SetHashedID()
		if !hasNodeWithHashedID(nodes, includeNode.HashedID) {
			nodes = append(nodes, includeNode)
		}
	}

	nl := &NodeList{Comparator: hashedTarget, Nodes: nodes}
	nl.Sort()
	nl.TopN(num)
	return nl
}

// closestContactsWithIncludingNodeWithIgnoredSet is an optimized variant for batch callers that
// can precompute the ignored hashed-ID set once and reuse it across many lookups.
func (ht *HashTable) closestContactsWithIncludingNodeWithIgnoredSet(num int, target []byte, ignoredSet hashedIDSet, includeNode *Node) *NodeList {
	hashedTarget := ensureHashedTarget(target)

	ht.mutex.RLock()
	total := 0
	for _, bucket := range ht.routeTable {
		total += len(bucket)
	}
	nodes := make([]*Node, 0, total+1)
	for _, bucket := range ht.routeTable {
		for _, node := range bucket {
			if node == nil {
				continue
			}
			if ignoredSet.contains(node.HashedID) {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	ht.mutex.RUnlock()

	if includeNode != nil {
		includeNode.SetHashedID()
		if !hasNodeWithHashedID(nodes, includeNode.HashedID) {
			nodes = append(nodes, includeNode)
		}
	}

	nl := &NodeList{Comparator: hashedTarget, Nodes: nodes}
	nl.Sort()
	nl.TopN(num)
	return nl
}

func (ht *HashTable) closestContactsWithIncludingNodeList(num int, target []byte, ignoredNodes []*Node, nodesToInclude []*Node) *NodeList {
	hashedTarget := ensureHashedTarget(target)
	ignoredKeys := hashedKeysFromNodes(ignoredNodes)

	ht.mutex.RLock()
	total := 0
	for _, bucket := range ht.routeTable {
		total += len(bucket)
	}
	nodes := make([]*Node, 0, total+len(nodesToInclude))
	for _, bucket := range ht.routeTable {
		for _, node := range bucket {
			if node == nil {
				continue
			}
			if hasHashedID(node.HashedID, ignoredKeys) {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	ht.mutex.RUnlock()

	if len(nodesToInclude) > 0 {
		for _, node := range nodesToInclude {
			if node == nil {
				continue
			}
			node.SetHashedID()
			if hasNodeWithHashedID(nodes, node.HashedID) {
				continue
			}
			nodes = append(nodes, node)
		}
	}

	nl := &NodeList{Comparator: hashedTarget, Nodes: nodes}
	nl.Sort()
	nl.TopN(num)
	return nl
}

// bucketIndex: guard length
func (*HashTable) bucketIndex(id1, id2 []byte) int {
	if len(id1) != 32 || len(id2) != 32 {
		// defensive: treat as far (top bucket)
		return B - 1
	}
	for j := 0; j < len(id1); j++ {
		xor := id1[j] ^ id2[j]
		for i := 0; i < 8; i++ {
			if hasBit(xor, uint(i)) {
				byteIndex := j * 8
				bitIndex := i
				return B - (byteIndex + bitIndex) - 1
			}
		}
	}
	return 0 // identical IDs
}

// nodes: optional safe snapshot (shallow copy of slice; caller must not mutate Nodes)
func (ht *HashTable) nodes() []*Node {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	total := 0
	for _, b := range ht.routeTable {
		total += len(b)
	}
	out := make([]*Node, 0, total)
	for _, b := range ht.routeTable {
		out = append(out, b...)
	}
	return out
}

func (ht *HashTable) peersCount() int {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	total := 0
	for _, b := range ht.routeTable {
		total += len(b)
	}
	return total
}

// newRandomID: match B=256 (32 bytes)
func newRandomID() ([]byte, error) {
	id := make([]byte, B/8)
	_, err := rand.Read(id)
	return id, err
}

// AddOrUpdate inserts or refreshes LRU for a node (updates IP/Port on change).
func (ht *HashTable) AddOrUpdate(n *Node) {
	if n == nil || len(n.ID) == 0 {
		return
	}
	n.SetHashedID()

	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	bi := ht.bucketIndex(ht.self.HashedID, n.HashedID)
	b := ht.routeTable[bi]

	// Refresh if present
	for i, v := range b {
		if bytes.Equal(v.HashedID, n.HashedID) {
			v.IP, v.Port = n.IP, n.Port // refresh coordinates
			cur := b[i]
			b = append(b[:i], b[i+1:]...)
			b = append(b, cur)
			ht.routeTable[bi] = b
			ht.resetRefreshTime(bi)
			return
		}
	}
	// Insert if space
	if len(b) < K {
		ht.routeTable[bi] = append(b, n)
		ht.resetRefreshTime(bi)
		return
	}
	// Else: bucket full; caller should ping LRU and then call ReplaceLRUIf(...)
}

func (ht *HashTable) LRUForBucket(n *Node) *Node {
	if n == nil || len(n.HashedID) != 32 {
		return nil
	}
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	bi := ht.bucketIndex(ht.self.HashedID, n.HashedID)
	if len(ht.routeTable[bi]) == 0 {
		return nil
	}
	return ht.routeTable[bi][0]
}

func (ht *HashTable) ReplaceLRUIf(n, lru *Node) bool {
	if n == nil || lru == nil {
		return false
	}
	n.SetHashedID()
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	bi := ht.bucketIndex(ht.self.HashedID, n.HashedID)
	b := ht.routeTable[bi]
	if len(b) == 0 || !bytes.Equal(b[0].HashedID, lru.HashedID) {
		return false // LRU moved meanwhile
	}
	b = b[1:]
	b = append(b, n)
	ht.routeTable[bi] = b
	ht.resetRefreshTime(bi)
	return true
}
