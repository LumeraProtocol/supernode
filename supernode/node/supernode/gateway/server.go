package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// Server represents the HTTP gateway server
type Server struct {
	port            int
	server          *http.Server
	supernodeServer pb.SupernodeServiceServer
}

// NewServer creates a new HTTP gateway server that directly calls the service
func NewServer(port int, supernodeServer pb.SupernodeServiceServer) (*Server, error) {
	if supernodeServer == nil {
		return nil, fmt.Errorf("supernode server is required")
	}

	return &Server{
		port:            port,
		supernodeServer: supernodeServer,
	}, nil
}

// Run starts the HTTP gateway server (implements service interface)
func (s *Server) Run(ctx context.Context) error {
	// Create gRPC-Gateway mux
	mux := runtime.NewServeMux()

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
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/swagger-ui/", http.StatusFound)
		} else {
			http.NotFound(w, r)
		}
	})

	// Create HTTP server
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.corsMiddleware(httpMux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logtrace.Info(ctx, "Starting HTTP gateway server", logtrace.Fields{
		"port": s.port,
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

	logtrace.Info(ctx, "Shutting down HTTP gateway server", nil)
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