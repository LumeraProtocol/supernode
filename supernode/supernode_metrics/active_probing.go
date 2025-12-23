package supernode_metrics

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
)

type probeTarget struct {
	identity    string
	hostIPv4    string
	grpcPort    int
	p2pPort     int
	gatewayPort int

	metricsHeight int64
}

// probingLoop is a deterministic traffic generator:
//   - It computes a per-epoch sender->receiver assignment from chain inputs.
//   - It probes a bounded number of peers each epoch (K targets per ACTIVE sender).
//   - Probe results are not used as evidence; they exist to induce inbound traffic
//     on the target, which the target records as reachability evidence.
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

	var plan *probePlan
	cursor := 0
	for {
		var resetCursor bool
		// Rebuild the probing plan when the epoch changes. The plan is also stored
		// on the Collector so other components (quorum, logs) can use a consistent
		// snapshot for the current epoch.
		plan, resetCursor = hm.refreshProbePlanIfNeeded(ctx, plan, selfIdentity)
		if resetCursor {
			cursor = 0
		}
		hm.runProbeRound(ctx, clients, plan, &cursor, probeTimeout)

		outboundTargets := 0
		if plan != nil {
			outboundTargets = len(plan.outboundTargets)
		}
		// Spread probes across the reporting interval when possible so we generate
		// inbound evidence continuously rather than in bursts.
		if !hm.waitForNextProbe(ctx, rng, outboundTargets) {
			return
		}
	}
}

