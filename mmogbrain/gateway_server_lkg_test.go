package main

import (
	"net/http/httptest"
	"testing"
)

// The game parses /play/lkg's serverHost as an IP only; a DNS name (what
// SERVER_IP became with PUBLIC_HOST) crashed every client right after lkg.
func TestLkgHostIsAlwaysAnIP(t *testing.T) {
	t.Setenv("MMOG_HOST", "")
	t.Setenv("SERVER_IP", "localhost")

	req := httptest.NewRequest("GET", "https://203.0.113.1:65443/api/v1/play/lkg", nil)
	req.Host = "203.0.113.7:65443"
	if host, src := lkgHostForClient(req); host != "203.0.113.7" {
		t.Errorf("request-host IP: got %q (%s), want the IP the client dialled", host, src)
	}

	req.Host = "mmog.greybox.sixfoot.live:65443" // a name: fall back to SERVER_IP, resolved
	if host, src := lkgHostForClient(req); host != "127.0.0.1" {
		t.Errorf("name host: got %q (%s), want SERVER_IP=localhost resolved to 127.0.0.1", host, src)
	}

	t.Setenv("MMOG_HOST", "10.1.2.3")
	if host, _ := lkgHostForClient(req); host != "10.1.2.3" {
		t.Errorf("MMOG_HOST override: got %q", host)
	}
}
