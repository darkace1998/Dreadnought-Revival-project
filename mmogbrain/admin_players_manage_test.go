package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const adminTestPID = "650dd79476a1484b8adcd01ac2f17354"

func adminPost(r http.Handler, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type adminDetailResp struct {
	Credits int64       `json:"credits"`
	Premium int64       `json:"premium"`
	FreeXP  int64       `json:"free_xp"`
	Ships   []adminShip `json:"ships"`
	Heroes  []adminShip `json:"heroes"`
}

func decodeAdminDetail(t *testing.T, rec *httptest.ResponseRecorder) adminDetailResp {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var d adminDetailResp
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Player management needs the admin key, like every other data route.
func TestAdminPlayerManagementRequiresTheKey(t *testing.T) {
	r := adminTestRouter()
	base := "/admin/api/players/" + adminTestPID
	for _, key := range []string{"", "wrong"} {
		if rec := adminGet(r, base, key); rec.Code != http.StatusForbidden {
			t.Errorf("detail with key %q: %d", key, rec.Code)
		}
		if rec := adminPost(r, base+"/currency", key, `{"mode":"add","credits":5}`); rec.Code != http.StatusForbidden {
			t.Errorf("currency with key %q: %d", key, rec.Code)
		}
		if rec := adminPost(r, base+"/ships", key, `{"ship_id":1}`); rec.Code != http.StatusForbidden {
			t.Errorf("ships with key %q: %d", key, rec.Code)
		}
	}
}

func TestAdminChangesBalances(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=1000, premium_currency=50, free_xp=200 WHERE user_id=?`, adminTestPID); err != nil {
		t.Fatal(err)
	}
	r := adminTestRouter()
	path := "/admin/api/players/" + adminTestPID + "/currency"

	d := decodeAdminDetail(t, adminPost(r, path, "test-admin-key", `{"mode":"add","credits":500,"premium":25}`))
	if d.Credits != 1500 || d.Premium != 75 || d.FreeXP != 200 {
		t.Fatalf("after add: %+v, want 1500/75/200 (free XP untouched)", d)
	}
	d = decodeAdminDetail(t, adminPost(r, path, "test-admin-key", `{"mode":"add","credits":-99999}`))
	if d.Credits != 0 {
		t.Fatalf("subtracting past zero left %d credits, want 0", d.Credits)
	}
	d = decodeAdminDetail(t, adminPost(r, path, "test-admin-key", `{"mode":"set","free_xp":12345}`))
	if d.FreeXP != 12345 || d.Premium != 75 {
		t.Fatalf("after set: %+v, want free XP 12345 and GP unchanged", d)
	}
	for _, bad := range []string{`{"mode":"set","credits":-1}`, `{"mode":"add"}`, `{"mode":"steal","credits":1}`,
		`{"mode":"add","credits":10000000000}`, `not json`} {
		if rec := adminPost(r, path, "test-admin-key", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, rec.Code)
		}
	}
	// No ghost accounts from a typo.
	if rec := adminPost(r, "/admin/api/players/ffffffffffffffffffffffffffffffff/currency", "test-admin-key",
		`{"mode":"add","credits":1}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown player: %d, want 404", rec.Code)
	}
	if rec := adminGet(r, "/admin/api/players/not-a-pid", "test-admin-key"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed pid: %d, want 400", rec.Code)
	}
}

func TestAdminGrantsHeroShips(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	squadHubInstance.connected(adminTestPID)
	t.Cleanup(func() { squadHubInstance.disconnected(adminTestPID) })
	_ = squadHubInstance.drainPushes(adminTestPID)
	r := adminTestRouter()
	path := "/admin/api/players/" + adminTestPID + "/ships"
	hero := heroShipLoadouts[0].loadoutID

	d := decodeAdminDetail(t, adminPost(r, path, "test-admin-key", `{"ship_id":`+strconv.Itoa(int(hero))+`}`))
	owned := false
	for _, s := range d.Ships {
		owned = owned || (s.ID == hero && s.Hero)
	}
	if !owned {
		t.Fatal("granted hero ship is not in the player's ships")
	}
	for _, h := range d.Heroes {
		if h.ID == hero && !h.Owned {
			t.Error("hero list does not mark the granted ship owned")
		}
	}
	// An online player gets the ship now.
	sawClaim := false
	for _, p := range squadHubInstance.drainPushes(adminTestPID) {
		sawClaim = sawClaim || strings.Contains(string(p), "YA_ClaimItem")
	}
	if !sawClaim {
		t.Error("no YA_ClaimItem push for an online player")
	}
	if rec := adminPost(r, path, "test-admin-key", `{"ship_id":`+strconv.Itoa(int(hero))+`}`); rec.Code != http.StatusConflict {
		t.Errorf("granting an owned ship again: %d, want 409", rec.Code)
	}
	if rec := adminPost(r, path, "test-admin-key", `{"ship_id":33489265}`); rec.Code != http.StatusBadRequest {
		t.Errorf("granting a base ship: %d, want 400 (heroes only)", rec.Code)
	}
}
