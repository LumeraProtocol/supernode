package utils

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lukechampine.com/blake3"
)

func TestChunkSizeFor(t *testing.T) {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)

	cases := []struct {
		name  string
		input int64
		want  int64
	}{
		{"unknownOrZero", 0, 1 * mib},
		{"negative", -1, 1 * mib},
		{"under4MiB", 3*mib + 512*kib, 512 * kib},
		{"exact4MiB", 4 * mib, 512 * kib},
		{"justOver4MiB", 4*mib + 1, 1 * mib},
		{"exact32MiB", 32 * mib, 1 * mib},
		{"justOver32MiB", 32*mib + 1, 2 * mib},
		{"exact2GiB", 2 * gib, 2 * mib},
		{"above2GiB", 2*gib + 1, 4 * mib},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := chunkSizeFor(tc.input); got != tc.want {
				t.Fatalf("chunkSizeFor(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestBlake3Hash(t *testing.T) {
	t.Parallel()

	msg := []byte(strings.Repeat("blake3 data", 1024))
	want := blake3.Sum256(msg)

	got, err := blake3Hash(msg, 0)
	if err != nil {
		t.Fatalf("blake3Hash returned error: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("hash mismatch for auto chunking")
	}

	got, err = blake3Hash(msg, 512)
	if err != nil {
		t.Fatalf("blake3Hash with buf size returned error: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("hash mismatch with explicit chunk size")
	}
}

func TestBlake3HashFileWithChunkSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	blakeHex := func(data []byte) string {
		sum := blake3.Sum256(data)
		return hex.EncodeToString(sum[:])
	}

	createFile := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		return path
	}

	smallData := []byte(strings.Repeat("0123456789abcdef", 1<<10))
	emptyData := []byte{}
	largeData := make([]byte, 5<<20) // 5 MiB zeroed payload

	smallFile := createFile("small.bin", smallData)
	emptyFile := createFile("empty.bin", emptyData)
	largeFile := createFile("large.bin", largeData)

	tests := []struct {
		name      string
		path      string
		chunkSize int64
		wantHex   string
		wantErr   bool
	}{
		{
			name:      "small file",
			path:      smallFile,
			chunkSize: 4 << 10,
			wantHex:   blakeHex(smallData),
		},
		{
			name:      "empty file",
			path:      emptyFile,
			chunkSize: 1 << 10,
			wantHex:   blakeHex(emptyData),
		},
		{
			name:      "large file",
			path:      largeFile,
			chunkSize: 1 << 20,
			wantHex:   blakeHex(largeData),
		},
		{
			name:      "file missing",
			path:      filepath.Join(dir, "missing.bin"),
			chunkSize: 4 << 10,
			wantErr:   true,
		},
		{
			name:      "zero chunk uses default",
			path:      smallFile,
			chunkSize: 0,
			wantHex:   blakeHex(smallData),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Blake3HashFileWithChunkSize(tc.path, tc.chunkSize)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error mismatch: wantErr=%v err=%v", tc.wantErr, err)
			}
			if tc.wantErr {
				return
			}

			gotHex := hex.EncodeToString(got)
			if gotHex != tc.wantHex {
				t.Fatalf("hash mismatch: got %s want %s", gotHex, tc.wantHex)
			}
		})
	}
}

func TestBlake3HashWrapper(t *testing.T) {
	t.Parallel()

	msg := []byte(strings.Repeat("wrapper data", 512))
	want := blake3.Sum256(msg)

	got, err := Blake3Hash(msg)
	if err != nil {
		t.Fatalf("Blake3Hash returned error: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("Blake3Hash returned unexpected digest")
	}
}

func TestBlake3HashWithChunkSizeWrapper(t *testing.T) {
	t.Parallel()

	msg := []byte(strings.Repeat("chunked data", 256))
	want := blake3.Sum256(msg)

	got, err := Blake3HashWithChunkSize(msg, 1<<10)
	if err != nil {
		t.Fatalf("Blake3HashWithChunkSize returned error: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("Blake3HashWithChunkSize returned unexpected digest")
	}
}

func TestBlake3HashFileWrapper(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "wrapper.bin")
	msg := []byte(strings.Repeat("file data", 1024))
	if err := os.WriteFile(file, msg, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	want := blake3.Sum256(msg)

	got, err := Blake3HashFile(file)
	if err != nil {
		t.Fatalf("Blake3HashFile returned error: %v", err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("Blake3HashFile returned unexpected digest")
	}
}

func TestGetHashFromBytes(t *testing.T) {
	t.Parallel()

	msg := []byte(strings.Repeat("hash from bytes", 256))
	sum := blake3.Sum256(msg)
	want := hex.EncodeToString(sum[:])

	got := GetHashFromBytes(msg)
	if got != want {
		t.Fatalf("GetHashFromBytes() = %q, want %q", got, want)
	}
	if got == "" {
		t.Fatalf("GetHashFromBytes returned empty string")
	}
}

func TestGetHashFromString(t *testing.T) {
	t.Parallel()

	input := "string payload for hashing"
	sum := blake3.Sum256([]byte(input))

	got := GetHashFromString(input)
	if !bytes.Equal(got, sum[:]) {
		t.Fatalf("GetHashFromString() = %x, want %x", got, sum)
	}
	if len(got) != len(sum) {
		t.Fatalf("unexpected digest length: got %d, want %d", len(got), len(sum))
	}
}

type errorAfterFirstRead struct {
	first bool
	err   error
	data  []byte
}

func (r *errorAfterFirstRead) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}

func TestHashReaderBLAKE3ReadError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("read boom")
	r := &errorAfterFirstRead{
		data: []byte("abc"),
		err:  readErr,
	}

	if _, err := hashReaderBLAKE3(r, 0); !errors.Is(err, readErr) {
		t.Fatalf("expected read error to propagate, got %v", err)
	}
}
