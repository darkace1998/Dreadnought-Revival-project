package main

import (
	"strconv"
	"strings"
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// A large account's owned list does not fit YA_PlayerGet; whatever is cut
// must reach the client in YA_PushInventory, whole and within the frame
// budget. Cosmetics go last in the list, so they are what was lost: bought
// paints looked unowned in the hangar (operator, 2026-10-01).
func TestOwnedItemsCutFromPlayerGetArriveInThePush(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	var bought []int32
	for _, v := range dreadconfig.VanityItems() {
		if c := v.Category(); c >= 20 && c <= 24 && len(bought) < 560 {
			bought = append(bought, v.ItemID)
		}
	}
	buildMmogPlayerGetPayload(pid) // creates the player row
	for _, id := range bought {
		if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'vanity',350,'gp')`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	playerGet := string(buildMmogPlayerGetPayload(pid))
	if _, cut := inventoryNeedsPush.Load(pid); !cut {
		t.Fatal("560 cosmetics fit YA_PlayerGet; this test no longer exercises the cut")
	}
	push := buildMmogPushInventoryPayload(pid)
	if len(push) > playerDataFrameBudget {
		t.Fatalf("YA_PushInventory is %d bytes, over the %d budget", len(push), playerDataFrameBudget)
	}
	missingInGet, missingInPush := 0, 0
	for _, id := range bought {
		s := strconv.Itoa(int(id))
		if countWireStringField(playerGet, "ItemID", s) == 0 {
			missingInGet++
		}
		if countWireStringField(string(push), "ItemID", s) == 0 {
			missingInPush++
		}
	}
	if missingInGet == 0 {
		t.Fatal("nothing was cut from YA_PlayerGet")
	}
	if missingInPush != 0 {
		t.Errorf("%d of %d bought cosmetics are missing from YA_PushInventory", missingInPush, len(bought))
	}
	if !strings.Contains(string(push), "YA_PushInventory") || !strings.Contains(string(push), "inventory") {
		t.Error("push is not a YA_PushInventory with a root inventory")
	}
}

// A hero ship wears its own appearance, not its hull line's default, even
// when a base appearance was saved for it.
func TestHeroShipsWearTheirOwnAppearance(t *testing.T) {
	for _, h := range heroShipLoadouts {
		if h.name != "Hermes" {
			continue
		}
		lo := mmogShipLoadoutSeed{precastLoadoutID: h.loadoutID,
			savedDisplayInfo: dreadconfig.DefaultShipDisplayInfo(h.hullLine, h.manufacturer)}
		got := lo.displayInfo()
		a, _ := dreadconfig.HeroShipAppearance(h.loadoutID)
		for _, id := range a.Items() {
			if !strings.Contains(got, strconv.Itoa(int(id))) {
				t.Errorf("Hermes display info %q lacks its own part %d", got, id)
			}
		}
		if !allowAppearance("enforce", map[int32]bool{}, "p", h.loadoutID, got) {
			t.Error("a hero's own appearance is refused as unowned")
		}
		return
	}
	t.Fatal("no Hermes in the hero roster")
}
