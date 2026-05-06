package config

import (
	"fmt"
	"strings"
)

// LEP6OperatorOptInAdvisory returns a non-empty advisory string when this
// supernode is opted out of any LEP-6 service. Empty string means everything
// is opted in.
//
// LEP-6 review C1 (Matee, 2026-05-06): pre-Wave-4 the missing-block default
// for LEP-6 toggles was TRUE, which silently auto-opted operators in. After
// Wave 4 the default is FALSE — but that opens the inverse risk: an operator
// who upgrades and forgets to add the toggles now silently opts OUT while
// the chain may be enforcing SOFT/FULL. This helper produces a startup
// advisory that supernode/cmd/start.go logs at WARN so operators can tell
// at a glance which LEP-6 services are off. The chain enforcement mode
// remains authoritative — even when toggles are TRUE every LEP-6 service
// no-ops while the chain is UNSPECIFIED, so the safe failure mode is
// "operator opts in unconditionally; chain decides when work happens".
//
// The advisory is purely informational; it does not change any behaviour.
func (c *Config) LEP6OperatorOptInAdvisory() string {
	disabled := make([]string, 0, 3)
	if !c.StorageChallengeConfig.LEP6.Enabled {
		disabled = append(disabled, "storage_challenge.lep6.enabled=false")
	}
	if !c.StorageChallengeConfig.LEP6.Recheck.Enabled {
		disabled = append(disabled, "storage_challenge.lep6.recheck.enabled=false")
	}
	if !c.SelfHealingConfig.Enabled {
		disabled = append(disabled, "self_healing.enabled=false")
	}
	if len(disabled) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"LEP-6: operator opted out of one or more services — [%s]; "+
			"this supernode will not produce reports / heal-ops / recheck evidence for the disabled services. "+
			"If chain enforcement is SOFT or FULL this can incur scoring penalties; flip the toggles in supernode.yml to opt in",
		strings.Join(disabled, ", "),
	)
}
