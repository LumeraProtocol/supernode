// Package self_healing implements the LEP-6 chain-driven heal-op runtime.
//
// # Architecture
//
// LEP-6 §18-§22 (Workstream C) replaces the gonode-era peer-watchlist self-
// healing with a chain-mediated three-phase flow. The chain (lumera/x/audit)
// owns role assignment via HealOp.HealerSupernodeAccount + .VerifierSupernode
// Accounts, and quorum via MsgClaimHealComplete + MsgSubmitHealVerification
// (n/2+1 positive verifications). The supernode side is purely an executor:
//
//	Phase 1 — RECONSTRUCT (no publish)
//	  Healer fetches symbols from KAD, RaptorQ-decodes, verifies hash against
//	  Action.DataHash, re-encodes, STAGES to local disk, then submits
//	  MsgClaimHealComplete{HealManifestHash}. The reconstructed file MUST NOT
//	  enter KAD before chain VERIFIED — §19 healer-served path.
//
//	Phase 2 — VERIFY
//	  Each verifier fetches the reconstructed bytes from the assigned healer
//	  via supernode.SelfHealingService/ServeReconstructedArtefacts, hashes
//	  them with cascadekit.ComputeBlake3DataHashB64 (= Action.DataHash recipe),
//	  compares against op.ResultHash (NOT Action.DataHash — chain-side
//	  enforcement at lumera/x/audit/v1/keeper/msg_storage_truth.go:291), and
//	  submits MsgSubmitHealVerification{verified, hash}. The "compare against
//	  op.ResultHash" choice is the v3-plan landmine pinned by
//	  TestVerifier_ComparesAgainstOpResultHash.
//
//	Phase 3 — PUBLISH (only on VERIFIED)
//	  Healer's finalizer polls staging entries, calls
//	  cascadeService.PublishStagedArtefacts on op.Status == VERIFIED, then
//	  deletes the staging dir. On FAILED / EXPIRED, the staging dir is
//	  deleted with no publish — chain may reschedule with a different healer.
//
// # Concurrency
//
// Three-layer dedup so a process restart can never double-submit:
//  1. sync.Map keyed on (heal_op_id, role) for in-flight locking.
//  2. Buffered semaphore (default 2) capping concurrent RaptorQ reseeds —
//     reseed is RAM-heavy. Verification semaphore default 4, publish 2.
//  3. SQLite tables heal_claims_submitted + heal_verifications_submitted
//     (pkg/storage/queries/self_healing_lep6.go) for restart dedup.
//
// # Mode gate
//
// When params.StorageTruthEnforcementMode == UNSPECIFIED the chain creates
// no heal-ops, so the dispatcher early-returns from Service.tick. The check
// also serves as a final supernode-side guard.
package self_healing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	query "github.com/cosmos/cosmos-sdk/types/query"
	"golang.org/x/sync/semaphore"
)

// Defaults captured here for clarity at the boundary; Config exposes overrides.
const (
	defaultPollInterval               = 30 * time.Second
	defaultMaxConcurrentReconstructs  = 2
	defaultMaxConcurrentVerifications = 4
	defaultMaxConcurrentPublishes     = 2
	defaultStagingRoot                = "heal-staging"
	defaultVerifierFetchTimeout       = 60 * time.Second
	defaultVerifierFetchAttempts      = 3
	defaultVerifierBackoffBase        = 2 * time.Second
	defaultAuditQueryTimeout          = 10 * time.Second
	// defaultDispatchOpTimeout caps the wall time any single per-op
	// dispatcher goroutine (healer/verifier/publisher) may run before its
	// derived ctx cancels. Wave 2 / M2 fix — prevents a wedged peer fetch
	// or hung RaptorQ from holding its semaphore slot + inFlight key
	// forever.
	defaultDispatchOpTimeout = 15 * time.Minute
)

