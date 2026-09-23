package main

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"

	_ "github.com/mattn/go-sqlite3"
)

// realRemoveFromFleetPayload is the exact body a client sent for
// YA_RemoveFromFleet, captured live on 2026-08-02:
//
//	RT     STRING "YA_RemoveFromFleet"
//	fleet  tag 0x02, 16 raw bytes = the player's own PID as a GUID
//	shipId INT32  33489262        <- the PRECAST LOADOUT id, not a ship id
//
// The bytes are reproduced rather than rebuilt so the test keeps failing if the
// parser stops handling the 0x02 GUID field sitting between RT and shipId.
func realRemoveFromFleetPayload() []byte {
	return []byte{
		0x02, 'R', 'T', 0x09, 0x12, 0x00, 0x00, 0x00,
		'Y', 'A', '_', 'R', 'e', 'm', 'o', 'v', 'e', 'F', 'r', 'o', 'm', 'F', 'l', 'e', 'e', 't',
		0x05, 'f', 'l', 'e', 'e', 't', 0x02,
		0x65, 0x0d, 0xd7, 0x94, 0x76, 0xa1, 0x48, 0x4b,
		0x8a, 0xdc, 0xd0, 0x1a, 0xc2, 0xf1, 0x73, 0x54,
		0x06, 's', 'h', 'i', 'p', 'I', 'd', 0x56, 0x6e, 0x01, 0xff, 0x01,
		0x00, 0x0e, 0x00, 0x00, 0x00, 0x00,
	}
}

func fleetEditTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:fleetedit?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE player_fleets(user_id TEXT, fleet_id INTEGER, active INTEGER)`,
		`CREATE TABLE player_ship_loadouts(user_id TEXT, loadout_id INTEGER, precast_loadout_id INTEGER, ship_id INTEGER, position INTEGER)`,
		`CREATE TABLE player_fleet_loadouts(user_id TEXT, fleet_id INTEGER, position INTEGER, loadout_id INTEGER)`,
		`INSERT INTO player_fleets VALUES('p',1,0),('p',2,1)`,
		`INSERT INTO player_ship_loadouts VALUES('p',33489262,33489262,184483982,0)`,
		`INSERT INTO player_fleet_loadouts VALUES('p',2,0,33489262)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return db
}

// The client's shipId carries a precast loadout id (top byte 1), not a ship id
// (top byte 10). Resolving it against ship_id matched nothing, so every fleet
// edit silently returned nil -- twenty no-op removals in one observed session.
func TestFleetEditResolvesPrecastLoadoutIDFromShipIdField(t *testing.T) {
	db := fleetEditTestDB(t)

	got, err := fleetEditLoadoutID(db, "p", realRemoveFromFleetPayload())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 33489262 {
		t.Fatalf("loadout id = %d, want 33489262 resolved from the shipId field", got)
	}
}

// The request identifies the owner, not which of the three fleets, so an edit
// has to target the fleet the player currently has active -- not fleet 1.
func TestFleetEditTargetsTheActiveFleet(t *testing.T) {
	db := fleetEditTestDB(t)

	if got := fleetEditTargetFleetID(db, "p", realRemoveFromFleetPayload()); got != 2 {
		t.Fatalf("target fleet = %d, want the active fleet 2", got)
	}
}

// A YPawn id (top byte 10) must still resolve through ship_id.
func TestFleetEditStillResolvesAShipID(t *testing.T) {
	db := fleetEditTestDB(t)
	payload := append([]byte(nil), 0x06, 'S', 'h', 'i', 'p', 'I', 'D', 0x56)
	payload = append(payload, 0x8e, 0x00, 0xff, 0x0a) // 184483982
	payload = append(payload, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)

	got, err := fleetEditLoadoutID(db, "p", payload)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 33489262 {
		t.Fatalf("loadout id = %d, want the loadout owning ship 184483982", got)
	}
}