type probePlan struct {
	epochID         uint64
	epochBlocks     uint64
	peersByID       map[string]probeTarget
	senders         []probeTarget
	receivers       []probeTarget
	outboundTargets []probeTarget
	expectedInbound map[string]int
	assignedProbers map[string][]string
	chainID         string
	randomnessHex   string

	outAttempted atomic.Uint64
	outSuccess   atomic.Uint64
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

func (hm *Collector) waitForNextProbe(ctx context.Context, rng *rand.Rand, outboundTargets int) bool {
	base := defaultProbeInterval
	if hm != nil && hm.reportInterval > 0 && outboundTargets > 0 {
		base = probeRoundInterval(hm.reportInterval, outboundTargets)
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

func (hm *Collector) refreshProbePlanIfNeeded(ctx context.Context, plan *probePlan, selfIdentity string) (*probePlan, bool) {
	selfIdentity = strings.TrimSpace(selfIdentity)
	if selfIdentity == "" || hm.lumeraClient == nil {
		hm.setProbePlan(nil)
		return nil, true
	}

	height, heightOK := hm.latestBlockHeight(ctx)
	if !heightOK {
		// Without chain height we cannot compute a deterministic epoch plan.
		hm.setProbePlan(nil)
		return nil, true
	}

	epochBlocks := hm.probingEpochBlocks()
	if epochBlocks == 0 {
		hm.setProbePlan(nil)
		return nil, true
	}
	epochID := uint64(height) / epochBlocks
	reachability.SetCurrentEpochID(epochID)
	if plan != nil && plan.epochID == epochID && plan.epochBlocks == epochBlocks {
		return plan, false
	}

	resp, err := hm.lumeraClient.SuperNode().ListSuperNodes(ctx)
	if err != nil || resp == nil {
		logtrace.Warn(ctx, "Active probing: failed to refresh peer list", logtrace.Fields{logtrace.FieldError: fmt.Sprintf("%v", err)})
		hm.setProbePlan(nil)
		return nil, true
	}

	senders, receivers, peersByID := buildProbeCandidates(resp.Supernodes)
	if len(senders) == 0 || len(receivers) < 2 {
		// Need at least 2 receivers to satisfy the "avoid self-target" rule.
		hm.setProbePlan(nil)
		return nil, true
	}

	selfIsSender := false
	for i := range senders {
		if senders[i].identity == selfIdentity {
			selfIsSender = true
			break
		}
	}

	selfIsReceiver := false
	for i := range receivers {
		if receivers[i].identity == selfIdentity {
			selfIsReceiver = true
			break
		}
	}
	if !selfIsReceiver {
		// If we aren't even a receiver, the epoch snapshot isn't useful for this node.
		hm.setProbePlan(nil)
		return nil, true
	}

	chainID, randBytes, randHex, ok := hm.epochAssignmentInputs(ctx, epochID, epochBlocks)
	if !ok {
		hm.setProbePlan(nil)
		return nil, true
	}
	asn := buildAssignment(chainID, epochID, randBytes, senders, receivers, ProbeAssignmentsPerEpoch)

	var outboundIDs []string
	if selfIsSender {
		outboundIDs = asn.bySender[selfIdentity]
	}
	outbound := make([]probeTarget, 0, len(outboundIDs))
	for _, id := range outboundIDs {
		if t, ok := peersByID[id]; ok {
			outbound = append(outbound, t)
		}
	}

	plan = &probePlan{
		epochID:         epochID,
		epochBlocks:     epochBlocks,
		peersByID:       peersByID,
		senders:         senders,
		receivers:       receivers,
		outboundTargets: outbound,
		expectedInbound: asn.expected,
		assignedProbers: asn.byRecv,
		chainID:         chainID,
		randomnessHex:   randHex,
	}
	hm.setProbePlan(plan)
	logtrace.Debug(ctx, "Active probing: refreshed epoch plan", logtrace.Fields{
		"epoch":    epochID,
		"senders":  len(senders),
		"rcvrs":    len(receivers),
		"targets":  len(outbound),
		"identity": selfIdentity,
	})
	return plan, true
}

func (hm *Collector) runProbeRound(ctx context.Context, clients *probeClients, plan *probePlan, cursor *int, timeout time.Duration) {
	if clients == nil || plan == nil || cursor == nil || len(plan.outboundTargets) == 0 {
		return
	}

	// Round-robin across the outbound target set so each target receives probes
	// regularly across the epoch/report interval.
	target := plan.outboundTargets[*cursor%len(plan.outboundTargets)]
	*cursor++
	grpcOK, httpOK, p2pOK := hm.probePeerOnce(ctx, clients.grpcClient, clients.grpcOpts, clients.p2pCreds, clients.httpClient, target, timeout)
	plan.outAttempted.Add(1)
	if grpcOK && httpOK && p2pOK {
		plan.outSuccess.Add(1)
	}
}

func (hm *Collector) latestBlockHeight(ctx context.Context) (int64, bool) {
	if hm.lumeraClient == nil || hm.lumeraClient.Node() == nil {
		return 0, false
	}
	resp, err := hm.lumeraClient.Node().GetLatestBlock(ctx)
	if err != nil || resp == nil {
		return 0, false
	}
	if sdkBlk := resp.GetSdkBlock(); sdkBlk != nil {
		return sdkBlk.GetHeader().Height, true
	}
	if blk := resp.GetBlock(); blk != nil {
		return blk.Header.Height, true
	}
	return 0, false
}

func buildProbeCandidates(supernodes []*sntypes.SuperNode) (senders []probeTarget, receivers []probeTarget, peersByID map[string]probeTarget) {
	peersByID = make(map[string]probeTarget, len(supernodes))
	senders = make([]probeTarget, 0, len(supernodes))
	receivers = make([]probeTarget, 0, len(supernodes))

	for _, sn := range supernodes {
		if sn == nil {
			continue
		}

		peerID := strings.TrimSpace(sn.GetSupernodeAccount())
		if peerID == "" {
			continue
		}

		state := latestState(sn)
		if state == sntypes.SuperNodeStateStopped {
			continue
		}
		if state != sntypes.SuperNodeStateActive && state != sntypes.SuperNodeStatePostponed {
			continue
		}

		latestAddr := latestIPAddress(sn)
		host, grpcPort, ok := parseHostAndPort(latestAddr, APIPort)
		if !ok {
			continue
		}

		host = strings.TrimSpace(host)
		if host == "" {
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

		t := probeTarget{
			identity:      peerID,
			hostIPv4:      host,
			grpcPort:      grpcPort,
			p2pPort:       p2pPort,
			gatewayPort:   StatusPort,
			metricsHeight: metricsHeight,
		}
		peersByID[peerID] = t
		receivers = append(receivers, t)
		if state == sntypes.SuperNodeStateActive {
			senders = append(senders, t)
		}
	}
	sort.Slice(senders, func(i, j int) bool { return senders[i].identity < senders[j].identity })
	sort.Slice(receivers, func(i, j int) bool { return receivers[i].identity < receivers[j].identity })
	return senders, receivers, peersByID
}

func (hm *Collector) probePeerOnce(
	ctx context.Context,
	grpcProbeClient *grpcclient.Client,
	grpcProbeOpts *grpcclient.ClientOptions,
	p2pCreds *ltc.LumeraTC,
	httpClient *http.Client,
	target probeTarget,
	timeout time.Duration,
) (grpcOK bool, httpOK bool, p2pOK bool) {
	if grpcProbeClient != nil {
		if err := probeGRPC(ctx, grpcProbeClient, grpcProbeOpts, target, timeout); err != nil {
			logtrace.Debug(ctx, "Active probing: gRPC probe failed", logtrace.Fields{
				"peer":              target.identity,
				"grpc_host":         target.hostIPv4,
				"grpc_port":         target.grpcPort,
				logtrace.FieldError: err.Error(),
			})
		} else {
			grpcOK = true
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
		} else {
			httpOK = true
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
		} else {
			p2pOK = true
		}
	}

	return grpcOK, httpOK, p2pOK
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

	urlStr := gatewayStatusURL(target.hostIPv4, target.gatewayPort)
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

func gatewayStatusURL(host string, port int) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	u := url.URL{Scheme: "http", Host: hostPort, Path: "/api/v1/status"}
	return u.String()
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

func (hm *Collector) epochAssignmentInputs(ctx context.Context, epochID uint64, epochBlocks uint64) (chainID string, randomness []byte, randomnessHex string, ok bool) {
	if hm == nil || hm.lumeraClient == nil || hm.lumeraClient.Node() == nil {
		return "", nil, "", false
	}

	info, err := hm.lumeraClient.Node().GetNodeInfo(ctx)
	if err != nil || info == nil || info.DefaultNodeInfo == nil {
		return "", nil, "", false
	}
	chainID = strings.TrimSpace(info.DefaultNodeInfo.Network)
	if chainID == "" {
		return "", nil, "", false
	}

	epochStartHeight := int64(epochID * epochBlocks)
	blk, err := hm.lumeraClient.Node().GetBlockByHeight(ctx, epochStartHeight)
	if err != nil || blk == nil {
		return "", nil, "", false
	}
	if blkID := blk.GetBlockId(); blkID != nil && len(blkID.Hash) > 0 {
		randomness = blkID.Hash
	} else if sdkBlk := blk.GetSdkBlock(); sdkBlk != nil && len(sdkBlk.GetHeader().AppHash) > 0 {
		randomness = sdkBlk.GetHeader().AppHash
	} else if legacyBlk := blk.GetBlock(); legacyBlk != nil && len(legacyBlk.Header.AppHash) > 0 {
		randomness = legacyBlk.Header.AppHash
	} else {
		return "", nil, "", false
	}

	randomnessHex = fmt.Sprintf("%x", randomness)
	return chainID, randomness, randomnessHex, true
}
