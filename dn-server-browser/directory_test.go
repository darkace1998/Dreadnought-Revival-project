package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectoryList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clusters" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"clusters": []Cluster{
			{ID: "c1", Name: "Alpha", WebURL: "https://a.example", BattleIP: "203.0.113.7", Players: 4},
			{ID: "c2", Name: "Beta", WebURL: "https://b.example", BattleIP: "203.0.113.8"},
		}, "count": 2})
	}))
	defer srv.Close()

	got, err := (&DirectoryClient{BaseURL: srv.URL}).List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Name != "Alpha" || got[1].BattleIP != "203.0.113.8" {
		t.Fatalf("unexpected list: %+v", got)
	}
}

func TestDirectoryListFailsCleanly(t *testing.T) {
	if _, err := (&DirectoryClient{}).List(); err == nil {
		t.Fatal("expected an error with no directory configured")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := (&DirectoryClient{BaseURL: srv.URL}).List(); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}

func TestNormalizeUserID(t *testing.T) {
	for in, want := range map[string]string{
		"AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"  AAAA-1111 ":                        "aaaa1111",
		"": "",
	} {
		if got := normalizeUserID(in); got != want {
			t.Errorf("normalizeUserID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPresenceBlocksAndExcludes(t *testing.T) {
	var lastPath, lastExcept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath, lastExcept = r.URL.Path, r.URL.Query().Get("except")
		inMatch := lastExcept == "" || (lastExcept != "c1" && lastExcept != "Alpha")
		_ = json.NewEncoder(w).Encode(map[string]any{"in_match": inMatch, "cluster": "Beta"})
	}))
	defer srv.Close()
	c := &DirectoryClient{BaseURL: srv.URL}

	inMatch, cluster, err := c.Presence("BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB", "")
	if err != nil || !inMatch || cluster != "Beta" {
		t.Fatalf("blocked = %v %q err %v", inMatch, cluster, err)
	}
	if lastPath != "/presence/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("wrong path (id not normalized): %s", lastPath)
	}
	// Own cluster excluded, by id and by name (manual entries have no id).
	if inMatch, _, err := c.Presence("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "c1"); err != nil || inMatch {
		t.Fatalf("own id must be excluded: %v %v", inMatch, err)
	}
	if inMatch, _, err := c.Presence("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Alpha"); err != nil || inMatch {
		t.Fatalf("own name must be excluded: %v %v", inMatch, err)
	}
	if _, _, err := (&DirectoryClient{}).Presence("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ""); err == nil {
		t.Fatal("expected an error with no directory configured")
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	for in, want := range map[string]string{
		"AB:CD:ef:01": "abcdef01",
		"abcdef01":    "abcdef01",
		" AB CD ":     "abcd",
		"":            "",
	} {
		if got := normalizeFingerprint(in); got != want {
			t.Errorf("normalizeFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FingerprintDisplay("abcdef01"); got != "AB:CD:EF:01" {
		t.Errorf("display = %q", got)
	}
}

func TestRegisterCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register-check" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		taken := r.URL.Query().Get("username") == "alice" || r.URL.Query().Get("email") == "a@x.org"
		_ = json.NewEncoder(w).Encode(map[string]any{"taken": taken})
	}))
	defer srv.Close()
	c := &DirectoryClient{BaseURL: srv.URL}

	if taken, err := c.RegisterCheck("alice", "b@x.org"); err != nil || !taken {
		t.Fatalf("taken name: %v %v", taken, err)
	}
	if taken, err := c.RegisterCheck("bob", "a@x.org"); err != nil || !taken {
		t.Fatalf("taken address: %v %v", taken, err)
	}
	if taken, err := c.RegisterCheck("bob", "b@x.org"); err != nil || taken {
		t.Fatalf("free name+address: %v %v", taken, err)
	}
	if _, err := c.RegisterCheck("", ""); err == nil {
		t.Fatal("expected an error for empty input")
	}
	if _, err := (&DirectoryClient{}).RegisterCheck("alice", "a@x.org"); err == nil {
		t.Fatal("expected an error with no directory configured")
	}
}
