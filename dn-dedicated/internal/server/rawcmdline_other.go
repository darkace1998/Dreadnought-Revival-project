//go:build !windows

package server

import "os/exec"

// applyRawCmdLine is a no-op off Windows.
//
// The raw-command-line override exists only to satisfy Unreal's in-place parsing
// of the Win32 command line string; on Linux the direct-exec path passes a real
// argv, and the Wine path is handled by Wine's own CreateProcess emulation.
func applyRawCmdLine(_ *exec.Cmd, _ string, _ []string) {}
