package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "trusted-cas.json")
	s, err := OpenTrustStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	key := clusterTrustKey("c1", "https://a.example")
	if s.Trusted(key, "abcdef01") {
		t.Fatal("unknown fingerprint must not be trusted")
	}
	if err := s.Remember(key, "AB:CD:EF:01"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !s.Trusted(key, "abcdef01") {
		t.Fatal("remembered fingerprint must be trusted in any spelling")
	}
	if s.Trusted(key, "00000000") {
		t.Fatal("different fingerprint must not be trusted")
	}
	// Reloaded from disk.
	s2, err := OpenTrustStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !s2.Trusted(key, "ab:cd:ef:01") {
		t.Fatal("trust did not survive reload")
	}
}

func TestTrustStoreCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted-cas.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenTrustStore(path)
	if err != nil {
		t.Fatalf("open corrupt: %v", err)
	}
	if s.Trusted("id:x", "abcdef01") {
		t.Fatal("corrupt store must trust nothing")
	}
}

func TestClusterTrustKey(t *testing.T) {
	if got := clusterTrustKey("c1", "https://a.example"); got != "id:c1" {
		t.Errorf("got %q", got)
	}
	if got := clusterTrustKey("", "https://A.example/"); got != "manual:https://a.example/" {
		t.Errorf("got %q", got)
	}
}
