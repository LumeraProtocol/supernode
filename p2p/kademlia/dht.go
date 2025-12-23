package kademlia

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/cenkalti/backoff/v4"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"golang.org/x/sync/semaphore"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/memory"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	defaultNetworkPort                   uint16 = 4445
	defaultNetworkAddr                          = "0.0.0.0"
	defaultRefreshTime                          = time.Second * 3600
	defaultPingTime                             = 5 * time.Second
	defaultCleanupInterval                      = time.Minute * 2
	defaultDisabledKeyExpirationInterval        = time.Minute * 30
	defaultRedundantDataCleanupInterval         = 12 * time.Hour
	defaultDeleteDataInterval                   = 11 * time.Hour
	delKeysCountThreshold                       = 10
	lowSpaceThreshold                           = 50 // GB
	batchRetrieveSize                           = 1500

	storeSameSymbolsBatchConcurrency = 4
	fetchSymbolsBatchConcurrency     = 8
	minimumDataStoreSuccessRate      = 75.0
	perNodeRequestCap                = 600 // ~36 MB per RPC at 60KB/symbol

	maxIterations                  = 4
	maxConcurrentNetworkStoreCalls = 16
)

// DHT represents the state of the queries node in the distributed hash table
type DHT struct {
	ht             *HashTable       // the hashtable for routing
	options        *Options         // the options of DHT
	network        *Network         // the network of DHT
	store          Store            // the storage of DHT
	metaStore      MetaStore        // the meta storage of DHT
	done           chan struct{}    // distributed hash table is done
	cache          storage.KeyValue // store bad bootstrap addresses
	bsConnected    *sync.Map        // map of connected bootstrap nodes [identity] -> connected
	supernodeAddr  string           // cached address from chain
	mtx            sync.RWMutex
	ignorelist     *BanList
	replicationMtx sync.RWMutex
	rqstore        rqstore.Store
	metrics        DHTMetrics
}

// bootstrapIgnoreList seeds the in-memory ignore list with nodes that are
// currently marked inactive in the replication info store so we avoid
// contacting them during initial bootstrap. This does not fail the Start.
func (s *DHT) bootstrapIgnoreList(ctx context.Context) error {
	if s.store == nil {
		return nil
	}

	infos, err := s.store.GetAllReplicationInfo(ctx)
	if err != nil {
		return fmt.Errorf("get replication info: %w", err)
	}
	if len(infos) == 0 {
		return nil
	}

	added := 0
	for _, info := range infos {
		if info.Active {
			continue
		}
		// Seed as banned by setting count above threshold; use UpdatedAt as createdAt basis
		n := &Node{ID: info.ID, IP: info.IP, Port: info.Port}
		createdAt := info.UpdatedAt
		if createdAt.IsZero() && info.LastSeen != nil {
			createdAt = *info.LastSeen
		}
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		s.ignorelist.AddWithCreatedAt(n, createdAt, threshold+1)
		added++
	}

	if added > 0 {
		logtrace.Debug(ctx, "Ignore list bootstrapped from replication info", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"ignored_count":      added,
		})
	}
	return nil
}
func (s *DHT) MetricsSnapshot() DHTMetricsSnapshot {
	return s.metrics.Snapshot()
}

func (s *DHT) BanListSnapshot() []BanSnapshot {
	return s.ignorelist.Snapshot(0)
}

func (s *DHT) ConnPoolSnapshot() map[string]int64 {
	return s.network.connPool.metrics.Snapshot()
}

// Options contains configuration options for the queries node
type Options struct {
	ID []byte

	// The queries IPv4 or IPv6 address
	IP string

	// The queries port to listen for connections
	Port uint16

	// The nodes being used to bootstrap the network. Without a bootstrap
	// node there is no way to connect to the network
	BootstrapNodes []*Node

	// Lumera client for interacting with the blockchain
	LumeraClient lumera.Client

	// Keyring for credentials
	Keyring keyring.Keyring
}

// NewDHT returns a new DHT node
func NewDHT(ctx context.Context, store Store, metaStore MetaStore, options *Options, rqstore rqstore.Store) (*DHT, error) {
	// validate the options, if it's invalid, set them to default value
	if options.IP == "" {
		options.IP = defaultNetworkAddr
	}
	if options.Port <= 0 {
		options.Port = defaultNetworkPort
	}

	s := &DHT{
		metaStore:      metaStore,
		store:          store,
		options:        options,
		done:           make(chan struct{}),
		cache:          memory.NewKeyValue(),
		bsConnected:    &sync.Map{},
		ignorelist:     NewBanList(ctx),
		replicationMtx: sync.RWMutex{},
		rqstore:        rqstore,
	}

	// Check that keyring is provided
	if options.Keyring == nil {
		return nil, fmt.Errorf("keyring is required but not provided")
	}

	// Initialize client credentials with the provided keyring
	clientCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       options.Keyring,
			LocalIdentity: string(options.ID),
			PeerType:      securekeyx.Supernode,
			Validator:     lumera.NewSecureKeyExchangeValidator(options.LumeraClient),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client credentials: %w", err)
	}

	// server creds for incoming
	serverCreds, err := ltc.NewServerCreds(&ltc.ServerOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       options.Keyring,
			LocalIdentity: string(options.ID),
			PeerType:      securekeyx.Supernode,
			Validator:     lumera.NewSecureKeyExchangeValidator(options.LumeraClient),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create server credentials: %w", err)
	}

	// new a hashtable with options
	ht, err := NewHashTable(options)
	if err != nil {
		return nil, fmt.Errorf("new hashtable: %v", err)
	}
	s.ht = ht

	// add bad boostrap addresss
	s.skipBadBootstrapAddrs()

	// new network service for dht
	network, err := NewNetwork(ctx, s, ht.self, clientCreds, serverCreds)
	if err != nil {
		return nil, fmt.Errorf("new network: %v", err)
	}
	s.network = network

	return s, nil
}

func (s *DHT) NodesLen() int {
	return len(s.ht.nodes())
}

func (s *DHT) getSupernodeAddress(ctx context.Context) (string, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	// Return cached value if already determined
	if s.supernodeAddr != "" {
		return s.supernodeAddr, nil
	}

	// Query chain for supernode info
	supernodeInfo, err := s.options.LumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, string(s.options.ID))
	if err != nil {
		return "", fmt.Errorf("failed to get supernode address: %w", err)
	}
	s.supernodeAddr = strings.TrimSpace(supernodeInfo.LatestAddress)
	return s.supernodeAddr, nil
}

// getCachedSupernodeAddress returns cached supernode address without chain queries
func (s *DHT) getCachedSupernodeAddress() string {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	if s.supernodeAddr != "" {
		return s.supernodeAddr
	}
	return s.ht.self.IP // fallback without chain query
}

// parseSupernodeAddress extracts the host part from a URL or address string.
// It handles http/https prefixes, optional ports, and raw host:port formats.
func parseSupernodeAddress(address string) string {
	// Always trim whitespace first
	address = strings.TrimSpace(address)

	// If it looks like a URL, parse with net/url
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		host, _, err := net.SplitHostPort(u.Host)
		if err == nil {
			return strings.TrimSpace(host)
		}
		return strings.TrimSpace(u.Host) // no port present
	}

	// If it's just host:port, handle with SplitHostPort
	if host, _, err := net.SplitHostPort(address); err == nil {
		return strings.TrimSpace(host)
	}

	// Otherwise return as-is (probably just a bare host)
	return address
}

// Start the distributed hash table
func (s *DHT) Start(ctx context.Context) error {
	// start the network
	if err := s.network.Start(ctx); err != nil {
		return fmt.Errorf("start network: %v", err)
	}

	// Pre-fetch supernode address with generous timeout to avoid chain queries during message creation
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := s.getSupernodeAddress(initCtx); err != nil {
		logtrace.Warn(ctx, "Failed to pre-fetch supernode address, will use fallback", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
	}

	// Bootstrap ignore list from persisted replication info (inactive nodes)
	if err := s.bootstrapIgnoreList(ctx); err != nil {
		logtrace.Warn(ctx, "Failed to bootstrap ignore list", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
		})
	}

	go s.StartReplicationWorker(ctx)
	go s.startDisabledKeysCleanupWorker(ctx)
	go s.startCleanupRedundantDataWorker(ctx)
	go s.startDeleteDataWorker(ctx)
	go s.startStoreSymbolsWorker(ctx)

	return nil
}

// Stop the distributed hash table
func (s *DHT) Stop(ctx context.Context) {
	if s.done != nil {
		close(s.done)
	}

	// stop the network
	s.network.Stop(ctx)
}

func (s *DHT) retryStore(ctx context.Context, key []byte, data []byte, typ int) error {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 1 * time.Minute
	b.InitialInterval = 200 * time.Millisecond

	return backoff.Retry(backoff.Operation(func() error {
		return s.store.Store(ctx, key, data, typ, true)
	}), b)
}

// Store the data into the network
func (s *DHT) Store(ctx context.Context, data []byte, typ int) (string, error) {
	key, _ := utils.Blake3Hash(data)

	retKey := base58.Encode(key)
	// store the key to queries storage
	if err := s.retryStore(ctx, key, data, typ); err != nil {
		logtrace.Error(ctx, "Local data store failure after retries", logtrace.Fields{
			logtrace.FieldModule: "dht",
			logtrace.FieldError:  err.Error(),
		})
		return "", fmt.Errorf("retry store data to queries storage: %v", err)
	}

	if _, err := s.iterate(ctx, IterateStore, key, data, typ); err != nil {
		logtrace.Error(ctx, "Iterate data store failure", logtrace.Fields{
			logtrace.FieldModule: "dht",
			logtrace.FieldError:  err.Error(),
		})
		return "", fmt.Errorf("iterative store data: %v", err)
	}

	return retKey, nil
}