// Seeding runs on every login. Re-inserting the starter ships each time
// silently undid the player's own edits: a session that removed all four kept
// only the one removed after the last seed, and the other three came back.
// Membership must therefore be seeded only when the fleet row is created.
func TestSeedingDoesNotRestoreRemovedFleetShips(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:seedguard?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE player_fleets(user_id TEXT, fleet_id INTEGER, PRIMARY KEY(user_id,fleet_id))`,
		`CREATE TABLE player_fleet_loadouts(user_id TEXT, fleet_id INTEGER, position INTEGER, loadout_id INTEGER,
		   PRIMARY KEY(user_id,fleet_id,position))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	// First login creates the fleet -> membership is seeded.
	res, err := db.Exec(`INSERT OR IGNORE INTO player_fleets(user_id,fleet_id) VALUES('p',1)`)
	if err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatal("first insert should have created the fleet row")
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO player_fleet_loadouts VALUES('p',1,0,33489262)`); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// Player removes it.
	if _, err := db.Exec(`DELETE FROM player_fleet_loadouts WHERE user_id='p' AND fleet_id=1 AND loadout_id=33489262`); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Second login: the fleet already exists, so RowsAffected is 0 and the
	// seeding branch must be skipped.
	res, err = db.Exec(`INSERT OR IGNORE INTO player_fleets(user_id,fleet_id) VALUES('p',1)`)
	if err != nil {
		t.Fatalf("re-insert fleet: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("second insert affected %d rows, want 0 -- the guard relies on this", n)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_fleet_loadouts WHERE user_id='p'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("fleet has %d ships after re-login, want the removal to stick", count)
	}
}

// The client compares `result` ITSELF against "ok" and echoes back `fleet` and
// `shipId`; a nested result object left it logging
// "Failed to Remove ship [0] from fleet [None]. Error: []" even though the
// database change had already succeeded.
func TestFleetMutationResponseCarriesWhatTheClientReads(t *testing.T) {
	payload := buildMmogFleetMutationPayload("YA_RemoveFromFleet", realRemoveFromFleetPayload())

	// result is an OBJECT carrying its own "result": the arm looks the name up
	// twice (0x142a31543 then 0x142a31565). A bare top-level string was tried
	// live and rejected.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "result", "ok")) {
		t.Error(`result object must contain result:"ok"`)
	}
	if bytes.Index(payload, []byte("result")) == bytes.LastIndex(payload, []byte("result")) {
		t.Error("expected result to appear twice: the object and its inner field")
	}
	// The fleet GUID the client sent, echoed back as hex.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "fleet", "650dd79476a1484b8adcd01ac2f17354")) {
		t.Error("fleet GUID was not echoed back")
	}
	// shipId as a numeric string -- an int32 reads as 0 through the client's union.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "shipId", "33489262")) {
		t.Error("shipId was not echoed back as a numeric string")
	}
	if !bytes.Contains(payload, []byte("YA_RemoveFromFleet")) {
		t.Error("response is not tagged with the request name")
	}
}

// The client resolves a fleet entry by looking its loadout id up IN the tech
// tree, and builds the owned-ship overview from the same rows. techTreeShips()
// synthesises those rows only for the four STARTER loadouts, so a player given
// any other ship had no row for it: observed live as an empty owned-ship
// overview, with the only addable ships being ones just removed from a fleet.
func TestOwnedShipsIncludePersistedNonStarterLoadouts(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	// A T3 loadout, exactly as the Veteran fleet carries it.
	if _, err := database.Exec(`INSERT OR REPLACE INTO player_ship_loadouts
		(user_id,loadout_id,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active)
		VALUES(?,?,?,?,?,0,?,9,1)`,
		pid, 33489272, "Default__VH_AssaultMedium_T3_PrecastLoadout_BP_C", 33489272, 184483980, "Otranto"); err != nil {
		t.Fatalf("insert loadout: %v", err)
	}

	var found bool
	for _, ship := range playerOwnedTechTreeShips(pid) {
		if ship.id == 33489272 {
			found = true
			if !ship.owned {
				t.Error("the persisted loadout's row is not marked owned")
			}
		}
	}
	if !found {
		t.Fatal("no tech tree row for a persisted non-starter loadout; the client cannot place or list that ship")
	}
}

// realUnlockItemPayload is the exact body the client sent for YA_UnlockItem,
// captured live 2026-08-02 while trying to unlock the T2 Scout Light:
//
//	ItemID tag 0x76 (8-byte int) = 33489267
//	ShipXp tag 0x66 (4-byte int) = 0
//	FreeXp tag 0x66 (4-byte int) = 5000
//
// Neither tag had a case in the scanners, so reading stopped at ItemID and the
// whole request came back empty.
func realUnlockItemPayload() []byte {
	return []byte{
		0x02, 'R', 'T', 0x09, 0x0d, 0x00, 0x00, 0x00,
		'Y', 'A', '_', 'U', 'n', 'l', 'o', 'c', 'k', 'I', 't', 'e', 'm',
		0x06, 'I', 't', 'e', 'm', 'I', 'D', 0x76, 0x73, 0x01, 0xff, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x06, 'S', 'h', 'i', 'p', 'X', 'p', 0x66, 0x00, 0x00, 0x00, 0x00,
		0x06, 'F', 'r', 'e', 'e', 'X', 'p', 0x66, 0x88, 0x13, 0x00, 0x00,
		0x00, 0x0e, 0x00, 0x00, 0x00, 0x00,
	}
}

func TestUnlockItemFieldsAreReadable(t *testing.T) {
	p := realUnlockItemPayload()
	if got := firstMmogInt32Field(p, "ItemID"); got != 33489267 {
		t.Errorf("ItemID = %d, want 33489267 (tag 0x76, 8-byte int)", got)
	}
	if got := firstMmogInt32Field(p, "FreeXp"); got != 5000 {
		t.Errorf("FreeXp = %d, want 5000 -- a field AFTER ItemID, so it only parses if 0x76 is skipped correctly", got)
	}
}

// Unlocking must actually record ownership and charge the free XP, or the ship
// never becomes owned and can be unlocked forever.
func TestUnlockItemRecordsOwnershipAndCharges(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=10000 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}

	if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	var owned int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_id=?`,
		pid, 33489267).Scan(&owned); err != nil {
		t.Fatalf("count: %v", err)
	}
	if owned != 1 {
		t.Fatalf("purchases for the unlocked item = %d, want 1", owned)
	}
	var freeXP int32
	if err := database.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP); err != nil {
		t.Fatalf("read free xp: %v", err)
	}
	if freeXP != 5000 {
		t.Errorf("free xp = %d, want 5000 after a 5000 charge from 10000", freeXP)
	}
}

