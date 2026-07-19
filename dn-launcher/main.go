//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// ---- TLS configuration --------------------------------------------------

func buildTLSConfig() *tls.Config {
	fingerprint := strings.TrimSpace(os.Getenv("TLS_CERT_FINGERPRINT"))
	if fingerprint == "" {
		fmt.Fprintln(os.Stderr, "[!] TLS_CERT_FINGERPRINT not set — certificate verification is disabled.")
		fmt.Fprintln(os.Stderr, "[!] Set TLS_CERT_FINGERPRINT to the SHA256 hex fingerprint of the server certificate for security.")
		fmt.Fprintln(os.Stderr, "[!] Find it with: openssl x509 -in server.crt -fingerprint -sha256 -noout")
		//nolint:gosec // Intentional fallback for private servers without configured fingerprint.
		return &tls.Config{InsecureSkipVerify: true}
	}
	expected := strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			for _, raw := range rawCerts {
				cert, err := x509.ParseCertificate(raw)
				if err != nil {
					continue
				}
				sum := sha256.Sum256(cert.Raw)
			actual := hex.EncodeToString(sum[:])
				if strings.ToLower(actual) == expected {
					return nil
				}
			}
			return fmt.Errorf("server certificate fingerprint does not match TLS_CERT_FINGERPRINT")
		},
	}
}

// ---- DPAPI (Windows CryptProtectData) --------------------------------

var (
	crypt32dll       = syscall.NewLazyDLL("crypt32.dll")
	kernel32dll      = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect = crypt32dll.NewProc("CryptProtectData")
	procLocalFree    = kernel32dll.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

// dpapiEncrypt wraps CryptProtectData (user scope, no UI).
func dpapiEncrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	if len(data) > int(^uint32(0)) {
		return nil, fmt.Errorf("data too large")
	}
	inBlob := dataBlob{
		//nolint:gosec // Length is bounded above and DPAPI requires a uint32 byte count.
		cbData: uint32(len(data)),
		pbData: &data[0],
	}
	var outBlob dataBlob
	const CRYPTPROTECT_UI_FORBIDDEN = 1
	ret, _, _ := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, // szDataDescr
		0, // pOptionalEntropy
		0, // pvReserved
		0, // pPromptStruct
		CRYPTPROTECT_UI_FORBIDDEN,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", syscall.GetLastError())
	}
	result := make([]byte, outBlob.cbData)
	copy(result, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
	return result, nil
}

// ---- Player identity --------------------------------------------------

type playerIdentity struct {
	PlayerID string `json:"player_id"`
}

// loadOrCreatePlayerID returns a stable ID derived from machine+user info,
// persisted in %LOCALAPPDATA%\DreadnoughtPS\player.json.
func loadOrCreatePlayerID() (string, error) {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "DreadnoughtPS")
	//nolint:gosec // Path is confined to the current user's LOCALAPPDATA/temp private launcher directory.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create player identity dir: %w", err)
	}
	fpath := filepath.Join(dir, "player.json")

	//nolint:gosec // Path is derived from LOCALAPPDATA/temp and a fixed filename under the launcher's private directory.
	data, err := os.ReadFile(fpath)
	if err == nil {
		var ident playerIdentity
		if json.Unmarshal(data, &ident) == nil && ident.PlayerID != "" {
			return ident.PlayerID, nil
		}
		fmt.Fprintf(os.Stderr, "[!] Player identity file corrupted, regenerating...\n")
	} else {
		fmt.Fprintf(os.Stderr, "[!] No player identity file found, generating new identity...\n")
	}

	hostname, _ := os.Hostname()
	username := os.Getenv("USERNAME")
	entropy := make([]byte, 16)
	rand.Read(entropy)
	seed := sha256.Sum256([]byte(hostname + ":" + username + ":" + hex.EncodeToString(entropy)))
	id := hex.EncodeToString(seed[:16])

	out, err := json.Marshal(playerIdentity{PlayerID: id})
	if err != nil {
		return "", fmt.Errorf("marshal player identity: %w", err)
	}
	//nolint:gosec // Path is confined to the launcher-managed player identity file under LOCALAPPDATA/temp.
	if err := os.WriteFile(fpath, out, 0o600); err != nil {
		return "", fmt.Errorf("write player identity: %w", err)
	}
	return id, nil
}

// ---- Auth ------------------------------------------------------------

type authRequest struct {
	ID      string     `json:"id"`
	JSONRPC string     `json:"jsonrpc"`
	Method  string     `json:"method"`
	Params  authParams `json:"params"`
}

type authParams struct {
	Ticket        string `json:"ticket"`
	AppID         int    `json:"appid"`
	Audience      string `json:"audience"`
	Realm         string `json:"realm"`
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
}

type authResponse struct {
	ID      string            `json:"id"`
	JSONRPC string            `json:"jsonrpc"`
	Result  []json.RawMessage `json:"result"`
}