// StoreBatch stores a batch of values with their Blake3 hash as the key.
// It persists to the local store then performs the network store. If the
// measured success rate for node RPCs is below the configured minimum, an error
// is returned. Metrics are not returned through the API.
func (s *DHT) StoreBatch(ctx context.Context, values [][]byte, typ int, taskID string) error {
	logtrace.Debug(ctx, "DHT StoreBatch begin", logtrace.Fields{
		logtrace.FieldModule: "dht",
		logtrace.FieldTaskID: taskID,
		"records":            len(values),
	})
	if err := s.store.StoreBatch(ctx, values, typ, true); err != nil {
		return fmt.Errorf("store batch: %v", err)
	}
	logtrace.Debug(ctx, "DHT StoreBatch: local stored; network begin", logtrace.Fields{
		logtrace.FieldModule: "dht",
		logtrace.FieldTaskID: taskID,
	})

	err := s.IterateBatchStore(ctx, values, typ, taskID)
	if err != nil {
		return fmt.Errorf("iterate batch store: %v", err)
	}

	logtrace.Debug(ctx, "DHT StoreBatch: network done", logtrace.Fields{
		logtrace.FieldModule: "dht",
		logtrace.FieldTaskID: taskID,
	})

	return nil
}

// Retrieve data from the networking using key. Key is the base58 encoded
// identifier of the data.
func (s *DHT) Retrieve(ctx context.Context, key string, localOnly ...bool) ([]byte, error) {
	start := time.Now()
	decoded := base58.Decode(key)
	if len(decoded) != B/8 {
		return nil, fmt.Errorf("invalid key: %v", key)
	}

	dbKey := hex.EncodeToString(decoded)
	if s.metaStore != nil {
		if err := s.metaStore.Retrieve(ctx, dbKey); err == nil {
			return nil, fmt.Errorf("key is disabled: %v", key)
		}
	}

	// retrieve the key/value from queries storage
	value, err := s.store.Retrieve(ctx, decoded)
	if err == nil && len(value) > 0 {
		logtrace.Debug(ctx, "DHT Retrieve local hit", logtrace.Fields{"key": hex.EncodeToString(decoded), "ms": time.Since(start).Milliseconds()})
		return value, nil
	} else if err != nil {
		logtrace.Error(ctx, "Error retrieving key from local storage", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"db_key":             dbKey,
			logtrace.FieldError:  err.Error(),
		})
	}

	// if queries only option is set, do not search just return error
	if len(localOnly) > 0 && localOnly[0] {
		return nil, errors.Errorf("queries-only failed to get properly: %w", err)
	}

	// if not found locally, iterative find value from kademlia network
	logtrace.Debug(ctx, "DHT Retrieve network lookup", logtrace.Fields{"key": dbKey})
	peerValue, err := s.iterate(ctx, IterateFindValue, decoded, nil, 0)
	if err != nil {
		return nil, errors.Errorf("retrieve from peer: %w", err)
	}
	if len(peerValue) > 0 {
		logtrace.Debug(ctx, "DHT Retrieve network hit", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"key":                dbKey,
			"data_len":           len(peerValue),
			"ms":                 time.Since(start).Milliseconds(),
		})
	} else {
		logtrace.Debug(ctx, "DHT Retrieve miss", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"key":                dbKey,
			"ms":                 time.Since(start).Milliseconds(),
		})
	}

	return peerValue, nil
}

// Delete delete key in queries node
func (s *DHT) Delete(ctx context.Context, key string) error {
	decoded := base58.Decode(key)
	if len(decoded) != B/8 {
		return fmt.Errorf("invalid key: %v", key)
	}

	s.store.Delete(ctx, decoded)

	return nil
}

// Stats returns stats of DHT
func (s *DHT) Stats(ctx context.Context) (map[string]interface{}, error) {
	if s.store == nil {
		return nil, fmt.Errorf("store is nil")
	}

	dbStats, err := s.store.Stats(ctx)
	if err != nil {
		return nil, err
	}

	dhtStats := map[string]any{}
	dhtStats["self"] = s.ht.self
	dhtStats["peers_count"] = len(s.ht.nodes())
	dhtStats["peers"] = s.ht.nodes()
	dhtStats["network"] = s.network.HandleMetricsSnapshot()
	// Removed: recent per-request snapshots (logs provide visibility)
	dhtStats["database"] = dbStats

	return dhtStats, nil
}

// newMessage creates a new message
func (s *DHT) newMessage(messageType int, receiver *Node, data interface{}) *Message {
	supernodeAddr := s.getCachedSupernodeAddress()
	hostIP := parseSupernodeAddress(supernodeAddr)

	// If fallback produced an invalid address (e.g., 0.0.0.0), choose a safe sender IP
	if ip := net.ParseIP(hostIP); ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() {
		// Prefer valid self IP; in integration tests, allow loopback and private
		isIntegrationTest := os.Getenv("INTEGRATION_TEST") == "true"
		if sip := net.ParseIP(s.ht.self.IP); sip != nil {
			if !sip.IsUnspecified() && (isIntegrationTest || (!sip.IsLoopback() && !sip.IsPrivate())) {
				hostIP = s.ht.self.IP
			} else if isIntegrationTest {
				// Default to localhost when running local tests and no valid external IP
				hostIP = "127.0.0.1"
			}
		} else if isIntegrationTest {
			hostIP = "127.0.0.1"
		}
	}

	sender := &Node{
		IP:      hostIP,
		ID:      s.ht.self.ID,
		Port:    s.ht.self.Port,
		Version: localVersion(),
	}
	return &Message{
		Sender:      sender,
		Receiver:    receiver,
		MessageType: messageType,
		Data:        data,
	}
}

// GetValueFromNode gets values from node
func (s *DHT) GetValueFromNode(ctx context.Context, target []byte, n *Node) ([]byte, error) {
	messageType := FindValue
	data := &FindValueRequest{Target: target}

	request := s.newMessage(messageType, n, data)
	// send the request and receive the response
	cctx, ccancel := context.WithTimeout(ctx, time.Second*5)
	defer ccancel()

	// Minimal per-RPC visibility
	logtrace.Debug(ctx, "RPC FindValue send", logtrace.Fields{"node": n.String(), "key": hex.EncodeToString(target)})
	response, err := s.network.Call(cctx, request, false)
	if err != nil {
		logtrace.Debug(ctx, "Network call request failed", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			logtrace.FieldError:  err.Error(),
			"request":            request.String(),
		})
		return nil, fmt.Errorf("network call request %s failed: %w", request.String(), err)
	}
	logtrace.Debug(ctx, "RPC FindValue completed", logtrace.Fields{"node": n.String()})

	v, ok := response.Data.(*FindValueResponse)
	if ok && v.Status.Result == ResultOk && len(v.Value) > 0 {
		return v.Value, nil
	}

	return nil, fmt.Errorf("claim to have value but not found - %s - node: %s", response.String(), n.String())
}

func (s *DHT) doMultiWorkers(ctx context.Context, iterativeType int, target []byte, nl *NodeList, contacted map[string]bool, haveRest bool) chan *Message {
	// responses from remote node
	responses := make(chan *Message, Alpha)

	go func() {
		// the nodes which are unreachable
		//var removedNodes []*Node

		var wg sync.WaitGroup

		var number int
		// send the message to the first (closest) alpha nodes
		for _, node := range nl.Nodes {
			// only contact alpha nodes
			if number >= Alpha && !haveRest {
				break
			}
			// ignore the contacted node
			if contacted[string(node.ID)] {
				continue
			}
			contacted[string(node.ID)] = true

			// update the running goroutines
			number++

			logtrace.Debug(ctx, "Start work for node", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"iterate_type":       iterativeType,
				"node":               node.String(),
			})

			wg.Add(1)
			// send and receive message concurrently
			go func(receiver *Node) {
				defer wg.Done()

				var data interface{}
				var messageType int
				switch iterativeType {
				case IterateFindNode, IterateStore:
					messageType = FindNode
					data = &FindNodeRequest{Target: target}
				case IterateFindValue:
					messageType = FindValue
					data = &FindValueRequest{Target: target}
				}

				// new a request message
				request := s.newMessage(messageType, receiver, data)
				// Minimal per-RPC visibility
				op := ""
				switch messageType {
				case FindNode:
					op = "FindNode"
				case FindValue:
					op = "FindValue"
				default:
					op = "RPC"
				}
				fields := logtrace.Fields{"node": receiver.String()}
				if messageType == FindValue {
					fields["key"] = hex.EncodeToString(target)
				}
				logtrace.Debug(ctx, "RPC "+op+" send", fields)
				// send the request and receive the response
				response, err := s.network.Call(ctx, request, false)
				if err != nil {
					logtrace.Debug(ctx, "Iterate worker RPC failed", logtrace.Fields{
						logtrace.FieldModule: "p2p",
						logtrace.FieldError:  err.Error(),
						"request":            request.String(),
						"node":               receiver.String(),
					})
					// node is unreachable, remove the node
					//removedNodes = append(removedNodes, receiver)
					return
				}
				logtrace.Debug(ctx, "RPC "+op+" completed", logtrace.Fields{"node": receiver.String()})

				// send the response to message channel
				responses <- response
			}(node)
		}

		// wait until tasks are done
		wg.Wait()

		// close the message channel
		close(responses)
	}()

	return responses
}

