package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// minimalConfig is a tiny subset of ~/.supernode/config.yml we care about
type minimalConfig struct {
	Supernode struct {
		Identity    string `yaml:"identity"`
		GatewayPort uint16 `yaml:"gateway_port"`
	} `yaml:"supernode"`
}

// ReadIdentityAndPort reads the local supernode identity and gateway port from ~/.supernode/config.yml
func ReadIdentityAndPort() (identity string, port int) {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	cfgPath := filepath.Join(home, ".supernode", "config.yml")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", 0
	}
	var c minimalConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return "", 0
	}
	p := int(c.Supernode.GatewayPort)
	if p == 0 {
		p = 8002
	}
	return c.Supernode.Identity, p
}

// FetchLocalGatewayIP polls the local HTTP gateway until it returns ip_address or timeout.
func FetchLocalGatewayIP(port int, totalWait time.Duration) (string, error) {
	deadline := time.Now().Add(totalWait)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/status", port)
	type statusResp struct {
		IP string `json:"ip_address"`
	}
	client := &http.Client{}
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		cancel()
		if err == nil && resp.StatusCode == 200 {
			var sr statusResp
			if err := json.NewDecoder(resp.Body).Decode(&sr); err == nil && sr.IP != "" {
				resp.Body.Close()
				return sr.IP, nil
			}
			if resp.Body != nil {
				resp.Body.Close()
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("gateway ip not available within %s", totalWait)
}
