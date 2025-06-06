// Code generated by MockGen. DO NOT EDIT.
// Source: interface.go
//
// Generated by this command:
//
//	mockgen -destination=tx_mock.go -package=tx -source=interface.go
//

// Package tx is a generated GoMock package.
package tx

import (
	context "context"
	reflect "reflect"

	types "github.com/cosmos/cosmos-sdk/types"
	tx "github.com/cosmos/cosmos-sdk/types/tx"
	types0 "github.com/cosmos/cosmos-sdk/x/auth/types"
	gomock "go.uber.org/mock/gomock"
)

// MockModule is a mock of Module interface.
type MockModule struct {
	ctrl     *gomock.Controller
	recorder *MockModuleMockRecorder
	isgomock struct{}
}

// MockModuleMockRecorder is the mock recorder for MockModule.
type MockModuleMockRecorder struct {
	mock *MockModule
}

// NewMockModule creates a new mock instance.
func NewMockModule(ctrl *gomock.Controller) *MockModule {
	mock := &MockModule{ctrl: ctrl}
	mock.recorder = &MockModuleMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockModule) EXPECT() *MockModuleMockRecorder {
	return m.recorder
}

// BroadcastTransaction mocks base method.
func (m *MockModule) BroadcastTransaction(ctx context.Context, txBytes []byte) (*tx.BroadcastTxResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "BroadcastTransaction", ctx, txBytes)
	ret0, _ := ret[0].(*tx.BroadcastTxResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// BroadcastTransaction indicates an expected call of BroadcastTransaction.
func (mr *MockModuleMockRecorder) BroadcastTransaction(ctx, txBytes any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "BroadcastTransaction", reflect.TypeOf((*MockModule)(nil).BroadcastTransaction), ctx, txBytes)
}

// BuildAndSignTransaction mocks base method.
func (m *MockModule) BuildAndSignTransaction(ctx context.Context, msgs []types.Msg, accountInfo *types0.BaseAccount, gasLimit uint64, fee string, config *TxConfig) ([]byte, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "BuildAndSignTransaction", ctx, msgs, accountInfo, gasLimit, fee, config)
	ret0, _ := ret[0].([]byte)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// BuildAndSignTransaction indicates an expected call of BuildAndSignTransaction.
func (mr *MockModuleMockRecorder) BuildAndSignTransaction(ctx, msgs, accountInfo, gasLimit, fee, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "BuildAndSignTransaction", reflect.TypeOf((*MockModule)(nil).BuildAndSignTransaction), ctx, msgs, accountInfo, gasLimit, fee, config)
}

// CalculateFee mocks base method.
func (m *MockModule) CalculateFee(gasAmount uint64, config *TxConfig) string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CalculateFee", gasAmount, config)
	ret0, _ := ret[0].(string)
	return ret0
}

// CalculateFee indicates an expected call of CalculateFee.
func (mr *MockModuleMockRecorder) CalculateFee(gasAmount, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CalculateFee", reflect.TypeOf((*MockModule)(nil).CalculateFee), gasAmount, config)
}

// ProcessTransaction mocks base method.
func (m *MockModule) ProcessTransaction(ctx context.Context, msgs []types.Msg, accountInfo *types0.BaseAccount, config *TxConfig) (*tx.BroadcastTxResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ProcessTransaction", ctx, msgs, accountInfo, config)
	ret0, _ := ret[0].(*tx.BroadcastTxResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ProcessTransaction indicates an expected call of ProcessTransaction.
func (mr *MockModuleMockRecorder) ProcessTransaction(ctx, msgs, accountInfo, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ProcessTransaction", reflect.TypeOf((*MockModule)(nil).ProcessTransaction), ctx, msgs, accountInfo, config)
}

// SimulateTransaction mocks base method.
func (m *MockModule) SimulateTransaction(ctx context.Context, msgs []types.Msg, accountInfo *types0.BaseAccount, config *TxConfig) (*tx.SimulateResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SimulateTransaction", ctx, msgs, accountInfo, config)
	ret0, _ := ret[0].(*tx.SimulateResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// SimulateTransaction indicates an expected call of SimulateTransaction.
func (mr *MockModuleMockRecorder) SimulateTransaction(ctx, msgs, accountInfo, config any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SimulateTransaction", reflect.TypeOf((*MockModule)(nil).SimulateTransaction), ctx, msgs, accountInfo, config)
}
