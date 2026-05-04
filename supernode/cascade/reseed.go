package cascade

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// RecoveryReseedRequest carries the inputs for an end-to-end LEP-6 heal
// reconstruction. When PersistArtifacts is true (legacy / register-equivalent
// behavior) the rebuilt artefacts are stored to KAD via the same store path
// register/upload uses. When PersistArtifacts is false (LEP-6 §19 healer-served
// path) the artefacts are STAGED to StagingDir and not published; a later
// PublishStagedArtefacts call performs the KAD store after chain VERIFIED.
type RecoveryReseedRequest struct {
	ActionID         string
	PersistArtifacts bool   // false = stage only (LEP-6 default); true = publish to KAD
	StagingDir       string // required when PersistArtifacts=false
}

type RecoveryReseedResult struct {
	ActionID             string
	DownloadEvents       int
	DownloadLastEvent    string
	DecodeCleanupError   string
	RQIC                 uint64
	RQMax                uint64
	DataHashVerified     bool
	IndexIDs             []string
	LayoutIDs            []string
	SymbolKeys           []string
	IndexFilesGenerated  int
	LayoutFilesGenerated int
	IDFilesGenerated     int
	SymbolsGenerated     int
	// StagingDir is set when artefacts were staged rather than published.
	StagingDir string
	// ReconstructedFilePath is the local path of the decoded original file.
	// Caller is responsible for cleanup; on staged paths this is informational.
	ReconstructedFilePath string
	// ReconstructedHashB64 is the base64-encoded BLAKE3 of the reconstructed
	// file (= action.DataHash recipe; LEP-6 HealManifestHash).
	ReconstructedHashB64 string
}

// stagedManifest is the on-disk descriptor written into a heal-op staging dir
// so a later PublishStagedArtefacts() call can reconstruct the storeArtefacts
// inputs without re-running download/decode/encode.
type stagedManifest struct {
	ActionID         string       `json:"action_id"`
	Layout           codec.Layout `json:"layout"`
	IDFiles          []string     `json:"id_files"`         // base64 of idFile bytes
	SymbolKeys       []string     `json:"symbol_keys"`      // ordered, deduped
	SymbolsDir       string       `json:"symbols_dir"`      // absolute path inside StagingDir/symbols
	ReconstructedRel string       `json:"reconstructed_rel"`// staging-dir-relative path of the reconstructed file
	ManifestHashB64  string       `json:"manifest_hash_b64"`// = action.DataHash recipe; HealManifestHash
}

const stagedManifestFilename = "manifest.json"
const stagedSymbolsDirname = "symbols"
const stagedIDFilesDirname = "id_files"
const stagedReconstructedFilename = "reconstructed.bin"

