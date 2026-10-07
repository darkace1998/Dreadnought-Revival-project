package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Fixtures (asset names from data/vanity/VanityItems_cooked.jsonl).
const (
	vanityKoreHull     int32 = 352256122 // VAN_H_SniperH_Kore_Hull_DA: Onager's own hull part
	vanityDreadMPatter int32 = 402587698 // VAN_PTN_DreadM_Deathnote_DA: a DreadnoughtMedium pattern
	vanityFigureheadDL int32 = 352256504 // VAN_FH_DreadL_Gargoyle_DA: a DreadnoughtLight figurehead
)

// Which ship classes a ship cosmetic fits, from its asset: the hull line in
// its folder, a figurehead's size in its name, and every ship for the shared
// emblems, paints and decals.
func TestShipVanityClasses(t *testing.T) {
	for _, c := range []struct {
		id   int32
		want []int32
	}{
		{vanityKoreHull, []int32{eyShipClassByKey["SniperHeavy"]}},
		{vanityDreadMPatter, []int32{eyShipClassByKey["DreadnoughtMedium"]}},
		{vanityFigureheadDL, []int32{eyShipClassByKey["DreadnoughtLight"]}},
		{vanityPaidEmblem, nil},
	} {
		v, ok := dreadconfig.VanityItemByID(c.id)
		if !ok {
			t.Fatalf("fixture %d is not a cosmetic", c.id)
		}
		got := shipVanityClasses(v)
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Errorf("%s: classes %v, want %v", v.Name, got, c.want)
		}
	}
	// Every hull-specific one resolves: of the ship cosmetics only the
	// emblems, paints, decals, shared patterns and the NPC Titan's three
	// parts fit "every ship".
	for _, v := range dreadconfig.VanityItems() {
		if v.Category() == 20 && shipVanityClasses(v) == nil && !strings.Contains(v.File, "/Titan/") {
			t.Errorf("mesh part %s fits no class", v.Name)
		}
	}
}

// A new player owns the free cosmetics -- in the forms the customizer checks:
// a captain one in PurchasesData, a ship one in Items as the per-ship id of
// each class it fits. The 2026-10-06 audit found none of it: default eyes were
// sold for 100 GP and no cosmetic was ever owned in the customizer.
func TestNewPlayerOwnsTheFreeCosmetics(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)

	purchases := string(buildMmogPlayerPurchasesPayloadForPlayer(pid))
	if !strings.Contains(purchases, strconv.Itoa(int(vanityFreeEyes))) {
		t.Error("free default eyes are not in PurchasesData")
	}

	items := string(buildMmogPlayerGetPayload(pid))
	classes := ownedShipClasses(pid)
	if len(classes) == 0 {
		t.Fatal("the starter account owns no ship class")
	}
	defaults := dreadconfig.AllDefaultShipVanityItemIDs()
	checked := 0
	for id := range defaults {
		v, ok := dreadconfig.VanityItemByID(id)
		if !ok {
			continue
		}
		fits := shipVanityClasses(v)
		if fits == nil {
			fits = classes
		}
		for _, c := range fits {
			if countWireStringField(items, "ItemID", strconv.Itoa(int(shipVanityClientID(id, c)))) != 1 {
				t.Errorf("default %s for class %d not owned in its per-ship form", v.Name, c)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no default ship cosmetic checked")
	}
}

// A ship's own HULL does not come with the ship as an owned item, in any
// form, and is not for sale: the client owns hull parts per hull line, so it
// would be selectable on every ship of the line (CHANGED 2026-10-07, operator
// report: other makers' hulls on the Jutland). Onager is built from the Kore
// parts (class SniperHeavy).
func TestOwnedShipBringsItsLookPerShip(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := grantUnlockedShipLoadout(tx, pid, 33489292); err != nil { // Onager
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	perShip := shipVanityClientID(vanityKoreHull, eyShipClassByKey["SniperHeavy"])
	items := string(buildMmogPlayerGetPayload(pid))
	for _, id := range []int32{vanityKoreHull, perShip} {
		if countWireStringField(items, "ItemID", strconv.Itoa(int(id))) != 0 {
			t.Errorf("Onager's own hull part %d is owned: selectable on every SniperHeavy", id)
		}
		if _, sold, _ := vanityOffer(id); sold {
			t.Errorf("Onager's own hull part %d is for sale", id)
		}
	}
	// Its line's free default hull stays owned.
	for _, v := range dreadconfig.VanityItems() {
		if v.Category() == 20 && dreadconfig.VanityItemIsFree(v) && strings.Contains(v.Name, "SniperH_") {
			if countWireStringField(items, "ItemID", strconv.Itoa(int(shipVanityClientID(v.ItemID, eyShipClassByKey["SniperHeavy"])))) != 1 {
				t.Errorf("free default %s not owned for SniperHeavy", v.Name)
			}
			return
		}
	}
	t.Error("no free SniperHeavy default hull part found")
}

// A paint (fits every ship) bought once is owned on every class the player
// has a ship of; buying it again, in either form, is refused for free.
func TestBoughtPaintIsOwnedOnEveryShipClass(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	setPremium(t, pid, 1000)
	var paint int32
	for _, v := range dreadconfig.VanityItems() {
		if _, sold, _ := vanityOffer(v.ItemID); v.Category() == 22 && sold && !dreadconfig.VanityItemIsFree(v) {
			paint = v.ItemID
			break
		}
	}
	reply := string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(paint)))
	if countWireStringField(reply, "result", "bought") != 1 || premium(t, pid) != 1000-coatingPrice {
		t.Fatalf("paint %d: reply %q premium %d", paint, reply, premium(t, pid))
	}
	items := string(buildMmogPlayerGetPayload(pid))
	for _, c := range ownedShipClasses(pid) {
		if countWireStringField(items, "ItemID", strconv.Itoa(int(shipVanityClientID(paint, c)))) != 1 {
			t.Errorf("bought paint not owned on class %d", c)
		}
	}
	// Again, as the customizer names it (per-ship form): already owned.
	c := ownedShipClasses(pid)[0]
	reply = string(buildMmogPurchasePayload("YA_PurchaseItem", pid, vanityPurchaseRequest(shipVanityClientID(paint, c))))
	if !strings.Contains(reply, "item already owned") || premium(t, pid) != 1000-coatingPrice {
		t.Errorf("rebuying in per-ship form: reply %q premium %d, want refused, nothing charged", reply, premium(t, pid))
	}
}

