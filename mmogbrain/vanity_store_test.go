package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

const (
	vanityPaidEmblem int32 = 369033217 // VAN_EMB_Bear_DA: public-ready, not a default
	vanityFreeEyes   int32 = 855572482 // Mat_Eyes_Default: a body feature, free
	vanityTestBody   int32 = 872349906 // NewSet_Body_F: developers' Test folder
)

func vanityPurchaseRequest(itemID int32) []byte {
	return protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(itemID)))
}

func setCredits(t *testing.T, pid string, credits int) {
	t.Helper()
	database := currentMmogPlayerStateDB()
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=? WHERE user_id=?`, credits, pid); err != nil {
		t.Fatalf("set credits: %v", err)
	}
}

func credits(t *testing.T, pid string) int {
	t.Helper()
	var c int
	if err := currentMmogPlayerStateDB().QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVanityStoreListsEveryPublicCosmeticAtItsPrice(t *testing.T) {
	seeds := vanityCatalogSeeds(map[int32]struct{}{vanityPaidEmblem: {}})
	if len(seeds) < 1000 {
		t.Fatalf("%d cosmetics listed, want the ~1027 public-ready ones", len(seeds))
	}
	byID := map[int32]gatewayCatalogEntitySeed{}
	for _, s := range seeds {
		byID[s.itemID] = s
		if s.localizationKey == "" {
			t.Errorf("%d (%s) has no name key; the client would show <DNT>[[NotFound]]", s.itemID, s.displayName)
		}
	}
	if s := byID[vanityPaidEmblem]; s.priceAmount != vanityPrice || !s.owned || s.itemType != "vanity" {
		t.Errorf("emblem: price=%d owned=%v type=%q, want %d, owned, vanity", s.priceAmount, s.owned, s.itemType, vanityPrice)
	}
	if _, section, _, _ := gatewayMarketCategoryMetadata(byID[vanityPaidEmblem]); section != "Emblems Collection" {
		t.Errorf("emblem store section %q, want Emblems Collection", section)
	}
	if s, ok := byID[vanityFreeEyes]; !ok || s.priceAmount != 0 || s.owned {
		t.Errorf("default eyes: listed=%v price=%d owned=%v, want listed at 0, not yet owned", ok, s.priceAmount, s.owned)
	}
	if _, ok := byID[vanityTestBody]; ok {
		t.Error("a Test-folder item is listed")
	}
}

func TestBuyingCosmetics(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 25000)

	reply := string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityPaidEmblem)))
	if countWireStringField(reply, "result", "bought") != 1 || credits(t, pid) != 15000 {
		t.Fatalf("paid emblem: reply %q, credits %d, want bought and 15000 left", reply, credits(t, pid))
	}
	var itemType string
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT item_type FROM player_purchases WHERE user_id=? AND item_id=?`, pid, vanityPaidEmblem).Scan(&itemType)
	if itemType != "vanity" {
		t.Errorf("recorded as %q, want vanity (it used to fall through to ship)", itemType)
	}

	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityFreeEyes)))
	if countWireStringField(reply, "result", "bought") != 1 || credits(t, pid) != 15000 {
		t.Fatalf("free eyes: reply %q, credits %d, want bought for 0", reply, credits(t, pid))
	}

	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityTestBody)))
	if !strings.Contains(reply, "not for sale") || credits(t, pid) != 15000 {
		t.Fatalf("test item: reply %q, credits %d, want refused and nothing charged", reply, credits(t, pid))
	}

	// Owned now: in the Items list, and NOT in the tech tree's PurchasesData.
	playerData := string(buildMmogPlayerGetPayload(pid))
	for _, id := range []int32{vanityPaidEmblem, vanityFreeEyes} {
		if countWireStringField(playerData, "ItemID", strconv.Itoa(int(id))) != 1 {
			t.Errorf("cosmetic %d bought but not in the owned-item list", id)
		}
	}
	for _, id := range withoutVanity(clientOwnedItemIDs(pid)) {
		if isVanityItemID(id) {
			t.Errorf("cosmetic %d leaked into PurchasesData", id)
		}
	}
}

// Cosmetics go last and newest first, so a budget cut drops the oldest
// cosmetic rather than a module.
func TestCosmeticsComeLastNewestFirst(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0) // the player row the purchases reference
	database := currentMmogPlayerStateDB()
	for _, row := range []struct {
		id  int32
		typ string
	}{{vanityPaidEmblem, "vanity"}, {83820825, "weapon"}, {vanityFreeEyes, "vanity"}} {
		if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,?,?,?)`,
			pid, row.id, row.typ, 0, "gp"); err != nil {
			t.Fatal(err)
		}
	}
	data := string(buildMmogPlayerGetPayload(pid))
	// Search for the owned-list ENTRY: the eyes id also appears in the
	// captain's display info string earlier in the payload.
	entry := func(id int32) int {
		return strings.Index(data, string(protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(id)))))
	}
	weapon, eyes, emblem := entry(83820825), entry(vanityFreeEyes), entry(vanityPaidEmblem)
	if weapon < 0 || eyes < 0 || emblem < 0 || !(weapon < eyes && eyes < emblem) {
		t.Errorf("order weapon=%d eyes=%d emblem=%d, want weapon < newest cosmetic (eyes) < oldest (emblem)", weapon, eyes, emblem)
	}
}