// Config captures supernode-binary-owned tunables for the LEP-6 heal runtime.
type Config struct {
	// Enabled toggles the entire dispatcher. Independent of the chain mode
	// gate; if Enabled=false the service never runs even when chain mode is
	// FULL. Used for staged rollouts.
	Enabled                    bool
	PollInterval               time.Duration
	MaxConcurrentReconstructs  int
	MaxConcurrentVerifications int
	MaxConcurrentPublishes     int

	// StagingRoot is the local directory under which per-heal-op staging
	// dirs are created. Default: ~/.supernode/heal-staging/.
	StagingRoot string

	// VerifierFetchTimeout / VerifierFetchAttempts / VerifierBackoffBase
	// shape the retry policy verifiers use when fetching from the assigned
	// healer. After exhausting attempts, verifier submits verified=false
	// with reason "fetch_failed".
	VerifierFetchTimeout  time.Duration
	VerifierFetchAttempts int
	VerifierBackoffBase   time.Duration

	// AuditQueryTimeout bounds each chain query made by the dispatcher. A
	// wedged status/params query must not pin the whole tick forever and starve
	// other roles (especially verifier dispatch while a healer-reported op is
	// waiting on quorum before deadline).
	AuditQueryTimeout time.Duration

	// DispatchOpTimeout is a hard ceiling on each per-op goroutine
	// (healer reconstruct+claim, verifier fetch+submit, publisher).
	// Wave 2 / M2 fix — prevents semaphore-slot/inFlight-key leak on a
	// wedged peer fetch or hung RaptorQ. 0 → defaultDispatchOpTimeout.
	DispatchOpTimeout time.Duration

	// KeyName is the supernode's keyring key used to sign claim/verification
	// txs. Must match the on-chain HealerSupernodeAccount /
	// VerifierSupernodeAccount.
	KeyName string
}

func (c Config) withDefaults() Config {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.MaxConcurrentReconstructs <= 0 {
		c.MaxConcurrentReconstructs = defaultMaxConcurrentReconstructs
	}
	if c.MaxConcurrentVerifications <= 0 {
		c.MaxConcurrentVerifications = defaultMaxConcurrentVerifications
	}
	if c.MaxConcurrentPublishes <= 0 {
		c.MaxConcurrentPublishes = defaultMaxConcurrentPublishes
	}
	if strings.TrimSpace(c.StagingRoot) == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.StagingRoot = filepath.Join(home, ".supernode", defaultStagingRoot)
		} else {
			c.StagingRoot = filepath.Join(os.TempDir(), defaultStagingRoot)
		}
	}
	if c.VerifierFetchTimeout <= 0 {
		c.VerifierFetchTimeout = defaultVerifierFetchTimeout
	}
	if c.VerifierFetchAttempts <= 0 {
		c.VerifierFetchAttempts = defaultVerifierFetchAttempts
	}
	if c.VerifierBackoffBase <= 0 {
		c.VerifierBackoffBase = defaultVerifierBackoffBase
	}
	if c.AuditQueryTimeout <= 0 {
		c.AuditQueryTimeout = defaultAuditQueryTimeout
	}
	if c.DispatchOpTimeout <= 0 {
		c.DispatchOpTimeout = defaultDispatchOpTimeout
	}
	return c
}

// VerifierFetcher abstracts the verifier→healer transport. Real
// implementation is grpc-based (peer_client.go); tests inject in-memory
// fakes that don't need a listening server.
type VerifierFetcher interface {
	// FetchReconstructed retrieves the reconstructed file bytes from the
	// healer assigned to healOpID. Implementations are responsible for
	// dialing the healer's grpc endpoint (resolved from the supernode
	// registry) and authenticating as verifierAccount.
	FetchReconstructed(ctx context.Context, healOpID uint64, healerAccount, verifierAccount string) ([]byte, error)
}

// Service is the single LEP-6 heal-op dispatcher. One instance per
// supernode binary.
type Service struct {
	cfg      Config
	identity string

	lumera         lumera.Client
	store          queries.LocalStoreInterface
	cascadeFactory cascadeService.CascadeServiceFactory
	fetcher        VerifierFetcher

	// In-flight dedup. Key: opRoleKey(healOpID, role). Value: struct{}.
	inFlight sync.Map

	// Per-role concurrency caps.
	semReconstruct *semaphore.Weighted
	semVerify      *semaphore.Weighted
	semPublish     *semaphore.Weighted
}

