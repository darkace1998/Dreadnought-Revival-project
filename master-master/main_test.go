package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	masterdb "github.com/darkace1998/Dreadnought-Revival-project/master-master/db"
	"github.com/darkace1998/Dreadnought-Revival-project/master-master/handlers"
	"github.com/sirupsen/logrus"
)

func testRouters(t *testing.T) (api, admin http.Handler) {
	t.Helper()
	database, err := masterdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	log := logrus.New()
	log.SetOutput(io.Discard)
	h := &handlers.Handler{DB: database, Log: log}
	return newAPIRouter(h, log), newAdminRouter(h, "test-admin-pass", log)
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The admin surface must not exist on the cluster listener at all: not
// "forbidden", but 404 — nothing to probe.
func TestAdminRoutesAbsentOnPublicListener(t *testing.T) {
	api, _ := testRouters(t)
	for _, path := range []string{"/admin", "/admin/", "/admin/api/clusters"} {
		if rec := get(t, api, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on cluster listener: got %d, want 404", path, rec.Code)
		}
	}
	// ...while cluster routes stay public there.
	if rec := get(t, api, "/clusters"); rec.Code != http.StatusOK {
		t.Errorf("GET /clusters: got %d, want 200", rec.Code)
	}
}

// The panel listener serves the page and the JSON API only with credentials,
// and nothing cluster-facing.
func TestAdminListenerNeedsAuth(t *testing.T) {
	_, admin := testRouters(t)
	if rec := get(t, admin, "/admin/api/clusters"); rec.Code != http.StatusUnauthorized {
		t.Errorf("no creds: got %d, want 401", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/clusters", nil)
	req.SetBasicAuth("", "wrong-pass")
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong pass: got %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/clusters", nil)
	req.SetBasicAuth("", "test-admin-pass")
	rec = httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("right pass: got %d, want 200", rec.Code)
	}

	// No cluster routes on the panel listener.
	for _, path := range []string{"/clusters", "/clusters/register"} {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Errorf("%s on panel listener: got %d, want not-2xx", path, rec.Code)
		}
	}
}
