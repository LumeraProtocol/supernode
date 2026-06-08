package cascade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StagedHealOpInfo is the public projection of stagedManifest used by the
// LEP-6 §19 healer-served transport (supernode/transport/grpc/self_healing).
type StagedHealOpInfo struct {
	ActionID              string
	ReconstructedFilePath string
	ManifestHashB64       string
}

// ReadStagedHealOp loads the manifest from a heal-op staging directory and
// returns the absolute reconstructed-file path the §19 transport streams to
// verifiers, plus the manifest hash for cross-checks. Returns os.ErrNotExist
// (wrapped) when the staging dir or its manifest is missing — caller may
// treat that as "not yet staged" and respond NotFound to the gRPC client.
func ReadStagedHealOp(stagingDir string) (StagedHealOpInfo, error) {
	manifestPath := filepath.Join(stagingDir, stagedManifestFilename)
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return StagedHealOpInfo{}, fmt.Errorf("read staged manifest %q: %w", manifestPath, err)
	}
	var m stagedManifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return StagedHealOpInfo{}, fmt.Errorf("parse staged manifest %q: %w", manifestPath, err)
	}
	rel := m.ReconstructedRel
	if rel == "" {
		rel = stagedReconstructedFilename
	}
	return StagedHealOpInfo{
		ActionID:              m.ActionID,
		ReconstructedFilePath: filepath.Join(stagingDir, rel),
		ManifestHashB64:       m.ManifestHashB64,
	}, nil
}
