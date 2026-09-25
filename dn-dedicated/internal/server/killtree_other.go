//go:build !linux

package server

// Off Linux the battle server is a native process whose children end with it;
// there is no Wine tree to chase.
func killTaggedProcesses(_ string) int { return 0 }

func leaderIsZombie(_ int) bool { return false }
