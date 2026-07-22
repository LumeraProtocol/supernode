package updater

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// preflightDecision represents the outcome of a pre-update EVM compatibility check.
type preflightDecision int

const (
	// preflightAllow: proceed with the normal update flow.
	preflightAllow preflightDecision = iota
	// preflightBlock: the target version requires evm_key_name but the node
	// is not prepared to migrate. Refuse to install; keep the current binary.
	preflightBlock
)

func (d preflightDecision) String() string {
	switch d {
	case preflightAllow:
		return "allow"
	case preflightBlock:
		return "block"
	}
	return "unknown"
}

// preflightInputs is the pure predicate input for decidePreflight. Testing
// against this table is the "predicate-refactor" pattern from
// invariant-first-coding §Anti-pattern 6.
type preflightInputs struct {
	chainHasEVM    bool
	evmKeyName     string
	currentVersion string
	targetVersion  string
}

// decidePreflight is a pure function: no I/O, no logging. Given the four
// axes of the invariant table, it returns allow or block plus a
// human-readable reason.
func decidePreflight(in preflightInputs) (preflightDecision, string) {
	if !in.chainHasEVM {
		return preflightAllow, "chain has no evm module active"
	}
	if strings.TrimSpace(in.evmKeyName) != "" {
		return preflightAllow, "supernode config has evm_key_name set"
	}
	if utils.IsV260OrAbove(in.currentVersion) {
		return preflightAllow, fmt.Sprintf(
			"current supernode %s is evm-capable; startup owns migration validation",
			in.currentVersion,
		)
	}
	if utils.IsV260OrAbove(in.targetVersion) {
		return preflightBlock, fmt.Sprintf(
			"chain has evm module active, target %s requires evm_key_name but config has none",
			in.targetVersion,
		)
	}
	return preflightAllow, "target below evm-required threshold"
}

// chainEVMProbeTimeout bounds the gRPC dial + ModuleVersions call.
const chainEVMProbeTimeout = 15 * time.Second

// queryEVMModuleActive dials the Lumera gRPC endpoint at grpcAddr and asks
// whether the "evm" upgrade-module version is reported. Returns:
//
//	(true, nil)   — chain confirmed to have EVM module
//	(false, nil)  — chain answered and EVM module is absent
//	(false, err)  — query failed (dial error, timeout, gRPC error)
//
// The caller MUST treat the error case as "unknown" and fail-open (allow) —
// a transient chain outage must never block an update.
func queryEVMModuleActive(ctx context.Context, grpcAddr string) (bool, error) {
	if strings.TrimSpace(grpcAddr) == "" {
		return false, fmt.Errorf("empty grpc_addr")
	}
	host, useTLS, err := parseGRPCAddr(grpcAddr)
	if err != nil {
		return false, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, chainEVMProbeTimeout)
	defer cancel()

	var creds grpc.DialOption
	if useTLS {
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}))
	} else {
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	conn, err := grpc.DialContext(dialCtx, host, creds, grpc.WithBlock())
	if err != nil {
		return false, fmt.Errorf("grpc dial %s: %w", host, err)
	}
	defer conn.Close()

	client := upgradetypes.NewQueryClient(conn)
	resp, err := client.ModuleVersions(dialCtx, &upgradetypes.QueryModuleVersionsRequest{ModuleName: "evm"})
	if err != nil {
		return false, fmt.Errorf("ModuleVersions: %w", err)
	}
	return len(resp.ModuleVersions) > 0, nil
}

// parseGRPCAddr normalizes a supernode `lumera.grpc_addr` value into a
// (host:port, useTLS) pair. Accepts bare host:port ("grpc.testnet.lumera.io:443"),
// or a URL with scheme ("https://grpc.testnet.lumera.io" → :443 + TLS,
// "http://..." → :80 + no TLS, "grpc://..." → :9090 + no TLS).
func parseGRPCAddr(raw string) (host string, useTLS bool, err error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, uerr := url.Parse(raw)
		if uerr != nil {
			return "", false, fmt.Errorf("parse grpc_addr %q: %w", raw, uerr)
		}
		host = u.Host
		switch strings.ToLower(u.Scheme) {
		case "https", "grpcs":
			useTLS = true
			if u.Port() == "" {
				host = host + ":443"
			}
		default:
			useTLS = false
			if u.Port() == "" {
				host = host + ":9090"
			}
		}
		return host, useTLS, nil
	}
	// bare host[:port] — infer TLS from :443
	host = raw
	if strings.HasSuffix(host, ":443") {
		useTLS = true
	}
	return host, useTLS, nil
}

// preflightCheck loads the current runtime state (chain evm status, supernode
// evm_key_name, current installed version) and returns a decision for the
// proposed target version. On chain-query failure it FAILS OPEN (returns
// allow) — a transient chain outage must never block an update.
func (u *AutoUpdater) preflightCheck(ctx context.Context, targetVersion string) (preflightDecision, string) {
	grpcAddr, _ := utils.ReadSupernodeGRPCAddr()
	hasEVM, err := queryEVMModuleActive(ctx, grpcAddr)
	if err != nil {
		log.Printf("preflight: chain evm probe failed (fail-open): %v", err)
		return preflightAllow, "chain unreachable; fail-open"
	}
	evmKey, _ := utils.ReadSupernodeEVMKeyName()

	in := preflightInputs{
		chainHasEVM:    hasEVM,
		evmKeyName:     evmKey,
		currentVersion: u.config.Updates.CurrentVersion,
		targetVersion:  targetVersion,
	}
	decision, reason := decidePreflight(in)
	return decision, reason
}