const (
	roleHealer    = "healer"
	roleVerifier  = "verifier"
	rolePublisher = "publisher"
)

func opRoleKey(healOpID uint64, role string) string {
	return fmt.Sprintf("%d/%s", healOpID, role)
}

// New constructs a Service. fetcher may be nil if Config.Enabled is false
// (constructor still validates required deps so misconfig is caught early).
func New(
	identity string,
	cfg Config,
	lumeraClient lumera.Client,
	store queries.LocalStoreInterface,
	cascadeFactory cascadeService.CascadeServiceFactory,
	fetcher VerifierFetcher,
) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.Audit() == nil || lumeraClient.AuditMsg() == nil {
		return nil, fmt.Errorf("lumera client missing required audit modules")
	}
	if store == nil {
		return nil, fmt.Errorf("local store is nil")
	}
	if cascadeFactory == nil {
		return nil, fmt.Errorf("cascade service factory is nil")
	}
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.StagingRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create staging root %q: %w", cfg.StagingRoot, err)
	}
	return &Service{
		cfg:            cfg,
		identity:       identity,
		lumera:         lumeraClient,
		store:          store,
		cascadeFactory: cascadeFactory,
		fetcher:        fetcher,
		semReconstruct: semaphore.NewWeighted(int64(cfg.MaxConcurrentReconstructs)),
		semVerify:      semaphore.NewWeighted(int64(cfg.MaxConcurrentVerifications)),
		semPublish:     semaphore.NewWeighted(int64(cfg.MaxConcurrentPublishes)),
	}, nil
}

// Run blocks until ctx is cancelled, ticking every cfg.PollInterval.
// Tick steps (single mechanism per LEP-6 plan §C.4 finalizer Opt-2b decision):
//
//  1. Mode gate: query audit params; if UNSPECIFIED, skip everything.
//  2. Healer dispatch: GetHealOpsByStatus(SCHEDULED), filter by
//     HealerSupernodeAccount==identity, run reconstructHealOp() bounded by
//     semReconstruct.
//  3. Verifier dispatch: GetHealOpsByStatus(HEALER_REPORTED), filter by
//     identity ∈ VerifierSupernodeAccounts, run verifyHealOp() bounded by
//     semVerify.
//  4. Finalizer (Opt 2b per-op poll): for each row in heal_claims_submitted,
//     GetHealOp(opID) and act on Status (VERIFIED → publish, FAILED/EXPIRED
//     → cleanup).
//
// Final-state ops are excluded by status filter, so a misordered tick is
// idempotent (sync.Map dedup + sqlite dedup catch any race).
func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		logtrace.Info(ctx, "self_healing(LEP-6): disabled in config; not starting", logtrace.Fields{})
		return nil
	}
	logtrace.Info(ctx, "self_healing(LEP-6): start", logtrace.Fields{
		"identity":                     s.identity,
		"poll_interval":                s.cfg.PollInterval.String(),
		"max_concurrent_reconstructs":  s.cfg.MaxConcurrentReconstructs,
		"max_concurrent_verifications": s.cfg.MaxConcurrentVerifications,
		"max_concurrent_publishes":     s.cfg.MaxConcurrentPublishes,
		"staging_root":                 s.cfg.StagingRoot,
	})
	t := time.NewTicker(s.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.tick(ctx); err != nil {
				logtrace.Warn(ctx, "self_healing(LEP-6): tick error", logtrace.Fields{logtrace.FieldError: err.Error()})
			}
		}
	}
}

