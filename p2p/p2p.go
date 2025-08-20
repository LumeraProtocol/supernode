package p2p

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/cloud.go"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/meta"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/sqlite"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/btcsuite/btcutil/base58"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

const (
	logPrefix = "p2p"
	// B is the number of bits in a Blake3 hash
	B = 256
)

var (
	defaultReplicateInterval = time.Second * 3600
	defaultRepublishInterval = time.Second * 3600 * 24
)

// P2P represents the p2p service.
type P2P interface {
	Client

	// Run the node of distributed hash table
	Run(ctx context.Context) error
}

// p2p structure to implements interface
type p2p struct {
	store        kademlia.Store // the store for kademlia network
	metaStore    kademlia.MetaStore
	dht          *kademlia.DHT // the kademlia network
	config       *Config       // the service configuration
	running      bool          // if the kademlia network is ready
	lumeraClient lumera.Client
	keyring      keyring.Keyring // Add the keyring field
	rqstore      rqstore.Store
}

// Run the kademlia network
func (s *p2p) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
			if err := s.run(ctx); err != nil {
				if utils.IsContextErr(err) {
					return err
				}

				logtrace.Error(ctx, "failed to run kadmelia, retrying.", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
			} else {
				logtrace.Info(ctx, "kadmelia started successfully", logtrace.Fields{logtrace.FieldModule: "p2p"})
				return nil
			}
		}
	}
}

