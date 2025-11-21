package cascadekit

import (
	"bytes"
	"strconv"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/btcutil/base58"
	"github.com/DataDog/zstd"
)

// GenerateLayoutIDs computes IDs for redundant layout files (not the final index IDs).
// The ID is base58(blake3(zstd(layout_signature_format.counter))).
// layoutSignatureFormat must be: base64(JSON(layout)).layout_signature_base64
func GenerateLayoutIDs(layoutSignatureFormat string, ic, max uint32) ([]string, error) {
	return generateIDs([]byte(layoutSignatureFormat), ic, max)
}

// GenerateIndexIDs computes IDs for index files from the full index signature format string.
func GenerateIndexIDs(indexSignatureFormat string, ic, max uint32) ([]string, error) {
	return generateIDs([]byte(indexSignatureFormat), ic, max)
}

// getIDFiles generates ID files by appending a '.' and counter, compressing,
// and returning both IDs and compressed payloads.
// generateIDFiles builds compressed ID files from a base payload and returns
// both their content-addressed IDs and the compressed files themselves.
// For each counter in [ic..ic+max-1], the payload is:
//
//	base + '.' + counter
//
// then zstd-compressed; the ID is base58(blake3(compressed)).
func generateIDFiles(base []byte, ic uint32, max uint32) (ids []string, files [][]byte, err error) {
	idFiles := make([][]byte, 0, max)
	ids = make([]string, 0, max)
	var buffer bytes.Buffer

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		counter := ic + i

		buffer.Write(base)
		buffer.WriteByte(SeparatorByte)
		// Append counter efficiently without intermediate string
		var tmp [20]byte
		cnt := strconv.AppendUint(tmp[:0], uint64(counter), 10)
		buffer.Write(cnt)

		// Compress with official zstd C library at level 3 (matches SDK-JS)
		compressedData, zerr := zstd.CompressLevel(nil, buffer.Bytes(), 3)
		if zerr != nil {
			return ids, idFiles, errors.Errorf("compress identifiers file: %w", zerr)
		}

		idFiles = append(idFiles, compressedData)

		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			return ids, idFiles, errors.Errorf("blake3 hash error getting an id file: %w", err)
		}

		ids = append(ids, base58.Encode(hash))
	}

	return ids, idFiles, nil
}

// generateIDs computes base58(blake3(zstd(base + '.' + counter))) for counters ic..ic+max-1.
// Uses official zstd C library at level 3 to match SDK-JS compression.
func generateIDs(base []byte, ic, max uint32) ([]string, error) {
	ids := make([]string, max)

	var buffer bytes.Buffer
	// Reserve base length + dot + up to 10 digits
	buffer.Grow(len(base) + 12)

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		buffer.Write(base)
		buffer.WriteByte(SeparatorByte)
		var tmp [20]byte
		cnt := strconv.AppendUint(tmp[:0], uint64(ic+i), 10)
		buffer.Write(cnt)

		// Compress with official zstd C library at level 3 (matches SDK-JS)
		compressed, err := zstd.CompressLevel(nil, buffer.Bytes(), 3)
		if err != nil {
			return nil, errors.Errorf("zstd compress (i=%d): %w", i, err)
		}
		h, err := utils.Blake3Hash(compressed)
		if err != nil {
			return nil, errors.Errorf("blake3 hash (i=%d): %w", i, err)
		}
		ids[i] = base58.Encode(h)
	}
	return ids, nil
}