// RecoveryReseed decodes an existing action, re-encodes the reconstructed file,
// regenerates RQ artefacts with the action's original RQ params, and either
// stages them to disk (LEP-6 healer flow, PersistArtifacts=false) or stores
// them via the same store path used by register (legacy / republish flow,
// PersistArtifacts=true).
//
// LEP-6 §19 mandates the healer-served path: heal-op artefacts MUST NOT enter
// KAD until the chain has reached VERIFIED quorum, otherwise verifiers could
// fetch from KAD before the healer's hash is attested. PR-4 finalizer calls
// PublishStagedArtefacts only after observing op.Status == VERIFIED.
func (task *CascadeRegistrationTask) RecoveryReseed(ctx context.Context, req *RecoveryReseedRequest) (*RecoveryReseedResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing request")
	}
	actionID := strings.TrimSpace(req.ActionID)
	if actionID == "" {
		return nil, fmt.Errorf("missing action_id")
	}
	if !req.PersistArtifacts && strings.TrimSpace(req.StagingDir) == "" {
		return nil, fmt.Errorf("staging_dir required when persist_artifacts=false")
	}

	task.taskID = actionID
	fields := logtrace.Fields{logtrace.FieldMethod: "RecoveryReseed", logtrace.FieldActionID: actionID, "persist_artifacts": req.PersistArtifacts}

	action, err := task.fetchAction(ctx, actionID, fields)
	if err != nil {
		return nil, err
	}
	meta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, fields)
	}

	result := &RecoveryReseedResult{
		ActionID: actionID,
		RQIC:     meta.RqIdsIc,
		RQMax:    meta.RqIdsMax,
	}

	var decodeFilePath string
	var decodeTmpDir string

	dlErr := task.Download(ctx, &DownloadRequest{
		ActionID:               actionID,
		BypassPrivateSignature: true,
	}, func(resp *DownloadResponse) error {
		result.DownloadEvents++
		result.DownloadLastEvent = fmt.Sprintf("%d:%s", resp.EventType, resp.Message)
		if resp.EventType == SupernodeEventTypeDecodeCompleted {
			decodeFilePath = strings.TrimSpace(resp.FilePath)
			decodeTmpDir = strings.TrimSpace(resp.DownloadedDir)
		} else if decodeFilePath == "" && strings.TrimSpace(resp.FilePath) != "" {
			decodeFilePath = strings.TrimSpace(resp.FilePath)
		}
		return nil
	})
	if dlErr != nil {
		if decodeTmpDir != "" {
			if cerr := task.CleanupDownload(ctx, decodeTmpDir); cerr != nil {
				result.DecodeCleanupError = cerr.Error()
			}
		}
		return result, dlErr
	}
	if decodeFilePath == "" {
		return result, task.wrapErr(ctx, "recovery decode did not produce a file", fmt.Errorf("missing decoded file path"), fields)
	}
	if decodeTmpDir != "" {
		defer func() {
			if cerr := task.CleanupDownload(ctx, decodeTmpDir); cerr != nil {
				result.DecodeCleanupError = cerr.Error()
				logtrace.Warn(ctx, "recovery reseed decode cleanup failed", logtrace.Fields{
					logtrace.FieldActionID: actionID,
					logtrace.FieldError:    cerr.Error(),
					"tmp_dir":              decodeTmpDir,
				})
			}
		}()
	}

	fileHash, err := utils.Blake3HashFile(decodeFilePath)
	if err != nil {
		return result, task.wrapErr(ctx, "failed to hash decoded file", err, fields)
	}
	if fileHash == nil {
		return result, task.wrapErr(ctx, "failed to hash decoded file", fmt.Errorf("file hash is nil"), fields)
	}
	if err := cascadekit.VerifyB64DataHash(fileHash, meta.DataHash); err != nil {
		return result, task.wrapErr(ctx, "decoded file hash does not match action metadata", err, fields)
	}
	result.DataHashVerified = true
	result.ReconstructedFilePath = decodeFilePath
	// HealManifestHash = base64(BLAKE3(reconstructed_file)) — same recipe as
	// Action.DataHash (cascadekit.ComputeBlake3DataHashB64). meta.DataHash is
	// already that exact string, and VerifyB64DataHash above proved equality.
	result.ReconstructedHashB64 = strings.TrimSpace(meta.DataHash)

	encodeResult, err := task.encodeInput(ctx, actionID, decodeFilePath, fields)
	if err != nil {
		return result, err
	}
	indexFile, layoutB64, err := task.validateIndexAndLayout(ctx, action.Creator, action.ActionID, action.AppPubkey, meta.Signatures, encodeResult.Layout)
	if err != nil {
		return result, task.wrapErr(ctx, "signature or index validation failed", err, fields)
	}
	indexIDs, layoutIDs, idFiles, err := task.generateRQIDFilesDetailed(ctx, meta, indexFile.LayoutSignature, layoutB64, fields)
	if err != nil {
		return result, err
	}

	if req.PersistArtifacts {
		if err := task.storeArtefacts(ctx, action.ActionID, idFiles, encodeResult.SymbolsDir, encodeResult.Layout, fields); err != nil {
			return result, err
		}
	} else {
		if err := task.stageArtefacts(ctx, req.StagingDir, action.ActionID, idFiles, encodeResult.SymbolsDir, encodeResult.Layout, decodeFilePath, result.ReconstructedHashB64, fields); err != nil {
			return result, err
		}
		result.StagingDir = req.StagingDir
	}

	result.IndexIDs = indexIDs
	result.LayoutIDs = layoutIDs
	result.SymbolKeys = symbolIDsFromLayout(encodeResult.Layout)
	result.IndexFilesGenerated = len(indexIDs)
	result.LayoutFilesGenerated = len(layoutIDs)
	result.IDFilesGenerated = len(idFiles)
	result.SymbolsGenerated = len(result.SymbolKeys)

	return result, nil
}

