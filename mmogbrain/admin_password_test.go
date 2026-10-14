package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The dashboard's password reset goes to the auth server, returns the new
// password once, and audits the reset without it.
func TestAdminResetPasswordRelaysTheAuthServer(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	var gotKey, gotBody string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotKey, gotBody = r.Header.Get("X-Admin-Key"), string(b)
		if r.URL.Path != "/admin/password" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"reset","username":"pilot","password":"NewSecret234567"}`))
	}))
	defer auth.Close()
	r := adminTestRouter()
	oldURL := adminDash.authURL
	adminDash.authURL = auth.URL
	defer func() { adminDash.authURL = oldURL }()

	base := "/admin/api/players/" + adminTestPID
	if rec := adminPost(r, base+"/password", "wrong", `{}`); rec.Code != 403 {
		t.Errorf("wrong key: %d, want 403", rec.Code)
	}
	rec := adminPost(r, base+"/password", "test-admin-key", `{}`)
	var out struct{ Username, Password string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Password != "NewSecret234567" {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if gotKey == "" || !strings.Contains(gotBody, adminTestPID) {
		t.Errorf("auth server got key %q body %q", gotKey, gotBody)
	}
	audit := adminGet(r, base+"/audit", "test-admin-key").Body.String()
	if !strings.Contains(audit, "password reset") || strings.Contains(audit, "NewSecret234567") {
		t.Errorf("audit: %s", audit)
	}
}
