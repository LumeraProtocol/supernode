package selfhealing

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/common/log"
	pb "github.com/LumeraProtocol/supernode/proto/supernode"
)

// Ping acknowledges the received message and return with the timestamp
func (task *SHTask) Ping(ctx context.Context, _ *pb.PingRequest) (*pb.PingResponse, error) {
	log.WithContext(ctx).WithField("node_id", task.nodeID).Debug("ping request received at the server")

	return &pb.PingResponse{
		ReceiverId:  task.nodeID,
		IsOnline:    true,
		RespondedAt: time.Now().UTC().String(),
	}, nil
}