// stageArtefacts copies the encoded symbols + idFiles + layout + the
// reconstructed file into stagingDir, writing a manifest the finalizer reads
// when publishing and the §19 transport reads when serving verifiers.
// stagingDir is the per-heal-op directory (e.g.
// ~/.supernode/heal-staging/<heal_op_id>/).
func (task *CascadeRegistrationTask) stageArtefacts(ctx context.Context, stagingDir, actionID string, idFiles [][]byte, symbolsDir string, layout codec.Layout, reconstructedFilePath, manifestHashB64 string, f logtrace.Fields) error {
	if f == nil {
		f = logtrace.Fields{}
	}
	lf := logtrace.Fields{logtrace.FieldActionID: actionID, logtrace.FieldTaskID: task.taskID, "staging_dir": stagingDir, "id_files_count": len(idFiles)}
	for k, v := range f {
		lf[k] = v
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return task.wrapErr(ctx, "failed to create staging dir", err, lf)
	}
	stagedSymbols := filepath.Join(stagingDir, stagedSymbolsDirname)
	if err := os.MkdirAll(stagedSymbols, 0o700); err != nil {
		return task.wrapErr(ctx, "failed to create staged symbols dir", err, lf)
	}
	if err := copyDirContents(symbolsDir, stagedSymbols); err != nil {
		return task.wrapErr(ctx, "failed to copy symbols into staging dir", err, lf)
	}
	stagedIDDir := filepath.Join(stagingDir, stagedIDFilesDirname)
	if err := os.MkdirAll(stagedIDDir, 0o700); err != nil {
		return task.wrapErr(ctx, "failed to create staged id_files dir", err, lf)
	}
	idFilesEncoded := make([]string, 0, len(idFiles))
	for i, b := range idFiles {
		// Persist raw bytes for fidelity; encode to base64 in manifest for
		// portability across filesystems / observation.
		path := filepath.Join(stagedIDDir, fmt.Sprintf("idfile_%05d.bin", i))
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return task.wrapErr(ctx, "failed to write staged id file", err, lf)
		}
		idFilesEncoded = append(idFilesEncoded, base64.StdEncoding.EncodeToString(b))
	}
	manifest := stagedManifest{
		ActionID:         actionID,
		Layout:           layout,
		IDFiles:          idFilesEncoded,
		SymbolKeys:       symbolIDsFromLayout(layout),
		SymbolsDir:       stagedSymbols,
		ReconstructedRel: stagedReconstructedFilename,
		ManifestHashB64:  manifestHashB64,
	}
	// Stage the reconstructed file bytes so the §19 healer-served-path
	// transport can stream them to verifiers without re-running download +
	// decode.
	if strings.TrimSpace(reconstructedFilePath) != "" {
		src, err := os.ReadFile(reconstructedFilePath)
		if err != nil {
			return task.wrapErr(ctx, "failed to read reconstructed file for staging", err, lf)
		}
		if err := os.WriteFile(filepath.Join(stagingDir, stagedReconstructedFilename), src, 0o600); err != nil {
			return task.wrapErr(ctx, "failed to stage reconstructed file", err, lf)
		}
	}
	manifestPath := filepath.Join(stagingDir, stagedManifestFilename)
	mb, err := json.Marshal(manifest)
	if err != nil {
		return task.wrapErr(ctx, "failed to marshal staged manifest", err, lf)
	}
	if err := os.WriteFile(manifestPath, mb, 0o600); err != nil {
		return task.wrapErr(ctx, "failed to write staged manifest", err, lf)
	}
	logtrace.Info(ctx, "stage: artefacts staged", lf)
	return nil
}

// PublishStagedArtefacts reads a stagingDir produced by stageArtefacts and
// performs the KAD store via the same store path register/upload uses. Called
// by the LEP-6 finalizer after the chain reports HealOp.Status == VERIFIED.
func (task *CascadeRegistrationTask) PublishStagedArtefacts(ctx context.Context, stagingDir string) error {
	stagingDir = strings.TrimSpace(stagingDir)
	if stagingDir == "" {
		return fmt.Errorf("missing staging_dir")
	}
	manifestPath := filepath.Join(stagingDir, stagedManifestFilename)
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read staged manifest: %w", err)
	}
	var manifest stagedManifest
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return fmt.Errorf("parse staged manifest: %w", err)
	}
	idFiles := make([][]byte, 0, len(manifest.IDFiles))
	for i, enc := range manifest.IDFiles {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return fmt.Errorf("decode id_file[%d]: %w", i, err)
		}
		idFiles = append(idFiles, b)
	}
	task.taskID = manifest.ActionID
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "PublishStagedArtefacts",
		logtrace.FieldActionID: manifest.ActionID,
		"staging_dir":          stagingDir,
	}
	return task.storeArtefacts(ctx, manifest.ActionID, idFiles, manifest.SymbolsDir, manifest.Layout, fields)
}

func symbolIDsFromLayout(layout codec.Layout) []string {
	seen := make(map[string]struct{}, 1024)
	for _, block := range layout.Blocks {
		for _, symbolID := range block.Symbols {
			symbolID = strings.TrimSpace(symbolID)
			if symbolID == "" {
				continue
			}
			seen[symbolID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for symbolID := range seen {
		out = append(out, symbolID)
	}
	sort.Strings(out)
	return out
}

func copyDirContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			// symbols layout is flat; recurse defensively
			if err := os.MkdirAll(filepath.Join(dstDir, e.Name()), 0o700); err != nil {
				return err
			}
			if err := copyDirContents(filepath.Join(srcDir, e.Name()), filepath.Join(dstDir, e.Name())); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dstDir, e.Name()), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}
