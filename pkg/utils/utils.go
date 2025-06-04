package utils

import (
	"bytes"
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lukechampine.com/blake3"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"golang.org/x/sync/semaphore"

	"github.com/klauspost/compress/zstd"
)

const (
	maxParallelHighCompressCalls = 5
	semaphoreWeight              = 1
	highCompressTimeout          = 30 * time.Minute
	highCompressionLevel         = 4
)

var ipEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.co/ip",
	"https://checkip.amazonaws.com",
	"https://ipv4.icanhazip.com",
}

// GetExternalIPAddress returns the first valid public IP obtained
// from a list of providers, or an error if none work.
// func GetExternalIPAddress() (string, error) {
// 	client := &http.Client{Timeout: 4 * time.Second}

// 	for _, url := range ipEndpoints {
// 		req, _ := http.NewRequest(http.MethodGet, url, nil)

// 		resp, err := client.Do(req)
// 		if err != nil {
// 			continue // provider down? try next
// 		}

// 		body, err := io.ReadAll(resp.Body)
// 		resp.Body.Close()
// 		if err != nil {
// 			continue
// 		}

// 		ip := strings.TrimSpace(string(body))
// 		if net.ParseIP(ip) != nil {
// 			return ip, nil
// 		}
// 	}

// 	return "", errors.New("unable to determine external IP address from any provider")
// }

var sem = semaphore.NewWeighted(maxParallelHighCompressCalls)

// DiskStatus cotains info of disk storage
type DiskStatus struct {
	All  float64 `json:"all"`
	Used float64 `json:"used"`
	Free float64 `json:"free"`
}

// SafeErrStr returns err string
func SafeErrStr(err error) string {
	if err != nil {
		return err.Error()
	}

	return ""
}

// SafeString returns value of str ptr or empty string if ptr is nil
func SafeString(ptr *string) string {
	if ptr != nil {
		return *ptr
	}
	return ""
}

// SafeInt returns value of int ptr or default int if ptr is nil
func SafeInt(ptr *int, def int) int {
	if ptr != nil {
		return *ptr
	}
	return def
}

// SafeFloat returns value of float ptr or default float if ptr is nil
func SafeFloat(ptr *float64, def float64) float64 {
	if ptr != nil {
		return *ptr
	}
	return def
}

// SafeBool returns value of bool ptr or default bool if ptr is nil
func SafeBool(ptr *bool, def bool) bool {
	if ptr != nil {
		return *ptr
	}
	return def
}

// IsContextErr checks if err is related to context
func IsContextErr(err error) bool {
	if err != nil {
		errStr := strings.ToLower(err.Error())
		return strings.Contains(errStr, "context") || strings.Contains(errStr, "ctx")
	}

	return false
}

// GetExternalIPAddress returns external IP address
func GetExternalIPAddress() (externalIP string, err error) {
	return "localhost", nil
	/*
		resp, err := http.Get("https://api.ipify.org")
		if err != nil {
			return "", err
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		if net.ParseIP(string(body)) == nil {
			return "", errors.Errorf("invalid IP response from %s", "ipconf.ip")
		}

		return string(body), nil */
}

// B64Encode base64 encodes
func B64Encode(in []byte) (out []byte) {
	out = make([]byte, base64.StdEncoding.EncodedLen(len(in)))
	base64.StdEncoding.Encode(out, in)

	return out
}

// B64Decode decode base64 input
func B64Decode(in []byte) (out []byte, err error) {
	out = make([]byte, base64.StdEncoding.DecodedLen(len(in)))
	n, err := base64.StdEncoding.Decode(out, in)
	if err != nil {
		return nil, errors.Errorf("b64 decode: %w", err)
	}

	return out[:n], nil
}

// EqualStrList tells whether a and b contain the same elements.
// A nil argument is equivalent to an empty slice.
func EqualStrList(a, b []string) error {
	if len(a) != len(b) {
		return errors.Errorf("unequal length: %d  & %d", len(a), len(b))
	}

	for i, v := range a {
		if v != b[i] {
			return errors.Errorf("index %d mismatch, vals: %s   &  %s", i, v, b[i])
		}
	}

	return nil
}

// Blake3Hash returns Blake3 hash of input message
func Blake3Hash(msg []byte) ([]byte, error) {
	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, bytes.NewReader(msg)); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

// GetHashFromBytes generate blake3 hash string from a given byte array
func GetHashFromBytes(msg []byte) string {
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, bytes.NewReader(msg)); err != nil {
		return ""
	}

	return hex.EncodeToString(h.Sum(nil))
}

// GetHashFromString returns blake3 hash of a given string
func GetHashFromString(s string) []byte {
	sum := blake3.Sum256([]byte(s))
	return sum[:]
}

func ComputeHashOfFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, err
	}

	return hasher.Sum(nil), nil
}

