package p2pdemo

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/common/storage/rqstore"
	"github.com/LumeraProtocol/supernode/common/utils"
	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/cosmos/btcutil/base58"
	"github.com/stretchr/testify/require"
)

func TestP2PBasicIntegration(t *testing.T) {
	log.Println("Starting P2P test...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Println("Setting up P2P nodes...")
	services, rqStores, err := SetupTestP2PNodes(ctx)
	require.NoError(t, err)
	log.Printf("Setup complete. Got %d services and %d stores\n", len(services), len(rqStores))

	defer func() {
		log.Println("Closing RQ stores...")
		for i, store := range rqStores {
			log.Printf("Closing store %d", i)
			store.Close()
		}
	}()

	defer func() {
		log.Println("Cleaning up test data directories...")

		// Remove the entire data directory and its contents
		if err := os.RemoveAll("./data"); err != nil {
			log.Printf("Warning: Failed to cleanup test data directory: %v", err)
		} else {
			log.Println("Successfully cleaned up test data directories")
		}
	}()
	log.Println("Waiting 5 seconds for nodes to start and connect...")
	time.Sleep(5 * time.Second)

	t.Run("Single Store and Retrieve", func(t *testing.T) {
		log.Println("\n=== Single Store and Retrieve Test ===")
		testData := []byte("test data")
		log.Printf("Test data: %s", testData)

		log.Println("Storing data on first node...")
		key, err := services[0].Store(ctx, testData, 0)
		require.NoError(t, err)
		log.Printf("Stored data with key: %s", key)

		log.Println("Waiting 2 seconds for replication...")
		time.Sleep(2 * time.Second)

		log.Println("Testing retrieval from all nodes...")
		for i, service := range services {
			log.Printf("Retrieving from node %d...", i)
			retrieved, err := service.Retrieve(ctx, key)
			if err != nil {
				log.Printf("Error retrieving from node %d: %v", i, err)
			} else {
				log.Printf("Node %d retrieved: %s", i, retrieved)
			}
			require.NoError(t, err)
		}
	})

	t.Run("Batch Store and Retrieve", func(t *testing.T) {
		batchSize := 5
		batchData := make([][]byte, batchSize)
		var expectedKeys []string

		for i := 0; i < batchSize; i++ {
			data := []byte(fmt.Sprintf("batch data %d", i))
			batchData[i] = data
			// Use the same hashing algorithm as the store
			hash, _ := utils.Sha3256hash(data)
			key := base58.Encode(hash)
			expectedKeys = append(expectedKeys, key)
			log.Printf("Batch data %d: %s, Expected key: %s", i, data, key)
		}

		taskID := "test-task-1"

		// Add debug logging
		log.Printf("Storing batch with keys: %v", expectedKeys)
		err := services[0].StoreBatch(ctx, batchData, 0, taskID)
		require.NoError(t, err)

		// Add immediate verification
		for _, key := range expectedKeys {
			data, err := services[0].Retrieve(ctx, key)
			if err != nil {
				t.Logf("Failed to retrieve key %s: %v", key, err)
			} else {
				t.Logf("Successfully retrieved key %s: %s", key, string(data))
			}
		}

		// Now try batch retrieve
		retrieved, err := services[0].BatchRetrieve(ctx, expectedKeys, batchSize, taskID)
		require.NoError(t, err)
		require.Equal(t, batchSize, len(retrieved), "Expected %d items, got %d", batchSize, len(retrieved))

		// Verify data matches
		for key, data := range retrieved {
			log.Printf("Retrieved key %s: %s", key, data)
			found := false
			for _, original := range batchData {
				if bytes.Equal(data, original) {
					found = true
					break
				}
			}
			require.True(t, found, "Retrieved data not found in original batch")
		}
	})

	log.Println("\nTest complete!")
}

// SetupTestP2PNodes initializes and starts a test P2P network with multiple nodes
func SetupTestP2PNodes(ctx context.Context) ([]p2p.Client, []*rqstore.SQLiteRQStore, error) {
	var services []p2p.Client
	var rqStores []*rqstore.SQLiteRQStore

	// Setup node addresses and their corresponding Lumera IDs
	nodeConfigs := &lumera.LumeraClientConfig{
		{
			Address:  "127.0.0.1:9000",
			LumeraID: "lumera1xdxm4tytunhhq5hs45nwxds3h73qu4hf9nnjhr",
		},
		{
			Address:  "127.0.0.1:9001",
			LumeraID: "lumera1xdxm4tytunhhq5hs45nwxds3h73qu4hf9nnjhr",
		},
		{
			Address:  "127.0.0.1:9002",
			LumeraID: "lumera1gx4a82h6fq8jhkn08a5k55k5a3e2ygktujtxca",
		},
	}

	// Create and start nodes
	for i, config := range *nodeConfigs {
		mockClient := lumera.NewLumeraClient(*nodeConfigs)

		// Create data directory for the node
		dataDir := fmt.Sprintf("./data/node%d", i)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, nil, fmt.Errorf("failed to create data directory for node %d: %v", i, err)
		}

		// Collect addresses from previous nodes
		bootstrapAddresses := make([]string, i)
		for j := 0; j < i; j++ {
			bootstrapAddresses[j] = (*nodeConfigs)[j].Address
		}

		p2pConfig := &p2p.Config{
			ListenAddress: "127.0.0.1",
			Port:          9000 + i,
			DataDir:       dataDir,
			ID:            config.LumeraID,
			BootstrapIPs:  strings.Join(bootstrapAddresses, ","),
		}

		// Initialize SQLite RQ store for each node
		rqStoreFile := filepath.Join(dataDir, "rqstore.db")
		if err := os.MkdirAll(filepath.Dir(rqStoreFile), 0755); err != nil {
			return nil, nil, fmt.Errorf("failed to create rqstore directory for node %d: %v", i, err)
		}

		rqStore, err := rqstore.NewSQLiteRQStore(rqStoreFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create rqstore for node %d: %v", i, err)
		}
		rqStores = append(rqStores, rqStore)

		service, err := p2p.New(ctx, p2pConfig, mockClient, nil, rqStore, nil, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create p2p service for node %d: %v", i, err)
		}

		// Start P2P service
		go func(nodeID int, rqStore *rqstore.SQLiteRQStore) {
			defer rqStore.Close()
			if err := service.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("Node %d P2P service failed: %v", nodeID, err)
			}
		}(i, rqStore)

		services = append(services, service)

		// Give nodes time to start up and connect
		time.Sleep(2 * time.Second)
	}

	// Give extra time for all nodes to connect
	time.Sleep(3 * time.Second)

	return services, rqStores, nil
}
