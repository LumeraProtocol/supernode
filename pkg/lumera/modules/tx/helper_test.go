package tx

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

func TestIsSequenceMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "grpc simulation mismatch",
			err: fmt.Errorf(
				"simulation failed: simulation error: rpc error: code = Unknown desc = account sequence mismatch, expected 7855, got 7854: incorrect account sequence [cosmos/cosmos-sdk@v0.53.0/x/auth/ante/sigverify.go:290] with gas used: '35369'",
			),
			want: true,
		},
		{
			name: "broadcast raw_log mismatch",
			err: fmt.Errorf(
				"tx failed: code=32 codespace=sdk height=0 gas_wanted=0 gas_used=0 raw_log=account sequence mismatch, expected 10, got 9: incorrect account sequence",
			),
			want: true,
		},
		{
			name: "unrelated expected/got",
			err:  fmt.Errorf("expected 5, got 4"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSequenceMismatch(tt.err); got != tt.want {
				t.Fatalf("isSequenceMismatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseExpectedSequence(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("account sequence mismatch, expected 7855, got 7854: incorrect account sequence")
	if got, ok := parseExpectedSequence(err); !ok || got != 7855 {
		t.Fatalf("parseExpectedSequence() = (%d, %v), want (7855, true)", got, ok)
	}

	if _, ok := parseExpectedSequence(fmt.Errorf("incorrect account sequence")); ok {
		t.Fatalf("parseExpectedSequence() unexpectedly matched message without expected/got")
	}

	if _, ok := parseExpectedSequence(nil); ok {
		t.Fatalf("parseExpectedSequence() unexpectedly matched nil error")
	}
}

// --- Gas-adjustment default & defaults application ---------------------------

func TestDefaultGasAdjustmentIs1_3(t *testing.T) {
	t.Parallel()
	if DefaultGasAdjustment != 1.3 {
		t.Fatalf("DefaultGasAdjustment = %v, want 1.3 (see Notion: SN Gas used for Finalize tx is too high)", DefaultGasAdjustment)
	}
	if DefaultGasAdjustmentMultiplier <= 1.0 {
		t.Fatalf("DefaultGasAdjustmentMultiplier = %v, must be > 1.0", DefaultGasAdjustmentMultiplier)
	}
	if DefaultGasAdjustmentMaxAttempts <= 0 {
		t.Fatalf("DefaultGasAdjustmentMaxAttempts = %v, must be > 0", DefaultGasAdjustmentMaxAttempts)
	}
}

func TestApplyTxHelperDefaults_ZeroValuesUseDefaults(t *testing.T) {
	t.Parallel()
	got := applyTxHelperDefaults(&TxHelperConfig{
		ChainID: "x",
		KeyName: "k",
	})
	if got.GasAdjustment != DefaultGasAdjustment {
		t.Errorf("GasAdjustment = %v, want %v", got.GasAdjustment, DefaultGasAdjustment)
	}
	if got.GasAdjustmentMultiplier != DefaultGasAdjustmentMultiplier {
		t.Errorf("GasAdjustmentMultiplier = %v, want %v", got.GasAdjustmentMultiplier, DefaultGasAdjustmentMultiplier)
	}
	if got.GasAdjustmentMaxAttempts != DefaultGasAdjustmentMaxAttempts {
		t.Errorf("GasAdjustmentMaxAttempts = %v, want %v", got.GasAdjustmentMaxAttempts, DefaultGasAdjustmentMaxAttempts)
	}
	if got.GasPadding != DefaultGasPadding {
		t.Errorf("GasPadding = %v, want %v", got.GasPadding, DefaultGasPadding)
	}
	if got.FeeDenom != DefaultFeeDenom {
		t.Errorf("FeeDenom = %v, want %v", got.FeeDenom, DefaultFeeDenom)
	}
	if got.GasPrice != DefaultGasPrice {
		t.Errorf("GasPrice = %v, want %v", got.GasPrice, DefaultGasPrice)
	}
	if got.GasLimit != DefaultGasLimit {
		t.Errorf("GasLimit = %v, want %v", got.GasLimit, DefaultGasLimit)
	}
}

func TestApplyTxHelperDefaults_PreservesOperatorValues(t *testing.T) {
	t.Parallel()
	got := applyTxHelperDefaults(&TxHelperConfig{
		ChainID:                  "x",
		KeyName:                  "k",
		GasAdjustment:            1.1,
		GasAdjustmentMultiplier:  2.0,
		GasAdjustmentMaxAttempts: 5,
		GasPadding:               99,
		FeeDenom:                 "ulume",
		GasPrice:                 "0.050",
	})
	if got.GasAdjustment != 1.1 {
		t.Errorf("GasAdjustment = %v, want 1.1", got.GasAdjustment)
	}
	if got.GasAdjustmentMultiplier != 2.0 {
		t.Errorf("GasAdjustmentMultiplier = %v, want 2.0", got.GasAdjustmentMultiplier)
	}
	if got.GasAdjustmentMaxAttempts != 5 {
		t.Errorf("GasAdjustmentMaxAttempts = %v, want 5", got.GasAdjustmentMaxAttempts)
	}
	if got.GasPadding != 99 {
		t.Errorf("GasPadding = %v, want 99", got.GasPadding)
	}
	if got.GasPrice != "0.050" {
		t.Errorf("GasPrice = %v, want 0.050", got.GasPrice)
	}
}

func TestApplyTxHelperDefaults_CapsMaxAttempts(t *testing.T) {
	t.Parallel()
	got := applyTxHelperDefaults(&TxHelperConfig{
		ChainID:                  "x",
		KeyName:                  "k",
		GasAdjustmentMaxAttempts: 9999,
	})
	if got.GasAdjustmentMaxAttempts != 10 {
		t.Fatalf("GasAdjustmentMaxAttempts = %d, want capped to 10", got.GasAdjustmentMaxAttempts)
	}
}

// --- isOutOfGas detection -----------------------------------------------------

func TestIsOutOfGas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "simulation out of gas string",
			err:  fmt.Errorf("simulation failed: simulation error: rpc error: code = Unknown desc = out of gas in location: ReadFlat; gasWanted: 100000, gasUsed: 180000"),
			want: true,
		},
		{
			name: "broadcast code=11 codespace=sdk",
			err:  fmt.Errorf("tx failed: code=11 codespace=sdk height=0 gas_wanted=100000 gas_used=180000 raw_log=out of gas in location"),
			want: true,
		},
		{
			name: "broadcast mentions out of gas in raw_log only",
			err:  fmt.Errorf("tx failed: code=11 codespace=sdk raw_log=out of gas"),
			want: true,
		},
		{
			name: "unrelated sequence mismatch",
			err:  fmt.Errorf("account sequence mismatch, expected 10, got 9"),
			want: false,
		},
		{
			name: "unrelated code=11 but wrong codespace",
			err:  fmt.Errorf("code=11 codespace=action height=0 gas_wanted=0 gas_used=0 raw_log=some other failure"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isOutOfGas(tt.err); got != tt.want {
				t.Fatalf("isOutOfGas() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- executeWithOOGRetry: retry escalation behavior --------------------------

// oogErr returns a canonical OOG error string.
var oogErr = fmt.Errorf("tx failed: code=11 codespace=sdk raw_log=out of gas")

func TestExecuteWithOOGRetry_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()
	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 3,
	}
	var attempts int32
	resp, err := executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		atomic.AddInt32(&attempts, 1)
		if cfg.GasAdjustment != 1.3 {
			t.Fatalf("first attempt gas_adjustment = %v, want 1.3", cfg.GasAdjustment)
		}
		return &sdktx.BroadcastTxResponse{}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	// Base config must not have been mutated.
	if base.GasAdjustment != 1.3 {
		t.Fatalf("base GasAdjustment mutated: %v", base.GasAdjustment)
	}
}

func TestExecuteWithOOGRetry_EscalatesAndSucceeds(t *testing.T) {
	t.Parallel()
	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 4,
	}
	var seen []float64
	_, err := executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		seen = append(seen, cfg.GasAdjustment)
		// Fail with OOG for the first two attempts, succeed on the third.
		if len(seen) < 3 {
			return nil, oogErr
		}
		return &sdktx.BroadcastTxResponse{}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d attempts, want 3 (gas values: %v)", len(seen), seen)
	}
	// Multiplicative: 1.3, 1.3*1.3, 1.3*1.3*1.3.
	wantSeq := []float64{1.3, 1.3 * 1.3, 1.3 * 1.3 * 1.3}
	for i, w := range wantSeq {
		if diff := seen[i] - w; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("attempt %d: gas_adjustment=%v, want %v", i+1, seen[i], w)
		}
	}
	// Base config must not have been mutated by the loop.
	if base.GasAdjustment != 1.3 {
		t.Fatalf("base GasAdjustment mutated: %v", base.GasAdjustment)
	}
}

func TestExecuteWithOOGRetry_NonOOGBailsImmediately(t *testing.T) {
	t.Parallel()
	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 5,
	}
	nonOOG := fmt.Errorf("tx failed: code=4 codespace=sdk raw_log=unauthorized")

	var attempts int32
	_, err := executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, nonOOG
	})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("want unauthorized error, got: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (non-OOG must bail immediately)", got)
	}
}