// Too little free XP must record nothing, so the client's view and ours agree.
func TestUnlockItemRefusesWhenFreeXPIsShort(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=10 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}

	if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	var owned int
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_id=?`,
		pid, 33489267).Scan(&owned)
	if owned != 0 {
		t.Fatalf("recorded %d purchases despite insufficient free xp, want 0", owned)
	}
}

// A tech tree unlock names the PRECAST LOADOUT, and the ship list is keyed by
// those ids -- but techTreeShips() only holds T1/T2 pawns plus starter aliases,
// so an unlocked ship had no row for the purchase to mark owned. Three unlocks
// were charged and recorded live (33489267, 33489277, 33489281) while the ships
// stayed invisible, which looks exactly like "the unlock did nothing".
func TestPurchasedLoadoutGetsAnOwnedShipRow(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The T2 Scout Light, exactly as the live unlock recorded it.
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		VALUES(?,?,?,?,?)`, pid, 33489267, "loadout", 5000, "freexp"); err != nil {
		t.Fatalf("record purchase: %v", err)
	}

	var found bool
	for _, ship := range playerOwnedTechTreeShips(pid) {
		if ship.id == 33489267 {
			found = true
			if !ship.owned {
				t.Error("the unlocked ship's row is not marked owned")
			}
			if ship.classID == 0 {
				t.Error("row carries no ship identity; it was not derived from the pawn behind the loadout")
			}
		}
	}
	if !found {
		t.Fatal("no ship row for an unlocked precast loadout; the unlock is charged but invisible")
	}
}

// The class/size -> classID/shipClass/weight mapping was read off the built-in
// T1/T2 rows. Re-derive each of those rows from its asset path and require the
// same answer, so a wrong pair cannot be introduced silently.
func TestDerivedShipSeedMatchesTheBuiltInRows(t *testing.T) {
	checked := 0
	for _, want := range t1t2TechTreeShips {
		got, ok := deriveShipSeedFromAssetPath(want.id)
		if !ok {
			continue // hero/alias rows have no pawn asset path
		}
		checked++
		if got.classID != want.classID || got.shipClass != want.shipClass || got.weight != want.weight {
			t.Errorf("%s (%d): derived classID/shipClass/weight = %d/%d/%d, built-in row says %d/%d/%d",
				want.name, want.id, got.classID, got.shipClass, got.weight,
				want.classID, want.shipClass, want.weight)
		}
	}
	if checked == 0 {
		t.Fatal("derived nothing; the asset-path pattern no longer matches any built-in ship")
	}
	t.Logf("re-derived %d built-in ship rows", checked)
}

