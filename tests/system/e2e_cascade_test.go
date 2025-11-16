package system

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"

	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"

	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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
	os.Setenv("INTEGRATION_TEST", "true")
	defer os.Unsetenv("INTEGRATION_TEST")
	// Test account credentials - these values are consistent across test runs
	const testMnemonic = "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"
	const expectedAddress = "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4"
	const testKeyName = "testkey1"
	const userKeyName = "user"
	const userMnemonic = "little tone alley oval festival gloom sting asthma crime select swap auto when trip luxury pact risk sister pencil about crisp upon opera timber"
	const fundAmount = "1000000ulume"

	// Network and service configuration constants
	const (
		raptorQHost     = "localhost"                            // RaptorQ service host
		raptorQPort     = 50051                                  // RaptorQ service port
		raptorQFilesDir = "./supernode-data1/raptorq_files_test" // Directory for RaptorQ files
		lumeraGRPCAddr  = "localhost:9090"                       // Lumera blockchain GRPC address
		lumeraChainID   = "testing"                              // Lumera chain ID for testing
	)

	// Action request parameters
	const actionType = "CASCADE" // The action type for fountain code processing
	t.Log("Step 1: Starting all services")

	// Update the genesis file with required params before starting
	// - Set staking bond denom to match ulume used by gentxs
	// - Configure action module params used by the test
	sut.ModifyGenesisJSON(t, SetStakingBondDenomUlume(t), SetActionParams(t))

	// Reset and start the blockchain
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)
	// ---------------------------------------
	// Register Multiple Supernodes to process the request
	// ---------------------------------------
	t.Log("Registering multiple supernodes to process requests")

	// Helper function to register a supernode
	registerSupernode := func(nodeKey string, port string, addr string, p2pPort string) {
		// Get account and validator addresses for registration
		accountAddr := cli.GetKeyAddr(nodeKey)
		valAddrOutput := cli.Keys("keys", "show", nodeKey, "--bech", "val", "-a")
		valAddr := strings.TrimSpace(valAddrOutput)

		t.Logf("Registering supernode for %s (validator: %s, account: %s)", nodeKey, valAddr, accountAddr)

		// Register the supernode with the network
		registerCmd := []string{
			"tx", "supernode", "register-supernode",
			valAddr,
			"localhost:" + port,
			addr,
			"--p2p-port", p2pPort,
			"--from", nodeKey,
		}

		resp := cli.CustomCommand(registerCmd...)
		RequireTxSuccess(t, resp)

		// Wait for transaction to be included in a block
		sut.AwaitNextBlock(t)
	}

	// Register three supernodes with different ports
	registerSupernode("node0", "4444", "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4", "4445")
	registerSupernode("node1", "4446", "lumera1cf0ms9ttgdvz6zwlqfty4tjcawhuaq69p40w0c", "4447")
	registerSupernode("node2", "4448", "lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp", "4449")
	t.Log("Successfully registered three supernodes")

	// Fund Lume
	cli.FundAddress("lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4", "100000ulume")
	cli.FundAddressWithNode("lumera1cf0ms9ttgdvz6zwlqfty4tjcawhuaq69p40w0c", "100000ulume", "node1")
	cli.FundAddressWithNode("lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp", "100000ulume", "node2")

	queryHeight := sut.AwaitNextBlock(t)
	args := []string{
		"query",
		"supernode",
		"get-top-supernodes-for-block",
		fmt.Sprint(queryHeight),
		"--output", "json",
	}

	// Get initial response to compare against
	resp := cli.CustomQuery(args...)

	t.Logf("Initial response: %s", resp)

	// ---------------------------------------
	// Step 1: Start all required services
	// ---------------------------------------

	// Start the supernode service to process cascade requests
	cmds := StartAllSupernodes(t)
	defer StopAllSupernodes(cmds)
	// Ensure service is stopped after test

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

	// Add user key to the blockchain using the provided mnemonic
	cmd = exec.Command(
		binaryPath,
		"keys", "add", userKeyName,
		"--recover",
		"--keyring-backend=test",
		"--home", homePath,
	)
	cmd.Stdin = strings.NewReader(userMnemonic + "\n")
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "User key recovery failed: %s", string(output))
	t.Logf("User key recovery output: %s", string(output))

	// Get the user address
	userAddress := cli.GetKeyAddr(userKeyName)
	t.Logf("Recovered user key with address: %s", userAddress)

	// Fund the account with tokens for transactions
	t.Logf("Funding test address %s with %s", recoveredAddress, fundAmount)
	cli.FundAddress(recoveredAddress, fundAmount) // ulume tokens for action fees

	// Fund user account
	t.Logf("Funding user address %s with %s", userAddress, fundAmount)
	cli.FundAddress(userAddress, fundAmount) // ulume tokens for action fees

	sut.AwaitNextBlock(t) // Wait for funding transaction to be processed

	// Create an in-memory keyring for cryptographic operations
	// This keyring is separate from the blockchain keyring and used for local signing
	keplrKeyring, err := keyring.InitKeyring(config.KeyringConfig{
		Backend: "memory",
		Dir:     "",
	})
	require.NoError(t, err, "Failed to initialize in-memory keyring")

	// Add the test key to the in-memory keyring
	record, err := keyring.RecoverAccountFromMnemonic(keplrKeyring, testKeyName, testMnemonic)
	require.NoError(t, err, "Failed to recover test account from mnemonic in local keyring")

	// Also add the user key to the in-memory keyring
	userRecord, err := keyring.RecoverAccountFromMnemonic(keplrKeyring, userKeyName, userMnemonic)
	require.NoError(t, err, "Failed to recover user account from mnemonic in local keyring")

	// Verify the addresses match between chain and local keyring
	localAddr, err := record.GetAddress()
	require.NoError(t, err, "Failed to get address from record")
	require.Equal(t, expectedAddress, localAddr.String(),
		"Local keyring address should match expected address")
	t.Logf("Successfully recovered test key in local keyring with matching address: %s", localAddr.String())

	userLocalAddr, err := userRecord.GetAddress()
	require.NoError(t, err, "Failed to get user address from record")
	require.Equal(t, userAddress, userLocalAddr.String(),
		"User local keyring address should match user address")
	t.Logf("Successfully recovered user key in local keyring with matching address: %s", userLocalAddr.String())

	// Initialize Lumera blockchain client for interactions

	ctx := context.Background()

	lumeraCfg, err := lumera.NewConfig(
		lumeraGRPCAddr,
		lumeraChainID,
		userKeyName,
		keplrKeyring,
	)
	require.NoError(t, err, "Failed to create Lumera client configuration")

	lumeraClinet, err := lumera.NewClient(context.Background(), lumeraCfg)
	require.NoError(t, err, "Failed to initialize Lumera client")

	// ---------------------------------------
	// Step 4: Create and prepare layout file for RaptorQ encoding
	// ---------------------------------------
	t.Log("Step 4: Creating test file for RaptorQ encoding")

	// Use test file from tests/system directory
	testFileName := "test.txt"
	testFileFullpath := filepath.Join(testFileName)

	// Verify test file exists
	_, err = os.Stat(testFileFullpath)
	require.NoError(t, err, "Test file not found: %s", testFileFullpath)

	// Read the file into memory for processing
	file, err := os.Open(testFileFullpath)
	require.NoError(t, err, "Failed to open test file")
	defer file.Close()

	// Read the entire file content into a byte slice
	fileInfo, err := file.Stat()
	require.NoError(t, err, "Failed to get file stats")
	data := make([]byte, fileInfo.Size())
	_, err = io.ReadFull(file, data)
	require.NoError(t, err, "Failed to read file contents")
	t.Logf("Read %d bytes from test file", len(data))

	// Calculate SHA256 hash of original file for later comparison
	originalHash := sha256.Sum256(data)
	t.Logf("Original file SHA256 hash: %x", originalHash)

	// Cascade signature creation process (high-level via action SDK)

	// Build action client for metadata generation and cascade operations
	// Use the same account that submits RequestAction so signatures match the on-chain creator
	accConfig := sdkconfig.AccountConfig{LocalCosmosAddress: userAddress, KeyName: userKeyName, Keyring: keplrKeyring}
	lumraConfig := sdkconfig.LumeraConfig{GRPCAddr: lumeraGRPCAddr, ChainID: lumeraChainID}
	actionConfig := sdkconfig.Config{Account: accConfig, Lumera: lumraConfig}
	actionClient, err := action.NewClient(context.Background(), actionConfig, nil)
	require.NoError(t, err, "Failed to create action client")

	// Use the new SDK helper to build Cascade metadata (includes signatures, price, and expiration)
	builtMeta, autoPrice, expirationTime, err := actionClient.BuildCascadeMetadataFromFile(ctx, testFileFullpath, false)
	require.NoError(t, err, "Failed to build cascade metadata from file")

	// Create a signature for StartCascade using the SDK helper
	signedHashBase64, err := actionClient.GenerateStartCascadeSignatureFromFile(ctx, testFileFullpath)
	require.NoError(t, err, "Failed to generate StartCascade signature")

	// ---------------------------------------
	t.Log("Step 7: Creating metadata and submitting action request")

	// Marshal the helper-built metadata to JSON for the blockchain transaction
	metadataBytes, err := json.Marshal(builtMeta)
	require.NoError(t, err, "Failed to marshal CascadeMetadata to JSON")
	metadata := string(metadataBytes)

	t.Logf("Requesting cascade action with metadata: %s", metadata)
	t.Logf("Action type: %s, Price: %s, Expiration: %s", actionType, autoPrice, expirationTime)

	response, err := lumeraClinet.ActionMsg().RequestAction(ctx, actionType, metadata, autoPrice, expirationTime)
	require.NoError(t, err, "RequestAction failed")

	require.NotNil(t, resp, "RequestAction returned nil response")

	txresp := response.TxResponse

	// Verify the transaction was successful
	require.Zero(t, txresp.Code, "Transaction should have success code 0")

	// Wait for transaction to be included in a block
	sut.AwaitNextBlock(t)
	time.Sleep(5 * time.Second) // Allow some time for the transaction to be processed
	// Verify the account can be queried with its public key
	//accountResp := cli.CustomQuery("q", "auth", "account", userAddress)
	//require.Contains(t, accountResp, "public_key", "User account public key should be available")

	// Extract transaction hash from response for verification
	txHash := txresp.TxHash
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
	time.Sleep(40 * time.Second)

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

	// ---------------------------------------
	// Step 9: Subscribe to all events and extract tx hash
	// ---------------------------------------

	// Channels to receive async signals
	txHashCh := make(chan string, 1)
	completionCh := make(chan bool, 1)
	errCh := make(chan string, 1)

	// Subscribe to ALL events (non-blocking sends to avoid handler stalls)
	err = actionClient.SubscribeToAllEvents(context.Background(), func(ctx context.Context, e event.Event) {
		// Log every event for debugging and capture key ones
		t.Logf("SDK event: type=%s data=%v", e.Type, e.Data)
		// Only capture TxhasReceived events
		if e.Type == event.SDKTaskTxHashReceived {
			if txHash, ok := e.Data[event.KeyTxHash].(string); ok && txHash != "" {
				// Non-blocking send; drop if buffer full
				select {
				case txHashCh <- txHash:
				default:
				}
			}
		}

		// Also monitor for task completion
		if e.Type == event.SDKTaskCompleted {
			// Non-blocking send; drop if buffer full
			select {
			case completionCh <- true:
			default:
			}
		}
		// Capture task failures and propagate error message to main goroutine
		if e.Type == event.SDKTaskFailed {
			if msg, ok := e.Data[event.KeyError].(string); ok && msg != "" {
				select {
				case errCh <- msg:
				default:
				}
			} else {
				select {
				case errCh <- "task failed (no error message)":
				default:
				}
			}
		}
	})
	require.NoError(t, err, "Failed to subscribe to events")

	// Start cascade operation

	//
	t.Logf("Starting cascade operation with action ID: %s", actionID)
	taskID, err := actionClient.StartCascade(
		ctx,
		testFileFullpath, // path
		actionID,         // Action ID from the transaction
		signedHashBase64, // Signed hash of the file
	)
	require.NoError(t, err, "Failed to start cascade operation")
	t.Logf("Cascade operation started with task ID: %s", taskID)

	// Wait for both tx-hash and completion with a timeout
	var recievedhash string
	done := false
	timeout := time.After(2 * time.Minute)