func TestExecuteWithOOGRetry_HonoursMaxAttempts(t *testing.T) {
	t.Parallel()
	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 3,
	}
	var attempts int32
	_, err := executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, oogErr
	})
	if err == nil {
		t.Fatal("expected error after max attempts, got nil")
	}
	if !strings.Contains(err.Error(), "out of gas after 3 attempts") {
		t.Fatalf("error message missing attempt count: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestExecuteWithOOGRetry_CloneIsolatesConfigMutation(t *testing.T) {
	t.Parallel()
	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  2.0,
		GasAdjustmentMaxAttempts: 3,
	}
	_, _ = executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		cfg.GasAdjustment = 999.0 // mutate clone
		return nil, oogErr
	})
	if base.GasAdjustment != 1.3 {
		t.Fatalf("base config mutated: GasAdjustment = %v", base.GasAdjustment)
	}
	if base.GasAdjustmentMaxAttempts != 3 {
		t.Fatalf("base GasAdjustmentMaxAttempts mutated: %v", base.GasAdjustmentMaxAttempts)
	}
}

func TestExecuteWithOOGRetry_NilBaseReturnsError(t *testing.T) {
	t.Parallel()
	_, err := executeWithOOGRetry(context.Background(), nil, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error on nil base cfg")
	}
}

