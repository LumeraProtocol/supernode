package self_healing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	lumeraclient "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	actionmodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	lumerasn "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"github.com/golang/protobuf/proto"
	"go.uber.org/mock/gomock"
)

func TestProcessEventsRecipientDownTerminal(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 2,
		MaxEventAttempts:  1,
		MaxWindowAge:      time.Hour,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-recipient-down", challengeEventPayload{
		WindowID:  time.Now().UTC().Unix(),
		FileKey:   "file-1",
		Recipient: "recipient-1",
		Observers: []string{"observer-1", "observer-2"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		return nil, errors.New("dial failed")
	}

	svc.processEvents(context.Background())

	ev, err := store.GetSelfHealingChallengeEvent("ch-recipient-down")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "terminal"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if !ev.IsProcessed {
		t.Fatalf("expected event marked processed")
	}
	if ev.AttemptCount != 1 {
		t.Fatalf("attempt_count=%d want=1", ev.AttemptCount)
	}
}

func TestProcessEventsQuorumFailureTerminalAtMaxAttempts(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 2,
		MaxEventAttempts:  1,
		MaxWindowAge:      time.Hour,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-quorum-fail", challengeEventPayload{
		WindowID:  time.Now().UTC().Unix(),
		FileKey:   "file-2",
		Recipient: "recipient-1",
		Observers: []string{"observer-1", "observer-2"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId:            req.ChallengeId,
			RecipientId:            req.RecipientId,
			Accepted:               true,
			ReconstructionRequired: true,
			ReconstructedHashHex:   "abcd",
		}, nil
	}
	svc.verifySelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySelfHealingRequest, timeout time.Duration) (*supernode.VerifySelfHealingResponse, error) {
		if remoteIdentity == "observer-1" {
			return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, ObserverId: remoteIdentity, Ok: true}, nil
		}
		return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, ObserverId: remoteIdentity, Ok: false, Error: "mismatch"}, nil
	}

	svc.processEvents(context.Background())

	ev, err := store.GetSelfHealingChallengeEvent("ch-quorum-fail")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "terminal"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if !ev.IsProcessed {
		t.Fatalf("expected event marked processed")
	}
}

func TestProcessEventsCommitAfterObserverQuorum(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 2,
		MaxEventAttempts:  3,
		MaxWindowAge:      time.Hour,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-commit-after-quorum", challengeEventPayload{
		EpochID:   21,
		WindowID:  time.Now().UTC().Unix(),
		ActionID:  "action-commit-1",
		FileKey:   "file-commit-1",
		Recipient: "recipient-1",
		Observers: []string{"observer-1", "observer-2"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	commitCalled := false
	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId:            req.ChallengeId,
			RecipientId:            req.RecipientId,
			Accepted:               true,
			ReconstructionRequired: true,
			ReconstructedHashHex:   "abcd",
		}, nil
	}
	svc.verifySelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySelfHealingRequest, timeout time.Duration) (*supernode.VerifySelfHealingResponse, error) {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			ObserverId:  remoteIdentity,
			Ok:          true,
		}, nil
	}
	svc.commitSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.CommitSelfHealingRequest, timeout time.Duration) (*supernode.CommitSelfHealingResponse, error) {
		commitCalled = true
		if req.ActionId != "action-commit-1" {
			t.Fatalf("unexpected action_id in commit request: %s", req.ActionId)
		}
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			RecipientId: remoteIdentity,
			Stored:      true,
		}, nil
	}

	svc.processEvents(context.Background())

	if !commitCalled {
		t.Fatalf("expected commit RPC to be called after observer quorum")
	}
	ev, err := store.GetSelfHealingChallengeEvent("ch-commit-after-quorum")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "completed"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if !ev.IsProcessed {
		t.Fatalf("expected event marked processed")
	}
}

func TestProcessEventsStaleWindowTerminalWithoutRPC(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 2,
		MaxEventAttempts:  3,
		MaxWindowAge:      5 * time.Minute,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-stale-window", challengeEventPayload{
		WindowID:  time.Now().UTC().Add(-2 * time.Hour).Unix(),
		FileKey:   "file-3",
		Recipient: "recipient-1",
		Observers: []string{"observer-1", "observer-2"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	rpcCalled := false
	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		rpcCalled = true
		return nil, errors.New("unexpected call")
	}

	svc.processEvents(context.Background())

	if rpcCalled {
		t.Fatalf("request RPC should not be called for stale window")
	}
	ev, err := store.GetSelfHealingChallengeEvent("ch-stale-window")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "terminal"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
}