waitLoop:
	for {
		if recievedhash != "" && done {
			break waitLoop
		}
		select {
		case h := <-txHashCh:
			if recievedhash == "" {
				recievedhash = h
			}
		case <-completionCh:
			done = true
		case emsg := <-errCh:
			t.Fatalf("cascade task reported failure: %s", emsg)
		case <-timeout:
			t.Fatalf("timeout waiting for events; recievedhash=%q done=%v", recievedhash, done)
		}
	}

	t.Logf("Received transaction hash: %s", recievedhash)

	time.Sleep(10 * time.Second)
	txReponse := cli.CustomQuery("q", "tx", recievedhash)

	t.Logf("Transaction response: %s", txReponse)

	// ---------------------------------------
	// Step 10: Validate Transaction Events
	// ---------------------------------------
	t.Log("Step 9: Validating transaction events and payments")

	// Check for action_finalized event
	events = gjson.Get(txReponse, "events").Array()
	var actionFinalized bool
	var feeSpent bool
	var feeReceived bool
	var fromAddress string
	var toAddress string
	var amount string

	for _, event := range events {
		// Check for action finalized event
		if event.Get("type").String() == "action_finalized" {
			actionFinalized = true
			attrs := event.Get("attributes").Array()
			for _, attr := range attrs {
				if attr.Get("key").String() == "action_type" {
					require.Equal(t, "ACTION_TYPE_CASCADE", attr.Get("value").String(), "Action type should be CASCADE")
				}
				if attr.Get("key").String() == "action_id" {
					require.Equal(t, actionID, attr.Get("value").String(), "Action ID should match")
				}
			}
		}

		// Check for fee spent event
		if event.Get("type").String() == "coin_spent" {
			attrs := event.Get("attributes").Array()
			for i, attr := range attrs {
				if attr.Get("key").String() == "amount" && attr.Get("value").String() == autoPrice {
					feeSpent = true
					// Get the spender address from the same event group
					for j, addrAttr := range attrs {
						if j < i && addrAttr.Get("key").String() == "spender" {
							fromAddress = addrAttr.Get("value").String()
							break
						}
					}
				}
			}
		}

		// Check for fee received event
		if event.Get("type").String() == "coin_received" {
			attrs := event.Get("attributes").Array()
			for i, attr := range attrs {
				if attr.Get("key").String() == "amount" && attr.Get("value").String() == autoPrice {
					feeReceived = true
					// Get the receiver address from the same event group
					for j, addrAttr := range attrs {
						if j < i && addrAttr.Get("key").String() == "receiver" {
							toAddress = addrAttr.Get("value").String()
							break
						}
					}
					amount = attr.Get("value").String()
				}
			}
		}
	}

	// Validate events
	require.True(t, actionFinalized, "Action finalized event should be emitted")
	require.True(t, feeSpent, "Fee spent event should be emitted")
	require.True(t, feeReceived, "Fee received event should be emitted")

	// Validate payment flow
	t.Logf("Payment flow: %s paid %s to %s", fromAddress, amount, toAddress)
	require.NotEmpty(t, fromAddress, "Spender address should not be empty")
	require.NotEmpty(t, toAddress, "Receiver address should not be empty")
	require.Equal(t, autoPrice, amount, "Payment amount should match action price")

	time.Sleep(10 * time.Second)

	outputFileBaseDir := filepath.Join(".")
	// Create download signature for actionID (using the same address that was used for StartCascade)
	signature, err := actionClient.GenerateDownloadSignature(context.Background(), actionID, userAddress)
	// Try to download the file using the action ID and signature
	dtaskID, err := actionClient.DownloadCascade(context.Background(), actionID, outputFileBaseDir, signature)

	t.Logf("Download response: %s", dtaskID)
	require.NoError(t, err, "Failed to download cascade data using action ID")

	time.Sleep(10 * time.Second)

	// ---------------------------------------
	// Step 11: Validate downloaded files exist
	// ---------------------------------------
	t.Log("Step 11: Validating downloaded files exist in expected directory structure")

	// Construct expected directory path: baseDir/{actionID}/
	expectedDownloadDir := filepath.Join(outputFileBaseDir, actionID)
	t.Logf("Checking for files in directory: %s", expectedDownloadDir)

	// Check if the action directory exists
	if _, err := os.Stat(expectedDownloadDir); os.IsNotExist(err) {
		t.Fatalf("Expected download directory does not exist: %s", expectedDownloadDir)
	}

	// Read directory contents
	files, err := os.ReadDir(expectedDownloadDir)
	require.NoError(t, err, "Failed to read download directory: %s", expectedDownloadDir)

	// Filter out directories, only count actual files
	fileCount := 0
	var fileNames []string
	for _, file := range files {
		if !file.IsDir() {
			fileCount++
			fileNames = append(fileNames, file.Name())
		}
	}

	t.Logf("Found %d files in download directory: %v", fileCount, fileNames)

	// Validate that at least one file was downloaded
	require.True(t, fileCount >= 1, "Expected at least 1 file in download directory %s, found %d files", expectedDownloadDir, fileCount)

	// ---------------------------------------
	// Step 12: Validate downloaded file content matches original
	// ---------------------------------------
	t.Log("Step 12: Validating downloaded file content matches original file")

	// Find and verify the downloaded file that matches our original test file
	var downloadedFilePath string
	for _, fileName := range fileNames {
		filePath := filepath.Join(expectedDownloadDir, fileName)
		// Check if this is our test file by comparing base name
		if filepath.Base(fileName) == filepath.Base(testFileFullpath) {
			downloadedFilePath = filePath
			break
		}
	}

	if downloadedFilePath == "" {
		// If exact name match not found, use the first file (common in single-file downloads)
		if len(fileNames) > 0 {
			downloadedFilePath = filepath.Join(expectedDownloadDir, fileNames[0])
			t.Logf("Using first downloaded file for verification: %s", fileNames[0])
		} else {
			t.Fatalf("No files found in download directory for content verification")
		}
	}

	// Read the downloaded file
	downloadedFile, err := os.Open(downloadedFilePath)
	require.NoError(t, err, "Failed to open downloaded file: %s", downloadedFilePath)
	defer downloadedFile.Close()

	// Read downloaded file content
	downloadedData, err := io.ReadAll(downloadedFile)
	require.NoError(t, err, "Failed to read downloaded file content")

	// Calculate SHA256 hash of downloaded file
	downloadedHash := sha256.Sum256(downloadedData)
	t.Logf("Downloaded file SHA256 hash: %x", downloadedHash)

	// Compare file sizes
	require.Equal(t, len(data), len(downloadedData), "Downloaded file size should match original file size")

	// Compare file hashes
	require.Equal(t, originalHash, downloadedHash, "Downloaded file hash should match original file hash")

	t.Logf("File verification successful: downloaded file content matches original file")

	status, err := actionClient.GetSupernodeStatus(ctx, "lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp")
	t.Logf("Supernode status: %+v", status)
	require.NoError(t, err, "Failed to get supernode status")

}