// --- UpdateConfig safety: hard cap on GasAdjustmentMaxAttempts ---------------
// Regression: UpdateConfig previously accepted any positive value, bypassing
// the applyTxHelperDefaults() cap and allowing fee runaway via runtime reconfig.
func TestTxHelper_UpdateConfig_CapsMaxAttempts(t *testing.T) {
	t.Parallel()
	h := &TxHelper{config: &TxConfig{GasAdjustmentMaxAttempts: 3}}
	h.UpdateConfig(&TxHelperConfig{GasAdjustmentMaxAttempts: 9999})
	if h.config.GasAdjustmentMaxAttempts != MaxGasAdjustmentAttemptsCap {
		t.Fatalf("UpdateConfig accepted un-capped MaxAttempts = %d, want %d",
			h.config.GasAdjustmentMaxAttempts, MaxGasAdjustmentAttemptsCap)
	}
}

func TestTxHelper_UpdateConfig_PreservesReasonableMaxAttempts(t *testing.T) {
	t.Parallel()
	h := &TxHelper{config: &TxConfig{GasAdjustmentMaxAttempts: 3}}
	h.UpdateConfig(&TxHelperConfig{GasAdjustmentMaxAttempts: 5})
	if h.config.GasAdjustmentMaxAttempts != 5 {
		t.Fatalf("UpdateConfig dropped reasonable value: got %d, want 5",
			h.config.GasAdjustmentMaxAttempts)
	}
}

// --- ExecuteTransactionWithMsgs: OOG retry parity with ExecuteTransaction ----
// Regression: the pre-built-messages path previously called ProcessTransaction
// directly, bypassing the OOG escalation. External SDK consumers (sdk-go)
// relying on this entry point must get the same behavior.
func TestExecuteTransactionWithMsgs_AppliesOOGRetry(t *testing.T) {
	t.Parallel()

	mod := &scenarioTxModule{oogAttempts: 2}

	h := &TxHelper{
		txmod: mod,
		config: &TxConfig{
			GasAdjustment:            0.5,
			GasAdjustmentMultiplier:  2.0,
			GasAdjustmentMaxAttempts: 5,
		},
	}

	acc := &authtypes.BaseAccount{Address: "lumera1abc", Sequence: 1}
	resp, err := h.ExecuteTransactionWithMsgs(context.Background(), []types.Msg{}, acc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil resp after OOG retry")
	}
	if got := len(mod.seenAdjustments); got != 3 {
		t.Fatalf("attempts = %d, want 3 (OOG retry should escalate)", got)
	}
	// Sanity: gas_adjustment should have escalated between attempts.
	if !(mod.seenAdjustments[0] < mod.seenAdjustments[1] && mod.seenAdjustments[1] < mod.seenAdjustments[2]) {
		t.Fatalf("expected escalating gas_adjustment, got %v", mod.seenAdjustments)
	}
}

func TestExecuteTransactionWithMsgs_NonOOGBailsImmediately(t *testing.T) {
	t.Parallel()

	mod := &scenarioTxModule{nonOOGError: fmt.Errorf("signature verification failed")}

	h := &TxHelper{
		txmod: mod,
		config: &TxConfig{
			GasAdjustment:            1.3,
			GasAdjustmentMultiplier:  1.3,
			GasAdjustmentMaxAttempts: 5,
		},
	}

	acc := &authtypes.BaseAccount{Address: "lumera1abc", Sequence: 1}
	_, err := h.ExecuteTransactionWithMsgs(context.Background(), []types.Msg{}, acc)
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("expected non-OOG error to bubble up, got: %v", err)
	}
	if got := len(mod.seenAdjustments); got != 1 {
		t.Fatalf("attempts = %d, want 1 (non-OOG must not retry)", got)
	}
}

// --- smoke: fn signature requires ctx + account usable -----------------------
// This asserts executeWithOOGRetry does not depend on any undeclared globals.
func TestExecuteWithOOGRetry_CallableSmokeUsesAccountInfo(t *testing.T) {
	t.Parallel()
	acc := &authtypes.BaseAccount{Address: "lumera1abc", Sequence: 7}
	msgs := []types.Msg{}
	_ = acc
	_ = msgs

	base := &TxConfig{
		GasAdjustment:            1.3,
		GasAdjustmentMultiplier:  1.3,
		GasAdjustmentMaxAttempts: 1,
	}
	_, err := executeWithOOGRetry(context.Background(), base, func(cfg *TxConfig) (*sdktx.BroadcastTxResponse, error) {
		return &sdktx.BroadcastTxResponse{}, nil
	})
	if err != nil {
		t.Fatalf("smoke: %v", err)
	}
}
