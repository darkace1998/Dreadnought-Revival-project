package main

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SHA-384 fallback for clients whose TLS handshake fails with "bad record MAC".
//
// Observed 2026-10-02: one player's client failed every Firmament handshake
// (17 in 15 minutes, "local error: tls: bad record MAC"), while every other
// client completed it with TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 and the
// same player's HTTPS login on :65443 worked. The server could not verify the
// client's Finished message: the client computed its handshake hash wrongly.
//
// GUESS at the cause (not verified on that machine): the game's Firmament
// client links an old OpenSSL whose CPU-feature detection enables the SHA
// extensions (SHA-NI) incorrectly on newer CPUs (Intel 10th gen+, AMD Zen),
// so its SHA-1/SHA-256 results are wrong -- the known fault behind the
// OPENSSL_ia32cap="~0x20000000" workaround for old games. A TLS 1.2 suite
// that ends in SHA384 hashes the handshake with SHA-384, which SHA-NI does
// not cover.
//
// Every other client works with the SHA-256 suite, so it is not changed for
// them: a client that failed with bad record MAC is remembered by address for
// a day -- on disk too (DN_TLS_SHA384_FILE, default tls-sha384-clients.json),
// so a server restart does not cost those players another failed login (seen
// 2026-10-02: one failed again after a deploy) -- and its next handshake (the game retries: "Unable to connect to the
// server. Try again in a moment") is offered only the SHA-384 suites, if its
// ClientHello lists one. DN_TLS_SHA384=off disables this, =all applies it to
// every client.

var (
	sha384Suites = []uint16{
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	}
	badMACMu      sync.Mutex
	badMACClients = map[string]time.Time{}
)

const badMACMemory = 24 * time.Hour

func remoteHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// noteHandshakeFailure remembers a client whose handshake failed with a bad
// record MAC.
func noteHandshakeFailure(remote string, err error) {
	if err == nil || !strings.Contains(err.Error(), "bad record MAC") {
		return
	}
	badMACMu.Lock()
	loadBadMACClientsLocked()
	badMACClients[remoteHost(remote)] = time.Now()
	saveBadMACClientsLocked()
	badMACMu.Unlock()
}

var badMACLoaded bool

func badMACFile() string {
	if f := os.Getenv("DN_TLS_SHA384_FILE"); f != "" {
		return f
	}
	return "tls-sha384-clients.json"
}

// loadBadMACClientsLocked reads the remembered clients once (badMACMu held).
func loadBadMACClientsLocked() {
	if badMACLoaded {
		return
	}
	badMACLoaded = true
	raw, err := os.ReadFile(badMACFile())
	if err != nil {
		return
	}
	var stored map[string]time.Time
	if json.Unmarshal(raw, &stored) != nil {
		return
	}
	for host, at := range stored {
		if time.Since(at) <= badMACMemory {
			badMACClients[host] = at
		}
	}
}

// saveBadMACClientsLocked writes the remembered clients (badMACMu held).
// Best effort: the file only saves a retry.
func saveBadMACClientsLocked() {
	raw, err := json.Marshal(badMACClients)
	if err != nil {
		return
	}
	tmp := badMACFile() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, badMACFile())
	}
}

func needsSHA384(remote string) bool {
	switch os.Getenv("DN_TLS_SHA384") {
	case "off":
		return false
	case "all":
		return true
	}
	badMACMu.Lock()
	defer badMACMu.Unlock()
	loadBadMACClientsLocked()
	at, ok := badMACClients[remoteHost(remote)]
	if ok && time.Since(at) > badMACMemory {
		delete(badMACClients, remoteHost(remote))
		return false
	}
	return ok
}

// sha384FallbackConfig is the listener's GetConfigForClient: the base config,
// or for a remembered client one restricted to the SHA-384 suites it offers.
func sha384FallbackConfig(base *tls.Config, log *logrus.Logger) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		remote := ""
		if hello.Conn != nil {
			remote = hello.Conn.RemoteAddr().String()
		}
		if !needsSHA384(remote) {
			return nil, nil // the base config
		}
		var offered []uint16
		for _, want := range sha384Suites {
			for _, have := range hello.CipherSuites {
				if have == want {
					offered = append(offered, want)
					break
				}
			}
		}
		if len(offered) == 0 {
			log.WithField("remote", remote).Warn("firmament: client failed with bad record MAC but offers no SHA-384 suite; using the normal suites")
			return nil, nil
		}
		cfg := base.Clone()
		cfg.GetConfigForClient = nil
		cfg.CipherSuites = offered
		log.WithFields(logrus.Fields{"remote": remote, "suites": len(offered)}).
			Warn("firmament: offering only SHA-384 suites to a client whose last handshake failed with bad record MAC")
		return cfg, nil
	}
}
