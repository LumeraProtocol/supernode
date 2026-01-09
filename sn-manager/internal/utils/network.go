package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
	"gopkg.in/yaml.v3"
)

const testnetTagMarker = "-testnet"

// SupernodeConfigPath returns the expected path for the SuperNode config file
// based on the current process HOME/user.
func SupernodeConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".supernode", "config.yml")
}

// ReadSupernodeChainID reads lumera.chain_id from the SuperNode config file.
func ReadSupernodeChainID() (string, error) {
	path := SupernodeConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var cfg struct {
		Lumera struct {
			ChainID string `yaml:"chain_id"`
		} `yaml:"lumera"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("failed to parse supernode config %s: %w", path, err)
	}

	chainID := strings.TrimSpace(cfg.Lumera.ChainID)
	if chainID == "" {
		return "", fmt.Errorf("chain_id not set in %s", path)
	}
	return chainID, nil
}

func IsTestnetChainID(chainID string) bool {
	return strings.Contains(strings.ToLower(chainID), "testnet")
}

func IsTestnetReleaseTag(tag string) bool {
	return strings.Contains(strings.ToLower(tag), testnetTagMarker)
}

// LatestTestnetRelease returns the most recent non-draft release whose tag
// contains "-testnet". Release ordering is taken from the GitHub API response.
func LatestTestnetRelease(client github.GithubClient) (*github.Release, error) {
	releases, err := client.ListReleases()
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		if r == nil || r.Draft {
			continue
		}
		if IsTestnetReleaseTag(r.TagName) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no testnet releases found")
}

// LatestReleaseForChainID selects the appropriate "latest" release based on the
// chain ID. Testnet chains prefer "-testnet" tagged releases; otherwise it
// falls back to the latest stable release.
func LatestReleaseForChainID(client github.GithubClient, chainID string) (*github.Release, error) {
	if IsTestnetChainID(chainID) {
		if r, err := LatestTestnetRelease(client); err == nil {
			return r, nil
		}
	}
	return client.GetLatestStableRelease()
}
