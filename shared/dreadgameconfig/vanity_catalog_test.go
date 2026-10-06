package dreadgameconfig

import (
	"strings"
	"testing"
)

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
	// Every named, non-test item is sold (2026-09-28: 1,465 of the 1,494),
	// except captain meshes no captain template lists (2026-10-06: 38 more
	// out -- the male C00a/C00b/C01 heads, the seasonal "_Outfit" variants,
	// the Explorer sets, Outfit_Base_Military, 44 head attachments...).
	if sold < 1400 || sold > 1440 {
		t.Errorf("%d sold, want the named, non-test, wearable items (~1427)", sold)
	}
	// A set piece the templates list is sold; one they do not list is not.
	for name, wantSold := range map[string]bool{"Body_Female_AutumnSet": true, "Body_Female_ExplorerSet": false, "Head_Male_C00a": false} {
		found := false
		for _, v := range items {
			if v.Name == name {
				found = true
				if VanityItemIsSold(v) != wantSold {
					t.Errorf("%s sold=%v, want %v", name, VanityItemIsSold(v), wantSold)
				}
			}
		}
		if !found {
			t.Errorf("%s missing from the cooked dump", name)
		}
	}
	for _, v := range items {
		if VanityItemIsSold(v) && (v.HeadlineKey == "" || strings.HasSuffix(strings.ToUpper(v.Name), "_TEST")) {
			t.Errorf("%s is sold but is a test item or has no name", v.Name)
		}
	}
	// The pieces of the captain the client saved live (2026-09-24) are free.
	// Body_Male_Base_MC01 was one of them, but no template lists it, and owned
	// (2026-10-06) it made a captain-editor suit tab with nothing in it; it is
	// still free, not sold, and not granted (see VanityItemIsPlayerFacing).
	if v, ok := VanityItemByID(872349903); !ok || !VanityItemIsFree(v) || VanityItemIsSold(v) {
		t.Errorf("Body_Male_Base_MC01: known=%v free=%v sold=%v, want free and not sold", ok, ok && VanityItemIsFree(v), ok && VanityItemIsSold(v))
	}
	for _, id := range []int32{855572482 /*Mat_Eyes_Default*/, 872349840 /*Head_Male_B03*/} {
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