// SetActionParams sets the initial parameters for the action module in genesis
func SetActionParams(t *testing.T) GenesisMutator {
	return func(genesis []byte) []byte {
		t.Helper()
		state, err := sjson.SetRawBytes(genesis, "app_state.action.params", []byte(`{
            "base_action_fee": {
                "amount": "10000",
                "denom": "ulume"
            },
            "expiration_duration": "24h0m0s",
            "fee_per_kbyte": {
                "amount": "0",
                "denom": "ulume"
            },
            "foundation_fee_share": "0.000000000000000000",
            "max_actions_per_block": "10",
            "max_dd_and_fingerprints": "50",
            "max_processing_time": "1h0m0s",
            "max_raptor_q_symbols": "50",
            "min_processing_time": "1m0s",
            "min_super_nodes": "1",
            "super_node_fee_share": "1.000000000000000000"
        }`))
		require.NoError(t, err)
		return state
	}
}

// SetStakingBondDenomUlume sets the staking module bond denom to "ulume" in genesis
func SetStakingBondDenomUlume(t *testing.T) GenesisMutator {
	return func(genesis []byte) []byte {
		t.Helper()
		state, err := sjson.SetBytes(genesis, "app_state.staking.params.bond_denom", "ulume")
		require.NoError(t, err)
		return state
	}
}
