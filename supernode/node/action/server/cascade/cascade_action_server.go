package cascade

import (
	"fmt"
	"io"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/services/cascade"
	"google.golang.org/grpc"
)

type CascadeActionServer struct {
	pb.UnimplementedCascadeServiceServer
	service *cascadeService.CascadeService
}

func NewCascadeActionServer(service *cascadeService.CascadeService) *CascadeActionServer {
	return &CascadeActionServer{
		service: service,
	}
}

func (server *CascadeActionServer) Desc() *grpc.ServiceDesc {
	return &pb.CascadeService_ServiceDesc
}
func (server *CascadeActionServer) Register(stream pb.CascadeService_RegisterServer) error {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "Register",
		logtrace.FieldModule: "CascadeActionServer",
	}

	ctx := stream.Context()
	logtrace.Info(ctx, "client streaming request to upload cascade input data received", fields)

	// Collect data chunks
	var allData []byte
	var metadata *pb.Metadata

	// Process incoming stream
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// End of stream
			break
		}
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "error receiving stream data", fields)
			return fmt.Errorf("failed to receive stream data: %w", err)
		}

		// Check which type of message we received
		switch x := req.RequestType.(type) {
		case *pb.RegisterRequest_Chunk:
			// Add data chunk to our collection
			allData = append(allData, x.Chunk.Data...)
			logtrace.Info(ctx, "received data chunk", logtrace.Fields{
				"chunk_size":        len(x.Chunk.Data),
				"total_size_so_far": len(allData),
			})

		case *pb.RegisterRequest_Metadata:
			// Store metadata - this should be the final message
			metadata = x.Metadata
			logtrace.Info(ctx, "received metadata", logtrace.Fields{
				"task_id":   metadata.TaskId,
				"action_id": metadata.ActionId,
			})
		}
	}

	// Verify we received metadata
	if metadata == nil {
		logtrace.Error(ctx, "no metadata received in stream", fields)
		return fmt.Errorf("no metadata received")
	}

	// Process the complete data
	task := server.service.NewCascadeRegistrationTask()
	res, err := task.Register(ctx, &cascadeService.RegisterRequest{
		TaskID:   metadata.TaskId,
		ActionID: metadata.ActionId,
		Data:     allData,
	})

	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to upload input data", fields)
		return fmt.Errorf("cascade services upload input data error: %w", err)
	}

	// Send the response
	return stream.SendMsg(&pb.RegisterResponse{
		Success: res.Success,
		Message: res.Message,
	})
}
