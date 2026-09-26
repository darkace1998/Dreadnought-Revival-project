//go:build windows

package main

import (
	"crypto/x509"
	"io"
	"os"
	"strings"
	"testing"
)

// Opt-in, against a running server: DN_LAUNCHER_DIAL_IP=<server ip> and
// DN_LAUNCHER_CA_DIR=<dir holding its ca.crt>. The sign-in URL keeps its
// original host name; nothing resolves it -- the transport dials the IP and
// verifies the certificate under that name with the shipped CA.
func TestSignInReachesTheServerWithoutDNS(t *testing.T) {
	ip, dir := os.Getenv("DN_LAUNCHER_DIAL_IP"), os.Getenv("DN_LAUNCHER_CA_DIR")
	if ip == "" || dir == "" {
		t.Skip("set DN_LAUNCHER_DIAL_IP and DN_LAUNCHER_CA_DIR")
	}
	der, err := readCACert(dir)
	if err != nil || der == nil {
		t.Fatalf("readCACert: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	serverDialIP, serverCAPool = ip, x509.NewCertPool()
	serverCAPool.AddCert(cert)
	serverWebPort = os.Getenv("DN_LAUNCHER_WEB_PORT") // a router's 8443 -> 443, say
	defer func() { serverDialIP, serverCAPool, serverWebPort = "", nil, "" }()

	// An empty login: the auth server must answer it (4xx), which proves TLS
	// verified and the gateway routed the Host header to it.
	resp, err := launcherHTTPClient().Post(defaultConfig().AuthURL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST %s via %s: %v", defaultConfig().AuthURL, ip, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("%s -> %d %s", defaultConfig().AuthURL, resp.StatusCode, strings.TrimSpace(string(body)))
	if resp.StatusCode == 404 || resp.StatusCode >= 500 {
		t.Fatalf("gateway did not route the sign-in: %d", resp.StatusCode)
	}
}

// The launcher's news feed through the same direct dial.
func TestNewsReachesTheServerWithoutDNS(t *testing.T) {
	ip, dir := os.Getenv("DN_LAUNCHER_DIAL_IP"), os.Getenv("DN_LAUNCHER_CA_DIR")
	if ip == "" || dir == "" {
		t.Skip("set DN_LAUNCHER_DIAL_IP and DN_LAUNCHER_CA_DIR")
	}
	der, err := readCACert(dir)
	if err != nil || der == nil {
		t.Fatalf("readCACert: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	serverDialIP, serverCAPool = ip, x509.NewCertPool()
	serverCAPool.AddCert(cert)
	serverWebPort = os.Getenv("DN_LAUNCHER_WEB_PORT") // a router's 8443 -> 443, say
	defer func() { serverDialIP, serverCAPool, serverWebPort = "", nil, "" }()

	tiles, err := fetchNews()
	if err != nil {
		t.Fatalf("fetchNews: %v", err)
	}
	if len(tiles) == 0 {
		t.Fatal("no active news tiles")
	}
	for _, tile := range tiles {
		t.Logf("tile %q: %q", tile.Title, tile.Body)
	}
}