func TestProcessEventsDuplicateReplayProcessedOnce(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 1,
		MaxEventAttempts:  3,
		MaxWindowAge:      time.Hour,
	})
	defer cleanup()

	payload := challengeEventPayload{
		WindowID:  time.Now().UTC().Unix(),
		FileKey:   "file-4",
		Recipient: "recipient-1",
		Observers: []string{"observer-1"},
	}
	if err := insertEvent(t, store, "ch-dup", payload); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	// Duplicate delivery should be ignored by deterministic unique key.
	if err := insertEvent(t, store, "ch-dup", payload); err != nil {
		t.Fatalf("insert duplicate event: %v", err)
	}

	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId:            req.ChallengeId,
			RecipientId:            req.RecipientId,
			Accepted:               true,
			ReconstructionRequired: false,
		}, nil
	}

	svc.processEvents(context.Background())
	svc.processEvents(context.Background())

	ev, err := store.GetSelfHealingChallengeEvent("ch-dup")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "completed"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if ev.AttemptCount != 1 {
		t.Fatalf("attempt_count=%d want=1", ev.AttemptCount)
	}
}

func TestProcessEventsRetryThenComplete(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold: 1,
		MaxEventAttempts:  3,
		EventRetryBase:    10 * time.Millisecond,
		EventRetryMax:     20 * time.Millisecond,
		MaxWindowAge:      time.Hour,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-retry-then-complete", challengeEventPayload{
		WindowID:  time.Now().UTC().Unix(),
		FileKey:   "file-5",
		Recipient: "recipient-1",
		Observers: []string{"observer-1"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	calls := 0
	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary network failure")
		}
		return &supernode.RequestSelfHealingResponse{
			ChallengeId:            req.ChallengeId,
			RecipientId:            req.RecipientId,
			Accepted:               true,
			ReconstructionRequired: false,
		}, nil
	}

	svc.processEvents(context.Background())

	ev, err := store.GetSelfHealingChallengeEvent("ch-retry-then-complete")
	if err != nil {
		t.Fatalf("get event after first attempt: %v", err)
	}
	if got, want := ev.Status, "retry"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if ev.AttemptCount != 1 {
		t.Fatalf("attempt_count=%d want=1", ev.AttemptCount)
	}

	time.Sleep(20 * time.Millisecond)
	svc.processEvents(context.Background())

	ev, err = store.GetSelfHealingChallengeEvent("ch-retry-then-complete")
	if err != nil {
		t.Fatalf("get event after second attempt: %v", err)
	}
	if got, want := ev.Status, "completed"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if ev.AttemptCount != 2 {
		t.Fatalf("attempt_count=%d want=2", ev.AttemptCount)
	}
}

func TestProcessEventsReclaimsExpiredLease(t *testing.T) {
	svc, store, cleanup := newServiceForEventTests(t, Config{
		ObserverThreshold:  1,
		MaxEventAttempts:   3,
		EventLeaseDuration: 20 * time.Millisecond,
		MaxWindowAge:       time.Hour,
	})
	defer cleanup()

	if err := insertEvent(t, store, "ch-expired-lease", challengeEventPayload{
		WindowID:  time.Now().UTC().Unix(),
		FileKey:   "file-6",
		Recipient: "recipient-1",
		Observers: []string{"observer-1"},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	claimed, err := store.ClaimPendingSelfHealingChallengeEvents(context.Background(), "other-node", 10*time.Millisecond, 1)
	if err != nil {
		t.Fatalf("claim event as other-node: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed event, got %d", len(claimed))
	}

	time.Sleep(20 * time.Millisecond)

	svc.requestSelfHealingFn = func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId:            req.ChallengeId,
			RecipientId:            req.RecipientId,
			Accepted:               true,
			ReconstructionRequired: false,
		}, nil
	}

	svc.processEvents(context.Background())

	ev, err := store.GetSelfHealingChallengeEvent("ch-expired-lease")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got, want := ev.Status, "completed"; got != want {
		t.Fatalf("status=%s want=%s", got, want)
	}
	if ev.AttemptCount != 2 {
		t.Fatalf("attempt_count=%d want=2", ev.AttemptCount)
	}
}

func TestCountWeightedClosedVotes(t *testing.T) {
	reports := []audittypes.StorageChallengeReport{
		{ReporterSupernodeAccount: "rep-1", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_CLOSED}},
		{ReporterSupernodeAccount: "rep-2", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_OPEN}},
		{ReporterSupernodeAccount: "rep-3", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_CLOSED}},
	}
	total, closed := countWeightedClosedVotes(reports, 2)
	if total != 3 || closed != 2 {
		t.Fatalf("got total=%d closed=%d, want total=3 closed=2", total, closed)
	}
}

