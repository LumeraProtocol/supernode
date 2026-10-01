package status

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/task"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

// Version is the supernode version, set by the main application
var Version = "dev"

const statusSubsystemTimeout = 8 * time.Second

// SupernodeStatusService provides centralized status information
type SupernodeStatusService struct {
	metrics      *MetricsCollector
	storagePaths []string
	startTime    time.Time
	p2pService   p2p.Client
	lumeraClient lumera.Client
	config       *config.Config
	tracker      task.Tracker
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService(p2pService p2p.Client, lumeraClient lumera.Client, cfg *config.Config, tracker task.Tracker) *SupernodeStatusService {
	storagePaths := []string{}
	if cfg != nil && cfg.BaseDir != "" {
		// disk.Usage reports filesystem totals for the underlying mount. Using the
		// base directory ensures we measure the volume where the supernode stores
		// its data (e.g. /opt when --basedir is /opt/lumera/.supernode).
		storagePaths = []string{cfg.BaseDir}
	}

	return &SupernodeStatusService{
		metrics:      NewMetricsCollector(),
		storagePaths: storagePaths,
		startTime:    time.Now(),
		p2pService:   p2pService,
		lumeraClient: lumeraClient,
		config:       cfg,
		tracker:      tracker,
	}
}

// GetChainID returns the chain ID from the configuration
func (s *SupernodeStatusService) GetChainID() string {
	if s.config != nil {
		return s.config.LumeraClientConfig.ChainID
	}
	return ""
}

func lep6StatusMetrics(s lep6metrics.MetricsSnapshot) *pb.StatusResponse_LEP6Metrics {
	return &pb.StatusResponse_LEP6Metrics{
		RecentChallengeRefs:                  lep6ChallengeRefs(s.RecentChallengeRefs),
		RecentEpochSummaries:                 lep6EpochSummaries(s.RecentEpochSummaries),
		DispatchResultsTotal:                 cloneUint64Map(s.DispatchResultsTotal),
		DispatchResultDetailsTotal:           cloneUint64Map(s.DispatchResultDetailsTotal),
		DispatchSignFailuresTotal:            cloneUint64Map(s.DispatchSignFailuresTotal),
		DispatchInternalFailuresTotal:        cloneUint64Map(s.DispatchInternalFailuresTotal),
		ObserverProofsTotal:                  cloneUint64Map(s.ObserverProofsTotal),
		NoEligibleReasonsTotal:               cloneUint64Map(s.NoEligibleReasonsTotal),
		ActionableResultsTotal:               cloneUint64Map(s.ActionableResultsTotal),
		ParticipationTotal:                   cloneUint64Map(s.ParticipationTotal),
		DispatchThrottledTotal:               cloneUint64Map(s.DispatchThrottledTotal),
		DispatchEpochDurationMillisTotal:     cloneUint64Map(s.DispatchEpochDurationMillisTotal),
		DispatchEpochDurationMillisMax:       cloneUint64Map(s.DispatchEpochDurationMillisMax),
		DispatchEpochDurationCount:           cloneUint64Map(s.DispatchEpochDurationCount),
		TicketDiscoveryTotal:                 cloneUint64Map(s.TicketDiscoveryTotal),
		NoTicketProviderActive:               s.NoTicketProviderActive,
		HealClaimsSubmittedTotal:             cloneUint64Map(s.HealClaimsSubmittedTotal),
		HealClaimsReconciledTotal:            s.HealClaimsReconciledTotal,
		HealVerificationsSubmittedTotal:      cloneUint64Map(s.HealVerificationsSubmittedTotal),
		HealVerificationsAlreadyExistsTotal:  s.HealVerificationsAlreadyExistsTotal,
		HealFinalizePublishesTotal:           s.HealFinalizePublishesTotal,
		HealFinalizeCleanupsTotal:            cloneUint64Map(s.HealFinalizeCleanupsTotal),
		HealOrphanedStagingCleanupsTotal:     s.HealOrphanedStagingCleanupsTotal,
		SelfHealingPendingClaims:             s.SelfHealingPendingClaims,
		SelfHealingStagingBytes:              s.SelfHealingStagingBytes,
		RecheckCandidatesFoundTotal:          s.RecheckCandidatesFoundTotal,
		RecheckEvidenceSubmittedTotal:        cloneUint64Map(s.RecheckEvidenceSubmittedTotal),
		RecheckEvidenceAlreadySubmittedTotal: s.RecheckEvidenceAlreadySubmittedTotal,
		RecheckExecutionFailuresTotal:        cloneUint64Map(s.RecheckExecutionFailuresTotal),
		RecheckPendingCandidates:             s.RecheckPendingCandidates,
	}
}

func lep6EpochSummaries(in []lep6metrics.EpochSummary) []*pb.StatusResponse_LEP6Metrics_LEP6EpochSummary {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pb.StatusResponse_LEP6Metrics_LEP6EpochSummary, 0, len(in))
	for _, e := range in {
		out = append(out, &pb.StatusResponse_LEP6Metrics_LEP6EpochSummary{
			EpochId:             e.EpochID,
			ValidChallenges:     e.ValidChallenges,
			NoEligibleTickets:   e.NoEligibleTickets,
			ProofFailures:       e.ProofFailures,
			FailureReasonsTotal: cloneUint64Map(e.FailureReasonsTotal),
		})
	}
	return out
}

