package layers

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getLayersDir returns ~/.docksmith/layers/
func getLayersDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docksmith", "layers")
}

// digestToPath converts "sha256:abc123" → "~/.docksmith/layers/abc123.tar"
func digestToPath(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	return filepath.Join(getLayersDir(), hex+".tar")
}

// fileToDigest computes sha256 digest of a file
func fileToDigest(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	
	hexStr := hex.EncodeToString(h.Sum(nil))
	return "sha256:" + hexStr, nil
}

// ============================================================================
// MAIN FUNCTIONS CALLED BY BUILDER
// ============================================================================

// LayerExists reports whether the layer tar for the given digest exists on disk.
func LayerExists(digest string) bool {
	path := digestToPath(digest)
	_, err := os.Stat(path)
	return err == nil
}

// ExtractLayers extracts the given layer tars (by digest, in order) into destDir.
func ExtractLayers(digests []string, destDir string) error {
	for _, digest := range digests {
		if err := extractSingleLayer(digest, destDir); err != nil {
			return fmt.Errorf("extract layer %s: %w", digest, err)
		}
	}
	return nil
}

// extractSingleLayer extracts one tar into destDir
func extractSingleLayer(digest, destDir string) error {
	tarPath := digestToPath(digest)
	
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	
	tr := tar.NewReader(f)
	
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		
		// target path inside container
		targetPath := filepath.Join(destDir, header.Name)
		
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			// ensure parent dir exists
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			
			f, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// Handle symlinks (optional, for robustness)
			os.Symlink(header.Linkname, targetPath)
		}
	}
	
	return nil
}

// CreateCopyLayer copies files from contextDir into assembledRoot and creates a tar layer
func CreateCopyLayer(contextDir, assembledRoot, instrArgs string) (string, error) {
	// Parse COPY instruction: "SRC DEST" or "SRC1 SRC2 ... DEST"
	parts := strings.Fields(instrArgs)
	if len(parts) < 2 {
		return "", fmt.Errorf("COPY requires at least SRC and DEST")
	}
	
	sources := parts[:len(parts)-1]
	dest := parts[len(parts)-1]
	
	// Normalize destination path
	if !filepath.IsAbs(dest) {
		dest = "/" + dest
	}
	destInRoot := filepath.Join(assembledRoot, strings.TrimPrefix(dest, "/"))
	
	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(destInRoot, 0755); err != nil {
		return "", err
	}
	
	// Copy each source
	for _, src := range sources {
		srcPath := filepath.Join(contextDir, src)
		
		// Check if source exists
		info, err := os.Stat(srcPath)
		if err != nil {
			return "", fmt.Errorf("source not found: %s", src)
		}
		
		if info.IsDir() {
			// Copy directory recursively
			if err := copyDir(srcPath, filepath.Join(destInRoot, filepath.Base(src))); err != nil {
				return "", err
			}
		} else {
			// Copy single file
			if err := copyFile(srcPath, filepath.Join(destInRoot, filepath.Base(src))); err != nil {
				return "", err
			}
		}
	}
	
	// Create tar of the entire root directory
	tempTar := filepath.Join(getLayersDir(), "temp_layer.tar")
	if err := createLayerTar(assembledRoot, tempTar); err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	// Compute digest
	digest, err := fileToDigest(tempTar)
	if err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	// Move to final location
	finalPath := digestToPath(digest)
	if err := os.Rename(tempTar, finalPath); err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	return digest, nil
}

// copyFile copies a single file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	
	_, err = io.Copy(dstFile, srcFile)
	return err
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	
	return nil
}

// createLayerTar tars the entire root directory
func createLayerTar(rootDir, tarPath string) error {
	tf, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer tf.Close()
	
	tw := tar.NewWriter(tf)
	defer tw.Close()
	
	return filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Get relative path for tar header
		relPath, _ := filepath.Rel(rootDir, path)
		
		// Skip the root itself
		if relPath == "." {
			return nil
		}
		
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath
		
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		
		// For directories, just write header; for files, write content
		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		}
		
		return nil
	})
}

// HashCopySources computes a deterministic hash of all source files
func HashCopySources(contextDir, instrArgs string) (string, error) {
	parts := strings.Fields(instrArgs)
	if len(parts) < 2 {
		return "", fmt.Errorf("COPY requires at least SRC and DEST")
	}
	
	sources := parts[:len(parts)-1]
	
	h := sha256.New()
	
	// Hash each source file in order
	for _, src := range sources {
		srcPath := filepath.Join(contextDir, src)
		
		if err := hashPath(srcPath, h); err != nil {
			return "", err
		}
	}
	
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashPath recursively hashes a file or directory
func hashPath(path string, h io.Writer) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		
		for _, entry := range entries {
			if err := hashPath(filepath.Join(path, entry.Name()), h); err != nil {
				return err
			}
		}
		return nil
	}
	
	// Hash the filename and content
	io.WriteString(h, info.Name())
	
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	
	_, err = io.Copy(h, f)
	return err
}
