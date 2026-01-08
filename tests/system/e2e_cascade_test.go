package system

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"

	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"

	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	icatypes "github.com/cosmos/ibc-go/v10/modules/apps/27-interchain-accounts/types"
	chantypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	icaAckRetries        = 120
	icaAckDelay          = 3 * time.Second
	icaPacketInfoRetries = 20
	icaPacketInfoDelay   = 3 * time.Second
	actionStateRetries   = 40
	actionStateDelay     = 3 * time.Second
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
	// - Relax supernode metrics params so nodes don't get POSTPONED immediately in tests
	sut.ModifyGenesisJSON(t, SetStakingBondDenomUlume(t), SetActionParams(t), SetSupernodeMetricsParams(t))

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
	accConfig := sdkconfig.AccountConfig{KeyName: userKeyName, Keyring: keplrKeyring}
	lumraConfig := sdkconfig.LumeraConfig{GRPCAddr: lumeraGRPCAddr, ChainID: lumeraChainID}
	actionConfig := sdkconfig.Config{Account: accConfig, Lumera: lumraConfig}
	actionClient, err := action.NewClient(context.Background(), actionConfig, nil)
	require.NoError(t, err, "Failed to create action client")

	// Use the new SDK helper to build Cascade metadata (includes signatures, price, and expiration)
	builtMeta, autoPrice, expirationTime, err := actionClient.BuildCascadeMetadataFromFile(ctx, testFileFullpath, false, "")
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

	fi, err := os.Stat(testFileFullpath)
	require.NoError(t, err, "Failed to stat test file")
	fileSizeKbs := int64(0)
	if fi != nil && fi.Size() > 0 {
		fileSizeKbs = (fi.Size() + 1023) / 1024
	}

	response, err := lumeraClinet.ActionMsg().RequestAction(ctx, actionType, metadata, autoPrice, expirationTime, strconv.FormatInt(fileSizeKbs, 10))
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

	// ---------------------------------------
	// Step 13: ICA request + upload/download flow (optional)
	// ---------------------------------------
	t.Log("Step 13: ICA request + upload/download flow")
	icaCfg, ok := loadICAControllerConfig()
	if !ok {
		t.Skip("ICA controller env not configured; set ICA_CONTROLLER_* and ICA_CONNECTION_ID to run")
	}

	icaCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	ownerAddr, err := icaControllerKeyAddress(icaCtx, icaCfg)
	require.NoError(t, err, "resolve ICA controller owner address")

	icaAddr, err := queryICAAddress(icaCtx, icaCfg, ownerAddr)
	require.NoError(t, err, "resolve ICA address")
	require.NotEmpty(t, icaAddr, "ICA address is empty")

	icaMeta, icaPrice, icaExpiration, err := actionClient.BuildCascadeMetadataFromFile(icaCtx, testFileFullpath, false, icaAddr)
	require.NoError(t, err, "build ICA cascade metadata")
	icaMetaBytes, err := json.Marshal(icaMeta)
	require.NoError(t, err, "marshal ICA metadata")

	icaMsg := &actiontypes.MsgRequestAction{
		Creator:        icaAddr,
		ActionType:     actionType,
		Metadata:       string(icaMetaBytes),
		Price:          icaPrice,
		ExpirationTime: icaExpiration,
		FileSizeKbs:    strconv.FormatInt(fileSizeKbs, 10),
	}

	_, err = sendICARequestAction(icaCtx, icaCfg, icaMsg)
	require.Error(t, err, "missing app_pubkey should be rejected")
	if err != nil {
		require.Contains(t, err.Error(), "app_pubkey")
	}

	userPubKey, err := userRecord.GetPubKey()
	require.NoError(t, err, "get user pubkey for ICA request")
	icaMsg.AppPubkey = userPubKey.Bytes()
	actionIDs, err := sendICARequestAction(icaCtx, icaCfg, icaMsg)
	require.NoError(t, err, "ICA request action failed")
	require.NotEmpty(t, actionIDs, "ICA request action returned no IDs")
	icaActionID := actionIDs[0]
	require.NotEmpty(t, icaActionID, "ICA action ID is empty")

	require.NoError(t, waitForActionStateWithClient(icaCtx, lumeraClinet, icaActionID, actiontypes.ActionStatePending))

	actionClientImpl, ok := actionClient.(*action.ClientImpl)
	require.True(t, ok, "action client must be ClientImpl for ICA signing")

	icaStartSig, err := actionClientImpl.GenerateStartCascadeSignatureFromFileWithSigner(icaCtx, testFileFullpath, icaAddr)
	require.NoError(t, err, "generate ICA start signature")

	icaTaskID, err := actionClient.StartCascade(icaCtx, testFileFullpath, icaActionID, icaStartSig)
	require.NoError(t, err, "start ICA cascade")
	t.Logf("ICA cascade task started: %s", icaTaskID)

	require.NoError(t, waitForActionStateWithClient(icaCtx, lumeraClinet, icaActionID, actiontypes.ActionStateDone))

	icaDownloadSig, err := actionClient.GenerateDownloadSignature(icaCtx, icaActionID, icaAddr)
	require.NoError(t, err, "generate ICA download signature")

	icaDownloadDir := t.TempDir()
	icaDownloadTaskID, err := actionClient.DownloadCascade(icaCtx, icaActionID, icaDownloadDir, icaDownloadSig)
	require.NoError(t, err, "download ICA cascade")
	t.Logf("ICA download task started: %s", icaDownloadTaskID)

	verifyDownloadedFile(t, icaDownloadDir, icaActionID, testFileFullpath, data, originalHash)

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