// A hull-specific cosmetic is offered as its per-ship id -- what the
// customizer's buy button looks for -- and buying that offer records the
// shared item once.
func TestCustomizerCanBuyHullCosmetic(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	setPremium(t, pid, 1000)
	perShip := shipVanityClientID(vanityDreadMPatter, eyShipClassByKey["DreadnoughtMedium"])
	var sku string
	for _, s := range vanityCatalogSeeds(nil) {
		if s.itemID == perShip {
			sku = s.externalID
		}
		if s.itemID == vanityDreadMPatter {
			t.Error("the hull pattern is still offered in its shared form")
		}
	}
	if sku == "" {
		t.Fatalf("no offer for the per-ship pattern %d", perShip)
	}
	req := protocol.AppendStringField(nil, "offer", sku)
	reply := string(buildMmogPurchasePayload("YA_PurchaseItem", pid, req))
	if countWireStringField(reply, "result", "bought") != 1 {
		t.Fatalf("buying offer %s: %q", sku, reply)
	}
	var stored int32
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT item_id FROM player_purchases WHERE user_id=? AND item_type='vanity'`, pid).Scan(&stored)
	if stored != vanityDreadMPatter {
		t.Errorf("recorded %d, want the shared item %d", stored, vanityDreadMPatter)
	}
}

// Purchases of free cosmetics are refunded once; priced ones are not.
func TestRefundFreeCosmeticPurchases(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	setPremium(t, pid, 0)
	for _, r := range []struct {
		id    int32
		price int
	}{{vanityFreeEyes, 100}, {vanityPaidBody, 100}} {
		if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'vanity',?,'SP_regular')`,
			pid, r.id, r.price); err != nil {
			t.Fatal(err)
		}
	}
	if n := refundFreeCosmeticPurchases(database); n != 1 || premium(t, pid) != 100 {
		t.Fatalf("refunded %d rows, premium %d; want the free eyes only (1 row, 100)", n, premium(t, pid))
	}
	if n := refundFreeCosmeticPurchases(database); n != 0 || premium(t, pid) != 100 {
		t.Errorf("second run refunded %d rows (premium %d); want none", n, premium(t, pid))
	}
	var currency string
	_ = database.QueryRow(`SELECT currency FROM player_purchases WHERE user_id=? AND item_id=?`, pid, vanityPaidBody).Scan(&currency)
	if currency != "SP_regular" {
		t.Errorf("the priced outfit's row became %q", currency)
	}
}

// Captain items: only what a captain template lists is granted, sold or shown
// -- the NPC heads, the male C00a head and Outfit_Base_Military were captain
// editor options with no head or nothing to wear (operator, 2026-10-06).
// What a player bought of them is hidden and refunded.
func TestOnlyWearableCaptainItems(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	setPremium(t, pid, 0)
	const (
		headA02      int32 = 872349774 // Head_Male_A02, "Facial Alteration 8": in the male template
		headC00a     int32 = 872349823 // Head_Male_C00a: in no template
		headChief    int32 = 872349768 // Head_ChiefOfficer: an NPC's
		outfitBase   int32 = 872349717 // Outfit_Base_Military: in no template
		springOutfit int32 = 872349888 // Body_Male_SpringSet_Outfit: in no template
	)
	if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'vanity',100,'SP_regular')`,
		pid, springOutfit); err != nil {
		t.Fatal(err)
	}
	owned := map[int32]bool{}
	for _, id := range ownedVanityItemIDs(pid) {
		owned[id] = true
	}
	if !owned[headA02] {
		t.Error("the template head A02 (free) is not owned")
	}
	for _, id := range []int32{headC00a, headChief, outfitBase, springOutfit} {
		if owned[id] {
			t.Errorf("unwearable captain item %d is owned/shown", id)
		}
		if _, sold, _ := vanityOffer(id); sold {
			t.Errorf("unwearable captain item %d is sold", id)
		}
	}
	if n := refundFreeCosmeticPurchases(database); n != 1 || premium(t, pid) != 100 {
		t.Errorf("refunded %d rows, premium %d; want the bought spring outfit back (1, 100)", n, premium(t, pid))
	}
	// The store names the head as the game does, in every language.
	for _, s := range vanityCatalogSeeds(nil) {
		if s.itemID == headA02 {
			if s.localizedName["en"] != "Facial Alteration 8" || s.localizedDescription["en"] == "" || s.displayName != "Facial Alteration 8" {
				t.Errorf("head A02 offer: name %v description %v display %q", s.localizedName, s.localizedDescription, s.displayName)
			}
			if len(s.localizedName) < 2 {
				t.Errorf("head A02 name in %d language(s) only", len(s.localizedName))
			}
		}
	}
}