// A T3+ unlock has no built-in row to copy from -- that is why two of four
// live unlocks were charged and stayed invisible while the T2 one appeared.
func TestPurchasedT3LoadoutGetsARow(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 33489277 = VH_ScoutMedium_T3, one of the two that vanished.
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		VALUES(?,?,?,?,?)`, pid, 33489277, "loadout", 15000, "freexp"); err != nil {
		t.Fatalf("record purchase: %v", err)
	}

	for _, ship := range playerOwnedTechTreeShips(pid) {
		if ship.id == 33489277 {
			if !ship.owned {
				t.Error("T3 unlock present but not owned")
			}
			if ship.classID == 0 || ship.name == "" {
				t.Errorf("T3 row lacks identity: classID=%d name=%q", ship.classID, ship.name)
			}
			return
		}
	}
	t.Fatal("no row for a purchased T3 loadout; the unlock is charged but invisible")
}

// The client re-sends YA_UnlockItem for anything it does not believe it owns.
// One live session sent six unlock requests while the purchase count stayed at
// four -- each repeat silently took another 5,000 free XP for an item the
// player already had, because INSERT OR IGNORE swallowed the duplicate row
// while the charge above it had already gone through.
func TestUnlockItemDoesNotChargeTwiceForTheSameItem(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=20000 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
			t.Fatalf("unlock %d: %v", i, err)
		}
	}

	var freeXP int32
	if err := database.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP); err != nil {
		t.Fatalf("read: %v", err)
	}
	if freeXP != 15000 {
		t.Fatalf("free xp = %d after three unlocks of the same item, want 15000 (charged once)", freeXP)
	}
}

// The unlock response arm reads result/status/reason/ShipXp/FreeXp/ItemID/ShipID
// and compares the status against "succeeded" -- not "ok". With the generic
// success envelope the client logged "Failed to unlock item 0. Error:" : id 0
// because nothing was echoed, failure because the word was wrong.
func TestUnlockItemResponseCarriesWhatTheClientReads(t *testing.T) {
	// Where the client reads each field, from the YA_UnlockItem reply branch
	// (0x2A25DAE-0x2A263DB): status and ShipID under "result"; ItemID, ShipXp
	// and FreeXp at the ROOT. The earlier version of this test only checked the
	// names appeared SOMEWHERE, and passed while all of them sat under
	// "result": the client read ItemID as 0, appended 0 to its researched
	// list, and the Research button never cleared (live, 2026-09-23).
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=50000 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}
	unlock := func() (root, result []byte) {
		t.Helper()
		if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
			t.Fatalf("unlock: %v", err)
		}
		payload := buildMmogUnlockItemPayload(pid, realUnlockItemPayload())
		result = extractNamedMmogObject(t, payload, "result")
		return bytes.Replace(payload, result, nil, 1), result
	}

	root, result := unlock()
	if !bytes.Contains(result, protocol.AppendStringField(nil, fieldStatus, "succeeded")) {
		t.Error(`result.status must be "succeeded"; "ok" is what made every unlock report failure`)
	}
	if !bytes.Contains(result, []byte("ShipID")) {
		t.Error("result.ShipID missing")
	}
	if !bytes.Contains(root, protocol.AppendStringField(nil, "ItemID", "33489267")) {
		t.Error("ItemID must be a numeric string at the ROOT -- under result the client reads 0")
	}
	// The client SUBTRACTS these, so they are what was spent: the captured
	// request offered 5000 free XP.
	if !bytes.Contains(root, protocol.AppendStringField(nil, "FreeXp", "5000")) {
		t.Error("root FreeXp must be the 5000 this unlock charged")
	}
	if !bytes.Contains(root, protocol.AppendStringField(nil, "ShipXp", "0")) {
		t.Error("root ShipXp must be 0: ship XP is not charged server-side")
	}
	for _, field := range []string{"ItemID", "ShipXp", "FreeXp"} {
		if bytes.Contains(result, []byte(field)) {
			t.Errorf("%s is under result, where the client does not read it", field)
		}
	}

	// A repeat for an item already owned charges nothing, so the client must
	// not be told to subtract anything either.
	root, _ = unlock()
	if !bytes.Contains(root, protocol.AppendStringField(nil, "FreeXp", "0")) {
		t.Error("a repeat unlock of an owned item must report 0 free XP spent")
	}
}

// When the player cannot pay, nothing is recorded, and the reply must say so
// rather than "succeeded" -- which would make the client subtract XP and mark
// the item researched for something the server never granted.
func TestUnlockItemReportsARefusal(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=0 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	payload := buildMmogUnlockItemPayload(pid, realUnlockItemPayload())
	if bytes.Contains(payload, protocol.AppendStringField(nil, fieldStatus, "succeeded")) {
		t.Error("an unlock the player could not pay for was reported as succeeded")
	}
}

// A purchase record alone is not ownership to the client: with the ids in
// PurchasesData and m_isOwned set, it still re-sent YA_UnlockItem for 33489267,
// an id already in player_purchases. Every ship the client treats as owned is
// one the player has a LOADOUT for -- that is what fills UYLoadoutManager, and
// it is why the four starters are owned.
func TestUnlockGrantsAShipLoadout(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=50000 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}

	if err := persistUnlockItem(database, pid, realUnlockItemPayload()); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	var native, name string
	var shipID int32
	if err := database.QueryRow(`SELECT native_loadout_id,ship_id,loadout_name FROM player_ship_loadouts
		WHERE user_id=? AND loadout_id=?`, pid, 33489267).Scan(&native, &shipID, &name); err != nil {
		t.Fatalf("no loadout row granted for the unlocked ship: %v", err)
	}
	if shipID == 0 {
		t.Error("granted loadout has no pawn behind it")
	}
	// Must name the SHIPPING precast blueprint, never a Development one -- the
	// client instantiates this class and reports its own precast id.
	if !strings.Contains(native, "PrecastLoadout") || strings.Contains(native, "Development") {
		t.Errorf("native loadout id = %q, want the shipping PrecastLoadout class", native)
	}
	if name == "" {
		t.Error("granted loadout has no ship name")
	}
}

// Researching a module makes it RESEARCHED (ProgressionData), not OWNED
// (PurchasesData); buying it with credits afterwards makes it owned and costs
// credits. Before this, a research was recorded as a purchase and the item was
// owned for free -- live report 2026-09-23.
func TestResearchIsNotAPurchaseUntilBoughtWithCredits(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=50000, soft_currency=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatalf("fund: %v", err)
	}
	const module = 68026413 // "Trafalgar Goliath Torpedo II", researched live
	request := func(name string, fields ...[]byte) []byte {
		b := protocol.AppendStringField(nil, "RT", name)
		for _, f := range fields {
			b = append(b, f...)
		}
		return protocol.AppendRootEnd(b)
	}
	unlock := request("YA_UnlockItem",
		protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module)),
		protocol.AppendStringField(nil, "FreeXp", "2000"))
	if err := persistUnlockItem(database, pid, unlock); err != nil {
		t.Fatalf("research: %v", err)
	}
	researched := extractNamedMmogArray(t, buildMmogPlayerProgressionPayload(pid), "ProgressionData")
	owned := extractNamedMmogArray(t, buildMmogPlayerPurchasesPayloadForPlayer(pid), "PurchasesData")
	if !bytes.Contains(researched, []byte(strconv.Itoa(module))) {
		t.Error("a researched module is missing from ProgressionData")
	}
	if bytes.Contains(owned, []byte(strconv.Itoa(module))) {
		t.Error("researching a module also made it OWNED, without paying credits")
	}

	var credits int64
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&credits)
	reply := buildMmogPurchasePayload("YA_PurchaseItem", pid,
		request("YA_PurchaseItem", protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module))))
	if bytes.Contains(reply, []byte("already owned")) {
		t.Fatal("buying a researched module was refused as already owned")
	}
	var after int64
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&after)
	if after >= credits {
		t.Errorf("buying a researched module cost nothing (credits %d -> %d)", credits, after)
	}
	owned = extractNamedMmogArray(t, buildMmogPlayerPurchasesPayloadForPlayer(pid), "PurchasesData")
	if !bytes.Contains(owned, []byte(strconv.Itoa(module))) {
		t.Error("a bought module is not owned")
	}
	// A second buy is a real "already owned".
	if reply := buildMmogPurchasePayload("YA_PurchaseItem", pid,
		request("YA_PurchaseItem", protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module)))); !bytes.Contains(reply, []byte("already owned")) {
		t.Error("a second purchase of an owned module was not refused")
	}
}

// Research with free XP, then BUY with credits through YA_ClaimItem -- the
// original game's flow (confirmed by the operator, 2026-09-23) and the client's:
// a researched item with no market offer makes the tech tree send YA_ClaimItem
// with only ItemID (0x4FDCE0 -> 0x2A16A80). The reply handler (0x2A38B10) reads
// result.status and the ROOT ItemID, and re-requests purchases/progression.
func TestClaimBuysAResearchedItemWithCredits(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const module = 68026413 // "Trafalgar Goliath Torpedo II"
	price := purchasePriceForItem(module)
	if price <= 0 {
		t.Fatalf("no credit price for %d", module)
	}
	credits := func() int64 {
		var c int64
		_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&c)
		return c
	}
	setCredits := func(c int64) {
		if _, err := database.Exec(`UPDATE player_state SET soft_currency=?, free_xp=50000 WHERE user_id=?`, c, pid); err != nil {
			t.Fatal(err)
		}
	}
	claim := func() (root, result []byte) {
		req := protocol.AppendRootEnd(append(protocol.AppendStringField(nil, "RT", "YA_ClaimItem"),
			protocol.AppendInt32Field(nil, "ItemID", module)...))
		payload := buildMmogClaimItemPayload(pid, req)
		result = extractNamedMmogObject(t, payload, "result")
		return bytes.Replace(payload, result, nil, 1), result
	}
	status := func(result []byte, want string) bool {
		return bytes.Contains(result, protocol.AppendStringField(nil, fieldStatus, want))
	}
	owned := func() bool {
		return bytes.Contains(extractNamedMmogArray(t, buildMmogPlayerPurchasesPayloadForPlayer(pid), "PurchasesData"),
			[]byte(strconv.Itoa(module)))
	}

	// Not researched yet: refused, nothing charged.
	setCredits(1000000)
	if _, result := claim(); !status(result, "failed") || credits() != 1000000 {
		t.Fatal("buying an item that was never researched must fail and charge nothing")
	}

	// Research it.
	unlock := protocol.AppendRootEnd(append(append(protocol.AppendStringField(nil, "RT", "YA_UnlockItem"),
		protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module))...),
		protocol.AppendStringField(nil, "FreeXp", "2000")...))
	if err := persistUnlockItem(database, pid, unlock); err != nil {
		t.Fatal(err)
	}

	// Researched but broke: refused, still not owned.
	setCredits(int64(price) - 1)
	if _, result := claim(); !status(result, "failed") || !bytes.Contains(result, []byte("insufficient credits")) {
		t.Error("buying without enough credits must fail with a reason")
	}
	if owned() {
		t.Fatal("a refused purchase made the item owned")
	}

	// Researched and funded: charged the price, owned.
	setCredits(1000000)
	root, result := claim()
	if !status(result, "succeeded") {
		t.Fatal("buying a researched item with enough credits failed")
	}
	if !bytes.Contains(root, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module))) {
		t.Error("ItemID must be at the ROOT, where the reply handler reads it")
	}
	if bytes.Contains(result, []byte("inventory")) {
		t.Error("the reply must not carry inventory: a populated one wiped the owned items live (2026-08-14)")
	}
	if got := credits(); got != 1000000-int64(price) {
		t.Errorf("credits %d, want %d after paying %d", got, 1000000-int64(price), price)
	}
	if !owned() {
		t.Error("a bought item is not in PurchasesData")
	}
	// Bookkeeping: the research XP and the credits are kept apart (they used
	// to be summed into price_paid), and the currency is the store's CR.
	var paid, researchXP int64
	var currency string
	if err := database.QueryRow(`SELECT price_paid, research_xp, currency FROM player_purchases WHERE user_id=? AND item_id=?`,
		pid, module).Scan(&paid, &researchXP, &currency); err != nil {
		t.Fatal(err)
	}
	if paid != int64(price) || researchXP != 2000 || currency != "CR" {
		t.Errorf("bought row: price_paid %d research_xp %d currency %q, want %d/2000/CR", paid, researchXP, currency, price)
	}

	// Buying again: success, no second charge.
	if _, result := claim(); !status(result, "succeeded") || credits() != 1000000-int64(price) {
		t.Error("claiming an owned item must succeed without charging again")
	}
}

// The store path for a researched module, replayed from the live client:
//
//	Sending PurchaseItem request (99968026432, 1, CR, )
//
// An offer must exist for a RESEARCHED per-ship item (the buy button needs one)
// and must NOT exist for an unresearched one (offering those coincided with
// research breaking live). Buying through the offer SKU charges credits, makes
// the item owned, and replies with the ROOT fields the client reads
// (0x2A2CAE8): result "bought", offer, quantity, currency.
func TestResearchedModuleIsBoughtThroughItsStoreOffer(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=1000000, free_xp=50000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	const module = 68026432 // researched live: "Trafalgar Torpedo Salvo II"
	offerFor := func() (gatewayCatalogEntitySeed, bool) {
		for _, seed := range gatewayItemCatalogSeeds(pid) {
			if seed.itemID == module {
				return seed, true
			}
		}
		return gatewayCatalogEntitySeed{}, false
	}
	if _, ok := offerFor(); ok {
		t.Fatal("an unresearched module has a store offer")
	}

	unlock := protocol.AppendRootEnd(append(append(protocol.AppendStringField(nil, "RT", "YA_UnlockItem"),
		protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module))...),
		protocol.AppendStringField(nil, "FreeXp", "2000")...))
	if err := persistUnlockItem(database, pid, unlock); err != nil {
		t.Fatal(err)
	}
	offer, ok := offerFor()
	if !ok {
		t.Fatal("a researched module has no store offer; the buy button shows price 0")
	}
	if offer.priceAmount <= 0 || offer.owned {
		t.Fatalf("researched module offer: price %d owned %v", offer.priceAmount, offer.owned)
	}
	// The offer's "name" is a localization key; without one the client
	// rendered "<DNT> Empty Name in Json en". 67CA6007... is the Goliath
	// Torpedo II blueprint's own m_headline, "Goliath Torpedo II" in English.
	if offer.localizationKey == "" {
		t.Error("offer has no localization key; the client renders '<DNT> Empty Name in Json en'")
	}

	sku := "999" + strconv.Itoa(module)
	req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
	req = append(req, protocol.AppendStringField(nil, "offer", sku)...)
	req = append(req, protocol.AppendInt32Field(nil, "quantity", 1)...)
	req = append(req, protocol.AppendStringField(nil, "currency", "CR")...)
	req = append(req, protocol.AppendStringField(nil, "campaign", "")...)
	req = protocol.AppendRootEnd(req)
	reply := buildMmogPurchasePayload("YA_PurchaseItem", pid, req)
	for _, want := range [][]byte{
		protocol.AppendStringField(nil, "result", "bought"),
		protocol.AppendStringField(nil, "offer", sku),
		protocol.AppendStringField(nil, "quantity", "1"),
		protocol.AppendStringField(nil, "currency", "CR"),
	} {
		if !bytes.Contains(reply, want) {
			t.Errorf("purchase reply lacks %q", want)
		}
	}
	var credits int64
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&credits)
	if credits != 1000000-int64(offer.priceAmount) {
		t.Errorf("credits %d, want %d (offer price %d)", credits, 1000000-int64(offer.priceAmount), offer.priceAmount)
	}
	if !bytes.Contains(extractNamedMmogArray(t, buildMmogPlayerPurchasesPayloadForPlayer(pid), "PurchasesData"),
		[]byte(strconv.Itoa(module))) {
		t.Error("a module bought through its offer is not owned")
	}
	if offer, _ := offerFor(); !offer.owned {
		t.Error("the offer of a bought module is not marked owned")
	}
}

// Trafalgar flies Flak Turrets I (84804388); the operator researched it before
// fitted defaults were reported as owned. It must count as owned -- its offer
// marked owned, and buying it charging nothing -- not be sold back to them.
// Also a regression guard for a deadlock: this path once queried the database
// from inside its own transaction on the single-connection store and hung.
func TestResearchedFittedDefaultIsNotSoldAgain(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := grantUnlockedShipLoadout(tx, pid, hullNamed(t, "Trafalgar").loadoutID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	const flak = 84804388
	if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'weapon',1000,'freexp')`, pid, flak); err != nil {
		t.Fatal(err)
	}
	for _, seed := range gatewayItemCatalogSeeds(pid) {
		if seed.itemID == flak && !seed.owned {
			t.Error("the offer for a fitted default is not marked owned")
		}
	}
	if status, _, charged := claimResearchedItem(pid, flak); status != "succeeded" || charged != 0 {
		t.Errorf("claiming a fitted default: status %q charged %d, want succeeded/0", status, charged)
	}
	var credits int64
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&credits)
	if credits != 1000000 {
		t.Errorf("credits changed to %d buying something the ship already flies", credits)
	}
}

