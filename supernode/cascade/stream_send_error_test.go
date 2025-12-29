package cascade

import (
	"context"
	"errors"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

type stubLumeraClient struct {
	action *actiontypes.Action
}

func (s *stubLumeraClient) GetAction(_ context.Context, _ string) (*actiontypes.QueryGetActionResponse, error) {
	return &actiontypes.QueryGetActionResponse{Action: s.action}, nil
}

func (s *stubLumeraClient) GetTopSupernodes(context.Context, uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	panic("unexpected call")
}

func (s *stubLumeraClient) Verify(context.Context, string, []byte, []byte) error {
	panic("unexpected call")
}

func (s *stubLumeraClient) GetActionFee(context.Context, string) (*actiontypes.QueryGetActionFeeResponse, error) {
	panic("unexpected call")
}

func (s *stubLumeraClient) SimulateFinalizeAction(context.Context, string, []string) (*sdktx.SimulateResponse, error) {
	panic("unexpected call")
}

func (s *stubLumeraClient) FinalizeAction(context.Context, string, []string) (*sdktx.BroadcastTxResponse, error) {
	panic("unexpected call")
}

func TestRegister_AbortsOnEventSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	sendCalls := 0

	service := &CascadeService{
		LumeraClient: &stubLumeraClient{action: &actiontypes.Action{ActionID: "action123"}},
	}
	task := NewCascadeRegistrationTask(service)

	err := task.Register(context.Background(), &RegisterRequest{TaskID: "task123", ActionID: "action123"}, func(*RegisterResponse) error {
		sendCalls++
		return sendErr
	})

	if !errors.Is(err, sendErr) {
		t.Fatalf("expected send error; got %v", err)
	}
	if sendCalls != 1 {
		t.Fatalf("expected 1 send call, got %d", sendCalls)
	}
}

func TestDownload_AbortsOnEventSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	sendCalls := 0

	service := &CascadeService{
		LumeraClient: &stubLumeraClient{action: &actiontypes.Action{ActionID: "action123", State: actiontypes.ActionStateDone}},
	}
	task := NewCascadeRegistrationTask(service)

	err := task.Download(context.Background(), &DownloadRequest{ActionID: "action123"}, func(*DownloadResponse) error {
		sendCalls++
		return sendErr
	})

	if !errors.Is(err, sendErr) {
		t.Fatalf("expected send error; got %v", err)
	}
	if sendCalls != 1 {
		t.Fatalf("expected 1 send call, got %d", sendCalls)
	}
}