func TestDeriveWindowChallengeIDIgnoresRecipientSelection(t *testing.T) {
	first := deriveWindowChallengeID(1710000000, "file-key-1")
	second := deriveWindowChallengeID(1710000000, "file-key-1")
	if first != second {
		t.Fatalf("challenge id should be stable for same window/file; got %q vs %q", first, second)
	}
}

func TestAuditWeightedWatchlistUsesQuorumAndThreshold(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	lumeraClient := lumeraclient.NewMockClient(ctrl)
	auditModule := &stubAuditModule{
		paramsResp: &audittypes.QueryParamsResponse{
			Params: audittypes.Params{
				PeerQuorumReports:                3,
				PeerPortPostponeThresholdPercent: 66,
				RequiredOpenPorts:                []uint32{4444, 4445},
			},
		},
		currentEpochResp: &audittypes.QueryCurrentEpochResponse{EpochId: 55},
		reportsByTarget: map[string][]audittypes.StorageChallengeReport{
			"target-a": {
				{ReporterSupernodeAccount: "rep-1", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_CLOSED, audittypes.PortState_PORT_STATE_OPEN}},
				{ReporterSupernodeAccount: "rep-2", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_CLOSED, audittypes.PortState_PORT_STATE_OPEN}},
				{ReporterSupernodeAccount: "rep-3", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_OPEN}},
			},
			"target-b": {
				{ReporterSupernodeAccount: "rep-1", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_OPEN}},
				{ReporterSupernodeAccount: "rep-2", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_CLOSED, audittypes.PortState_PORT_STATE_OPEN}},
				{ReporterSupernodeAccount: "rep-3", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_OPEN, audittypes.PortState_PORT_STATE_OPEN}},
			},
			"target-c": {
				{ReporterSupernodeAccount: "rep-1", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_CLOSED, audittypes.PortState_PORT_STATE_OPEN}},
				{ReporterSupernodeAccount: "rep-2", PortStates: []audittypes.PortState{audittypes.PortState_PORT_STATE_CLOSED, audittypes.PortState_PORT_STATE_OPEN}},
			},
		},
	}
	lumeraClient.EXPECT().Audit().AnyTimes().Return(auditModule)

	svc := &Service{
		cfg: Config{
			WatchlistThreshold: 2,
		},
		identity: "self-node",
		lumera:   lumeraClient,
	}

	watch, err := svc.auditWeightedWatchlist(context.Background(), []string{"target-b", "target-a", "target-c", "self-node"})
	if err != nil {
		t.Fatalf("auditWeightedWatchlist error: %v", err)
	}
	if len(watch) != 1 || watch[0] != "target-a" {
		t.Fatalf("unexpected watchlist: %v", watch)
	}
}

func TestListCascadeHealingTargetsUsesOnChainActions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	lumeraClient := lumeraclient.NewMockClient(ctrl)
	actionMod := actionmodule.NewMockModule(ctrl)
	lumeraClient.EXPECT().Action().AnyTimes().Return(actionMod)

	metaA, err := proto.Marshal(&actiontypes.CascadeMetadata{RqIdsIds: []string{"rq-z", "rq-a"}})
	if err != nil {
		t.Fatalf("marshal metaA: %v", err)
	}
	metaB, err := proto.Marshal(&actiontypes.CascadeMetadata{RqIdsIds: []string{"rq-m"}})
	if err != nil {
		t.Fatalf("marshal metaB: %v", err)
	}

	actionMod.EXPECT().ListActions(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, req *actiontypes.QueryListActionsRequest) (*actiontypes.QueryListActionsResponse, error) {
			switch req.ActionState {
			case actiontypes.ActionStateDone:
				return &actiontypes.QueryListActionsResponse{
					Actions: []*actiontypes.Action{
						{ActionID: "action-2", Metadata: metaB, State: actiontypes.ActionStateDone},
						{ActionID: "action-1", Metadata: metaA, State: actiontypes.ActionStateDone},
					},
				}, nil
			case actiontypes.ActionStateApproved:
				return &actiontypes.QueryListActionsResponse{
					Actions: []*actiontypes.Action{
						// Duplicate action id across states should be deduped.
						{ActionID: "action-1", Metadata: metaA, State: actiontypes.ActionStateApproved},
					},
				}, nil
			default:
				return &actiontypes.QueryListActionsResponse{}, nil
			}
		},
	)

	svc := &Service{lumera: lumeraClient}
	targets, err := svc.listCascadeHealingTargets(context.Background())
	if err != nil {
		t.Fatalf("listCascadeHealingTargets error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets length=%d want=2", len(targets))
	}
	if targets[0].ActionID != "action-1" || targets[0].FileKey != "rq-a" {
		t.Fatalf("unexpected first target: %+v", targets[0])
	}
	if targets[1].ActionID != "action-2" || targets[1].FileKey != "rq-m" {
		t.Fatalf("unexpected second target: %+v", targets[1])
	}
}

