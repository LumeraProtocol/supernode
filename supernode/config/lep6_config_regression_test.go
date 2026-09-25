package config

import "testing"

// LEP-6 config regression tests.
//
// Coverage:
//   - Missing-block defaults are ON for storage_challenge, LEP-6 dispatch,
//     recheck, and self-healing so testnet operators get storage-truth
//     runtime after update.
//   - Explicit `enabled:false` is overridden so operators cannot bypass
//     network credibility checks locally.

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

func TestLoadConfig_LEP6ExplicitFalseForcedOnRegression(t *testing.T) {
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
		t.Fatalf("explicit storage_challenge.enabled=false must be overridden")
	}
	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("explicit storage_challenge.lep6.enabled=false must be overridden")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("explicit recheck.enabled=false must be overridden")
	}
	if !cfg.SelfHealingConfig.Enabled {
		t.Fatalf("explicit self_healing.enabled=false must be overridden")
	}
	if got := cfg.LEP6OperatorOptInAdvisory(); got != "" {
		t.Fatalf("advisory should be empty after forced enablement; got %q", got)
	}
}

func TestLoadConfig_L6_ExplicitDisabledParentsForcedOnBeforeValidation(t *testing.T) {
	t.Parallel()

	cfg := loadConfigFromBody(t, baseConfigYAML()+`
storage_challenge:
  enabled: false
  lep6:
    enabled: false
    recheck:
      enabled: true
`)

	if !cfg.StorageChallengeConfig.Enabled {
		t.Fatalf("storage_challenge.enabled=false should be forced true before validation")
	}
	if !cfg.StorageChallengeConfig.LEP6.Enabled {
		t.Fatalf("storage_challenge.lep6.enabled=false should be forced true before validation")
	}
	if !cfg.StorageChallengeConfig.LEP6.Recheck.Enabled {
		t.Fatalf("recheck.enabled=true should remain true")
	}
}
