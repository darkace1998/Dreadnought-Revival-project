package main

import (
	"bytes"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// A module's prerequisite is its hull, in the tree the client reads and on
// the server. Modules carried none, so every module of every ship was
// researchable with free XP ("u can research modules of ships u dont own",
// operator 2026-09-29).
func TestModulesRequireTheirHull(t *testing.T) {
	for _, hull := range baseShipLoadouts {
		for _, item := range techTreeModuleItems(hull, 0) {
			if len(item.prereq) != 1 || item.prereq[0] != hull.loadoutID {
				t.Fatalf("%s module %d: prereq %v, want its hull %d", hull.name, item.id, item.prereq, hull.loadoutID)
			}
		}
	}
}

func TestModuleResearchRefusedWithoutTheHull(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	owned := map[int32]bool{}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		owned[l.precastLoadoutID] = true
	}
	researched := func(item int32) bool {
		for _, id := range researchedOrOwnedItemIDs(pid) {
			if id == item {
				return true
			}
		}
		return false
	}
	research := func(item int32) {
		req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(item)))...)
		req = append(req, protocol.AppendStringField(nil, "FreeXp", "5000")...)
		if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
			t.Fatal(err)
		}
	}

	var foreign, own int32
	for _, hull := range baseShipLoadouts {
		items := techTreeModuleItems(hull, 0)
		if len(items) == 0 {
			continue
		}
		if !owned[hull.loadoutID] && foreign == 0 {
			foreign = items[0].id
		}
		if owned[hull.loadoutID] && own == 0 {
			own = items[0].id
		}
	}
	if foreign == 0 || own == 0 {
		t.Fatal("need a module of an unowned hull and one of an owned hull")
	}
	research(foreign)
	if researched(foreign) {
		t.Error("a module of an unowned, unresearched hull was researched")
	}
	research(own)
	if !researched(own) {
		t.Error("a module of an owned hull could not be researched")
	}
}

// grantModuleHull records the module's hull as researched for pid, so a test
// that researches the module meets its prerequisite (the hull).
func grantModuleHull(t *testing.T, database interface {
	Exec(string, ...any) (sql.Result, error)
}, pid string, module int32) {
	t.Helper()
	hull, ok := researchHullLoadout(module)
	if !ok {
		t.Fatalf("module %d has no hull", module)
	}
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency,research_xp) VALUES(?,?,'ship',0,'freexp',0)`, pid, hull); err != nil {
		t.Fatalf("grant hull %d: %v", hull, err)
	}
}

// ...and the prerequisite reaches the wire: module entries are written by a
// minimal writer (appendMmogTechTreeModuleItem) that used to drop Prereq, so
// setting it on the item alone changed nothing the client received.
//
// Off by default since it crashed the client; runs with the switch on.
func TestModulePrereqsAreSent(t *testing.T) {
	old := techTreeModulePrereq
	techTreeModulePrereq = true
	t.Cleanup(func() { techTreeModulePrereq = old })
	modules := 0
	for _, hull := range baseShipLoadouts {
		modules += len(techTreeModuleItems(hull, 0))
	}
	doc := string(inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))
	withEntry := len(regexp.MustCompile(`\x06Prereq\x0d.{4}\x00\x09`).FindAllStringIndex(doc, -1))
	if withEntry < modules {
		t.Errorf("%d tech tree entries carry a prerequisite, want at least the %d modules", withEntry, modules)
	}
}

// NumTechTreeItemsRequired is how many of the PARENT ship's modules must be
// bought before a hull can be researched ("PURCHASE MODULES TO UNLOCK HIGHER
// TIER SHIPS"). Sent as len(prereq) until 2026-09-30, then briefly as 0 for
// every entry, which switched the original mechanic off. Every hull with a
// parent requires techTreeShipUnlockModules (capped at what the parent
// offers); roots, heroes and modules require nothing.
func TestHullsRequireTheirParentsModules(t *testing.T) {
	items := techTreeBaseItems()
	modulesOf := map[int32]int32{}
	for _, item := range items {
		if item.module {
			modulesOf[item.classID]++
		}
	}
	gated := 0
	for _, item := range items {
		switch {
		case item.module || len(item.prereq) == 0:
			if item.techItemsRequired != 0 {
				t.Errorf("item %d requires %d, want 0", item.id, item.techItemsRequired)
			}
		default:
			want := min(techTreeShipUnlockModules, modulesOf[item.prereq[0]])
			if want == 0 || item.techItemsRequired != want {
				t.Errorf("hull %d requires %d of parent %d's %d modules, want %d (and >0)",
					item.id, item.techItemsRequired, item.prereq[0], modulesOf[item.prereq[0]], want)
			}
			gated++
		}
	}
	if gated == 0 {
		t.Fatal("no hull is gated on its parent's modules")
	}
	// ...and it reaches the wire, where the client reads it.
	doc := string(inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))
	want := string(protocol.AppendStringField(nil, "NumTechTreeItemsRequired", strconv.Itoa(techTreeShipUnlockModules)))
	if n := strings.Count(doc, want); n == 0 {
		t.Errorf("no tech tree entry carries NumTechTreeItemsRequired=%d", techTreeShipUnlockModules)
	}
}

// grantParentModulesBought gives pid bought (credit) rows for the modules of
// hull's parent that the unlock gate requires, so a test researching the hull
// is past it.
func grantParentModulesBought(t *testing.T, database interface {
	Exec(string, ...any) (sql.Result, error)
}, pid string, hull int32) {
	t.Helper()
	var parent, need int32
	items := techTreeBaseItems()
	for _, item := range items {
		if !item.module && item.id == hull && len(item.prereq) > 0 {
			parent, need = item.prereq[0], item.techItemsRequired
		}
	}
	for _, item := range items {
		if need == 0 {
			break
		}
		if item.module && item.classID == parent {
			if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'module',1,'CR')`, pid, item.id); err != nil {
				t.Fatal(err)
			}
			need--
		}
	}
}

