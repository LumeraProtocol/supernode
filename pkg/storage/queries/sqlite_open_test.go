package queries

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenHistoryDBAtUsesConfiguredBaseDir(t *testing.T) {
	baseDir := t.TempDir()
	store, err := OpenHistoryDBAt(baseDir)
	if err != nil {
		t.Fatalf("OpenHistoryDBAt: %v", err)
	}
	t.Cleanup(func() { store.CloseHistoryDB(context.Background()) })

	if _, err := os.Stat(filepath.Join(baseDir, historyDBName)); err != nil {
		t.Fatalf("expected history db under configured base dir: %v", err)
	}
}

func TestOpenHistoryDBAtRejectsEmptyBaseDir(t *testing.T) {
	if _, err := OpenHistoryDBAt(" "); err == nil {
		t.Fatal("expected error for empty base dir")
	}
}
