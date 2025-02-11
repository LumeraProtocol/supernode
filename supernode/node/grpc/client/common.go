package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"

	"github.com/LumeraProtocol/supernode/common/log"
	"github.com/LumeraProtocol/supernode/proto"
)

func contextWithMDSessID(ctx context.Context, sessID string) context.Context {
	md := metadata.Pairs(proto.MetadataKeySessID, sessID)
	return metadata.NewOutgoingContext(ctx, md)
}

func contextWithLogPrefix(ctx context.Context, connID string) context.Context {
	return log.ContextWithPrefix(ctx, fmt.Sprintf("%s-%s", logPrefix, connID))
}
