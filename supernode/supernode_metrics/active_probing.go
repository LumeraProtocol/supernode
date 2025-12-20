package supernode_metrics

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
)

type probeTarget struct {
	identity    string
	hostIPv4    string
	grpcPort    int
	p2pPort     int
	gatewayPort int

	metricsHeight int64
}

func (hm *Collector) probingLoop(ctx context.Context) {
	defer hm.wg.Done()

	selfIdentity, ok := hm.probingIdentity()
	if !ok {
		logtrace.Warn(ctx, "Active probing disabled: missing identity/keyring/client", nil)
		return
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	if !hm.waitForProbeStart(ctx, rng) {
		return
	}

	probeTimeout := time.Duration(ProbeTimeoutSeconds) * time.Second
	clients, err := hm.newProbeClients(selfIdentity, probeTimeout)
	if err != nil {
		logtrace.Error(ctx, "Active probing disabled: failed to initialize probe clients", logtrace.Fields{logtrace.FieldError: err.Error()})
		return
	}

	cache := &probeCache{}
	for {
		hm.refreshProbePlanIfNeeded(ctx, cache, selfIdentity)
		hm.runProbeRound(ctx, clients, cache, probeTimeout)

		if !hm.waitForNextProbe(ctx, rng, cache) {
			return
		}
	}
}

type probeCache struct {
	plan   *probePlan
	cursor int
}

type probePlan struct {
	epochID         uint64
	epochBlocks     uint64
	eligiblePeers   []probeTarget
	outboundTargets []probeTarget
	builtAt         time.Time
}

type probeClients struct {
	grpcClient *grpcclient.Client
	grpcOpts   *grpcclient.ClientOptions
	p2pCreds   *ltc.LumeraTC
	httpClient *http.Client
}

func (hm *Collector) probingIdentity() (string, bool) {
	selfIdentity := strings.TrimSpace(hm.identity)
	if selfIdentity == "" || hm.keyring == nil || hm.lumeraClient == nil {
		return "", false
	}
	return selfIdentity, true
}

func (hm *Collector) waitForProbeStart(ctx context.Context, rng *rand.Rand) bool {
	startupDelay := time.Duration(DefaultStartupDelaySeconds) * time.Second
	// Add jitter so that not all nodes start probing at the same wall-clock time.
	startupDelay = withJitter(startupDelay, defaultProbeJitterFraction, rng)
	return waitOrStop(ctx, hm.stopChan, startupDelay)
}

func (hm *Collector) waitForNextProbe(ctx context.Context, rng *rand.Rand, cache *probeCache) bool {
	base := defaultProbeInterval
	if hm != nil && hm.reportInterval > 0 && cache != nil && cache.plan != nil && len(cache.plan.outboundTargets) > 0 {
		base = probeRoundInterval(hm.reportInterval, len(cache.plan.outboundTargets))
	}
	if base < time.Second {
		base = time.Second
	}
	next := withJitter(base, defaultProbeJitterFraction, rng)
	return waitOrStop(ctx, hm.stopChan, next)
}

func probeRoundInterval(reportInterval time.Duration, outboundTargets int) time.Duration {
	if reportInterval <= 0 || outboundTargets <= 0 {
		return 0
	}

	attempts := outboundTargets
	if attempts < MinProbeAttemptsPerReportInterval {
		attempts = MinProbeAttemptsPerReportInterval
	}
	return reportInterval / time.Duration(attempts)
}

func (hm *Collector) newProbeClients(selfIdentity string, timeout time.Duration) (*probeClients, error) {
	validator := lumera.NewSecureKeyExchangeValidator(hm.lumeraClient)

	grpcCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       hm.keyring,
			LocalIdentity: selfIdentity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create gRPC client creds: %w", err)
	}

	grpcProbeClient := grpcclient.NewClient(grpcCreds)
	grpcProbeOpts := grpcclient.DefaultClientOptions()
	grpcProbeOpts.EnableRetries = false
	grpcProbeOpts.ConnWaitTime = timeout
	grpcProbeOpts.MinConnectTimeout = timeout

	p2pCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       hm.keyring,
			LocalIdentity: selfIdentity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create P2P client creds: %w", err)
	}

	lumeraTC, ok := p2pCreds.(*ltc.LumeraTC)
	if !ok {
		return nil, fmt.Errorf("invalid P2P creds type (expected *LumeraTC)")
	}

	return &probeClients{
		grpcClient: grpcProbeClient,
		grpcOpts:   grpcProbeOpts,
		p2pCreds:   lumeraTC,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (hm *Collector) refreshProbePlanIfNeeded(ctx context.Context, cache *probeCache, selfIdentity string) {
	if cache == nil {
		return
	}

	selfIdentity = strings.TrimSpace(selfIdentity)
	if selfIdentity == "" || hm.lumeraClient == nil {
		cache.plan = nil
		return
	}

	height, heightOK := hm.latestBlockHeight(ctx)
	now := time.Now()
	if !heightOK {
		// When the chain is temporarily unreachable, keep using the existing plan
		// (if any) so we continue generating inbound traffic for peers.
		if cache.plan != nil && now.Sub(cache.plan.builtAt) < defaultPeerRefreshInterval {
			return
		}
		cache.plan = nil
		return
	}

	epochBlocks := hm.probingEpochBlocks()
	if epochBlocks == 0 {
		cache.plan = nil
		return
	}
	epochID := uint64(height) / epochBlocks
	if cache.plan != nil && cache.plan.epochID == epochID && cache.plan.epochBlocks == epochBlocks {
		return
	}

	resp, err := hm.lumeraClient.SuperNode().ListSuperNodes(ctx)
	if err != nil || resp == nil {
		logtrace.Warn(ctx, "Active probing: failed to refresh peer list", logtrace.Fields{logtrace.FieldError: fmt.Sprintf("%v", err)})
		return
	}

	isTest := strings.EqualFold(strings.TrimSpace(os.Getenv("INTEGRATION_TEST")), "true")

	peers := buildEligiblePeers(resp.Supernodes, isTest)
	if len(peers) < 2 {
		cache.plan = nil
		return
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].identity < peers[j].identity
	})

	selfIndex := -1
	for i := range peers {
		if peers[i].identity == selfIdentity {
			selfIndex = i
			break
		}
	}
	if selfIndex < 0 {
		// Only ACTIVE nodes participate in probing.
		cache.plan = nil
		return
	}

	offsets := deriveProbeOffsets(epochID, ProbeAssignmentsPerEpoch, len(peers))
	outbound := make([]probeTarget, 0, len(offsets))
	for _, off := range offsets {
		idx := (selfIndex + off) % len(peers)
		outbound = append(outbound, peers[idx])
	}

	cache.plan = &probePlan{
		epochID:         epochID,
		epochBlocks:     epochBlocks,
		eligiblePeers:   peers,
		outboundTargets: outbound,
		builtAt:         now,
	}
	hm.setProbePlan(cache.plan)
	cache.cursor = 0
	logtrace.Debug(ctx, "Active probing: refreshed epoch plan", logtrace.Fields{
		"epoch":    epochID,
		"peers":    len(peers),
		"targets":  len(outbound),
		"identity": selfIdentity,
	})
}

