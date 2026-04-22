package tx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// ---------------------------------------------------------------------------
// Stubs for TxHelper.ExecuteTransaction integration test.
//
// We want to prove end-to-end that the OOG-retry path is actually wired into
// the public API that every msg module (action_msg / supernode_msg / audit_msg)
// uses — not just the inner executeWithOOGRetry helper.
// ---------------------------------------------------------------------------

// stubAuthModule implements auth.Module just enough for TxHelper.ExecuteTransaction
// to fetch initial account info.
type stubAuthModule struct {
	addr string
	seq  uint64
	num  uint64
}

func (s *stubAuthModule) AccountInfoByAddress(_ context.Context, _ string) (*authtypes.QueryAccountInfoResponse, error) {
	return &authtypes.QueryAccountInfoResponse{
		Info: &authtypes.BaseAccount{
			Address:       s.addr,
			AccountNumber: s.num,
			Sequence:      s.seq,
		},
	}, nil
}

func (s *stubAuthModule) AccountByAddress(_ context.Context, _ string) (types.AccountI, error) {
	return nil, fmt.Errorf("not implemented in stub")
}

func (s *stubAuthModule) Verify(_ context.Context, _ string, _, _ []byte) error { return nil }

// scenarioTxModule is a test double for tx.Module that records every
// ProcessTransaction call and can be scripted to return OOG for the first N
// invocations, then succeed. All other Module methods are unused in this path.
type scenarioTxModule struct {
	mu sync.Mutex

	// If non-zero, fail this many attempts with OOG before the first success.
	oogAttempts int

	// Records the config.GasAdjustment seen on each attempt.
	seenAdjustments []float64

	// Optional: fail with a non-OOG error instead of an OOG one.
	nonOOGError error
}

func (s *scenarioTxModule) ProcessTransaction(_ context.Context, _ []types.Msg, _ *authtypes.BaseAccount, cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAdjustments = append(s.seenAdjustments, cfg.GasAdjustment)

	attempt := len(s.seenAdjustments) // 1-indexed

	if s.nonOOGError != nil {
		return nil, s.nonOOGError
	}

	if attempt <= s.oogAttempts {
		// Emulate the chain/sim returning ErrOutOfGas.
		return nil, fmt.Errorf("tx failed: code=11 codespace=sdk height=0 gas_wanted=100000 gas_used=180000 raw_log=out of gas in location: ReadFlat; gasWanted: 100000, gasUsed: 180000")
	}

	// Success.
	return &sdktx.BroadcastTxResponse{
		TxResponse: nil, // TxHelper only consults error + full resp pointer
	}, nil
}

func (s *scenarioTxModule) SimulateTransaction(_ context.Context, _ []types.Msg, _ *authtypes.BaseAccount, _ *TxConfig) (*sdktx.SimulateResponse, error) {
	return &sdktx.SimulateResponse{}, nil
}

func (s *scenarioTxModule) BuildAndSignTransaction(_ context.Context, _ []types.Msg, _ *authtypes.BaseAccount, _ uint64, _ string, _ *TxConfig) ([]byte, error) {
	return nil, fmt.Errorf("not called in this path")
}
func (s *scenarioTxModule) BroadcastTransaction(_ context.Context, _ []byte) (*sdktx.BroadcastTxResponse, error) {
	return nil, fmt.Errorf("not called in this path")
}
func (s *scenarioTxModule) GetTransaction(_ context.Context, _ string) (*sdktx.GetTxResponse, error) {
	return nil, fmt.Errorf("not called in this path")
}
func (s *scenarioTxModule) GetTxsEvent(_ context.Context, _ string, _, _ uint64) (*sdktx.GetTxsEventResponse, error) {
	return nil, fmt.Errorf("not called in this path")
}
func (s *scenarioTxModule) CalculateFee(_ uint64, _ *TxConfig) string { return "" }

// newTestKeyring creates an in-memory keyring with a single deterministic key
// under name `keyName`, so TxHelper can resolve the creator address.
func newTestKeyring(t *testing.T, keyName string) (keyring.Keyring, string) {
	t.Helper()
	// The Cosmos SDK keyring requires a registered proto codec that knows
	// about the crypto interfaces to serialize keys.
	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kr := keyring.NewInMemory(cdc)

	// Deterministic test mnemonic.
	const mnemonic = "test test test test test test test test test test test junk"
	record, err := kr.NewAccount(keyName, mnemonic, "", "m/44'/118'/0'/0/0", hd.Secp256k1)
	if err != nil {
		t.Fatalf("keyring.NewAccount: %v", err)
	}
	addr, err := record.GetAddress()
	if err != nil {
		t.Fatalf("record.GetAddress: %v", err)
	}
	return kr, addr.String()
}

