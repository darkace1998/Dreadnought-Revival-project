package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"
)

func grantTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE player_state(
			user_id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT 'Local',
			soft_currency INTEGER NOT NULL DEFAULT 10000 CHECK (soft_currency >= 0),
			premium_currency INTEGER NOT NULL DEFAULT 0,
			free_xp INTEGER NOT NULL DEFAULT 0,
			current_rank INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO player_state(user_id,display_name,soft_currency,premium_currency,free_xp)
			VALUES('650dd79476a1484b8adcd01ac2f17354','Local',10000,0,50)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	log := logrus.New()
	log.SetOutput(nopWriter{})
	return &Handler{DB: db, Log: log}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func postGrant(t *testing.T, h *Handler, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.AdminGrant(rec, httptest.NewRequest(http.MethodPost, "/admin/grant", strings.NewReader(body)))
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

// The whole point of the endpoint: a tester with an empty account could not buy
// anything, which blocked the one open question about the tech tree screen
// (AGENT-CHAT C13.4).
func TestGrantAddsToTheExistingBalance(t *testing.T) {
	h := grantTestHandler(t)
	status, body := postGrant(t, h,
		`{"user_id":"650dd79476a1484b8adcd01ac2f17354","credits":5000,"free_xp":100}`)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %v", status, body)
	}
	// Adds, never sets: 10000 + 5000, and the untouched premium stays put.
	if got := body["credits"]; got != float64(15000) {
		t.Errorf("credits = %v, want 15000 (grant must add to the balance, not replace it)", got)
	}
	if got := body["free_xp"]; got != float64(150) {
		t.Errorf("free_xp = %v, want 150", got)
	}
	if got := body["premium"]; got != float64(0) {
		t.Errorf("premium = %v, want 0 -- an omitted amount must not change the balance", got)
	}
}

// A typo'd id must not create a funded account that belongs to nobody, and the
// operator has to be told the id was wrong rather than seeing "ok".
func TestGrantToAnUnknownPlayerIsRefused(t *testing.T) {
	h := grantTestHandler(t)
	status, _ := postGrant(t, h, `{"user_id":"00000000000000000000000000009999","credits":5000}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	var rows int
	if err := h.DB.QueryRow(`SELECT count(*) FROM player_state`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("player_state has %d rows, want 1 -- a bad id must not insert a ghost account", rows)
	}
}

func TestGrantRejectsBadInput(t *testing.T) {
	h := grantTestHandler(t)
	for name, body := range map[string]string{
		// A hyphenated UUID is the shape of the id everywhere EXCEPT the binary
		// protocol, so it is the likely paste, and it would silently match no row.
		"hyphenated uuid": `{"user_id":"650dd794-76a1-484b-8adc-d01ac2f17354","credits":10}`,
		"empty id":        `{"credits":10}`,
		// Negative amounts are a different operation (taking currency away) and
		// would trip the CHECK constraint or quietly underflow a balance.
		"negative":  `{"user_id":"650dd79476a1484b8adcd01ac2f17354","credits":-10}`,
		"no amount": `{"user_id":"650dd79476a1484b8adcd01ac2f17354"}`,
		"garbage":   `not json`,
	} {
		if status, _ := postGrant(t, h, body); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, status)
		}
	}
	var credits int64
	if err := h.DB.QueryRow(`SELECT soft_currency FROM player_state`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 10000 {
		t.Errorf("balance moved to %d on rejected input; want it untouched at 10000", credits)
	}
}

// players is how an operator finds the id to grant against: it appears nowhere
// in the client UI.
func TestPlayersListsBalances(t *testing.T) {
	h := grantTestHandler(t)
	rec := httptest.NewRecorder()
	h.AdminPlayers(rec, httptest.NewRequest(http.MethodGet, "/admin/players", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Players []struct {
			UserID  string `json:"user_id"`
			Credits int64  `json:"credits"`
		} `json:"players"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Players) != 1 {
		t.Fatalf("count = %d, players = %d, want 1", body.Count, len(body.Players))
	}
	if body.Players[0].UserID != "650dd79476a1484b8adcd01ac2f17354" || body.Players[0].Credits != 10000 {
		t.Errorf("got %+v", body.Players[0])
	}
}
