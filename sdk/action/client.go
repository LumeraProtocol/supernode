package action

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"
	"github.com/LumeraProtocol/supernode/v2/sdk/net"
	"github.com/LumeraProtocol/supernode/v2/sdk/task"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	keyringpkg "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Client defines the interface for action operations
//
//go:generate mockery --name=Client --output=testutil/mocks --outpkg=mocks --filename=client_mock.go
type Client interface {
	// StartCascade initiates a cascade operation with file path, action ID, and signature
	// signature: Base64-encoded signature of file's blake3 hash by action creator
	StartCascade(ctx context.Context, filePath string, actionID string, signature string) (string, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool)
	SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error
	SubscribeToAllEvents(ctx context.Context, handler event.Handler) error
	GetSupernodeStatus(ctx context.Context, supernodeAddress string) (*pb.StatusResponse, error)
	// DownloadCascade downloads cascade to outputDir, filename determined by action ID
	DownloadCascade(ctx context.Context, actionID, outputDir, signature string) (string, error)
	// BuildCascadeMetadataFromFile encodes the file to produce a single-block layout,
	// generates the cascade signatures, computes the blake3 data hash (base64),
	// and returns CascadeMetadata (with signatures) along with price and expiration time.
	// Internally derives ic (random in [1..100]), max (from chain params), price (GetActionFee),
	// and expiration (params duration + 1h buffer).
	BuildCascadeMetadataFromFile(ctx context.Context, filePath string, public bool) (actiontypes.CascadeMetadata, string, string, error)
	// GenerateStartCascadeSignatureFromFile computes blake3(file) and signs it with the configured key; returns base64 signature.
	GenerateStartCascadeSignatureFromFile(ctx context.Context, filePath string) (string, error)
	// GenerateDownloadSignature signs the payload "actionID" and returns a base64 signature.
	GenerateDownloadSignature(ctx context.Context, actionID, creatorAddr string) (string, error)
}

// ClientImpl implements the Client interface
type ClientImpl struct {
	config       config.Config
	taskManager  task.Manager
	logger       log.Logger
	keyring      keyring.Keyring
	lumeraClient lumera.Client
	signerAddr   string
}

// Verify interface compliance at compile time
var _ Client = (*ClientImpl)(nil)