func (s *DHT) fetchAndAddLocalKeys(ctx context.Context, hexKeys []string, result *sync.Map, req int32) (count int32, err error) {
	batchSize := 5000

	// Process in batches
	for start := 0; start < len(hexKeys); start += batchSize {
		end := start + batchSize
		if end > len(hexKeys) {
			end = len(hexKeys)
		}

		batchHexKeys := hexKeys[start:end]

		logtrace.Debug(ctx, "Processing batch of local keys", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"batch_size":         len(batchHexKeys),
			"total_keys":         len(hexKeys),
		})

		// Retrieve values for the current batch of local keys
		localValues, _, batchErr := s.store.RetrieveBatchValues(ctx, batchHexKeys, false)
		if batchErr != nil {
			logtrace.Error(ctx, "Failed to retrieve batch values", logtrace.Fields{
				logtrace.FieldModule: "dht",
				logtrace.FieldError:  batchErr.Error(),
			})
			err = fmt.Errorf("retrieve batch values (local): %v", batchErr)
			continue // Optionally continue with next batch or return depending on use case
		}

		// Populate the result map with the local values and count the found keys
		for i, val := range localValues {
			if len(val) > 0 {
				count++
				result.Store(batchHexKeys[i], val)
				if count >= req {
					return count, nil
				}
			}
		}
	}

	return count, err
}

func (s *DHT) BatchRetrieve(ctx context.Context, keys []string, required int32, txID string, localOnly ...bool) (result map[string][]byte, err error) {
	start := time.Now()
	logtrace.Debug(ctx, "DHT BatchRetrieve begin", logtrace.Fields{"txid": txID, "keys": len(keys), "required": required})
	result = make(map[string][]byte)
	var resMap sync.Map
	var foundLocalCount int32

	hexKeys := make([]string, len(keys))
	hashes := make([][]byte, len(keys))

	defer func() {
		resMap.Range(func(key, value interface{}) bool {
			hexKey := key.(string)
			valBytes := value.([]byte)
			k, err := hex.DecodeString(hexKey)
			if err != nil {
				logtrace.Error(ctx, "Failed to decode hex key in resMap.Range", logtrace.Fields{
					logtrace.FieldModule: "dht",
					"key":                hexKey, "txid": txID, logtrace.FieldError: err.Error(),
				})
				return true
			}
			result[base58.Encode(k)] = valBytes
			return true
		})
		for k, v := range result {
			if len(v) == 0 {
				delete(result, k)
			}
		}
	}()

	for i, key := range keys {
		decoded := base58.Decode(key)
		if len(decoded) != B/8 {
			return nil, fmt.Errorf("invalid key: %v", key)
		}
		hashes[i] = decoded
		hexKeys[i] = hex.EncodeToString(decoded)
		result[key] = nil
	}

	foundLocalCount, err = s.fetchAndAddLocalKeys(ctx, hexKeys, &resMap, required)
	if err != nil {
		return nil, fmt.Errorf("fetch and add local keys: %v", err)
	}
	// Found locally count is logged via summary below; no external metrics

	if foundLocalCount >= required {
		f := logtrace.Fields{"txid": txID, "found_local": foundLocalCount, "found_network": 0, "required": required, "ms": time.Since(start).Milliseconds(), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch retrieve summary", f)
		return result, nil
	}

	if len(localOnly) > 0 && localOnly[0] {
		f := logtrace.Fields{"txid": txID, "found_local": foundLocalCount, "found_network": 0, "required": required, "ms": time.Since(start).Milliseconds(), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch retrieve summary", f)
		return result, fmt.Errorf("local-only: found %d, required %d", foundLocalCount, required)
	}

	supernodeAddr, addrErr := s.getSupernodeAddress(ctx)
	if addrErr != nil {
		logtrace.Warn(ctx, "Failed to get supernode address", logtrace.Fields{
			logtrace.FieldModule: "dht",
			logtrace.FieldError:  addrErr.Error(),
		})
	}
	hostIP := parseSupernodeAddress(supernodeAddr)
	self := &Node{ID: s.ht.self.ID, IP: hostIP, Port: s.ht.self.Port}
	self.SetHashedID()

	knownNodes := make(map[string]*Node)
	var knownMu sync.Mutex

	for _, n := range s.ht.nodes() {
		nn := &Node{ID: n.ID, IP: n.IP, Port: n.Port}
		nn.SetHashedID()
		knownNodes[string(nn.ID)] = nn
	}

	ignoreList := s.ignorelist.ToNodeList()

	globalClosestContacts := make(map[string]*NodeList)
	var closestMu sync.RWMutex

	for i := range keys {
		if _, found := resMap.Load(hexKeys[i]); found {
			continue
		}

		top6 := s.ht.closestContactsWithIncludingNode(Alpha, hashes[i], ignoreList, nil)
		closestMu.Lock()
		globalClosestContacts[keys[i]] = top6
		closestMu.Unlock()
		s.addKnownNodesSafe(ctx, top6.Nodes, knownNodes, &knownMu)
	}

	delete(knownNodes, string(self.ID))

	batchSize := batchRetrieveSize
	var networkFound int32
	totalBatches := int(math.Ceil(float64(required) / float64(batchSize)))
	parallelBatches := int(math.Min(float64(totalBatches), float64(fetchSymbolsBatchConcurrency)))

	sem := semaphore.NewWeighted(int64(parallelBatches))
	var wg sync.WaitGroup
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Measure only the network retrieval phase (after local scan)
	netStart := time.Now()

	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}

		if atomic.LoadInt32(&networkFound)+int32(foundLocalCount) >= int32(required) {
			break
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()

			if err := sem.Acquire(gctx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			if atomic.LoadInt32(&networkFound)+int32(foundLocalCount) >= int32(required) {
				return
			}

			s.processBatch(
				gctx,
				keys[start:end],
				hexKeys[start:end],
				globalClosestContacts,
				&closestMu,
				knownNodes, &knownMu,
				&resMap,
				required,
				foundLocalCount,
				&networkFound,
				cancel,
				txID,
			)
		}(start, end)
	}

	wg.Wait()

	netFound := int(atomic.LoadInt32(&networkFound))

	f := logtrace.Fields{"txid": txID, "found_local": foundLocalCount, "found_network": netFound, "required": required, "ms": time.Since(start).Milliseconds(), logtrace.FieldRole: "client"}
	if o := logtrace.OriginFromContext(ctx); o != "" {
		f[logtrace.FieldOrigin] = o
	}
	logtrace.Info(ctx, "dht: batch retrieve summary", f)

	// Record batch retrieve stats for internal DHT snapshot window (network phase only)
	s.metrics.RecordBatchRetrieve(len(keys), int(required), int(foundLocalCount), netFound, time.Since(netStart))
	// No per-task metrics collector updates

	return result, nil
}

func (s *DHT) processBatch(
	ctx context.Context,
	batchKeys []string,
	batchHexKeys []string,
	globalClosestContacts map[string]*NodeList,
	closestMu *sync.RWMutex,
	knownNodes map[string]*Node,
	knownMu *sync.Mutex,
	resMap *sync.Map,
	required int32,
	foundLocalCount int32,
	networkFound *int32,
	cancel context.CancelFunc,
	txID string,
) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	fetchMap := make(map[string][]int)

	closestMu.RLock()
	localContacts := make(map[string]*NodeList, len(batchKeys))
	for _, key := range batchKeys {
		localContacts[key] = globalClosestContacts[key]
	}
	closestMu.RUnlock()

	for idx, key := range batchKeys {
		nl := localContacts[key]
		if nl == nil {
			continue
		}
		for _, node := range nl.Nodes {
			nodeID := string(node.ID)
			fetchMap[nodeID] = append(fetchMap[nodeID], idx)
		}
	}

	foundCount, batchErr := s.iterateBatchGetValues(
		ctx, knownNodes, batchHexKeys, fetchMap, resMap, required, foundLocalCount+atomic.LoadInt32(networkFound),
	)
	if batchErr != nil {
		logtrace.Error(ctx, "Iterate batch get values failed", logtrace.Fields{
			logtrace.FieldModule: "dht", "txid": txID, logtrace.FieldError: batchErr.Error(),
		})
	}

	atomic.AddInt32(networkFound, int32(foundCount))
	if atomic.LoadInt32(networkFound)+int32(foundLocalCount) >= int32(required) {
		cancel()
	}
}

func (s *DHT) iterateBatchGetValues(ctx context.Context, nodes map[string]*Node, hexKeys []string, fetchMap map[string][]int,
	resMap *sync.Map, req, alreadyFound int32) (int, error) {
	sem := semaphore.NewWeighted(int64(storeSameSymbolsBatchConcurrency))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex // To protect the firstErr
	foundCount := int32(0)

	gctx, cancel := context.WithCancel(ctx) // Create a cancellable context
	defer cancel()

	for nodeID := range fetchMap {
		node, ok := nodes[nodeID]
		if !ok {
			continue
		}

		if s.ignorelist.Banned(node) {
			logtrace.Debug(ctx, "Ignore banned node in iterate batch get values", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"node":               node.String(),
			})
			continue
		}

		wg.Add(1)
		go func(node *Node, nodeID string) {
			defer wg.Done()

			if err := sem.Acquire(gctx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			if atomic.LoadInt32(&foundCount) >= int32(req-alreadyFound) {
				return
			}

			indices := fetchMap[nodeID]
			requestKeys := make(map[string]KeyValWithClosest)
			for _, idx := range indices {
				if idx < len(hexKeys) {
					_, loaded := resMap.Load(hexKeys[idx]) //  check if key is already there in resMap
					if !loaded {
						requestKeys[hexKeys[idx]] = KeyValWithClosest{}
					}
				}
			}

			if len(requestKeys) == 0 {
				// No keys to request from this node (e.g., all keys already satisfied elsewhere).
				// Do not emit no-op retrieve metrics; skip silently.
				return
			}

			decompressedData, err := s.doBatchGetValuesCall(gctx, node, requestKeys)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				// per-node metrics removed; logs retained
				return
			}

			returned := 0
			for k, v := range decompressedData {
				if len(v.Value) > 0 {
					_, loaded := resMap.LoadOrStore(k, v.Value)
					if !loaded {
						atomic.AddInt32(&foundCount, 1)
						returned++
						if atomic.LoadInt32(&foundCount) >= int32(req-alreadyFound) {
							cancel() // Cancel context to stop other goroutines
							// don't early return; record metric and exit goroutine
							break
						}
					}
				}
			}

			// per-node metrics removed; logs retained
		}(node, nodeID)
	}

	wg.Wait()

	logtrace.Debug(ctx, "Iterate batch get values done", logtrace.Fields{
		logtrace.FieldModule: "dht",
		"found_count":        atomic.LoadInt32(&foundCount),
	})

	if firstErr != nil {
		logtrace.Error(ctx, "Encountered error in iterate batch get values", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"found_count":        atomic.LoadInt32(&foundCount),
			logtrace.FieldError:  firstErr.Error(),
		})
	}

	return int(foundCount), firstErr
}

