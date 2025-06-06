// Code generated by MockGen. DO NOT EDIT.
// Source: interface.go
//
// Generated by this command:
//
//	mockgen -destination=node_mock.go -package=node -source=interface.go
//

// Package node is a generated GoMock package.
package node

import (
	context "context"
	reflect "reflect"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
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

// GetBlockByHeight mocks base method.
func (m *MockModule) GetBlockByHeight(ctx context.Context, height int64) (*cmtservice.GetBlockByHeightResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBlockByHeight", ctx, height)
	ret0, _ := ret[0].(*cmtservice.GetBlockByHeightResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBlockByHeight indicates an expected call of GetBlockByHeight.
func (mr *MockModuleMockRecorder) GetBlockByHeight(ctx, height any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBlockByHeight", reflect.TypeOf((*MockModule)(nil).GetBlockByHeight), ctx, height)
}

// GetLatestBlock mocks base method.
func (m *MockModule) GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetLatestBlock", ctx)
	ret0, _ := ret[0].(*cmtservice.GetLatestBlockResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetLatestBlock indicates an expected call of GetLatestBlock.
func (mr *MockModuleMockRecorder) GetLatestBlock(ctx any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetLatestBlock", reflect.TypeOf((*MockModule)(nil).GetLatestBlock), ctx)
}

// GetLatestValidatorSet mocks base method.
func (m *MockModule) GetLatestValidatorSet(ctx context.Context) (*cmtservice.GetLatestValidatorSetResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetLatestValidatorSet", ctx)
	ret0, _ := ret[0].(*cmtservice.GetLatestValidatorSetResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetLatestValidatorSet indicates an expected call of GetLatestValidatorSet.
func (mr *MockModuleMockRecorder) GetLatestValidatorSet(ctx any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetLatestValidatorSet", reflect.TypeOf((*MockModule)(nil).GetLatestValidatorSet), ctx)
}

// GetNodeInfo mocks base method.
func (m *MockModule) GetNodeInfo(ctx context.Context) (*cmtservice.GetNodeInfoResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeInfo", ctx)
	ret0, _ := ret[0].(*cmtservice.GetNodeInfoResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNodeInfo indicates an expected call of GetNodeInfo.
func (mr *MockModuleMockRecorder) GetNodeInfo(ctx any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeInfo", reflect.TypeOf((*MockModule)(nil).GetNodeInfo), ctx)
}

// GetSyncing mocks base method.
func (m *MockModule) GetSyncing(ctx context.Context) (*cmtservice.GetSyncingResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSyncing", ctx)
	ret0, _ := ret[0].(*cmtservice.GetSyncingResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSyncing indicates an expected call of GetSyncing.
func (mr *MockModuleMockRecorder) GetSyncing(ctx any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSyncing", reflect.TypeOf((*MockModule)(nil).GetSyncing), ctx)
}

// GetValidatorSetByHeight mocks base method.
func (m *MockModule) GetValidatorSetByHeight(ctx context.Context, height int64) (*cmtservice.GetValidatorSetByHeightResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetValidatorSetByHeight", ctx, height)
	ret0, _ := ret[0].(*cmtservice.GetValidatorSetByHeightResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetValidatorSetByHeight indicates an expected call of GetValidatorSetByHeight.
func (mr *MockModuleMockRecorder) GetValidatorSetByHeight(ctx, height any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetValidatorSetByHeight", reflect.TypeOf((*MockModule)(nil).GetValidatorSetByHeight), ctx, height)
}

// Sign mocks base method.
func (m *MockModule) Sign(snAccAddress string, data []byte) ([]byte, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Sign", snAccAddress, data)
	ret0, _ := ret[0].([]byte)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Sign indicates an expected call of Sign.
func (mr *MockModuleMockRecorder) Sign(snAccAddress, data any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Sign", reflect.TypeOf((*MockModule)(nil).Sign), snAccAddress, data)
}
