package cascadekit

import (
	"encoding/base64"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/DataDog/zstd"
)

func TestExtractIndexAndCreatorSig_Strict(t *testing.T) {
	// too few parts
	if _, _, err := ExtractIndexAndCreatorSig("abc"); err == nil {
		t.Fatalf("expected error for single segment")
	}
	// too many parts
	if _, _, err := ExtractIndexAndCreatorSig("a.b.c"); err == nil {
		t.Fatalf("expected error for three segments")
	}
	// exactly two parts
	a, b, err := ExtractIndexAndCreatorSig("a.b")
	if err != nil || a != "a" || b != "b" {
		t.Fatalf("unexpected result: a=%q b=%q err=%v", a, b, err)
	}
}

func TestParseCompressedIndexFile_Strict(t *testing.T) {
	idx := IndexFile{LayoutIDs: []string{"L1", "L2"}, LayoutSignature: base64.StdEncoding.EncodeToString([]byte("sig"))}
	idxB64, err := EncodeIndexB64(idx)
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
	payload := []byte(idxB64 + "." + base64.StdEncoding.EncodeToString([]byte("sig2")) + ".0")

	compressed, _ := zstd.CompressLevel(nil, payload, 3)

	got, err := ParseCompressedIndexFile(compressed)
	if err != nil {
		t.Fatalf("parse compressed index: %v", err)
	}
	if got.LayoutSignature != idx.LayoutSignature || len(got.LayoutIDs) != 2 {
		t.Fatalf("unexpected index decoded: %+v", got)
	}

	// malformed: only two segments
	compressedBad, _ := zstd.CompressLevel(nil, []byte("a.b"), 3)
	if _, err := ParseCompressedIndexFile(compressedBad); err == nil {
		t.Fatalf("expected error for two segments")
	}
	// malformed: four segments
	compressedBad4, _ := zstd.CompressLevel(nil, []byte("a.b.c.d"), 3)
	if _, err := ParseCompressedIndexFile(compressedBad4); err == nil {
		t.Fatalf("expected error for four segments")
	}
}

func TestVerifySingleBlock(t *testing.T) {
	if err := VerifySingleBlock(codec.Layout{Blocks: []codec.Block{{}}}); err != nil {
		t.Fatalf("unexpected error for single block: %v", err)
	}
	if err := VerifySingleBlock(codec.Layout{Blocks: []codec.Block{{}, {}}}); err == nil {
		t.Fatalf("expected error for multi-block layout")
	}
}
