package system

import (
	"bytes"
	"context"
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

	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"lukechampine.com/blake3"

	"github.com/LumeraProtocol/supernode/sdk/action"
	"github.com/LumeraProtocol/supernode/sdk/event"

	"github.com/LumeraProtocol/lumera/x/action/types"
	sdkconfig "github.com/LumeraProtocol/supernode/sdk/config"

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
	os.Setenv("SYSTEM_TEST", "true")
	defer os.Unsetenv("SYSTEM_TEST")
	// Test account credentials - these values are consistent across test runs
	const testMnemonic = "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"
	const expectedAddress = "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4"
	const testKeyName = "testkey1"
	const userKeyName = "user"
	const userMnemonic = "little tone alley oval festival gloom sting asthma crime select swap auto when trip luxury pact risk sister pencil about crisp upon opera timber"
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
		actionType = "CASCADE"    // The action type for fountain code processing
		price      = "23800ulume" // Price for the action in ulume tokens
	)
	t.Log("Step 1: Starting all services")

	// Update the genesis file with action parameters
	sut.ModifyGenesisJSON(t, SetActionParams(t))

	// Reset and start the blockchain
	sut.ResetChain(t)
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)
	// ---------------------------------------
	// Register Multiple Supernodes to process the request
	// ---------------------------------------
	t.Log("Registering multiple supernodes to process requests")

	// Helper function to register a supernode
	registerSupernode := func(nodeKey string, port string, addr string) {
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
			addr,                // supernode account
			"--from", nodeKey,
		}

		resp := cli.CustomCommand(registerCmd...)
		RequireTxSuccess(t, resp)

		// Wait for transaction to be included in a block
		sut.AwaitNextBlock(t)
	}

	// Register three supernodes with different ports
	registerSupernode("node0", "4444", "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4")
	registerSupernode("node1", "4446", "lumera1cf0ms9ttgdvz6zwlqfty4tjcawhuaq69p40w0c")
	registerSupernode("node2", "4448", "lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp")
	t.Log("Successfully registered three supernodes")

	// Fund Lume
	cli.FundAddress("lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4", "100000ulume")
	cli.FundAddressWithNode("lumera1cf0ms9ttgdvz6zwlqfty4tjcawhuaq69p40w0c", "100000ulume", "node1")
	cli.FundAddressWithNode("lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp", "100000ulume", "node2")

	queryHeight := sut.AwaitNextBlock(t)
	args := []string{
		"query",
		"supernode",
		"get-top-super-nodes-for-block",
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
	cli.FundAddress(recoveredAddress, fundAmount)      // ulume tokens for action fees
	cli.FundAddress(recoveredAddress, "10000000stake") // stake tokens

	// Fund user account
	t.Logf("Funding user address %s with %s", userAddress, fundAmount)
	cli.FundAddress(userAddress, fundAmount)      // ulume tokens for action fees
	cli.FundAddress(userAddress, "10000000stake") // stake tokens

	sut.AwaitNextBlock(t) // Wait for funding transaction to be processed

	// Create an in-memory keyring for cryptographic operations
	// This keyring is separate from the blockchain keyring and used for local signing
	keplrKeyring, err := keyring.InitKeyring("memory", "")
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

	//
	require.NoError(t, err, "Failed to initialize Lumera client")

	// ---------------------------------------
	// Step 4: Create and prepare layout file for RaptorQ encoding
	// ---------------------------------------
	t.Log("Step 4: Creating test file for RaptorQ encoding")

	// Create a test file with sample data in a temporary directory

	testFileName := "testfile.txt"
	testFileFullpath := filepath.Join(t.TempDir(), testFileName)
	testData := []byte("This is test data for RaptorQ encoding in the Lumera nasaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaasassasetwork")
	err = os.WriteFile(testFileFullpath, testData, 0644)
	require.NoError(t, err, "Failed to write test file")

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

	rqCodec := codec.NewRaptorQCodec(raptorQFilesDir)

	ctx := context.Background()
	encodeRes, err := rqCodec.Encode(ctx, codec.EncodeRequest{
		Data:   data,
		TaskID: "1",
	})

	require.NoError(t, err, "Failed to encode data with RaptorQ")

	metadataFile := encodeRes.Metadata

	// Marshal metadata to JSON and convert to bytes
	me, err := json.Marshal(metadataFile)
	require.NoError(t, err, "Failed to marshal metadata to JSON")

	// Step 1: Encode the metadata JSON as base64 string
	// This becomes the first part of our signature format
	regularbase64EncodedData := base64.StdEncoding.EncodeToString(me)
	t.Logf("Base64 encoded RQ IDs file length: %d", len(regularbase64EncodedData))

	// Step 2: Sign the base64-encoded string with user key instead of testkey1
	signedMetaData, err := keyring.SignBytes(keplrKeyring, userKeyName, []byte(regularbase64EncodedData))
	require.NoError(t, err, "Failed to sign metadata")

	// Step 3: Encode the resulting signature as base64
	signedbase64EncodedData := base64.StdEncoding.EncodeToString(signedMetaData)
	t.Logf("Base64 signed RQ IDs file length: %d", len(signedbase64EncodedData))

	// Step 4: Format according to the expected verification pattern: Base64(rq_ids).signature
	// This format is expected by VerifySignature in the CascadeActionHandler.RegisterAction method
	// - regularbase64EncodedData: The base64-encoded metadata
	// - signedbase64EncodedData: The base64-encoded signature of the above
	signatureFormat := fmt.Sprintf("%s.%s", regularbase64EncodedData, signedbase64EncodedData)
	t.Logf("Signature format prepared with length: %d bytes", len(signatureFormat))

	// Data hash with blake3
	hash, err := Blake3Hash(data)
	b64EncodedHash := base64.StdEncoding.EncodeToString(hash)
	require.NoError(t, err, "Failed to compute Blake3 hash")
	// ---------------------------------------
	t.Log("Step 7: Creating metadata and submitting action request")

	// Create CascadeMetadata struct with all required fields
	cascadeMetadata := types.CascadeMetadata{
		DataHash:   b64EncodedHash,                  // Hash of the original file
		FileName:   filepath.Base(testFileFullpath), // Original filename
		RqIdsIc:    uint64(121),                     // Count of RQ identifiers
		Signatures: signatureFormat,                 // Combined signature format
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

	// Submit the action request transaction to the blockchain using user key
	// This registers the request with metadata for supernodes to process
	actionRequestResp := cli.CustomCommand(
		"tx", "action", "request-action",
		actionType,            // CASCADE action type
		metadata,              // JSON metadata with all required fields
		price,                 // Price in ulume tokens
		expirationTime,        // Unix timestamp for expiration
		"--from", userKeyName, // Use user key for transaction submission
		"--gas", "auto",
		"--gas-adjustment", "1.5",
	)

	// Verify the transaction was successful
	RequireTxSuccess(t, actionRequestResp)
	t.Logf("Action request successful: %s", actionRequestResp)

	// Wait for transaction to be included in a block
	sut.AwaitNextBlock(t)

	// Verify the account can be queried with its public key
	accountResp := cli.CustomQuery("q", "auth", "account", userAddress)
	require.Contains(t, accountResp, "public_key", "User account public key should be available")

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

	time.Sleep(60 * time.Second)

	// Set up action client configuration
	// This defines how to connect to network services
	accConfig := sdkconfig.AccountConfig{
		LocalCosmosAddress: recoveredAddress,
	}

	lumraConfig := sdkconfig.LumeraConfig{
		GRPCAddr: lumeraGRPCAddr,
		ChainID:  lumeraChainID,
		Timeout:  300, // 30 seconds timeout
		KeyName:  testKeyName,
	}
	actionConfig := sdkconfig.Config{
		Account: accConfig,
		Lumera:  lumraConfig,
	}

	// Initialize action client for cascade operations
	actionClient, err := action.NewClient(
		ctx,
		actionConfig,
		nil,          // Nil logger - use default
		keplrKeyring, // Use the in-memory keyring for signing
	)
	require.NoError(t, err, "Failed to create action client")

	// ---------------------------------------
	// Step 9: Subscribe to all events and extract tx hash
	// ---------------------------------------

	// Channel to receive the transaction hash
	txHashCh := make(chan string, 1)
	completionCh := make(chan bool, 1)

	// Subscribe to ALL events
	err = actionClient.SubscribeToAllEvents(ctx, func(ctx context.Context, e event.Event) {
		// Only capture TxhasReceived events
		if e.Type == event.TxhasReceived {
			if txHash, ok := e.Data["txhash"].(string); ok && txHash != "" {
				// Send the hash to our channel
				txHashCh <- txHash
			}
		}

		// Also monitor for task completion
		if e.Type == event.TaskCompleted {
			completionCh <- true
		}
	})
	require.NoError(t, err, "Failed to subscribe to events")

	// Start cascade operation
	t.Logf("Starting cascade operation with action ID: %s", actionID)
	taskID, err := actionClient.StartCascade(
		ctx,
		data,     // data []byte
		actionID, // Action ID from the transaction
	)
	require.NoError(t, err, "Failed to start cascade operation")
	t.Logf("Cascade operation started with task ID: %s", taskID)

	recievedhash := <-txHashCh
	<-completionCh

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
				if attr.Get("key").String() == "amount" && attr.Get("value").String() == price {
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
				if attr.Get("key").String() == "amount" && attr.Get("value").String() == price {
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
	require.Equal(t, price, amount, "Payment amount should match action price")

	t.Log("Test completed successfully!")
}
func Blake3Hash(msg []byte) ([]byte, error) {
	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, bytes.NewReader(msg)); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
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
            "fee_per_byte": {
                "amount": "100",
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
