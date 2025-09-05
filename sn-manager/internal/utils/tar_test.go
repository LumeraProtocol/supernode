package utils

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// helper to create a tar.gz at path with files: map[name]content
func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("hdr: %v", err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestExtractMultipleFromTarGz(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar.gz")
	files := map[string]string{
		"supernode":  "SNBIN",
		"sn-manager": "MGRBIN",
		"README.txt": "ignorable",
	}
	writeTarGz(t, tarPath, files)

	outSN := filepath.Join(dir, "out-supernode")
	outMGR := filepath.Join(dir, "out-sn-manager")
	found, err := ExtractMultipleFromTarGz(tarPath, map[string]string{
		"supernode":  outSN,
		"sn-manager": outMGR,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !found["supernode"] || !found["sn-manager"] {
		t.Fatalf("expected both binaries to be found: %v", found)
	}
	if b, _ := os.ReadFile(outSN); string(b) != "SNBIN" {
		t.Fatalf("supernode contents wrong: %q", string(b))
	}
	if b, _ := os.ReadFile(outMGR); string(b) != "MGRBIN" {
		t.Fatalf("sn-manager contents wrong: %q", string(b))
	}
}

func TestExtractFileFromTarGz(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar.gz")
	files := map[string]string{"supernode": "X", "sn-manager": "Y"}
	writeTarGz(t, tarPath, files)

	out := filepath.Join(dir, "only-supernode")
	if err := ExtractFileFromTarGz(tarPath, "supernode", out); err != nil {
		t.Fatalf("extract file: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "X" {
		t.Fatalf("content mismatch: %q", string(b))
	}
}
