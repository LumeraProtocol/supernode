// Code generated by MockGen. DO NOT EDIT.
// Source: rq.go

// Package cascadeadaptormocks is a generated GoMock package.
package cascadeadaptormocks

import (
	context "context"
	reflect "reflect"

	adaptors "github.com/LumeraProtocol/supernode/supernode/services/cascade/adaptors"
	gomock "github.com/golang/mock/gomock"
)

// MockCodecService is a mock of CodecService interface.
type MockCodecService struct {
	ctrl     *gomock.Controller
	recorder *MockCodecServiceMockRecorder
}

// MockCodecServiceMockRecorder is the mock recorder for MockCodecService.
type MockCodecServiceMockRecorder struct {
	mock *MockCodecService
}

// NewMockCodecService creates a new mock instance.
func NewMockCodecService(ctrl *gomock.Controller) *MockCodecService {
	mock := &MockCodecService{ctrl: ctrl}
	mock.recorder = &MockCodecServiceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockCodecService) EXPECT() *MockCodecServiceMockRecorder {
	return m.recorder
}

// EncodeInput mocks base method.
func (m *MockCodecService) EncodeInput(ctx context.Context, taskID, path string, dataSize int) (adaptors.EncodeResult, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EncodeInput", ctx, taskID, path, dataSize)
	ret0, _ := ret[0].(adaptors.EncodeResult)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// EncodeInput indicates an expected call of EncodeInput.
func (mr *MockCodecServiceMockRecorder) EncodeInput(ctx, taskID, path, dataSize interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EncodeInput", reflect.TypeOf((*MockCodecService)(nil).EncodeInput), ctx, taskID, path, dataSize)
}
