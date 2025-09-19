package cascade

const (
	// SkipArtifactStorageHeader is propagated via gRPC metadata to indicate
	// the supernode should bypass artifact persistence.
	SkipArtifactStorageHeader = "x-lumera-skip-artifact-storage"
	// SkipArtifactStorageHeaderValue marks the header value for opting out of storage.
	SkipArtifactStorageHeaderValue = "true"
	// LogFieldSkipStorage standardises the log key used when skip storage is triggered.
	LogFieldSkipStorage = "skip_storage"
)

const (
	// ArtifactStorageSkippedMessage is published when artifact persistence is skipped.
	ArtifactStorageSkippedMessage = "Artifact storage skipped by client instruction"
)