func lep6ChallengeRefs(in []lep6metrics.ChallengeRef) []*pb.StatusResponse_LEP6Metrics_LEP6ChallengeRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pb.StatusResponse_LEP6Metrics_LEP6ChallengeRef, 0, len(in))
	for _, c := range in {
		out = append(out, &pb.StatusResponse_LEP6Metrics_LEP6ChallengeRef{
			ChallengeId:   c.ChallengeID,
			TimestampUnix: c.TimestampUnix,
			EpochId:       c.EpochID,
			ResultClass:   c.ResultClass,
		})
	}
	return out
}

func lep6ChallengeDetail(c lep6metrics.ChallengeSnapshot) *pb.LEP6ChallengeDetailResponse {
	return &pb.LEP6ChallengeDetailResponse{
		ChallengeId:           c.ChallengeID,
		TimestampUnix:         c.TimestampUnix,
		EpochId:               c.EpochID,
		Challenger:            c.Challenger,
		Target:                c.Target,
		TicketId:              c.TicketID,
		Bucket:                c.Bucket,
		ArtifactClass:         c.ArtifactClass,
		ArtifactOrdinal:       c.ArtifactOrdinal,
		ArtifactCount:         c.ArtifactCount,
		ArtifactKey:           c.ArtifactKey,
		ResultClass:           c.ResultClass,
		TranscriptHash:        c.TranscriptHash,
		DerivationHash:        c.DerivationHash,
		ProofHash:             c.ProofHash,
		ObserverCount:         c.ObserverCount,
		AttestationCount:      c.AttestationCount,
		ObserverMismatch:      c.ObserverMismatch,
		Details:               c.Details,
		FailureStage:          c.FailureStage,
		CandidateSource:       c.CandidateSource,
		TargetExpectedHolder:  c.TargetExpectedHolder,
		ObserverCandidates:    append([]string(nil), c.ObserverCandidates...),
		SelectedObservers:     append([]string(nil), c.SelectedObservers...),
		ObserverOutcomes:      append([]string(nil), c.ObserverOutcomes...),
		EffectiveArtifactSize: c.EffectiveArtifactSize,
		RangeLen:              c.RangeLen,
		SizeSource:            c.SizeSource,
	}
}

func cloneUint64Map(in map[string]uint64) map[string]uint64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// GetLEP6ChallengeDetail returns one bounded in-memory LEP-6 challenge detail by id.
func (s *SupernodeStatusService) GetLEP6ChallengeDetail(ctx context.Context, challengeID string) (*pb.LEP6ChallengeDetailResponse, error) {
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return nil, fmt.Errorf("challenge_id is required")
	}
	c, ok := lep6metrics.GetChallenge(challengeID)
	if !ok {
		return nil, fmt.Errorf("LEP-6 challenge %q not found", challengeID)
	}
	return lep6ChallengeDetail(c), nil
}

