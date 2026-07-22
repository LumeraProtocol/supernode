package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChainIDFromInitArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"separate", []string{"--chain-id", "lumera-testnet-2", "--yes"}, "lumera-testnet-2"},
		{"equals", []string{"--chain-id=lumera-mainnet-1"}, "lumera-mainnet-1"},
		{"absent", []string{"--yes"}, ""},
		{"last wins", []string{"--chain-id", "old", "--chain-id=new"}, "new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chainIDFromInitArgs(tc.args); got != tc.want {
				t.Fatalf("chainIDFromInitArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func writeExistingInitConfig(t *testing.T, chainID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".supernode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("lumera:\n  chain_id: "+chainID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveInitChainIDUsesExistingConfig(t *testing.T) {
	writeExistingInitConfig(t, "lumera-testnet-2")
	flags := parseInitFlags([]string{"--yes"})
	if err := resolveInitChainID(flags); err != nil {
		t.Fatal(err)
	}
	if flags.chainID != "lumera-testnet-2" {
		t.Fatalf("chain ID = %q", flags.chainID)
	}
	if got := chainIDFromInitArgs(flags.supernodeArgs); got != flags.chainID {
		t.Fatalf("forwarded chain ID = %q, selected = %q", got, flags.chainID)
	}
}

func TestResolveInitChainIDRejectsExistingConfigMismatch(t *testing.T) {
	writeExistingInitConfig(t, "lumera-testnet-2")
	flags := parseInitFlags([]string{"--chain-id=lumera-mainnet-1", "--yes"})
	err := resolveInitChainID(flags)
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected chain conflict, got %v", err)
	}
}

func TestPromptForManagerConfigNonInteractiveForwardsDefaultChainID(t *testing.T) {
	flags := parseInitFlags([]string{"--yes"})
	if err := promptForManagerConfig(flags); err != nil {
		t.Fatal(err)
	}
	if flags.chainID == "" {
		t.Fatal("non-interactive init must resolve a release channel")
	}
	if got := chainIDFromInitArgs(flags.supernodeArgs); got != flags.chainID {
		t.Fatalf("forwarded chain ID = %q, selected = %q", got, flags.chainID)
	}
}
