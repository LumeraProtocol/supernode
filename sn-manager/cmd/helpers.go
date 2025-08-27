package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
)

// Common file markers and names used by the manager
const (
	managerPIDFile   = "sn-manager.pid"
	supernodePIDFile = "supernode.pid"
	stopMarkerFile   = ".stop_requested"
)

func checkInitialized() error {
	homeDir := config.GetManagerHome()
	configPath := filepath.Join(homeDir, "config.yml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("not initialized. Run: sn-manager init")
	}

	return nil
}

func loadConfig() (*config.Config, error) {
	homeDir := config.GetManagerHome()
	configPath := filepath.Join(homeDir, "config.yml")

	return config.Load(configPath)
}

func getHomeDir() string {
	return config.GetManagerHome()
}

// ensureSupernodeInitialized validates the user's SuperNode config exists.
func ensureSupernodeInitialized() error {
	userHome, _ := os.UserHomeDir()
	if userHome == "" {
		userHome = os.Getenv("HOME")
	}
	supernodeConfigPath := filepath.Join(userHome, ".supernode", "config.yml")
	if _, err := os.Stat(supernodeConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("SuperNode not initialized. Please run 'sn-manager init' first to configure your validator keys and network settings")
	}
	return nil
}

// readPIDFromFile loads and parses a PID from a file.
func readPIDFromFile(pidPath string) (int, error) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, nil
}

// getProcessIfAlive returns the process if it exists and is alive.
func getProcessIfAlive(pid int) (*os.Process, bool) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return nil, false
	}
	return process, true
}

// normalizeVersionTag ensures a version has a leading 'v'.
func normalizeVersionTag(v string) string {
	if v == "" {
		return v
	}
	if v[0] != 'v' {
		return "v" + v
	}
	return v
}

// newDownloadProgressPrinter returns a progress func and a done func.
// The progress func updates a single line at 10% increments; done prints a newline if needed.
func newDownloadProgressPrinter() (func(downloaded, total int64), func()) {
	lastPercent := -1
	wrote := false
	progress := func(downloaded, total int64) {
		if total <= 0 {
			return
		}
		percent := int(downloaded * 100 / total)
		if percent != lastPercent && percent%10 == 0 {
			fmt.Printf("\rProgress: %d%%", percent)
			lastPercent = percent
			wrote = true
		}
	}
	done := func() {
		if wrote {
			fmt.Println()
		}
	}
	return progress, done
}
