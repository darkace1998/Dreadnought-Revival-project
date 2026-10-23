//go:build windows

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestParseServerAddress(t *testing.T) {
	for in, want := range map[string]savedServer{
		"play.example.org":            {Address: "play.example.org"},
		" https://Play.Example.org/ ": {Address: "play.example.org"},
		"203.0.113.7:8443":            {Address: "203.0.113.7", WebPort: "8443"},
		"203.0.113.7:443":             {Address: "203.0.113.7"},
		"https://play.example.net:99": {Address: "play.example.net", WebPort: "99"},
	} {
		got, err := parseServerAddress(in)
		if err != nil || got != want {
			t.Errorf("parseServerAddress(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "host:0", "host:70000", "host:abc", "a b", "user@host", "host/path"} {
		if _, err := parseServerAddress(bad); err == nil {
			t.Errorf("parseServerAddress(%q) accepted", bad)
		}
	}
	if k := (savedServer{Address: "203.0.113.7", WebPort: "8443"}).key(); k != "203.0.113.7_8443" {
		t.Errorf("key = %q", k)
	}
}

// Default first, then the added servers.
func TestServerChoices(t *testing.T) {
	defer func(c Config) { baseConfig = c }(baseConfig)
	baseConfig = defaultConfig()
	baseConfig.Server = "play.example.org"
	s := launcherSettings{Servers: []savedServer{{Name: "Friend", Address: "friend.example"}, {Name: "empty"}}}
	got := serverChoices(s)
	if len(got) != 2 || got[0].id() != "" || got[1].Name != "Friend" {
		t.Fatalf("choices = %+v", got)
	}
	cfg := configFor(savedServer{Address: "friend.example", WebPort: "8443"})
	if cfg.Server != "friend.example" || cfg.WebPort != "8443" || cfg.caFile == "" || cfg.FirmamentHost != "" {
		t.Fatalf("configFor = %+v", cfg)
	}
}

func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return ca, key, der
}

// serveCA starts a TLS server whose certificate is issued by signer and which
// offers offered at /dn/ca.crt, like gateway/main.go.
func serveCA(t *testing.T, signer *x509.Certificate, signerKey *ecdsa.PrivateKey, offered []byte) (ip, port string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "server"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dn/ca.crt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: offered}))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Hostname(), u.Port()
}

func TestFetchServerCA(t *testing.T) {
	ca, caKey, caDER := testCA(t)
	ip, port := serveCA(t, ca, caKey, caDER)
	got, err := fetchServerCA(ip, port)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(caDER) {
		t.Fatal("fetched CA differs from the served one")
	}

	// A CA that did not issue the server's certificate is refused.
	_, _, otherDER := testCA(t)
	ip, port = serveCA(t, ca, caKey, otherDER)
	if _, err := fetchServerCA(ip, port); err == nil {
		t.Fatal("accepted a CA that did not sign the connection")
	}
}

// Each server remembers its own sign-in; the default keeps account.json.
func TestCredentialPathPerServer(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	defer func() { credentialServer = "" }()
	def, _ := credentialPath()
	credentialServer = "friend.example"
	other, _ := credentialPath()
	if def == other || !(len(def) > 0 && def[len(def)-len("account.json"):] == "account.json") {
		t.Fatalf("default %q, server %q", def, other)
	}
}
