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

func setPremium(t *testing.T, pid string, premium int) {
	t.Helper()
	if _, err := currentMmogPlayerStateDB().Exec(`UPDATE player_state SET premium_currency=? WHERE user_id=?`, premium, pid); err != nil {
		t.Fatalf("set premium: %v", err)
	}
}

func premium(t *testing.T, pid string) int {
	t.Helper()
	var c int
	if err := currentMmogPlayerStateDB().QueryRow(`SELECT premium_currency FROM player_state WHERE user_id=?`, pid).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
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
	if s := byID[vanityPaidEmblem]; s.priceAmount != vanityPrice || s.priceCurrencyID != "SP" || !s.owned || s.itemType != "vanity" {
		t.Errorf("emblem: price=%d %s owned=%v type=%q, want %d SP, owned, vanity", s.priceAmount, s.priceCurrencyID, s.owned, s.itemType, vanityPrice)
	}
	if _, section, _, _ := gatewayMarketCategoryMetadata(byID[vanityPaidEmblem]); section != "Emblems Collection" {
		t.Errorf("emblem store section %q, want Emblems Collection", section)
	}
	// CHANGED 2026-09-28: the "free" defaults cost the same as everything else
	// (at 0 the client never built a purchase for them).
	if s, ok := byID[vanityFreeEyes]; !ok || s.priceAmount != vanityPrice || s.priceCurrencyID != "SP" || s.owned {
		t.Errorf("default eyes: listed=%v price=%d %s owned=%v, want listed at %d SP, not yet owned", ok, s.priceAmount, s.priceCurrencyID, s.owned, vanityPrice)
	}
	if _, ok := byID[vanityTestBody]; ok {
		t.Error("a Test-folder item is listed")
	}
}

func TestBuyingCosmetics(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 25000)
	setPremium(t, pid, 150)

	// Cosmetics are charged in premium currency; credits stay untouched.
	reply := string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityPaidEmblem)))
	if countWireStringField(reply, "result", "bought") != 1 || premium(t, pid) != 150-vanityPrice || credits(t, pid) != 25000 {
		t.Fatalf("paid emblem: reply %q, premium %d, credits %d, want bought for %d premium", reply, premium(t, pid), credits(t, pid), vanityPrice)
	}
	// The reply must name the premium wallet as the client spells it, or the
	// client rejects it and hangs on its processing screen.
	if countWireStringField(reply, "currency", "SP_regular") != 1 {
		t.Errorf("paid emblem reply does not name the SP_regular wallet: %q", reply)
	}
	var itemType string
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT item_type FROM player_purchases WHERE user_id=? AND item_id=?`, pid, vanityPaidEmblem).Scan(&itemType)
	if itemType != "vanity" {
		t.Errorf("recorded as %q, want vanity (it used to fall through to ship)", itemType)
	}

	// 50 premium left: not enough, even with plenty of credits.
	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityFreeEyes)))
	if !strings.Contains(reply, "insufficient premium currency") || premium(t, pid) != 150-vanityPrice || credits(t, pid) != 25000 {
		t.Fatalf("short of premium: reply %q, premium %d, credits %d, want refused and nothing charged", reply, premium(t, pid), credits(t, pid))
	}

	setPremium(t, pid, vanityPrice)
	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityFreeEyes)))
	if countWireStringField(reply, "result", "bought") != 1 || premium(t, pid) != 0 {
		t.Fatalf("default eyes: reply %q, premium %d, want bought for %d", reply, premium(t, pid), vanityPrice)
	}

	setPremium(t, pid, 1000)
	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(vanityTestBody)))
	if !strings.Contains(reply, "not for sale") || premium(t, pid) != 1000 {
		t.Fatalf("test item: reply %q, premium %d, want refused and nothing charged", reply, premium(t, pid))
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

// Audit 2026-09-26: a client-sent quantity multiplied the price in int32, so
// quantity 85901 on a 25000-credit hull wrapped to a negative price and GAVE
// the player ~2.1 billion credits. Quantity is now always 1.
func TestPurchaseQuantityCannotOverflowThePrice(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	const hull int32 = 33489198 // a 25000-credit hull the new player does not own
	price := purchasePriceForItem(hull)
	if price <= 0 {
		t.Fatalf("hull %d has no price", hull)
	}
	setCredits(t, pid, int(price))
	req := protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(hull)))
	req = protocol.AppendInt32Field(req, "quantity", int32(2147483647/price)+2)
	reply := string(buildMmogPurchasePayload("YA_PurchaseItem", pid, req))
	if got := credits(t, pid); got != 0 {
		t.Fatalf("after buying one hull with a huge quantity: %d credits, want exactly 0 (reply %q)", got, reply)
	}
	var paid int
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT price_paid FROM player_purchases WHERE user_id=? AND item_id=?`, pid, hull).Scan(&paid)
	if paid != int(price) {
		t.Errorf("recorded price %d, want %d", paid, price)
	}
}

// The store reads a price's currency_id through the client's name mapper
// (0x2A618C0): only "SP_regular" lands in SPPrice. "GP" made every cosmetic a
// real-money offer and the store showed 0.
func TestCosmeticOfferIsPricedInTheClientsPremiumCurrency(t *testing.T) {
	var seed gatewayCatalogEntitySeed
	for _, s := range vanityCatalogSeeds(nil) {
		if s.itemID == vanityPaidEmblem {
			seed = s
		}
	}
	entity := gatewayMarketEntity(seed, true)
	prices, _ := entity["prices"].([]any)
	if len(prices) != 1 {
		t.Fatalf("%d price entries, want 1", len(prices))
	}
	price := prices[0].(map[string]any)
	if price["currency_id"] != "SP_regular" || price["amount"] != strconv.Itoa(vanityPrice) {
		t.Errorf("price entry currency_id=%v amount=%v, want SP_regular %d", price["currency_id"], price["amount"], vanityPrice)
	}
	if entity["SPCurrency"] != "SP_regular" || entity["SPPrice"] != vanityPrice || entity["CRPrice"] != 0 {
		t.Errorf("offer SPCurrency=%v SPPrice=%v CRPrice=%v, want SP_regular %d 0", entity["SPCurrency"], entity["SPPrice"], entity["CRPrice"], vanityPrice)
	}
}
