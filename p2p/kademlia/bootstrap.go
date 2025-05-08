package kademlia

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/utils"

	"github.com/LumeraProtocol/supernode/pkg/log"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
)

const (
	bootstrapRetryInterval  = 10
	badAddrExpiryHours      = 12
	defaultSuperNodeP2PPort = 4444
)

func (s *DHT) skipBadBootstrapAddrs() {
	//skipAddress1 := fmt.Sprintf("%s:%d", "127.0.0.1", s.options.Port)
	//skipAddress2 := fmt.Sprintf("%s:%d", "localhost", s.options.Port)
	//s.cache.Set(skipAddress1, []byte("true"))
	//s.cache.Set(skipAddress2, []byte("true"))
}

func (s *DHT) parseNode(extP2P string, selfAddr string) (*Node, error) {
	if extP2P == "" {
		return nil, errors.New("empty address")
	}

	/*if strings.Contains(extP2P, "0.0.0.0") {
		fmt.Println("skippping node")
		return nil, errors.New("invalid address")
	}*/

	if extP2P == selfAddr {
		return nil, errors.New("self address")
	}

	if _, err := s.cache.Get(extP2P); err == nil {
		return nil, errors.New("configure: skip bad p2p boostrap addr")
	}

	// Extract IP and port from the address
	var ip string
	var port uint16

	if idx := strings.LastIndex(extP2P, ":"); idx != -1 {
		ip = extP2P[:idx]
		portStr := extP2P[idx+1:]

		// If we have a port in the address, parse it
		if portStr != "" {
			portNum, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, errors.New("invalid port number")
			}

			// For system testing, use port+1 if SYSTEM_TEST=true
			if os.Getenv("SYSTEM_TEST") == "true" {
				port = uint16(portNum) + 1
				log.P2P().WithField("original_port", portNum).
					WithField("adjusted_port", port).
					Info("Using port+1 for system testing")
			} else {
				// For normal P2P operation, always use the default port
				port = defaultNetworkPort
			}
		}
	} else {
		// No port in the address
		ip = extP2P
		port = defaultNetworkPort
	}

	if ip == "" {
		return nil, errors.New("empty ip")
	}

	// Create the node with the correct IP and port
	return &Node{
		IP:   ip,
		Port: port,
	}, nil
}

// setBootstrapNodesFromConfigVar parses config.bootstrapNodes and sets them
// as the bootstrap nodes - As of now, this is only supposed to be used for testing
func (s *DHT) setBootstrapNodesFromConfigVar(ctx context.Context, bootstrapNodes string) error {
	nodes := []*Node{}
	bsNodes := strings.Split(bootstrapNodes, ",")
	for _, bsNode := range bsNodes {
		lumeraAddress, err := ltc.ParseLumeraAddress(bsNode)
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
	log.P2P().WithContext(ctx).WithField("bootstrap_nodes", nodes).Info("Bootstrap nodes set from config var")

	return nil
}

// ConfigureBootstrapNodes connects with lumera client & gets p2p boostrap ip & port
func (s *DHT) ConfigureBootstrapNodes(ctx context.Context, bootstrapNodes string) error {
	if bootstrapNodes != "" {
		return s.setBootstrapNodesFromConfigVar(ctx, bootstrapNodes)
	}

	selfAddress, err := s.getExternalIP()
	if err != nil {
		return fmt.Errorf("get external ip addr: %s", err)
	}
	selfAddress = fmt.Sprintf("%s:%d", selfAddress, s.options.Port)

	var boostrapNodes []*Node

	if s.options.LumeraClient != nil {
		// Get the latest block to determine height
		latestBlockResp, err := s.options.LumeraClient.Node().GetLatestBlock(ctx)
		if err != nil {
			return fmt.Errorf("failed to get latest block: %w", err)
		}

		// Get the block height
		blockHeight := uint64(latestBlockResp.SdkBlock.Header.Height)

		// Get top supernodes for this block
		supernodeResp, err := s.options.LumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, blockHeight)
		if err != nil {
			return fmt.Errorf("failed to get top supernodes: %w", err)
		}

		mapNodes := map[string]*Node{}

		for _, supernode := range supernodeResp.Supernodes {
			// Find the latest IP address (with highest block height)
			var latestIP string
			var maxHeight int64 = -1

			for _, ipHistory := range supernode.PrevIpAddresses {
				if ipHistory.Height > maxHeight {
					maxHeight = ipHistory.Height
					latestIP = ipHistory.Address
				}
			}

			if latestIP == "" {
				log.P2P().WithContext(ctx).WithField("supernode", supernode.SupernodeAccount).Warn("No valid IP address found for supernode")
				continue
			}

			// Parse the node from the IP address
			node, err := s.parseNode(latestIP, selfAddress)
			if err != nil {
				log.P2P().WithContext(ctx).WithError(err).WithField("address", latestIP).WithField("supernode", supernode.SupernodeAccount).
					Warn("Skip Bad Bootstrap Address")
				continue
			}

			// Store the supernode account as the node ID
			node.ID = []byte(supernode.SupernodeAccount)
			mapNodes[latestIP] = node
		}

		// Convert the map to a slice
		for _, node := range mapNodes {
			hID, _ := utils.Blake3Hash(node.ID)
			node.HashedID = hID
			fmt.Println("node adding", node.String(), "hashed id", string(node.HashedID))
			boostrapNodes = append(boostrapNodes, node)
		}
	}

	if len(boostrapNodes) == 0 {
		log.WithContext(ctx).Error("unable to fetch bootstrap IP addresses. No valid supernodes found.")
		return nil
	}

	for _, node := range boostrapNodes {
		log.WithContext(ctx).WithFields(log.Fields{"bootstap_ip": node.IP, "bootstrap_port": node.Port, "node_id": string(node.ID)}).
			Info("adding p2p bootstrap node")
	}

	s.options.BootstrapNodes = append(s.options.BootstrapNodes, boostrapNodes...)

	return nil
}

