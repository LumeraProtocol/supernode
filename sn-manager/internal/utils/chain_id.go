package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// supernodeYAML captures the fields sn-manager reads from ~/.supernode/config.yml.
// Keep this struct in sync with new preflight/rollback readers below.
type supernodeYAML struct {
	Supernode struct {
		EVMKeyName string `yaml:"evm_key_name"`
	} `yaml:"supernode"`
	Lumera struct {
		ChainID  string `yaml:"chain_id"`
		GRPCAddr string `yaml:"grpc_addr"`
	} `yaml:"lumera"`
}

func readSupernodeYAML() (*supernodeYAML, string, error) {
	path := SupernodeConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	var cfg supernodeYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, path, fmt.Errorf("failed to parse supernode config %s: %w", path, err)
	}
	return &cfg, path, nil
}

// ReadSupernodeChainID reads lumera.chain_id from the SuperNode config file.
func ReadSupernodeChainID() (string, error) {
	cfg, path, err := readSupernodeYAML()
	if err != nil {
		return "", err
	}
	chainID := strings.TrimSpace(cfg.Lumera.ChainID)
	if chainID == "" {
		return "", fmt.Errorf("chain_id not set in %s", path)
	}
	return chainID, nil
}

// ReadSupernodeEVMKeyName returns supernode.evm_key_name (trimmed). An empty
// string is returned if the field is absent or empty. It is NOT an error for
// the field to be missing — an unmigrated node is the exact condition the
// preflight/rollback logic looks for.
func ReadSupernodeEVMKeyName() (string, error) {
	cfg, _, err := readSupernodeYAML()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Supernode.EVMKeyName), nil
}

// ReadSupernodeGRPCAddr returns lumera.grpc_addr (trimmed). Empty if unset.
func ReadSupernodeGRPCAddr() (string, error) {
	cfg, _, err := readSupernodeYAML()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Lumera.GRPCAddr), nil
}

// SupernodeConfigMTime returns the modification time of ~/.supernode/config.yml.
// Used by the preflight to detect operator remediation between check cycles.
func SupernodeConfigMTime() (time.Time, error) {
	info, err := os.Stat(SupernodeConfigPath())
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
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
		return LatestTestnetRelease(client)
	}
	return client.GetLatestStableRelease()
}
