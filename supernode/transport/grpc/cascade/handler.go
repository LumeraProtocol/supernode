package cascade

import (
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	tasks "github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

type ActionServer struct {
	pb.UnimplementedCascadeServiceServer
	factory         cascadeService.CascadeServiceFactory
	tracker         tasks.Tracker
	uploadTimeout   time.Duration
	downloadTimeout time.Duration
}

const (
	serviceCascadeUpload   = "cascade.upload"
	serviceCascadeDownload = "cascade.download"
)

// NewCascadeActionServer creates a new CascadeActionServer with injected service and tracker
func NewCascadeActionServer(factory cascadeService.CascadeServiceFactory, tracker tasks.Tracker, uploadTO, downloadTO time.Duration) *ActionServer {
	if uploadTO <= 0 {
		uploadTO = 30 * time.Minute
	}
	if downloadTO <= 0 {
		downloadTO = 30 * time.Minute
	}
	return &ActionServer{factory: factory, tracker: tracker, uploadTimeout: uploadTO, downloadTimeout: downloadTO}
}