func (hm *Collector) runProbeRound(ctx context.Context, clients *probeClients, cache *probeCache, timeout time.Duration) {
	if clients == nil || cache == nil || cache.plan == nil || len(cache.plan.outboundTargets) == 0 {
		return
	}

	target := cache.plan.outboundTargets[cache.cursor%len(cache.plan.outboundTargets)]
	cache.cursor++
	hm.probePeerOnce(ctx, clients.grpcClient, clients.grpcOpts, clients.p2pCreds, clients.httpClient, target, timeout)
}

func (hm *Collector) latestBlockHeight(ctx context.Context) (int64, bool) {
	if hm.lumeraClient == nil || hm.lumeraClient.Node() == nil {
		return 0, false
	}
	resp, err := hm.lumeraClient.Node().GetLatestBlock(ctx)
	if err != nil || resp == nil || resp.Block == nil {
		return 0, false
	}
	return resp.Block.Header.Height, true
}

func buildEligiblePeers(supernodes []*sntypes.SuperNode, integrationTest bool) []probeTarget {
	out := make([]probeTarget, 0, len(supernodes))
	for _, sn := range supernodes {
		if sn == nil {
			continue
		}

		peerID := strings.TrimSpace(sn.GetSupernodeAccount())
		if peerID == "" {
			continue
		}

		state := latestState(sn)
		if state != sntypes.SuperNodeStateActive {
			continue
		}

		latestAddr := latestIPAddress(sn)
		host, grpcPort, ok := parseHostAndPort(latestAddr, APIPort)
		if !ok {
			continue
		}

		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			continue // IPv4-only
		}
		ip4 := ip.To4()
		if !isAllowedProbeIPv4(ip4, integrationTest) {
			continue
		}

		p2pPort := P2PPort
		if p := strings.TrimSpace(sn.GetP2PPort()); p != "" {
			if n, err := strconv.ParseUint(p, 10, 16); err == nil && n > 0 {
				p2pPort = int(n)
			}
		}

		var metricsHeight int64
		if m := sn.GetMetrics(); m != nil {
			metricsHeight = m.GetHeight()
		}

		out = append(out, probeTarget{
			identity:      peerID,
			hostIPv4:      ip4.String(),
			grpcPort:      grpcPort,
			p2pPort:       p2pPort,
			gatewayPort:   StatusPort,
			metricsHeight: metricsHeight,
		})
	}
	return out
}

