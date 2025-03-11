package common

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/proto"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade"
)

// RegisterCascade represents common grpc services for registration sense.
type RegisterCascade struct {
	*cascade.CascadeService
}

// SessID retrieves SessID from the metadata.
func (service *RegisterCascade) SessID(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}

	mdVals := md.Get(proto.MetadataKeySessID)
	if len(mdVals) == 0 {
		return "", false
	}
	return mdVals[0], true
}

// TaskFromMD returns task by SessID from the metadata.
func (service *RegisterCascade) TaskFromMD(ctx context.Context) (*cascade.CascadeRegistrationTask, error) {
	sessID, ok := service.SessID(ctx)
	if !ok {
		return nil, errors.New("not found sessID in metadata")
	}

	task := service.Task(sessID)
	if task == nil {
		return nil, errors.Errorf("not found %q task", sessID)
	}
	return task, nil
}

func (service *RegisterCascade) Desc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{ServiceName: "supernode.RegisterCascade", HandlerType: (*RegisterCascade)(nil)}
}

// NewRegisterCascade returns a new RegisterSense instance.
func NewRegisterCascade(service *cascade.CascadeService) *RegisterCascade {
	return &RegisterCascade{
		CascadeService: service,
	}
}
