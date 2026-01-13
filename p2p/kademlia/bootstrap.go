package kademlia

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	bootstrapRefreshInterval     = 10 * time.Minute
	defaultSuperNodeP2PPort  int = 4445
)

// seed a couple of obviously bad addrs (unless in integration tests)
func (s *DHT) skipBadBootstrapAddrs() {
	isTest := os.Getenv("INTEGRATION_TEST") == "true"
	if isTest {
		return
	}
	s.cache.Set(fmt.Sprintf("%s:%d", "127.0.0.1", s.options.Port), []byte("true"))
	s.cache.Set(fmt.Sprintf("%s:%d", "localhost", s.options.Port), []byte("true"))
}

// parseNode parses "host[:port]" into a Node with basic address hygiene.
// Loopback/private allow-listed only in integration tests.
func (s *DHT) parseNode(extP2P string, selfAddr string) (*Node, error) {
	if extP2P == "" {
		return nil, errors.New("empty address")
	}
	if extP2P == selfAddr {
		return nil, errors.New("self address")
	}
	if _, err := s.cache.Get(extP2P); err == nil {
		return nil, errors.New("skip cached-bad bootstrap addr")
	}

	// Extract IP and port from the address
	var ip string
	var port uint16
	if idx := strings.LastIndex(extP2P, ":"); idx != -1 {
		ip = extP2P[:idx]
		portStr := extP2P[idx+1:]
		if portStr != "" {
			portNum, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, errors.New("invalid port number")
			}
			port = uint16(portNum)
		}
	} else {
		ip = extP2P
		port = uint16(defaultSuperNodeP2PPort)
	}
	if ip == "" {
		return nil, errors.New("empty ip")
	}

	// Hygiene: reject non-routables unless in integration tests
	isTest := os.Getenv("INTEGRATION_TEST") == "true"
	if parsed := net.ParseIP(ip); parsed != nil {
		if parsed.IsUnspecified() || parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() {
			return nil, errors.New("non-routable address")
		}
		if parsed.IsLoopback() && !isTest {
			return nil, errors.New("loopback not allowed")
		}
		if parsed.IsPrivate() && !isTest {
			return nil, errors.New("private address not allowed")
		}
	}

	return &Node{IP: ip, Port: port}, nil
}

// setBootstrapNodesFromConfigVar parses CSV of Lumera addresses and fills s.options.BootstrapNodes.
// Intended for tests / controlled runs (no pings here).
func (s *DHT) setBootstrapNodesFromConfigVar(ctx context.Context, bootstrapNodes string) error {
	nodes := make([]*Node, 0, 8)
	bsNodes := strings.Split(bootstrapNodes, ",")
	for _, bsNode := range bsNodes {
		addr := strings.TrimSpace(bsNode)
		if addr == "" {
			continue
		}
		lumeraAddress, err := ltc.ParseLumeraAddress(addr)
		if err != nil {
			return fmt.Errorf("setBootstrapNodesFromConfigVar: %w", err)
		}
		nodes = append(nodes, &Node{
			ID:   []byte(lumeraAddress.Identity),
			IP:   lumeraAddress.Host,
			Port: lumeraAddress.Port,
		})
	}
	s.options.BootstrapNodes = nodes
	logtrace.Debug(ctx, "Bootstrap nodes set from config var", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"bootstrap_nodes":    nodes,
	})
	return nil
}

// loadBootstrapCandidatesFromChain returns active supernodes (by latest state)
// mapped by "ip:port". No pings here.
func (s *DHT) loadBootstrapCandidatesFromChain(ctx context.Context, selfAddress string) (map[string]*Node, map[[32]byte]struct{}, error) {
	resp, err := s.options.LumeraClient.SuperNode().ListSuperNodes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list supernodes: %w", err)
	}

	activeIDs := make(map[[32]byte]struct{}, len(resp.Supernodes))
	mapNodes := make(map[string]*Node, len(resp.Supernodes))
	for _, sn := range resp.Supernodes {
		if len(sn.States) == 0 {
			continue
		}
		var latestState int32
		var maxStateHeight int64 = -1
		for _, st := range sn.States {
			if st.Height > maxStateHeight {
				maxStateHeight = st.Height
				latestState = int32(st.State)
			}
		}
		if latestState != 1 { // SuperNodeStateActive = 1
			continue
		}

		id := strings.TrimSpace(sn.SupernodeAccount)
		if id == "" {
			continue
		}
		if h, err := utils.Blake3Hash([]byte(id)); err == nil && len(h) == 32 {
			var key [32]byte
			copy(key[:], h)
			activeIDs[key] = struct{}{}
		}

		// latest IP by height
		var latestIP string
		var maxHeight int64 = -1
		for _, ipHist := range sn.PrevIpAddresses {
			if ipHist.Height > maxHeight {
				maxHeight = ipHist.Height
				latestIP = ipHist.Address
			}
		}
		if latestIP == "" {
			logtrace.Warn(ctx, "No valid IP for supernode", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"supernode":          sn.SupernodeAccount,
			})
			continue
		}
		ip := parseSupernodeAddress(latestIP)

		p2pPort := defaultSuperNodeP2PPort
		if sn.P2PPort != "" {
			if port, err := strconv.ParseUint(sn.P2PPort, 10, 16); err == nil {
				p2pPort = int(port)
			}
		}

		full := fmt.Sprintf("%s:%d", ip, p2pPort)
		node, err := s.parseNode(full, selfAddress)
		if err != nil {
			logtrace.Warn(ctx, "Skipping bootstrap candidate (bad addr)", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
				"address":            full,
				"supernode":          sn.SupernodeAccount,
			})
			continue
		}
		node.ID = []byte(id)
		mapNodes[full] = node
	}
	return mapNodes, activeIDs, nil
}

