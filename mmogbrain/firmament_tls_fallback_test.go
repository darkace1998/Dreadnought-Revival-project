package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// A client that failed with bad record MAC is offered only SHA-384 suites on
// its next handshake; everyone else keeps the normal (SHA-256) suite.
func TestBadMACClientGetsSHA384Next(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	base := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS10, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384},
	}
	base.GetConfigForClient = sha384FallbackConfig(base, logrus.New())
	ln, err := tls.Listen("tcp", "127.0.0.1:0", base)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	suite := func() uint16 {
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12,
			CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384}})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.ConnectionState().CipherSuite
	}
	badMACMu.Lock()
	badMACClients = map[string]time.Time{}
	badMACMu.Unlock()

	if got := suite(); got != tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("a fresh client got %s", tls.CipherSuiteName(got))
	}
	noteHandshakeFailure("127.0.0.1:5555", errors.New("local error: tls: other"))
	if got := suite(); got != tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("an unrelated failure changed the suite to %s", tls.CipherSuiteName(got))
	}
	noteHandshakeFailure("127.0.0.1:5555", errors.New("local error: tls: bad record MAC"))
	if got := suite(); got != tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384 {
		t.Fatalf("after bad record MAC the client got %s, want the SHA-384 suite", tls.CipherSuiteName(got))
	}
	t.Setenv("DN_TLS_SHA384", "off")
	if got := suite(); got != tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("with DN_TLS_SHA384=off the client got %s", tls.CipherSuiteName(got))
	}
}
