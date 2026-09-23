package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// The ship roster is checked against the client's COOKED blueprints -- the
// precast and hero loadout .uasset files the game itself loads -- not against
// the community reference the roster is generated from, and not against
// ItemIDConversionTable, whose names are one build behind.
//
// The cooked files are dumped to JSONL by bpdump (see
// scripts/validate-precast-loadouts.py for the exact commands). Each blueprint
// names its weapons, abilities and officer perks by ASSET PATH; the path is
// resolved to an id through the client's own register, so nothing here is
// transcribed by hand.
//
// This is what found the Feronia bug: the T5 SupportMedium hull shipped with
// every slot zero -- no weapons, no abilities, no officers -- because a blank
// template at the end of the reference was parsed as part of it.

type cookedLoadout struct {
	File       string `json:"file"`
	Name       string `json:"m_name"`
	SystemData struct {
		ItemID int32 `json:"m_itemID"`
		Tier   int32 `json:"m_itemTier"`
	} `json:"m_itemSystemData"`
	Primary      string   `json:"m_primaryWeaponClass"`
	Secondary    string   `json:"m_secondaryWeaponClass"`
	Abilities    []string `json:"m_abilities"`
	OfficerFirst string   `json:"m_officerFirstPerk"`
	OfficerWpn   string   `json:"m_officerWeaponPerk"`
	OfficerNav   string   `json:"m_officerNavigationPerk"`
	OfficerEng   string   `json:"m_officerEngineerPerk"`
}

// There is deliberately no allow-list for hulls without a blueprint. Brutus
// (33489299) used to be kept on one: the register still carries its id, but the
// client has no .uasset for it, so it has been removed from the game and the
// generator now drops it (scripts/gen-base-ship-loadouts.py, load_cooked_ids).
// A server hull with no cooked blueprint is a failure, full stop.

func readCookedLoadouts(t *testing.T, file string) []cookedLoadout {
	t.Helper()
	path := filepath.Join(dreadconfig.LoadoutsDir(), file)
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no cooked dump at %s (%v); regenerate with bpdump --loadouts", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []cookedLoadout
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row cookedLoadout
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		out = append(out, row)
	}
	return out
}

// cookedID resolves "/Game/.../X_BP.X_BP_C" to its register id. An empty path
// is an empty slot (0); an unresolvable one fails the test, because a slot the
// register does not know is a slot the client cannot load.
func cookedID(t *testing.T, where, path string) int32 {
	t.Helper()
	if path == "" {
		return 0
	}
	pkg := strings.SplitN(path, ".", 2)[0]
	item, ok := dreadconfig.ItemByAssetPath(pkg)
	if !ok {
		t.Errorf("%s: cooked path %s is not in ItemIDRegister", where, pkg)
		return -1
	}
	return item.ItemID
}

func (c cookedLoadout) slots(t *testing.T) (primary, secondary int32, abilities, perks [4]int32) {
	where := c.Name
	primary = cookedID(t, where, c.Primary)
	secondary = cookedID(t, where, c.Secondary)
	for i, p := range c.Abilities {
		if i < 4 {
			abilities[i] = cookedID(t, where, p)
		}
	}
	for i, p := range []string{c.OfficerFirst, c.OfficerWpn, c.OfficerNav, c.OfficerEng} {
		perks[i] = cookedID(t, where, p)
	}
	return
}