func (s *DHT) doBatchGetValuesCall(ctx context.Context, node *Node, requestKeys map[string]KeyValWithClosest) (map[string]KeyValWithClosest, error) {
	request := s.newMessage(BatchGetValues, node, &BatchGetValuesRequest{Data: requestKeys})
	{
		f := logtrace.Fields{"node": node.String(), "keys": len(requestKeys), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch get send", f)
	}
	response, err := s.network.Call(ctx, request, false)
	if err != nil {
		return nil, fmt.Errorf("network call request %s failed: %w", request.String(), err)
	}
	{
		f := logtrace.Fields{"node": node.String(), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch get ok", f)
	}

	resp, ok := response.Data.(*BatchGetValuesResponse)
	if !ok {
		return nil, fmt.Errorf("invalid response type: %T", response.Data)
	}

	if resp.Status.Result != ResultOk {
		return nil, fmt.Errorf("response status: %v", resp.Status.ErrMsg)
	}

	return resp.Data, nil
}

func (s *DHT) BatchRetrieveStream(
	ctx context.Context,
	keys []string,
	required int32,
	txID string,
	onSymbol func(base58Key string, data []byte) error, // write-to-disk sink
	localOnly ...bool,
) (written int32, err error) {
	logtrace.Debug(ctx, "DHT BatchRetrieveStream begin", logtrace.Fields{"txid": txID, "keys": len(keys), "required": required})
	if required <= 0 || len(keys) == 0 {
		return 0, nil
	}

	hexKeys := make([]string, len(keys))
	hashes := make([][]byte, len(keys))
	for i, key := range keys {
		decoded := base58.Decode(key)
		if len(decoded) != B/8 {
			return 0, fmt.Errorf("invalid key: %v", key)
		}
		hashes[i] = decoded
		hexKeys[i] = hex.EncodeToString(decoded)
	}

	// resSeen tracks which symbols we already persisted (hexKey -> struct{})
	var resSeen sync.Map

	// 1) Local-first (BATCHED)
	foundLocalCount, lerr := s.fetchAndWriteLocalKeysBatched(ctx, hexKeys, &resSeen, required, onSymbol)
	if lerr != nil {
		return 0, fmt.Errorf("fetch&write local (batched): %w", lerr)
	}
	if foundLocalCount >= required {
		logtrace.Debug(ctx, "DHT BatchRetrieveStream satisfied from local storage", logtrace.Fields{
			"txid": txID, "found_local": foundLocalCount, "required": required,
		})
		return foundLocalCount, nil
	}
	if len(localOnly) > 0 && localOnly[0] {
		logtrace.Debug(ctx, "DHT BatchRetrieveStream local-only mode, insufficient keys", logtrace.Fields{
			"txid": txID, "found_local": foundLocalCount, "required": required,
		})
		return foundLocalCount, fmt.Errorf("local-only: found %d, required %d", foundLocalCount, required)
	}

	// Effective network requirement (avoid over-fetch)
	needNetwork := required - foundLocalCount
	if needNetwork <= 0 {
		return foundLocalCount, nil
	}

	// 2) Network phase: breadth-first
	supernodeAddr, addrErr := s.getSupernodeAddress(ctx)
	if addrErr != nil {
		logtrace.Warn(ctx, "Failed to get supernode address", logtrace.Fields{
			logtrace.FieldModule: "dht", logtrace.FieldError: addrErr.Error(),
		})
	}
	hostIP := parseSupernodeAddress(supernodeAddr)
	self := &Node{ID: s.ht.self.ID, IP: hostIP, Port: s.ht.self.Port}
	self.SetHashedID()

	knownNodes := make(map[string]*Node)
	var knownMu sync.Mutex
	for _, n := range s.ht.nodes() {
		nn := &Node{ID: n.ID, IP: n.IP, Port: n.Port}
		nn.SetHashedID()
		knownNodes[string(nn.ID)] = nn
	}
	delete(knownNodes, string(self.ID))

	ignoreList := s.ignorelist.ToNodeList()
	globalClosestContacts := make(map[string]*NodeList)
	var closestMu sync.RWMutex

	// Build top-k once for unsatisfied keys
	for i := range keys {
		if _, found := resSeen.Load(hexKeys[i]); found {
			continue
		}
		topK := s.ht.closestContactsWithIncludingNode(Alpha, hashes[i], ignoreList, nil)
		closestMu.Lock()
		globalClosestContacts[keys[i]] = topK
		closestMu.Unlock()
		s.addKnownNodesSafe(ctx, topK.Nodes, knownNodes, &knownMu)
	}

	var networkFound int32
	totalFound := func() int32 { return foundLocalCount + atomic.LoadInt32(&networkFound) }

	batchSize := batchRetrieveSize
	totalBatches := int(math.Ceil(float64(len(keys)) / float64(batchSize)))
	parallelBatches := int(math.Min(float64(totalBatches), float64(fetchSymbolsBatchConcurrency)))

	sem := semaphore.NewWeighted(int64(parallelBatches))
	var wg sync.WaitGroup
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	netStart := time.Now()
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		if totalFound() >= required {
			break
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			if err := sem.Acquire(gctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			if totalFound() >= required {
				return
			}
			// IMPORTANT: pass needNetwork (not total required) into the batch
			s.processBatchStream(
				gctx,
				keys[start:end],
				hexKeys[start:end],
				globalClosestContacts, &closestMu,
				knownNodes, &knownMu,
				&resSeen,
				needNetwork,
				&networkFound,
				onSymbol,
				cancel,
				txID,
			)
		}(start, end)
	}
	wg.Wait()

	netFound := int(atomic.LoadInt32(&networkFound))
	{
		f := logtrace.Fields{"txid": txID, "found_local": foundLocalCount, "found_network": netFound, "required": required, "ms": time.Since(netStart).Milliseconds(), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch retrieve stream summary", f)
	}
	s.metrics.RecordBatchRetrieve(len(keys), int(required), int(foundLocalCount), netFound, time.Since(netStart))
	return foundLocalCount + int32(netFound), nil
}

func (s *DHT) processBatchStream(
	ctx context.Context,
	batchKeys []string,
	batchHexKeys []string,
	globalClosestContacts map[string]*NodeList,
	closestMu *sync.RWMutex,
	knownNodes map[string]*Node,
	knownMu *sync.Mutex,
	resSeen *sync.Map,
	needNetwork int32, // << only network requirement
	networkFound *int32,
	onSymbol func(base58Key string, data []byte) error,
	cancel context.CancelFunc,
	txID string,
) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Snapshot contacts for this batch once
	closestMu.RLock()
	localContacts := make(map[string]*NodeList, len(batchKeys))
	for _, key := range batchKeys {
		localContacts[key] = globalClosestContacts[key]
	}
	closestMu.RUnlock()

	// Determine max K across keys in this batch
	maxK := 0
	for _, key := range batchKeys {
		if nl := localContacts[key]; nl != nil && len(nl.Nodes) > maxK {
			maxK = len(nl.Nodes)
		}
	}
	if maxK == 0 {
		return
	}

	remainingNeeded := func() int32 { return needNetwork - atomic.LoadInt32(networkFound) }

	// Up to K waves; stop early if we've met the network target
	for wave := 0; wave < maxK; wave++ {
		if remainingNeeded() <= 0 || ctx.Err() != nil {
			break
		}

		// Build node->indices for this wave using the wave's provider
		fetchMap := make(map[string][]int)
		for idx, key := range batchKeys {
			hexK := batchHexKeys[idx]
			if _, seen := resSeen.Load(hexK); seen {
				continue
			}
			nl := localContacts[key]
			if nl == nil || len(nl.Nodes) == 0 {
				continue
			}
			k := len(nl.Nodes)
			base := providerIndexForKey(key, txID, k)
			provider := nl.Nodes[(base+wave)%k] // rotate per wave

			nodeID := string(provider.ID)
			fetchMap[nodeID] = append(fetchMap[nodeID], idx)
		}

		// Per-node request cap to bound payload sizes
		if perNodeRequestCap > 0 {
			for nodeID, idxs := range fetchMap {
				if len(idxs) > perNodeRequestCap {
					fetchMap[nodeID] = idxs[:perNodeRequestCap]
				}
			}
		}

		foundCount, _ := s.iterateBatchGetValuesStream(
			ctx,
			knownNodes,
			batchKeys,    // base58 for sink
			batchHexKeys, // hex for dedup
			fetchMap,
			resSeen,
			needNetwork, // << pass network-only target
			atomic.LoadInt32(networkFound),
			onSymbol,
		)
		if foundCount > 0 {
			atomic.AddInt32(networkFound, int32(foundCount))
		}
		if atomic.LoadInt32(networkFound) >= needNetwork {
			cancel()
			break
		}
	}
}
func (s *DHT) iterateBatchGetValuesStream(
	ctx context.Context,
	nodes map[string]*Node,
	base58Keys []string,
	hexKeys []string,
	fetchMap map[string][]int,
	resSeen *sync.Map, // hexKey -> struct{}
	needNetwork int32, // << network-only target for this call
	alreadyFound int32,
	onSymbol func(base58Key string, data []byte) error,
) (int, error) {
	sem := semaphore.NewWeighted(int64(storeSameSymbolsBatchConcurrency))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	foundCount := int32(0)

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for nodeID := range fetchMap {
		node, ok := nodes[nodeID]
		if !ok {
			continue
		}
		if s.ignorelist.Banned(node) {
			logtrace.Debug(ctx, "Ignore banned node in iterate batch get values (stream)", logtrace.Fields{
				logtrace.FieldModule: "dht", "node": node.String(),
			})
			continue
		}

		wg.Add(1)
		go func(node *Node, nodeID string) {
			defer wg.Done()

			if err := sem.Acquire(gctx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			if atomic.LoadInt32(&foundCount) >= int32(needNetwork-alreadyFound) {
				return
			}

			// Prepare request set for this node (only for keys not yet seen)
			indices := fetchMap[nodeID]
			requestKeys := make(map[string]KeyValWithClosest) // hex -> payload placeholder
			for _, idx := range indices {
				if idx < len(hexKeys) {
					if _, loaded := resSeen.Load(hexKeys[idx]); !loaded {
						requestKeys[hexKeys[idx]] = KeyValWithClosest{}
					}
				}
			}
			if len(requestKeys) == 0 {
				return
			}

			decompressedData, err := s.doBatchGetValuesCall(gctx, node, requestKeys)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			// Build hex->base58 for this set
			hexToB58 := make(map[string]string, len(indices))
			for _, idx := range indices {
				if idx < len(hexKeys) {
					hexToB58[hexKeys[idx]] = base58Keys[idx]
				}
			}

			for hexK, v := range decompressedData {
				if len(v.Value) == 0 {
					continue
				}
				if _, loaded := resSeen.LoadOrStore(hexK, struct{}{}); loaded {
					continue
				}
				b58, ok := hexToB58[hexK]
				if !ok {
					continue
				}
				if err := onSymbol(b58, v.Value); err != nil {
					// allow retry from another node
					resSeen.Delete(hexK)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				atomic.AddInt32(&foundCount, 1)
				if atomic.LoadInt32(&foundCount) >= int32(needNetwork-alreadyFound) {
					cancel()
					break
				}
			}
		}(node, nodeID)
	}

	wg.Wait()

	logtrace.Debug(ctx, "Iterate batch get values (stream) done", logtrace.Fields{
		logtrace.FieldModule: "dht", "found_count": atomic.LoadInt32(&foundCount),
	})
	if firstErr != nil {
		logtrace.Error(ctx, "Encountered error in iterate batch get values (stream)", logtrace.Fields{
			logtrace.FieldModule: "dht", "found_count": atomic.LoadInt32(&foundCount), logtrace.FieldError: firstErr.Error(),
		})
	}
	return int(foundCount), firstErr
}

func (s *DHT) fetchAndWriteLocalKeysBatched(
	ctx context.Context,
	hexKeys []string,
	resSeen *sync.Map, // hexKey -> struct{}
	req int32,
	onSymbol func(string, []byte) error, // base58 key -> data
) (int32, error) {
	const batchSize = 5000
	var count int32

	for start := 0; start < len(hexKeys); start += batchSize {
		end := start + batchSize
		if end > len(hexKeys) {
			end = len(hexKeys)
		}
		batchHexKeys := hexKeys[start:end]

		logtrace.Debug(ctx, "Processing batch of local keys (stream)", logtrace.Fields{
			logtrace.FieldModule: "dht", "batch_size": len(batchHexKeys), "total_keys": len(hexKeys),
		})

		localValues, _, batchErr := s.store.RetrieveBatchValues(ctx, batchHexKeys, false)
		if batchErr != nil {
			logtrace.Error(ctx, "Failed to retrieve local batch values", logtrace.Fields{
				logtrace.FieldModule: "dht", logtrace.FieldError: batchErr.Error(),
			})
			// Continue to next batch (same behavior as your fetchAndAddLocalKeys)
			continue
		}

		for i, val := range localValues {
			if len(val) == 0 {
				continue
			}
			hk := batchHexKeys[i]
			if _, loaded := resSeen.LoadOrStore(hk, struct{}{}); loaded {
				continue
			}
			raw, err := hex.DecodeString(hk)
			if err != nil {
				// Skip malformed key; very unlikely because hk came from our own decode
				continue
			}
			b58 := base58.Encode(raw)
			if err := onSymbol(b58, val); err != nil {
				// allow retry later
				resSeen.Delete(hk)
				continue
			}
			count++
			if count >= req {
				return count, nil
			}
		}
	}
	return count, nil
}

func providerIndexForKey(key string, txID string, k int) int {
	// cheap FNV-1a; any stable hash works
	const (
		offset64 = 1469598103934665603
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= prime64
	}
	for i := 0; i < len(txID); i++ {
		h ^= uint64(txID[i])
		h *= prime64
	}
	if k <= 0 {
		return 0
	}
	return int(h % uint64(k))
}

// Iterate does an iterative search through the kademlia network
// - IterativeStore - used to store new information in the kademlia network
// - IterativeFindNode - used to bootstrap the network
// - IterativeFindValue - used to find a value among the network given a key
func (s *DHT) iterate(ctx context.Context, iterativeType int, target []byte, data []byte, typ int) ([]byte, error) {
	if iterativeType == IterateFindValue {
		return s.iterateFindValue(ctx, iterativeType, target)
	}

	// Get task ID from context
	taskID := ""
	if val := ctx.Value(logtrace.CorrelationIDKey); val != nil {
		taskID = fmt.Sprintf("%v", val)
	}

	sKey := hex.EncodeToString(target)

	igList := s.ignorelist.ToNodeList()
	// find the closest contacts for the target node from queries route tables
	nl, _ := s.ht.closestContacts(Alpha, target, igList)
	if len(igList) > 0 {
		logtrace.Debug(ctx, "Closest contacts", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"nodes":              nl.String(),
			"ignored":            s.ignorelist.String(),
		})
	}
	// if no closer node, stop search
	if nl.Len() == 0 {
		return nil, nil
	}
	logtrace.Debug(ctx, "Iterate start", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"task_id":            taskID,
		"type":               iterativeType,
		"target":             sKey,
		"nodes":              nl.String(),
	})

	// keep the closer node
	closestNode := nl.Nodes[0]
	// if it's a find node, reset the refresh timer
	if iterativeType == IterateFindNode {
		hashedTargetID, _ := utils.Blake3Hash(target)
		bucket := s.ht.bucketIndex(s.ht.self.HashedID, hashedTargetID)
		logtrace.Debug(ctx, "Bucket for target", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"target":             sKey,
		})

		// reset the refresh time for the bucket
		s.ht.resetRefreshTime(bucket)
	}

	// According to the Kademlia white paper, after a round of FIND_NODE RPCs
	// fails to provide a node closer than closestNode, we should send a
	// FIND_NODE RPC to all remaining nodes in the node list that have not
	// yet been contacted.
	searchRest := false

	// keep track of nodes contacted
	var contacted = make(map[string]bool)

	// Set a timeout for the iteration process
	timeout := time.After(10 * time.Second) // Quick iteration window

	// Set a maximum number of iterations to prevent indefinite looping
	maxIterations := 5 // Adjust the maximum iterations as needed

	logtrace.Debug(ctx, "Begin iteration", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"task_id":            taskID,
		"key":                sKey,
	})

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("iterate cancelled: %w", ctx.Err())
		case <-timeout:
			logtrace.Debug(ctx, "Iteration timed out", logtrace.Fields{
				logtrace.FieldModule: "p2p",
			})
			return nil, nil
		default:
			// Do the requests concurrently
			responses := s.doMultiWorkers(ctx, iterativeType, target, nl, contacted, searchRest)

			// Handle the responses one by one
			for response := range responses {
				// Add the target node to the bucket
				s.addNode(ctx, response.Sender)

				switch response.MessageType {
				case FindNode, StoreData:
					v, ok := response.Data.(*FindNodeResponse)
					if ok && v.Status.Result == ResultOk {
						if len(v.Closest) > 0 {
							nl.AddNodes(v.Closest)
						}
					}

				default:
					logtrace.Debug(ctx, "Unknown message type", logtrace.Fields{
						logtrace.FieldModule: "dht",
						"type":               response.MessageType,
					})
				}
			}

			// Stop search if no more nodes to contact
			if !searchRest && len(nl.Nodes) == 0 {
				logtrace.Debug(ctx, "Search stopped", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"task_id":            taskID,
					"key":                sKey,
				})
				return nil, nil
			}

			// Sort the nodes in the node list
			nl.Comparator = target
			nl.Sort()

			logtrace.Debug(ctx, "Iterate sorted nodes", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"id":                 base58.Encode(s.ht.self.ID),
				"iterate":            iterativeType,
				"nodes":              nl.String(),
			})

			switch iterativeType {
			case IterateFindNode:
				// If closestNode is unchanged
				if bytes.Equal(nl.Nodes[0].ID, closestNode.ID) || searchRest {
					if !searchRest {
						// Search all the remaining nodes
						searchRest = true
						continue
					}
					return nil, nil
				}
				// Update the closest node
				closestNode = nl.Nodes[0]

			case IterateStore:
				// Store the value to the node list
				if err := s.storeToAlphaNodes(ctx, nl, data, typ, taskID); err != nil {
					logtrace.Error(ctx, "Could not store value to remaining network", logtrace.Fields{
						logtrace.FieldModule: "dht",
						"task_id":            taskID,
						"key":                sKey,
						logtrace.FieldError:  err.Error(),
					})
				}

				return nil, nil
			}

		}
	}
	logtrace.Debug(ctx, "Finish iteration without results", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"task_id":            taskID,
		"key":                sKey,
	})
	return nil, nil
}

