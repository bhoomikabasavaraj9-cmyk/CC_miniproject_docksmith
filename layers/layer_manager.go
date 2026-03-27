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
// ============================================================================
// SNAPSHOT & DELTA STRUCTURES
// ============================================================================

// FileSnapshot represents the state of a file at a point in time
type FileSnapshot struct {
	Path    string
	ModTime int64
	Size    int64
	IsDir   bool
	Mode    os.FileMode
}

// snapshotFilesystem creates a snapshot of all files and directories in root
func snapshotFilesystem(root string) (map[string]FileSnapshot, error) {
	snapshot := make(map[string]FileSnapshot)
	
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		
		// Get relative path
		relPath, _ := filepath.Rel(root, path)
		
		// Skip the root directory itself
		if relPath == "." {
			return nil
		}
		
		fs := FileSnapshot{
			Path:    relPath,
			ModTime: info.ModTime().Unix(),
			Size:    info.Size(),
			IsDir:   info.IsDir(),
			Mode:    info.Mode(),
		}
		
		snapshot[relPath] = fs
		return nil
	})
	
	return snapshot, err
}

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
// CreateCopyLayer copies files from contextDir into assembledRoot and creates a DELTA tar layer
func CreateCopyLayer(contextDir, assembledRoot, instrArgs string) (string, error) {
	// 1. Take a SNAPSHOT of assembledRoot BEFORE copying
	snapshotBefore, err := snapshotFilesystem(assembledRoot)
	if err != nil {
		return "", fmt.Errorf("snapshot before copy: %w", err)
	}
	
	// 2. Do the actual COPY
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
	
	// 3. Take a SNAPSHOT AFTER copying
	snapshotAfter, err := snapshotFilesystem(assembledRoot)
	if err != nil {
		return "", fmt.Errorf("snapshot after copy: %w", err)
	}
	
	// 4. Compute DELTA (only changed/new files)
	delta := computeDeltaForCopy(snapshotBefore, snapshotAfter)
	
	// 5. Create tar of ONLY the delta
	tempTar := filepath.Join(getLayersDir(), "temp_layer.tar")
	if err := createDeltaTar(assembledRoot, delta, tempTar); err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	// 6. Compute digest
	digest, err := fileToDigest(tempTar)
	if err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	// 7. Move to final location
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
// createDeltaTar creates a tar archive of only the changed/new files
func createDeltaTar(root string, delta map[string]FileSnapshot, tarPath string) error {
	tf, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer tf.Close()
	
	tw := tar.NewWriter(tf)
	defer tw.Close()
	
	for relPath, snapshot := range delta {
		fullPath := filepath.Join(root, relPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			continue // file was deleted, skip
		}
		
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			continue
		}
		header.Name = relPath
		
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		
		// For regular files, write content
		if !info.IsDir() && info.Mode().IsRegular() {
			f, err := os.Open(fullPath)
			if err != nil {
				continue
			}
			io.Copy(tw, f)
			f.Close()
		}
	}
	
	return nil
}

// computeDeltaForCopy identifies changed and new files
func computeDeltaForCopy(before, after map[string]FileSnapshot) map[string]FileSnapshot {
	delta := make(map[string]FileSnapshot)
	
	for path, afterFile := range after {
		beforeFile, exists := before[path]
		
		// New file or file was modified (by ModTime or Size)
		if !exists || beforeFile.ModTime != afterFile.ModTime || beforeFile.Size != afterFile.Size {
			delta[path] = afterFile
		}
	}
	
	return delta
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
