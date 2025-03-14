package client

import (
	"context"

	"github.com/LumeraProtocol/supernode/proto"
	"google.golang.org/grpc/metadata"
)

func contextWithMDSessID(ctx context.Context, sessID string) context.Context {
	md := metadata.Pairs(proto.MetadataKeySessID, sessID)
	return metadata.NewOutgoingContext(ctx, md)
}
