package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func testServer() *server {
	return &server{
		cfg:      config{adminKey: "test-admin-key", internalKey: "test-admin-key"},
		log:      logrus.New(),
		sessions: newSessions(),
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

func TestCheckAdminKey(t *testing.T) {
	if !checkAdminKey("test-admin-key", "test-admin-key") {
		t.Fatal("equal keys must match")
	}
	if checkAdminKey("wrong", "test-admin-key") {
		t.Fatal("wrong key must not match")
	}
	if checkAdminKey("", "test-admin-key") {
		t.Fatal("empty key must not match")
	}
}

func TestSessionsMintValidRevoke(t *testing.T) {
	s := newSessions()
	tok, err := s.mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !s.valid(tok) {
		t.Fatal("fresh token must be valid")
	}
	if s.valid("nope") {
		t.Fatal("unknown token must be invalid")
	}
	s.revoke(tok)
	if s.valid(tok) {
		t.Fatal("revoked token must be invalid")
	}
}

func TestSessionsExpiry(t *testing.T) {
	s := newSessions()
	tok, err := s.mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	s.mu.Lock()
	s.tokens[tok] = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if s.valid(tok) {
		t.Fatal("expired token must be invalid")
	}
}

func TestRequireAuthRejectsAnonymous(t *testing.T) {
	s := testServer()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	s.requireAuth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestRequireAuthAcceptsHeader(t *testing.T) {
	s := testServer()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("X-Admin-Key", "test-admin-key")
	rec := httptest.NewRecorder()
	s.requireAuth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestInstanceIDValidation(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest(http.MethodGet, "/api/instance/../admin", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "../admin"})
	rec := httptest.NewRecorder()
	s.apiInstance(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for traversal id", rec.Code)
	}
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		sb.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, truncated, err := tailFile(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if len(lines) != 3 || lines[2] != "line 10" {
		t.Fatalf("unexpected tail: %v", lines)
	}
	if _, _, err := tailFile(filepath.Join(dir, "missing.log"), 10); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestClampLines(t *testing.T) {
	if clampLines("") != 200 || clampLines("abc") != 200 {
		t.Fatal("default must be 200")
	}
	if clampLines("5000") != 2000 {
		t.Fatal("must cap at 2000")
	}
	if clampLines("50") != 50 {
		t.Fatal("must pass through valid values")
	}
}

func TestTrimLeadingSlash(t *testing.T) {
	if trimLeadingSlash("//app.js") != "app.js" {
		t.Fatal("must strip all leading slashes")
	}
}

func authedRequest(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-Admin-Key", "test-admin-key")
	return httptest.NewRecorder(), req
}

func TestNormJoinIDFoldsBothSpellings(t *testing.T) {
	dashed := "09C6570A-36D3-4DE2-9485-9AE9D6A44673"
	undashed := "09c6570a36d34de294859ae9d6a44673"
	if normJoinID(dashed) != undashed {
		t.Fatalf("normJoinID(%q) = %q, want %q", dashed, normJoinID(dashed), undashed)
	}
}

// Validation must reject before any upstream call: these tests make no
// network requests (there is no upstream listening in tests).
func TestGrantAllRejectsEmpty(t *testing.T) {
	s := testServer()
	rec, req := authedRequest(t, http.MethodPost, "/api/grant-all", `{}`)
	s.apiGrantAll(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for empty grant-all", rec.Code)
	}
}

func TestGrantAllRejectsNegative(t *testing.T) {
	s := testServer()
	rec, req := authedRequest(t, http.MethodPost, "/api/grant-all", `{"credits":-5}`)
	s.apiGrantAll(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for negative grant-all", rec.Code)
	}
}

func TestGrantAllRejectsGarbage(t *testing.T) {
	s := testServer()
	rec, req := authedRequest(t, http.MethodPost, "/api/grant-all", `not json`)
	s.apiGrantAll(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for malformed grant-all", rec.Code)
	}
}
