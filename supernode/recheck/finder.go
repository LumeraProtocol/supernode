package recheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

type FinderConfig struct {
	LookbackEpochs uint64
	MaxPerTick     int
}

func (c FinderConfig) withDefaults() FinderConfig {
	if c.LookbackEpochs == 0 {
		c.LookbackEpochs = DefaultLookbackEpochs
	}
	if c.MaxPerTick <= 0 {
		c.MaxPerTick = DefaultMaxPerTick
	}
	return c
}

type Finder struct {
	audit     AuditReader
	store     Store
	reporters ReporterSource
	self      string
	cfg       FinderConfig
}

func NewFinder(audit AuditReader, store Store, self string, cfg FinderConfig) *Finder {
	return NewFinderWithReporters(audit, store, self, cfg, NewStaticReporterSource(self))
}

func NewFinderWithReporters(audit AuditReader, store Store, self string, cfg FinderConfig, reporters ReporterSource) *Finder {
	self = strings.TrimSpace(self)
	if reporters == nil {
		reporters = NewStaticReporterSource(self)
	}
	return &Finder{audit: audit, store: store, reporters: reporters, self: self, cfg: cfg.withDefaults()}
}

func (f *Finder) Find(ctx context.Context) ([]Candidate, error) {
	if f.audit == nil || f.store == nil {
		return nil, fmt.Errorf("recheck finder missing deps")
	}
	cur, err := f.audit.GetCurrentEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("current epoch: %w", err)
	}
	if cur == nil || cur.EpochId == 0 {
		return nil, nil
	}
	start := uint64(1)
	if cur.EpochId > f.cfg.LookbackEpochs {
		start = cur.EpochId - f.cfg.LookbackEpochs
	}
	reporters, err := f.reporters.ReporterAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, f.cfg.MaxPerTick)
	seen := map[string]struct{}{}
	for epoch := cur.EpochId; epoch >= start; epoch-- {
		results := make([]Candidate, 0)
		for _, reporter := range reporters {
			rep, err := f.audit.GetEpochReportsByReporter(ctx, reporter, epoch)
			if err != nil {
				return nil, fmt.Errorf("epoch reports reporter %s epoch %d: %w", reporter, epoch, err)
			}
			if rep == nil {
				continue
			}
			for _, report := range rep.Reports {
				results = append(results, candidatesFromReport(epoch, report)...)
			}
		}
		if len(results) == 0 {
			if epoch == start {
				break
			}
			continue
		}
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].TicketID == results[j].TicketID {
				return results[i].TargetAccount < results[j].TargetAccount
			}
			return results[i].TicketID < results[j].TicketID
		})
		for _, c := range results {
			if !c.Valid() || c.TargetAccount == f.self || c.OriginalReporter == f.self {
				continue
			}
			key := fmt.Sprintf("%d/%s", c.EpochID, c.TicketID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			done, err := f.store.HasRecheckSubmission(ctx, c.EpochID, c.TicketID)
			if err != nil {
				return nil, err
			}
			if done {
				continue
			}
			out = append(out, c)
			if len(out) >= f.cfg.MaxPerTick {
				return out, nil
			}
		}
		if epoch == 0 || epoch == start {
			break
		}
	}
	return out, nil
}

func candidatesFromReport(epochID uint64, report audittypes.EpochReport) []Candidate {
	out := make([]Candidate, 0, len(report.StorageProofResults))
	for _, r := range report.StorageProofResults {
		if r == nil {
			continue
		}
		out = append(out, Candidate{
			EpochID:                  epochID,
			TargetAccount:            strings.TrimSpace(r.TargetSupernodeAccount),
			TicketID:                 strings.TrimSpace(r.TicketId),
			ChallengedTranscriptHash: strings.TrimSpace(r.TranscriptHash),
			OriginalReporter:         strings.TrimSpace(r.ChallengerSupernodeAccount),
			OriginalResultClass:      r.ResultClass,
		})
	}
	return out
}
