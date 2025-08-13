package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
)

const maxFileSize = 1 * 1024 * 1024 * 1024 // 1GB limit

// ValidateFileSize checks if a file size is within the allowed 1GB limit
func ValidateFileSize(filePath string) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to check file: %w", err)
	}

	if fileInfo.Size() > maxFileSize {
		return fmt.Errorf("file size %d bytes exceeds maximum allowed size of 1GB", fileInfo.Size())
	}

	return nil
}

func (m *ManagerImpl) validateAction(ctx context.Context, actionID string) (lumera.Action, error) {
	action, err := m.lumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return lumera.Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Check if action exists
	if action.ID == "" {
		return lumera.Action{}, fmt.Errorf("no action found with the specified ID")
	}

	// Check action state
	if action.State != lumera.ACTION_STATE_PENDING {
		return lumera.Action{}, fmt.Errorf("action is in %s state, expected PENDING", action.State)
	}

	return action, nil
}

// validateSignature verifies the authenticity of a signature against an action's data hash.
//
// This function performs the following steps:
// 1. Decodes the CASCADE metadata from the provided Lumera action
// 2. Extracts the base64-encoded data hash from the metadata
// 3. Decodes both the data hash and the provided signature from base64 format
// 4. Verifies the signature against the data hash using the Lumera client
//
// Parameters:
//   - ctx: Context for the operation, used for cancellation and tracing
//   - action: The Lumera action object containing CASCADE metadata with the data hash
//   - signature: Base64-encoded signature string to verify
//
// Returns:
//   - nil if the signature is valid
//   - An error if any step fails, including metadata decoding issues,
//     base64 decoding problems, or if the signature is invalid
//
// The signature is expected to be produced by the creator of the action,
// and the verification uses the creator's public key to validate the signature.
func (m *ManagerImpl) validateSignature(ctx context.Context, action lumera.Action, signature string) error {
	// Decode the CASCADE metadata to access the data hash
	cascadeMetaData, err := m.lumeraClient.DecodeCascadeMetadata(ctx, action)
	if err != nil {
		return fmt.Errorf("failed to decode cascade metadata: %w", err)
	}

	// Extract the base64-encoded data hash from the metadata
	base64EnTcketDataHash := cascadeMetaData.DataHash

	// Decode the data hash from base64 to raw bytes
	dataHashBytes, err := base64.StdEncoding.DecodeString(base64EnTcketDataHash)
	if err != nil {
		return fmt.Errorf("failed to decode data hash: %w", err)
	}

	// Decode the provided signature from base64 to raw bytes
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify the signature using the Lumera client
	// This checks if the signature was produced by the action creator
	// for the given data hash
	err = m.lumeraClient.VerifySignature(ctx, action.Creator, dataHashBytes, signatureBytes)
	if err != nil {
		m.logger.Error(ctx, "Signature validation failed", "actionID", action.ID, "error", err)
		return fmt.Errorf("signature validation failed: %w", err)
	}

	return nil
}

func (m *ManagerImpl) validateDownloadAction(ctx context.Context, actionID string) (lumera.Action, error) {
	action, err := m.lumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return lumera.Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Check if action exists
	if action.ID == "" {
		return lumera.Action{}, fmt.Errorf("no action found with the specified ID")
	}

	// Check action state
	if action.State != lumera.ACTION_STATE_DONE {
		return lumera.Action{}, fmt.Errorf("action is in %s state, expected DONE", action.State)
	}

	return action, nil
}

// Helper function to ensure output path has the correct filename
func ensureOutputPathWithFilename(outputPath, filename string) string {
	// If outputPath is empty, just return the filename
	if outputPath == "" {
		return filename
	}

	// Check if the path already ends with the filename
	if strings.HasSuffix(outputPath, filename) {
		return outputPath
	}

	// Otherwise, append the filename to the path
	return filepath.Join(outputPath, filename)
}
