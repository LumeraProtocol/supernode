package cascadekit

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

// GenerateLayoutFilesFromB64 builds redundant metadata files using a precomputed
// base64(JSON(layout)) and the layout signature, avoiding an extra JSON marshal.
// The content is: base64(JSON(layout)).layout_signature
func GenerateLayoutFilesFromB64(layoutB64 []byte, layoutSigB64 string, ic uint32, max uint32) (ids []string, files [][]byte, err error) {
	enc := make([]byte, 0, len(layoutB64)+1+len(layoutSigB64))
	enc = append(enc, layoutB64...)
	enc = append(enc, SeparatorByte)
	enc = append(enc, []byte(layoutSigB64)...)
	return generateIDFiles(enc, ic, max)
}

// GenerateIndexFiles generates index files and their IDs from the full index signature format.
func GenerateIndexFiles(indexSignatureFormat string, ic uint32, max uint32) (indexIDs []string, indexFiles [][]byte, err error) {
	// Use the full index signature format that matches what was sent during RequestAction
	// The chain expects this exact format for ID generation
	indexIDs, indexFiles, err = generateIDFiles([]byte(indexSignatureFormat), ic, max)
	if err != nil {
		return nil, nil, errors.Errorf("get index ID files: %w", err)
	}
	return indexIDs, indexFiles, nil
}
