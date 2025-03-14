package raptorq

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"strconv"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/cosmos/btcutil/base58"
)

const (
	InputEncodeFileName      = "input.data"
	SeparatorByte       byte = 46 // separator in dd_and_fingerprints.signature i.e. '.'
)

// EncoderParameters represents the encoding params used by raptorq services
type EncoderParameters struct {
	Oti []byte
}

// EncodeInfo represents the response returns by encodeInfo method
type EncodeInfo struct {
	SymbolIDFiles map[string]RawSymbolIDFile
	EncoderParam  EncoderParameters
}

// Encode represents the response returns by Encode method
type Encode struct {
	Symbols      map[string][]byte
	EncoderParam EncoderParameters
}

// Decode represents the response returns by Decode method
type Decode struct {
	File []byte
}

func (s *raptorQServerClient) encodeInfo(ctx context.Context, data []byte, copies uint32, blockHash string, pastelID string) (*EncodeInfo, error) {
	s.semaphore <- struct{}{} // Acquire slot
	defer func() {
		<-s.semaphore // Release the semaphore slot
	}()

	if data == nil {
		return nil, errors.Errorf("invalid data")
	}

	_, inputPath, err := createInputEncodeFile(s.config.RqFilesDir, data)
	if err != nil {
		return nil, errors.Errorf("create input file: %w", err)
	}
	res, err := s.EncodeMetaData(ctx, EncodeMetadataRequest{
		FilesNumber: copies,
		BlockHash:   blockHash,
		PastelId:    pastelID,
		Path:        inputPath,
	})
	if err != nil {
		return nil, errors.Errorf("encode metadata %s: %w", res.Path, err)
	}

	filesMap, err := scanSymbolIDFiles(res.Path)
	if err != nil {
		return nil, errors.Errorf("scan symbol id files folder %s: %w", res.Path, err)
	}

	if len(filesMap) != int(copies) {
		return nil, errors.Errorf("symbol id files count not match: expect %d, output %d", copies, len(filesMap))
	}

	output := &EncodeInfo{
		SymbolIDFiles: filesMap,
		EncoderParam: EncoderParameters{
			Oti: res.EncoderParameters,
		},
	}

	if err := os.Remove(inputPath); err != nil {
		logtrace.Error(ctx, "encode info: error removing input file", logtrace.Fields{"Path": inputPath})
	}

	return output, nil
}

func (s *raptorQServerClient) generateRQIDs(ctx context.Context, rawFile RawSymbolIDFile, snAccAddress string, maxFiles uint32) (RQIDsIc uint32, RQIDs []string, RQIDsFile []byte, signature []byte, err error) {
	rqIDsfile, err := json.Marshal(rawFile)
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFile, signature, errors.Errorf("marshal rqID file")
	}

	// FIXME : msgs param
	signature, err = s.lumeraClient.Node().Sign(snAccAddress, rqIDsfile) // FIXME : confirm the data
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFile, signature, errors.Errorf("sign identifiers file: %w", err)
	}

	encRqIDsfile := utils.B64Encode(rqIDsfile)

	var buffer bytes.Buffer
	buffer.Write(encRqIDsfile)
	buffer.WriteString(".")
	buffer.Write(signature)
	rqIDFile := buffer.Bytes()

	RQIDsIc = rand.Uint32()
	RQIDs, _, err = GetIDFiles(ctx, rqIDFile, RQIDsIc, maxFiles)
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFile, signature, errors.Errorf("get ID Files: %w", err)
	}

	comp, err := utils.HighCompress(ctx, rqIDFile)
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFile, signature, errors.Errorf("compress: %w", err)
	}
	RQIDsFile = utils.B64Encode(comp)

	return RQIDsIc, RQIDs, RQIDsFile, signature, nil
}

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

		hash, err := utils.Sha3256hash(compressedData)
		if err != nil {
			return ids, idFiles, errors.Errorf("sha3-256-hash error getting an id file: %w", err)
		}

		ids = append(ids, base58.Encode(hash))
	}

	return ids, idFiles, nil
}
