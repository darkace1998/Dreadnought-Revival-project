//go:build windows

package main

// First-run setup for a tester's PC, with NO admin rights and NO hosts-file
// edit. What each backend name needs:
//
//   - The GAME never resolves a backend by name. Its only compiled-in backend
//     host is a default Firmament address (firmament.integration.greybox.
//     aviary.cloud) that -YFirmamentAddress overrides; the web-services gateway
//     comes from -GatewayAddress. Both are passed as the server's IP below.
//     Measured from run/gateway.log: every by-name request that ever reached
//     the gateway was the LAUNCHER's own sign-in (POST /auth/, /auth/register)
//     plus one July visit to the old launcher tiles page.
//   - The LAUNCHER's sign-in keeps its original URL
//     (profile-api.prod.greybox.sixfoot.live/auth/), because the gateway routes
//     by Host header. launcherTransport dials the server's address directly
//     instead of resolving that name, and verifies the certificate against the
//     shipped ca.crt under that name (the server cert carries it as a SAN).
//   - The CA itself still has to be trusted by the game: it verifies the
//     gateway and Firmament certificates (and Firmament pins the "Amazon RSA
//     2048 M01" issuer our certs/firmament.* mimic). A ca.crt beside the
//     launcher goes into the CURRENT USER's Trusted Root store -- no admin, no
//     UAC; Windows shows its own confirmation once.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// serverDialIP, when set, is where every launcher connection goes,
	// whatever host name the URL carries.
	serverDialIP string
	// serverCAPool verifies the server's certificate when a ca.crt shipped.
	serverCAPool *x509.CertPool
	// serverWebPort replaces 443 when dialing serverDialIP (Config.WebPort).
	serverWebPort string
)

// launcherTransport is the one HTTP transport the launcher uses.
func launcherTransport() *http.Transport {
	t := &http.Transport{}
	if serverCAPool != nil {
		// Real verification: the shipped CA, under the URL's own host name.
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

// resolveServerIP turns the configured server (hostname or IP) into an IPv4
// address.
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

// ---- CA certificate -----------------------------------------------------------

var procCertAddEncodedCertificateToStore = syscall.NewLazyDLL("crypt32.dll").NewProc("CertAddEncodedCertificateToStore")

// readCACert loads ca.crt (PEM or DER) from beside the launcher. Absent means
// "nothing to install".
func readCACert(exeDir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(exeDir, "ca.crt"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	der := raw
	if block, _ := pem.Decode(raw); block != nil {
		der = block.Bytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca.crt is not a certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("ca.crt is not a CA certificate")
	}
	return der, nil
}

// ensureCAInstalled adds the CA to the current user's Trusted Root store
// unless an identical certificate is already there.
func ensureCAInstalled(der []byte) error {
	want := sha256.Sum256(der)

	name, _ := windows.UTF16PtrFromString("ROOT")
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER, uintptr(unsafe.Pointer(name)))
	if err != nil {
		return fmt.Errorf("open the Trusted Root store: %w", err)
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()

	var ctx *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if err != nil || ctx == nil {
			break
		}
		if sha256.Sum256(unsafe.Slice(ctx.EncodedCert, ctx.Length)) == want {
			_ = windows.CertFreeCertificateContext(ctx)
			fmt.Println("[+] Server certificate already trusted.")
			return nil
		}
	}

	fmt.Println("[*] Installing the server's certificate; Windows will ask you to confirm it once.")
	if r, _, callErr := procCertAddEncodedCertificateToStore.Call(uintptr(store),
		uintptr(windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING),
		uintptr(unsafe.Pointer(&der[0])), uintptr(len(der)),
		uintptr(windows.CERT_STORE_ADD_REPLACE_EXISTING), 0); r == 0 {
		return fmt.Errorf("install ca.crt (declined?): %v", callErr)
	}
	fmt.Println("[+] Server certificate installed.")
	return nil
}

// runMachineSetup points the launcher and the game at cfg.Server. Nothing to
// do without one (the old gateway_ip/hosts-file setup keeps working).
func runMachineSetup(exeDir string, cfg *Config) error {
	server := strings.TrimSpace(cfg.Server)
	if server == "" {
		return nil
	}
	ip, err := resolveServerIP(server)
	if err != nil {
		return err
	}
	fmt.Printf("[*] Server: %s (%s)\n", server, ip)
	serverDialIP = ip // the launcher's own connections, resolved fresh each start
	if p := strings.TrimSpace(cfg.WebPort); p != "" && p != "443" {
		serverWebPort = p
		fmt.Printf("[*] Sign-in and news via port %s\n", p)
	}
	// The GAME gets the name when there is one: it resolves names itself (its
	// gateway address becomes an https:// URL; Firmament logs "Connecting to
	// <host>:<port> (<resolved>)"), and a name keeps working when a home
	// connection's outside IP changes -- the certificates carry it as a SAN
	// (gen-certs.sh SERVER_NAME).
	cfg.GatewayIP = server
	if cfg.FirmamentHost == "" {
		cfg.FirmamentHost = server
	}

	der, err := readCACert(exeDir)
	if err != nil {
		return err
	}
	if der == nil {
		fmt.Println("[*] No ca.crt beside the launcher; assuming the server certificate is already trusted.")
		return nil
	}
	cert, _ := x509.ParseCertificate(der) // validated by readCACert
	serverCAPool = x509.NewCertPool()
	serverCAPool.AddCert(cert)
	return ensureCAInstalled(der)
}