func (s *DHT) handleResponses(ctx context.Context, responses <-chan *Message, nl *NodeList) (*NodeList, []byte) {
	for response := range responses {
		s.addNode(ctx, response.Sender)
		switch response.MessageType {
		case FindNode, StoreData:
			v, ok := response.Data.(*FindNodeResponse)
			if ok && v.Status.Result == ResultOk && len(v.Closest) > 0 {
				nl.AddNodes(v.Closest)
			}
		case FindValue:
			v, ok := response.Data.(*FindValueResponse)
			if ok {
				if v.Status.Result == ResultOk && len(v.Value) > 0 {
					logtrace.Debug(ctx, "Iterate found value from network", logtrace.Fields{
						logtrace.FieldModule: "p2p",
					})
					return nl, v.Value
				} else if len(v.Closest) > 0 {
					nl.AddNodes(v.Closest)
				}
			}
		}
	}

	return nl, nil
}

func (s *DHT) iterateFindValue(ctx context.Context, iterativeType int, target []byte) ([]byte, error) {
	// Get task ID from context
	taskID := ""
	if val := ctx.Value(logtrace.CorrelationIDKey); val != nil {
		taskID = fmt.Sprintf("%v", val)
	}

	// for logging, helps with debugging
	sKey := hex.EncodeToString(target)

	// the nodes which are unreachable right now are stored in 'ignore list'- we want to avoid hitting them repeatedly
	igList := s.ignorelist.ToNodeList()

	// nl will have the closest nodes to the target value, it will ignore the nodes in igList
	nl, _ := s.ht.closestContacts(Alpha, target, igList)
	if len(igList) > 0 {
		logtrace.Debug(ctx, "Closest contacts", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"nodes":              nl.String(),
			"ignored":            s.ignorelist.String(),
		})
	}

	// if no nodes are found, return - this is a corner case and should not happen in practice
	if nl.Len() == 0 {
		return nil, nil
	}

	searchRest := false
	// keep track of contacted nodes so that we don't hit them again
	contacted := make(map[string]bool)
	logtrace.Debug(ctx, "Begin iteration", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"task_id":            taskID,
		"key":                sKey,
	})

	var closestNode *Node
	var iterationCount int
	for iterationCount = 0; iterationCount < maxIterations; iterationCount++ {
		logtrace.Debug(ctx, "Begin find value", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"task_id":            taskID,
			"nl":                 nl.Len(),
			"target":             sKey,
			"nodes":              nl.String(),
		})

		if nl.Len() == 0 {
			logtrace.Debug(ctx, "Nodes list length is 0", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"task_id":            taskID,
				"key":                sKey,
				"iteration_count":    iterationCount,
			})
			return nil, nil
		}

		// if the closest node is the same as the last iteration  and we don't want to search rest of nodes, we are done
		if !searchRest && (closestNode != nil && bytes.Equal(nl.Nodes[0].ID, closestNode.ID)) {
			logtrace.Debug(ctx, "Closest node is the same as the last iteration", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"task_id":            taskID,
				"key":                sKey,
				"iteration_count":    iterationCount,
			})
			return nil, nil
		}

		closestNode = nl.Nodes[0]
		responses := s.doMultiWorkers(ctx, iterativeType, target, nl, contacted, searchRest)
		var value []byte
		nl, value = s.handleResponses(ctx, responses, nl)
		if len(value) > 0 {
			return value, nil
		}

		nl.Sort()

		logtrace.Debug(ctx, "Iteration progress", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"task_id":            taskID,
			"key":                sKey,
			"iteration":          iterationCount,
			"nodes":              nl.Len(),
		})
	}

	logtrace.Debug(ctx, "Finished iterations without results", logtrace.Fields{
		logtrace.FieldModule: "p2p",
		"task_id":            taskID,
		"key":                sKey,
	})
	return nil, nil
}

