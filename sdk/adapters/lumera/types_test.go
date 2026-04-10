package lumera

import "testing"

func TestParseSupernodeStateRecognizesStorageFull(t *testing.T) {
	if got := ParseSupernodeState(string(SUPERNODE_STATE_STORAGE_FULL)); got != SUPERNODE_STATE_STORAGE_FULL {
		t.Fatalf("expected STORAGE_FULL, got %q", got)
	}
}
