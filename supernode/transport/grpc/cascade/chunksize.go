package cascade

// calculateOptimalChunkSize returns an optimal chunk size based on file size
// to balance throughput and memory usage
func calculateOptimalChunkSize(fileSize int64) int {
	const (
		minChunkSize        = 64 * 1024         // 64 KB minimum
		maxChunkSize        = 4 * 1024 * 1024   // 4 MB maximum for 1GB+ files
		smallFileThreshold  = 1024 * 1024       // 1 MB
		mediumFileThreshold = 50 * 1024 * 1024  // 50 MB
		largeFileThreshold  = 500 * 1024 * 1024 // 500 MB
	)

	var chunkSize int

	switch {
	case fileSize <= smallFileThreshold:
		chunkSize = minChunkSize
	case fileSize <= mediumFileThreshold:
		chunkSize = 256 * 1024
	case fileSize <= largeFileThreshold:
		chunkSize = 1024 * 1024
	default:
		chunkSize = maxChunkSize
	}

	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	return chunkSize
}