func TestBaseShipRosterMatchesCookedBlueprints(t *testing.T) {
	cooked := map[int32]cookedLoadout{}
	for _, c := range readCookedLoadouts(t, "PrecastLoadouts_cooked.jsonl") {
		// Player-facing tiered loadouts only. The root of Precast/ also holds
		// 15 tier-less legacy duplicates with different ids, plus Special/, TM/
		// (training match), PVE/ and Tutorial/ variants.
		for tier := 1; tier <= 5; tier++ {
			if strings.Contains(c.File, "/Loadouts/Precast/T"+string(rune('0'+tier))+"/") {
				cooked[c.SystemData.ItemID] = c
			}
		}
	}
	if len(cooked) < 50 {
		t.Fatalf("only %d cooked tiered loadouts; the dump looks incomplete", len(cooked))
	}

	server := map[int32]baseShipLoadout{}
	for _, hull := range baseShipLoadouts {
		server[hull.loadoutID] = hull
	}
	for id, c := range cooked {
		if _, ok := server[id]; !ok {
			t.Errorf("cooked tiered loadout %d %q (T%d) is not in the server roster", id, c.Name, c.SystemData.Tier)
		}
	}
	for id, hull := range server {
		c, ok := cooked[id]
		if !ok {
			t.Errorf("server hull %d %q has no cooked tiered loadout", id, hull.name)
			continue
		}
		primary, secondary, abilities, perks := c.slots(t)
		if hull.tier != c.SystemData.Tier {
			t.Errorf("%s: tier %d, cooked %d", hull.name, hull.tier, c.SystemData.Tier)
		}
		if hull.primary != primary || hull.secondary != secondary {
			t.Errorf("%s: weapons %d/%d, cooked %d/%d", hull.name, hull.primary, hull.secondary, primary, secondary)
		}
		if hull.abilities != abilities {
			t.Errorf("%s: abilities %v, cooked %v", hull.name, hull.abilities, abilities)
		}
		if hull.perks != perks {
			t.Errorf("%s: officer perks %v, cooked %v", hull.name, hull.perks, perks)
		}
	}
}

func TestHeroShipRosterMatchesCookedBlueprints(t *testing.T) {
	cooked := map[int32]cookedLoadout{}
	for _, c := range readCookedLoadouts(t, "HeroLoadouts_cooked.jsonl") {
		cooked[c.SystemData.ItemID] = c
	}
	// 48, not 47: Phoenix is VH_ScoutMedium_Phoenix_Heroloadout_BP (lowercase
	// "l"), which a "*_HeroLoadout_BP" glob silently drops on Linux.
	if len(cooked) != len(heroShipLoadouts) {
		t.Errorf("%d cooked hero loadouts, %d server heroes", len(cooked), len(heroShipLoadouts))
	}
	for _, hero := range heroShipLoadouts {
		c, ok := cooked[hero.loadoutID]
		if !ok {
			t.Errorf("server hero %d %q has no cooked hero loadout", hero.loadoutID, hero.name)
			continue
		}
		primary, secondary, abilities, perks := c.slots(t)
		if hero.name != c.Name {
			t.Errorf("hero %d: named %q, blueprint m_name %q", hero.loadoutID, hero.name, c.Name)
		}
		if hero.tier != c.SystemData.Tier {
			t.Errorf("%s: tier %d, cooked %d", hero.name, hero.tier, c.SystemData.Tier)
		}
		if hero.primary != primary || hero.secondary != secondary {
			t.Errorf("%s: weapons %d/%d, cooked %d/%d", hero.name, hero.primary, hero.secondary, primary, secondary)
		}
		if hero.abilities != abilities {
			t.Errorf("%s: abilities %v, cooked %v", hero.name, hero.abilities, abilities)
		}
		if hero.perks != perks {
			t.Errorf("%s: officer perks %v, cooked %v", hero.name, hero.perks, perks)
		}
		// And the name that actually reaches the wire -- shipDisplayName goes
		// through AuthoritativeItemName, which used to fall back to the
		// conversion table's older names for every hero.
		if got, ok := dreadconfig.AuthoritativeItemName(hero.loadoutID); !ok || got != c.Name {
			t.Errorf("hero %d: authoritative name %q (ok=%v), blueprint %q", hero.loadoutID, got, ok, c.Name)
		}
	}
}