// tick performs one dispatch cycle. Exposed for tests.
func (s *Service) tick(ctx context.Context) error {
	skip, err := s.modeGate(ctx)
	if err != nil {
		return fmt.Errorf("mode gate: %w", err)
	}
	if !skip {
		// Mode-gated phases — chain creates no fresh heal-ops in
		// UNSPECIFIED, so dispatching healer/verifier work would be
		// a no-op anyway.
		if err := s.dispatchHealerOps(ctx); err != nil {
			logtrace.Warn(ctx, "self_healing(LEP-6): dispatch healer ops", logtrace.Fields{logtrace.FieldError: err.Error()})
		}
		if err := s.dispatchVerifierOps(ctx); err != nil {
			logtrace.Warn(ctx, "self_healing(LEP-6): dispatch verifier ops", logtrace.Fields{logtrace.FieldError: err.Error()})
		}
	}
	// M7 fix: finalizer runs ALWAYS, even when modeGate says skip.
	// Pre-existing pending claim rows + staging dirs from a prior run
	// (or from a governance rollback to UNSPECIFIED while in-flight ops
	// were live) must still be drained — otherwise staging dirs leak
	// forever after rollback. Finalizer is read-mostly w.r.t. chain
	// (GetHealOp queries) so it doesn't generate work in UNSPECIFIED.
	if err := s.dispatchFinalizer(ctx); err != nil {
		logtrace.Warn(ctx, "self_healing(LEP-6): dispatch finalizer", logtrace.Fields{logtrace.FieldError: err.Error()})
	}
	return nil
}

// modeGate returns (skip=true) when the chain enforcement mode is
// UNSPECIFIED. Heal-ops only exist in SHADOW/SOFT/FULL.
func (s *Service) modeGate(ctx context.Context) (bool, error) {
	queryCtx, cancel := s.auditQueryContext(ctx)
	defer cancel()
	resp, err := s.lumera.Audit().GetParams(queryCtx)
	if err != nil {
		return false, err
	}
	mode := resp.Params.StorageTruthEnforcementMode
	if mode == audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED {
		return true, nil
	}
	return false, nil
}

// dispatchHealerOps: pulls SCHEDULED ops where I'm the assigned healer and
// kicks off reconstruction via the healer goroutine pool.
func (s *Service) dispatchHealerOps(ctx context.Context) error {
	ops, err := s.listOps(ctx, audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED)
	if err != nil {
		return err
	}
	for i := range ops {
		op := ops[i]
		if op.HealerSupernodeAccount != s.identity {
			continue
		}
		if isFinalStatus(op.Status) {
			continue
		}
		key := opRoleKey(op.HealOpId, roleHealer)
		if _, loaded := s.inFlight.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		// Restart-time dedup: if a row already exists in heal_claims_submitted
		// the chain has accepted the claim — switch to publisher / leave to
		// finalizer.
		has, err := s.store.HasHealClaim(ctx, op.HealOpId)
		if err != nil {
			s.inFlight.Delete(key)
			logtrace.Warn(ctx, "self_healing(LEP-6): HasHealClaim", logtrace.Fields{logtrace.FieldError: err.Error(), "heal_op_id": op.HealOpId})
			continue
		}
		if has {
			s.inFlight.Delete(key)
			continue
		}
		// C5 fix: if a pending row exists from an interrupted previous
		// tick (crash between RecordPendingHealClaim and chain ack),
		// HasHealClaim above returns false (submitted-only). Run the
		// resume-reconcile path BEFORE attempting fresh reconstruct so
		// we don't waste RaptorQ + bandwidth re-rebuilding bytes the
		// chain may have already accepted.
		hasPending, perr := s.store.HasPendingHealClaim(ctx, op.HealOpId)
		if perr != nil {
			s.inFlight.Delete(key)
			logtrace.Warn(ctx, "self_healing(LEP-6): HasPendingHealClaim", logtrace.Fields{logtrace.FieldError: perr.Error(), "heal_op_id": op.HealOpId})
			continue
		}
		if hasPending {
			go func(op audittypes.HealOp, key string) {
				defer s.inFlight.Delete(key)
				if err := s.resumePendingHealClaim(ctx, op); err != nil {
					logtrace.Warn(ctx, "self_healing(LEP-6): resumePendingHealClaim", logtrace.Fields{
						logtrace.FieldError: err.Error(),
						"heal_op_id":        op.HealOpId,
					})
				}
			}(op, key)
			continue
		}
		go func(op audittypes.HealOp, key string) {
			defer s.inFlight.Delete(key)
			// M2 fix: bound the per-op goroutine so a wedged
			// reconstruct or hung RaptorQ releases its semaphore slot.
			opCtx, cancel := s.dispatchOpContext(ctx)
			defer cancel()
			if err := s.reconstructAndClaim(opCtx, op); err != nil {
				logtrace.Warn(ctx, "self_healing(LEP-6): reconstructAndClaim", logtrace.Fields{
					logtrace.FieldError: err.Error(),
					"heal_op_id":        op.HealOpId,
					"ticket_id":         op.TicketId,
				})
			}
		}(op, key)
	}
	return nil
}

