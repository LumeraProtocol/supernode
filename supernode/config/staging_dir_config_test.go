package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestM1_StagingDirResolvesAgainstBaseDir pins LEP-6 review M1: the default
// `heal-staging` is relative; supernode/cmd/start.go must resolve it against
// appConfig.BaseDir via GetFullPath BEFORE handing it to the self-healing
// service. We validate the resolution helper directly.
func TestM1_StagingDirResolvesAgainstBaseDir(t *testing.T) {
	t.Parallel()

	baseDir := "/var/lib/supernode"
	c := &Config{BaseDir: baseDir, SelfHealingConfig: SelfHealingConfig{StagingDir: DefaultSelfHealingStagingDir}}

	got := c.GetFullPath(c.SelfHealingConfig.StagingDir)
	want := filepath.Join(baseDir, DefaultSelfHealingStagingDir)
	if got != want {
		t.Fatalf("M1: GetFullPath relative resolution = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, baseDir+string(filepath.Separator)) {
		t.Fatalf("M1: resolved path %q is not under base dir %q", got, baseDir)
	}

	// Absolute path stays absolute (no double-prepend).
	c.SelfHealingConfig.StagingDir = "/srv/heal-staging"
	if got := c.GetFullPath(c.SelfHealingConfig.StagingDir); got != "/srv/heal-staging" {
		t.Fatalf("M1: absolute path mangled = %q, want /srv/heal-staging", got)
	}
}
