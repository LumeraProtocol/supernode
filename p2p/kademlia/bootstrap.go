package kademlia

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"

	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
)

const (
	bootstrapRetryInterval = 10
	badAddrExpiryHours     = 12
)

func (s *DHT) skipBadBootstrapAddrs() {
	skipAddress1 := fmt.Sprintf("%s:%d", "127.0.0.1", s.options.Port)
	skipAddress2 := fmt.Sprintf("%s:%d", "localhost", s.options.Port)
	s.cache.Set(skipAddress1, []byte("true"))
	s.cache.Set(skipAddress2, []byte("true"))
}

func (s *DHT) parseNode(extP2P string, selfAddr string) (*Node, error) {
	if extP2P == "" {
		return nil, errors.New("empty address")
	}

	if strings.Contains(extP2P, "0.0.0.0") {
		return nil, errors.New("invalid address")
	}

	if extP2P == selfAddr {
		return nil, errors.New("self address")
	}

	if _, err := s.cache.Get(extP2P); err == nil {
		return nil, errors.New("configure: skip bad p2p boostrap addr")
	}

	addr := strings.Split(extP2P, ":")
	if len(addr) != 2 {
		return nil, errors.New("wrong number of field")
	}
	ip := addr[0]

	if ip == "" {
		return nil, errors.New("empty ip")
	}

	port, err := strconv.ParseUint(addr[1], 10, 16)
	if err != nil {
		return nil, errors.New("invalid port number")
	}

	return &Node{
		IP:   ip,
		Port: uint16(port),
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
			ID:	  []byte(lumeraAddress.Identity),
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

	get := func(ctx context.Context, f func(context.Context) (lumera.SuperNodeAddressInfos, error)) ([]*Node, error) {
		mns, err := f(ctx)
		if err != nil {
			return []*Node{}, err
		}

		mapNodes := map[string]*Node{}
		for _, mn := range mns {
			node, err := s.parseNode(mn.ExtP2P, selfAddress)
			if err != nil {
				log.P2P().WithContext(ctx).WithError(err).WithField("extP2P", mn.ExtP2P).Warn("Skip Bad Boostrap Address")
				continue
			}

			mapNodes[mn.ExtP2P] = node
		}

		nodes := []*Node{}
		for _, node := range mapNodes {
			nodes = append(nodes, node)
		}

		return nodes, nil
	}

	var boostrapNodes []*Node
	if s.options.LumeraNetwork != nil {
		boostrapNodes, err := get(ctx, s.options.LumeraNetwork.MasterNodesExtra)
		if err != nil {
			return fmt.Errorf("masternodesTop failed: %s", err)
		} else if len(boostrapNodes) == 0 {
			boostrapNodes, err = get(ctx, s.options.LumeraNetwork.MasterNodesTop)
			if err != nil {
				return fmt.Errorf("masternodesExtra failed: %s", err)
			} else if len(boostrapNodes) == 0 {
				log.P2P().WithContext(ctx).Error("unable to fetch bootstrap ip. Missing extP2P")

				return nil
			}
		}
	}

	for _, node := range boostrapNodes {
		log.P2P().WithContext(ctx).WithFields(log.Fields{
			"bootstap_ip":    node.IP,
			"bootstrap_port": node.Port,
		}).Info("adding p2p bootstap node")
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
