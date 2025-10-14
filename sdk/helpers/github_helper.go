package helpers

import (
	"strings"
	"sync"

	gh "github.com/LumeraProtocol/supernode/v2/pkg/github"
)

var (
	requiredSupernodeVersion string
	requiredVersionOnce      sync.Once
)

// ResolveRequiredSupernodeVersion returns the latest stable SuperNode tag from GitHub.
// The value is fetched once per process and cached. If lookup fails, it returns
// an empty string so callers can gracefully skip strict version gating.
func ResolveRequiredSupernodeVersion() string {
	requiredVersionOnce.Do(func() {
		client := gh.NewClient("LumeraProtocol/supernode")
		if client != nil {
			if release, err := client.GetLatestStableRelease(); err == nil {
				if tag := strings.TrimSpace(release.TagName); tag != "" {
					requiredSupernodeVersion = tag
					return
				}
			}
		}
		requiredSupernodeVersion = ""
	})
	return requiredSupernodeVersion
}
