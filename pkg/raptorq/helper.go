package raptorq

import (
	"bytes"
	"context"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/btcutil/base58"
	"strconv"
)

const (
	InputEncodeFileName      = "input.data"
	SeparatorByte       byte = 46 // separator in dd_and_fingerprints.signature i.e. '.'
)

// GetIDFiles generates ID Files for dd_and_fingerprints files and rq_id files
// file is b64 encoded file appended with signatures and compressed, ic is the initial counter
// and max is the number of ids to generate
func GetIDFiles(ctx context.Context, file []byte, ic uint32, max uint32) (ids []string, files [][]byte, err error) {
	idFiles := make([][]byte, 0, max)
	ids = make([]string, 0, max)
	var buffer bytes.Buffer

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		counter := ic + i

		buffer.Write(file)
		buffer.WriteByte(SeparatorByte)
		buffer.WriteString(strconv.Itoa(int(counter))) // Using the string representation to maintain backward compatibility

		compressedData, err := utils.HighCompress(ctx, buffer.Bytes()) // Ensure you're using the same compression level
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