// dispatchVerifierOps: pulls HEALER_REPORTED ops where I'm an assigned
// verifier and kicks off verification.
func (s *Service) dispatchVerifierOps(ctx context.Context) error {
	ops, err := s.listOps(ctx, audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED)
	if err != nil {
		return err
	}
	if len(ops) > 0 {
		logtrace.Info(ctx, "self_healing(LEP-6): verifier status scan", logtrace.Fields{
			"identity": s.identity,
			"ops":      len(ops),
		})
	}
	for i := range ops {
		op := ops[i]
		if !accountInList(s.identity, op.VerifierSupernodeAccounts) {
			logtrace.Debug(ctx, "self_healing(LEP-6): verifier op not assigned locally", logtrace.Fields{
				"identity":   s.identity,
				"heal_op_id": op.HealOpId,
			})
			continue
		}
		if isFinalStatus(op.Status) {
			continue
		}
		key := opRoleKey(op.HealOpId, roleVerifier)
		if _, loaded := s.inFlight.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		has, err := s.store.HasHealVerification(ctx, op.HealOpId, s.identity)
		if err != nil {
			s.inFlight.Delete(key)
			logtrace.Warn(ctx, "self_healing(LEP-6): HasHealVerification", logtrace.Fields{logtrace.FieldError: err.Error(), "heal_op_id": op.HealOpId})
			continue
		}
		if has {
			s.inFlight.Delete(key)
			continue
		}
		// C5 fix: detect a stranded `pending` verifier row from a crash
		// mid-submit. The next tick must retry; we cannot just leave the
		// row stuck or quorum may fail.
		hasPending, perr := s.store.HasPendingHealVerification(ctx, op.HealOpId, s.identity)
		if perr != nil {
			s.inFlight.Delete(key)
			logtrace.Warn(ctx, "self_healing(LEP-6): HasPendingHealVerification", logtrace.Fields{logtrace.FieldError: perr.Error(), "heal_op_id": op.HealOpId})
			continue
		}
		if hasPending {
			// Best-effort: drop the stale pending row so the next
			// re-attempt below proceeds normally. Chain-side dedup
			// (ErrHealVerificationExists) absorbs any duplicate that
			// did get through.
			if delErr := s.store.DeletePendingHealVerification(ctx, op.HealOpId, s.identity); delErr != nil {
				logtrace.Warn(ctx, "self_healing(LEP-6): drop stale pending verification", logtrace.Fields{logtrace.FieldError: delErr.Error(), "heal_op_id": op.HealOpId})
			}
		}
		go func(op audittypes.HealOp, key string) {
			defer s.inFlight.Delete(key)
			// M2 fix: bound per-op verifier goroutine.
			opCtx, cancel := s.dispatchOpContext(ctx)
			defer cancel()
			logtrace.Info(opCtx, "self_healing(LEP-6): verifier dispatch start", logtrace.Fields{
				"identity":   s.identity,
				"heal_op_id": op.HealOpId,
				"ticket_id":  op.TicketId,
			})
			if err := s.verifyAndSubmit(opCtx, op); err != nil {
				logtrace.Warn(ctx, "self_healing(LEP-6): verifyAndSubmit", logtrace.Fields{
					logtrace.FieldError: err.Error(),
					"heal_op_id":        op.HealOpId,
				})
			}
			logtrace.Info(ctx, "self_healing(LEP-6): verifier dispatch end", logtrace.Fields{
				"identity":   s.identity,
				"heal_op_id": op.HealOpId,
			})
		}(op, key)
	}
	return nil
}

