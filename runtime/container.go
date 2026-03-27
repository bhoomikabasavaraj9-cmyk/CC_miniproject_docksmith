package runtime

// This file contains STUB implementations.
// Person 4 will replace these with real logic.

// RunInContainer executes cmd inside root using OS-level process isolation.
// Used by the build engine for RUN instructions.
// Returns the sha256 digest of the delta layer produced by the command.
func RunInContainer(root, cmd, workdir string, env []string) (string, error) {
	panic("runtime.RunInContainer not yet implemented — Person 4 will implement this")
}

// RunContainerForeground assembles and runs a container in the foreground.
// Used by `docksmith run`. Returns the process exit code.
func RunContainerForeground(root string, cmd []string, workdir string, env []string) (int, error) {
	panic("runtime.RunContainerForeground not yet implemented — Person 4 will implement this")
}
