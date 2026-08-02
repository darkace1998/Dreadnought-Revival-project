package main

import (
	"database/sql"
	"testing"

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
