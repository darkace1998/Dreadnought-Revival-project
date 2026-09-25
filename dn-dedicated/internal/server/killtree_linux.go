//go:build linux

package server

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// wineSharedProcesses are the Wine prefix's own infrastructure. They are
// started by whichever wine process in the prefix comes up first, so they carry
// THAT host's DN_INSTANCE_ID -- but every host in the prefix depends on them.
//
// Measured 2026-09-25: two hosts shared a prefix; the reaper stopped the first
// (port 7777, "every player has left", 17:42:34) and the second (port 7778, a
// live match) went silent at 17:42:34.047 with no exception and was found with
// its main thread dead. Killing the tagged wineserver/services took it down.
// They exit on their own once the last wine process in the prefix is gone.
var wineSharedProcesses = map[string]bool{
	"wineserver":     true,
	"wineserver64":   true,
	"services.exe":   true,
	"winedevice.exe": true,
	"explorer.exe":   true,
	"plugplay.exe":   true,
	"rpcss.exe":      true,
	"svchost.exe":    true,
	"wineboot.exe":   true,
}

// isWineSharedProcess reports whether a /proc/<pid>/cmdline belongs to the
// prefix's shared infrastructure. argv[0] is a Unix path for wineserver and a
// Windows path ("C:\\windows\\system32\\services.exe") for the rest.
func isWineSharedProcess(cmdline []byte) bool {
	argv0 := string(cmdline)
	if i := strings.IndexByte(argv0, 0); i >= 0 {
		argv0 = argv0[:i]
	}
	if i := strings.LastIndexAny(argv0, "/\\"); i >= 0 {
		argv0 = argv0[i+1:]
	}
	return wineSharedProcesses[strings.ToLower(argv0)]
}

// killTaggedProcesses SIGKILLs every process whose environment carries
// DN_INSTANCE_ID=<id>. Under Wine the engine's children are not in our process
// group and are reparented to init when the engine dies, so the tag is the only
// reliable handle on them. The prefix's shared Wine processes are spared (see
// wineSharedProcesses). Returns how many were signalled.
func killTaggedProcesses(id string) int {
	if id == "" {
		return 0
	}
	want := []byte(instanceEnvKey + "=" + id)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	self := os.Getpid()
	killed := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		env, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil || !environHas(env, want) {
			continue
		}
		if cmd, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil && isWineSharedProcess(cmd) {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}

// environHas reports whether a NUL-separated environ block holds exactly the
// entry want (not merely a prefix of a longer value).
func environHas(env, want []byte) bool {
	for _, kv := range bytes.Split(env, []byte{0}) {
		if bytes.Equal(kv, want) {
			return true
		}
	}
	return false
}

// leaderIsZombie reports whether the process's thread-group leader has exited.
// Under Wine that is the engine's main thread: on every dead host of 2026-09-24
// it read 'Z' while orphaned threads sat blocked on wineserver pipes (Wine no
// longer listed the process), and on every live host it read 'S' or 'R'.
func leaderIsZombie(pid int) bool {
	if pid <= 0 {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// Format: pid (comm) state ... -- comm may contain spaces, so find the last ')'.
	i := bytes.LastIndexByte(b, ')')
	return i >= 0 && i+2 < len(b) && b[i+2] == 'Z'
}
