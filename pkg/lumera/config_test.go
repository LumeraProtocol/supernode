package lumera

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

func TestNewConfig_DefaultTxOptionsZeroed(t *testing.T) {
	t.Parallel()
	kr := keyring.NewInMemory(nil)
	cfg, err := NewConfig("localhost:9090", "testing", "key", kr)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.TxOptions.GasAdjustment != 0 {
		t.Errorf("GasAdjustment = %v, want 0 (zero → default at TxHelper layer)", cfg.TxOptions.GasAdjustment)
	}
}

func TestNewConfig_TxOptionsPropagated(t *testing.T) {
	t.Parallel()
	kr := keyring.NewInMemory(nil)
	cfg, err := NewConfig("localhost:9090", "testing", "key", kr, TxOptions{
		GasAdjustment:            1.21,
		GasAdjustmentMultiplier:  1.5,
		GasAdjustmentMaxAttempts: 4,
		GasPadding:               99,
		GasPrice:                 "0.030",
		FeeDenom:                 "ulume",
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}

	thc := cfg.toTxHelperConfig()
	if thc.GasAdjustment != 1.21 {
		t.Errorf("GasAdjustment = %v, want 1.21", thc.GasAdjustment)
	}
	if thc.GasAdjustmentMultiplier != 1.5 {
		t.Errorf("GasAdjustmentMultiplier = %v, want 1.5", thc.GasAdjustmentMultiplier)
	}
	if thc.GasAdjustmentMaxAttempts != 4 {
		t.Errorf("GasAdjustmentMaxAttempts = %v, want 4", thc.GasAdjustmentMaxAttempts)
	}
	if thc.GasPadding != 99 {
		t.Errorf("GasPadding = %v, want 99", thc.GasPadding)
	}
	if thc.GasPrice != "0.030" {
		t.Errorf("GasPrice = %q, want 0.030", thc.GasPrice)
	}
	if thc.FeeDenom != "ulume" {
		t.Errorf("FeeDenom = %q, want ulume", thc.FeeDenom)
	}
	if thc.ChainID != "testing" || thc.KeyName != "key" {
		t.Errorf("ChainID/KeyName not propagated: %+v", thc)
	}
}

func TestNewConfig_RejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	kr := keyring.NewInMemory(nil)
	cases := []struct {
		name  string
		grpc  string
		chain string
		key   string
		kr    keyring.Keyring
	}{
		{"empty grpc", "", "x", "k", kr},
		{"empty chain", "host", "", "k", kr},
		{"nil keyring", "host", "x", "k", nil},
		{"empty key", "host", "x", "", kr},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewConfig(tc.grpc, tc.chain, tc.key, tc.kr); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