func (s *DHT) sendReplicateData(ctx context.Context, n *Node, request *ReplicateDataRequest) (*ReplicateDataResponse, error) {
	// new a request message
	reqMsg := s.newMessage(Replicate, n, request)

	rspMsg, err := s.network.Call(ctx, reqMsg, true)
	if err != nil {
		return nil, errors.Errorf("replicate network call: %w", err)
	}

	response, ok := rspMsg.Data.(*ReplicateDataResponse)
	if !ok {
		return nil, errors.New("invalid ReplicateDataResponse")
	}

	return response, nil
}

func (s *DHT) sendStoreData(ctx context.Context, n *Node, request *StoreDataRequest) (*StoreDataResponse, error) {
	// new a request message
	reqMsg := s.newMessage(StoreData, n, request)

	rspMsg, err := s.network.Call(ctx, reqMsg, false)
	if err != nil {
		return nil, errors.Errorf("network call: %w", err)
	}

	response, ok := rspMsg.Data.(*StoreDataResponse)
	if !ok {
		return nil, errors.New("invalid StoreDataResponse")
	}

	return response, nil
}

// add a node into the appropriate k bucket, return the removed node if it's full
func (s *DHT) addNode(ctx context.Context, node *Node) *Node {
	// Minimum-version gating: reject nodes below configured minimum.
	peerVer := ""
	if node != nil {
		peerVer = node.Version
	}
	if minRequired, tooOld := versionTooOld(peerVer); tooOld {
		fields := logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"min_required":       minRequired,
			"peer_version":       strings.TrimSpace(peerVer),
		}
		if node != nil {
			fields["peer"] = node.String()
		}
		logtrace.Debug(ctx, "Rejecting node: peer below minimum version", fields)
		return nil
	}
	// Allow localhost for integration testing
	isIntegrationTest := os.Getenv("INTEGRATION_TEST") == "true"
	if node.IP == "" || node.IP == "0.0.0.0" || (!isIntegrationTest && node.IP == "127.0.0.1") {
		logtrace.Info(ctx, "Rejecting node: invalid IP", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"ip":                 node.IP,
			"node":               node.String(),
			"integration_test":   isIntegrationTest,
		})
		return nil
	}
	if bytes.Equal(node.ID, s.ht.self.ID) {
		logtrace.Info(ctx, "Rejecting node: is self", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"node":               node.String(),
		})
		return nil
	}
	node.SetHashedID()

	idx := s.ht.bucketIndex(s.ht.self.HashedID, node.HashedID)

	// already in table? refresh to MRU
	if s.ht.hasBucketNode(idx, node.HashedID) {
		s.ht.refreshNode(node.HashedID)
		return nil
	}

	s.ht.mutex.Lock()
	defer s.ht.mutex.Unlock()

	b := s.ht.routeTable[idx]
	if len(b) < K {
		s.ht.routeTable[idx] = append(b, node)
		return nil
	}

	// Bucket full:
	lru := b[0]
	// If we already know the LRU is bad, replace immediately.
	if s.ignorelist.Banned(lru) {
		s.ignorelist.IncrementCount(lru) // optional: nudge the counter
		b[0] = node
		s.ht.routeTable[idx] = b
		return lru
	}

	// Otherwise keep the resident, drop the newcomer.
	// (The periodic ping/health loop will evict bad nodes later.)
	return nil
}

// NClosestNodes get n closest nodes to a key string
func (s *DHT) NClosestNodes(_ context.Context, n int, key string, ignores ...*Node) []*Node {
	list := s.ignorelist.ToNodeList()
	ignores = append(ignores, list...)
	nodeList, _ := s.ht.closestContacts(n, base58.Decode(key), ignores)

	return nodeList.Nodes
}

// NClosestNodesWithIncludingNodelist get n closest nodes to a key string with including node list
func (s *DHT) NClosestNodesWithIncludingNodelist(_ context.Context, n int, key string, ignores, includeNodeList []*Node) []*Node {
	list := s.ignorelist.ToNodeList()
	ignores = append(ignores, list...)

	for i := 0; i < len(includeNodeList); i++ {
		includeNodeList[i].SetHashedID()
	}

	nodeList := s.ht.closestContactsWithIncludingNodeList(n, base58.Decode(key), ignores, includeNodeList)

	return nodeList.Nodes
}

// LocalStore the data into the network
func (s *DHT) LocalStore(ctx context.Context, key string, data []byte) (string, error) {
	decoded := base58.Decode(key)
	if len(decoded) != B/8 {
		return "", fmt.Errorf("invalid key: %v", key)
	}

	// store the key to queries storage
	if err := s.retryStore(ctx, decoded, data, 0); err != nil {
		logtrace.Error(ctx, "Local data store failure after retries", logtrace.Fields{
			logtrace.FieldModule: "dht",
			logtrace.FieldError:  err.Error(),
		})
		return "", fmt.Errorf("retry store data to queries storage: %v", err)
	}

	return key, nil
}