// NewClient creates a new action client
func NewClient(ctx context.Context, config config.Config, logger log.Logger) (Client, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	addr, err := keyringpkg.GetAddress(config.Account.Keyring, config.Account.KeyName)
	if err != nil {
		return nil, fmt.Errorf("resolve signer address: %w", err)
	}

	// Create lumera client once
	lumeraClient, err := lumera.NewAdapter(ctx,
		lumera.ConfigParams{
			GRPCAddr: config.Lumera.GRPCAddr,
			ChainID:  config.Lumera.ChainID,
			KeyName:  config.Account.KeyName,
			Keyring:  config.Account.Keyring,
		},
		logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create lumera client: %w", err)
	}

	// Create task manager with shared lumera client
	taskManager, err := task.NewManagerWithLumeraClient(ctx, config, logger, lumeraClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	return &ClientImpl{
		config:       config,
		taskManager:  taskManager,
		logger:       logger,
		keyring:      config.Account.Keyring,
		lumeraClient: lumeraClient,
		signerAddr:   addr.String(),
	}, nil
}

// StartCascade initiates a cascade operation
func (c *ClientImpl) StartCascade(ctx context.Context, filePath string, actionID string, signature string) (string, error) {
	if actionID == "" {
		c.logger.Error(ctx, "Empty action ID provided")
		return "", ErrEmptyActionID
	}
	if filePath == "" {
		c.logger.Error(ctx, "Empty file path provided")
		return "", ErrEmptyData
	}

	taskID, err := c.taskManager.CreateCascadeTask(ctx, filePath, actionID, signature)
	if err != nil {
		c.logger.Error(ctx, "Failed to create cascade task", "error", err)
		return "", fmt.Errorf("failed to create cascade task: %w", err)
	}

	c.logger.Info(ctx, "Cascade task created successfully", "taskID", taskID)
	return taskID, nil
}

// GetTask retrieves a task by its ID
func (c *ClientImpl) GetTask(ctx context.Context, taskID string) (*task.TaskEntry, bool) {
	task, found := c.taskManager.GetTask(ctx, taskID)
	if found {
		return task, true
	}
	c.logger.Debug(ctx, "Task not found", "taskID", taskID)

	return nil, false
}

// DeleteTask removes a task by its ID
func (c *ClientImpl) DeleteTask(ctx context.Context, taskID string) error {
	c.logger.Debug(ctx, "Deleting task", "taskID", taskID)
	if taskID == "" {
		c.logger.Error(ctx, "Empty task ID provided")
		return fmt.Errorf("task ID cannot be empty")
	}

	if err := c.taskManager.DeleteTask(ctx, taskID); err != nil {
		c.logger.Error(ctx, "Failed to delete task", "taskID", taskID, "error", err)
		return fmt.Errorf("failed to delete task: %w", err)
	}
	c.logger.Info(ctx, "Task deleted successfully", "taskID", taskID)

	return nil
}

// SubscribeToEvents registers a handler for specific event types
func (c *ClientImpl) SubscribeToEvents(ctx context.Context, eventType event.EventType, handler event.Handler) error {
	if c.taskManager == nil {
		return fmt.Errorf("TaskManager is nil, cannot subscribe to events")
	}

	c.logger.Debug(ctx, "Subscribing to events via task manager", "eventType", eventType)
	c.taskManager.SubscribeToEvents(ctx, eventType, handler)

	return nil
}

// SubscribeToAllEvents registers a handler for all events
func (c *ClientImpl) SubscribeToAllEvents(ctx context.Context, handler event.Handler) error {
	if c.taskManager == nil {
		return fmt.Errorf("TaskManager is nil, cannot subscribe to events")
	}

	c.logger.Debug(ctx, "Subscribing to all events via task manager")
	c.taskManager.SubscribeToAllEvents(ctx, handler)

	return nil
}

// GetSupernodeStatus retrieves the status of a specific supernode by its address
func (c *ClientImpl) GetSupernodeStatus(ctx context.Context, supernodeAddress string) (*pb.StatusResponse, error) {
	if supernodeAddress == "" {
		c.logger.Error(ctx, "Empty supernode address provided")
		return nil, fmt.Errorf("supernode address cannot be empty")
	}

	c.logger.Debug(ctx, "Getting supernode status", "address", supernodeAddress)

	// Get supernode info including latest address
	supernodeInfo, err := c.lumeraClient.GetSupernodeWithLatestAddress(ctx, supernodeAddress)
	if err != nil {
		c.logger.Error(ctx, "Failed to get supernode info", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to get supernode info: %w", err)
	}

	// Create lumera supernode object for network client
	lumeraSupernode := lumera.Supernode{
		CosmosAddress: supernodeAddress,
		GrpcEndpoint:  supernodeInfo.LatestAddress,
		State:         lumera.ParseSupernodeState(supernodeInfo.CurrentState),
	}

	// Create network client factory
	clientFactory, err := net.NewClientFactory(ctx, c.logger, c.keyring, c.lumeraClient, net.FactoryConfig{
		KeyName:  c.config.Account.KeyName,
		PeerType: c.config.Account.PeerType,
	})
	if err != nil {
		c.logger.Error(ctx, "Failed to create client factory", "error", err)
		return nil, fmt.Errorf("failed to create client factory: %w", err)
	}

	// Create client for the specific supernode
	supernodeClient, err := clientFactory.CreateClient(ctx, lumeraSupernode)
	if err != nil {
		c.logger.Error(ctx, "Failed to create supernode client", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to create supernode client: %w", err)
	}
	defer supernodeClient.Close(ctx)

	// Get the supernode status
	status, err := supernodeClient.GetSupernodeStatus(ctx)
	if err != nil {
		c.logger.Error(ctx, "Failed to get supernode status", "address", supernodeAddress, "error", err)
		return nil, fmt.Errorf("failed to get supernode status: %w", err)
	}

	c.logger.Info(ctx, "Successfully retrieved supernode status", "address", supernodeAddress)
	return status, nil
}

func (c *ClientImpl) DownloadCascade(ctx context.Context, actionID, outputDir, signature string) (string, error) {

	if actionID == "" {
		return "", fmt.Errorf("actionID is empty")
	}

	taskID, err := c.taskManager.CreateDownloadTask(ctx, actionID, outputDir, signature)
	if err != nil {
		return "", fmt.Errorf("create download task: %w", err)
	}

	c.logger.Info(ctx, "cascade download task created",
		"task_id", taskID,
		"action_id", actionID,
	)

	return taskID, nil
}

// BuildCascadeMetadataFromFile produces Cascade metadata (including signatures) from a local file path.
// It generates only the single-block RaptorQ layout metadata (no symbols), signs it,
// and returns metadata, price and expiration.
func (c *ClientImpl) BuildCascadeMetadataFromFile(ctx context.Context, filePath string, public bool) (actiontypes.CascadeMetadata, string, string, error) {
	if filePath == "" {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("file path is empty")
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("stat file: %w", err)
	}

	// Build layout metadata only (no symbols). Supernodes will create symbols.
	rq := codec.NewRaptorQCodec("")
	metaResp, err := rq.CreateMetadata(ctx, codec.CreateMetadataRequest{Path: filePath})
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("raptorq create metadata: %w", err)
	}
	layout := metaResp.Layout

	// Derive `max` from chain params, then create signatures and index IDs
	paramsResp, err := c.lumeraClient.GetActionParams(ctx)
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("get action params: %w", err)
	}
	// Use MaxRaptorQSymbols as the count for rq_ids generation.
	var max uint32
	if paramsResp != nil && paramsResp.Params.MaxRaptorQSymbols > 0 {
		max = uint32(paramsResp.Params.MaxRaptorQSymbols)
	} else {
		// Fallback to a sane default if params missing
		max = 50
	}
	// Pick a random initial counter in [1,100]
	rnd, _ := crand.Int(crand.Reader, big.NewInt(100))
	ic := uint32(rnd.Int64() + 1) // 1..100

	// Create signatures from the layout struct using ADR-36 scheme (JS compatible).
	indexSignatureFormat, _, err := cascadekit.CreateSignaturesWithKeyringADR36(
		layout,
		c.keyring,
		c.config.Account.KeyName,
		ic,
		max,
	)
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("create signatures: %w", err)
	}

	// Compute data hash (blake3) as base64 using a streaming file hash to avoid loading entire file
	h, err := utils.Blake3HashFile(filePath)
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("hash data: %w", err)
	}
	dataHashB64 := base64.StdEncoding.EncodeToString(h)

	// Derive file name from path
	fileName := filepath.Base(filePath)

	// Build metadata proto
	meta := cascadekit.NewCascadeMetadata(dataHashB64, fileName, uint64(ic), indexSignatureFormat, public)

	// Fetch params (already fetched) to get denom and expiration duration
	denom := paramsResp.Params.BaseActionFee.Denom
	exp := paramsResp.Params.ExpirationDuration

	// Compute data size in KB for fee, rounding up to avoid underpaying
	sizeBytes := fi.Size()
	kb := (sizeBytes + 1023) / 1024 // int64 division
	feeResp, err := c.lumeraClient.GetActionFee(ctx, strconv.FormatInt(kb, 10))
	if err != nil {
		return actiontypes.CascadeMetadata{}, "", "", fmt.Errorf("get action fee: %w", err)
	}
	price := feeResp.Amount + denom

	// Expiration: now + chain duration + 1h buffer (to avoid off-by-margin rejections)
	expirationUnix := time.Now().Add(exp).Add(1 * time.Hour).Unix()
	expirationTime := fmt.Sprintf("%d", expirationUnix)

	return meta, price, expirationTime, nil
}

