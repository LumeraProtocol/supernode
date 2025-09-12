package cascade

import (
	"fmt"
	"lukechampine.com/blake3"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

func initializeHasherAndTempFile() (*blake3.Hasher, *os.File, string, error) {
    hasher := blake3.New(32, nil)

    // Create a unique temp file to avoid collisions across concurrent calls
    tempFile, err := os.CreateTemp("", "cascade-upload-*")
    if err != nil {
        return nil, nil, "", fmt.Errorf("could not create temp file: %w", err)
    }

    return hasher, tempFile, tempFile.Name(), nil
}

func replaceTempDirWithTaskDir(taskID, tempFilePath string, tempFile *os.File) (targetPath string, err error) {
	if err := tempFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	targetDir := filepath.Join(os.TempDir(), taskID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("could not create task directory: %w", err)
	}
	targetPath = filepath.Join(targetDir, fmt.Sprintf("uploaded-%s.dat", taskID))
	if err := os.Rename(tempFilePath, targetPath); err != nil {
		return "", fmt.Errorf("could not move file to final location: %w", err)
	}

	return targetPath, nil
}