// SetSupernodeMetricsParams configures supernode metrics-related params for faster testing.
func SetSupernodeMetricsParams(t *testing.T) GenesisMutator {
	return func(genesis []byte) []byte {
		t.Helper()

		state, err := sjson.SetRawBytes(genesis, "app_state.supernode.params.minimum_stake_for_sn", []byte(`{"denom":"ulume","amount":"100000000"}`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.reporting_threshold", []byte(`"1"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.slashing_threshold", []byte(`"1"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.metrics_thresholds", []byte(`""`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.evidence_retention_period", []byte(`"180days"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.slashing_fraction", []byte(`"0.010000000000000000"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.inactivity_penalty_period", []byte(`"86400s"`))
		require.NoError(t, err)

		// Allow plenty of time for supernodes to start and report metrics in tests.
		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.metrics_update_interval_blocks", []byte(`"120"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.metrics_grace_period_blocks", []byte(`"120"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.metrics_freshness_max_blocks", []byte(`"500"`))
		require.NoError(t, err)

		// Permit any version in tests; binaries are often built with "dev".
		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.min_supernode_version", []byte(`"0.0.0"`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.min_cpu_cores", []byte(`1`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.max_cpu_usage_percent", []byte(`100`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.min_mem_gb", []byte(`1`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.max_mem_usage_percent", []byte(`100`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.min_storage_gb", []byte(`1`))
		require.NoError(t, err)

		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.max_storage_usage_percent", []byte(`100`))
		require.NoError(t, err)

		// Allow any open ports for multi-supernode tests.
		state, err = sjson.SetRawBytes(state, "app_state.supernode.params.required_open_ports", []byte(`[]`))
		require.NoError(t, err)

		return state
	}
}

type icaControllerConfig struct {
	Bin          string
	RPC          string
	ChainID      string
	KeyName      string
	Keyring      string
	Home         string
	GasPrices    string
	ConnectionID string
}

func loadICAControllerConfig() (icaControllerConfig, bool) {
	cfg := icaControllerConfig{
		Bin:          strings.TrimSpace(os.Getenv("ICA_CONTROLLER_BIN")),
		RPC:          strings.TrimSpace(os.Getenv("ICA_CONTROLLER_RPC")),
		ChainID:      strings.TrimSpace(os.Getenv("ICA_CONTROLLER_CHAIN_ID")),
		KeyName:      strings.TrimSpace(os.Getenv("ICA_CONTROLLER_KEY_NAME")),
		Keyring:      strings.TrimSpace(os.Getenv("ICA_CONTROLLER_KEYRING")),
		Home:         strings.TrimSpace(os.Getenv("ICA_CONTROLLER_HOME")),
		GasPrices:    strings.TrimSpace(os.Getenv("ICA_CONTROLLER_GAS_PRICES")),
		ConnectionID: strings.TrimSpace(os.Getenv("ICA_CONNECTION_ID")),
	}
	if cfg.Keyring == "" {
		cfg.Keyring = "test"
	}
	if cfg.Bin == "" || cfg.RPC == "" || cfg.ChainID == "" || cfg.KeyName == "" || cfg.ConnectionID == "" {
		return cfg, false
	}
	return cfg, true
}

func icaControllerKeyAddress(ctx context.Context, cfg icaControllerConfig) (string, error) {
	out, err := runICAControllerCmd(ctx, cfg,
		"keys", "show", cfg.KeyName, "-a",
		"--keyring-backend", cfg.Keyring,
	)
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(out)
	if addr == "" {
		return "", fmt.Errorf("controller owner address is empty")
	}
	return addr, nil
}

func queryICAAddress(ctx context.Context, cfg icaControllerConfig, ownerAddr string) (string, error) {
	out, err := runICAControllerCmd(ctx, cfg,
		"q", "interchain-accounts", "controller", "interchain-account",
		ownerAddr, cfg.ConnectionID,
		"--node", cfg.RPC,
		"--output", "json",
	)
	if err != nil {
		return "", err
	}
	addr := gjson.Get(out, "address").String()
	if addr == "" {
		return "", fmt.Errorf("ICA address not found for owner %s", ownerAddr)
	}
	return addr, nil
}

type icaPacketInfo struct {
	Port     string
	Channel  string
	Sequence uint64
}

func sendICARequestAction(ctx context.Context, cfg icaControllerConfig, msg *actiontypes.MsgRequestAction) ([]string, error) {
	if msg == nil {
		return nil, fmt.Errorf("request action msg is nil")
	}
	any, err := codectypes.NewAnyWithValue(msg)
	if err != nil {
		return nil, fmt.Errorf("pack msg: %w", err)
	}
	tx := &icatypes.CosmosTx{Messages: []*codectypes.Any{any}}
	data, err := gogoproto.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("marshal cosmos tx: %w", err)
	}
	packet := icatypes.InterchainAccountPacketData{
		Type: icatypes.EXECUTE_TX,
		Data: data,
	}
	packetJSON, err := marshalICAPacketJSON(packet)
	if err != nil {
		return nil, err
	}

	tmpFile, err := os.CreateTemp("", "ica-packet-*.json")
	if err != nil {
		return nil, err
	}
	if _, err := tmpFile.Write(packetJSON); err != nil {
		_ = tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}

	txArgs := []string{
		"tx", "interchain-accounts", "controller", "send-tx", cfg.ConnectionID, tmpFile.Name(),
		"--from", cfg.KeyName,
		"--chain-id", cfg.ChainID,
		"--keyring-backend", cfg.Keyring,
		"--node", cfg.RPC,
		"--gas", "auto",
		"--gas-adjustment", "1.3",
		"--broadcast-mode", "sync",
		"--output", "json",
		"--yes",
	}
	if cfg.GasPrices != "" {
		txArgs = append(txArgs, "--gas-prices", cfg.GasPrices)
	}

	out, err := runICAControllerCmd(ctx, cfg, txArgs...)
	if err != nil {
		return nil, err
	}
	txHash, err := parseTxHash(out)
	if err != nil {
		return nil, err
	}

	info, err := waitForICAPacketInfo(ctx, cfg, txHash)
	if err != nil {
		return nil, err
	}
	ackBytes, err := waitForICAAck(ctx, cfg, info)
	if err != nil {
		return nil, err
	}
	return extractActionIDsFromAck(ackBytes)
}

func marshalICAPacketJSON(packet icatypes.InterchainAccountPacketData) ([]byte, error) {
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	return cdc.MarshalJSON(&packet)
}

func parseTxHash(output string) (string, error) {
	txHash := gjson.Get(output, "txhash").String()
	if txHash != "" {
		return txHash, nil
	}
	txHash = gjson.Get(output, "tx_response.txhash").String()
	if txHash != "" {
		return txHash, nil
	}
	return "", fmt.Errorf("tx hash not found in output")
}

func waitForICAPacketInfo(ctx context.Context, cfg icaControllerConfig, txHash string) (icaPacketInfo, error) {
	for i := 0; i < icaPacketInfoRetries; i++ {
		out, err := runICAControllerCmd(ctx, cfg,
			"q", "tx", txHash,
			"--node", cfg.RPC,
			"--output", "json",
		)
		if err == nil {
			if info, ok := extractPacketInfoFromTxJSON(out); ok {
				return info, nil
			}
		}
		time.Sleep(icaPacketInfoDelay)
	}
	return icaPacketInfo{}, fmt.Errorf("packet info not found for tx %s", txHash)
}

func extractPacketInfoFromTxJSON(txJSON string) (icaPacketInfo, bool) {
	eventSets := []gjson.Result{
		gjson.Get(txJSON, "events"),
		gjson.Get(txJSON, "tx_response.events"),
		gjson.Get(txJSON, "tx_response.logs.0.events"),
		gjson.Get(txJSON, "logs.0.events"),
	}
	for _, events := range eventSets {
		for _, evt := range events.Array() {
			if evt.Get("type").String() != "send_packet" {
				continue
			}
			attr := make(map[string]string)
			for _, a := range evt.Get("attributes").Array() {
				key := decodeEventValue(a.Get("key").String())
				val := decodeEventValue(a.Get("value").String())
				if key != "" {
					attr[key] = val
				}
			}
			seqStr := attr["packet_sequence"]
			port := attr["packet_src_port"]
			channel := attr["packet_src_channel"]
			if seqStr == "" || port == "" || channel == "" {
				continue
			}
			seq, err := strconv.ParseUint(seqStr, 10, 64)
			if err != nil {
				return icaPacketInfo{}, false
			}
			return icaPacketInfo{Port: port, Channel: channel, Sequence: seq}, true
		}
	}
	return icaPacketInfo{}, false
}

func waitForICAAck(ctx context.Context, cfg icaControllerConfig, info icaPacketInfo) ([]byte, error) {
	for i := 0; i < icaAckRetries; i++ {
		out, err := runICAControllerCmd(ctx, cfg,
			"q", "ibc", "channel", "packet-acknowledgement",
			info.Port, info.Channel, fmt.Sprint(info.Sequence),
			"--node", cfg.RPC,
			"--output", "json",
		)
		if err == nil {
			ackB64 := gjson.Get(out, "acknowledgement").String()
			if ackB64 != "" {
				ackBytes, err := base64.StdEncoding.DecodeString(ackB64)
				if err == nil {
					return ackBytes, nil
				}
			}
		}
		time.Sleep(icaAckDelay)
	}
	return nil, fmt.Errorf("packet acknowledgement not found")
}

func extractActionIDsFromAck(ackBytes []byte) ([]string, error) {
	var ack chantypes.Acknowledgement
	if err := gogoproto.Unmarshal(ackBytes, &ack); err != nil {
		if err := chantypes.SubModuleCdc.UnmarshalJSON(ackBytes, &ack); err != nil {
			return nil, err
		}
	}
	if ack.GetError() != "" {
		return nil, fmt.Errorf("ica ack error: %s", ack.GetError())
	}
	result := ack.GetResult()
	if len(result) == 0 {
		return nil, fmt.Errorf("ack result is empty")
	}
	var msgData sdk.TxMsgData
	if err := gogoproto.Unmarshal(result, &msgData); err != nil {
		return nil, err
	}
	ids := extractActionIDsFromTxMsgData(&msgData)
	if len(ids) == 0 {
		return nil, fmt.Errorf("no action ids found in ack result")
	}
	return ids, nil
}

func extractActionIDsFromTxMsgData(msgData *sdk.TxMsgData) []string {
	var ids []string
	if msgData == nil {
		return ids
	}
	for _, any := range msgData.MsgResponses {
		if any == nil || any.TypeUrl == "" {
			continue
		}
		if !strings.HasSuffix(any.TypeUrl, "MsgRequestActionResponse") {
			continue
		}
		var resp actiontypes.MsgRequestActionResponse
		if err := gogoproto.Unmarshal(any.Value, &resp); err != nil {
			continue
		}
		if resp.ActionId != "" {
			ids = append(ids, resp.ActionId)
		}
	}
	return ids
}

func runICAControllerCmd(ctx context.Context, cfg icaControllerConfig, args ...string) (string, error) {
	cmdArgs := append([]string{}, args...)
	if cfg.Home != "" {
		cmdArgs = append([]string{"--home", cfg.Home}, cmdArgs...)
	}
	cmd := exec.CommandContext(ctx, locateExecutable(cfg.Bin), cmdArgs...) //nolint:gosec
	cmd.Dir = WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ica controller cmd failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func decodeEventValue(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return raw
	}
	return string(decoded)
}

func waitForActionStateWithClient(ctx context.Context, client lumera.Client, actionID string, state actiontypes.ActionState) error {
	for i := 0; i < actionStateRetries; i++ {
		resp, err := client.Action().GetAction(ctx, actionID)
		if err == nil && resp != nil && resp.Action != nil && resp.Action.State == state {
			return nil
		}
		time.Sleep(actionStateDelay)
	}
	return fmt.Errorf("action %s did not reach state %s", actionID, state.String())
}

func verifyDownloadedFile(t *testing.T, baseDir, actionID, originalPath string, originalData []byte, originalHash [32]byte) {
	t.Helper()

	expectedDownloadDir := filepath.Join(baseDir, actionID)
	if _, err := os.Stat(expectedDownloadDir); os.IsNotExist(err) {
		t.Fatalf("expected download directory does not exist: %s", expectedDownloadDir)
	}
	files, err := os.ReadDir(expectedDownloadDir)
	require.NoError(t, err, "failed to read download directory: %s", expectedDownloadDir)

	var downloadedFilePath string
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if filepath.Base(file.Name()) == filepath.Base(originalPath) {
			downloadedFilePath = filepath.Join(expectedDownloadDir, file.Name())
			break
		}
	}
	if downloadedFilePath == "" && len(files) > 0 {
		downloadedFilePath = filepath.Join(expectedDownloadDir, files[0].Name())
	}
	if downloadedFilePath == "" {
		t.Fatalf("no files found in download directory for content verification")
	}

	downloadedData, err := os.ReadFile(downloadedFilePath)
	require.NoError(t, err, "failed to read downloaded file")

	downloadedHash := sha256.Sum256(downloadedData)
	require.Equal(t, len(originalData), len(downloadedData), "downloaded file size should match original file size")
	require.Equal(t, originalHash, downloadedHash, "downloaded file hash should match original file hash")
}
