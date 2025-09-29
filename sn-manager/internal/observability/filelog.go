package observability

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// rotatingWriter is a simple size-based log file rotator (local only).
// It rotates when size exceeds maxBytes: file.log -> file.log.1 ...
type rotatingWriter struct {
	mu        sync.Mutex
	basePath  string
	maxBytes  int64
	keep      int
	f         *os.File
	currBytes int64
}

func newRotatingWriter(basePath string, maxBytes int64, keep int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(basePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	st, _ := f.Stat()
	rw := &rotatingWriter{basePath: basePath, maxBytes: maxBytes, keep: keep, f: f}
	if st != nil {
		rw.currBytes = st.Size()
	}
	return rw, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.currBytes+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			// best-effort: continue writing current file if rotation fails
		}
	}
	n, err := w.f.Write(p)
	w.currBytes += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	_ = w.f.Close()
	// remove oldest
	oldest := fmt.Sprintf("%s.%d", w.basePath, w.keep)
	_ = os.Remove(oldest)
	// shift
	for i := w.keep - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.basePath, i)
		dst := fmt.Sprintf("%s.%d", w.basePath, i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	// rotate current -> .1
	_ = os.Rename(w.basePath, fmt.Sprintf("%s.%d", w.basePath, 1))
	// reopen new file
	f, err := os.OpenFile(w.basePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.currBytes = 0
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

// GetSupernodeLogWriters returns stdout/stderr writers that tee to console and a rotating file.
// - baseDir: manager home (e.g., ~/.sn-manager) used to resolve paths; supernode logs are stored under ~/.supernode/logs.
func GetSupernodeLogWriters() (stdout io.Writer, stderr io.Writer, closeFn func() error, path string, err error) {
	logPath := SupernodeLogPath()

	rot, err := newRotatingWriter(logPath, 100*1024*1024 /*100MB*/, 5)
	if err != nil {
		return nil, nil, nil, "", err
	}
	closeFn = rot.Close
	// Tee to console and file
	stdout = io.MultiWriter(os.Stdout, rot)
	stderr = io.MultiWriter(os.Stderr, rot)
	return stdout, stderr, closeFn, logPath, nil
}

// SupernodeLogPath returns the absolute path to the supernode log file.
func SupernodeLogPath() string {
	userHome, _ := os.UserHomeDir()
	if userHome == "" {
		userHome = os.Getenv("HOME")
	}
	logDir := filepath.Join(userHome, ".supernode", "logs")
	return filepath.Join(logDir, "supernode.json.log")
}
