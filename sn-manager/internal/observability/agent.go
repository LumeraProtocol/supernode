package observability

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AgentManager installs and runs Grafana Agent to ship system metrics to Grafana Cloud.
// This is optional and only started if Prometheus remote_write credentials are provided.
type AgentManager struct {
	HomeDir      string
	Settings     Settings
	LogsPath     string
	VersionLabel string
	Identity     string
	IPAddress    string

	binPath string
	confDir string
	conf    string
	cmd     *exec.Cmd
}

func (am *AgentManager) EnsureInstalled() error {
	binDir := filepath.Join(am.HomeDir, "binaries")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	am.binPath = filepath.Join(binDir, "grafana-agent")
	if st, err := os.Stat(am.binPath); err == nil && st.Mode()&0o111 != 0 {
		return nil
	}
	url := am.Settings.GrafanaAgentURL
	if url == "" {
		return fmt.Errorf("missing Grafana Agent URL")
	}
	tmp := am.binPath + ".tmp"
	if err := downloadFile(url, tmp); err != nil {
		return fmt.Errorf("download grafana-agent: %w", err)
	}
	// Allow either raw binary or zip archive
	if strings.HasSuffix(strings.ToLower(url), ".zip") {
		// Extract first file named like grafana-agent*
		zr, err := zip.OpenReader(tmp)
		if err != nil {
			return fmt.Errorf("open zip: %w", err)
		}
		defer zr.Close()
		var found bool
		for _, f := range zr.File {
			name := filepath.Base(f.Name)
			if f.FileInfo().IsDir() {
				continue
			}
			if strings.HasPrefix(name, "grafana-agent") {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				out, err := os.OpenFile(am.binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
				if err != nil {
					return err
				}
				if _, err := io.Copy(out, rc); err != nil {
					out.Close()
					return err
				}
				out.Close()
				found = true
				break
			}
		}
		_ = os.Remove(tmp)
		if !found {
			return fmt.Errorf("grafana-agent binary not found in zip")
		}
		return nil
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, am.binPath)
}

func (am *AgentManager) WriteConfig() error {
	am.confDir = filepath.Join(am.HomeDir, "agent")
	if err := os.MkdirAll(am.confDir, 0o755); err != nil {
		return err
	}
	am.conf = filepath.Join(am.confDir, "agent.yml")

	host, _ := os.Hostname()
	cfg := fmt.Sprintf(`server:
  log_level: info
metrics:
  wal_directory: %s
  global:
    scrape_interval: 15s
  configs:
    - name: integrations
      host_filter: false
      remote_write:
        - url: %s
          basic_auth:
            username: %s
            password: %s
integrations:
  node_exporter:
    enabled: true
    disable_collectors: ["mdadm"]

loki:
  configs:
    - name: default
      positions:
        filename: %s
      clients:
        - url: %s
          basic_auth:
            username: %s
            password: %s
      scrape_configs:
        - job_name: supernode
          static_configs:
            - targets: ["localhost"]
              labels:
                job: supernode
                service: supernode
                instance: %s
                version: %s
                identity: %s
                ip: %s
                __path__: %s
`,
		filepath.Join(am.confDir, "wal"),
		am.Settings.PromRemoteWriteURL,
		am.Settings.PromRemoteWriteUser,
		am.Settings.PromRemoteWritePass,
		filepath.Join(am.confDir, "positions.yaml"),
		am.Settings.LokiURL,
		am.Settings.LokiUser,
		am.Settings.LokiPass,
		host,
		am.VersionLabel,
		am.Identity,
		am.IPAddress,
		am.LogsPath,
	)
	return os.WriteFile(am.conf, []byte(cfg), 0o600)
}

func (am *AgentManager) Start() error {
	if am.binPath == "" || am.conf == "" {
		return fmt.Errorf("agent not prepared")
	}
	cmd := exec.Command(am.binPath, "--config.file", am.conf)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	am.cmd = cmd
	return nil
}

func (am *AgentManager) Stop() error {
	if am.cmd == nil || am.cmd.Process == nil {
		return nil
	}
	_ = am.cmd.Process.Kill()
	return nil
}
