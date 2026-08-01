//go:build !windows

package server

import "os/exec"

// Off Windows there is no window to hide: the battle server runs under Wine on a
// headless host, or under a null RHI with no display attached at all.

func hideProcessWindows(_ int) int { return 0 }

func configureHidden(_ *exec.Cmd) {}
