package cascade

import (
	"os"
	"strings"
	"testing"
)

func TestStreamCopyFileSyncsBeforeClose(t *testing.T) {
	src, err := os.ReadFile("reseed.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	syncIdx := strings.Index(body, "dst.Sync()")
	closeIdx := strings.LastIndex(body, "dst.Close()")
	if syncIdx < 0 {
		t.Fatalf("streamCopyFile must fsync destination before close")
	}
	if closeIdx < 0 {
		t.Fatalf("streamCopyFile close call not found")
	}
	if syncIdx > closeIdx {
		t.Fatalf("streamCopyFile must call dst.Sync before final dst.Close")
	}
}
