package layers

// This file contains STUB implementations.
// Person 3 will replace these with real logic.

// CreateCopyLayer copies files matching the COPY instruction args from contextDir
// into assembledRoot and returns the sha256 digest of the resulting layer tar.
func CreateCopyLayer(contextDir, assembledRoot, instrArgs string) (string, error) {
	panic("layers.CreateCopyLayer not yet implemented — Person 3 will implement this")
}

// HashCopySources returns a deterministic hash of all source files matched by
// the COPY instruction (for cache key computation).
func HashCopySources(contextDir, instrArgs string) (string, error) {
	panic("layers.HashCopySources not yet implemented — Person 3 will implement this")
}

// LayerExists reports whether the layer tar for the given digest exists on disk.
func LayerExists(digest string) bool {
	panic("layers.LayerExists not yet implemented — Person 3 will implement this")
}

// ExtractLayers extracts the given layer tars (by digest, in order) into destDir.
func ExtractLayers(digests []string, destDir string) error {
	panic("layers.ExtractLayers not yet implemented — Person 3 will implement this")
}
