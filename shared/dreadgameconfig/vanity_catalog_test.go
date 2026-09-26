package dreadgameconfig

import "testing"

func TestVanityCatalogFromTheClientsAssets(t *testing.T) {
	items := VanityItems()
	if len(items) < 1400 {
		t.Fatalf("%d vanity items, the cooked dump has 1494", len(items))
	}
	sold, free := 0, 0
	for _, v := range items {
		if VanityItemIsSold(v) {
			sold++
			if VanityItemIsFree(v) {
				free++
			}
		}
	}
	t.Logf("%d cosmetics, %d sold, %d of them free", len(items), sold, free)
	// The pieces of the captain the client saved live (2026-09-24) are free.
	for _, id := range []int32{855572482 /*Mat_Eyes_Default*/, 872349840 /*Head_Male_B03*/, 872349903 /*Body_Male_Base_MC01*/} {
		v, ok := VanityItemByID(id)
		if !ok || !VanityItemIsFree(v) || !VanityItemIsSold(v) {
			t.Errorf("item %d (%s): known=%v free=%v sold=%v", id, v.Name, ok, ok && VanityItemIsFree(v), ok && VanityItemIsSold(v))
		}
	}
	// A plain emblem is sold, not free.
	if v, ok := VanityItemByID(369033217); !ok || VanityItemIsFree(v) || !VanityItemIsSold(v) {
		t.Errorf("VAN_EMB_Bear should be sold and not free")
	}
	// NPC heads and the gender items have no player-facing name: not sold.
	for _, id := range []int32{872349768 /*Head_ChiefOfficer*/, 889126914 /*CH_Gender_Male*/} {
		if v, ok := VanityItemByID(id); ok && VanityItemIsSold(v) {
			t.Errorf("%s is sold but has no player-facing name", v.Name)
		}
	}
	if v, ok := VanityItemByID(369033217); ok && v.HeadlineKey == "" {
		t.Error("VAN_EMB_Bear has no headline localization key")
	}
}
