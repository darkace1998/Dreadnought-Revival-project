package main

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The converter's rate reaches the client at the ROOT of YA_GetGameConfigData
// (parser 0x142A2EEFA); without it the client's ship-XP increment is 0 and it
// never offers a conversion.
func TestGameConfigSendsTheXPConversionRate(t *testing.T) {
	p := buildMmogGameConfigDataPayload()
	conv := extractNamedMmogObject(t, p, "XpConversion")
	for field, want := range map[string]string{"HardCurrency": "1", "ShipXp": "40", "FreeXp": "40"} {
		if !bytes.Contains(conv, protocol.AppendStringField(nil, field, want)) {
			t.Errorf("XpConversion.%s is not %s", field, want)
		}
	}
	if bytes.Index(p, []byte("\x0cXpConversion")) > bytes.Index(p, []byte("\x06result")) {
		t.Error("XpConversion must be at the root, before result")
	}
}

func convertRequest(entries ...[2]int32) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_ConvertShipXP")
	b, stack = protocol.AppendArrayStart(b, stack, "ShipXps")
	for _, e := range entries {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "ShipID", e[0])
		b = protocol.AppendInt32Field(b, "ShipXp", e[1])
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	return protocol.AppendRootEnd(b)
}

// 1 GP per 40 ship XP -> 40 free XP. The reply says "bought" (the handler's
// success value), carries the new free XP total and what came off each ship.
func TestConvertShipXPChargesGPAndMovesTheXP(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	const ship = 33489262 // a starter, as the client names it (hull loadout id)
	pawn := battleFleetLoadouts(pid, 0)[0].ship.id
	for _, l := range battleFleetLoadouts(pid, 0) {
		if l.precastLoadoutID == ship {
			pawn = l.ship.id
		}
	}
	if _, err := database.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,1000)`, pid, pawn); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=100, free_xp=5 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	read := func() (gp, free, shipXP int32) {
		_ = database.QueryRow(`SELECT premium_currency, free_xp FROM player_state WHERE user_id=?`, pid).Scan(&gp, &free)
		_ = database.QueryRow(`SELECT xp FROM player_ship_xp WHERE user_id=? AND ship_id=?`, pid, pawn).Scan(&shipXP)
		return
	}

	// 410 ship XP = 10 steps of 40 (the 10 left over is not taken).
	reply := buildMmogConvertShipXPPayload(pid, convertRequest([2]int32{ship, 410}))
	if got := protocol.ExtractStringField(reply, "result"); got != "bought" {
		t.Fatalf("result %q: %s", got, reply)
	}
	gp, free, shipXP := read()
	if gp != 90 || free != 405 || shipXP != 600 {
		t.Fatalf("after converting 410: GP %d free %d ship %d; want 90 / 405 / 600", gp, free, shipXP)
	}
	if protocol.ExtractStringField(reply, "FreeXp") != "405" {
		t.Error("reply FreeXp must be the new total (the client assigns it)")
	}
	if !bytes.Contains(reply, append(protocol.AppendStringField(nil, "ShipID", strconv.Itoa(ship)),
		protocol.AppendStringField(nil, "ShipXp", "400")...)) {
		t.Error("reply must list the 400 ship XP taken from the ship (the client subtracts it)")
	}

	// More ship XP than the ship has, or more GP than the player has: refused,
	// nothing changes.
	for _, amount := range []int32{4000, 39} {
		if got := protocol.ExtractStringField(buildMmogConvertShipXPPayload(pid, convertRequest([2]int32{ship, amount})), "result"); got == "bought" {
			t.Errorf("converting %d was accepted", amount)
		}
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=0 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	if got := protocol.ExtractStringField(buildMmogConvertShipXPPayload(pid, convertRequest([2]int32{ship, 40})), "result"); got == "bought" {
		t.Error("converting with 0 GP was accepted")
	}
	if _, free2, ship2 := read(); free2 != 405 || ship2 != 600 {
		t.Errorf("a refused conversion changed something: free %d ship %d", free2, ship2)
	}
}
