package storagechallenge

import (
	"strings"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

func TestResolveArtifactCount_Index_Symbol_Unspecified(t *testing.T) {
	meta := &actiontypes.CascadeMetadata{
		RqIdsIc:  7,
		RqIdsMax: 12,
		RqIdsIds: []string{"a", "b", "c", "d"},
	}

	gotIdx, err := ResolveArtifactCount(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX)
	if err != nil {
		t.Fatalf("INDEX: unexpected error: %v", err)
	}
	if gotIdx != 7 {
		t.Fatalf("INDEX count: want 7, got %d", gotIdx)
	}

	gotSym, err := ResolveArtifactCount(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL)
	if err != nil {
		t.Fatalf("SYMBOL: unexpected error: %v", err)
	}
	if gotSym != 4 {
		t.Fatalf("SYMBOL count: want 4, got %d", gotSym)
	}

	if _, err := ResolveArtifactCount(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED); err == nil {
		t.Fatalf("UNSPECIFIED: expected error, got nil")
	}
}

func TestResolveArtifactCount_LegacyZero(t *testing.T) {
	meta := &actiontypes.CascadeMetadata{} // both INDEX (RqIdsIc) and SYMBOL (len(RqIdsIds)) are zero
	for _, class := range []audittypes.StorageProofArtifactClass{
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX,
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL,
	} {
		got, err := ResolveArtifactCount(meta, class)
		if err != nil {
			t.Fatalf("class=%v: legacy zero should not error, got: %v", class, err)
		}
		if got != 0 {
			t.Fatalf("class=%v: want 0, got %d", class, got)
		}
	}
}

func TestResolveArtifactKey_Symbol_OutOfRange(t *testing.T) {
	meta := &actiontypes.CascadeMetadata{RqIdsIds: []string{"id-0", "id-1"}}

	got, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 1)
	if err != nil {
		t.Fatalf("in-range SYMBOL: unexpected error: %v", err)
	}
	if got != "id-1" {
		t.Fatalf("SYMBOL[1]: want id-1, got %q", got)
	}

	if _, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 2); err == nil {
		t.Fatalf("SYMBOL[2]: expected out-of-range error, got nil")
	} else if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("SYMBOL[2]: error should mention out of range, got: %v", err)
	}
}

func TestResolveArtifactKey_Index_KnownVector(t *testing.T) {
	meta := &actiontypes.CascadeMetadata{Signatures: "index-signature-format", RqIdsIc: 2, RqIdsMax: 5}
	got0, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 0)
	if err != nil {
		t.Fatalf("INDEX[0]: unexpected error: %v", err)
	}
	got1, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 1)
	if err != nil {
		t.Fatalf("INDEX[1]: unexpected error: %v", err)
	}
	if got0 == "" || got1 == "" || got0 == got1 {
		t.Fatalf("expected distinct non-empty index ids, got %q and %q", got0, got1)
	}
	if _, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 99); err == nil {
		t.Fatalf("INDEX[99]: expected out-of-range error, got nil")
	}
}

func TestResolveArtifactSize_SymbolUsesCeilFileBytesOverRqMax(t *testing.T) {
	act := &actiontypes.Action{FileSizeKbs: 10}
	meta := &actiontypes.CascadeMetadata{RqIdsMax: 3, RqIdsIds: []string{"s0", "s1", "s2"}}
	got, err := ResolveArtifactSize(act, meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 2)
	if err != nil {
		t.Fatalf("SYMBOL size: unexpected error: %v", err)
	}
	// ceil(10*1024 / 3) = 3414.
	if got != 3414 {
		t.Fatalf("SYMBOL size: want 3414, got %d", got)
	}
}

func TestResolveArtifactSize_IndexUsesGeneratedFileLength(t *testing.T) {
	act := &actiontypes.Action{FileSizeKbs: 10}
	meta := &actiontypes.CascadeMetadata{Signatures: "index-signature-format", RqIdsIc: 2, RqIdsMax: 5}
	got, err := ResolveArtifactSize(act, meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 1)
	if err != nil {
		t.Fatalf("INDEX size: unexpected error: %v", err)
	}
	if got == 0 {
		t.Fatalf("INDEX size: expected non-zero generated file length")
	}
	if _, err := ResolveArtifactSize(act, meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 99); err == nil {
		t.Fatalf("INDEX[99]: expected out-of-range error, got nil")
	}
}

func TestResolveArtifactKey_Unspecified(t *testing.T) {
	meta := &actiontypes.CascadeMetadata{}
	if _, err := ResolveArtifactKey(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED, 0); err == nil {
		t.Fatalf("UNSPECIFIED: expected error, got nil")
	}
}