func (s *DHT) storeToAlphaNodes(ctx context.Context, nl *NodeList, data []byte, typ int, taskID string) error {
	storeCount := int32(0)
	alphaCh := make(chan bool, Alpha)

	// Launch up to Alpha requests in parallel (non-banned only)
	launched := 0
	for i := 0; i < Alpha && i < nl.Len(); i++ {
		n := nl.Nodes[i]
		if s.ignorelist.Banned(n) {
			continue
		}
		launched++
		go func(n *Node) {
			request := &StoreDataRequest{Data: data, Type: typ}
			response, err := s.sendStoreData(ctx, n, request)
			if err != nil {
				logtrace.Error(ctx, "Send store data failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"node":               n.String(),
					"task_id":            taskID,
					logtrace.FieldError:  err.Error(),
				})
				alphaCh <- false
				return
			}
			if response.Status.Result != ResultOk {
				logtrace.Error(ctx, "Reply store data failed", logtrace.Fields{
					logtrace.FieldModule: "p2p",
					"node":               n.String(),
					"task_id":            taskID,
					logtrace.FieldError:  response.Status.ErrMsg,
				})
				alphaCh <- false
				return
			}
			atomic.AddInt32(&storeCount, 1)
			alphaCh <- true
		}(n)
	}

	// Collect only what we launched
	for i := 0; i < launched; i++ {
		<-alphaCh
	}

	// If needed, continue sequentially
	finalStoreCount := atomic.LoadInt32(&storeCount)
	for i := Alpha; i < nl.Len() && finalStoreCount < int32(Alpha); i++ {
		n := nl.Nodes[i]
		if s.ignorelist.Banned(n) {
			logtrace.Debug(ctx, "Ignore banned node during sequential store", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"node":               n.String(),
				"task_id":            taskID,
			})
			continue
		}
		request := &StoreDataRequest{Data: data, Type: typ}
		response, err := s.sendStoreData(ctx, n, request)
		if err != nil {
			logtrace.Error(ctx, "Send store data failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"node":               n.String(),
				"task_id":            taskID,
				logtrace.FieldError:  err.Error(),
			})
			continue
		}
		if response.Status.Result != ResultOk {
			logtrace.Error(ctx, "Reply store data failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"node":               n.String(),
				"task_id":            taskID,
				logtrace.FieldError:  response.Status.ErrMsg,
			})
			continue
		}
		finalStoreCount++
	}

	skey, _ := utils.Blake3Hash(data)

	if finalStoreCount >= int32(Alpha) {
		logtrace.Debug(ctx, "Store data to alpha nodes success", logtrace.Fields{
			logtrace.FieldModule: "dht",
			"task_id":            taskID,
			"len_total_nodes":    nl.Len(),
			"skey":               hex.EncodeToString(skey),
		})
		nl.TopN(Alpha)
		return nil
	}

	logtrace.Debug(ctx, "Store data to alpha nodes failed", logtrace.Fields{
		logtrace.FieldModule: "dht",
		"task_id":            taskID,
		"store_count":        finalStoreCount,
		"skey":               hex.EncodeToString(skey),
	})
	return fmt.Errorf("store data to alpha nodes failed, only %d nodes stored", finalStoreCount)
}

// remove node from appropriate k bucket
func (s *DHT) removeNode(ctx context.Context, node *Node) {
	// ensure this is not itself address
	if bytes.Equal(node.ID, s.ht.self.ID) {
		logtrace.Debug(ctx, "Trying to remove itself", logtrace.Fields{
			logtrace.FieldModule: "p2p",
		})
		return
	}
	node.SetHashedID()

	index := s.ht.bucketIndex(s.ht.self.HashedID, node.HashedID)

	if removed := s.ht.RemoveNode(index, node.HashedID); !removed {
		logtrace.Error(ctx, "Remove node not found in bucket", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"node":               node.String(),
			"bucket":             index,
		})
	} else {
		logtrace.Debug(ctx, "Removed node from bucket success", logtrace.Fields{
			logtrace.FieldModule: "p2p",
			"node":               node.String(),
			"bucket":             index,
		})
	}
}

func (s *DHT) addKnownNodes(ctx context.Context, nodes []*Node, knownNodes map[string]*Node) {
	for _, node := range nodes {
		if _, ok := knownNodes[string(node.ID)]; ok {
			continue
		}

		// Reject bind/local/link-local/private/bogus addresses early
		// Allow loopback/private addresses during integration testing
		isIntegrationTest := os.Getenv("INTEGRATION_TEST") == "true"
		if ip := net.ParseIP(node.IP); ip != nil {
			// Always reject unspecified. For integration tests, allow loopback/link-local.
			if ip.IsUnspecified() || (!isIntegrationTest && (ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())) {
				s.ignorelist.IncrementCount(node)
				continue
			}
			// If this overlay is public, also reject RFC1918/CGNAT. Allow during integration tests.
			if !isIntegrationTest && ip.IsPrivate() {
				s.ignorelist.IncrementCount(node)
				continue
			}
		} else {
			// Hostname: basic sanity (must look like a FQDN)
			if !strings.Contains(node.IP, ".") {
				if !(isIntegrationTest && strings.EqualFold(node.IP, "localhost")) {
					s.ignorelist.IncrementCount(node)
					continue
				}
			}
		}

		node.SetHashedID()
		knownNodes[string(node.ID)] = node

		s.addNode(ctx, node)
		bucket := s.ht.bucketIndex(s.ht.self.HashedID, node.HashedID)
		s.ht.resetRefreshTime(bucket)
	}
}

// IterateBatchStore performs the network store and returns (successRatePct, requestCount, error).
//
// Request count is computed as the number of per-node batch store RPCs attempted
// during this run; success rate is successful responses divided by this count.
// If the success rate is below `minimumDataStoreSuccessRate`, an error is
// returned alongside the measured rate and request count.
func (s *DHT) IterateBatchStore(ctx context.Context, values [][]byte, typ int, id string) error {
	globalClosestContacts := make(map[string]*NodeList)
	knownNodes := make(map[string]*Node)
	hashes := make([][]byte, len(values))

	{
		f := logtrace.Fields{logtrace.FieldModule: "dht", "task_id": id, "keys": len(values), "len_nodes": len(s.ht.nodes()), logtrace.FieldRole: "client"}
		if o := logtrace.OriginFromContext(ctx); o != "" {
			f[logtrace.FieldOrigin] = o
		}
		logtrace.Info(ctx, "dht: batch store start", f)
	}
	for i := 0; i < len(values); i++ {
		target, _ := utils.Blake3Hash(values[i])
		hashes[i] = target
		top6 := s.ht.closestContactsWithIncludingNode(Alpha, target, s.ignorelist.ToNodeList(), nil)

		globalClosestContacts[base58.Encode(target)] = top6
		// log.WithContext(ctx).WithField("top 6", top6).Info("iterate batch store begin")
		s.addKnownNodes(ctx, top6.Nodes, knownNodes)
	}

	storageMap := make(map[string][]int) // This will store the index of the data in the values array that needs to be stored to the node
	for i := 0; i < len(hashes); i++ {
		storageNodes := globalClosestContacts[base58.Encode(hashes[i])]
		for j := 0; j < len(storageNodes.Nodes); j++ {
			storageMap[string(storageNodes.Nodes[j].ID)] = append(storageMap[string(storageNodes.Nodes[j].ID)], i)
		}
	}

	requests := 0
	successful := 0

	logtrace.Debug(ctx, "Iterate batch store: dispatching to nodes", logtrace.Fields{"task_id": id, "nodes": len(knownNodes)})
	storeResponses := s.batchStoreNetwork(ctx, values, knownNodes, storageMap, typ)
	for response := range storeResponses {
		requests++
		var nodeAddr string
		if response.Receiver != nil {
			nodeAddr = response.Receiver.String()
		} else if response.Message != nil && response.Message.Sender != nil {
			nodeAddr = response.Message.Sender.String()
		}

		errMsg := ""
		if response.Error != nil {
			errMsg = response.Error.Error()
			logtrace.Error(ctx, "Batch store failed on a node", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"node":               nodeAddr,
				logtrace.FieldError:  errMsg,
			})
		}

		if response.Message != nil {
			if v, ok := response.Message.Data.(*StoreDataResponse); ok {
				if v.Status.Result == ResultOk {
					successful++
				} else {
					if v.Status.ErrMsg != "" {
						errMsg = v.Status.ErrMsg
					}
					logtrace.Error(ctx, "Batch store to node failed", logtrace.Fields{
						logtrace.FieldModule: "dht",
						"err":                errMsg,
						"task_id":            id,
						"node":               nodeAddr,
					})
				}
			}
		}

		// per-node store metrics removed; logs retained

	}

	if requests > 0 {
		s.metrics.RecordStoreSuccess(requests, successful)

		successRate := float64(successful) / float64(requests) * 100
		if successRate >= minimumDataStoreSuccessRate {
			logtrace.Info(ctx, "dht: batch store ok", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"task_id":            id,
				"success_rate":       fmt.Sprintf("%.2f%%", successRate),
			})
			return nil
		} else {
			logtrace.Info(ctx, "dht: batch store below threshold", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"task_id":            id,
				"success_rate":       fmt.Sprintf("%.2f%%", successRate),
			})
			return fmt.Errorf("failed to achieve desired success rate, only: %.2f%% successful", successRate)
		}

	}

	return fmt.Errorf("no store operations were performed")
}