// run the kademlia network
func (s *p2p) run(ctx context.Context) error {

	logtrace.Info(ctx, "Running  kademlia network", logtrace.Fields{logtrace.FieldModule: "p2p"})
	// configure the kademlia dht for p2p service
	if err := s.configure(ctx); err != nil {
		return errors.Errorf("configure kademlia dht: %w", err)
	}

	// close the store of kademlia network
	defer s.store.Close(ctx)

	// start the node for kademlia network
	if err := s.dht.Start(ctx); err != nil {
		logtrace.Error(ctx, "failed to start kademlia network", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
		return errors.Errorf("start the kademlia network: %w", err)
	}

	if err := s.dht.ConfigureBootstrapNodes(ctx, s.config.BootstrapNodes); err != nil {
		logtrace.Error(ctx, "failed to configure bootstrap nodes", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
		logtrace.Error(ctx, "failed to get bootstap ip", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
	}

	// join the kademlia network if bootstrap nodes is set
	if err := s.dht.Bootstrap(ctx, s.config.BootstrapNodes); err != nil {
		// stop the node for kademlia network
		s.dht.Stop(ctx)
		return errors.Errorf("bootstrap the node: %w", err)
	}
	s.running = true

	logtrace.Info(ctx, "p2p service is started", logtrace.Fields{})

	// block until context is done
	<-ctx.Done()

	// stop the node for kademlia network
	s.dht.Stop(ctx)

	logtrace.Info(ctx, "p2p service is stopped", logtrace.Fields{})
	return nil
}

// Store store data into the kademlia network
func (s *p2p) Store(ctx context.Context, data []byte, typ int) (string, error) {

	if !s.running {
		return "", errors.New("p2p service is not running")
	}

	return s.dht.Store(ctx, data, typ)
}

// StoreBatch will store a batch of values with their Blake3 hash as the key
func (s *p2p) StoreBatch(ctx context.Context, data [][]byte, typ int, taskID string) error {

	if !s.running {
		return errors.New("p2p service is not running")
	}

	return s.dht.StoreBatch(ctx, data, typ, taskID)
}

// Retrieve retrive the data from the kademlia network
func (s *p2p) Retrieve(ctx context.Context, key string, localOnly ...bool) ([]byte, error) {

	if !s.running {
		return nil, errors.New("p2p service is not running")
	}

	return s.dht.Retrieve(ctx, key, localOnly...)
}

// BatchRetrieve retrive the data from the kademlia network
func (s *p2p) BatchRetrieve(ctx context.Context, keys []string, reqCount int, txID string, localOnly ...bool) (map[string][]byte, error) {

	if !s.running {
		return nil, errors.New("p2p service is not running")
	}

	return s.dht.BatchRetrieve(ctx, keys, int32(reqCount), txID, localOnly...)
}

// Delete delete key in queries node
func (s *p2p) Delete(ctx context.Context, key string) error {

	if !s.running {
		return errors.New("p2p service is not running")
	}

	return s.dht.Delete(ctx, key)
}

// Stats return status of p2p
func (s *p2p) Stats(ctx context.Context) (map[string]interface{}, error) {
	retStats := map[string]interface{}{}
	dhtStats, err := s.dht.Stats(ctx)
	if err != nil {
		return nil, err
	}

	retStats["dht"] = dhtStats
	retStats["config"] = s.config

	// get free space of current kademlia folder
	diskUse, err := utils.DiskUsage(s.config.DataDir)
	if err != nil {
		return nil, errors.Errorf("get disk info failed: %w", err)
	}

	retStats["disk-info"] = &diskUse
	return retStats, nil
}

// NClosestNodes returns a list of n closest masternode to a given string
func (s *p2p) NClosestNodes(ctx context.Context, n int, key string, ignores ...string) []string {
	var ret = make([]string, 0)
	var ignoreNodes = make([]*kademlia.Node, len(ignores))
	for idx, node := range ignores {
		ignoreNodes[idx] = &kademlia.Node{ID: []byte(node)}
	}
	nodes := s.dht.NClosestNodes(ctx, n, key, ignoreNodes...)
	for _, node := range nodes {
		ret = append(ret, string(node.ID))
	}

	logtrace.Debug(ctx, "closest nodes retrieved", logtrace.Fields{"no_of_closest_nodes": n, "file_hash": key, "closest_nodes": ret})
	return ret
}

func (s *p2p) NClosestNodesWithIncludingNodeList(ctx context.Context, n int, key string, ignores, nodesToInclude []string) []string {
	var ret = make([]string, 0)
	var ignoreNodes = make([]*kademlia.Node, len(ignores))
	for idx, node := range ignores {
		ignoreNodes[idx] = &kademlia.Node{ID: []byte(node)}
	}

	var includeNodeList = make([]*kademlia.Node, len(nodesToInclude))
	for idx, node := range nodesToInclude {
		includeNodeList[idx] = &kademlia.Node{ID: []byte(node)}
	}

	nodes := s.dht.NClosestNodesWithIncludingNodelist(ctx, n, key, ignoreNodes, includeNodeList)
	for _, node := range nodes {
		ret = append(ret, string(node.ID))
	}

	logtrace.Debug(ctx, "closest nodes retrieved", logtrace.Fields{"no_of_closest_nodes": n, "file_hash": key, "closest_nodes": ret})
	return ret
}

// configure the distributed hash table for p2p service
func (s *p2p) configure(ctx context.Context) error {
	// new the queries storage
	kadOpts := &kademlia.Options{
		LumeraClient:   s.lumeraClient,
		Keyring:        s.keyring, // Pass the keyring
		BootstrapNodes: []*kademlia.Node{},
		IP:             s.config.ListenAddress,
		Port:           s.config.Port,
		ID:             []byte(s.config.ID),
	}

	if len(kadOpts.ID) == 0 {
		errors.Errorf("node id is empty")
	}

	// new a kademlia distributed hash table
	dht, err := kademlia.NewDHT(ctx, s.store, s.metaStore, kadOpts, s.rqstore)

	if err != nil {
		return errors.Errorf("new kademlia dht: %w", err)
	}
	s.dht = dht

	return nil
}

// New returns a new p2p instance.
func New(ctx context.Context, config *Config, lumeraClient lumera.Client, kr keyring.Keyring, rqstore rqstore.Store, cloud cloud.Storage, mst *sqlite.MigrationMetaStore) (P2P, error) {
	store, err := sqlite.NewStore(ctx, config.DataDir, cloud, mst)
	if err != nil {
		return nil, errors.Errorf("new kademlia store: %w", err)
	}

	meta, err := meta.NewStore(ctx, config.DataDir)
	if err != nil {
		return nil, errors.Errorf("new kademlia meta store: %w", err)
	}

	return &p2p{
		store:        store,
		metaStore:    meta,
		config:       config,
		lumeraClient: lumeraClient,
		keyring:      kr, // Store the keyring
		rqstore:      rqstore,
	}, nil
}

// LocalStore store data into the kademlia network
func (s *p2p) LocalStore(ctx context.Context, key string, data []byte) (string, error) {

	if !s.running {
		return "", errors.New("p2p service is not running")
	}

	return s.dht.LocalStore(ctx, key, data)
}

// DisableKey adds key to disabled keys list - It takes in a B58 encoded Blake3 hash
func (s *p2p) DisableKey(ctx context.Context, b58EncodedHash string) error {
	decoded := base58.Decode(b58EncodedHash)
	if len(decoded) != B/8 {
		return fmt.Errorf("invalid key: %v", b58EncodedHash)
	}

	return s.metaStore.Store(ctx, decoded)
}

// EnableKey removes key from disabled list - It takes in a B58 encoded Blake3 hash
func (s *p2p) EnableKey(ctx context.Context, b58EncodedHash string) error {
	decoded := base58.Decode(b58EncodedHash)
	if len(decoded) != B/8 {
		return fmt.Errorf("invalid key: %v", b58EncodedHash)
	}

	s.metaStore.Delete(ctx, decoded)

	return nil
}

// GetLocalKeys returns a list of all keys stored locally
func (s *p2p) GetLocalKeys(ctx context.Context, from *time.Time, to time.Time) ([]string, error) {
	if from == nil {
		fromTime, err := s.store.GetOwnCreatedAt(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get own created at: %w", err)
		}

		from = &fromTime
	}

	keys, err := s.store.GetLocalKeys(*from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to get queries keys: %w", err)
	}

	retkeys := make([]string, len(keys))
	for i := 0; i < len(keys); i++ {
		str, err := hex.DecodeString(keys[i])
		if err != nil {
			logtrace.Error(ctx, "replicate failed to hex decode key", logtrace.Fields{"key": keys[i]})
			continue
		}

		retkeys[i] = base58.Encode(str)
	}

	return retkeys, nil
}
