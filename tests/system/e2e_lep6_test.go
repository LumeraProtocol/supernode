package system

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestLEP6RealChainIntegration exercises the real Lumera binary/local-chain
// harness. It intentionally avoids mocks: genesis is mutated, lumerad nodes are
// started, audit queries go through the live RPC endpoint, and a LEP-6 tx command
// is submitted far enough to be rejected by the real audit keeper.
func TestLEP6RealChainIntegration(t *testing.T) {
	sut.ModifyGenesisJSON(t, SetAuditParams(t))
	sut.StartChain(t)

	cli := NewLumeradCLI(t, sut, true)

	t.Run("audit query surface is available", func(t *testing.T) {
		params := cli.CustomQuery("query", "audit", "params", "--output", "json")
		require.True(t, gjson.Valid(params), "audit params query must return JSON: %s", params)
		require.NotEmpty(t, gjson.Get(params, "params").Raw, "audit params response must contain params: %s", params)

		currentEpoch := cli.CustomQuery("query", "audit", "current-epoch", "--output", "json")
		require.True(t, gjson.Valid(currentEpoch), "current epoch query must return JSON: %s", currentEpoch)
	})

	t.Run("heal tx command is wired to chain validation", func(t *testing.T) {
		out := runLumeradNoCheck(t,
			"tx", "audit", "claim-heal-complete", "999999", "missing-ticket", "missing-manifest-hash",
			"--from", "node0",
			"--yes",
			"--gas", "auto",
			"--gas-adjustment", "1.5",
			"--fees", "10ulume",
			"--broadcast-mode", "sync",
			"--output", "json",
		)
		require.Contains(t, out, "heal op 999999 not found", "absent heal-op claim should be rejected by the real audit keeper: %s", out)
	})
}

func runLumeradNoCheck(t *testing.T, args ...string) string {
	t.Helper()
	binaryPath := locateExecutable(sut.ExecBinary)
	homePath := filepath.Join(WorkDir, sut.outputDir)
	base := []string{
		"--home", homePath,
		"--keyring-backend", "test",
		"--chain-id", "testing",
		"--node", "tcp://localhost:26657",
	}
	cmd := exec.Command(binaryPath, append(args, base...)...)
	out, _ := cmd.CombinedOutput()
	return fmt.Sprintf("%s", out)
}
