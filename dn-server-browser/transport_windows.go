//go:build windows

package main

// Per-cluster connection setup. dn-launcher keeps one global server; the
// browser switches between clusters, so selecting a cluster resets these
// globals (only one cluster is ever active) and every existing flow —
// sign-in, news, play — then works unchanged against it.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	// serverDialIP, when set, is where every browser connection goes,
	// whatever host name the URL carries.
	serverDialIP string
	// serverCAPool verifies the cluster's certificate when its CA is known
	// (TOFU from the directory, or a built-in/pasted CA).
	serverCAPool *x509.CertPool
	// serverWebPort replaces 443 when dialing serverDialIP.
	serverWebPort string
)

// browserTransport is the one HTTP transport the browser uses.
func browserTransport() *http.Transport {
	t := &http.Transport{}
	if serverCAPool != nil {
		// Real verification: the cluster's CA, under the URL's own host name.
		t.TLSClientConfig = &tls.Config{RootCAs: serverCAPool, MinVersion: tls.VersionTLS12}
	} else {
		t.TLSClientConfig = buildTLSConfig()
	}
	if serverDialIP != "" {
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if port == "443" && serverWebPort != "" {
				port = serverWebPort
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(serverDialIP, port))
		}
	}
	return t
}

func browserHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: browserTransport(),
	}
}

// resolveServerIP turns the configured server (hostname or IP) into an IPv4
// address. The game takes dotted IPv4 only for -GatewayAddress
// (FInternetAddr::SetIp parses nothing else), so names are resolved here.
func resolveServerIP(server string) (string, error) {
	server = strings.TrimSpace(server)
	if ip := net.ParseIP(server); ip != nil {
		return ip.String(), nil
	}
	addrs, err := net.LookupIP(server)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", server, err)
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address", server)
}

// selectClusterTransport points every browser connection at one cluster.
// server is its IP or public hostname, webPort its outside HTTPS port ("" for
// 443), caDER its CA certificate or nil when unknown (then the insecure
// fallback below applies, with a warning, exactly as dn-launcher behaves
// without a CA).
func selectClusterTransport(server, webPort string, caDER []byte) (string, error) {
	ip, err := resolveServerIP(server)
	if err != nil {
		return "", err
	}
	serverDialIP = ip
	serverWebPort = strings.TrimSpace(webPort)
	serverCAPool = nil
	if len(caDER) > 0 {
		cert, err := x509.ParseCertificate(caDER)
		if err != nil {
			return "", fmt.Errorf("cluster CA is not a certificate: %w", err)
		}
		serverCAPool = x509.NewCertPool()
		serverCAPool.AddCert(cert)
	}
	return ip, nil
}

func buildTLSConfig() *tls.Config {
	fingerprint := strings.TrimSpace(os.Getenv("TLS_CERT_FINGERPRINT"))
	if fingerprint == "" {
		fmt.Fprintln(os.Stderr, "[!] TLS_CERT_FINGERPRINT not set — certificate verification is disabled.")
		fmt.Fprintln(os.Stderr, "[!] Set TLS_CERT_FINGERPRINT to the SHA256 hex fingerprint of the server certificate for security.")
		fmt.Fprintln(os.Stderr, "[!] Find it with: openssl x509 -in server.crt -fingerprint -sha256 -noout")
		//nolint:gosec // Intentional fallback for manually added servers whose CA is unknown.
		return &tls.Config{InsecureSkipVerify: true}
	}
	expected := normalizeFingerprint(fingerprint)
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			for _, raw := range rawCerts {
				cert, err := x509.ParseCertificate(raw)
				if err != nil {
					continue
				}
				sum := sha256.Sum256(cert.Raw)
				if hex.EncodeToString(sum[:]) == expected {
					return nil
				}
			}
			return fmt.Errorf("server certificate fingerprint does not match TLS_CERT_FINGERPRINT")
		},
	}
}