func (hm *Collector) probePeerOnce(
	ctx context.Context,
	grpcProbeClient *grpcclient.Client,
	grpcProbeOpts *grpcclient.ClientOptions,
	p2pCreds *ltc.LumeraTC,
	httpClient *http.Client,
	target probeTarget,
	timeout time.Duration,
) {
	if grpcProbeClient != nil {
		if err := probeGRPC(ctx, grpcProbeClient, grpcProbeOpts, target, timeout); err != nil {
			logtrace.Debug(ctx, "Active probing: gRPC probe failed", logtrace.Fields{
				"peer":              target.identity,
				"grpc_host":         target.hostIPv4,
				"grpc_port":         target.grpcPort,
				logtrace.FieldError: err.Error(),
			})
		}
	}

	if httpClient != nil {
		if err := probeGateway(ctx, httpClient, target, timeout); err != nil {
			logtrace.Debug(ctx, "Active probing: gateway probe failed", logtrace.Fields{
				"peer":              target.identity,
				"gateway_host":      target.hostIPv4,
				"gateway_port":      target.gatewayPort,
				logtrace.FieldError: err.Error(),
			})
		}
	}

	if p2pCreds != nil {
		if err := probeP2P(ctx, p2pCreds, target, timeout); err != nil {
			logtrace.Debug(ctx, "Active probing: P2P probe failed", logtrace.Fields{
				"peer":              target.identity,
				"p2p_host":          target.hostIPv4,
				"p2p_port":          target.p2pPort,
				logtrace.FieldError: err.Error(),
			})
		}
	}
}

