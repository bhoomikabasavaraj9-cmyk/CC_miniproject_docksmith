package cli

// cli_helpers.go
// These thin wrappers let commands.go call layers/runtime without builder knowing about cli.

import (
	"docksmith/layers"
	"docksmith/runtime"
)

func extractLayersForRun(digests []string, destDir string) error {
	return layers.ExtractLayers(digests, destDir)
}

func runContainerForRun(root string, cmd []string, workdir string, env []string) (int, error) {
	return runtime.RunContainerForeground(root, cmd, workdir, env)
}