// The starter fleet is built from a DIFFERENT table than the roster
// (StarterInventoryLoadouts). It is what every new player actually flies, so it
// is pinned to the validated roster: if the two ever disagree, one of them has
// drifted from the client.
func TestStarterLoadoutsMatchTheValidatedRoster(t *testing.T) {
	roster := map[int32]baseShipLoadout{}
	for _, hull := range baseShipLoadouts {
		roster[hull.loadoutID] = hull
	}
	checked := 0
	for _, starter := range starterShipLoadouts() {
		hull, ok := roster[starter.precastLoadoutID]
		if !ok {
			continue // development loadouts are appended to the same list
		}
		checked++
		if starter.weaponPrimaryID != hull.primary || starter.weaponSecondaryID != hull.secondary {
			t.Errorf("starter %s: weapons %d/%d, roster %d/%d", hull.name,
				starter.weaponPrimaryID, starter.weaponSecondaryID, hull.primary, hull.secondary)
		}
		if starter.abilityIDs != hull.abilities {
			t.Errorf("starter %s: abilities %v, roster %v", hull.name, starter.abilityIDs, hull.abilities)
		}
		if starter.perkIDs != hull.perks {
			t.Errorf("starter %s: officer perks %v, roster %v", hull.name, starter.perkIDs, hull.perks)
		}
	}
	if checked != 4 {
		t.Errorf("checked %d starter loadouts against the roster, want the 4 starters", checked)
	}
}

// Every weapon, ability and officer perk the server can put in front of a
// player -- each hull's defaults, each hero's, and every tech-tree module
// offered as an unlock -- must be a real, loadable item: in the client's
// register, in the category its slot implies, and present as a cooked .uasset.
//
// Measured 2026-09-22: 577 distinct ids (114 weapons, 434 abilities, 29 officer
// perks), 837 tech-tree module offerings, zero failures.
//
// It also pins that an unlock is offered in the RIGHT slot and at a sane tier,
// which is the intent techTreeSlotUpgrades documents: a module is an
// ALTERNATIVE from a sibling line in the same slot group as something the hull
// equips (group = ship class + slot family, e.g. Assault primary abilities, or
// Assault secondaries across SecShort/SecMid/SecLong), taken at or below the
// hull's own tier. So a perimeter ability cannot surface on the primary-ability
// rail, another class's module cannot surface at all, and a Tier 1 hull cannot
// be offered a Tier 5 module -- the live bug that gate was added for.
//
// (A first draft of this test required modules to be tier variants of the
// equipped item's OWN line. That is wrong: the equipped line is excluded
// entirely and the offers are the SIBLING lines. 16 failures on Dola alone.)
func TestEveryOfferedItemIsARealCookedAsset(t *testing.T) {
	content := os.Getenv("DN_CLIENT_CONTENT")
	if content == "" {
		content = "/root/projects/DreadGame/Content"
	}
	if _, err := os.Stat(content); err != nil {
		t.Skipf("client Content not found at %s; set DN_CLIENT_CONTENT", content)
	}
	techTreeBuildSlotIndex()

	check := func(id int32, wantCategory int32, where string) {
		if id <= 0 {
			return
		}
		item, ok := dreadconfig.ItemByID(id)
		if !ok || item.AssetPath == "" {
			t.Errorf("%s: %d is not in ItemIDRegister", where, id)
			return
		}
		if got := (id >> 24) & 0xff; got != wantCategory {
			t.Errorf("%s: %d is category %d, want %d (%s)", where, id, got, wantCategory, item.AssetPath)
		}
		file := filepath.Join(content, strings.TrimPrefix(item.AssetPath, "/Game/")) + ".uasset"
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%s: %d has no cooked asset at %s", where, id, file)
		}
	}
	slotsOf := func(name string, primary, secondary int32, abilities, perks [4]int32) {
		check(primary, 5, name+" primary")
		check(secondary, 5, name+" secondary")
		for _, a := range abilities {
			check(a, 4, name+" ability")
		}
		for _, p := range perks {
			check(p, 6, name+" officer perk")
		}
	}

	offered := 0
	for _, hull := range baseShipLoadouts {
		slotsOf(hull.name, hull.primary, hull.secondary, hull.abilities, hull.perks)
		groups := map[string]bool{}
		for _, id := range append(append([]int32{hull.primary, hull.secondary}, hull.abilities[:]...), hull.perks[:]...) {
			if key, ok := techTreeSlotOf[id]; ok {
				groups[key.group] = true
			}
		}
		m := shipManufacturerID(baseShipManufacturerByClassSize[hull.hullLine])
		for _, module := range techTreeModuleItems(hull, m) {
			offered++
			cat := (module.id >> 24) & 0xff
			check(module.id, cat, hull.name+" tech-tree module")
			if cat != 4 && cat != 5 && cat != 6 {
				t.Errorf("%s: module %d is category %d, not a weapon/ability/perk", hull.name, module.id, cat)
			}
			item, _ := dreadconfig.ItemByID(module.id)
			key, ok := techTreeSlotOf[module.id]
			if !ok || !groups[key.group] {
				t.Errorf("%s: module %d (%s) is in slot group %q, which nothing the hull equips belongs to",
					hull.name, module.id, item.AssetPath, key.group)
			}
			if tier, ok := techTreeSlotTier[module.id]; ok && tier > hull.tier {
				t.Errorf("%s (T%d): module %d (%s) is tier %d, above the hull",
					hull.name, hull.tier, module.id, item.AssetPath, tier)
			}
		}
	}
	for _, hero := range heroShipLoadouts {
		slotsOf(hero.name, hero.primary, hero.secondary, hero.abilities, hero.perks)
	}
	if offered == 0 {
		t.Fatal("no tech-tree modules were generated; the check proved nothing")
	}
	t.Logf("checked %d tech-tree module offerings", offered)
}

