package kademlia

import (
	"strings"
	"testing"
)

func TestValidateBatchProbeKeysRequest(t *testing.T) {
	validKey := strings.Repeat("a", batchProbeKeyHexLen)

	if err := validateBatchProbeKeysRequest([]string{validKey}); err != nil {
		t.Fatalf("expected valid request, got error: %v", err)
	}

	tooMany := make([]string, maxBatchProbeKeysPerRequest+1)
	for i := range tooMany {
		tooMany[i] = validKey
	}
	if err := validateBatchProbeKeysRequest(tooMany); err == nil {
		t.Fatalf("expected too-many-keys validation error")
	}

	if err := validateBatchProbeKeysRequest([]string{"abcd"}); err == nil {
		t.Fatalf("expected invalid key length validation error")
	}

	invalidHex := strings.Repeat("z", batchProbeKeyHexLen)
	if err := validateBatchProbeKeysRequest([]string{invalidHex}); err == nil {
		t.Fatalf("expected invalid hex validation error")
	}
}