// GetStatus returns the current system status including optional P2P info
func (s *SupernodeStatusService) GetStatus(ctx context.Context, includeP2PMetrics bool) (*pb.StatusResponse, error) {
	fields := logtrace.Fields{logtrace.FieldMethod: "GetStatus", logtrace.FieldModule: "SupernodeStatusService"}
	logtrace.Debug(ctx, "status request received", fields)

	resp := &pb.StatusResponse{}
	resp.Version = Version
	resp.UptimeSeconds = uint64(time.Since(s.startTime).Seconds())

	cpuUsage, err := s.metrics.CollectCPUMetrics(ctx)
	if err != nil {
		return resp, err
	}
	if resp.Resources == nil {
		resp.Resources = &pb.StatusResponse_Resources{}
	}
	if resp.Resources.Cpu == nil {
		resp.Resources.Cpu = &pb.StatusResponse_Resources_CPU{}
	}
	resp.Resources.Cpu.UsagePercent = cpuUsage
	cores, err := s.metrics.GetCPUCores(ctx)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu cores", logtrace.Fields{logtrace.FieldError: err.Error()})
		cores = 0
	}
	resp.Resources.Cpu.Cores = cores
	memTotal, memUsed, memAvail, memUsedPerc, err := s.metrics.CollectMemoryMetrics(ctx)
	if err != nil {
		return resp, err
	}
	const bytesToGB = 1024 * 1024 * 1024
	if resp.Resources.Memory == nil {
		resp.Resources.Memory = &pb.StatusResponse_Resources_Memory{}
	}
	resp.Resources.Memory.TotalGb = float64(memTotal) / bytesToGB
	resp.Resources.Memory.UsedGb = float64(memUsed) / bytesToGB
	resp.Resources.Memory.AvailableGb = float64(memAvail) / bytesToGB
	resp.Resources.Memory.UsagePercent = memUsedPerc
	if cores > 0 && resp.Resources.Memory.TotalGb > 0 {
		resp.Resources.HardwareSummary = fmt.Sprintf("%d cores / %.0fGB RAM", cores, resp.Resources.Memory.TotalGb)
	}
	// Storage metrics
	if storageInfos := s.metrics.CollectStorageMetrics(ctx, s.storagePaths); len(storageInfos) > 0 {
		// Rationale: report only the first volume everywhere (status + on-chain
		// metrics) to avoid ambiguity across environments where multiple mounts
		// exist (e.g. container overlay + host filesystem). The *selection* of that
		// first volume is based on the configured base directory so eligibility
		// checks reflect where supernode data actually lives.
		si := storageInfos[0]
		resp.Resources.StorageVolumes = append(resp.Resources.StorageVolumes, &pb.StatusResponse_Resources_Storage{
			Path:           si.Path,
			TotalBytes:     si.TotalBytes,
			UsedBytes:      si.UsedBytes,
			AvailableBytes: si.AvailableBytes,
			UsagePercent:   si.UsagePercent,
		})
	}

	// Populate running tasks from injected tracker
	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		if len(snap) > 0 {
			for svc, ids := range snap {
				resp.RunningTasks = append(resp.RunningTasks, &pb.StatusResponse_ServiceTasks{
					ServiceName: svc,
					TaskIds:     ids,
					TaskCount:   int32(len(ids)),
				})
			}
		}
	}

	// LEP-6 metrics are cheap in-memory counters/gauges. Include them on every
	// status response so operators can inspect storage-truth runtime state through
	// the existing status endpoint instead of a LEP-6-only metrics endpoint.
	resp.Lep6Metrics = lep6StatusMetrics(lep6metrics.Snapshot())

	if includeP2PMetrics && s.p2pService != nil {
		// Prepare optional P2P metrics container (only when requested).
		pm := &pb.StatusResponse_P2PMetrics{
			DhtMetrics:           &pb.StatusResponse_P2PMetrics_DhtMetrics{},
			NetworkHandleMetrics: map[string]*pb.StatusResponse_P2PMetrics_HandleCounters{},
			ConnPoolMetrics:      map[string]int64{},
			BanList:              []*pb.StatusResponse_P2PMetrics_BanEntry{},
			Database:             &pb.StatusResponse_P2PMetrics_DatabaseStats{},
			Disk:                 &pb.StatusResponse_P2PMetrics_DiskStatus{},
		}

		// Bound P2P metrics collection so status can't hang if P2P is slow
		p2pCtx, cancel := context.WithTimeout(ctx, statusSubsystemTimeout)
		defer cancel()
		snap, err := s.p2pService.Stats(p2pCtx)
		if err != nil {
			logtrace.Error(ctx, "failed to get p2p stats snapshot", logtrace.Fields{logtrace.FieldError: err.Error()})
			return resp, err
		}

		if resp.Network == nil {
			resp.Network = &pb.StatusResponse_Network{}
		}
		resp.Network.PeersCount = snap.PeersCount
		resp.Network.PeerAddresses = []string{}
		if peers := snap.Peers; len(peers) > 0 {
			resp.Network.PeerAddresses = make([]string, 0, len(peers))
			for _, peer := range peers {
				if peer == nil {
					continue
				}
				resp.Network.PeerAddresses = append(resp.Network.PeerAddresses, fmt.Sprintf("%s@%s:%d", string(peer.ID), peer.IP, peer.Port))
			}
		}

		if snap.DiskInfo != nil {
			pm.Disk.AllMb = snap.DiskInfo.All
			pm.Disk.UsedMb = snap.DiskInfo.Used
			pm.Disk.FreeMb = snap.DiskInfo.Free
		}
		for _, b := range snap.BanList {
			pm.BanList = append(pm.BanList, &pb.StatusResponse_P2PMetrics_BanEntry{Id: b.ID, Ip: b.IP, Port: uint32(b.Port), Count: int32(b.Count), CreatedAtUnix: b.CreatedAt.Unix(), AgeSeconds: int64(b.Age.Seconds())})
		}
		for k, v := range snap.ConnPool {
			pm.ConnPoolMetrics[k] = v
		}

		pm.Database.P2PDbSizeMb = snap.Database.P2PDbSizeMb
		pm.Database.P2PDbRecordsCount = snap.Database.P2PDbRecordsCount

		for k, c := range snap.NetworkHandleMetrics {
			pm.NetworkHandleMetrics[k] = &pb.StatusResponse_P2PMetrics_HandleCounters{Total: c.Total, Success: c.Success, Failure: c.Failure, Timeout: c.Timeout}
		}

		for _, sp := range snap.DHTMetrics.StoreSuccessRecent {
			pm.DhtMetrics.StoreSuccessRecent = append(pm.DhtMetrics.StoreSuccessRecent, &pb.StatusResponse_P2PMetrics_DhtMetrics_StoreSuccessPoint{TimeUnix: sp.Time.Unix(), Requests: int32(sp.Requests), Successful: int32(sp.Successful), SuccessRate: sp.SuccessRate})
		}
		for _, bp := range snap.DHTMetrics.BatchRetrieveRecent {
			pm.DhtMetrics.BatchRetrieveRecent = append(pm.DhtMetrics.BatchRetrieveRecent, &pb.StatusResponse_P2PMetrics_DhtMetrics_BatchRetrievePoint{TimeUnix: bp.Time.Unix(), Keys: int32(bp.Keys), Required: int32(bp.Required), FoundLocal: int32(bp.FoundLocal), FoundNetwork: int32(bp.FoundNet), DurationMs: bp.Duration.Milliseconds()})
		}
		pm.DhtMetrics.HotPathBannedSkips = snap.DHTMetrics.HotPathBannedSkips
		pm.DhtMetrics.HotPathBanIncrements = snap.DHTMetrics.HotPathBanIncrements

		resp.P2PMetrics = pm
	} else if includeP2PMetrics {
		return resp, fmt.Errorf("p2p service is nil")
	}

	if s.config != nil && s.lumeraClient != nil {
		// Bound chain query for latest address to avoid slow network hangs
		chainCtx, cancel := context.WithTimeout(ctx, statusSubsystemTimeout)
		defer cancel()
		if supernodeInfo, err := s.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(chainCtx, s.config.SupernodeConfig.Identity); err == nil && supernodeInfo != nil {
			resp.IpAddress = supernodeInfo.LatestAddress
		} else if err != nil {
			logtrace.Error(ctx, "failed to resolve latest supernode address", logtrace.Fields{logtrace.FieldError: err.Error()})
		}
	}
	return resp, nil
}
