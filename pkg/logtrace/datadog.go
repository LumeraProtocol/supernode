package logtrace

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// Minimal Datadog Logs Forwarder (hard-coded config) kept separate for cleanliness.

type ddCfg struct {
	APIKey  string
	Site    string // e.g. "datadoghq.com", "datadoghq.eu"
	Service string // e.g. used as Datadog 'service'; we will set to node IP
	Host    string // optional; defaults to machine hostname
}

var (
	ddOnce   sync.Once
	ddConfig ddCfg
	ddClient = &http.Client{Timeout: 5 * time.Second}
	ddQueue  chan map[string]any
	// Optional build-time injection via -ldflags
	//   -ldflags "-X github.com/LumeraProtocol/supernode/v2/pkg/logtrace.DDAPIKey=... -X github.com/LumeraProtocol/supernode/v2/pkg/logtrace.DDSite=us5.datadoghq.com"
	DDAPIKey string
	DDSite   string
)

// SetupDatadog initializes the Datadog forwarding once.
func SetupDatadog(service string) {
	ddOnce.Do(func() {
		initDatadog(service)
	})
}

// ForwardDatadog enqueues a log line for forwarding (non-blocking).
func ForwardDatadog(level zapcore.Level, ctx context.Context, msg string, fields Fields) {
	ddForward(level, ctx, msg, fields)
}

// SetDatadogService allows setting the Datadog service (e.g., to the node IP)
func SetDatadogService(service string) {
	if s := strings.TrimSpace(service); s != "" {
		ddConfig.Service = s
	}
}

// SetDatadogHost sets the Datadog host field (use the supernode identity)
func SetDatadogHost(host string) {
	if h := strings.TrimSpace(host); h != "" {
		ddConfig.Host = h
	}
}

func initDatadog(service string) {
	// Base defaults (site default chosen based on earlier validation)
	ddConfig = ddCfg{Site: "us5.datadoghq.com", Service: service, Host: ""}

	// Resolve from env and build flags
	apiKey := strings.TrimSpace(os.Getenv("DD_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(DDAPIKey)
	}

	site := strings.TrimSpace(os.Getenv("DD_SITE"))
	if site == "" {
		site = strings.TrimSpace(DDSite)
		if site == "" {
			site = ddConfig.Site
		}
	}

	ddConfig.APIKey = apiKey
	ddConfig.Site = site

	// Only enable forwarding when a real key is present
	if ddConfig.APIKey == "" {
		return
	}

	ddQueue = make(chan map[string]any, 256)
	go ddLoop()
}

// ddForward enqueues a single log entry for Datadog intake.
func ddForward(level zapcore.Level, ctx context.Context, msg string, fields Fields) {
	if ddQueue == nil {
		return
	}

	// Map zap level to Datadog status
	status := "info"
	switch level {
	case zapcore.DebugLevel:
		status = "debug"
	case zapcore.InfoLevel:
		status = "info"
	case zapcore.WarnLevel:
		status = "warn"
	case zapcore.ErrorLevel:
		status = "error"
	case zapcore.FatalLevel:
		status = "critical"
	}

	// Build a compact attributes map
	attrs := map[string]any{}
	for k, v := range fields {
		attrs[k] = v
	}
	// Attach correlation ID if present
	if cid := extractCorrelationID(ctx); cid != "unknown" {
		attrs["correlation_id"] = cid
	}

	entry := map[string]any{
		"message":    msg,
		"status":     status,
		"service":    ddConfig.Service,
		"host":       ddConfig.Host,
		"attributes": attrs, // avoid collisions with top-level fields
	}

	select {
	case ddQueue <- entry:
	default:
		// drop if queue is full to avoid blocking critical paths
	}
}

// ddLoop batches log entries and sends to Datadog intake.
func ddLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	batch := make([]map[string]any, 0, 32)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Marshal batch
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(batch); err != nil {
			batch = batch[:0]
			return
		}
		_ = ddPost(buf.Bytes())
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-ddQueue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= 32 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func ddPost(payload []byte) error {
	url := "https://http-intake.logs." + strings.TrimSpace(ddConfig.Site) + "/api/v2/logs"

	// gzip the JSON payload
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(payload); err == nil {
		_ = gw.Close()
	} else {
		_ = gw.Close()
		gzBuf = *bytes.NewBuffer(payload)
	}

	req, err := http.NewRequest(http.MethodPost, url, &gzBuf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("DD-API-KEY", ddConfig.APIKey)

	resp, err := ddClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