// dispatchFinalizer: for each persisted heal_claims_submitted row, look up
// the on-chain status and either publish (VERIFIED) or cleanup
// (FAILED/EXPIRED). SCHEDULED / HEALER_REPORTED / IN_PROGRESS are no-ops.
func (s *Service) dispatchFinalizer(ctx context.Context) error {
	claims, err := s.store.ListHealClaims(ctx)
	if err != nil {
		return err
	}
	lep6metrics.SetSelfHealingPendingClaims(len(claims))
	lep6metrics.SetSelfHealingStagingBytes(totalStagingBytes(claims))
	for _, claim := range claims {
		key := opRoleKey(claim.HealOpID, rolePublisher)
		if _, loaded := s.inFlight.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		go func(claim queries.HealClaimRecord, key string) {
			defer s.inFlight.Delete(key)
			// M2 fix: bound per-op finalizer goroutine.
			opCtx, cancel := s.dispatchOpContext(ctx)
			defer cancel()
			if err := s.finalizeClaim(opCtx, claim); err != nil {
				logtrace.Warn(ctx, "self_healing(LEP-6): finalizeClaim", logtrace.Fields{
					logtrace.FieldError: err.Error(),
					"heal_op_id":        claim.HealOpID,
				})
			}
		}(claim, key)
	}
	return nil
}

// listOps wraps the paginated audit query. Walks pagination.NextKey until
// exhausted (H2 fix — previous nil-pagination call dropped any heal-op past
// the SDK default page size of 100, silently). A hard ceiling of
// maxHealOpListPages prevents runaway loops if a buggy chain build never
// returns an empty NextKey.
func (s *Service) listOps(ctx context.Context, status audittypes.HealOpStatus) ([]audittypes.HealOp, error) {
	const maxHealOpListPages = 100
	const pageLimit uint64 = 100
	var (
		all     []audittypes.HealOp
		pageKey []byte
	)
	for page := 0; page < maxHealOpListPages; page++ {
		queryCtx, cancel := s.auditQueryContext(ctx)
		resp, err := s.lumera.Audit().GetHealOpsByStatus(queryCtx, status, &query.PageRequest{Key: pageKey, Limit: pageLimit})
		cancel()
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return all, nil
		}
		all = append(all, resp.HealOps...)
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			return all, nil
		}
		pageKey = resp.Pagination.NextKey
	}
	logtrace.Warn(ctx, "self_healing(LEP-6): listOps hit max-pages ceiling; results truncated", logtrace.Fields{
		"status":    status.String(),
		"max_pages": maxHealOpListPages,
		"collected": len(all),
	})
	return all, nil
}

func (s *Service) auditQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.cfg.AuditQueryTimeout
	if timeout <= 0 {
		timeout = defaultAuditQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// dispatchOpContext derives a per-op-goroutine ctx with the configured
// hard ceiling so a wedged reconstruct/fetch/publish releases its
// semaphore slot + inFlight key. Wave 2 / M2 fix.
func (s *Service) dispatchOpContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.cfg.DispatchOpTimeout
	if timeout <= 0 {
		timeout = defaultDispatchOpTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func totalStagingBytes(claims []queries.HealClaimRecord) int64 {
	var total int64
	for _, claim := range claims {
		if strings.TrimSpace(claim.StagingDir) == "" {
			continue
		}
		_ = filepath.WalkDir(claim.StagingDir, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			if info, statErr := d.Info(); statErr == nil {
				total += info.Size()
			}
			return nil
		})
	}
	return total
}

func accountInList(account string, list []string) bool {
	for _, a := range list {
		if a == account {
			return true
		}
	}
	return false
}

func isFinalStatus(s audittypes.HealOpStatus) bool {
	switch s {
	case audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED:
		return true
	}
	return false
}
