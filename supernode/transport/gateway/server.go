package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// DefaultGatewayPort is an uncommon port for internal gateway use
const DefaultGatewayPort = 8002

// Server represents the HTTP gateway server
type Server struct {
	ipAddress       string
	port            int
	server          *http.Server
	supernodeServer pb.SupernodeServiceServer
	chainID         string
	pprofEnabled    bool
}

// NewServer creates a new HTTP gateway server that directly calls the service
// If port is 0, it will use the default port
func NewServer(ipAddress string, port int, supernodeServer pb.SupernodeServiceServer) (*Server, error) {
	if supernodeServer == nil {
		return nil, fmt.Errorf("supernode server is required")
	}

	// Use default port if not specified
	if port == 0 {
		port = DefaultGatewayPort
	}

	return &Server{
		ipAddress:       ipAddress,
		port:            port,
		supernodeServer: supernodeServer,
	}, nil
}

// NewServerWithConfig creates a new HTTP gateway server with additional configuration
func NewServerWithConfig(ipAddress string, port int, supernodeServer pb.SupernodeServiceServer, chainID string) (*Server, error) {
	if supernodeServer == nil {
		return nil, fmt.Errorf("supernode server is required")
	}

	// Use default port if not specified
	if port == 0 {
		port = DefaultGatewayPort
	}

	// Determine if pprof should be enabled
	pprofEnabled := strings.Contains(strings.ToLower(chainID), "testnet") || os.Getenv("ENABLE_PPROF") == "true"

	return &Server{
		ipAddress:       ipAddress,
		port:            port,
		supernodeServer: supernodeServer,
		chainID:         chainID,
		pprofEnabled:    pprofEnabled,
	}, nil
}

// Run starts the HTTP gateway server (implements service interface)
func (s *Server) Run(ctx context.Context) error {
	// Create gRPC-Gateway mux with custom JSON marshaler options
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			EmitDefaults: true, // This ensures zero values are included
			OrigName:     true, // Use original proto field names
		}),
	)

	// Register the service handler directly
	err := pb.RegisterSupernodeServiceHandlerServer(ctx, mux, s.supernodeServer)
	if err != nil {
		return fmt.Errorf("failed to register gateway handler: %w", err)
	}

	// Create HTTP mux for custom endpoints
	httpMux := http.NewServeMux()

	// Register gRPC-Gateway endpoints
	httpMux.Handle("/api/", mux)

	// Register Swagger endpoints
	httpMux.HandleFunc("/swagger.json", s.serveSwaggerJSON)
	httpMux.HandleFunc("/swagger-ui/", s.serveSwaggerUI)

	// Register pprof endpoints (only on testnet)
	if s.pprofEnabled {
		httpMux.HandleFunc("/debug/pprof/", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/cmdline", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/profile", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/symbol", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/trace", s.pprofHandler)
		// Register specific pprof profiles
		httpMux.HandleFunc("/debug/pprof/allocs", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/block", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/goroutine", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/heap", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/mutex", s.pprofHandler)
		httpMux.HandleFunc("/debug/pprof/threadcreate", s.pprofHandler)

		logtrace.Debug(ctx, "Pprof endpoints enabled on gateway", logtrace.Fields{
			"chain_id": s.chainID,
			"port":     s.port,
		})
	}

	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/swagger-ui/", http.StatusFound)
		} else {
			http.NotFound(w, r)
		}
	})

	// Create HTTP server
	s.server = &http.Server{
		Addr:         net.JoinHostPort(s.ipAddress, strconv.Itoa(s.port)),
		Handler:      s.corsMiddleware(httpMux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logtrace.Debug(ctx, "Starting HTTP gateway server", logtrace.Fields{
		"address":       s.ipAddress,
		"port":          s.port,
		"pprof_enabled": s.pprofEnabled,
	})

	// Start server
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("gateway server failed: %w", err)
	}

	return nil
}

// Stop gracefully stops the HTTP gateway server (implements service interface)
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}

	logtrace.Debug(ctx, "Shutting down HTTP gateway server", nil)
	return s.server.Shutdown(ctx)
}

// corsMiddleware adds CORS headers for web access
func (s *Server) corsMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		h.ServeHTTP(w, r)
	})
}

// pprofHandler proxies requests to the pprof handlers
func (s *Server) pprofHandler(w http.ResponseWriter, r *http.Request) {
	// Check if pprof is enabled
	if !s.pprofEnabled {
		http.Error(w, "Profiling is not enabled", http.StatusForbidden)
		return
	}

	// Get the default pprof handler and serve
	if handler, pattern := http.DefaultServeMux.Handler(r); pattern != "" {
		handler.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}