// upsertBootstrapNode inserts/updates replication_info for the discovered node (Active=false).
// No pings or routing decisions here; the health loop will manage Active/LastSeen.
func (s *DHT) upsertBootstrapNode(ctx context.Context, n *Node) error {
	now := time.Now().UTC()
	exists, err := s.store.RecordExists(string(n.ID))
	if err != nil {
		return fmt.Errorf("check replication record: %w", err)
	}
	info := domain.NodeReplicationInfo{
		ID:         n.ID,
		IP:         n.IP,
		Port:       n.Port,
		Active:     false, // health loop flips to true when node responds
		IsAdjusted: false,
		UpdatedAt:  now,
	}
	if exists {
		return s.store.UpdateReplicationInfo(ctx, info)
	}
	return s.store.AddReplicationInfo(ctx, info)
}

// seedRoutingFromDB adds nodes (from replication_info) into the routing table (in-memory only).
// Cheap; improves initial graph connectivity without pings.
func (s *DHT) seedRoutingFromDB(ctx context.Context) {
	repInfo, err := s.store.GetAllReplicationInfo(ctx)
	if err != nil {
		logtrace.Warn(ctx, "seed routing: get replication info failed", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
		return
	}
	for _, ri := range repInfo {
		if len(ri.ID) == 0 || ri.IP == "" || ri.Port == 0 {
			continue
		}
		n := &Node{ID: ri.ID, IP: ri.IP, Port: ri.Port}
		s.addNode(ctx, n)
	}
}

// SyncBootstrapOnce pulls candidates (config var or chain), upserts them into replication_info,
// populates s.options.BootstrapNodes, and seeds the routing table. No pings here.
func (s *DHT) SyncBootstrapOnce(ctx context.Context, bootstrapNodes string) error {
	s.skipBadBootstrapAddrs()

	// If config var provided, prefer that (tests)
	if strings.TrimSpace(bootstrapNodes) != "" {
		if err := s.setBootstrapNodesFromConfigVar(ctx, bootstrapNodes); err != nil {
			return err
		}
		for _, n := range s.options.BootstrapNodes {
			if err := s.upsertBootstrapNode(ctx, n); err != nil {
				logtrace.Warn(ctx, "bootstrap upsert failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					logtrace.FieldError:  err.Error(),
					"node":               n.String(),
				})
			}
		}
		s.seedRoutingFromDB(ctx)
		return nil
	}

	// From chain
	supernodeAddr, err := s.getSupernodeAddress(ctx)
	if err != nil {
		return fmt.Errorf("get supernode address: %s", err)
	}
	selfAddress := fmt.Sprintf("%s:%d", parseSupernodeAddress(supernodeAddr), s.options.Port)

	cands, activeIDs, err := s.loadBootstrapCandidatesFromChain(ctx, selfAddress)
	if err != nil {
		return err
	}

	// Update eligibility gate from chain Active state and prune any peers that slipped in via
	// inbound traffic before the last bootstrap refresh.
	s.setRoutingAllowlist(ctx, activeIDs)
	s.pruneIneligibleRoutingPeers(ctx)

	// Upsert candidates to replication_info
	seen := make(map[string]struct{}, len(cands))
	s.options.BootstrapNodes = s.options.BootstrapNodes[:0]
	for _, n := range cands {
		if err := s.upsertBootstrapNode(ctx, n); err != nil {
			logtrace.Warn(ctx, "bootstrap upsert failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
				"node":               n.String(),
			})
			continue
		}
		if _, ok := seen[string(n.ID)]; ok {
			continue
		}
		s.options.BootstrapNodes = append(s.options.BootstrapNodes, n)
		seen[string(n.ID)] = struct{}{}
	}

	// Seed routing
	s.seedRoutingFromDB(ctx)
	return nil
}

// StartBootstrapRefresher runs SyncBootstrapOnce every 10 minutes (idempotent upserts).
// This keeps replication_info and routing table current as the validator set changes.
func (s *DHT) StartBootstrapRefresher(ctx context.Context, bootstrapNodes string) {
	go func() {
		// Initial sync
		if err := s.SyncBootstrapOnce(ctx, bootstrapNodes); err != nil {
			logtrace.Warn(ctx, "initial bootstrap sync failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				logtrace.FieldError:  err.Error(),
			})
		}
		t := time.NewTicker(bootstrapRefreshInterval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.SyncBootstrapOnce(ctx, bootstrapNodes); err != nil {
					logtrace.Warn(ctx, "periodic bootstrap sync failed", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						logtrace.FieldError:  err.Error(),
					})
				}
			}
		}
	}()
}

// ConfigureBootstrapNodes wires to the new sync/refresher (no pings here).
func (s *DHT) ConfigureBootstrapNodes(ctx context.Context, bootstrapNodes string) error {
	// One-time sync; start refresher in the background
	if err := s.SyncBootstrapOnce(ctx, bootstrapNodes); err != nil {
		return err
	}

	s.StartBootstrapRefresher(ctx, bootstrapNodes)

	return nil
}
