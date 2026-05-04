package system

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const systemTestHardCleanupEnv = "SYSTEM_TEST_HARD_CLEANUP"

type listeningProcess struct {
	PID     int
	Command string
	Port    int
}

type processPortSet struct {
	Command string
	Ports   map[int]struct{}
}

func performSystemTestRuntimeCleanup(nodesCount int, verbose bool) error {
	if !isSystemTestHardCleanupEnabled() {
		return nil
	}

	lsofPath, err := exec.LookPath("lsof")
	if err != nil {
		if verbose {
			fmt.Printf("system tests: skipping hard cleanup (%s not found)\n", "lsof")
		}
		return nil
	}

	ports := systemTestReservedPorts(nodesCount)
	targetsByPID := make(map[int]*processPortSet)
	blockers := make([]string, 0)

	for _, port := range ports {
		listeners, err := listListeningProcesses(lsofPath, port)
		if err != nil {
			return fmt.Errorf("list listeners on port %d: %w", port, err)
		}
		for _, p := range listeners {
			cmd := normalizeCommandName(p.Command)
			if !isSystemTestProcess(cmd) {
				blockers = append(blockers, fmt.Sprintf("port %d occupied by non-test process %q (pid=%d)", port, cmd, p.PID))
				continue
			}

			meta, ok := targetsByPID[p.PID]
			if !ok {
				meta = &processPortSet{Command: cmd, Ports: make(map[int]struct{})}
				targetsByPID[p.PID] = meta
			}
			meta.Ports[port] = struct{}{}
		}
	}

	if len(blockers) > 0 {
		sort.Strings(blockers)
		return fmt.Errorf("%s", strings.Join(blockers, "; "))
	}

	pids := make([]int, 0, len(targetsByPID))
	for pid := range targetsByPID {
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	for _, pid := range pids {
		meta := targetsByPID[pid]
		if verbose {
			fmt.Printf("system tests: cleaning stale process pid=%d cmd=%s ports=%v\n", pid, meta.Command, sortedPorts(meta.Ports))
		}
		if err := terminateProcess(pid, 2*time.Second); err != nil {
			return fmt.Errorf("terminate stale process pid=%d cmd=%s: %w", pid, meta.Command, err)
		}
	}

	return nil
}

func isSystemTestHardCleanupEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(systemTestHardCleanupEnv)))
	switch v {
	case "", "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func systemTestReservedPorts(nodesCount int) []int {
	if nodesCount <= 0 {
		nodesCount = 4
	}

	ports := make(map[int]struct{})
	addRange := func(start, end int) {
		for p := start; p <= end; p++ {
			ports[p] = struct{}{}
		}
	}

	// lumerad testnet ports (safety ranges include small offsets for prior failed runs)
	addRange(16656, 16656+nodesCount+2) // CometBFT P2P
	addRange(26656, 26656+nodesCount+2) // CometBFT P2P/RPC defaults
	addRange(26657, 26657+nodesCount+2) // CometBFT RPC
	addRange(9090, 9090+nodesCount+2)   // gRPC
	addRange(1317, 1317+nodesCount+2)   // API
	addRange(6060, 6060+nodesCount+2)   // pprof
	addRange(7180, 7180+nodesCount+2)   // telemetry

	// supernode system-test ports
	addRange(4444, 4449)
	addRange(8002, 8004)
	ports[50051] = struct{}{} // optional RaptorQ service

	out := make([]int, 0, len(ports))
	for p := range ports {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func listListeningProcesses(lsofPath string, port int) ([]listeningProcess, error) {
	cmd := exec.Command(lsofPath, "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fpc")
	out, err := cmd.Output()
	if err != nil {
		// lsof exits non-zero when no matches are found.
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, err
	}

	byPID := make(map[int]listeningProcess)
	currentPID := 0
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, convErr := strconv.Atoi(line[1:])
			if convErr != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
			if _, exists := byPID[pid]; !exists {
				byPID[pid] = listeningProcess{PID: pid, Port: port}
			}
		case 'c':
			if currentPID == 0 {
				continue
			}
			p := byPID[currentPID]
			p.Command = line[1:]
			byPID[currentPID] = p
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	listeners := make([]listeningProcess, 0, len(byPID))
	for _, p := range byPID {
		listeners = append(listeners, p)
	}
	sort.Slice(listeners, func(i, j int) bool {
		return listeners[i].PID < listeners[j].PID
	})
	return listeners, nil
}

func normalizeCommandName(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	cmd = strings.Fields(cmd)[0]
	return filepath.Base(cmd)
}

func isSystemTestProcess(command string) bool {
	return strings.HasPrefix(command, "lumerad") || strings.HasPrefix(command, "supernode")
}

func terminateProcess(pid int, timeout time.Duration) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	_ = proc.Signal(syscall.SIGTERM)
	if waitForProcessExit(pid, timeout) {
		return nil
	}

	_ = proc.Signal(syscall.SIGKILL)
	if waitForProcessExit(pid, timeout) {
		return nil
	}

	return fmt.Errorf("process %d did not exit", pid)
}

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !processExists(pid)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil || err == syscall.EPERM {
		return true
	}
	return false
}

func sortedPorts(portSet map[int]struct{}) []int {
	ports := make([]int, 0, len(portSet))
	for p := range portSet {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}
