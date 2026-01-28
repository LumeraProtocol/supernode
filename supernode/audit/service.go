package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	supernodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	statusService "github.com/LumeraProtocol/supernode/v2/supernode/status"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

const (
	statusTimeout = 8 * time.Second
	txTimeout     = 30 * time.Second
)

type Service struct {
	lumeraClient lumera.Client
	statusSvc    *statusService.SupernodeStatusService
	keyring      keyring.Keyring

	selfIdentity string
	dbPath       string

	store  *Store
	prober *Prober
}

func NewService(lumeraClient lumera.Client, statusSvc *statusService.SupernodeStatusService, kr keyring.Keyring, selfIdentity, dbPath string) (*Service, error) {
	selfIdentity = strings.TrimSpace(selfIdentity)
	dbPath = strings.TrimSpace(dbPath)
	if lumeraClient == nil {
		return nil, fmt.Errorf("lumera client is nil")
	}
	if statusSvc == nil {
		return nil, fmt.Errorf("status service is nil")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring is nil")
	}
	if selfIdentity == "" {
		return nil, fmt.Errorf("self identity is empty")
	}
	if dbPath == "" {
		return nil, fmt.Errorf("db path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("create audit db directory: %w", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		return nil, err
	}

	prober, err := NewProber(lumeraClient, kr, selfIdentity, ProbeTimeout)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	return &Service{
		lumeraClient: lumeraClient,
		statusSvc:    statusSvc,
		keyring:      kr,
		selfIdentity: selfIdentity,
		dbPath:       dbPath,
		store:        store,
		prober:       prober,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !EnabledByDefault {
		return nil
	}
	if s == nil || s.lumeraClient == nil || s.store == nil || s.prober == nil {
		return fmt.Errorf("audit service not initialized")
	}
	defer func() { _ = s.store.Close() }()

	if InitialStartupDelay > 0 {
		select {
		case <-time.After(InitialStartupDelay):
		case <-ctx.Done():
			return nil
		}
	}

	ticker := time.NewTicker(CurrentWindowPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	window, err := s.lumeraClient.Audit().CurrentWindow(ctx)
	if err != nil || window == nil {
		logtrace.Debug(ctx, "Audit: failed to query current window", logtrace.Fields{logtrace.FieldError: errString(err)})
		return
	}

	windowID := window.GetWindowId()
	sub, found, err := s.store.GetSubmission(windowID)
	if err != nil {
		logtrace.Warn(ctx, "Audit: failed to read local submission state", logtrace.Fields{logtrace.FieldError: err.Error()})
		return
	}
	if found && sub.Status == SubmissionStatusSubmitted {
		return
	}

	if found && !shouldRetry(sub.UpdatedAtUnix, SubmitRetryBackoff) {
		return
	}

	height, heightOK := latestBlockHeight(ctx, s.lumeraClient)
	if heightOK && height > window.GetWindowEndHeight() {
		if found {
			_ = s.store.UpsertSubmission(SubmissionRecord{
				WindowID:         windowID,
				SupernodeAccount: s.selfIdentity,
				ReportJSON:       sub.ReportJSON,
				TxHash:           sub.TxHash,
				Status:           SubmissionStatusFailed,
				AttemptCount:     sub.AttemptCount,
				LastError:        "window ended",
				UpdatedAtUnix:    time.Now().Unix(),
			})
		}
		return
	}

	assigned, err := s.lumeraClient.Audit().AssignedTargets(ctx, s.selfIdentity, windowID)
	if err != nil || assigned == nil {
		logtrace.Debug(ctx, "Audit: failed to query assigned targets", logtrace.Fields{
			"window_id":         windowID,
			"supernode_account": s.selfIdentity,
			logtrace.FieldError: errString(err),
		})
		return
	}

	_ = s.store.UpsertWindow(WindowRecord{
		WindowID:             windowID,
		WindowStartHeight:    assigned.GetWindowStartHeight(),
		KWindow:              uint32(len(assigned.GetTargetSupernodeAccounts())),
		RequiredOpenPorts:    assigned.GetRequiredOpenPorts(),
		TargetSupernodeAccts: assigned.GetTargetSupernodeAccounts(),
		FetchedAtUnix:        time.Now().Unix(),
	})

	selfReport := s.buildSelfReport(ctx, assigned.GetRequiredOpenPorts())
	peerObs := s.buildPeerObservations(ctx, assigned.GetTargetSupernodeAccounts(), assigned.GetRequiredOpenPorts())

	reportJSON, _ := json.Marshal(struct {
		SupernodeAccount string                             `json:"supernode_account"`
		WindowID         uint64                             `json:"window_id"`
		SelfReport       audittypes.AuditSelfReport         `json:"self_report"`
		PeerObservations []*audittypes.AuditPeerObservation `json:"peer_observations"`
	}{
		SupernodeAccount: s.selfIdentity,
		WindowID:         windowID,
		SelfReport:       selfReport,
		PeerObservations: peerObs,
	})

	attemptCount := 1
	if found {
		attemptCount = sub.AttemptCount + 1
	}

	_ = s.store.UpsertSubmission(SubmissionRecord{
		WindowID:         windowID,
		SupernodeAccount: s.selfIdentity,
		ReportJSON:       string(reportJSON),
		Status:           SubmissionStatusPending,
		AttemptCount:     attemptCount,
		UpdatedAtUnix:    time.Now().Unix(),
	})

	txCtx, cancel := context.WithTimeout(ctx, txTimeout)
	defer cancel()

	txResp, err := s.lumeraClient.AuditMsg().SubmitAuditReport(txCtx, windowID, selfReport, peerObs)
	if err != nil {
		_ = s.store.UpsertSubmission(SubmissionRecord{
			WindowID:         windowID,
			SupernodeAccount: s.selfIdentity,
			ReportJSON:       string(reportJSON),
			Status:           SubmissionStatusPending,
			AttemptCount:     attemptCount,
			LastError:        err.Error(),
			UpdatedAtUnix:    time.Now().Unix(),
		})
		logtrace.Debug(ctx, "Audit: submit report failed", logtrace.Fields{
			"window_id":         windowID,
			"supernode_account": s.selfIdentity,
			logtrace.FieldError: err.Error(),
		})
		return
	}

	txHash := ""
	if txResp != nil && txResp.TxResponse != nil {
		txHash = txResp.TxResponse.TxHash
	}

	_ = s.store.UpsertSubmission(SubmissionRecord{
		WindowID:         windowID,
		SupernodeAccount: s.selfIdentity,
		ReportJSON:       string(reportJSON),
		TxHash:           txHash,
		Status:           SubmissionStatusSubmitted,
		AttemptCount:     attemptCount,
		UpdatedAtUnix:    time.Now().Unix(),
	})
}

func (s *Service) buildSelfReport(ctx context.Context, requiredPorts []uint32) audittypes.AuditSelfReport {
	self := audittypes.AuditSelfReport{
		InboundPortStates:  make([]audittypes.PortState, len(requiredPorts)),
		FailedActionsCount: 0,
	}
	for i := range self.InboundPortStates {
		self.InboundPortStates[i] = audittypes.PortState_PORT_STATE_UNKNOWN
	}

	statusCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()

	resp, err := s.statusSvc.GetStatus(statusCtx, false)
	if err != nil || resp == nil || resp.Resources == nil {
		return self
	}
	if resp.Resources.Cpu != nil {
		self.CpuUsagePercent = resp.Resources.Cpu.UsagePercent
	}
	if resp.Resources.Memory != nil {
		self.MemUsagePercent = resp.Resources.Memory.UsagePercent
	}
	if len(resp.Resources.StorageVolumes) > 0 && resp.Resources.StorageVolumes[0] != nil {
		self.DiskUsagePercent = resp.Resources.StorageVolumes[0].UsagePercent
	}
	return self
}

func (s *Service) buildPeerObservations(ctx context.Context, targetAccounts []string, requiredPorts []uint32) []*audittypes.AuditPeerObservation {
	if len(targetAccounts) == 0 {
		return nil
	}

	out := make([]*audittypes.AuditPeerObservation, len(targetAccounts))

	concurrency := MaxConcurrentProbes
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)

	type result struct {
		i   int
		obs *audittypes.AuditPeerObservation
	}
	results := make(chan result, len(targetAccounts))

	unknownStates := make([]audittypes.PortState, len(requiredPorts))
	for i := range unknownStates {
		unknownStates[i] = audittypes.PortState_PORT_STATE_UNKNOWN
	}

	spawned := 0
	for i, target := range targetAccounts {
		i := i
		target := strings.TrimSpace(target)

		select {
		case sem <- struct{}{}:
			spawned++
		case <-ctx.Done():
			out[i] = &audittypes.AuditPeerObservation{
				TargetSupernodeAccount: target,
				PortStates:             append([]audittypes.PortState(nil), unknownStates...),
			}
			continue
		}

		go func() {
			defer func() { <-sem }()

			host := s.resolveTargetHost(ctx, target)
			states := make([]audittypes.PortState, len(requiredPorts))
			for j, port := range requiredPorts {
				states[j] = s.prober.ProbePortState(ctx, target, host, port, ProbeTimeout)
			}

			select {
			case results <- result{i: i, obs: &audittypes.AuditPeerObservation{
				TargetSupernodeAccount: target,
				PortStates:             states,
			}}:
			case <-ctx.Done():
			}
		}()
	}

collectResults:
	for remaining := spawned; remaining > 0; remaining-- {
		select {
		case r := <-results:
			out[r.i] = r.obs
		case <-ctx.Done():
			break collectResults
		}
	}

	for i, obs := range out {
		if obs != nil {
			continue
		}
		target := strings.TrimSpace(targetAccounts[i])
		out[i] = &audittypes.AuditPeerObservation{
			TargetSupernodeAccount: target,
			PortStates:             append([]audittypes.PortState(nil), unknownStates...),
		}
	}

	return out
}

func (s *Service) resolveTargetHost(ctx context.Context, targetSupernodeAccount string) string {
	if s.lumeraClient == nil || s.lumeraClient.SuperNode() == nil {
		return ""
	}

	sn, err := s.lumeraClient.SuperNode().GetSupernodeBySupernodeAddress(ctx, targetSupernodeAccount)
	if err != nil || sn == nil {
		return ""
	}

	addr, err := supernodemod.GetLatestIP(sn)
	if err != nil {
		return ""
	}

	host, _, ok := parseHostAndPort(addr)
	if !ok {
		return ""
	}
	return host
}

func parseHostAndPort(address string) (host string, port int, ok bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false
	}

	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
	}

	if h, p, err := net.SplitHostPort(address); err == nil {
		h = strings.TrimSpace(h)
		if h == "" {
			return "", 0, false
		}
		if p != "" {
			return h, 0, true
		}
		return h, 0, true
	}

	return address, 0, true
}

func latestBlockHeight(ctx context.Context, client lumera.Client) (int64, bool) {
	if client == nil || client.Node() == nil {
		return 0, false
	}
	resp, err := client.Node().GetLatestBlock(ctx)
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

func shouldRetry(updatedAtUnix int64, backoff time.Duration) bool {
	if backoff <= 0 {
		return true
	}
	if updatedAtUnix <= 0 {
		return true
	}
	return time.Since(time.Unix(updatedAtUnix, 0)) >= backoff
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
