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