func (s *DHT) batchStoreNetwork(ctx context.Context, values [][]byte, nodes map[string]*Node, storageMap map[string][]int, typ int) chan *MessageWithError {
	responses := make(chan *MessageWithError, len(nodes))
	maxStore := maxConcurrentNetworkStoreCalls
	if ln := len(nodes); ln < maxStore {
		maxStore = ln
	}
	if maxStore < 1 {
		maxStore = 1
	}
	semaphore := make(chan struct{}, maxStore)

	var wg sync.WaitGroup

	for key, node := range nodes {
		logtrace.Debug(ctx, "Preparing batch store to node", logtrace.Fields{logtrace.FieldModule: "dht", "node": node.String()})
		if s.ignorelist.Banned(node) {
			logtrace.Debug(ctx, "Ignoring banned node in batch store network call", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"node":               node.String(),
			})
			s.metrics.IncHotPathBannedSkip()
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore
		go func(receiver *Node, key string) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			select {
			case <-ctx.Done():
				responses <- &MessageWithError{Error: ctx.Err(), Receiver: receiver}
				return
			default:
				callStart := time.Now()
				keysToStore := storageMap[key]
				toStore := make([][]byte, len(keysToStore))
				totalBytes := 0
				for i, idx := range keysToStore {
					toStore[i] = values[idx]
					totalBytes += len(values[idx])
				}

				{
					f := logtrace.Fields{logtrace.FieldModule: "dht", "node": receiver.String(), "keys": len(toStore), "size_mb": utils.BytesIntToMB(totalBytes), logtrace.FieldRole: "client"}
					if o := logtrace.OriginFromContext(ctx); o != "" {
						f[logtrace.FieldOrigin] = o
					}
					logtrace.Info(ctx, "dht: batch store RPC send", f)
				}

				// Skip empty payloads: avoid sending empty store RPCs and do not record no-op metrics.
				if len(toStore) == 0 {
					logtrace.Debug(ctx, "Skipping store RPC with empty payload", logtrace.Fields{
						logtrace.FieldModule: "dht",
						"node":               receiver.String(),
					})
					return
				}

				data := &BatchStoreDataRequest{Data: toStore, Type: typ}
				request := s.newMessage(BatchStoreData, receiver, data)
				response, err := s.network.Call(ctx, request, false)
				dur := time.Since(callStart).Milliseconds()
				if err != nil {
					if !isLocalCancel(err) {
						s.ignorelist.IncrementCount(receiver)
						s.metrics.IncHotPathBanIncr()
					}

					logtrace.Error(ctx, "RPC BatchStoreData failed", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err.Error(), "node": receiver.String(), "ms": dur})
					responses <- &MessageWithError{Error: err, Message: response, KeysCount: len(toStore), Receiver: receiver, DurationMS: dur}
					return
				}

				{
					f := logtrace.Fields{logtrace.FieldModule: "p2p", "node": receiver.String(), "keys": len(toStore), "ms": dur, logtrace.FieldRole: "client"}
					if o := logtrace.OriginFromContext(ctx); o != "" {
						f[logtrace.FieldOrigin] = o
					}
					logtrace.Info(ctx, "dht: batch store RPC ok", f)
				}
				responses <- &MessageWithError{Message: response, KeysCount: len(toStore), Receiver: receiver, DurationMS: dur}
			}
		}(node, key)
	}

	wg.Wait()
	close(responses)

	return responses
}

func (s *DHT) batchFindNode(ctx context.Context, payload [][]byte, nodes map[string]*Node, contacted map[string]bool, txid string) (chan *MessageWithError, bool) {
	logtrace.Debug(ctx, "Batch find node begin", logtrace.Fields{
		logtrace.FieldModule: "dht",
		"task_id":            txid,
		"nodes_count":        len(nodes),
	})

	responses := make(chan *MessageWithError, len(nodes))
	atleastOneContacted := false
	var wg sync.WaitGroup
	maxInFlight := 64
	if ln := len(nodes); ln < maxInFlight {
		maxInFlight = ln
	}
	if maxInFlight < 1 {
		maxInFlight = 1
	}
	semaphore := make(chan struct{}, maxInFlight)

	for _, node := range nodes {
		if _, ok := contacted[string(node.ID)]; ok {
			continue
		}
		if s.ignorelist.Banned(node) {
			logtrace.Debug(ctx, "Ignoring banned node in batch find call", logtrace.Fields{
				logtrace.FieldModule: "dht",
				"node":               node.String(),
				"txid":               txid,
			})
			s.metrics.IncHotPathBannedSkip()
			continue
		}

		contacted[string(node.ID)] = true
		atleastOneContacted = true
		wg.Add(1)
		semaphore <- struct{}{}

		go func(receiver *Node) {
			defer wg.Done()
			defer func() { <-semaphore }()

			select {
			case <-ctx.Done():
				responses <- &MessageWithError{Error: ctx.Err()}
				return
			default:
				data := &BatchFindNodeRequest{HashedTarget: payload}
				request := s.newMessage(BatchFindNode, receiver, data)
				response, err := s.network.Call(ctx, request, false)
				if err != nil {
					if !isLocalCancel(err) {
						s.ignorelist.IncrementCount(receiver)
						s.metrics.IncHotPathBanIncr()
					}

					logtrace.Warn(ctx, "Batch find node network call request failed", logtrace.Fields{
						logtrace.FieldModule: "dht",
						"node":               receiver.String(),
						"txid":               txid,
						logtrace.FieldError:  err.Error(),
					})
					responses <- &MessageWithError{Error: err, Message: response}
					return
				}

				responses <- &MessageWithError{Message: response}
			}
		}(node)
	}
	wg.Wait()
	close(responses)
	logtrace.Debug(ctx, "Batch find node done", logtrace.Fields{
		logtrace.FieldModule: "dht",
		"nodes_count":        len(nodes),
		"len_resp":           len(responses),
		"txid":               txid,
	})

	return responses, atleastOneContacted
}

// addKnownNodesSafe wraps addKnownNodes with a mutex to avoid concurrent writes to knownNodes.
func (s *DHT) addKnownNodesSafe(ctx context.Context, nodes []*Node, knownNodes map[string]*Node, mu *sync.Mutex) {
	mu.Lock()
	s.addKnownNodes(ctx, nodes, knownNodes)
	mu.Unlock()
}

// ---- DHT metrics -----------------------------------------------------------

type StoreSuccessPoint struct {
	Time        time.Time `json:"time"`
	Requests    int       `json:"requests"`
	Successful  int       `json:"successful"`
	SuccessRate float64   `json:"success_rate"`
}

type BatchRetrievePoint struct {
	Time       time.Time     `json:"time"`
	Keys       int           `json:"keys"`
	Required   int           `json:"required"`
	FoundLocal int           `json:"found_local"`
	FoundNet   int           `json:"found_network"`
	Duration   time.Duration `json:"duration"`
}

type DHTMetricsSnapshot struct {
	// rolling windows
	StoreSuccessRecent  []StoreSuccessPoint  `json:"store_success_recent"`
	BatchRetrieveRecent []BatchRetrievePoint `json:"batch_retrieve_recent"`

	// hot path counters
	HotPathBannedSkips   int64 `json:"hot_path_banned_skips"`
	HotPathBanIncrements int64 `json:"hot_path_ban_increments"`
}

type DHTMetrics struct {
	mu sync.Mutex

	// bounded windows (most recent first)
	storeSuccess  []StoreSuccessPoint
	batchRetrieve []BatchRetrievePoint
	maxWindow     int

	// hot path counters
	hotPathBannedSkips   atomic.Int64
	hotPathBanIncrements atomic.Int64
}

func (m *DHTMetrics) init() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxWindow == 0 {
		m.maxWindow = 48 // e.g. last 48 events (~several hours)
	}
}

func (m *DHTMetrics) trimStore() {
	if len(m.storeSuccess) > m.maxWindow {
		m.storeSuccess = m.storeSuccess[:m.maxWindow]
	}
}
func (m *DHTMetrics) trimRetrieve() {
	if len(m.batchRetrieve) > m.maxWindow {
		m.batchRetrieve = m.batchRetrieve[:m.maxWindow]
	}
}

func (m *DHTMetrics) RecordStoreSuccess(req, succ int) {
	m.init()
	rate := 0.0
	if req > 0 {
		rate = (float64(succ) / float64(req)) * 100.0
	}
	m.mu.Lock()
	m.storeSuccess = append([]StoreSuccessPoint{{
		Time:        time.Now().UTC(),
		Requests:    req,
		Successful:  succ,
		SuccessRate: rate,
	}}, m.storeSuccess...)
	m.trimStore()
	m.mu.Unlock()
}

func (m *DHTMetrics) RecordBatchRetrieve(keys, required, foundLocal, foundNet int, dur time.Duration) {
	m.init()
	m.mu.Lock()
	m.batchRetrieve = append([]BatchRetrievePoint{{
		Time:       time.Now().UTC(),
		Keys:       keys,
		Required:   required,
		FoundLocal: foundLocal,
		FoundNet:   foundNet,
		Duration:   dur,
	}}, m.batchRetrieve...)
	m.trimRetrieve()
	m.mu.Unlock()
}

func (m *DHTMetrics) IncHotPathBannedSkip() { m.hotPathBannedSkips.Add(1) }
func (m *DHTMetrics) IncHotPathBanIncr()    { m.hotPathBanIncrements.Add(1) }

func (m *DHTMetrics) Snapshot() DHTMetricsSnapshot {
	m.init()
	m.mu.Lock()
	defer m.mu.Unlock()
	// shallow copies of bounded windows
	storeCopy := append([]StoreSuccessPoint(nil), m.storeSuccess...)
	retrCopy := append([]BatchRetrievePoint(nil), m.batchRetrieve...)
	return DHTMetricsSnapshot{
		StoreSuccessRecent:   storeCopy,
		BatchRetrieveRecent:  retrCopy,
		HotPathBannedSkips:   m.hotPathBannedSkips.Load(),
		HotPathBanIncrements: m.hotPathBanIncrements.Load(),
	}
}
