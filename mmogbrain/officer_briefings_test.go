package main

import (
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Every ship with officer slots (tier III-V) researches the store's officer
// briefings in its own tech tree, like modules; ships without slots get none.
// The tree carried no briefing at all, so officer slots could never be
// upgraded (operator, 2026-10-01).
func TestShipsWithOfficerSlotsResearchBriefings(t *testing.T) {
	if n := len(officerBriefings()); n != 20 {
		t.Fatalf("%d officer briefings, want the 20 the store sold", n)
	}
	withSlots := 0
	for _, hull := range baseShipLoadouts {
		fitted := map[int32]bool{}
		for _, p := range hull.perks {
			fitted[p] = true
		}
		briefings := 0
		for _, item := range techTreeModuleItems(hull, 0) {
			if !isOfficerBriefing(item.id) {
				continue
			}
			briefings++
			if fitted[item.id] {
				t.Errorf("%s offers its own fitted briefing %d", hull.name, item.id)
			}
			if item.classID != hull.loadoutID || len(item.prereq) != 1 || item.prereq[0] != hull.loadoutID {
				t.Errorf("%s briefing %d is not filed under the ship", hull.name, item.id)
			}
		}
		if hullHasOfficerSlots(hull) {
			withSlots++
			if briefings < 16 {
				t.Errorf("%s (tier %d) has officer slots but %d briefings to research", hull.name, hull.tier, briefings)
			}
		} else if briefings != 0 {
			t.Errorf("%s (tier %d) has no officer slots but %d briefings", hull.name, hull.tier, briefings)
		}
		if hull.tier <= 2 && hullHasOfficerSlots(hull) {
			t.Errorf("%s is tier %d and has officer slots", hull.name, hull.tier)
		}
	}
	if withSlots < 30 {
		t.Errorf("only %d ships have officer slots", withSlots)
	}
}

// Research then buy: a briefing cannot be bought unresearched, researching it
// spends the XP, and buying it charges the price the store shows and makes it
// owned. Every briefing is offered -- hidden, reachable from the tech tree.
func TestBriefingIsResearchedThenBought(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=100000, soft_currency=100000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	briefing := officerBriefings()[0]
	buy := func() string {
		req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
		req = append(req, protocol.AppendStringField(nil, "offer", "999"+strconv.Itoa(int(briefing)))...)
		return protocol.ExtractStringField(buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req)), "result")
	}
	if got := buy(); got == "bought" {
		t.Fatal("an unresearched briefing was bought")
	}
	req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
	req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(briefing)))...)
	req = append(req, protocol.AppendStringField(nil, "FreeXp", strconv.Itoa(int(officerBriefingXPCost)))...)
	if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
		t.Fatal(err)
	}
	var freeXP, credits int32
	_ = database.QueryRow(`SELECT free_xp, soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&freeXP, &credits)
	if freeXP != 100000-officerBriefingXPCost {
		t.Fatalf("research left %d free XP, want %d", freeXP, 100000-officerBriefingXPCost)
	}
	if got := buy(); got != "bought" {
		t.Fatalf("buying the researched briefing: %q", got)
	}
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&credits)
	if credits != 100000-officerBriefingPrice {
		t.Errorf("buying cost %d credits, want %d", 100000-credits, officerBriefingPrice)
	}
	owned := false
	for _, id := range ownedPurchaseItemIDs(pid) {
		owned = owned || id == briefing
	}
	if !owned {
		t.Error("the bought briefing is not owned")
	}

	offers := map[int32]gatewayCatalogEntitySeed{}
	for _, s := range gatewayItemCatalogSeeds(pid) {
		if isOfficerBriefing(s.itemID) {
			offers[s.itemID] = s
		}
	}
	for _, id := range officerBriefings() {
		s, ok := offers[id]
		if !ok {
			name, _ := dreadconfig.AuthoritativeItemName(id)
			t.Errorf("briefing %d (%s) has no offer", id, name)
			continue
		}
		if !s.hidden {
			t.Errorf("briefing %d is sold openly, without research", id)
		}
		if p, _ := purchasePriceForItemChecked(id); p != s.priceAmount {
			t.Errorf("briefing %d: store shows %d, purchase charges %d", id, s.priceAmount, p)
		}
	}
}

// Ship XP for a briefing comes from a ship that has officer slots and can pay.
func TestBriefingShipXPIsChargedToAShipWithOfficerSlots(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	var hull baseShipLoadout
	for _, h := range baseShipLoadouts {
		if h.tier == 3 && hullHasOfficerSlots(h) {
			hull = h
			break
		}
	}
	if err := func() error {
		tx, err := database.Begin()
		if err != nil {
			return err
		}
		if err := grantUnlockedShipLoadout(tx, pid, hull.loadoutID); err != nil {
			return err
		}
		return tx.Commit()
	}(); err != nil {
		t.Fatal(err)
	}
	pawn, _ := dreadconfig.ShipIDForPrecastLoadout(hull.loadoutID)
	if _, err := database.Exec(`INSERT OR REPLACE INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,10000)`, pid, pawn); err != nil {
		t.Fatal(err)
	}
	key, gotPawn, ok := officerResearchShip(pid, officerBriefingXPCost)
	if !ok || key != hull.loadoutID || gotPawn != pawn {
		t.Fatalf("paying ship %d/%d (%v), want %s %d/%d", key, gotPawn, ok, hull.name, hull.loadoutID, pawn)
	}
	if _, _, ok := officerResearchShip(pid, 20000); ok {
		t.Error("a ship that cannot cover the cost was chosen")
	}
}
