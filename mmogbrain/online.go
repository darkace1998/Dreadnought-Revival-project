package main

import (
	"encoding/json"
	"net"
	"net/http"
	"sort"
)

// onlinePlayer is one connected player as GET /online lists them.
type onlinePlayer struct {
	PID    string `json:"pid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// onlinePlayers lists everyone with a live Firmament (social) connection --
// the socket every logged-in client holds -- sorted by name.
func (h *socialHub) onlinePlayers() []onlinePlayer {
	h.mu.RLock()
	peers := make([]*socialPeer, 0, len(h.peers))
	for _, p := range h.peers {
		peers = append(peers, p)
	}
	h.mu.RUnlock()
	out := make([]onlinePlayer, 0, len(peers))
	for _, p := range peers {
		p.mu.Lock()
		status := p.status
		p.mu.Unlock()
		if status == "" {
			status = "online"
		}
		name := mmogPlayerStateForPID(p.playerID).displayName
		out = append(out, onlinePlayer{PID: p.playerID, Name: name, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GET /online -> {"count": N, "players": [...]}. Loopback only: it names
// players, which is not for the internet.
func onlineHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	players := []onlinePlayer{}
	if socialHubInstance != nil {
		players = socialHubInstance.onlinePlayers()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"count": len(players), "players": players})
}