// XORBytes returns the XOR of two same-length byte slices.
func XORBytes(a, b []byte) ([]byte, error) {
	if len(a) != len(b) {
		return nil, fmt.Errorf("length of byte slices is not equivalent: %d != %d", len(a), len(b))
	}
	buf := make([]byte, len(a))
	for i := range a {
		buf[i] = a[i] ^ b[i]
	}
	return buf, nil
}

// BytesToInt convert input bytes data to big.Int value
func BytesToInt(inputBytes []byte) *big.Int {
	z := new(big.Int)
	z.SetBytes(inputBytes)
	return z
}

// ComputeXorDistanceBetweenTwoStrings computes the XOR distance between the hashes of two strings.
func ComputeXorDistanceBetweenTwoStrings(string1, string2 string) *big.Int {
	string1Hash := GetHashFromString(string1)
	string2Hash := GetHashFromString(string2)
	xorDistance, _ := XORBytes(string1Hash, string2Hash)
	return big.NewInt(0).SetBytes(xorDistance)
}

// GetNClosestXORDistanceStringToAGivenComparisonString finds the top N closest strings to the comparison string based on XOR distance.
func GetNClosestXORDistanceStringToAGivenComparisonString(n int, comparisonString string, sliceOfComputingXORDistance []string, ignores ...string) []string {
	h := &MaxHeap{}
	heap.Init(h)

	for _, currentComputing := range sliceOfComputingXORDistance {
		ignoreCurrent := false
		for _, ignore := range ignores {
			if ignore == currentComputing {
				ignoreCurrent = true
				break
			}
		}
		if !ignoreCurrent {
			currentXORDistance := ComputeXorDistanceBetweenTwoStrings(currentComputing, comparisonString)
			entry := DistanceEntry{distance: currentXORDistance, str: currentComputing}
			if h.Len() < n {
				heap.Push(h, entry)
			} else if entry.distance.Cmp((*h)[0].distance) < 0 {
				heap.Pop(h)
				heap.Push(h, entry)
			}
		}
	}

	result := make([]string, h.Len()) // Create the result slice with the actual size of the heap
	for i := h.Len() - 1; i >= 0; i-- {
		entry := heap.Pop(h).(DistanceEntry)
		result[i] = entry.str
	}

	return result
}

// GetFileSizeInMB returns size of the file in MB
func GetFileSizeInMB(data []byte) float64 {
	return math.Ceil(float64(len(data)) / (1024 * 1024))
}

// BytesToMB ...
func BytesToMB(bytes uint64) float64 {
	return float64(bytes) / 1048576.0
}

// StringInSlice checks if str is in slice
func StringInSlice(list []string, str string) bool {

	for _, v := range list {
		if strings.EqualFold(v, str) {
			return true
		}
	}

	return false
}

func hasActiveNetworkInterface() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
			return true
		}
	}

	return false
}

func hasInternetConnectivity() bool {
	hosts := []string{"google.com:80", "microsoft.com:80", "amazon.com:80", "8.8.8.8:53", "1.1.1.1:53"}
	timeout := 1 * time.Second

	for _, host := range hosts {
		conn, err := net.DialTimeout("tcp", host, timeout)
		if err == nil {
			defer conn.Close()
			return true
		}
	}

	return false
}

// CheckInternetConnectivity checks if the device is connected to the internet
func CheckInternetConnectivity() bool {
	if hasActiveNetworkInterface() {
		return hasInternetConnectivity()
	}

	return false
}

// BytesIntToMB converts bytes to MB
func BytesIntToMB(b int) int {
	return int(math.Ceil(float64(b) / 1048576.0))
}

// Compress compresses the data
func Compress(data []byte, level int) ([]byte, error) {
	if level < 1 || level > 4 {
		return nil, fmt.Errorf("invalid compression level: %d - allowed levels are 1 - 4", level)
	}

	numCPU := runtime.NumCPU()
	// Create a buffer to store compressed data
	var compressedData bytes.Buffer

	// Create a new Zstd encoder with concurrency set to the number of CPU cores
	encoder, err := zstd.NewWriter(&compressedData, zstd.WithEncoderConcurrency(numCPU), zstd.WithEncoderLevel(zstd.EncoderLevel(level)))
	if err != nil {
		return nil, fmt.Errorf("failed to create Zstd encoder: %v", err)
	}

	// Perform the compression
	_, err = io.Copy(encoder, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to compress data: %v", err)
	}

	// Close the encoder to flush any remaining data
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("failed to close encoder: %v", err)
	}

	return compressedData.Bytes(), nil
}

// Decompress decompresses the data
func Decompress(data []byte) ([]byte, error) {
	// Get the number of CPU cores available
	numCPU := runtime.NumCPU()

	// Create a new Zstd decoder with concurrency set to the number of CPU cores
	decoder, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(numCPU))
	if err != nil {
		return nil, fmt.Errorf("failed to create Zstd decoder: %v", err)
	}
	defer decoder.Close()

	// Perform the decompression
	decompressedData, err := io.ReadAll(decoder)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress data: %v", err)
	}

	return decompressedData, nil
}

