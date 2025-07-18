package capabilities

// CompatibilityManager handles version comparison and compatibility checking between supernodes
type CompatibilityManager interface {
	// CheckCompatibility compares local and peer capabilities and returns compatibility result
	CheckCompatibility(local, peer *Capabilities) (*CompatibilityResult, error)

	// IsVersionCompatible checks if two version strings are compatible using semantic versioning
	IsVersionCompatible(localVersion, peerVersion string) (bool, error)

	// HasRequiredCapabilities checks if the given capabilities include all required actions
	HasRequiredCapabilities(caps *Capabilities, required []string) bool
}
