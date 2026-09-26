//go:build linux

package server

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// totalMemoryBytes reads MemTotal from /proc/meminfo (inside an LXC this is the
// container's limit), or 0.
func totalMemoryBytes() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}
