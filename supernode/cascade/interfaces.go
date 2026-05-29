package cascade

import (
	"context"
)

// CascadeServiceFactory defines an interface to create cascade tasks
//

type CascadeServiceFactory interface {
	NewCascadeRegistrationTask() CascadeTask
}

// CascadeTask interface defines operations for cascade registration and data management
type CascadeTask interface {
	Register(ctx context.Context, req *RegisterRequest, send func(resp *RegisterResponse) error) error
	Download(ctx context.Context, req *DownloadRequest, send func(resp *DownloadResponse) error) error
	CleanupDownload(ctx context.Context, tmpDir string) error

	// LEP-6 healer entrypoints. Surface RecoveryReseed and the staged-publish
	// promotion so the self_healing service can consume the cascade pipeline
	// through CascadeServiceFactory without depending on the concrete
	// *CascadeRegistrationTask.
	RecoveryReseed(ctx context.Context, req *RecoveryReseedRequest) (*RecoveryReseedResult, error)
	PublishStagedArtefacts(ctx context.Context, stagingDir string) error
}
