package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// battleLoadoutHandler serves one player's loadout to the battle-server mod.
//
// Why it exists (verified 2026-09-25 from the exe): the only loadout message a
// client sends a battle server is ServerPlayerClickedShipLoadout(FName id); the
// server's mmog AutoLogin (0x2AABCB0) is a stub in this exe, and its fleet
// manager (UpdatePlayerFleetData, 0x35FDF0) only ever reads the host's OWN
// account. So a host that should spawn a player's custom fit has no way to
// learn it -- the original servers were a separate server build. The mod
// learns whose connection it is from the ?DNPID= option mmogbrain adds to the
// YA_Connect travel address (read back from UNetConnection+0x198 on the host)
// and asks here for the loadout the client picked.
//
// The answer is the same record the client builds its own loadout from
// (YA_PlayerFleets -> 0x34E550 -> FYShipImportLoadoutInfo): precast id, name,
// ship class, display info, 2 weapon slots, 4 ability slots, 4 perk slots --
// positional, zeros included, because the game's record is positional.
//
// GET /battle/loadout?pid=<pid>&id=<loadout id as the client sent it>
// Loopback only: the battle servers run on this host, and nothing here needs a
// secret passed through Wine. Plain "key=value" lines so the mod parses it
// without a JSON library.
func battleLoadoutHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	pid := normalizedPlayerStatePID(r.URL.Query().Get("pid"))
	id := r.URL.Query().Get("id")
	if pid == "" || id == "" {
		http.Error(w, "pid and id are required", http.StatusBadRequest)
		return
	}
	loadout, ok := battleLoadoutFor(pid, id)
	if !ok {
		http.Error(w, "no such loadout for this player", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(formatBattleLoadout(id, pid, loadout)))
}

// battleLoadoutFor finds the player's loadout whose client-facing id (the
// "ID" field of the YA_PlayerFleets entry) is id -- the list the client itself
// received, so both sides resolve the same row.
func battleLoadoutFor(pid, id string) (mmogShipLoadoutSeed, bool) {
	for _, loadout := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		if loadout.entryID() == id {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}

func formatBattleLoadout(id, pid string, loadout mmogShipLoadoutSeed) string {
	ints := func(vs ...int32) string {
		parts := make([]string, len(vs))
		for i, v := range vs {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, ",")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id=%s\n", id)
	fmt.Fprintf(&b, "pid=%s\n", pid)
	fmt.Fprintf(&b, "precast=%d\n", loadout.precastLoadoutID)
	fmt.Fprintf(&b, "name=%s\n", loadout.loadoutName)
	fmt.Fprintf(&b, "class=%d\n", loadoutEYShipClass(loadout))
	fmt.Fprintf(&b, "display=%s\n", loadout.displayInfo())
	fmt.Fprintf(&b, "weapons=%s\n", ints(loadout.weaponPrimaryItemID(), loadout.weaponSecondaryItemID()))
	fmt.Fprintf(&b, "abilities=%s\n", ints(loadout.abilityItemID(0), loadout.abilityItemID(1), loadout.abilityItemID(2), loadout.abilityItemID(3)))
	fmt.Fprintf(&b, "perks=%s\n", ints(loadout.perkItemID(0), loadout.perkItemID(1), loadout.perkItemID(2), loadout.perkItemID(3)))
	return b.String()
}