// Every item a ship can research must have a name key for its store offer, or
// the buy popup shows "<DNT> Empty Name in Json en" (live, 2026-09-24).
func TestEveryResearchItemHasANameKey(t *testing.T) {
	missing := 0
	for _, hull := range baseShipLoadouts {
		for _, item := range techTreeModuleItems(hull, 0) {
			if _, ok := dreadconfig.ItemHeadlineKey(baseItemID(item.id)); !ok {
				missing++
				if missing <= 5 {
					t.Errorf("%s: research item %d (base %d) has no name key", hull.name, item.id, baseItemID(item.id))
				}
			}
		}
	}
	if missing > 0 {
		t.Errorf("%d research items without a name key", missing)
	}
}

// Every research item must map to exactly the hull whose research list it is
// on -- that ship's XP pays for it.
func TestEveryResearchItemKnowsWhichShipPaysForIt(t *testing.T) {
	for _, hull := range baseShipLoadouts {
		pawn, ok := dreadconfig.ShipIDForPrecastLoadout(hull.loadoutID)
		if !ok {
			continue
		}
		for _, item := range techTreeModuleItems(hull, 0) {
			got, ok := researchHullPawn(item.id)
			if !ok || got != pawn {
				t.Errorf("%s: research item %d is paid by ship %d, want %d", hull.name, item.id, got, pawn)
			}
		}
	}
}

