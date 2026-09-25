//go:build linux

package server

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestEnvironHasMatchesWholeEntriesOnly(t *testing.T) {
	env := []byte("A=1\x00DN_INSTANCE_ID=abc\x00B=2\x00")
	if !environHas(env, []byte("DN_INSTANCE_ID=abc")) {
		t.Error("exact entry not found")
	}
	if environHas(env, []byte("DN_INSTANCE_ID=ab")) {
		t.Error("prefix of a value matched")
	}
}

// A child that inherits the tag is found and killed; this is the property the
// Wine CEF helpers rely on.
func TestKillTaggedProcessesKillsTheTaggedChild(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), instanceEnvKey+"=test-"+t.Name())
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if n := killTaggedProcesses("test-" + t.Name()); n != 1 {
		t.Errorf("killed %d processes, want 1", n)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("tagged child still running")
	}
}

func TestLeaderIsZombieIsFalseForALiveProcess(t *testing.T) {
	if leaderIsZombie(os.Getpid()) {
		t.Error("this test process reported as zombie")
	}
	if leaderIsZombie(0) || leaderIsZombie(-1) {
		t.Error("invalid pid reported as zombie")
	}
}

// The argv[0] forms seen in /proc on this box (2026-09-25): wineserver as a
// Unix path, the prefix services as Windows paths, the game and CEF as neither.
func TestWineSharedProcessesAreSpared(t *testing.T) {
	cases := map[string]bool{
		"/usr/lib/wine/wineserver64\x00-p0\x00":                              true,
		"C:\\windows\\system32\\services.exe\x00":                            true,
		"C:\\windows\\system32\\explorer.exe\x00/desktop\x00":                true,
		"C:\\windows\\system32\\winedevice.exe\x00":                          true,
		"C:\\windows\\system32\\rpcss.exe\x00":                               true,
		"Z:\\root\\projects\\src\\DreadGame-Win64-Shipping.exe\x00/Game\x00": false,
		"UnrealCEFSubProcess.exe\x00--type=gpu-process\x00":                  false,
		"winedbg\x00--auto\x00312\x00":                                       false,
		"":                                                                   false,
	}
	for cmd, want := range cases {
		if got := isWineSharedProcess([]byte(cmd)); got != want {
			t.Errorf("isWineSharedProcess(%q) = %v, want %v", cmd, got, want)
		}
	}
}
