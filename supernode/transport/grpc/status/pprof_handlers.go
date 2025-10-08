package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

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

// GetPprofIndex returns the pprof index page
func (s *SupernodeServer) GetPprofIndex(ctx context.Context, req *pb.GetPprofIndexRequest) (*pb.GetPprofIndexResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.GetPprofIndexResponse{
			Html:    "",
			Enabled: false,
		}, nil
	}

	// Generate a simple index page with links to available profiles
	html := `<!DOCTYPE html>
<html>
<head>
<title>Supernode Profiling</title>
<style>
body {
	font-family: Arial, sans-serif;
	margin: 40px;
}
h1 {
	color: #333;
}
ul {
	line-height: 1.8;
}
a {
	color: #0066cc;
	text-decoration: none;
}
a:hover {
	text-decoration: underline;
}
</style>
</head>
<body>
<h1>Supernode Profiling</h1>
<p>Available profiles:</p>
<ul>
<li><a href="/api/v1/debug/pprof/heap">heap</a> - A sampling of memory allocations of live objects</li>
<li><a href="/api/v1/debug/pprof/goroutine">goroutine</a> - Stack traces of all current goroutines</li>
<li><a href="/api/v1/debug/pprof/allocs">allocs</a> - A sampling of all past memory allocations</li>
<li><a href="/api/v1/debug/pprof/block">block</a> - Stack traces that led to blocking on synchronization primitives</li>
<li><a href="/api/v1/debug/pprof/mutex">mutex</a> - Stack traces of holders of contended mutexes</li>
<li><a href="/api/v1/debug/pprof/threadcreate">threadcreate</a> - Stack traces that led to the creation of new OS threads</li>
<li><a href="/api/v1/debug/pprof/profile">profile</a> - CPU profile (specify ?seconds=30 for duration)</li>
</ul>
</body>
</html>`

	return &pb.GetPprofIndexResponse{
		Html:    html,
		Enabled: true,
	}, nil
}

// GetPprofHeap returns the heap profile
func (s *SupernodeServer) GetPprofHeap(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("heap", req.GetDebug())
}

// GetPprofGoroutine returns the goroutine profile
func (s *SupernodeServer) GetPprofGoroutine(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("goroutine", req.GetDebug())
}

// GetPprofAllocs returns the allocations profile
func (s *SupernodeServer) GetPprofAllocs(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("allocs", req.GetDebug())
}

// GetPprofBlock returns the block profile
func (s *SupernodeServer) GetPprofBlock(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("block", req.GetDebug())
}

// GetPprofMutex returns the mutex profile
func (s *SupernodeServer) GetPprofMutex(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("mutex", req.GetDebug())
}

// GetPprofThreadcreate returns the threadcreate profile
func (s *SupernodeServer) GetPprofThreadcreate(ctx context.Context, req *pb.GetPprofProfileRequest) (*pb.GetPprofProfileResponse, error) {
	return s.getPprofProfile("threadcreate", req.GetDebug())
}

// GetPprofProfile returns the CPU profile
func (s *SupernodeServer) GetPprofProfile(ctx context.Context, req *pb.GetPprofCpuProfileRequest) (*pb.GetPprofProfileResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.GetPprofProfileResponse{
			Enabled: false,
			Error:   "Profiling is disabled. Enable on testnet or set ENABLE_PPROF=true",
		}, nil
	}

	seconds := req.GetSeconds()
	if seconds <= 0 {
		seconds = 30 // Default to 30 seconds
	}
	if seconds > 300 {
		seconds = 300 // Cap at 5 minutes
	}

	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		return &pb.GetPprofProfileResponse{
			Enabled: true,
			Error:   fmt.Sprintf("Failed to start CPU profile: %v", err),
		}, nil
	}

	// Profile for the specified duration
	time.Sleep(time.Duration(seconds) * time.Second)
	pprof.StopCPUProfile()

	return &pb.GetPprofProfileResponse{
		Data:        buf.Bytes(),
		ContentType: "application/octet-stream",
		Enabled:     true,
	}, nil
}

// getPprofProfile is a helper function to get various runtime profiles
func (s *SupernodeServer) getPprofProfile(profileType string, debug int32) (*pb.GetPprofProfileResponse, error) {
	if !s.isPprofEnabled() {
		return &pb.GetPprofProfileResponse{
			Enabled: false,
			Error:   "Profiling is disabled. Enable on testnet or set ENABLE_PPROF=true",
		}, nil
	}

	var buf bytes.Buffer
	var contentType string

	// Get the appropriate profile
	var p *pprof.Profile
	switch profileType {
	case "heap":
		runtime.GC() // Force GC before heap profile
		p = pprof.Lookup("heap")
		contentType = "application/octet-stream"
	case "goroutine":
		p = pprof.Lookup("goroutine")
		contentType = "application/octet-stream"
	case "allocs":
		p = pprof.Lookup("allocs")
		contentType = "application/octet-stream"
	case "block":
		p = pprof.Lookup("block")
		contentType = "application/octet-stream"
	case "mutex":
		p = pprof.Lookup("mutex")
		contentType = "application/octet-stream"
	case "threadcreate":
		p = pprof.Lookup("threadcreate")
		contentType = "application/octet-stream"
	default:
		return &pb.GetPprofProfileResponse{
			Enabled: true,
			Error:   fmt.Sprintf("Unknown profile type: %s", profileType),
		}, nil
	}

	if p == nil {
		return &pb.GetPprofProfileResponse{
			Enabled: true,
			Error:   fmt.Sprintf("Profile %s not found", profileType),
		}, nil
	}

	// Write the profile to buffer
	// If debug > 0, write in text format for human reading
	if debug > 0 {
		if err := p.WriteTo(&buf, int(debug)); err != nil {
			return &pb.GetPprofProfileResponse{
				Enabled: true,
				Error:   fmt.Sprintf("Failed to write profile: %v", err),
			}, nil
		}
		contentType = "text/plain"
	} else {
		// Write in binary pprof format
		if err := p.WriteTo(&buf, 0); err != nil {
			return &pb.GetPprofProfileResponse{
				Enabled: true,
				Error:   fmt.Sprintf("Failed to write profile: %v", err),
			}, nil
		}
	}

	return &pb.GetPprofProfileResponse{
		Data:        buf.Bytes(),
		ContentType: contentType,
		Enabled:     true,
	}, nil
}