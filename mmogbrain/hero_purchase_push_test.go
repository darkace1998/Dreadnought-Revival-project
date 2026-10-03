package main

import (
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// A bought hero reaches the client at once: the purchase queues a YA_ClaimItem
// push carrying the new loadout and a YA_FleetUpdate. Before, it appeared in
// neither the owned ships nor the fleet roster until a relog (live
// 2026-10-03). The request is the live shape: the store names the OFFER
// ("999" + the hero's id), with no ItemID.
func TestBoughtHeroIsPushedToTheClient(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=100000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	squadHubInstance.connected(pid) // pushes go only to players online
	t.Cleanup(func() { squadHubInstance.disconnected(pid) })
	_ = squadHubInstance.drainPushes(pid)

	const hero int32 = 67043390 // XA-5
	req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
	req = append(req, protocol.AppendStringField(nil, "offer", "99967043390")...)
	req = append(req, protocol.AppendStringField(nil, "currency", "SP_regular")...)
	reply := buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req))
	if !countWireStringContains(reply, "bought") {
		t.Fatalf("purchase not bought: %q", reply)
	}

	var claim, fleets bool
	for _, p := range squadHubInstance.drainPushes(pid) {
		switch protocol.ExtractStringField(p, "RT") {
		case "YA_ClaimItem":
			claim = protocol.ExtractStringField(p, "ItemID") == "67043390"
		case "YA_FleetUpdate":
			fleets = true
		}
	}
	if !claim || !fleets {
		t.Errorf("pushes after buying hero %d: claim=%v fleet update=%v, want both", hero, claim, fleets)
	}
}

func countWireStringContains(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