// Every ship in the roster must resolve to a pawn, because an unlock with no
// pawn is silently a no-op: grantUnlockedShipLoadout returns nil, so the player
// is charged, the purchase is recorded, and no ship appears. Before the cooked
// pawn fallback this failed for 63 of 99 ships -- all 15 tier-4 hulls and all
// 48 heroes -- found by provisioning an account with every ship (99 unlocked,
// 36 loadouts created).
//
// Where the old path-pattern lookup does answer, it must agree with the pawn
// the blueprint itself names; a disagreement means one of them is wrong.
func TestEveryRosterShipResolvesToItsBlueprintPawn(t *testing.T) {
	check := func(id int32, name string) {
		pawn, ok := dreadconfig.ShipIDForPrecastLoadout(id)
		if !ok {
			t.Errorf("%s (%d): no ship pawn -- unlocking it would grant nothing", name, id)
			return
		}
		if cooked, ok := dreadconfig.CookedPawnForLoadout(id); ok && cooked != pawn {
			t.Errorf("%s (%d): pawn %d, but its blueprint names %d", name, id, pawn, cooked)
		}
		if _, ok := nativeStarterLoadoutClassName(id); !ok {
			t.Errorf("%s (%d): no native loadout class -- the grant would be skipped", name, id)
		}
	}
	for _, hull := range baseShipLoadouts {
		check(hull.loadoutID, hull.name)
	}
	for _, hero := range heroShipLoadouts {
		check(hero.loadoutID, hero.name)
	}
}

// A tech-tree slot must resolve to ONE asset, chosen by rule rather than by map
// iteration order. 36 of 421 slots have two candidates -- 35 a normal variant
// and its _Hero_BP twin, one a current file and a legacy tier-less copy -- and
// the index used to keep whichever GetAllRegistryEntries (a map) yielded last,
// so the modules offered changed on every restart (578/573/574/577 items across
// four runs). No slot is hero-only, so a hero twin must never be what a base
// hull is offered.
func TestTechTreeSlotsPreferTheNormalCurrentAsset(t *testing.T) {
	for _, hull := range baseShipLoadouts {
		m := shipManufacturerID(baseShipManufacturerByClassSize[hull.hullLine])
		for _, module := range techTreeModuleItems(hull, m) {
			item, _ := dreadconfig.ItemByID(module.id)
			if strings.Contains(item.AssetPath, "_Hero_BP") {
				t.Errorf("%s is offered hero-ship variant %d (%s)", hull.name, module.id, item.AssetPath)
			}
			// No filename-shape assertion here: the "current file over legacy
			// copy" preference only decides between two candidates. Where a
			// slot has one asset it is offered whatever its name -- e.g.
			// AB_AS_Int_Mov_Side_Ability_T5_BP_2 is the only T5 dodge.
		}
	}
}

