package config

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		SupernodeConfig        SupernodeConfig        `yaml:"supernode"`
		KeyringConfig          KeyringConfig          `yaml:"keyring"`
		P2PConfig              P2PConfig              `yaml:"p2p"`
		LumeraClientConfig     LumeraClientConfig     `yaml:"lumera"`
		RaptorQConfig          RaptorQConfig          `yaml:"raptorq"`
		StorageChallengeConfig StorageChallengeConfig `yaml:"storage_challenge"`
		SelfHealingConfig      SelfHealingConfig      `yaml:"self_healing"`
	}
	var out raw
	if err := value.Decode(&out); err != nil {
		return err
	}
	c.SupernodeConfig = out.SupernodeConfig
	c.KeyringConfig = out.KeyringConfig
	c.P2PConfig = out.P2PConfig
	c.LumeraClientConfig = out.LumeraClientConfig
	c.RaptorQConfig = out.RaptorQConfig
	c.StorageChallengeConfig = out.StorageChallengeConfig
	c.SelfHealingConfig = out.SelfHealingConfig
	return nil
}

func (c *StorageChallengeLEP6Config) UnmarshalYAML(value *yaml.Node) error {
	type raw StorageChallengeLEP6Config
	var out raw
	if err := value.Decode(&out); err != nil {
		return err
	}
	*c = StorageChallengeLEP6Config(out)
	c.enabledSet = hasYAMLKey(value, "enabled")
	return nil
}

func (c *StorageRecheckConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw StorageRecheckConfig
	var out raw
	if err := value.Decode(&out); err != nil {
		return err
	}
	*c = StorageRecheckConfig(out)
	c.enabledSet = hasYAMLKey(value, "enabled")
	return nil
}

func (c *SelfHealingConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw SelfHealingConfig
	var out raw
	if err := value.Decode(&out); err != nil {
		return err
	}
	*c = SelfHealingConfig(out)
	c.enabledSet = hasYAMLKey(value, "enabled")
	return nil
}

func hasYAMLKey(value *yaml.Node, key string) bool {
	if value == nil || value.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == key {
			return true
		}
	}
	return false
}

