package recheck

import (
	"context"
	"fmt"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

type Config struct {
	Enabled        bool
	LookbackEpochs uint64
	MaxPerTick     int
	TickInterval   time.Duration
	Jitter         time.Duration
}

func (c Config) WithDefaults() Config {
	if c.LookbackEpochs == 0 {
		c.LookbackEpochs = DefaultLookbackEpochs
	}
	if c.MaxPerTick <= 0 {
		c.MaxPerTick = DefaultMaxPerTick
	}
	if c.TickInterval <= 0 {
		c.TickInterval = DefaultTickInterval
	}
	if c.Jitter < 0 {
		c.Jitter = 0
	}
	return c
}

type Service struct {
	cfg       Config
	audit     AuditReader
	finder    *Finder
	rechecker Rechecker
	attestor  *Attestor
}

func NewService(cfg Config, audit AuditReader, store Store, rechecker Rechecker, attestor *Attestor, self string) (*Service, error) {
	return NewServiceWithReporters(cfg, audit, store, rechecker, attestor, self, NewStaticReporterSource(self))
}

func NewServiceWithReporters(cfg Config, audit AuditReader, store Store, rechecker Rechecker, attestor *Attestor, self string, reporters ReporterSource) (*Service, error) {
	cfg = cfg.WithDefaults()
	if audit == nil || store == nil || attestor == nil || rechecker == nil || reporters == nil {
		return nil, fmt.Errorf("recheck service missing deps")
	}
	finder := NewFinderWithReporters(audit, store, self, FinderConfig{LookbackEpochs: cfg.LookbackEpochs, MaxPerTick: cfg.MaxPerTick}, reporters)
	return &Service{cfg: cfg, audit: audit, finder: finder, rechecker: rechecker, attestor: attestor}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		<-ctx.Done()
		return nil
	}
	if s.cfg.Jitter > 0 {
		select {
		case <-time.After(s.cfg.Jitter):
		case <-ctx.Done():
			return nil
		}
	}
	if err := s.Tick(ctx); err != nil {
		logtrace.Warn(ctx, "lep6 recheck: tick failed", logtrace.Fields{"error": err.Error()})
	}
	t := time.NewTicker(s.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				logtrace.Warn(ctx, "lep6 recheck: tick failed", logtrace.Fields{"error": err.Error()})
			}
		}
	}
}

func (s *Service) Tick(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	params, err := s.audit.GetParams(ctx)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	if params == nil || params.Params.StorageTruthEnforcementMode == audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED {
		return nil
	}
	candidates, err := s.finder.Find(ctx)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil
		}
		result, err := s.rechecker.Recheck(ctx, c)
		if err != nil {
			logtrace.Warn(ctx, "lep6 recheck: execution failed", logtrace.Fields{"epoch_id": c.EpochID, "ticket_id": c.TicketID, "error": err.Error()})
			continue
		}
		if err := s.attestor.Submit(ctx, c, result); err != nil {
			logtrace.Warn(ctx, "lep6 recheck: submit failed", logtrace.Fields{"epoch_id": c.EpochID, "ticket_id": c.TicketID, "error": err.Error()})
		}
	}
	return nil
}
