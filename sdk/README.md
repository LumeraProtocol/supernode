# Supernode SDK

The Lumera Supernode SDK is a comprehensive toolkit  for interacting with the Lumera Protocol's supernode network to perform cascade operations

## Cascade End-to-End

This walks through building Cascade metadata, submitting the on‑chain action, starting Cascade, and downloading the result using the SDK (sdk/action), low‑level helpers (pkg/cascadekit), and the Lumera client (pkg/lumera).

1) Build metadata (+ price, expiration)
```
meta, price, expiration, err := client.BuildCascadeMetadataFromFile(ctx, filePath, /*public=*/false)
if err != nil { /* handle */ }
```
Under the hood: encodes file to a single‑block layout, signs layout/index (creator key), computes blake3(data), picks a random ic (1..100), derives max from chain params, computes price from file size + fee params, and expiration from chain duration (+1h buffer).

2) Submit RequestAction (via pkg/lumera)
```
b, _ := json.Marshal(meta)
resp, err := lumeraClient.ActionMsg().RequestAction(ctx, "CASCADE", string(b), price, expiration, "0")
if err != nil { /* handle */ }
// Extract actionID from tx events or query later
```

3) Start Cascade
```
sig, _ := client.GenerateStartCascadeSignatureFromFile(ctx, filePath)
taskID, err := client.StartCascade(ctx, filePath, actionID, sig)
```

4) Download Cascade
```
// Public (meta.Public == true): empty signature
taskID, _ := client.DownloadCascade(ctx, actionID, outDir, "")

// Private: sign only the actionID with the creator's key (helper shown)
dlSig, _ := client.GenerateDownloadSignature(ctx, actionID, creatorAddr)
taskID, _ = client.DownloadCascade(ctx, actionID, outDir, dlSig)
```

Notes
- Public downloads require no signature.
- The SDK derives ic/max/price/expiration internally; you don’t need to fetch params yourself.

## Table of Contents

