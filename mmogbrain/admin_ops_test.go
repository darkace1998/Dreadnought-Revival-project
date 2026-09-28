package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestAdminProvisionValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"garbage pid", `{"user_id":"nope"}`},
		{"missing pid", `{"rank":20}`},
		{"bad rank", `{"user_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","rank":99}`},
		{"negative credits", `{"user_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","credits":-1}`},
		{"garbage body", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			adminProvision(rec, httptest.NewRequest(http.MethodPost, "/admin/provision", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400", rec.Code)
			}
		})
	}
}

func TestAdminQueueKick(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,fleet_type,status,queued_at)
		VALUES('q1','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','TDM',1,5,1,'waiting',datetime('now'))`); err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	kick := func(id string) int {
		req := httptest.NewRequest(http.MethodDelete, "/admin/queue/kick/"+id, nil)
		req = mux.SetURLVars(req, map[string]string{"entry": id})
		rec := httptest.NewRecorder()
		adminQueueKick(rec, req)
		return rec.Code
	}
	if code := kick("q1"); code != http.StatusOK {
		t.Errorf("kick = %d, want 200", code)
	}
	if code := kick("q1"); code != http.StatusNotFound {
		t.Errorf("second kick = %d, want 404", code)
	}
	if code := kick("../admin"); code != http.StatusBadRequest {
		t.Errorf("traversal kick = %d, want 400", code)
	}
}

func TestAdminResetValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"garbage pid", `{"user_id":"nope"}`},
		{"missing pid", `{"currencies":true}`},
		{"nothing selected", `{"user_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","currencies":false,"research":false}`},
		{"garbage body", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			adminReset(rec, httptest.NewRequest(http.MethodPost, "/admin/reset", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400", rec.Code)
			}
		})
	}
}

func TestAdminResetRestoresFreshAccount(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	pid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := database.Exec(`INSERT INTO player_state(user_id,display_name,soft_currency,premium_currency,free_xp,current_xp,current_rank,rank_xp)
		VALUES(?, 'Alice', 99999, 111, 222, 333, 9, 444)`, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		VALUES(?, 1234, 'weapon', 5000, 'CR')`, pid); err != nil {
		t.Fatalf("seed purchase: %v", err)
	}

	rec := httptest.NewRecorder()
	adminReset(rec, httptest.NewRequest(http.MethodPost, "/admin/reset",
		strings.NewReader(`{"user_id":"`+pid+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d, body %s", rec.Code, rec.Body.String())
	}

	var soft, premium, freeXP, curXP, rank, rankXP int64
	if err := database.QueryRow(`SELECT soft_currency,premium_currency,free_xp,current_xp,current_rank,rank_xp
		FROM player_state WHERE user_id=?`, pid).Scan(&soft, &premium, &freeXP, &curXP, &rank, &rankXP); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if soft != 10000 || premium != 0 || freeXP != 0 || curXP != 100 || rank != 1 || rankXP != 100 {
		t.Errorf("currencies not at seed values: %d/%d/%d xp=%d rank=%d rankxp=%d",
			soft, premium, freeXP, curXP, rank, rankXP)
	}
	var purchases int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=?`, pid).Scan(&purchases); err != nil {
		t.Fatal(err)
	}
	if purchases != 0 {
		t.Errorf("%d purchases left, want 0", purchases)
	}
	// The starter fleet must have been rebuilt, or the hangar breaks.
	var fleets, loadouts int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_fleets WHERE user_id=?`, pid).Scan(&fleets); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_ship_loadouts WHERE user_id=?`, pid).Scan(&loadouts); err != nil {
		t.Fatal(err)
	}
	if fleets == 0 || loadouts == 0 {
		t.Errorf("starter fleet not rebuilt: %d fleets, %d loadouts", fleets, loadouts)
	}
	// And the connected client must learn about it without a restart.
	if !consumeCurrencyDirty(pid) {
		t.Error("reset did not mark the account dirty")
	}
}

func TestAdminResetUnknownPlayer(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	rec := httptest.NewRecorder()
	adminReset(rec, httptest.NewRequest(http.MethodPost, "/admin/reset",
		strings.NewReader(`{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestAdminQueueClear(t *testing.T) {	database := useTempMmogPlayerStateDB(t)
	for _, id := range []string{"q1", "q2"} {
		if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,fleet_type,status,queued_at)
			VALUES(?,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','TDM',1,5,1,'waiting',datetime('now'))`, id); err != nil {
			t.Fatalf("seed queue: %v", err)
		}
	}
	rec := httptest.NewRecorder()
	adminQueueClear(rec, httptest.NewRequest(http.MethodPost, "/admin/queue/clear", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"cleared":2`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}
