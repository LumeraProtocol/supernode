package client

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	node "github.com/LumeraProtocol/supernode/supernode/node/supernode"
)

type SupernodeCascadeActionClient struct {
	sessID string

	conn   *clientConn
	client pb.CascadeServiceClient
}

func (service *SupernodeCascadeActionClient) SessID() string {
	return service.sessID
}

func (service *SupernodeCascadeActionClient) Session(ctx context.Context, nodeID, sessID string) error {
	service.sessID = sessID

	stream, err := service.client.Session(ctx)
	if err != nil {
		return errors.Errorf("open Health stream: %w", err)
	}

	req := &pb.SessionRequest{
		NodeID: nodeID,
	}

	if err := stream.Send(req); err != nil {
		return errors.Errorf("send Session request: %w", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		switch status.Code(err) {
		case codes.Canceled, codes.Unavailable:
			return nil
		}
		return errors.Errorf("receive Session response: %w", err)
	}
	log.WithContext(ctx).WithField("resp", resp).Debug("Session response")

	go func() {
		defer service.conn.Close()
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()

	return nil
}

func newSupernodeCascadeActionClient(conn *clientConn) node.RegisterCascadeInterface {
	return &SupernodeCascadeActionClient{
		conn:   conn,
		client: pb.NewCascadeServiceClient(conn),
	}
}
