package tx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyTxHelperDefaults_SequenceMismatchCap verifies the M12 fix:
// SequenceMismatchMaxAttempts is capped at MaxSequenceMismatchAttemptsCap
// in applyTxHelperDefaults, mirroring the GasAdjustmentMaxAttempts cap.
func TestApplyTxHelperDefaults_SequenceMismatchCap(t *testing.T) {
	t.Run("zero defaults to package default", func(t *testing.T) {
		out := applyTxHelperDefaults(&TxHelperConfig{})
		require.Equal(t, DefaultSequenceMismatchMaxAttempts, out.SequenceMismatchMaxAttempts)
	})
	t.Run("explicit value preserved when below cap", func(t *testing.T) {
		out := applyTxHelperDefaults(&TxHelperConfig{SequenceMismatchMaxAttempts: 5})
		require.Equal(t, 5, out.SequenceMismatchMaxAttempts)
	})
	t.Run("over-cap clamped to MaxSequenceMismatchAttemptsCap", func(t *testing.T) {
		out := applyTxHelperDefaults(&TxHelperConfig{SequenceMismatchMaxAttempts: 1000})
		require.Equal(t, MaxSequenceMismatchAttemptsCap, out.SequenceMismatchMaxAttempts)
	})
}

// TestUpdateConfig_SequenceMismatchCapMirror verifies that runtime
// reconfiguration also honours the cap (the safety-cap-mirroring rule
// noted by Matee — operators must not be able to bypass the cap by
// re-sending a config).
func TestUpdateConfig_SequenceMismatchCapMirror(t *testing.T) {
	h := NewTxHelper(nil, nil, &TxHelperConfig{
		ChainID: "test", KeyName: "k", SequenceMismatchMaxAttempts: 3,
	})
	require.Equal(t, 3, h.GetConfig().SequenceMismatchMaxAttempts)

	// Try to push over cap via UpdateConfig — must clamp.
	h.UpdateConfig(&TxHelperConfig{SequenceMismatchMaxAttempts: 1000})
	require.Equal(t, MaxSequenceMismatchAttemptsCap, h.GetConfig().SequenceMismatchMaxAttempts)

	// Below-cap update preserved.
	h.UpdateConfig(&TxHelperConfig{SequenceMismatchMaxAttempts: 7})
	require.Equal(t, 7, h.GetConfig().SequenceMismatchMaxAttempts)

	// 0 means "leave as-is" (UpdateConfig only writes positive values).
	h.UpdateConfig(&TxHelperConfig{SequenceMismatchMaxAttempts: 0})
	require.Equal(t, 7, h.GetConfig().SequenceMismatchMaxAttempts)
}
