//go:build windows

package main

// Cluster CA trust. The game verifies the gateway and Firmament certificates
// against the Windows store, so a cluster with a self-signed CA needs one
// confirmation per CA — installed by this code into the CURRENT USER's
// Trusted Root store (no admin, no certmgr skill needed; Windows shows its
// own confirmation). Clusters with publicly trusted certificates skip this
// entirely (probePublicTrust), as do CAs already in the store.

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCertAddEncodedCertificateToStore = syscall.NewLazyDLL("crypt32.dll").NewProc("CertAddEncodedCertificateToStore")

// defaultCA is a CA built into the browser (-ldflags
// "-X main.defaultCA=<base64 of ca.crt>") for the operator's own cluster, so
// testers need no file. A directory/manual CA always wins per cluster.
var defaultCA = ""

// pendingCA is the cluster CA awaiting the player's confirmation. It is NOT
// installed silently: the window shows its fingerprint first.
var pendingCA []byte

// parseCAPEM decodes a PEM (or DER) CA certificate to DER, validating it.
func parseCAPEM(raw []byte) ([]byte, error) {
	der := bytes.TrimSpace(raw)
	if block, _ := pem.Decode(der); block != nil {
		der = block.Bytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("not a certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("not a CA certificate")
	}
	return der, nil
}

func bytes_TrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// readBuiltinCA loads the built-in default CA, if the operator baked one in.
func readBuiltinCA() ([]byte, error) {
	if strings.TrimSpace(defaultCA) == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(defaultCA))
	if err != nil {
		return nil, fmt.Errorf("built-in CA: %w", err)
	}
	return parseCAPEM(raw)
}

// probePublicTrust reports whether webURL's certificate already verifies
// against the system roots (publicly trusted cluster certificate). Called
// with a plain client so the cluster transport settings cannot interfere.
func probePublicTrust(webURL string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(webURL, "/") + "/health")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode < 500
}

// clusterCAFile is where a manually added server's pasted CA can live beside
// the exe (cluster-ca.crt), as an alternative to pasting it every time.
// Directory clusters never need it: their CA arrives with the listing.
func clusterCAFile(exeDir string) string {
	return filepath.Join(exeDir, "cluster-ca.crt")
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

// caSummary is what the trust prompt shows: the name and the SHA-256
// fingerprint to compare with the directory's value.
func caSummary(der []byte) (name, fingerprint string) {
	fingerprint = FingerprintDisplay(fingerprintHex(der))
	if cert, err := x509.ParseCertificate(der); err == nil {
		name = cert.Subject.CommonName
	}
	return name, fingerprint
}

func fingerprintHex(der []byte) string {
	sum := sha256.Sum256(der)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(sum)*2)
	for _, b := range sum {
		out = append(out, hexdigits[b>>4], hexdigits[b&0xf])
	}
	return string(out)
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
		return fmt.Errorf("install certificate (declined?): %v", callErr)
	}
	fmt.Println("[+] Server certificate installed.")
	return nil
}
