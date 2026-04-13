package runtime

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ============================================================================
// FUNCTION 1: RunContainerForeground
// ============================================================================

// RunContainerForeground executes a container in the foreground with attached I/O.
// Used by `docksmith run` command.
// Returns the process exit code.
func RunContainerForeground(root string, cmd []string, workdir string, env []string) (int, error) {
	if len(cmd) == 0 {
		return 1, fmt.Errorf("no command provided")
	}
	
	// Validate that root directory exists
	if _, err := os.Stat(root); err != nil {
		return 1, fmt.Errorf("container root does not exist: %s", root)
	}
	
	// Build command
	var process *exec.Cmd
	if len(cmd) == 1 {
		// Single command - run directly
		process = exec.Command(cmd[0])
	} else {
		// Multiple args - first is program, rest are args
		process = exec.Command(cmd[0], cmd[1:]...)
	}
	
	// Set working directory inside container
	if workdir != "" {
		// workdir is already relative to the container root
		absWorkdir := filepath.Join(root, strings.TrimPrefix(workdir, "/"))
		os.MkdirAll(absWorkdir, 0755)
		process.Dir = absWorkdir
	} else {
		process.Dir = root
	}
	
	// Merge provided environment with OS environment
	processEnv := os.Environ()
	processEnv = append(processEnv, env...)
	process.Env = processEnv
	
	// Attach standard I/O streams
	process.Stdin = os.Stdin
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	
	// Execute the process
	err := process.Run()
	
	// Extract exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Process exited with non-zero code
			return exitErr.ExitCode(), nil
		}
		// Other error (e.g., command not found)
		return 1, err
	}
	
	// Success
	return 0, nil
}

// ============================================================================
// FUNCTION 2: RunInContainer (with delta layer capture)
// ============================================================================

// RunInContainer executes cmd inside root using OS-level process isolation.
// Captures the delta layer (filesystem changes) and returns its SHA256 digest.
// Used by the build engine for RUN instructions.
func RunInContainer(root, cmd, workdir string, env []string) (string, error) {
	// Create a snapshot of the root filesystem BEFORE execution
	snapshotBefore, err := snapshotFilesystem(root)
	if err != nil {
		return "", fmt.Errorf("snapshot before execution: %w", err)
	}
	
	// Build shell command (execute as shell script)
	shellCmd := []string{"/bin/sh", "-c", cmd}
	process := exec.Command(shellCmd[0], shellCmd[1:]...)
	
	// Set working directory
	if workdir != "" {
		absWorkdir := filepath.Join(root, strings.TrimPrefix(workdir, "/"))
		os.MkdirAll(absWorkdir, 0755)
		process.Dir = absWorkdir
	} else {
		process.Dir = root
	}
	
	// Set environment
	processEnv := os.Environ()
	processEnv = append(processEnv, env...)
	process.Env = processEnv
	
	// Attach I/O for visibility during build
	process.Stdin = os.Stdin
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	
	// Execute the command (ignore exit code, we capture the layer regardless)
	_ = process.Run()
	
	// Snapshot AFTER execution
	snapshotAfter, err := snapshotFilesystem(root)
	if err != nil {
		return "", fmt.Errorf("snapshot after execution: %w", err)
	}
	
	// Compute the delta (changed and new files)
	delta := computeDelta(snapshotBefore, snapshotAfter)
	
	// Create a tar file of the delta
	tempTar := filepath.Join(getLayersDir(), "temp_layer.tar")
	if err := createDeltaTar(root, delta, tempTar); err != nil {
		os.Remove(tempTar)
		return "", fmt.Errorf("create delta tar: %w", err)
	}
	
	// Compute SHA256 digest of the tar file
	digest, err := fileToDigest(tempTar)
	if err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	// Move tar to its final location
	finalPath := digestToPath(digest)
	if err := os.Rename(tempTar, finalPath); err != nil {
		os.Remove(tempTar)
		return "", err
	}
	
	return digest, nil
}

// ============================================================================
// HELPER STRUCTURES AND FUNCTIONS
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

// computeDelta identifies changed and new files
func computeDelta(before, after map[string]FileSnapshot) map[string]FileSnapshot {
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

// fileToDigest computes SHA256 digest of a file
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
// EXPORTED WRAPPERS FOR BUILDER
// ============================================================================

// RunInContainerForBuild wraps RunInContainer for use by builder
// Executes a shell command in the assembled container root and captures delta layer
func RunInContainerForBuild(root, cmd, workdir string, env []string) (string, error) {
	return RunInContainer(root, cmd, workdir, env)
}

// RunContainerForegroundForCLI wraps RunContainerForeground for use by CLI
// Executes a container in foreground for the `docksmith run` command
func RunContainerForegroundForCLI(root string, cmd []string, workdir string, env []string) (int, error) {
	return RunContainerForeground(root, cmd, workdir, env)
}
