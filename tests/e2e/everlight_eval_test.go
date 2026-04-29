//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestEverlightRetrospectiveCriticalPaths(t *testing.T) {
	cmd := exec.Command("go", "test",
		"./supernode/host_reporter",
		"./p2p/kademlia",
		"./supernode/verifier",
		"./pkg/lumera/modules/supernode",
		"./pkg/cascadekit",
		"./supernode/cmd",
	)
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("targeted Everlight critical path tests failed: %v\n%s", err, out)
	}
}

func TestEverlightGateReportPassed(t *testing.T) {
	cmd := exec.Command("sed", "-n", "1,40p", "docs/gates-evals/S01-gate-report.md")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read gate report: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("**OVERALL: PASS**")) {
		t.Fatalf("gate report does not show PASS in first 40 lines:\n%s", out)
	}
	if strings.Contains(string(out), "**OVERALL: FAIL**") {
		t.Fatalf("gate report still contains FAIL marker in first 40 lines:\n%s", out)
	}
}
