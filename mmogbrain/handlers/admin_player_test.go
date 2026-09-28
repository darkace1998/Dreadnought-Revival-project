package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"
)

const detailTestPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func detailTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE player_state(user_id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT 'Local',
			soft_currency INTEGER NOT NULL DEFAULT 10000, premium_currency INTEGER NOT NULL DEFAULT 0,
			free_xp INTEGER NOT NULL DEFAULT 0, current_xp INTEGER NOT NULL DEFAULT 0,
			current_rank INTEGER NOT NULL DEFAULT 1, rank_xp INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`CREATE TABLE player_fleets(user_id TEXT, fleet_id INTEGER, display_name TEXT,
			fleet_type INTEGER DEFAULT 1, active INTEGER DEFAULT 0, flagship_ship_id INTEGER DEFAULT 0,
			PRIMARY KEY (user_id, fleet_id))`,
		`CREATE TABLE player_fleet_loadouts(user_id TEXT, fleet_id INTEGER, position INTEGER,
			loadout_id INTEGER, PRIMARY KEY (user_id, fleet_id, position))`,
		`CREATE TABLE player_ship_loadouts(user_id TEXT, loadout_id INTEGER, ship_id INTEGER,
			loadout_name TEXT, active INTEGER DEFAULT 0, PRIMARY KEY (user_id, loadout_id))`,
		`CREATE TABLE player_ship_xp(user_id TEXT, ship_id INTEGER, xp INTEGER,
			PRIMARY KEY (user_id, ship_id))`,
		`CREATE TABLE player_purchases(user_id TEXT, item_id INTEGER, item_type TEXT,
			PRIMARY KEY (user_id, item_id))`,
		`CREATE TABLE queue_entries(id TEXT PRIMARY KEY, user_id TEXT, game_mode TEXT,
			status TEXT, queued_at TEXT)`,
		`CREATE TABLE matches(id TEXT PRIMARY KEY, game_mode TEXT, map TEXT, server_ip TEXT,
			server_port INTEGER, status TEXT, created_at TEXT)`,
		`CREATE TABLE match_slots(match_id TEXT, user_id TEXT, team INTEGER,
			PRIMARY KEY (match_id, user_id))`,
		`CREATE TABLE battle_results(match_id TEXT, user_id TEXT, outcome TEXT, kills INTEGER,
			credits INTEGER, xp INTEGER, created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (match_id, user_id))`,
		`INSERT INTO player_state(user_id,display_name,soft_currency,free_xp,current_rank)
			VALUES('` + detailTestPID + `','Alice',15000,700,5)`,
		`INSERT INTO player_fleets(user_id,fleet_id,display_name,fleet_type,active)
			VALUES('` + detailTestPID + `',1,'Alpha',1,1)`,
		`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES('` + detailTestPID + `',10,5000)`,
		`INSERT INTO player_purchases(user_id,item_id,item_type) VALUES('` + detailTestPID + `',100,'weapon')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	log := logrus.New()
	log.SetOutput(nopWriter{})
	return &Handler{DB: db, Log: log}
}

func detailRequest(t *testing.T, h *Handler, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/player/"+id, nil)
	req = mux.SetURLVars(req, map[string]string{"id": id})
	rec := httptest.NewRecorder()
	h.AdminPlayerDetail(rec, req)
	var doc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	return rec.Code, doc
}

func TestAdminPlayerDetailRejectsBadID(t *testing.T) {
	h := detailTestHandler(t)
	if code, _ := detailRequest(t, h, "nope"); code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", code)
	}
	if code, _ := detailRequest(t, h, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); code != http.StatusNotFound {
		t.Errorf("got %d, want 404", code)
	}
}

func TestAdminPlayerDetailJoinsEverything(t *testing.T) {
	h := detailTestHandler(t)
	code, doc := detailRequest(t, h, detailTestPID)
	if code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%v)", code, doc)
	}
	player, _ := doc["player"].(map[string]any)
	if player["display_name"] != "Alice" || player["credits"] != float64(15000) {
		t.Errorf("player section wrong: %v", player)
	}
	if fleets, _ := doc["fleets"].([]any); len(fleets) != 1 {
		t.Errorf("fleets = %d, want 1", len(fleets))
	}
	if got, _ := doc["purchases"].(float64); got != 1 {
		t.Errorf("purchases = %v, want 1", got)
	}
	if _, ok := doc["in_match"]; !ok {
		t.Error("missing in_match flag")
	}
}