// GenerateStartCascadeSignatureFromFileDeprecated computes blake3(file) and signs it with the configured key.
// Returns base64-encoded signature suitable for StartCascade.
func (c *ClientImpl) GenerateStartCascadeSignatureFromFileDeprecated(ctx context.Context, filePath string) (string, error) {
	// Compute blake3(file), encode as base64 string, and sign the string bytes
	h, err := utils.Blake3HashFile(filePath)
	if err != nil {
		return "", fmt.Errorf("blake3: %w", err)
	}
	dataHashB64 := base64.StdEncoding.EncodeToString(h)
	sig, err := keyringpkg.SignBytes(c.keyring, c.config.Account.KeyName, []byte(dataHashB64))
	if err != nil {
		return "", fmt.Errorf("sign hash string: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// GenerateStartCascadeSignatureFromFile computes blake3(file) and signs it with the configured key
// using the ADR-36 scheme, matching Keplr's signArbitrary(dataHash) behavior.
// Returns base64-encoded signature suitable for StartCascade.
func (c *ClientImpl) GenerateStartCascadeSignatureFromFile(ctx context.Context, filePath string) (string, error) {
	// Compute blake3(file), encode as base64 string
	h, err := utils.Blake3HashFile(filePath)
	if err != nil {
		return "", fmt.Errorf("blake3: %w", err)
	}
	dataHashB64 := base64.StdEncoding.EncodeToString(h)

	// Sign the dataHashB64 string using ADR-36 (same as JS / Keplr).
	sigB64, err := cascadekit.SignADR36String(
		c.keyring,
		c.config.Account.KeyName,
		c.signerAddr, // bech32 address resolved in NewClient
		dataHashB64,
	)
	if err != nil {
		return "", fmt.Errorf("sign adr36 hash string: %w", err)
	}

	return sigB64, nil
}

// GenerateDownloadSignature signs the payload "actionID" and returns base64 signature.
func (c *ClientImpl) GenerateDownloadSignature(ctx context.Context, actionID, creatorAddr string) (string, error) {
	if actionID == "" {
		return "", fmt.Errorf("actionID is empty")
	}
	if creatorAddr == "" {
		return "", fmt.Errorf("creator address is empty")
	}
	// Sign only the actionID; creatorAddr is provided but not included in payload.
	sig, err := keyringpkg.SignBytes(c.keyring, c.config.Account.KeyName, []byte(actionID))
	if err != nil {
		return "", fmt.Errorf("sign download payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}