// RandomDuration returns a random duration between min and max
func RandomDuration(min, max int) time.Duration {
	if min > max {
		min, max = max, min
	}
	var n uint64
	binary.Read(rand.Reader, binary.LittleEndian, &n) // read a random uint64
	randomMillisecond := min + int(n%(uint64(max-min+1)))
	return time.Duration(randomMillisecond) * time.Millisecond
}

func ZstdCompress(data []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %v", err)
	}
	defer encoder.Close()

	return encoder.EncodeAll(data, nil), nil
}

func ZstdDecompress(data []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd decoder: %v", err)
	}
	defer decoder.Close()

	decoded, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress zstd data: %v", err)
	}

	return decoded, nil
}

// HighCompress compresses the data
func HighCompress(cctx context.Context, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(cctx, highCompressTimeout)
	defer cancel()

	// Acquire the semaphore. This will block if 5 other goroutines are already inside this function.
	if err := sem.Acquire(ctx, semaphoreWeight); err != nil {
		return nil, fmt.Errorf("failed to acquire semaphore: %v", err)
	}
	defer sem.Release(semaphoreWeight) // Ensure that the semaphore is always released

	numCPU := runtime.NumCPU()
	// Create a buffer to store compressed data
	var compressedData bytes.Buffer

	// Create a new Zstd encoder with concurrency set to the number of CPU cores
	encoder, err := zstd.NewWriter(&compressedData, zstd.WithEncoderConcurrency(numCPU), zstd.WithEncoderLevel(zstd.EncoderLevel(highCompressionLevel)))
	if err != nil {
		return nil, fmt.Errorf("failed to create Zstd encoder: %v", err)
	}

	// Perform the compression
	_, err = io.Copy(encoder, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to compress data: %v", err)
	}

	// Close the encoder to flush any remaining data
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("failed to close encoder: %v", err)
	}

	return compressedData.Bytes(), nil
}

// LoadSymbols takes a directory path and a map where keys are filenames. It reads each file in the directory
// corresponding to the keys in the map and updates the map with the content of the files as byte slices.
func LoadSymbols(dir string, keys []string) (ret [][]byte, err error) {
	// Iterate over the map keys which are filenames
	ret = make([][]byte, 0, len(keys))
	for _, filename := range keys {
		// Construct the full path to the file
		fullPath := filepath.Join(dir, filename)

		// Read the file content as bytes using os.ReadFile
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %v", fullPath, err)
		}

		// Update the map with the file data
		ret = append(ret, data)
	}

	// Return the updated map
	return ret, nil
}

// DeleteSymbols takes a directory path and a map where keys are filenames. It deletes each file in the directory
func DeleteSymbols(ctx context.Context, dir string, keys []string) error {
	// Iterate over the map keys which are filenames
	for _, filename := range keys {
		// Construct the full path to the file
		fullPath := filepath.Join(dir, filename)

		// Delete the file using os.Remove
		if err := os.Remove(fullPath); err != nil {
			log.Println("Failed to delete file", fullPath, ":", err.Error())
		}
	}
	// If all files are successfully deleted, return nil indicating no error
	return nil
}

// ReadDirFilenames returns a map whose keys are "block_*/file" paths, values nil.
func ReadDirFilenames(dirPath string) (map[string][]byte, error) {
	idMap := make(map[string][]byte)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read base dir: %w", err)
	}

	for _, ent := range entries {
		if !ent.IsDir() {
			// file at root (rare) – keep backward-compat
			if ent.Name() == "_raptorq_layout.json" {
				continue
			}
			idMap[ent.Name()] = nil
			continue
		}

		blockDir := filepath.Join(dirPath, ent.Name())
		files, err := os.ReadDir(blockDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", blockDir, err)
		}
		for _, f := range files {
			if f.IsDir() || f.Name() == "_raptorq_layout.json" {
				continue
			}
			rel := filepath.Join(ent.Name(), f.Name()) // "block_0/abc.sym"
			idMap[rel] = nil
		}
	}

	return idMap, nil
}

// CheckDiskSpace checks if the available space on the disk is less than 50 GB
func CheckDiskSpace(lowSpaceThreshold int) (bool, error) {
	// Define the command and its arguments
	cmd := exec.Command("bash", "-c", "df -BG --output=source,fstype,avail | egrep 'ext4|xfs' | sort -rn -k3 | awk 'NR==1 {print $3}'")

	// Execute the command
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false, fmt.Errorf("failed to execute command: %w", err)
	}

	// Process the output
	output := strings.TrimSpace(out.String())
	if len(output) < 2 {
		return false, errors.New("invalid output from disk space check")
	}

	// Convert the available space to an integer
	availSpace, err := strconv.Atoi(output[:len(output)-1])
	if err != nil {
		return false, fmt.Errorf("failed to parse available space: %w", err)
	}

	// Check if the available space is less than 50 GB
	return availSpace < lowSpaceThreshold, nil
}