type userObj struct {
	JWT         string `json:"jwt"`
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
}

func getJWT(authURL, playerID string) (token, username string, err error) {
	ticketHash := sha256.Sum256([]byte("steam:" + playerID))
	ticket := hex.EncodeToString(ticketHash[:])

	req := authRequest{
		ID:      "dn-launcher-001",
		JSONRPC: "2.0",
		Method:  "jwt.get.by_steam_ticket",
		Params: authParams{
			Ticket:        ticket,
			AppID:         835860,
			Audience:      "launcher",
			Realm:         "dreadnought.pc-us",
			ClientName:    "Dreadnought Launcher",
			ClientVersion: "live",
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("marshal auth request: %w", err)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: buildTLSConfig(),
		},
	}
	resp, err := client.Post(authURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("POST %s: %w", authURL, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("auth server returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", "", fmt.Errorf("read response: %w", err)
	}

	var ar authResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return "", "", fmt.Errorf("parse response: %w\nbody: %s", err, respBody)
	}
	if len(ar.Result) < 2 {
		return "", "", fmt.Errorf("unexpected result: %s", respBody)
	}

	var user userObj
	if err := json.Unmarshal(ar.Result[1], &user); err != nil {
		return "", "", fmt.Errorf("parse user: %w", err)
	}

	token = user.JWT
	if token == "" {
		token = user.AccessToken
	}
	if token == "" {
		token = user.Token
	}
	if token == "" {
		return "", "", fmt.Errorf("no token in response: %s", respBody)
	}
	return token, user.Username, nil
}

// ---- Registry --------------------------------------------------------

const (
	regPath      = `SOFTWARE\Grey Box\Dreadnought`
	regAuthToken = "AuthToken"
)

func writeAuthToken(jwtToken string) error {
	encrypted, err := dpapiEncrypt([]byte(jwtToken))
	if err != nil {
		return fmt.Errorf("DPAPI encrypt: %w", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create registry key: %w", err)
	}
	defer func() {
		_ = k.Close()
	}()
	return k.SetBinaryValue(regAuthToken, encrypted)
}

// ---- Config ----------------------------------------------------------

type Config struct {
	AuthURL        string `json:"auth_url"`
	GatewayIP      string `json:"gateway_ip"`
	GatewayPort    string `json:"gateway_port"`
	FirmamentHost  string `json:"firmament_host"`
	FirmamentPort  string `json:"firmament_port"`
	GamePath       string `json:"game_path"`
	VerboseLogging bool   `json:"verbose_logging"`
}

func defaultConfig() Config {
	return Config{
		AuthURL:       "https://profile-api.prod.greybox.sixfoot.live/auth/",
		GatewayIP:     "10.0.0.73",
		GatewayPort:   "65443",
		FirmamentHost: "", // empty = use GatewayIP (avoids need for hosts file entry)
		FirmamentPort: "48843",
	}
}

func loadConfig(exeDir string) Config {
	cfg := defaultConfig()
	//nolint:gosec // Path is the launcher's sibling config file in its own installation directory.
	data, err := os.ReadFile(filepath.Join(exeDir, "dn-launcher.json"))
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg
		}
	}
	// DN_VERBOSE_LOG lets verbose logging be toggled per-launch (e.g. for a
	// one-off debugging session) without editing dn-launcher.json.
	if v := strings.TrimSpace(os.Getenv("DN_VERBOSE_LOG")); v != "" && v != "0" {
		cfg.VerboseLogging = true
	}
	return cfg
}

// ---- Game binary detection -------------------------------------------

