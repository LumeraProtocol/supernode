package system

import (
	"context"
	"crypto/sha3"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"

	"github.com/LumeraProtocol/supernode/sdk/action"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/task"

	"github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	sdkconfig "github.com/LumeraProtocol/supernode/sdk/config"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestCascadeE2E performs an end-to-end test of the Cascade functionality in the Lumera network.
// This test covers the entire process from initializing services, setting up accounts,
// creating and processing data through RaptorQ, submitting action requests to the blockchain,
// and monitoring the task execution to completion.
//
// The test demonstrates how data flows through the Lumera system:
// 1. Start services (blockchain, RaptorQ, supernode)
// 2. Set up test accounts and keys
// 3. Create test data and process it through RaptorQ
// 4. Sign the data and RQ identifiers
// 5. Submit a CASCADE action request with proper metadata
// 6. Execute the Cascade operation with the action ID
// 7. Monitor task completion and verify results
func TestCascadeE2E(t *testing.T) {
	// ---------------------------------------
	// Constants and Configuration Parameters
	// ---------------------------------------

	// Test account credentials - these values are consistent across test runs
	const testMnemonic = "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"
	const expectedAddress = "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4"
	const testKeyName = "testkey1"
	const fundAmount = "1000000ulume"

	// Network and service configuration constants
	const (
		raptorQHost     = "localhost"                           // RaptorQ service host
		raptorQPort     = 50051                                 // RaptorQ service port
		raptorQFilesDir = "./supernode-data/raptorq_files_test" // Directory for RaptorQ files
		lumeraGRPCAddr  = "localhost:9090"                      // Lumera blockchain GRPC address
		lumeraChainID   = "testing"                             // Lumera chain ID for testing
	)

	// Action request parameters
	const (
		actionType = "CASCADE" // The action type for fountain code processing
		price      = "10ulume" // Price for the action in ulume tokens
	)
	t.Log("Step 1: Starting all services")

	// Reset and start the blockchain
	sut.ResetChain(t)
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)
	// ---------------------------------------
	// Register Multiple Supernodes to process the request
	// ---------------------------------------
	t.Log("Registering multiple supernodes to process requests")

	// Helper function to register a supernode
	registerSupernode := func(nodeKey string, port string) {
		// Get account and validator addresses for registration
		accountAddr := cli.GetKeyAddr(nodeKey)
		valAddrOutput := cli.Keys("keys", "show", nodeKey, "--bech", "val", "-a")
		valAddr := strings.TrimSpace(valAddrOutput)

		t.Logf("Registering supernode for %s (validator: %s, account: %s)", nodeKey, valAddr, accountAddr)

		// Register the supernode with the network
		registerCmd := []string{
			"tx", "supernode", "register-supernode",
			valAddr,             // validator address
			"localhost:" + port, // IP address with unique port
			"1.0.0",             // version
			accountAddr,         // supernode account
			"--from", nodeKey,
		}

		resp := cli.CustomCommand(registerCmd...)
		RequireTxSuccess(t, resp)

		// Wait for transaction to be included in a block
		sut.AwaitNextBlock(t)
	}

	// Register three supernodes with different ports
	registerSupernode("node0", "4444")
	registerSupernode("node1", "4446")
	registerSupernode("node2", "4448")

	t.Log("Successfully registered three supernodes")

	// ---------------------------------------
	// Step 1: Start all required services
	// ---------------------------------------

	// // Start the RaptorQ service for encoding/decoding with fountain codes
	rq_cmd := StartRQService(t)
	defer StopRQService(rq_cmd) // Ensure service is stopped after test

	// Start the supernode service to process cascade requests
	cmds := StartAllSupernodes(t)
	defer StopAllSupernodes(cmds) // Ensure service is stopped after test

	// ---------------------------------------
	// Step 2: Set up test account and keys
	// ---------------------------------------
	t.Log("Step 2: Setting up test account")

	// Locate and set up path to binary and home directory
	binaryPath := locateExecutable(sut.ExecBinary)
	homePath := filepath.Join(WorkDir, sut.outputDir)

	// Add account key to the blockchain using the mnemonic
	cmd := exec.Command(
		binaryPath,
		"keys", "add", testKeyName,
		"--recover",
		"--keyring-backend=test",
		"--home", homePath,
	)
	cmd.Stdin = strings.NewReader(testMnemonic + "\n")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Key recovery failed: %s", string(output))
	t.Logf("Key recovery output: %s", string(output))

	// Create CLI helper and verify the address matches expected
	recoveredAddress := cli.GetKeyAddr(testKeyName)
	t.Logf("Recovered key %s with address: %s", testKeyName, recoveredAddress)
	require.Equal(t, expectedAddress, recoveredAddress, "Recovered address should match expected address")

	// Fund the account with tokens for transactions
	t.Logf("Funding address %s with %s", recoveredAddress, fundAmount)
	cli.FundAddress(recoveredAddress, fundAmount)      // ulume tokens for action fees
	cli.FundAddress(recoveredAddress, "10000000stake") // stake tokens
	sut.AwaitNextBlock(t)                              // Wait for funding transaction to be processed

	// Create an in-memory keyring for cryptographic operations
	// This keyring is separate from the blockchain keyring and used for local signing
	keplrKeyring, err := keyring.InitKeyring("memory", "")
	require.NoError(t, err, "Failed to initialize in-memory keyring")

	// Add the same key to the in-memory keyring for consistency
	record, err := keyring.RecoverAccountFromMnemonic(keplrKeyring, testKeyName, testMnemonic)
	require.NoError(t, err, "Failed to recover account from mnemonic in local keyring")

	// Verify the addresses match between chain and local keyring
	localAddr, err := record.GetAddress()
	require.NoError(t, err, "Failed to get address from record")
	require.Equal(t, expectedAddress, localAddr.String(),
		"Local keyring address should match expected address")
	t.Logf("Successfully recovered key in local keyring with matching address: %s", localAddr.String())

	// Verify account has sufficient balance for transactions
	balanceOutput := cli.CustomQuery("query", "bank", "balances", recoveredAddress)
	t.Logf("Balance for account: %s", balanceOutput)
	require.Contains(t, balanceOutput, fundAmount[:len(fundAmount)-5],
		"Account should have the funded amount")

	// ---------------------------------------
	// Step 3: Set up RaptorQ service and clients
	// ---------------------------------------
	t.Log("Step 3: Setting up RaptorQ service and initializing clients")

	// Create directory for RaptorQ files if it doesn't exist
	err = os.MkdirAll(raptorQFilesDir, 0755)
	require.NoError(t, err, "Failed to create RaptorQ files directory")

	// Create context with timeout for service operations
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Second)
	defer cancel()

	// Initialize RaptorQ configuration with host, port and file directory
	rqConfig := raptorq.NewConfig()
	rqConfig.Host = raptorQHost
	rqConfig.Port = raptorQPort
	rqConfig.RqFilesDir = raptorQFilesDir

	// Create RaptorQ client and establish connection
	client := raptorq.NewClient()
	address := fmt.Sprintf("%s:%d", raptorQHost, raptorQPort)
	t.Logf("Connecting to RaptorQ server at %s", address)
	connection, err := client.Connect(ctx, address)
	require.NoError(t, err, "Failed to connect to RaptorQ server")
	defer connection.Close()

	// Initialize Lumera blockchain client for interactions
	t.Log("Initializing Lumera client")
	lumeraClient, err := lumera.NewClient(
		ctx,
		lumera.WithGRPCAddr(lumeraGRPCAddr),
		lumera.WithChainID(lumeraChainID),
	)
	require.NoError(t, err, "Failed to initialize Lumera client")

	// ---------------------------------------
	// Step 4: Create and prepare test file
	// ---------------------------------------
	t.Log("Step 4: Creating test file for RaptorQ encoding")

	// Create a test file with sample data in a temporary directory
	testFileName := filepath.Join(t.TempDir(), "testfile.data")
	testData := []byte("This is test data for RaptorQ encoding in the Lumera network")
	err = os.WriteFile(testFileName, testData, 0644)
	require.NoError(t, err, "Failed to write test file")

	// Read the file into memory for processing
	file, err := os.Open(testFileName)
	require.NoError(t, err, "Failed to open test file")
	defer file.Close()

	// Read the entire file content into a byte slice
	fileInfo, err := file.Stat()
	require.NoError(t, err, "Failed to get file stats")
	data := make([]byte, fileInfo.Size())
	_, err = io.ReadFull(file, data)
	require.NoError(t, err, "Failed to read file contents")
	t.Logf("Read %d bytes from test file", len(data))

	// Calculate SHA3-256 hash of the file data for identification
	// This hash is used in metadata and for CASCADE operation
	hash := sha3.New256()
	hash.Write(data)
	hashBytes := hash.Sum(nil)
	hashHex := fmt.Sprintf("%X", hashBytes)
	t.Logf("File hash: %s", hashHex)
	time.Sleep(1 * time.Minute)
	// ---------------------------------------
	// Step 5: Sign data and generate RaptorQ identifiers
	// ---------------------------------------
	t.Log("Step 5: Signing data and generating RaptorQ identifiers")

	// Sign the original file data for verification purposes
	// This signature proves the data came from this account
	signedData, err := keyring.SignBytes(keplrKeyring, testKeyName, data)
	base64EncodedData := base64.StdEncoding.EncodeToString(signedData)
	require.NoError(t, err, "Failed to sign data")
	t.Logf("Signed data length: %d bytes", len(signedData))

	// Initialize RaptorQ client for fountain code processing
	rq := connection.RaptorQ(rqConfig, lumeraClient)

	// Get current block hash or use file hash as fallback
	// Block hash adds randomness to the fountain code generation
	// blockHash := hashHex // Default to file hash
	// latestBlock, err := lumeraClient.Node().GetLatestBlock(ctx)
	// if err == nil && latestBlock != nil && len(latestBlock.BlockId.Hash) > 0 {
	// 	blockHash = fmt.Sprintf("%X", latestBlock.BlockId.Hash)
	// }
	// t.Logf("Using block hash: %s", blockHash)

	t.Log("Generating RQ identifiers")
	genRqIdsResp, err := rq.GenRQIdentifiersFiles(ctx, raptorq.GenRQIdentifiersFilesRequest{

		RqMax:            50,
		Data:             data,
		CreatorSNAddress: localAddr.String(),
		SignedData:       base64EncodedData,
	})
	require.NoError(t, err, "Failed to generate RQ identifiers")

	t.Logf("RQ identifiers generated successfully with RQ_IDs_IC: %d", genRqIdsResp.RQIDsIc)

	// ---------------------------------------
	// Step 6: Sign the RQ IDs file for verification
	// ---------------------------------------
	t.Log("Step 6: Signing the RQ IDs file for the action request")

	// Base64 encode the RQIDsFile for consistent transmission
	// IMPORTANT: We sign the encoded version, not the raw bytes
	rqIdsFileBase64 := base64.StdEncoding.EncodeToString(genRqIdsResp.RQIDsFile)
	t.Logf("Base64 encoded RQ IDs file length: %d", len(rqIdsFileBase64))

	// Sign the Base64-encoded RQ IDs file
	// This critical step creates a signature that proves ownership
	rqIdsSignature, err := keyring.SignBytes(keplrKeyring, testKeyName, []byte(rqIdsFileBase64))
	require.NoError(t, err, "Failed to sign Base64-encoded RQIDsFile")

	// Encode the signature itself to base64 for consistent format
	rqIdsSignatureBase64 := base64.StdEncoding.EncodeToString(rqIdsSignature)

	// Format according to expected verification pattern: Base64(rq_ids).signature
	// This format allows verification of both the data and the signature
	signatureFormat := fmt.Sprintf("%s.%s", rqIdsFileBase64, rqIdsSignatureBase64)
	t.Logf("Signature format prepared with length: %d bytes", len(signatureFormat))

	// ---------------------------------------
	// Step 7: Create metadata and submit action request
	// ---------------------------------------
	t.Log("Step 7: Creating metadata and submitting action request")

	// Create CascadeMetadata struct with all required fields
	// This structured approach ensures all required fields are included
	cascadeMetadata := types.CascadeMetadata{
		DataHash:   hashHex,                      // Hash of the original file
		FileName:   filepath.Base(testFileName),  // Original filename
		RqIdsIc:    uint64(genRqIdsResp.RQIDsIc), // Count of RQ identifiers
		Signatures: signatureFormat,              // Combined signature format
	}

	// Marshal the struct to JSON for the blockchain transaction
	metadataBytes, err := json.Marshal(cascadeMetadata)
	require.NoError(t, err, "Failed to marshal CascadeMetadata to JSON")
	metadata := string(metadataBytes)

	// Set expiration time 25 hours in the future (minimum is 24 hours)
	// This defines how long the action request is valid
	expirationTime := fmt.Sprintf("%d", time.Now().Add(25*time.Hour).Unix())

	t.Logf("Requesting cascade action with metadata: %s", metadata)
	t.Logf("Action type: %s, Price: %s, Expiration: %s", actionType, price, expirationTime)

	// Submit the action request transaction to the blockchain
	// This registers the request with metadata for supernodes to process
	actionRequestResp := cli.CustomCommand(
		"tx", "action", "request-action",
		actionType,     // CASCADE action type
		metadata,       // JSON metadata with all required fields
		price,          // Price in ulume tokens
		expirationTime, // Unix timestamp for expiration
		"--from", testKeyName,
		"--gas", "auto",
		"--gas-adjustment", "1.5",
	)

	// Verify the transaction was successful
	RequireTxSuccess(t, actionRequestResp)
	t.Logf("Action request successful: %s", actionRequestResp)

	// Wait for transaction to be included in a block
	sut.AwaitNextBlock(t)

	// Extract transaction hash from response for verification
	txHash := gjson.Get(actionRequestResp, "txhash").String()
	require.NotEmpty(t, txHash, "Transaction hash should not be empty")
	t.Logf("Transaction hash: %s", txHash)

	// Query the transaction by hash to verify success and extract events
	txResp := cli.CustomQuery("q", "tx", txHash)
	t.Logf("Transaction query response: %s", txResp)

	// Verify transaction code indicates success (0 = success)
	txCode := gjson.Get(txResp, "code").Int()
	require.Equal(t, int64(0), txCode, "Transaction should have success code 0")

	// ---------------------------------------
	// Step 8: Extract action ID and start cascade
	// ---------------------------------------
	t.Log("Step 8: Extracting action ID and creating cascade request")

	// Extract action ID from transaction events
	// The action_id is needed to reference this specific action in operations
	events := gjson.Get(txResp, "events").Array()
	var actionID string
	for _, event := range events {
		if event.Get("type").String() == "action_registered" {
			attrs := event.Get("attributes").Array()
			for _, attr := range attrs {
				if attr.Get("key").String() == "action_id" {
					actionID = attr.Get("value").String()
					break
				}
			}
			if actionID != "" {
				break
			}
		}
	}
	require.NotEmpty(t, actionID, "Action ID should not be empty")
	t.Logf("Extracted action ID: %s", actionID)

	// Set up action client configuration
	// This defines how to connect to network services
	actionConfig := sdkconfig.Config{
		Network: struct {
			DefaultSupernodePort int
			LocalCosmosAddress   string
		}{
			DefaultSupernodePort: 4444,             // Default supernode gRPC port
			LocalCosmosAddress:   recoveredAddress, // Account address
		},
		Lumera: struct {
			GRPCAddr string
			ChainID  string
			Timeout  int
		}{
			GRPCAddr: lumeraGRPCAddr,
			ChainID:  lumeraChainID,
			Timeout:  300, // 30 seconds timeout
		},
	}

	// Initialize action client for cascade operations
	actionClient, err := action.NewClient(
		ctx,
		actionConfig,
		nil,          // Nil logger - use default
		keplrKeyring, // Use the in-memory keyring for signing
	)
	require.NoError(t, err, "Failed to create action client")

	// Start cascade operation with all required parameters
	// INPUTS:
	// - hashHex: File hash for identification
	// - actionID: The blockchain action ID
	// - testFileName: Path to the original file
	// - signedData: Proof of data ownership
	t.Logf("Starting cascade operation with action ID: %s", actionID)
	taskID, err := actionClient.StartCascade(
		ctx,
		hashHex,           // File hash
		actionID,          // Action ID from the events
		testFileName,      // Path to the test file
		base64EncodedData, // Signed data
	)
	require.NoError(t, err, "Failed to start cascade operation")
	require.NotEmpty(t, taskID, "Task ID should not be empty")
	t.Logf("Cascade operation started with task ID: %s", taskID)

	// ---------------------------------------
	// Step 9: Monitor task completion
	// ---------------------------------------

	// Set up event channels for task monitoring
	completionCh := make(chan bool)
	errorCh := make(chan error)

	// Subscribe to task completion events
	actionClient.SubscribeToEvents(ctx, event.TaskCompleted, func(ctx context.Context, e event.Event) {
		if e.TaskID == taskID {
			t.Logf("Task completed: %s", taskID)
			completionCh <- true
		}
	})

	// Subscribe to task failure events
	actionClient.SubscribeToEvents(ctx, event.TaskFailed, func(ctx context.Context, e event.Event) {
		if e.TaskID == taskID {
			errorMsg, _ := e.Data["error"].(string)
			errorCh <- fmt.Errorf("task failed: %s", errorMsg)
		}
	})

	// Wait for task completion, failure, or timeout
	t.Log("Waiting for cascade task to complete...")
	select {
	case <-completionCh:
		t.Log("Cascade task completed successfully")
	case err := <-errorCh:
		t.Fatalf("Cascade task failed: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatalf("Timeout waiting for cascade task to complete")
	}

	// ---------------------------------------
	// Step 10: Verify task completion and results
	// ---------------------------------------

	// Get the task details to verify status
	taskEntry, found := actionClient.GetTask(ctx, taskID)
	require.True(t, found, "Task should be found")
	require.Equal(t, taskEntry.Status, task.StatusCompleted, "Task should be completed")
	t.Logf("Task status: %s", taskEntry.Status)

	// Additional verification based on the events in the task
	eventCount := len(taskEntry.Events)
	t.Logf("Task recorded %d events", eventCount)
	require.Greater(t, eventCount, 0, "Task should have recorded events")

	// Check if we can find a successful supernode in the events
	// This confirms the cascade operation was processed correctly
	var successfulSupernode string
	for _, e := range taskEntry.Events {
		if e.Type == event.SupernodeSucceeded {
			if addr, ok := e.Data["supernode_address"].(string); ok {
				successfulSupernode = addr
				break
			}
		}
	}
	require.NotEmpty(t, successfulSupernode, "Should have a successful supernode in events")
	t.Logf("Cascade successfully processed by supernode: %s", successfulSupernode)
}