// Ship XP: sent to the client (ShipXps was always empty -- "i have no
// battle/ship exp", 2026-09-24), charged when a module is researched with it,
// and reported against the right ship so the client deducts it locally.
func TestResearchSpendsTheShipsXP(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const module = 68026413 // Goliath Torpedo II, on Trafalgar's research list
	pawn, ok := researchHullPawn(module)
	if !ok {
		t.Fatal("no paying ship for the module")
	}
	if _, err := database.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,5000)`, pid, pawn); err != nil {
		t.Fatal(err)
	}
	get := buildMmogPlayerGetPayload(pid)
	if !bytes.Contains(extractNamedMmogArray(t, get, "ShipXps"), protocol.AppendStringField(nil, "ShipXp", "5000")) {
		t.Fatal("YA_PlayerGet does not carry the ship's 5000 XP")
	}

	research := func(shipXP int) []byte {
		req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(module))...)
		req = append(req, protocol.AppendStringField(nil, "ShipXp", strconv.Itoa(shipXP))...)
		req = append(req, protocol.AppendStringField(nil, "FreeXp", "0")...)
		req = protocol.AppendRootEnd(req)
		if err := persistUnlockItem(database, pid, req); err != nil {
			t.Fatal(err)
		}
		return buildMmogUnlockItemPayload(pid, req)
	}
	xp := func() int32 { return persistedPlayerShipXP(pid, pawn) }

	// More than the ship has: refused, nothing charged or recorded.
	if reply := research(6000); bytes.Contains(reply, protocol.AppendStringField(nil, fieldStatus, "succeeded")) || xp() != 5000 {
		t.Fatalf("researching with more ship XP than the ship has was accepted (xp now %d)", xp())
	}

	reply := research(2000)
	result := extractNamedMmogObject(t, reply, "result")
	root := bytes.Replace(reply, result, nil, 1)
	if !bytes.Contains(result, protocol.AppendStringField(nil, fieldStatus, "succeeded")) {
		t.Fatal("research with enough ship XP failed")
	}
	if xp() != 3000 {
		t.Errorf("ship XP %d, want 3000 after spending 2000", xp())
	}
	if !bytes.Contains(root, protocol.AppendStringField(nil, "ShipXp", "2000")) {
		t.Error("root ShipXp must be the 2000 spent: the client subtracts it")
	}
	if !bytes.Contains(result, protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(pawn)))) {
		t.Error("result.ShipID must name the ship whose XP was spent")
	}
}
