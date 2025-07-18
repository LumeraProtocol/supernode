package crypto

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"lukechampine.com/blake3"
)

func TestHashFileIncrementally(t *testing.T) {
	expectedBlake3 := func(data []byte) string {
		h := blake3.New(32, nil)
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}

	testData := []byte("hello world")
	emptyData := []byte("")
	largeData := make([]byte, 5*1024*1024)

	// Temp dir for test files
	tmpDir := t.TempDir()

	// Create helper function for file creation
	createTempFile := func(name string, content []byte) string {
		filePath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		return filePath
	}

	// Create test files
	smallFile := createTempFile("small.txt", testData)
	emptyFile := createTempFile("empty.txt", emptyData)
	largeFile := createTempFile("large.bin", largeData)

	tests := []struct {
		name       string
		filePath   string
		bufferSize int
		wantHash   string
		wantErr    bool
	}{
		{
			name:       "small file",
			filePath:   smallFile,
			bufferSize: 4 * 1024, // 4KB buffer
			wantHash:   expectedBlake3(testData),
			wantErr:    false,
		},
		{
			name:       "empty file",
			filePath:   emptyFile,
			bufferSize: 1024, // small buffer
			wantHash:   expectedBlake3(emptyData),
			wantErr:    false,
		},
		{
			name:       "large file",
			filePath:   largeFile,
			bufferSize: 1024 * 1024, // 1MB buffer
			wantHash:   expectedBlake3(largeData),
			wantErr:    false,
		},
		{
			name:       "file does not exist",
			filePath:   filepath.Join(tmpDir, "doesnotexist.txt"),
			bufferSize: 4096,
			wantHash:   "",
			wantErr:    true,
		},
		{
			name:       "zero buffer size (should use default)",
			filePath:   smallFile,
			bufferSize: 0,
			wantHash:   expectedBlake3(testData),
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHash, err := HashFileIncrementally(tt.filePath, tt.bufferSize)

			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error=%v, got err=%v", tt.wantErr, err)
			}

			if !tt.wantErr && hex.EncodeToString(gotHash) != tt.wantHash {
				t.Errorf("hash mismatch!\n got:  %s\n want: %s", gotHash, tt.wantHash)
			}
		})
	}
}
