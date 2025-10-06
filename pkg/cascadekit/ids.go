package cascadekit

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/btcutil/base58"
)

// GenerateLayoutIDs computes IDs for redundant layout files (not the final index IDs).
// The ID is base58(blake3(zstd(layout_b64.layout_sig_b64.counter))).
func GenerateLayoutIDs(layoutB64, layoutSigB64 string, ic, max uint32) []string {
	layoutWithSig := fmt.Sprintf("%s.%s", layoutB64, layoutSigB64)
	layoutIDs := make([]string, max)

	var buffer bytes.Buffer
	buffer.Grow(len(layoutWithSig) + 10)

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		buffer.WriteString(layoutWithSig)
		buffer.WriteByte('.')
		buffer.WriteString(fmt.Sprintf("%d", ic+i))

		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			continue
		}

		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			continue
		}

		layoutIDs[i] = base58.Encode(hash)
	}

	return layoutIDs
}

// GenerateIndexIDs computes IDs for index files from the full signatures string.
func GenerateIndexIDs(signatures string, ic, max uint32) []string {
	indexFileIDs := make([]string, max)

	var buffer bytes.Buffer
	buffer.Grow(len(signatures) + 10)

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		buffer.WriteString(signatures)
		buffer.WriteByte('.')
		buffer.WriteString(fmt.Sprintf("%d", ic+i))

		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			continue
		}
		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			continue
		}
		indexFileIDs[i] = base58.Encode(hash)
	}
	return indexFileIDs
}

// getIDFiles generates ID files by appending a '.' and counter, compressing,
// and returning both IDs and compressed payloads.
func getIDFiles(file []byte, ic uint32, max uint32) (ids []string, files [][]byte, err error) {
	idFiles := make([][]byte, 0, max)
	ids = make([]string, 0, max)
	var buffer bytes.Buffer

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		counter := ic + i

		buffer.Write(file)
		buffer.WriteByte(SeparatorByte)
		buffer.WriteString(strconv.Itoa(int(counter)))

		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			return ids, idFiles, errors.Errorf("compress identifiers file: %w", err)
		}

		idFiles = append(idFiles, compressedData)

		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			return ids, idFiles, errors.Errorf("sha3-256-hash error getting an id file: %w", err)
		}

		ids = append(ids, base58.Encode(hash))
	}

	return ids, idFiles, nil
}
