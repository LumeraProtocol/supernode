package tx

import (
	"context"
	"testing"

	"github.com/cosmos/cosmos-sdk/types"
)

// TestTxHelper_Simulate_RetriesOnSequenceMismatch verifies that a transient
// account-sequence-mismatch during simulation is retried (with a fresh account
// info fetch) rather than surfaced to the caller. This is the regression guard
// for TestLEP6ConcurrentCascadesContendedReporter, where one supernode
// finalizing several cascade actions concurrently could see the simulate
// pre-check fail with "account sequence mismatch, expected N, got M" even though
// the subsequent broadcast (which already retries) would have succeeded.
func TestTxHelper_Simulate_RetriesOnSequenceMismatch(t *testing.T) {
	t.Parallel()

	kr, addr := newTestKeyring(t, "validator")
	auth := &stubAuthModule{addr: addr, seq: 0, num: 7}

	// First two simulate attempts fail with a sequence mismatch; the third
	// succeeds — within the default 3-attempt cap.
	txMod := &scenarioTxModule{seqMismatchSims: 2}

	h := NewTxHelper(auth, txMod, &TxHelperConfig{
		ChainID: "lumera-devnet-1",
		Keyring: kr,
		KeyName: "validator",
	})

	acc, err := h.GetAccountInfo(context.Background())
	if err != nil {
		t.Fatalf("GetAccountInfo: %v", err)
	}

	resp, err := h.Simulate(context.Background(), []types.Msg{}, acc)
	if err != nil {
		t.Fatalf("Simulate: expected success after sequence-mismatch retries, got: %v", err)
	}
	if resp == nil {
		t.Fatal("Simulate: nil response")
	}
	if txMod.simCalls != 3 {
		t.Fatalf("Simulate: expected 3 attempts (2 mismatch + 1 success), got %d", txMod.simCalls)
	}
}

// TestTxHelper_Simulate_GivesUpAfterMaxAttempts verifies the retry is bounded:
// a persistent sequence mismatch eventually surfaces instead of looping forever.
func TestTxHelper_Simulate_GivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	kr, addr := newTestKeyring(t, "validator")
	auth := &stubAuthModule{addr: addr, seq: 0, num: 7}

	// Always fail with a sequence mismatch.
	txMod := &scenarioTxModule{seqMismatchSims: 1000}

	h := NewTxHelper(auth, txMod, &TxHelperConfig{
		ChainID:                     "lumera-devnet-1",
		Keyring:                     kr,
		KeyName:                     "validator",
		SequenceMismatchMaxAttempts: 2,
	})

	acc, err := h.GetAccountInfo(context.Background())
	if err != nil {
		t.Fatalf("GetAccountInfo: %v", err)
	}

	_, err = h.Simulate(context.Background(), []types.Msg{}, acc)
	if err == nil {
		t.Fatal("Simulate: expected error after exhausting retries")
	}
	if txMod.simCalls != 2 {
		t.Fatalf("Simulate: expected exactly 2 attempts (cap), got %d", txMod.simCalls)
	}
}
