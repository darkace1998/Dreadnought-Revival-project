package main

import (
	"encoding/json"
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

func auditTestServer(t *testing.T, runDir string) *server {
	t.Helper()
	s := testServer()
	s.cfg.runDir = runDir
	return s
}

func TestAuditAppendsAndReadsBack(t *testing.T) {
	s := auditTestServer(t, t.TempDir())
	s.audit("grant", "alice")
	s.audit("ban", "mallory")

	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req.Header.Set("X-Admin-Key", "test-admin-key")
	rec := httptest.NewRecorder()
	s.requireAuth(http.HandlerFunc(s.apiAudit)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	// Entries are JSON lines carried as JSON strings (double-encoded), so
	// parse twice: envelope first, then each entry.
	var doc struct {
		Entries []string `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("got %d entries, want 2 (%s)", len(doc.Entries), rec.Body.String())
	}
	seen := map[string]bool{}
	for _, line := range doc.Entries {
		var e struct {
			Action string `json:"action"`
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("decode entry %q: %v", line, err)
		}
		seen[e.Action+"/"+e.Detail] = true
	}
	if !seen["grant/alice"] || !seen["ban/mallory"] {
		t.Errorf("entries missing: %v", seen)
	}
}

func TestAuditEmptyWhenNoLogYet(t *testing.T) {
	s := auditTestServer(t, t.TempDir())
	rec := httptest.NewRecorder()
	s.apiAudit(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestCrashesListsAndTails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crash-1.log"), []byte("boom\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crash-1.dmp"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	s := testServer()
	s.cfg.runDir = dir
	// crashDir looks beside runDir; point it by env instead.
	t.Setenv("CRASH_REPORT_DIR", dir)

	rec := httptest.NewRecorder()
	s.apiCrashes(rec, httptest.NewRequest(http.MethodGet, "/api/crashes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "crash-1.log") {
		t.Errorf("missing entry: %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/crashes?file=crash-1.log", nil)
	rec = httptest.NewRecorder()
	s.apiCrashes(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("tail failed: %d %s", rec.Code, rec.Body.String())
	}

	// Binary dumps are listed but refused for viewing.
	req = httptest.NewRequest(http.MethodGet, "/api/crashes?file=crash-1.dmp", nil)
	rec = httptest.NewRecorder()
	s.apiCrashes(rec, req)
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"lines"`) {
		t.Errorf("binary dump served for viewing: %s", rec.Body.String())
	}

	// Traversal is rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/crashes?file=../x", nil)
	rec = httptest.NewRecorder()
	s.apiCrashes(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for traversal", rec.Code)
	}
}
