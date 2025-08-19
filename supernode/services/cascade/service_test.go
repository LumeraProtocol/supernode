package cascade_test

import (
	"context"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade"
	cascadeadaptormocks "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors/mocks"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestNewCascadeService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLumera := cascadeadaptormocks.NewMockLumeraClient(ctrl)
	mockP2P := cascadeadaptormocks.NewMockP2PService(ctrl)
	mockCodec := cascadeadaptormocks.NewMockCodecService(ctrl)

	config := &cascade.Config{
		Config: common.Config{
			SupernodeAccountAddress: "lumera1abcxyz",
		},
	}

	service := cascade.NewCascadeService(config, nil, nil, nil, nil)
	service.LumeraClient = mockLumera
	service.RQ = mockCodec
	service.P2P = mockP2P

	assert.NotNil(t, service)
	assert.NotNil(t, service.LumeraClient)
	assert.NotNil(t, service.P2P)
	assert.NotNil(t, service.RQ)
}

func TestNewCascadeRegistrationTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLumera := cascadeadaptormocks.NewMockLumeraClient(ctrl)
	mockP2P := cascadeadaptormocks.NewMockP2PService(ctrl)
	mockCodec := cascadeadaptormocks.NewMockCodecService(ctrl)

	config := &cascade.Config{
		Config: common.Config{
			SupernodeAccountAddress: "lumera1abcxyz",
		},
	}

	service := cascade.NewCascadeService(config, nil, nil, nil, nil)
	service.LumeraClient = mockLumera
	service.RQ = mockCodec
	service.P2P = mockP2P

	task := cascade.NewCascadeRegistrationTask(service)
	assert.NotNil(t, task)

	go func() {
		service.Worker.AddTask(task)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := service.RunHelper(ctx, "node-id", "prefix")
	assert.NoError(t, err)
}
