package codec

import (
	"context"
	"os"
	"testing"
)

func TestPrepareDecode_UniqueWorkspacePerCall(t *testing.T) {
	base := t.TempDir()
	c := NewRaptorQCodec(base)
	layout := Layout{Blocks: []Block{{BlockID: 0, Symbols: []string{"s1"}}}}

	_, _, cleanup1, ws1, err := c.PrepareDecode(context.Background(), "actionA", layout)
	if err != nil {
		t.Fatalf("prepare decode 1: %v", err)
	}
	t.Cleanup(func() { _ = cleanup1() })
	if ws1 == nil || ws1.SymbolsDir == "" {
		t.Fatalf("prepare decode 1 returned empty workspace")
	}
	if _, err := os.Stat(ws1.SymbolsDir); err != nil {
		t.Fatalf("stat ws1: %v", err)
	}

	_, _, cleanup2, ws2, err := c.PrepareDecode(context.Background(), "actionA", layout)
	if err != nil {
		t.Fatalf("prepare decode 2: %v", err)
	}
	t.Cleanup(func() { _ = cleanup2() })
	if ws2 == nil || ws2.SymbolsDir == "" {
		t.Fatalf("prepare decode 2 returned empty workspace")
	}
	if _, err := os.Stat(ws2.SymbolsDir); err != nil {
		t.Fatalf("stat ws2: %v", err)
	}

	if ws1.SymbolsDir == ws2.SymbolsDir {
		t.Fatalf("expected unique workspace per call; got same dir: %s", ws1.SymbolsDir)
	}

	if err := cleanup1(); err != nil {
		t.Fatalf("cleanup 1: %v", err)
	}
	if _, err := os.Stat(ws1.SymbolsDir); !os.IsNotExist(err) {
		t.Fatalf("expected ws1 removed; stat err=%v", err)
	}
	if _, err := os.Stat(ws2.SymbolsDir); err != nil {
		t.Fatalf("expected ws2 still present after ws1 cleanup; stat err=%v", err)
	}
}
