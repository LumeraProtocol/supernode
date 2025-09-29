package observability

import "os"

// Settings holds external service configuration (embedded at build time via ldflags).
type Settings struct {
	// Logs (Loki)
	LokiURL  string // e.g., https://logs-prod-<region>.grafana.net/loki/api/v1/push
	LokiUser string // Grafana Cloud stack ID
	LokiPass string // Grafana Cloud API token (logs:write)

	// Metrics (Grafana Cloud Prometheus via Grafana Agent)
	PromRemoteWriteURL  string // e.g., https://prometheus-prod-<region>.grafana.net/api/prom/push
	PromRemoteWriteUser string // Grafana Cloud stack ID
	PromRemoteWritePass string // Grafana Cloud API token (metrics:write)

	GrafanaAgentURL string // direct URL to linux-amd64 grafana-agent binary
}

// Build-time variables (populated via -ldflags in CI).
var (
	BuildLokiURL  string
	BuildLokiUser string
	BuildLokiPass string

	BuildPromURL  string
	BuildPromUser string
	BuildPromPass string
)

// Default agent binary URL
const DefaultGrafanaAgentURL = "https://github.com/grafana/agent/releases/download/v0.44.2/grafana-agent-linux-amd64.zip"

// No default credentials in code. Values come from build-time ldflags.

// FromBuild returns settings sourced from build-time variables only.
func FromBuild() Settings {
	return Settings{
		LokiURL:             BuildLokiURL,
		LokiUser:            BuildLokiUser,
		LokiPass:            BuildLokiPass,
		PromRemoteWriteURL:  BuildPromURL,
		PromRemoteWriteUser: BuildPromUser,
		PromRemoteWritePass: BuildPromPass,
		GrafanaAgentURL:     DefaultGrafanaAgentURL,
	}
}

// FromEnvOrBuild returns settings using build-time values, falling back to
// environment variables when build values are empty.
func FromEnvOrBuild() Settings {
	s := FromBuild()
	if s.LokiURL == "" {
		s.LokiURL = os.Getenv("GC_LOKI_URL")
	}
	if s.LokiUser == "" {
		s.LokiUser = os.Getenv("GC_LOKI_USER")
	}
	if s.LokiPass == "" {
		s.LokiPass = os.Getenv("GC_LOKI_PASS")
	}

	if s.PromRemoteWriteURL == "" {
		s.PromRemoteWriteURL = os.Getenv("GC_PROM_URL")
	}
	if s.PromRemoteWriteUser == "" {
		s.PromRemoteWriteUser = os.Getenv("GC_PROM_USER")
	}
	if s.PromRemoteWritePass == "" {
		s.PromRemoteWritePass = os.Getenv("GC_PROM_PASS")
	}

	if s.GrafanaAgentURL == "" {
		s.GrafanaAgentURL = os.Getenv("GRAFANA_AGENT_URL")
	}
	return s
}