func probeGRPC(ctx context.Context, client *grpcclient.Client, opts *grpcclient.ClientOptions, target probeTarget, timeout time.Duration) error {
	if client == nil {
		return fmt.Errorf("gRPC client is nil")
	}

	hostPort := net.JoinHostPort(target.hostIPv4, strconv.Itoa(target.grpcPort))
	addr := ltc.FormatAddressWithIdentity(target.identity, hostPort)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := client.Connect(probeCtx, addr, opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	healthClient := grpc_health_v1.NewHealthClient(conn)
	resp, err := healthClient.Check(probeCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("health status=%v", resp.GetStatus())
	}
	return nil
}

func probeGateway(ctx context.Context, client *http.Client, target probeTarget, timeout time.Duration) error {
	if client == nil {
		return fmt.Errorf("http client is nil")
	}

	urlStr := fmt.Sprintf("http://%s:%d/api/v1/status", target.hostIPv4, target.gatewayPort)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func probeP2P(ctx context.Context, creds *ltc.LumeraTC, target probeTarget, timeout time.Duration) error {
	if creds == nil {
		return fmt.Errorf("p2p creds is nil")
	}
	creds.SetRemoteIdentity(target.identity)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	rawConn, err := d.DialContext(probeCtx, "tcp", net.JoinHostPort(target.hostIPv4, strconv.Itoa(target.p2pPort)))
	if err != nil {
		return err
	}
	defer rawConn.Close()

	_ = rawConn.SetDeadline(time.Now().Add(timeout))

	secureConn, _, err := creds.ClientHandshake(probeCtx, "", rawConn)
	if err != nil {
		return err
	}
	_ = secureConn.Close()

	return nil
}

func deriveProbeOffsets(epochID uint64, k int, n int) []int {
	if k <= 0 || n <= 1 {
		return nil
	}
	maxOffset := n - 1
	if k > maxOffset {
		k = maxOffset
	}

	out := make([]int, 0, k)
	used := make(map[int]struct{}, k)
	for j := 0; len(out) < k; j++ {
		for attempt := 0; ; attempt++ {
			h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d", epochID, j, attempt)))
			v := binary.LittleEndian.Uint64(h[:8])
			offset := int(v%uint64(maxOffset)) + 1 // [1..N-1]
			if _, ok := used[offset]; ok {
				continue
			}
			used[offset] = struct{}{}
			out = append(out, offset)
			break
		}
	}
	return out
}

func withJitter(base time.Duration, fraction float64, rng *rand.Rand) time.Duration {
	if base <= 0 || fraction <= 0 {
		return base
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	maxJitter := int64(float64(base) * fraction)
	if maxJitter <= 0 {
		return base
	}
	// Uniform in [-maxJitter, +maxJitter].
	delta := rng.Int63n(2*maxJitter+1) - maxJitter
	out := base + time.Duration(delta)
	if out < time.Second {
		out = time.Second
	}
	return out
}

func latestState(sn *sntypes.SuperNode) sntypes.SuperNodeState {
	if sn == nil || len(sn.States) == 0 {
		return sntypes.SuperNodeStateUnspecified
	}
	var (
		best       *sntypes.SuperNodeStateRecord
		bestHeight int64 = -1
	)
	for _, st := range sn.States {
		if st == nil {
			continue
		}
		if st.Height > bestHeight {
			bestHeight = st.Height
			best = st
		}
	}
	if best == nil {
		return sntypes.SuperNodeStateUnspecified
	}
	return best.State
}

func latestIPAddress(sn *sntypes.SuperNode) string {
	if sn == nil || len(sn.PrevIpAddresses) == 0 {
		return ""
	}
	var (
		best       *sntypes.IPAddressHistory
		bestHeight int64 = -1
	)
	for _, rec := range sn.PrevIpAddresses {
		if rec == nil {
			continue
		}
		if rec.Height > bestHeight {
			bestHeight = rec.Height
			best = rec
		}
	}
	if best == nil {
		return ""
	}
	return strings.TrimSpace(best.Address)
}

func parseHostAndPort(address string, defaultPort int) (host string, port int, ok bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false
	}

	// If it looks like a URL, parse and use the host[:port] portion.
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
	}

	if h, p, err := net.SplitHostPort(address); err == nil {
		h = strings.TrimSpace(h)
		if h == "" {
			return "", 0, false
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			return h, n, true
		}
		return h, defaultPort, true
	}

	// No port present; return default.
	return address, defaultPort, true
}

func isAllowedProbeIPv4(ip net.IP, integrationTest bool) bool {
	if ip == nil || ip.To4() == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return integrationTest
	}
	return true
}

func waitOrStop(ctx context.Context, stopCh <-chan struct{}, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-stopCh:
		return false
	case <-ctx.Done():
		return false
	}
}
