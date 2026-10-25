//go:build windows

package main

// First-run setup for a tester's PC, with NO admin rights and NO hosts-file
// edit. What each backend name needs:
//
//   - The GAME never resolves a backend by name. Its only compiled-in backend
//     host is a default Firmament address (firmament.integration.greybox.
//     aviary.cloud) that -YFirmamentAddress overrides; the web-services gateway
//     comes from -GatewayAddress. Both are passed as the server's IP below
//     (the gateway parser accepts only an IP; see runMachineSetup).
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
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// serverDialIP, when set, is where every launcher connection goes,
	// whatever host name the URL carries. Re-resolved while the launcher runs
	// (refreshServerIP), so it is read through dialIP().
	serverDialIP   string
	serverDialIPMu sync.Mutex
	// serverConfigured: a server setting exists, so the launcher's own
	// connections go to serverDialIP (or fail while it is unknown).
	serverConfigured bool
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
	if serverConfigured {
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if dialIP() == "" {
				return nil, errors.New("the server's address could not be looked up")
			}
			if port == "443" && serverWebPort != "" {
				port = serverWebPort
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(dialIP(), port))
		}
	}
	return t
}

func dialIP() string {
	serverDialIPMu.Lock()
	defer serverDialIPMu.Unlock()
	return serverDialIP
}

func setDialIP(ip string) {
	serverDialIPMu.Lock()
	serverDialIP = ip
	serverDialIPMu.Unlock()
}

// publicResolvers are asked directly when Windows' own lookup fails (no DNS
// server configured, a filtering resolver, a stale negative answer). A home
// server's outside IP changes, and its DNS record (a dynamic-DNS name with a
// short TTL) is the only place that knows the new one.
var publicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

// resolveServerIP turns the configured server (hostname or IP) into an IPv4
// address: Windows' resolver first (it honours the record's TTL, the hosts
// file and a company network's own DNS), then the public resolvers.
func resolveServerIP(server string) (string, error) {
	server = strings.TrimSpace(server)
	if ip := net.ParseIP(server); ip != nil {
		return ip.String(), nil
	}
	ip, err := lookupIPv4(net.DefaultResolver, server)
	if err == nil {
		return ip, nil
	}
	for _, ns := range publicResolvers {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, network, ns)
		}}
		if ip, perr := lookupIPv4(r, server); perr == nil {
			fmt.Printf("[*] %s looked up through %s (Windows' lookup failed: %v)\n", server, ns, err)
			return ip, nil
		}
	}
	return "", fmt.Errorf("look up %s: %w", server, err)
}

func lookupIPv4(r *net.Resolver, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	addrs, err := r.LookupIPAddr(ctx, name)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address", name)
}

// refreshServerIP looks the server's name up again and points the launcher
// and the game (cfg) at the answer. Called at start, for the news and before
// every Play, so a launcher left open across a change of the server's outside
// IP still starts the game at the new one. Nothing to do without a server
// setting (the old gateway_ip/hosts-file setup).
func refreshServerIP(cfg *Config) (string, error) {
	server := strings.TrimSpace(cfg.Server)
	if server == "" {
		return cfg.GatewayIP, nil
	}
	ip, err := resolveServerIP(server)
	if err != nil {
		return dialIP(), err
	}
	if old := dialIP(); old != ip {
		if old != "" {
			fmt.Printf("[*] Server %s moved: %s -> %s\n", server, old, ip)
		}
		setDialIP(ip)
	}
	// The GAME gets the resolved IP, never the name. CORRECTED 2026-09-27:
	// this passed the name, on the belief that the game resolves names itself.
	// It does not for the web-services gateway: WebServiceSessionManager
	// (0x14041D6E0) hands -GatewayAddress to FInternetAddr::SetIp (vtable +8),
	// which parses dotted IPv4 only, and a live client logged "Invalid
	// address: <name>, Port: 65443 ... Cannot init request".
	//
	// Firmament follows the server too. CHANGED 2026-10-21: a firmament_host
	// in dn-launcher.json used to win here, and the dn-launcher.json handed to
	// testers carried the server's outside IP of the day in it -- so after the
	// IP changed, the game went to the new gateway but the OLD Firmament
	// address. With a server setting, both now come from the DNS answer.
	cfg.GatewayIP = ip
	cfg.FirmamentHost = ip
	return ip, nil
}

// ---- CA certificate -----------------------------------------------------------

var procCertAddEncodedCertificateToStore = syscall.NewLazyDLL("crypt32.dll").NewProc("CertAddEncodedCertificateToStore")

// defaultCA is the server's CA built into the launcher (-ldflags
// "-X main.defaultCA=<base64 of ca.crt>"), so a tester downloads one exe. A
// ca.crt beside the launcher still wins, so a re-issued CA needs no rebuild.
var defaultCA = ""

// pendingCA is the CA runMachineSetup found. It is NOT installed there: the
// desktop window asks the player first (dnInstallCert), and the console flow
// installs it with Windows' own confirmation (ensureCAInstalled).
var pendingCA []byte

// readCACert loads ca.crt (PEM or DER) from beside the launcher, else the
// built-in one. Neither means "nothing to install".
func readCACert(exeDir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(exeDir, "ca.crt"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		if strings.TrimSpace(defaultCA) == "" {
			return nil, nil
		}
		if raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(defaultCA)); err != nil {
			return nil, fmt.Errorf("built-in CA: %w", err)
		}
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

func openUserRootStore() (windows.Handle, error) {
	name, _ := windows.UTF16PtrFromString("ROOT")
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER, uintptr(unsafe.Pointer(name)))
	if err != nil {
		return 0, fmt.Errorf("open the Trusted Root store: %w", err)
	}
	return store, nil
}

// caTrusted reports whether this exact certificate is in the current user's
// Trusted Root store.
func caTrusted(der []byte) bool {
	store, err := openUserRootStore()
	if err != nil {
		return false
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()
	want := sha256.Sum256(der)
	var ctx *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if err != nil || ctx == nil {
			return false
		}
		if sha256.Sum256(unsafe.Slice(ctx.EncodedCert, ctx.Length)) == want {
			_ = windows.CertFreeCertificateContext(ctx)
			return true
		}
	}
}

// caSummary is what the certificate prompt shows: the name and the SHA-256
// fingerprint a player can compare with the one the operator publishes.
func caSummary(der []byte) (name, fingerprint string) {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	if cert, err := x509.ParseCertificate(der); err == nil {
		name = cert.Subject.CommonName
	}
	return name, strings.Join(parts, ":")
}

// ensureCAInstalled adds the CA to the current user's Trusted Root store
// unless an identical certificate is already there.
func ensureCAInstalled(der []byte) error {
	if caTrusted(der) {
		fmt.Println("[+] Server certificate already trusted.")
		return nil
	}
	return installCA(der)
}

// installCA adds the CA to the current user's Trusted Root store. Windows
// shows its own confirmation; declining it returns an error.
func installCA(der []byte) error {
	store, err := openUserRootStore()
	if err != nil {
		return err
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()

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
	serverConfigured = true
	// A failed lookup is not fatal any more: the window says the server cannot
	// be reached, and the news and Play look the name up again.
	if ip, err := refreshServerIP(cfg); err != nil {
		fmt.Printf("[!] %v -- trying again for the news and on Play\n", err)
	} else {
		fmt.Printf("[*] Server: %s (%s)\n", server, ip)
	}
	if p := strings.TrimSpace(cfg.WebPort); p != "" && p != "443" {
		serverWebPort = p
		fmt.Printf("[*] Sign-in and news via port %s\n", p)
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
	pendingCA = der
	return nil
}
