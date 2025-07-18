package capabilities

import (
	"context"
	"fmt"
	"time"

	pb "github.com/LumeraProtocol/supernode/gen/supernode"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// PeerCompatibilityChecker handles compatibility verification with remote peers
type PeerCompatibilityChecker struct {
	localCapabilities *Capabilities
	compatManager     CompatibilityManager
}

// NewPeerCompatibilityChecker creates a new peer compatibility checker
func NewPeerCompatibilityChecker(localCaps *Capabilities) *PeerCompatibilityChecker {
	return &PeerCompatibilityChecker{
		localCapabilities: localCaps,
		compatManager:     NewCompatibilityManager(),
	}
}

// CheckPeerCompatibility checks if a peer is compatible with this supernode
func (pcc *PeerCompatibilityChecker) CheckPeerCompatibility(ctx context.Context, peerAddr string) (*CompatibilityResult, error) {
	// Connect to peer
	conn, err := grpc.Dial(peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     fmt.Sprintf("failed to connect to peer: %v", err),
			Details:    map[string]interface{}{"error": err.Error()},
		}, nil
	}
	defer conn.Close()

	// Query peer capabilities
	client := pb.NewSupernodeServiceClient(conn)
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.GetCapabilities(ctxTimeout, &pb.GetCapabilitiesRequest{})
	if err != nil {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     fmt.Sprintf("failed to query peer capabilities: %v", err),
			Details:    map[string]interface{}{"error": err.Error()},
		}, nil
	}

	caps := resp.GetCapabilities()
	if caps == nil {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     "peer returned empty capabilities",
			Details:    map[string]interface{}{"peer_addr": peerAddr},
		}, nil
	}

	// Convert protobuf capabilities to local format
	peerCaps := &Capabilities{
		Version:          caps.Version,
		SupportedActions: caps.SupportedActions,
		ActionVersions:   make(map[string][]string),
		Metadata:         caps.Metadata,
		Timestamp:        time.Unix(caps.Timestamp, 0),
	}

	// Convert action versions
	for action, actionVersions := range caps.ActionVersions {
		if actionVersions != nil {
			peerCaps.ActionVersions[action] = actionVersions.Versions
		}
	}

	// Check compatibility
	return pcc.compatManager.CheckCompatibility(pcc.localCapabilities, peerCaps)
}

// CheckPeerCompatibilityWithFallback checks peer compatibility with graceful fallback
func (pcc *PeerCompatibilityChecker) CheckPeerCompatibilityWithFallback(ctx context.Context, peerAddr string) *CompatibilityResult {
	result, err := pcc.CheckPeerCompatibility(ctx, peerAddr)
	if err != nil {
		// If we can't check compatibility, assume compatible for backward compatibility
		return &CompatibilityResult{
			Compatible: true,
			Reason:     fmt.Sprintf("compatibility check failed, assuming compatible: %v", err),
			Details:    map[string]interface{}{"fallback": true, "error": err.Error()},
		}
	}
	return result
}