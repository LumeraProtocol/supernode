// Package storagechallenge contains the supernode-side off-chain glue for the
// LEP-6 compound storage challenge runtime. The deterministic primitives that
// must agree byte-for-byte across reporters live in
// pkg/storagechallenge/deterministic; this file exposes the integration helpers
// that depend on cascade metadata and chain-side caps.
package storagechallenge

import (
	"errors"
	"fmt"
	"math"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
)

// MaxStorageProofResultsPerReport mirrors the chain-side cap that the audit
// keeper enforces in DeliverTx for MsgSubmitEpochReport: an epoch report
// carrying more than this many StorageProofResults is rejected wholesale.
//
// Source of truth: lumera/x/audit/v1/types/keys.go (lines 11-13) at the
// pinned chain commit. The supernode result buffer must self-throttle to this
// cap before handing results to the host reporter — see
// supernode/storage_challenge/result_buffer.go.
const MaxStorageProofResultsPerReport = 16

// ErrUnspecifiedArtifactClass is returned when a caller passes the zero/UNSPECIFIED
// StorageProofArtifactClass to a resolver that requires a concrete class.
var ErrUnspecifiedArtifactClass = errors.New("storagechallenge: artifact class is UNSPECIFIED")

// ResolveArtifactCount returns the canonical artifact count for (meta, class)
// using only the cascade metadata that finalization committed on-chain. It
// replaces a chain GetTicketArtifactCount RPC that does not exist (LEP-6 v2
// plan §9, Resolved Decision 8).
//
// Semantics:
//   - INDEX  -> uint32(meta.RqIdsIc)
//   - SYMBOL -> uint32(len(meta.RqIdsIds))
//   - UNSPECIFIED -> error
//
// If both counts are zero (legacy / malformed ticket), this returns (0, nil)
// because the chain accepts that case via its TicketArtifactCountState fallback
// path (x/audit/v1/keeper/msg_submit_epoch_report_storage_proofs.go). Callers
// decide whether to skip such a ticket.
func ResolveArtifactCount(meta *actiontypes.CascadeMetadata, class audittypes.StorageProofArtifactClass) (uint32, error) {
	if meta == nil {
		return 0, errors.New("storagechallenge: nil cascade metadata")
	}
	switch class {
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:
		return uint32(meta.RqIdsIc), nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:
		return uint32(len(meta.RqIdsIds)), nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED:
		return 0, ErrUnspecifiedArtifactClass
	default:
		return 0, fmt.Errorf("storagechallenge: unknown artifact class %v", class)
	}
}

// ResolveArtifactKey returns the deterministic artifact key (content-addressed
// identifier) for (meta, class, ordinal).
//
//   - SYMBOL: meta.RqIdsIds[ordinal] (bounds-checked).
//   - INDEX:  derived via cascadekit.GenerateIndexIDs(meta.Signatures, RqIdsIc,
//     RqIdsMax) — the same derivation the supernode cascade module uses to
//     materialise INDEX files (supernode/cascade/helper.go,
//     supernode/cascade/reseed.go). Per LEP-6 v2 plan §9 Resolved Decision 2.
//
// Returns an error on UNSPECIFIED class, ordinal out of range, or empty
// metadata for the requested class.
func ResolveArtifactKey(meta *actiontypes.CascadeMetadata, class audittypes.StorageProofArtifactClass, ordinal uint32) (string, error) {
	if meta == nil {
		return "", errors.New("storagechallenge: nil cascade metadata")
	}
	switch class {
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:
		if int(ordinal) >= len(meta.RqIdsIds) {
			return "", fmt.Errorf("storagechallenge: SYMBOL ordinal %d out of range (have %d ids)", ordinal, len(meta.RqIdsIds))
		}
		return meta.RqIdsIds[ordinal], nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:
		if meta.Signatures == "" {
			return "", errors.New("storagechallenge: INDEX key requested but cascade metadata has empty signatures")
		}
		if meta.RqIdsMax == 0 {
			return "", errors.New("storagechallenge: INDEX key requested but RqIdsMax is zero")
		}
		ids, err := cascadekit.GenerateIndexIDs(meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
		if err != nil {
			return "", fmt.Errorf("storagechallenge: derive INDEX ids: %w", err)
		}
		if int(ordinal) >= len(ids) {
			return "", fmt.Errorf("storagechallenge: INDEX ordinal %d out of range (derived %d ids)", ordinal, len(ids))
		}
		return ids[ordinal], nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED:
		return "", ErrUnspecifiedArtifactClass
	default:
		return "", fmt.Errorf("storagechallenge: unknown artifact class %v", class)
	}
}

// ResolveArtifactSize returns the exact byte size used to derive LEP-6
// multi-range offsets for a selected artifact.
//
// SYMBOL artifacts are RaptorQ symbols. The exact symbol size is derived from
// the finalized Action.FileSizeKbs and meta.RqIdsMax:
//
//	symbolSize = ceil(fileSizeKbs*1024 / meta.RqIdsMax)
//
// INDEX artifacts are generated deterministically from meta.Signatures,
// meta.RqIdsIc, and meta.RqIdsMax; their exact compressed byte length is the
// length of the selected generated index file.
func ResolveArtifactSize(act *actiontypes.Action, meta *actiontypes.CascadeMetadata, class audittypes.StorageProofArtifactClass, ordinal uint32) (uint64, error) {
	if act == nil {
		return 0, errors.New("storagechallenge: nil action")
	}
	if meta == nil {
		return 0, errors.New("storagechallenge: nil cascade metadata")
	}
	switch class {
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:
		if act.FileSizeKbs <= 0 {
			return 0, fmt.Errorf("storagechallenge: action fileSizeKbs must be > 0 for SYMBOL artifact size (got %d)", act.FileSizeKbs)
		}
		if meta.RqIdsMax <= 0 {
			return 0, errors.New("storagechallenge: RqIdsMax must be > 0 for SYMBOL artifact size")
		}
		if int(ordinal) >= len(meta.RqIdsIds) {
			return 0, fmt.Errorf("storagechallenge: SYMBOL ordinal %d out of range (have %d ids)", ordinal, len(meta.RqIdsIds))
		}
		fileBytes := uint64(act.FileSizeKbs) * 1024
		if fileBytes > math.MaxUint64-uint64(meta.RqIdsMax)+1 {
			return 0, errors.New("storagechallenge: SYMBOL artifact size overflow")
		}
		return (fileBytes + uint64(meta.RqIdsMax) - 1) / uint64(meta.RqIdsMax), nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:
		if meta.Signatures == "" {
			return 0, errors.New("storagechallenge: INDEX size requested but cascade metadata has empty signatures")
		}
		if meta.RqIdsMax == 0 {
			return 0, errors.New("storagechallenge: INDEX size requested but RqIdsMax is zero")
		}
		_, files, err := cascadekit.GenerateIndexFiles(meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
		if err != nil {
			return 0, fmt.Errorf("storagechallenge: derive INDEX files: %w", err)
		}
		if int(ordinal) >= len(files) {
			return 0, fmt.Errorf("storagechallenge: INDEX ordinal %d out of range (derived %d files)", ordinal, len(files))
		}
		return uint64(len(files[ordinal])), nil
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED:
		return 0, ErrUnspecifiedArtifactClass
	default:
		return 0, fmt.Errorf("storagechallenge: unknown artifact class %v", class)
	}
}
