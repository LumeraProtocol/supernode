package crypto

import (
	"fmt"
	"io"
	"lukechampine.com/blake3"
	"os"
)

const defaultHashBufferSize = 1024 * 1024 // 1 MB

func HashFileIncrementally(filePath string, bufferSize int) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open decoded file: %w", err)
	}
	defer f.Close()

	if bufferSize == 0 {
		bufferSize = defaultHashBufferSize
	}

	hasher := blake3.New(32, nil)
	buf := make([]byte, bufferSize) // 4MB buffer to balance memory vs I/O

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("streaming file read failed: %w", readErr)
		}
	}

	return hasher.Sum(nil), nil
}