func (c *Config) applyLEP6DefaultsAndValidate() error {
	if !c.StorageChallengeConfig.LEP6.enabledSet {
		c.StorageChallengeConfig.LEP6.Enabled = true
	}
	if c.StorageChallengeConfig.LEP6.MaxConcurrentTargets == 0 {
		c.StorageChallengeConfig.LEP6.MaxConcurrentTargets = DefaultLEP6MaxConcurrentTargets
	}
	if c.StorageChallengeConfig.LEP6.RecipientReadTimeout == 0 {
		c.StorageChallengeConfig.LEP6.RecipientReadTimeout = DefaultLEP6RecipientReadTimeout
	}

	recheck := &c.StorageChallengeConfig.LEP6.Recheck
	if !recheck.enabledSet {
		recheck.Enabled = true
	}
	if recheck.LookbackEpochs == 0 {
		recheck.LookbackEpochs = DefaultLEP6RecheckLookbackEpochs
	}
	if recheck.MaxPerTick == 0 {
		recheck.MaxPerTick = DefaultLEP6RecheckMaxPerTick
	}
	if recheck.TickIntervalMs == 0 {
		recheck.TickIntervalMs = int(DefaultLEP6RecheckTickInterval / time.Millisecond)
	}
	if recheck.MaxFailureAttemptsPerTicket == 0 {
		recheck.MaxFailureAttemptsPerTicket = DefaultLEP6RecheckMaxFailureAttemptsPerTicket
	}
	if recheck.FailureBackoffTTLms == 0 {
		recheck.FailureBackoffTTLms = int(DefaultLEP6RecheckFailureBackoffTTL / time.Millisecond)
	}

	if !c.SelfHealingConfig.enabledSet {
		c.SelfHealingConfig.Enabled = true
	}
	if c.SelfHealingConfig.PollIntervalMs == 0 {
		c.SelfHealingConfig.PollIntervalMs = int(DefaultSelfHealingPollInterval / time.Millisecond)
	}
	if c.SelfHealingConfig.MaxConcurrentReconstructs == 0 {
		c.SelfHealingConfig.MaxConcurrentReconstructs = DefaultSelfHealingMaxConcurrentReconstructs
	}
	if c.SelfHealingConfig.MaxConcurrentVerifications == 0 {
		c.SelfHealingConfig.MaxConcurrentVerifications = DefaultSelfHealingMaxConcurrentVerifications
	}
	if c.SelfHealingConfig.MaxConcurrentPublishes == 0 {
		c.SelfHealingConfig.MaxConcurrentPublishes = DefaultSelfHealingMaxConcurrentPublishes
	}
	if strings.TrimSpace(c.SelfHealingConfig.StagingDir) == "" {
		c.SelfHealingConfig.StagingDir = DefaultSelfHealingStagingDir
	}
	if c.SelfHealingConfig.VerifierFetchTimeoutMs == 0 {
		c.SelfHealingConfig.VerifierFetchTimeoutMs = int(DefaultSelfHealingVerifierFetchTimeout / time.Millisecond)
	}
	if c.SelfHealingConfig.VerifierFetchAttempts == 0 {
		c.SelfHealingConfig.VerifierFetchAttempts = DefaultSelfHealingVerifierFetchAttempts
	}
	if c.SelfHealingConfig.VerifierBackoffBaseMs == 0 {
		c.SelfHealingConfig.VerifierBackoffBaseMs = int(DefaultSelfHealingVerifierBackoffBase / time.Millisecond)
	}
	if c.SelfHealingConfig.AuditQueryTimeoutMs == 0 {
		c.SelfHealingConfig.AuditQueryTimeoutMs = int(DefaultSelfHealingAuditQueryTimeout / time.Millisecond)
	}

	return c.validateLEP6Config()
}

func (c *Config) validateLEP6Config() error {
	lep6 := c.StorageChallengeConfig.LEP6
	if lep6.MaxConcurrentTargets < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.max_concurrent_targets must be >= 0")
	}
	if lep6.RecipientReadTimeout < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.recipient_read_timeout must be >= 0")
	}
	if lep6.Recheck.MaxPerTick < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.recheck.max_per_tick must be >= 0")
	}
	if lep6.Recheck.TickIntervalMs < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.recheck.tick_interval_ms must be >= 0")
	}
	if lep6.Recheck.MaxFailureAttemptsPerTicket < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.recheck.max_failure_attempts_per_ticket must be >= 0")
	}
	if lep6.Recheck.FailureBackoffTTLms < 0 {
		return fmt.Errorf("LEP-6 config: storage_challenge.lep6.recheck.failure_backoff_ttl_ms must be >= 0")
	}
	sh := c.SelfHealingConfig
	if sh.PollIntervalMs < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.poll_interval_ms must be >= 0")
	}
	if sh.MaxConcurrentReconstructs < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.max_concurrent_reconstructs must be >= 0")
	}
	if sh.MaxConcurrentVerifications < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.max_concurrent_verifications must be >= 0")
	}
	if sh.MaxConcurrentPublishes < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.max_concurrent_publishes must be >= 0")
	}
	if sh.VerifierFetchTimeoutMs < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.verifier_fetch_timeout_ms must be >= 0")
	}
	if sh.VerifierFetchAttempts < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.verifier_fetch_attempts must be >= 0")
	}
	if sh.VerifierBackoffBaseMs < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.verifier_backoff_base_ms must be >= 0")
	}
	if sh.AuditQueryTimeoutMs < 0 {
		return fmt.Errorf("LEP-6 config: self_healing.audit_query_timeout_ms must be >= 0")
	}
	return nil
}
