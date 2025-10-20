package codec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Constants: set InputPath and TaskID. BaseDir is the current directory.
const (
	BaseDir   = ""
	InputPath = ""        // set to an existing file path before running
	TaskID    = "rq-dirA" // both tests use the same directory
)

// TestEncode_ToDirA encodes InputPath into BaseDir/TaskID using default settings.
func TestEncode_ToDirA(t *testing.T) {
	if InputPath == "" {
		t.Skip("set InputPath constant to a file path to run this test")
	}

	fi, err := os.Stat(InputPath)
	if err != nil {
		t.Fatalf("stat input: %v", err)
	}

	c := NewRaptorQCodec(BaseDir)
	resp, err := c.Encode(context.TODO(), EncodeRequest{TaskID: TaskID, Path: InputPath, DataSize: int(fi.Size())})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	t.Logf("encoded to: %s", resp.SymbolsDir)

	// Log theoretical minimum percentage of symbols needed per block
	for _, b := range resp.Layout.Blocks {
		s := int64(rqSymbolSize)
		if s <= 0 {
			s = 65535
		}
		k := int((b.Size + s - 1) / s) // source symbols count
		ttotal := len(b.Symbols)       // total symbols count
		if ttotal > 0 {
			pct := 100.0 * float64(k) / float64(ttotal)
			t.Logf("block %d: min ~= %.2f%% (K=%d, Total=%d)", b.BlockID, pct, k, ttotal)
		} else {
			t.Logf("block %d: no symbols found to compute min%%", b.BlockID)
		}
	}
}

// TestDecode_FromDirA decodes using symbols created by TestEncode_ToDirA from the same directory.
// No deep assertions; only success/failure of decode is checked.
func TestDecode_FromDirA(t *testing.T) {
	symbolsDir := filepath.Join(BaseDir, TaskID)

	if InputPath == "" {
		t.Skip("set InputPath constant to a file path to run this test")
	}
	// Load layout from disk (prefer library-produced name)
	var layout Layout
	var layoutPath string
	for _, name := range []string{"_raptorq_layout.json", "layout.json"} {
		p := filepath.Join(symbolsDir, name)
		if _, err := os.Stat(p); err == nil {
			layoutPath = p
			break
		}
	}
	if layoutPath == "" {
		t.Fatalf("layout file not found in %s", symbolsDir)
	}
	data, err := os.ReadFile(layoutPath)
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if err := json.Unmarshal(data, &layout); err != nil {
		t.Fatalf("unmarshal layout: %v", err)
	}

	// Load symbols into memory per layout
	syms := make(map[string][]byte)
	for _, b := range layout.Blocks {
		blockDir := filepath.Join(symbolsDir, "block_"+itoa(b.BlockID))
		for _, id := range b.Symbols {
			p := filepath.Join(blockDir, id)
			if sdata, err := os.ReadFile(p); err == nil {
				syms[id] = sdata
			}
		}
	}

	c := NewRaptorQCodec(BaseDir)
	_, err = c.Decode(context.TODO(), DecodeRequest{ActionID: TaskID, Layout: layout, Symbols: syms})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Logf("decode succeeded for dir: %s", symbolsDir)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}

// TestCreateMetadata_SaveToFile generates layout metadata only and writes it to a file.
func TestCreateMetadata_SaveToFile(t *testing.T) {
	if InputPath == "" {
		t.Skip("set InputPath constant to a file path to run this test")
	}

	ctx := context.TODO()
	c := NewRaptorQCodec(BaseDir)

	// Create metadata using the codec and write it next to the input file.
	resp, err := c.CreateMetadata(ctx, CreateMetadataRequest{Path: InputPath})
	if err != nil {
		t.Fatalf("create metadata: %v", err)
	}
	data, err := json.MarshalIndent(resp.Layout, "", "  ")
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	outPath := InputPath + ".layout.json"
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("output file is empty: %s", outPath)
	}
	t.Logf("metadata saved to: %s (%d bytes)", outPath, fi.Size())
}