// The server applies the same gate on research, counting what the client
// counts: BOUGHT modules of the parent. Researched-only ones do not count.
func TestHullResearchNeedsTheParentsModulesBought(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	const hull = 33489267
	have, need, parent, short := hullUnlockShortfall(pid, hull)
	if !short || have != 0 || need != techTreeShipUnlockModules {
		t.Fatalf("fresh player: have %d need %d short %v", have, need, short)
	}
	var modules []int32
	for _, item := range techTreeBaseItems() {
		if item.module && item.classID == parent {
			modules = append(modules, item.id)
		}
	}
	researchedOnly := func(id int32) {
		if _, err := database.Exec(`INSERT OR REPLACE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'module',1000,'freexp')`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	bought := func(id int32) {
		if _, err := database.Exec(`INSERT OR REPLACE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'module',1,'CR')`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range modules[:need] {
		researchedOnly(id)
	}
	if _, _, _, short := hullUnlockShortfall(pid, hull); !short {
		t.Fatal("researched-but-not-bought modules opened the gate")
	}
	for _, id := range modules[:need-1] {
		bought(id)
	}
	research := func() bool {
		req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(hull))...)
		req = append(req, protocol.AppendStringField(nil, "FreeXp", "2500")...)
		if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
			t.Fatal(err)
		}
		for _, id := range researchedOrOwnedItemIDs(pid) {
			if id == hull {
				return true
			}
		}
		return false
	}
	if research() {
		t.Fatalf("hull researched with %d of %d parent modules bought", need-1, need)
	}
	bought(modules[need-1])
	if !research() {
		t.Fatalf("hull not researched with all %d parent modules bought", need)
	}
}

// Research must cover the tree's cost. The request's XP was taken at face
// value, so an item could be researched for 0.
func TestResearchMustCoverTheTreeCost(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	const hull = 33489267
	grantParentModulesBought(t, database, pid, hull)
	cost, ok := techTreeResearchCost(hull)
	if !ok || cost <= 0 {
		t.Fatalf("hull %d has no cost (%d, %v)", hull, cost, ok)
	}
	offer := func(xp int32) bool {
		req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(hull))...)
		req = append(req, protocol.AppendStringField(nil, "FreeXp", strconv.Itoa(int(xp)))...)
		if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
			t.Fatal(err)
		}
		for _, id := range researchedOrOwnedItemIDs(pid) {
			if id == hull {
				return true
			}
		}
		return false
	}
	if offer(0) || offer(cost-1) {
		t.Fatal("research accepted below the tree cost")
	}
	if !offer(cost) {
		t.Fatal("research refused at exactly the tree cost")
	}
}

// Ships and money. A tech-tree hull is claimed through research, never sold:
// a credit purchase used to take the credits and grant no ship. A hero ship is
// sold for GP and must arrive as an owned ship (a loadout row).
func TestShipPurchasesFollowTheOriginalRules(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=1000000, premium_currency=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	balances := func() (credits, gp int32) {
		if err := database.QueryRow(`SELECT soft_currency, premium_currency FROM player_state WHERE user_id=?`, pid).Scan(&credits, &gp); err != nil {
			t.Fatal(err)
		}
		return
	}
	buy := func(id int32) string {
		req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(id)))...)
		return protocol.ExtractStringField(buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req)), "result")
	}
	granted := func(id int32) bool {
		var n int
		if err := database.QueryRow(`SELECT COUNT(*) FROM player_ship_loadouts WHERE user_id=? AND precast_loadout_id=?`, pid, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}

	const hull = 33489265 // a tier-2 base hull
	if got := buy(hull); got != "failed" {
		t.Errorf("credit purchase of tech-tree hull: result %q, want failed", got)
	}
	if c, g := balances(); c != 1000000 || g != 1000000 {
		t.Errorf("refused hull purchase charged: credits %d, GP %d", c, g)
	}

	if len(heroShipLoadouts) == 0 {
		t.Fatal("no hero ships")
	}
	hero := heroShipLoadouts[0].loadoutID
	if got := buy(hero); got != "bought" {
		t.Fatalf("hero purchase: result %q, want bought", got)
	}
	if !granted(hero) {
		t.Error("bought hero ship was not granted (no loadout row)")
	}
	if c, g := balances(); c != 1000000 || g >= 1000000 {
		t.Errorf("hero must cost GP, not credits: credits %d, GP %d", c, g)
	}
}