// An account that owns everything must still be able to log in. YA_PlayerGet
// carries one Items entry per owned item, and with every ship and module owned
// it reached 62,150 bytes -- nearly twice the 32768-byte receive ring -- and the
// client hung on "entering game" with no error. The frame must stay within
// playerDataFrameBudget however much a player owns.
func TestPlayerDataFitsTheRingWhenEverythingIsOwned(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	pid := "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	ships, items := provisionUnlockSet()
	for _, id := range append(ships, items...) {
		if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
			VALUES(?,?,'x',0,'admin')`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(purchasedInventoryItemIDs(pid)); n < 600 {
		t.Fatalf("only %d owned items seeded; the test would prove nothing", n)
	}
	for _, name := range []string{"YA_PlayerGet", "YA_RefreshPlayerProfile"} {
		payload := buildMmogPlayerDataPayload(name, pid)
		t.Logf("%s: %d bytes", name, len(payload))
		// Against the RING, a fixed fact about the client -- not against
		// playerDataFrameBudget, which would move with the thing under test.
		if len(payload) > clientReceiveRingBytes-2048 {
			t.Errorf("%s is %d bytes for a player owning %d items; budget %d, ring 32768",
				name, len(payload), len(ships)+len(items), playerDataFrameBudget)
		}
	}
}

// The tech tree must fit the receive ring for an account that owns every ship,
// with modules on. It hung login once already at 35,023 bytes: the ignored
// plain techTreeRow block grew one row per owned ship. Checked against the
// fixed ring, not techTreeFrameBudget, which moves with the code under test.
func TestTechTreeFitsTheRingWhenEverythingIsOwned(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	pid := "0123456789abcdef0123456789abcdee"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	ships, items := provisionUnlockSet()
	for _, id := range append(ships, items...) {
		if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
			VALUES(?,?,'x',0,'admin')`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	if techTreeNoModules {
		t.Fatal("modules are off; this test is meant to measure the tree WITH them")
	}
	payload := buildMmogTechTreePayload(pid)
	t.Logf("YA_GetTechTree for an everything-owned account: %d bytes", len(payload))
	if len(payload) > clientReceiveRingBytes-2048 {
		t.Errorf("YA_GetTechTree is %d bytes; ring %d", len(payload), clientReceiveRingBytes)
	}
}

// No two ships may share a tech-tree cell (manufacturer, tier, position). Heroes
// used to be numbered from column 0 in each (manufacturer, tier) -- the columns
// the base hull lines occupy -- which was invisible while heroes were dropped by
// the ClassId <= 0 gate and put 23 of 76 cells under two ships the moment they
// were stored. Reported live as ships "missing or overlapping".
func TestTechTreeShipsNeverShareACell(t *testing.T) {
	type cell struct{ manufacturer, tier, position int32 }
	seen := map[cell]int32{}
	ships := 0
	for _, it := range append(techTreeBaseItems(), techTreeHeroItems()...) {
		if it.module {
			continue
		}
		ships++
		c := cell{it.manufacturer, it.tier, it.position}
		if other, ok := seen[c]; ok {
			t.Errorf("ships %d and %d share manufacturer %d tier %d position %d", other, it.id, c.manufacturer, c.tier, c.position)
		}
		seen[c] = it.id
	}
	if ships != len(baseShipLoadouts)+len(heroShipLoadouts) {
		t.Errorf("%d ship nodes, want %d", ships, len(baseShipLoadouts)+len(heroShipLoadouts))
	}
}

// A module the player unlocks must reach the client even when the item list is
// cut. Items went out in purchase order, ship unlocks first, so on an account
// owning every ship the budget cut exactly the newest unlock -- the client then
// offered to unlock the same weapon again and again.
func TestNewestUnlockedModuleSurvivesTheItemBudget(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	pid := "0123456789abcdef0123456789abcded"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	ships, items := provisionUnlockSet()
	// Built like the UnlockAll account: every ship recorded AND granted through
	// the real unlock path, so the ships take their share of the budget and the
	// item list really is cut. (A first draft only recorded the purchases, left
	// plenty of room, and passed with the bug present.)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ships {
		if _, err := tx.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'loadout',0,'admin')`, pid, id); err != nil {
			t.Fatal(err)
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	module := items[len(items)-1]
	if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'weapon',2000,'freexp')`, pid, module); err != nil {
		t.Fatal(err)
	}
	payload := buildMmogPlayerDataPayload("YA_PlayerGet", pid)
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(module)))) {
		t.Fatalf("module %d, unlocked after %d ships, was cut from the item list", module, len(ships))
	}
}