// TestTxHelper_ExecuteTransaction_OOGRetryEscalatesAndSucceeds simulates the
// exact production scenario requested: 1.3 is "not okay" (chain rejects with
// ErrOutOfGas), retry escalates gas_adjustment, and the tx eventually succeeds.
//
// This exercises the public TxHelper.ExecuteTransaction entrypoint that is
// used by every msg module (action_msg.FinalizeCascadeAction in particular).
func TestTxHelper_ExecuteTransaction_OOGRetryEscalatesAndSucceeds(t *testing.T) {
	t.Parallel()

	kr, addr := newTestKeyring(t, "validator")
	auth := &stubAuthModule{addr: addr, seq: 42, num: 7}

	// Chain rejects the first two attempts with OOG; third succeeds.
	txMod := &scenarioTxModule{oogAttempts: 2}

	h := NewTxHelper(auth, txMod, &TxHelperConfig{
		ChainID:                  "lumera-devnet-1",
		Keyring:                  kr,
		KeyName:                  "validator",
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 3,
	})

	resp, err := h.ExecuteTransaction(context.Background(), func(creator string) (types.Msg, error) {
		// We don't actually broadcast — the scenarioTxModule stubs ProcessTransaction —
		// so we can return any msg here. Use a dummy bank Send-shaped placeholder.
		_ = creator
		return nil, nil // nil msg is fine; ProcessTransaction stub ignores it
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction: unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("ExecuteTransaction: nil response")
	}

	// The tx module must have seen exactly 3 attempts with escalating adjustments.
	if got := len(txMod.seenAdjustments); got != 3 {
		t.Fatalf("attempts = %d, want 3 (scenario: OOG×2 → success), seen=%v", got, txMod.seenAdjustments)
	}

	wantSeq := []float64{1.3, 1.3 * 1.3, 1.3 * 1.3 * 1.3}
	for i, w := range wantSeq {
		diff := txMod.seenAdjustments[i] - w
		if diff > 1e-9 || diff < -1e-9 {
			t.Errorf("attempt %d: gas_adjustment = %v, want %v", i+1, txMod.seenAdjustments[i], w)
		}
	}

	// Sequence was advanced exactly once (the successful commit).
	if h.nextSequence != 43 {
		t.Errorf("nextSequence = %d, want 43 (42 + 1)", h.nextSequence)
	}

	// Caller-provided config must not have been mutated by the retry loop.
	if h.config.GasAdjustment != 1.3 {
		t.Errorf("helper config mutated: GasAdjustment = %v, want 1.3 (untouched base)", h.config.GasAdjustment)
	}
}

// TestTxHelper_ExecuteTransaction_OOGExhaustedSurfacesError ensures that
// when the chain keeps returning OOG past max attempts, the caller sees
// a clear error and the sequence is NOT advanced (no sequence hole).
func TestTxHelper_ExecuteTransaction_OOGExhaustedSurfacesError(t *testing.T) {
	t.Parallel()

	kr, addr := newTestKeyring(t, "validator")
	auth := &stubAuthModule{addr: addr, seq: 100, num: 1}

	// All attempts fail with OOG.
	txMod := &scenarioTxModule{oogAttempts: 999}

	h := NewTxHelper(auth, txMod, &TxHelperConfig{
		ChainID:                  "lumera-devnet-1",
		Keyring:                  kr,
		KeyName:                  "validator",
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 3,
	})

	_, err := h.ExecuteTransaction(context.Background(), func(_ string) (types.Msg, error) { return nil, nil })
	if err == nil {
		t.Fatal("expected error after exhausting OOG retries, got nil")
	}
	if !strings.Contains(err.Error(), "out of gas after 3 attempts") {
		t.Fatalf("error should name exhaustion: %v", err)
	}

	if got := len(txMod.seenAdjustments); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	// Sequence MUST NOT advance on exhausted OOG.
	if h.nextSequence != 100 {
		t.Fatalf("nextSequence = %d, want 100 (unchanged — no sequence hole)", h.nextSequence)
	}
}

// TestTxHelper_ExecuteTransaction_NonOOGErrorBailsAtFirstAttempt ensures that
// a non-OOG error (e.g. signature failure, unauthorized) is surfaced
// immediately without burning extra attempts or advancing sequence.
func TestTxHelper_ExecuteTransaction_NonOOGErrorBailsAtFirstAttempt(t *testing.T) {
	t.Parallel()

	kr, addr := newTestKeyring(t, "validator")
	auth := &stubAuthModule{addr: addr, seq: 7, num: 1}

	txMod := &scenarioTxModule{nonOOGError: fmt.Errorf("tx failed: code=4 codespace=sdk raw_log=unauthorized")}

	h := NewTxHelper(auth, txMod, &TxHelperConfig{
		ChainID:                  "lumera-devnet-1",
		Keyring:                  kr,
		KeyName:                  "validator",
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 5,
	})

	_, err := h.ExecuteTransaction(context.Background(), func(_ string) (types.Msg, error) { return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("want unauthorized, got: %v", err)
	}
	if got := len(txMod.seenAdjustments); got != 1 {
		t.Fatalf("attempts = %d, want 1 (non-OOG must not retry via OOG loop)", got)
	}
	if h.nextSequence != 7 {
		t.Fatalf("nextSequence advanced on non-OOG failure: %d, want 7", h.nextSequence)
	}
}