- [Cascade End-to-End](#cascade-end-to-end)

- [Configuration](#configuration)
- [Client Initialization](#client-initialization)
- [Action Client Methods](#action-client-methods)  
  - [StartCascade](#startcascade)
  - [DownloadCascade](#downloadcascade)
  - [GetTask](#gettask)
  - [DeleteTask](#deletetask)
  - [GetSupernodeStatus](#getsupernodestatus)
  - [SubscribeToEvents](#subscribetoevents)
  - [SubscribeToAllEvents](#subscribetoallevents)
- [Event System](#event-system)
- [Error Handling](#error-handling)
- [Timeouts & Networking](#timeouts--networking)

## Configuration

The SDK uses a structured configuration system with two main components: account settings and blockchain connection details.

### Configuration Structure

```go
import (
    "github.com/LumeraProtocol/supernode/v2/sdk/config"
    "github.com/cosmos/cosmos-sdk/crypto/keyring"
    "github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
)

// Account configuration
accountConfig := config.AccountConfig{
    KeyName:            "my-key",            // Key name in keyring  
    Keyring:            keyring,             // Cosmos SDK keyring instance
    // PeerType is optional - defaults to SIMPLENODE when omitted (recommended for most users)
}

// Blockchain connection configuration
lumeraConfig := config.LumeraConfig{
    GRPCAddr: "localhost:9090",  // Lumera blockchain GRPC endpoint
    ChainID:  "testing",         // Chain identifier
}

// Combine configurations
sdkConfig := config.NewConfig(accountConfig, lumeraConfig)

// Validate configuration (recommended)
if err := sdkConfig.Validate(); err != nil {
    // Handle validation error
}
```

### Required Fields

**AccountConfig:**
- `KeyName`: Name of the key in your keyring
- `Keyring`: Initialized Cosmos SDK keyring containing your keys
- `PeerType`: Peer type from securekeyx (optional, defaults to SIMPLENODE when left empty, which is suitable for most use cases)

**LumeraConfig:**
- `GRPCAddr`: GRPC endpoint of the Lumera blockchain
- `ChainID`: Chain identifier for the network you're connecting to

### Keyring Setup

The SDK requires a Cosmos SDK keyring. Here are common initialization patterns:

```go
import "github.com/cosmos/cosmos-sdk/crypto/keyring"

// For testing (stores keys in memory)
kr, err := keyring.New("app-name", "test", "/tmp", nil)

// For production (stores keys in OS keyring)
kr, err := keyring.New("app-name", "os", "", nil)

// For file-based storage
kr, err := keyring.New("app-name", "file", "/path/to/keys", nil)
```

### Example Configuration

```go
config := config.NewConfig(
    config.AccountConfig{
        KeyName:            "my-key",
        Keyring:            keyring,
    },
    config.LumeraConfig{
        GRPCAddr: "localhost:9090",
        ChainID:  "testing",
    },
)
```

## Client Initialization

The SDK requires a Cosmos SDK keyring containing your cryptographic keys for signing transactions and authenticating with supernodes. You must provide an initialized keyring instance along with the name of the key to use.

```go
import (
    "context"
    "github.com/LumeraProtocol/supernode/v2/sdk/action"
    "github.com/LumeraProtocol/supernode/v2/sdk/config"
    "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// keyring is a cosmos-sdk keyring.Keyring that contains your keys
// It should be initialized with your preferred backend (test, file, os, etc.)


// Set up configuration
accountConfig := config.AccountConfig{
    KeyName:            "your-key",   // Name of the key in your keyring
    Keyring:            keyring,      // Cosmos SDK keyring instance
}

lumeraConfig := config.LumeraConfig{
    GRPCAddr: "localhost:9090", // Lumera blockchain GRPC address
    ChainID:  "testing",        // Chain ID
}

sdkConfig := config.NewConfig(accountConfig, lumeraConfig)

// Initialize the action client
client, err := action.NewClient(context.Background(), sdkConfig, logger)
if err != nil {
    // Handle error
}
```

## Action Client Methods

### StartCascade

Initiates a cascade operation to upload and process a file through the supernode network.

```go
taskID, err := client.StartCascade(
    ctx,
    "/path/to/file.txt",  // File path to upload
    "action-id-123",      // Action ID from blockchain transaction
    "base64-signature",   // Base64-encoded signature of file's blake3 hash
)
if err != nil {
    // Handle error
}
// taskID can be used to track the operation progress
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `filePath string`: Path to the file to be processed
- `actionID string`: ID of the action registered on the blockchain
- `signature string`: Base64-encoded signature of the file's blake3 hash by the action creator

**Signature Creation Steps:**
1. Read the entire file content into a byte array
2. Compute the Blake3 hash of the file data using a 32-byte hasher
3. Sign the Blake3 hash bytes using your keyring and key name  
4. Base64 encode the signed hash to create the final signature

```go
import (
    "os"
    "io"
    "bytes"
    "encoding/base64"
    "lukechampine.com/blake3"
    "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
)

// 1. Read the file content
fileData, err := os.ReadFile("/path/to/file.txt")
if err != nil {
    // Handle error
}

// 2. Compute Blake3 hash of the file data
hasher := blake3.New(32, nil)
if _, err := io.Copy(hasher, bytes.NewReader(fileData)); err != nil {
    // Handle error
}
hash := hasher.Sum(nil)

// 3. Sign the hash using your keyring
signedHash, err := keyring.SignBytes(keyring, keyName, hash)
if err != nil {
    // Handle error
}

// 4. Base64 encode the signature - this is what you pass to StartCascade
signature := base64.StdEncoding.EncodeToString(signedHash)
```

**Returns:**
- `string`: Task ID for tracking the operation
- `error`: Error if the operation fails

### DownloadCascade

Downloads a cascade file from the supernode network using an action ID.

```go
taskID, err := client.DownloadCascade(
    ctx,
    "action-id-123",      // Action ID of the cascade to download
    "/output/directory",  // Directory where the file will be saved
    "base64-signature",   // Download signature
)
if err != nil {
    // Handle error
}
// taskID can be used to track the download progress
```

Note: If the action's cascade metadata sets `public: true`, the signature may be left empty to allow anonymous download.

**Parameters:**
- `ctx context.Context`: Context for the operation
- `actionID string`: ID of the action to download
- `outputDir string`: Directory where the downloaded file will be saved
- `signature string`: Base64-encoded signature for download authorization (leave empty for public cascades)

**Signature Creation for Download:**
For private cascades, sign only the action ID with the creator's key and base64‑encode the result.

```go
sig, err := client.GenerateDownloadSignature(ctx, actionID, creatorAddress)
// Pass `sig` to DownloadCascade
```

**Returns:**
- `string`: Task ID for tracking the download operation
- `error`: Error if the operation fails

### GetTask

Retrieves information about a specific task by its ID.

```go
taskEntry, found := client.GetTask(ctx, "task-id-123")
if !found {
    // Task not found
}
// Use taskEntry to access task information
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `taskID string`: ID of the task to retrieve

**Returns:**
- `*task.TaskEntry`: Task information including status, events, and metadata
- `bool`: Whether the task was found

### DeleteTask

Removes a task from the task cache by its ID.

```go
err := client.DeleteTask(ctx, "task-id-123")
if err != nil {
    // Handle error (task not found or deletion failed)
}
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `taskID string`: ID of the task to delete

**Returns:**
- `error`: Error if the task doesn't exist or deletion fails

### GetSupernodeStatus (Status API)

Retrieves the current status and resource information of a specific supernode.

```go
status, err := client.GetSupernodeStatus(ctx, "lumera1abc...")
if err != nil {
    // Handle error
}
// Use status to access CPU, memory, and service information
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `supernodeAddress string`: Cosmos address of the supernode

**Returns:**
- `*supernode.StatusResponse`: Status information including CPU usage, memory stats, active services, and P2P metrics
- `error`: Error if the supernode is unreachable or the query fails

Notes:
- The SDK always requests P2P metrics to ensure `Network.PeersCount` is populated for eligibility checks.
- Status response is the generated type; no mapping layer in the SDK.

### SubscribeToEvents

Registers an event handler for specific event types to monitor task progress.

```go
err := client.SubscribeToEvents(ctx, event.SDKTaskCompleted, func(ctx context.Context, e event.Event) {
    fmt.Printf("Task %s completed with type %s\n", e.TaskID, e.TaskType)
    // Handle the specific event
})
if err != nil {
    // Handle subscription error
}
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `eventType event.EventType`: Specific event type to listen for
- `handler event.Handler`: Function to call when the event occurs

**Returns:**
- `error`: Error if subscription fails

### SubscribeToAllEvents

Registers an event handler for all event types to monitor all task activity.

```go
err := client.SubscribeToAllEvents(ctx, func(ctx context.Context, e event.Event) {
    fmt.Printf("Event %s for task %s: %v\n", e.Type, e.TaskID, e.Data)
    // Handle any event type
})
if err != nil {
    // Handle subscription error
}
```

**Parameters:**
- `ctx context.Context`: Context for the operation
- `handler event.Handler`: Function to call for any event

**Returns:**
- `error`: Error if subscription fails

## Event System

The SDK provides an event system to monitor task progress through event subscriptions. You can subscribe to specific event types or all events to track the lifecycle of your tasks.

### Available Events

**SDK Task Events:**
- `SDKTaskStarted`: Task has been initiated
- `SDKSupernodesUnavailable`: No supernodes available for processing
- `SDKSupernodesFound`: Supernodes discovered for task processing
- `SDKRegistrationAttempt`: Attempting to register with a supernode
- `SDKRegistrationFailure`: Registration with supernode failed
- `SDKRegistrationSuccessful`: Successfully registered with supernode
- `SDKTaskTxHashReceived`: Transaction hash received from supernode
- `SDKTaskCompleted`: Task completed successfully
- `SDKTaskFailed`: Task failed with error
 - `SDKConnectionEstablished`: Connection to supernode established
 - `SDKUploadStarted`: Upload started (size, chunk size, est chunks)
 - `SDKUploadCompleted`: Upload completed (size, chunks, elapsed, avg throughput)
 - `SDKUploadFailed`: Upload failed (reason=timeout|send_error|read_error|file_open|file_stat|close_send)
 - `SDKProcessingStarted`: Waiting for server progress/final tx hash
 - `SDKProcessingFailed`: Processing failed (reason=stream_recv|missing_final_response)
 - `SDKProcessingTimeout`: Processing exceeded time budget and was cancelled
- `SDKDownloadAttempt`: Attempting to download from supernode
- `SDKDownloadFailure`: Download attempt failed
- `SDKOutputPathReceived`: File download path received
- `SDKDownloadSuccessful`: Download completed successfully

**Supernode Events (forwarded from supernodes):**
- `SupernodeActionRetrieved`: Action retrieved from blockchain
- `SupernodeActionFeeVerified`: Action fee verified
- `SupernodeTopCheckPassed`: Top supernode verification passed
- `SupernodeMetadataDecoded`: Action metadata decoded successfully
- `SupernodeDataHashVerified`: Data hash verification completed
- `SupernodeInputEncoded`: Input data encoded
- `SupernodeSignatureVerified`: Signature verification passed
- `SupernodeRQIDGenerated`: RaptorQ ID generated
- `SupernodeRQIDVerified`: RaptorQ ID verified
- `SupernodeFinalizeSimulated`: Finalize transaction simulated successfully (pre-storage)
- `SupernodeArtefactsStored`: Artifacts stored successfully
 - `SupernodeActionFinalized`: Action processing finalized
 - `SupernodeArtefactsDownloaded`: Artifacts downloaded
 - `SupernodeFinalizeSimulationFailed`: Finalize action simulation failed
 - `SupernodeUnknown`: Unknown supernode event

Note: For backward compatibility, older supernodes may emit the finalize simulation as an `RQID_VERIFIED` event with the message `"finalize action simulation passed"`. The SDK adapter maps this to `SupernodeFinalizeSimulated` automatically.

### Event Data Keys

Events may include additional data accessible through these keys:

- `event.KeyError`: Error message (for failure events)
- `event.KeyCount`: Count of items (e.g., supernodes found)
- `event.KeySupernode`: Supernode endpoint
- `event.KeySupernodeAddress`: Supernode cosmos address
- `event.KeyIteration`: Attempt iteration number
- `event.KeyTxHash`: Transaction hash
- `event.KeyMessage`: Event message
- `event.KeyProgress`: Progress information
- `event.KeyEventType`: Event type string
- `event.KeyOutputPath`: File output path
- `event.KeyTaskID`: Task ID
- `event.KeyActionID`: Action ID

### Usage Examples

**Subscribe to Task Completion:**
```go
err := client.SubscribeToEvents(ctx, event.SDKTaskCompleted, func(ctx context.Context, e event.Event) {
    fmt.Printf("Task %s completed\n", e.TaskID)
})
```

**Subscribe to All Events:**
```go
err := client.SubscribeToAllEvents(ctx, func(ctx context.Context, e event.Event) {
    fmt.Printf("Event: %s for task %s\n", e.Type, e.TaskID)
})
## Timeouts & Networking

For an in-depth explanation of how contexts, deadlines, client/server options, and cascade registration timeouts are applied see:

- `supernode/sdk/docs/cascade-timeouts.md`
