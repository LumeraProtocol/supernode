package self_healing

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"lukechampine.com/blake3"
)

const (
	defaultPollInterval   = 10 * time.Second
	filesPerChallenger    = uint32(2)
	recipientReplicaCount = uint32(5)
)

type Config struct {
	Enabled      bool
	PollInterval time.Duration
}

type Service struct {
	cfg      Config
	identity string
	lumera   lumera.Client
	p2p      p2p.Client
}

func NewService(identity string, lumeraClient lumera.Client, p2pClient p2p.Client, cfg Config) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.Node() == nil || lumeraClient.Audit() == nil {
		return nil, fmt.Errorf("lumera client is missing required modules")
	}
	if p2pClient == nil {
		return nil, fmt.Errorf("p2p client is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}

	return &Service{cfg: cfg, identity: identity, lumera: lumeraClient, p2p: p2pClient}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		<-ctx.Done()
		return nil
	}

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	var lastRunEpoch uint64
	var lastRunOK bool

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			height, ok := s.latestHeight(ctx)
			if !ok {
				continue
			}
			params, ok := s.auditParams(ctx)
			if !ok {
				continue
			}
			epochID, ok := deterministic.EpochID(height, params.EpochZeroHeight, params.EpochLengthBlocks)
			if !ok {
				continue
			}
			if lastRunOK && lastRunEpoch == epochID {
				continue
			}

			anchorResp, err := s.lumera.Audit().GetEpochAnchor(ctx, epochID)
			if err != nil || anchorResp == nil || anchorResp.Anchor.EpochId != epochID {
				continue
			}

			anchor := anchorResp.Anchor
			challengers := deterministic.SelectChallengers(anchor.ActiveSupernodeAccounts, anchor.Seed, epochID, params.ScChallengersPerEpoch)
			if !contains(challengers, s.identity) {
				lastRunEpoch = epochID
				lastRunOK = true
				continue
			}

			if err := s.runEpoch(ctx, anchor); err != nil {
				logtrace.Warn(ctx, "self-healing epoch run error", logtrace.Fields{"epoch_id": epochID, "error": err.Error()})
				lastRunEpoch = epochID
				lastRunOK = false
				continue
			}
			lastRunEpoch = epochID
			lastRunOK = true
		}
	}
}

func (s *Service) latestHeight(ctx context.Context) (int64, bool) {
	resp, err := s.lumera.Node().GetLatestBlock(ctx)
	if err != nil || resp == nil {
		return 0, false
	}
	if sdkBlk := resp.GetSdkBlock(); sdkBlk != nil {
		return sdkBlk.Header.Height, true
	}
	if blk := resp.GetBlock(); blk != nil {
		return blk.Header.Height, true
	}
	return 0, false
}

func (s *Service) auditParams(ctx context.Context) (audittypes.Params, bool) {
	resp, err := s.lumera.Audit().GetParams(ctx)
	if err != nil || resp == nil {
		return audittypes.Params{}, false
	}
	p := resp.Params.WithDefaults()
	if err := p.Validate(); err != nil {
		return audittypes.Params{}, false
	}
	return p, true
}

func (s *Service) runEpoch(ctx context.Context, anchor audittypes.EpochAnchor) error {
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)

	keys, err := s.p2p.GetLocalKeys(ctx, &from, to)
	if err != nil {
		return fmt.Errorf("get local keys: %w", err)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)

	fileKeys := deterministic.SelectFileKeys(keys, anchor.Seed, anchor.EpochId, s.identity, filesPerChallenger)
	for _, fileKey := range fileKeys {
		replicas, err := deterministic.SelectReplicaSet(anchor.TargetSupernodeAccounts, fileKey, recipientReplicaCount)
		if err != nil {
			continue
		}
		recipient := selectRecipient(replicas, s.identity)
		if recipient == "" {
			continue
		}
		challengeID := deriveChallengeID(anchor.Seed, anchor.EpochId, fileKey, s.identity, recipient)
		logtrace.Info(ctx, "self-healing challenge planned", logtrace.Fields{
			"epoch_id":      anchor.EpochId,
			"challenge_id":  challengeID,
			"file_key":      fileKey,
			"challenger_id": s.identity,
			"recipient_id":  recipient,
		})
	}
	return nil
}

func deriveChallengeID(seed []byte, epochID uint64, fileKey, challenger, recipient string) string {
	msg := []byte("sh:challenge:" + hex.EncodeToString(seed) + ":" + strconv.FormatUint(epochID, 10) + ":" + fileKey + ":" + challenger + ":" + recipient)
	sum := blake3.Sum256(msg)
	return hex.EncodeToString(sum[:])
}

func selectRecipient(replicas []string, self string) string {
	for _, id := range replicas {
		if id != self {
			return id
		}
	}
	return ""
}

func contains(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}
