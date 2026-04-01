package self_healing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	althandshake "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials/alts/handshake"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"google.golang.org/grpc/peer"
)

func TestRequestSelfHealingRejectsCallerIdentityMismatch(t *testing.T) {
	srv := NewServer("recipient-1", newFakeP2P(), nil, nil, SecurityConfig{
		EnforceAuthenticatedCaller: true,
	})

	resp, err := srv.RequestSelfHealing(authenticatedContext("challenger-a"), &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-1",
		FileKey:      "key-1",
		ChallengerId: "challenger-b",
		RecipientId:  "recipient-1",
	})
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected accepted=false on caller mismatch")
	}
	if !strings.Contains(strings.ToLower(resp.Error), "caller identity mismatch") {
		t.Fatalf("expected caller mismatch error, got: %q", resp.Error)
	}
}

func TestRequestSelfHealingRejectsUnauthenticatedCallerWhenStrict(t *testing.T) {
	srv := NewServer("recipient-1", newFakeP2P(), nil, nil, SecurityConfig{
		EnforceAuthenticatedCaller: true,
	})

	resp, err := srv.RequestSelfHealing(context.Background(), &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-1",
		FileKey:      "key-1",
		ChallengerId: "challenger-a",
		RecipientId:  "recipient-1",
	})
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected accepted=false on unauthenticated caller")
	}
	if !strings.Contains(strings.ToLower(resp.Error), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got: %q", resp.Error)
	}
}

func TestRequestSelfHealingRateLimitedPerPeer(t *testing.T) {
	srv := NewServer("recipient-1", newFakeP2P(), nil, nil, SecurityConfig{
		EnforceAuthenticatedCaller: false,
		AllowUnauthenticatedCaller: true,
		PerPeerRateLimitPerMin:     1,
		PerPeerBurst:               1,
	})

	req := &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-rate",
		FileKey:      "key-rate",
		ChallengerId: "challenger-a",
		RecipientId:  "recipient-1",
	}
	_, _ = srv.RequestSelfHealing(context.Background(), req)
	resp, err := srv.RequestSelfHealing(context.Background(), req)
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected accepted=false on rate-limit")
	}
	if !strings.Contains(strings.ToLower(resp.Error), "rate limited") {
		t.Fatalf("expected rate-limited error, got: %q", resp.Error)
	}
}

func TestRequestSelfHealingCircuitBreakerOpensAfterFailures(t *testing.T) {
	const fileKey = "key-breaker"
	const actionID = "action-breaker"
	const hashHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	lumeraClient, cleanup := mockLumeraActionLookup(t, fileKey, actionID, hashHex)
	defer cleanup()

	srv := NewServer("recipient-1", newFakeP2P(), lumeraClient, nil, SecurityConfig{
		EnforceAuthenticatedCaller: false,
		AllowUnauthenticatedCaller: true,
		BreakerFailThreshold:       1,
		BreakerCooldown:            time.Minute,
	}, &fakeCascadeFactory{
		task: &fakeCascadeTask{
			recoveryFn: func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
				return nil, fmt.Errorf("forced reseed failure")
			},
		},
	})

	req := &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-breaker",
		FileKey:      fileKey,
		ChallengerId: "challenger-a",
		RecipientId:  "recipient-1",
		ActionId:     actionID,
	}
	first, err := srv.RequestSelfHealing(context.Background(), req)
	if err != nil {
		t.Fatalf("request self-healing first: %v", err)
	}
	if first.Accepted {
		t.Fatalf("expected first request to fail due to forced reseed error")
	}
	second, err := srv.RequestSelfHealing(context.Background(), req)
	if err != nil {
		t.Fatalf("request self-healing second: %v", err)
	}
	if second.Accepted {
		t.Fatalf("expected second request to fail due to open circuit")
	}
	if !strings.Contains(strings.ToLower(second.Error), "circuit_open") {
		t.Fatalf("expected circuit_open error, got: %q", second.Error)
	}
}

func authenticatedContext(identity string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: &althandshake.AuthInfo{
			RemoteIdentity: identity,
		},
	})
}
