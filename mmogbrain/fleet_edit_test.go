package main

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"

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