// findGameBinary locates the game binary to launch, preferring the patched build
// (DreadGame-Win64-Shipping-patched.exe) which bypasses Firmament TLS cert pinning.
// Falls back to the original binary, then to the EAC wrapper.
func findGameBinary(exeDir string, cfg Config) string {
	if cfg.GamePath != "" {
		if _, err := os.Stat(cfg.GamePath); err == nil {
			return cfg.GamePath
		}
	}

	// Paths relative to the Dreadnought install root.
	// The actual game binary lives in DreadGame\DreadGame\Binaries\Win64\
	// Prefer the patched build (cert pinning bypassed) over the original.
	candidates := []string{
		filepath.Join(exeDir, `DreadGame`, `DreadGame`, `Binaries`, `Win64`, `DreadGame-Win64-Shipping-patched.exe`),
		filepath.Join(exeDir, `DreadGame`, `DreadGame`, `Binaries`, `Win64`, `DreadGame-Win64-Shipping.exe`),
		// EAC wrapper fallback (may reject patched binary via integrity check)
		filepath.Join(exeDir, `Launcher_DreadGame-Win64-Shipping.exe`),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// Steam registry fallback — look in the registered install location.
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Steam App 835860`,
		registry.QUERY_VALUE,
	)
	if err == nil {
		defer func() {
			_ = k.Close()
		}()
		if installDir, _, err := k.GetStringValue("InstallLocation"); err == nil && installDir != "" {
			for _, rel := range []string{
				filepath.Join(`DreadGame`, `DreadGame`, `Binaries`, `Win64`, `DreadGame-Win64-Shipping-patched.exe`),
				filepath.Join(`DreadGame`, `DreadGame`, `Binaries`, `Win64`, `DreadGame-Win64-Shipping.exe`),
				`Launcher_DreadGame-Win64-Shipping.exe`,
			} {
				p := filepath.Join(installDir, rel)
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	return ""
}

// ---- Main ------------------------------------------------------------

func main() {
	fmt.Println("=================================================")
	fmt.Println("  Dreadnought Private Server Launcher v1.0")
	fmt.Println("=================================================")
	fmt.Println()

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	cfg := loadConfig(exeDir)

	fmt.Println("[*] Loading player identity...")
	playerID, err := loadOrCreatePlayerID()
	if err != nil {
		fatalf("[!] Failed to load player identity: %v", err)
	}
	fmt.Printf("[*] Player ID: %s\n", playerID)

	fmt.Printf("[*] Authenticating with %s ...\n", cfg.AuthURL)
	jwtToken, username, err := getJWT(cfg.AuthURL, playerID)
	if err != nil {
		fatalf("[!] Auth failed: %v", err)
	}
	fmt.Printf("[+] Authenticated as: %s\n", username)

	fmt.Println("[*] Writing auth token to registry...")
	if err := writeAuthToken(jwtToken); err != nil {
		fatalf("[!] Registry write failed: %v", err)
	}
	fmt.Println("[+] Auth token written.")

	gamePath := findGameBinary(exeDir, cfg)
	if gamePath == "" {
		fmt.Fprintln(os.Stderr, "[!] Could not find game binary.")
		fmt.Fprintln(os.Stderr, "    Place dn-launcher.exe in your Dreadnought install directory")
		fmt.Fprintln(os.Stderr, "    and copy DreadGame-Win64-Shipping-patched.exe into")
		fmt.Fprintln(os.Stderr, "    DreadGame\\DreadGame\\Binaries\\Win64\\")
		fmt.Fprintln(os.Stderr, "    Or set 'game_path' in dn-launcher.json to the full path.")
		waitExit(1)
	}
	fmt.Printf("[*] Game binary: %s\n", gamePath)

	// FirmamentHost defaults to GatewayIP so no hosts-file entry is needed.
	// The cert now includes IP:10.0.0.73 as a SAN so TLS verification passes.
	firmamentHost := cfg.FirmamentHost
	if firmamentHost == "" {
		firmamentHost = cfg.GatewayIP
	}

	args := []string{
		"-LOG",
		"-GatewayAddress=" + cfg.GatewayIP,
		"-GatewayPort=" + cfg.GatewayPort,
		"-YFirmamentAddress=" + firmamentHost,
		"-YFirmamentPort=" + cfg.FirmamentPort,
		"-noeac",
		// DreadGame/Config/DefaultEngine.ini sets NativePlatformService=Steam,
		// so the client still initializes the real Steam online subsystem for
		// achievements/presence alongside our Mmogbrain subsystem even though
		// matchmaking doesn't need it. Against a private server, real Steam
		// calls (e.g. GetAchievementAndUnlockTime) can't succeed and appear to
		// block on a long timeout rather than failing fast — -NoSteam skips
		// Steam subsystem init entirely, matching the flag the project's own
		// dedicated-server launch command already uses (see README.md).
		"-NoSteam",
	}

	if cfg.VerboseLogging {
		// Bumps every UE4 log category (LogNet, LogHTTP, LogOnline,
		// LogYMmogbrain, LogWebServicesPlugin, etc.) to Verbose, and forces
		// the log file to flush after every line so nothing is lost if the
		// client crashes mid-session. Substantially larger log files —
		// intended for one-off debugging, not routine play.
		args = append(args, `-LogCmds=global verbose`, "-forcelogflush")
		fmt.Println("[*] Verbose logging enabled (DN_VERBOSE_LOG / verbose_logging) — expect much larger client log files.")
	}

	fmt.Printf("[*] Launching: %s\n", gamePath)
	fmt.Printf("    Args: %s\n", strings.Join(args, " "))
	fmt.Println()

	//nolint:gosec // gamePath is resolved from the local install/config and args are explicit argv values, not shell-expanded input.
	cmd := exec.Command(gamePath, args...)
	cmd.Dir = filepath.Dir(gamePath) // Dreadnought install root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fatalf("[!] Failed to launch game: %v", err)
	}
	fmt.Printf("[+] Game launched (PID %d). Launcher exiting.\n", cmd.Process.Pid)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	waitExit(1)
}

func waitExit(code int) {
	fmt.Println("\nPress Enter to exit (or wait 5 seconds)...")
	done := make(chan struct{})
	go func() {
		_, _ = fmt.Scanln()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	os.Exit(code)
}
