package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
)

// isPprofEnabled checks if pprof should be enabled based on chain ID or environment variable
func (s *SupernodeServer) isPprofEnabled() bool {
	// Check if chain ID contains testnet
	if s.statusService != nil && s.statusService.GetChainID() != "" {
		if strings.Contains(strings.ToLower(s.statusService.GetChainID()), "testnet") {
			return true
		}
	}

	// Check environment variable
	return os.Getenv("ENABLE_PPROF") == "true"
}

// Raw pprof handlers - these proxy to the actual pprof HTTP endpoints

// pprofProxy makes an internal HTTP request to the actual pprof endpoint
func (s *SupernodeServer) pprofProxy(path string, queryParams string) ([]byte, error) {
	// Determine the port - use gateway port if available, otherwise use default
	port := 8002 // Default gateway port
	if s.gatewayPort != 0 {
		port = s.gatewayPort
	}

	// Construct the URL
	url := fmt.Sprintf("http://localhost:%d/debug/pprof%s", port, path)
	if queryParams != "" {
		url += "?" + queryParams
	}

	// Make the HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the response body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// GetRawPprof returns the pprof index
func (s *SupernodeServer) GetRawPprof(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte("Profiling disabled")}, nil
	}

	data, err := s.pprofProxy("/", "")
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte(fmt.Sprintf("Error: %v", err))}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofHeap returns raw heap profile
func (s *SupernodeServer) GetRawPprofHeap(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/heap", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofGoroutine returns raw goroutine profile
func (s *SupernodeServer) GetRawPprofGoroutine(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/goroutine", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofAllocs returns raw allocations profile
func (s *SupernodeServer) GetRawPprofAllocs(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/allocs", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofBlock returns raw block profile
func (s *SupernodeServer) GetRawPprofBlock(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/block", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofMutex returns raw mutex profile
func (s *SupernodeServer) GetRawPprofMutex(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/mutex", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofThreadcreate returns raw threadcreate profile
func (s *SupernodeServer) GetRawPprofThreadcreate(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	queryParams := ""
	if req.GetDebug() > 0 {
		queryParams = fmt.Sprintf("debug=%d", req.GetDebug())
	}

	data, err := s.pprofProxy("/threadcreate", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofProfile returns raw CPU profile
func (s *SupernodeServer) GetRawPprofProfile(ctx context.Context, req *pb.RawPprofCpuRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	seconds := req.GetSeconds()
	if seconds <= 0 {
		seconds = 30
	}
	if seconds > 300 {
		seconds = 300
	}

	queryParams := fmt.Sprintf("seconds=%d", seconds)
	data, err := s.pprofProxy("/profile", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofCmdline returns the command line
func (s *SupernodeServer) GetRawPprofCmdline(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	data, err := s.pprofProxy("/cmdline", "")
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofSymbol returns symbol information
func (s *SupernodeServer) GetRawPprofSymbol(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	data, err := s.pprofProxy("/symbol", "")
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}

// GetRawPprofTrace returns execution trace
func (s *SupernodeServer) GetRawPprofTrace(ctx context.Context, req *pb.RawPprofRequest) (*pb.RawPprofResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	// Trace typically takes a seconds parameter
	queryParams := "seconds=1"
	data, err := s.pprofProxy("/trace", queryParams)
	if err != nil {
		return &pb.RawPprofResponse{Data: []byte{}}, nil
	}

	return &pb.RawPprofResponse{Data: data}, nil
}
