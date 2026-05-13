package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LEP-6 review regression: LEP-6 PR286 review fix regression tests.
//
// Coverage:
//   - C1: missing-block default for LEP-6 toggles is FALSE (no silent
//     upgrade-time opt-in). Already covered structurally by
//     TestLoadConfig_LEP6SafeDefaults; this file adds focused negative
//     cases (wrong-direction default would cause auto-opt-in) and the
//     advisory helper.
//   - L6: structural validator rejects recheck=true with disabled parents.
//     Before this fix, fixtures could carry recheck.enabled=true while
//     storage_challenge.enabled=false, silently no-op'd at runtime.

func TestLoadConfig_C1_MissingBlocksDefaultDisabled(t *testing.T) {
	t.Parallel()

	// No LEP-6 / recheck / self_healing block at all — defaults must be FALSE.
	cfg := loadConfigFromBody(t, baseConfigYAML())

	if cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("C1: storage_challenge.lep6.enabled = true on missing-block; want false (no silent opt-in)")
	}
	if cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("C1: storage_challenge.lep6.recheck.enabled = true on missing-block; want false")
	}
	if cfg.SelfHealingConfig.Enabled {
		t.Fatalf("C1: self_healing.enabled = true on missing-block; want false")
	}
}

func TestLoadConfig_C1_ExplicitTrueRespected(t *testing.T) {
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
		t.Fatalf("C1: explicit storage_challenge.lep6.enabled=true must be respected")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("C1: explicit recheck.enabled=true must be respected")
	}
	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("C1: explicit self_healing.enabled=true must be respected")
	}
}

func TestLoadConfig_C1_OptInAdvisory(t *testing.T) {
	t.Parallel()

	// All three opted out — advisory must mention each disabled service.
	allOff := loadConfigFromBody(t, baseConfigYAML())
	advisory := allOff.LEP6OperatorOptInAdvisory()
	if advisory == "" {
		t.Fatalf("C1: advisory must be non-empty when toggles are off")
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

	// All three opted in — advisory must be empty.
	allOn := loadConfigFromBody(t, baseConfigYAML()+`
storage_challenge:
  enabled: true
  lep6:
    enabled: true
    recheck:
      enabled: true
self_healing:
  enabled: true
`)
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
