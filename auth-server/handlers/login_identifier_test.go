package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dreadnought-ps/auth-server/db"
	"github.com/sirupsen/logrus"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	log := logrus.New()
	log.SetOutput(io.Discard)
	return &Handler{DB: database, Log: log, Secret: []byte("test-secret-value-for-signing")}
}

func register(t *testing.T, h *Handler, username, email, password string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "email": email, "password": password})
	rec := httptest.NewRecorder()
	h.Register(rec, httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(string(body))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: status %d, body %s", username, rec.Code, rec.Body.String())
	}
}

func passwordLogin(t *testing.T, h *Handler, identifier, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", identifier)
	form.Set("password", password)
	req := httptest.NewRequest(http.MethodPost, "/auth/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	return rec
}

// The launcher's sign-in screen asks for an email, so an account has to be
// reachable by the address it was created with -- not only by the callsign.
func TestPasswordLoginAcceptsEmailOrUsername(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "testpilot", "pilot@example.com", "hunter2x")

	for _, identifier := range []string{"pilot@example.com", "testpilot"} {
		rec := passwordLogin(t, h, identifier, "hunter2x")
		if rec.Code != http.StatusOK {
			t.Fatalf("login with %q: status %d, body %s", identifier, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("login with %q: unreadable body: %v", identifier, err)
		}
		if token, _ := body["access_token"].(string); token == "" {
			t.Errorf("login with %q returned no access_token", identifier)
		}
	}
}

func TestPasswordLoginRejectsBadCredentials(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "testpilot", "pilot@example.com", "hunter2x")

	for _, tc := range []struct{ name, identifier, password string }{
		{"wrong password", "pilot@example.com", "not-the-password"},
		{"unknown email", "nobody@example.com", "hunter2x"},
		{"unknown username", "nobody", "hunter2x"},
	} {
		if rec := passwordLogin(t, h, tc.identifier, tc.password); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", tc.name, rec.Code)
		}
	}
}

// Both columns are UNIQUE, so a second account cannot take either.
func TestRegisterRejectsDuplicateEmailOrUsername(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "testpilot", "pilot@example.com", "hunter2x")

	for _, tc := range []struct{ name, username, email string }{
		{"same username", "testpilot", "other@example.com"},
		{"same email", "otherpilot", "pilot@example.com"},
	} {
		body, _ := json.Marshal(map[string]string{"username": tc.username, "email": tc.email, "password": "hunter2x"})
		rec := httptest.NewRecorder()
		h.Register(rec, httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(string(body))))
		if rec.Code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", tc.name, rec.Code)
		}
	}
}
