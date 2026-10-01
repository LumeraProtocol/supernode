package config

import (
	"os"
	"testing"
)

// LEP-6 config regression tests.
//
// Coverage:
//   - Missing-block defaults are ON for storage_challenge, LEP-6 dispatch,
//     recheck, and self-healing so every updated supernode participates unless
//     chain audit params/mode no-op the runtime.
//   - Explicit local enabled:false values are ignored for participation gates;
//     local config can tune operational knobs, not opt out of protocol duties.
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

func TestLoadConfig_ExplicitFalseParticipationGatesIgnored(t *testing.T) {
	t.Parallel()

	cfg := loadConfigFromBody(t, baseConfigYAML()+`
storage_challenge:
  enabled: false
  lep6:
    enabled: false
    recheck:
      enabled: false
self_healing:
  enabled: false
`)

	if !cfg.StorageChallengeConfig.Enabled {
		t.Fatalf("storage_challenge.enabled explicit false was preserved; want forced true")
	}
	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("storage_challenge.lep6.enabled explicit false was preserved; want forced true")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("storage_challenge.lep6.recheck.enabled explicit false was preserved; want forced true")
	}
	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("self_healing.enabled explicit false was preserved; want forced true")
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

func TestLoadConfig_L6DisabledParentsAreNormalizedBeforeValidation(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"recheck_true_storage_disabled": baseConfigYAML() + `
storage_challenge:
  enabled: false
  lep6:
    enabled: true
    recheck:
      enabled: true
`,
		"recheck_true_lep6_disabled": baseConfigYAML() + `
storage_challenge:
  enabled: true
  lep6:
    enabled: false
    recheck:
      enabled: true
`,
	}

	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := loadConfigFromBody(t, body)
			if !cfg.StorageChallengeConfig.Enabled || !cfg.StorageChallengeConfig.LEP6.Enabled || !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
				t.Fatalf("participation gates must normalize true before validation: %+v", cfg.StorageChallengeConfig)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) error {
	t.Helper()
	return os.WriteFile(path, []byte(body), 0o600)
}