// Bootstrap attempts to bootstrap the network using the BootstrapNodes provided
// to the Options struct
func (s *DHT) Bootstrap(ctx context.Context, bootstrapNodes string) error {
	if len(s.options.BootstrapNodes) == 0 {
		time.AfterFunc(bootstrapRetryInterval*time.Minute, func() {
			s.retryBootstrap(ctx, bootstrapNodes)
		})

		return nil
	}

	var wg sync.WaitGroup
	for _, node := range s.options.BootstrapNodes {
		nodeId := string(node.ID)
		// sync the bootstrap node only once
		isConnected, exists := s.bsConnected[nodeId]
		if exists && isConnected {
			continue
		}

		addr := fmt.Sprintf("%s:%v", node.IP, node.Port)
		if _, err := s.cache.Get(addr); err == nil {
			log.P2P().WithContext(ctx).WithField("addr", addr).Info("skip bad p2p boostrap addr")
			continue
		}

		node := node
		s.bsConnected[nodeId] = false
		wg.Add(1)
		go func() {
			defer wg.Done()

			// new a ping request message
			request := s.newMessage(Ping, node, nil)
			// new a context with timeout
			ctx, cancel := context.WithTimeout(ctx, defaultPingTime)
			defer cancel()

			// invoke the request and handle the response
			for i := 0; i < 5; i++ {
				response, err := s.network.Call(ctx, request, false)
				if err != nil {
					// This happening in bootstrap - so potentially other nodes not yet started
					// So if bootstrap failed, should try to connect to node again for next bootstrap retry
					// s.cache.SetWithExpiry(addr, []byte("true"), badAddrExpiryHours*time.Hour)

					log.P2P().WithContext(ctx).WithError(err).Error("network call failed, sleeping 3 seconds")
					time.Sleep(5 * time.Second)
					continue
				}
				log.P2P().WithContext(ctx).Debugf("ping response: %v", response.String())

				// add the node to the route table
				log.P2P().WithContext(ctx).WithField("sender-id", string(response.Sender.ID)).
					WithField("sender-ip", string(response.Sender.IP)).
					WithField("sender-port", response.Sender.Port).Debug("add-node params")

				if len(response.Sender.ID) != len(s.ht.self.ID) {
					log.P2P().WithContext(ctx).WithField("sender-id", string(response.Sender.ID)).
						WithField("self-id", string(s.ht.self.ID)).Error("self ID && sender ID len don't match")

					continue
				}

				s.bsConnected[nodeId] = true
				s.addNode(ctx, response.Sender)
				break
			}
		}()
	}

	// wait until all are done
	wg.Wait()

	// if it has nodes in queries route tables
	if s.ht.totalCount() > 0 {
		// iterative find node from the nodes
		if _, err := s.iterate(ctx, IterateFindNode, s.ht.self.ID, nil, 0); err != nil {
			log.P2P().WithContext(ctx).WithError(err).Error("iterative find node failed")
			return err
		}
	} else {
		time.AfterFunc(bootstrapRetryInterval*time.Minute, func() {
			s.retryBootstrap(ctx, bootstrapNodes)
		})
	}

	return nil
}

func (s *DHT) retryBootstrap(ctx context.Context, bootstrapNodes string) {
	if err := s.ConfigureBootstrapNodes(ctx, bootstrapNodes); err != nil {
		log.P2P().WithContext(ctx).WithError(err).Error("retry failed to get bootstap ip")
		return
	}

	// join the kademlia network if bootstrap nodes is set
	if err := s.Bootstrap(ctx, bootstrapNodes); err != nil {
		log.P2P().WithContext(ctx).WithError(err).Error("retry failed - bootstrap the node.")
	}
}
