package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LEP-6 config regression tests.
//
// Coverage:
//   - Missing-block defaults are ON for storage_challenge, LEP-6 dispatch,
//     recheck, and self-healing so testnet operators get storage-truth
//     runtime after update unless they explicitly emergency-disable it.
//   - L6: structural validator rejects recheck=true with disabled parents.

func TestLoadConfig_MissingBlocksDefaultEnabled(t *testing.T) {
	t.Parallel()

	// No storage_challenge / LEP-6 / recheck / self_healing block at all — defaults must be TRUE.
	cfg := loadConfigFromBody(t, baseConfigYAML())

	if !cfg.StorageChallengeConfig.Enabled {
		t.Fatalf("storage_challenge.enabled = false on missing-block; want true")
	}
	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("storage_challenge.lep6.enabled = false on missing-block; want true")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("storage_challenge.lep6.recheck.enabled = false on missing-block; want true")
	}
	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("self_healing.enabled = false on missing-block; want true")
	}
}

func TestLoadConfig_ExplicitTrueRespected(t *testing.T) {
	t.Parallel()

	cfg := loadConfigFromBody(t, baseConfigYAML()+`
storage_challenge:
  enabled: true
  lep6:
    enabled: true
    recheck:
      enabled: true
self_healing:
  enabled: true
`)

	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("explicit storage_challenge.lep6.enabled=true must be respected")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("explicit recheck.enabled=true must be respected")
	}
	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("explicit self_healing.enabled=true must be respected")
	}
}

func TestLoadConfig_LEP6OperatorOptInAdvisory(t *testing.T) {
	t.Parallel()

	// Explicitly opted out — advisory must mention each disabled service.
	allOff := loadConfigFromBody(t, baseConfigYAML()+`
storage_challenge:
  enabled: true
  lep6:
    enabled: false
    recheck:
      enabled: false
self_healing:
  enabled: false
`)
	advisory := allOff.LEP6OperatorOptInAdvisory()
	if advisory == "" {
		t.Fatalf("advisory must be non-empty when toggles are off")
	}
	for _, want := range []string{
		"storage_challenge.lep6.enabled=false",
		"storage_challenge.lep6.recheck.enabled=false",
		"self_healing.enabled=false",
	} {
		if !strings.Contains(advisory, want) {
			t.Fatalf("C1 advisory missing %q in:\n%s", want, advisory)
		}
	}

	// Missing blocks now default on — advisory must be empty.
	allOn := loadConfigFromBody(t, baseConfigYAML())
	if got := allOn.LEP6OperatorOptInAdvisory(); got != "" {
		t.Fatalf("C1 advisory should be empty when all opted in; got %q", got)
	}
}

func TestLoadConfig_L6_RecheckRequiresParents(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body         string
		wantErrMatch string
	}{
		"recheck_true_storage_disabled": {
			body: baseConfigYAML() + `
storage_challenge:
  enabled: false
  lep6:
    enabled: true
    recheck:
      enabled: true
`,
			wantErrMatch: "storage_challenge.enabled=true",
		},
		"recheck_true_lep6_disabled": {
			body: baseConfigYAML() + `
storage_challenge:
  enabled: true
  lep6:
    enabled: false
    recheck:
      enabled: true
`,
			wantErrMatch: "storage_challenge.lep6.enabled=true",
		},
	}

	for name, tc := range cases {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "supernode.yml")
			if err := writeFile(t, path, tc.body); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := LoadConfig(path, dir)
			if err == nil {
				t.Fatalf("L6: LoadConfig succeeded; want validator rejection for %s", name)
			}
			if !strings.Contains(err.Error(), tc.wantErrMatch) {
				t.Fatalf("L6: error %q does not contain %q", err.Error(), tc.wantErrMatch)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) error {
	t.Helper()
	return os.WriteFile(path, []byte(body), 0o600)
}
