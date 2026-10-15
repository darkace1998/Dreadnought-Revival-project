package server

import (
	"os"
	"strconv"
)

// PublicUDPPort reports the UDP port handed to CLIENTS for a battle server
// listening on the given local port. Both are the same normally. Under a
// relay (playit.gg, ngrok, ...) the client-visible external port differs:
// the relay's edge port is what the travel URL must carry, and this is
// configured for it with DN_UDP_PORT_PUBLIC.
func PublicUDPPort(local int) int {
	if v := os.Getenv("DN_UDP_PORT_PUBLIC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return local
}