func newServiceForEventTests(t *testing.T, cfg Config) (*Service, queries.LocalStoreInterface, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	store, err := queries.OpenHistoryDB()
	if err != nil {
		t.Fatalf("open history db: %v", err)
	}

	ctrl := gomock.NewController(t)
	lumeraClient := lumeraclient.NewMockClient(ctrl)
	supernodeModule := lumerasn.NewMockModule(ctrl)

	lumeraClient.EXPECT().SuperNode().AnyTimes().Return(supernodeModule)
	supernodeModule.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, address string) (*lumerasn.SuperNodeInfo, error) {
			return &lumerasn.SuperNodeInfo{
				SupernodeAccount: address,
				LatestAddress:    "127.0.0.1:4444",
			}, nil
		},
	)

	if cfg.EventLeaseDuration <= 0 {
		cfg.EventLeaseDuration = 30 * time.Second
	}
	if cfg.EventRetryBase <= 0 {
		cfg.EventRetryBase = 100 * time.Millisecond
	}
	if cfg.EventRetryMax <= 0 {
		cfg.EventRetryMax = time.Second
	}
	if cfg.MaxEventsPerTick <= 0 {
		cfg.MaxEventsPerTick = 16
	}

	svc := &Service{
		cfg:      cfg,
		identity: "challenger-1",
		lumera:   lumeraClient,
		store:    store,
	}

	cleanup := func() {
		store.CloseHistoryDB(context.Background())
		ctrl.Finish()
	}
	return svc, store, cleanup
}

func insertEvent(t *testing.T, store queries.LocalStoreInterface, challengeID string, payload challengeEventPayload) error {
	t.Helper()
	bz, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	return store.BatchInsertSelfHealingChallengeEvents(context.Background(), []types.SelfHealingChallengeEvent{
		{
			TriggerID:   "window:test",
			TicketID:    payload.FileKey,
			ChallengeID: challengeID,
			Data:        bz,
			SenderID:    "challenger-1",
			ExecMetric: types.SelfHealingExecutionMetric{
				TriggerID:   "window:test",
				ChallengeID: challengeID,
				MessageType: int(types.SelfHealingChallengeMessage),
				Data:        []byte(`{"event":"challenge"}`),
				SenderID:    "challenger-1",
				CreatedAt:   time.Unix(now, 0).UTC(),
				UpdatedAt:   time.Unix(now, 0).UTC(),
			},
		},
	})
}

type stubAuditModule struct {
	paramsResp       *audittypes.QueryParamsResponse
	currentEpochResp *audittypes.QueryCurrentEpochResponse
	reportsByTarget  map[string][]audittypes.StorageChallengeReport
}

func (s *stubAuditModule) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	if s.paramsResp == nil {
		return nil, errors.New("params unavailable")
	}
	return s.paramsResp, nil
}

func (s *stubAuditModule) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	if s.currentEpochResp == nil {
		return nil, errors.New("epoch unavailable")
	}
	return s.currentEpochResp, nil
}

func (s *stubAuditModule) GetEpochAnchor(ctx context.Context, epochID uint64) (*audittypes.QueryEpochAnchorResponse, error) {
	return &audittypes.QueryEpochAnchorResponse{}, nil
}

func (s *stubAuditModule) GetCurrentEpochAnchor(ctx context.Context) (*audittypes.QueryCurrentEpochAnchorResponse, error) {
	return &audittypes.QueryCurrentEpochAnchorResponse{}, nil
}

func (s *stubAuditModule) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryAssignedTargetsResponse, error) {
	return &audittypes.QueryAssignedTargetsResponse{}, nil
}

func (s *stubAuditModule) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	return &audittypes.QueryEpochReportResponse{}, nil
}

func (s *stubAuditModule) GetStorageChallengeReports(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryStorageChallengeReportsResponse, error) {
	reports := s.reportsByTarget[supernodeAccount]
	return &audittypes.QueryStorageChallengeReportsResponse{Reports: reports}, nil
}