// A tier-2 hull's prerequisite is a starter hull, and the client accepts a
// prerequisite only when it is in PurchasesData or ProgressionData
// (GetTechTreeItemState 0x543890 -> 0x548990 / 0x547DD0). Starters are owned
// through loadout rows and have no purchase row, so they must be listed as
// owned or every tier-2 reads "Requirements Not Met" (operator 2026-09-30).
func TestOwnedStarterHullsAreInPurchasesData(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	owned := map[int32]bool{}
	for _, id := range clientOwnedItemIDs(pid) {
		owned[id] = true
	}
	loadouts := ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid)
	if len(loadouts) == 0 {
		t.Fatal("a new player owns no ships")
	}
	for _, l := range loadouts {
		if !owned[l.precastLoadoutID] {
			t.Errorf("owned hull %d is not in PurchasesData", l.precastLoadoutID)
		}
	}
	// ...and so every tier-2 whose parent is a starter has its parent met.
	for _, item := range techTreeBaseItems() {
		if item.module || item.tier != 2 || len(item.prereq) == 0 {
			continue
		}
		if p := item.prereq[0]; (p>>24)&0xff == 1 && !owned[p] {
			for _, l := range loadouts {
				if l.precastLoadoutID == p {
					t.Errorf("tier-2 %d: its owned parent %d is not listed", item.id, p)
				}
			}
		}
	}
}

// A successful purchase reply REPLACES the client's owned-item list with
// inventory.Items (0x142A2D245 -> 0x142A6CED0). It was an empty "inventory"
// array, which wiped the list: after the first purchase every other module
// read "Unowned Ship!" (operator 2026-09-30). It must be an OBJECT carrying
// the full owned list, and a failed reply must not carry it at all.
func TestPurchaseReplyKeepsTheOwnedList(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	buy := func(id int32) []byte {
		req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(id)))...)
		return buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req))
	}
	hero := heroShipLoadouts[0].loadoutID
	reply := buy(hero)
	if protocol.ExtractStringField(reply, "result") != "bought" {
		t.Fatalf("setup: hero purchase failed: %q", reply)
	}
	inv := extractNamedMmogContainer(t, reply, "inventory", 0x0c) // an OBJECT
	items := extractNamedMmogArray(t, inv, "Items")
	var starters int
	for _, seed := range starterOwnedInventorySeeds() {
		if bytes.Contains(items, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(seed.itemID)))) {
			starters++
		}
	}
	if starters == 0 || starters != len(starterOwnedInventorySeeds()) {
		t.Errorf("inventory.Items carries %d of %d starter items; the reply replaces the client's list", starters, len(starterOwnedInventorySeeds()))
	}
	if !bytes.Contains(items, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(hero)))) {
		t.Error("inventory.Items does not carry the item just bought")
	}
	// A refused purchase leaves the list alone.
	if failed := buy(33489265); bytes.Contains(failed, []byte("\x09inventory")) {
		t.Error("a failed purchase reply carries inventory, which would replace the owned list")
	}
}

// A ship claimed by research reaches the client in-session: the YA_ClaimItem
// handler (0x142A38B10) adds result.addedLoadouts to the loadout list and
// fires the loadout-added event. Without it a researched ship appeared only
// after a restart (operator 2026-09-30). No "inventory": its presence would
// replace the client's owned-item list.
func TestResearchedShipIsPushedToTheClient(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	const hull = 33489267
	grantParentModulesBought(t, database, pid, hull)
	req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
	req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(hull))...)
	req = append(req, protocol.AppendStringField(nil, "FreeXp", "2500")...)
	if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
		t.Fatal(err)
	}
	if !takeNewlyClaimedShip(pid, hull) {
		t.Fatal("a researched ship was not marked for the client")
	}
	if takeNewlyClaimedShip(pid, hull) {
		t.Error("the claim mark must be taken once")
	}
	push, ok := buildMmogShipClaimPush(pid, hull)
	if !ok {
		t.Fatal("no claim push for the granted ship")
	}
	added := extractNamedMmogArray(t, push, "addedLoadouts")
	if !bytes.Contains(added, protocol.AppendInt32Field(nil, "precastLoadout", hull)) {
		t.Error("addedLoadouts does not carry the claimed ship")
	}
	if !bytes.Contains(push, protocol.AppendStringField(nil, fieldStatus, "succeeded")) {
		t.Error(`result.status must be "succeeded"`)
	}
	if bytes.Contains(push, []byte("\x09inventory")) {
		t.Error("the claim push must not carry inventory: it would replace the owned-item list")
	}
	// A module grants no ship and is never marked.
	if _, ok := buildMmogShipClaimPush(pid, 68026413); ok {
		t.Error("a module produced a ship claim push")
	}
}
