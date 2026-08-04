//go:build windows

package main

import (
	"bytes"
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
	crypt32dll         = syscall.NewLazyDLL("crypt32.dll")
	kernel32dll        = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect   = crypt32dll.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32dll.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32dll.NewProc("LocalFree")
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

// dpapiDecrypt reverses dpapiEncrypt. It fails for a blob written by a
// different Windows user, which is the point: the launcher's stored session
// token is readable only by the account that signed in.
func dpapiDecrypt(data []byte) ([]byte, error) {
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
	ret, _, _ := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, // ppszDataDescr
		0, // pOptionalEntropy
		0, // pvReserved
		0, // pPromptStruct
		CRYPTPROTECT_UI_FORBIDDEN,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", syscall.GetLastError())
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

// loadOrCreatePlayerID returns this installation's account identity.
//
// This identity IS the account. The server has no other notion of who you are:
// the launcher sends sha256("steam:"+playerID) as the Steam ticket, the auth
// server hashes that again into a steam_id, and a steam_id it has not seen
// before auto-registers a brand new player. Losing the identity therefore
// silently abandons the account -- fresh credits, empty fleet, and the tutorial
// from the top.
//
// That used to be a real hazard, because the generated ID mixed in 16 random
// bytes. Nothing could reproduce it, so a missing or unreadable player.json
// meant the old account was gone for good. It is now derived only from stable
// machine and user identifiers, so regenerating produces the SAME ID as before
// and the account survives losing the file. Both hashes are one-way, so this is
// the only recovery route there can be.
//
// Precedence: PlayerID from dn-launcher.json (or DN_PLAYER_ID) wins outright,
// which is also how an account can be moved to another machine.
func loadOrCreatePlayerID(configured string) (string, error) {
	if id := strings.TrimSpace(configured); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(os.Getenv("DN_PLAYER_ID")); id != "" {
		return id, nil
	}
	return loadOrDerivePlayerID()
}

func loadOrDerivePlayerID() (string, error) {
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
		fmt.Fprintf(os.Stderr, "[!] Player identity file at %s is unreadable; re-deriving it.\n", fpath)
		fmt.Fprintf(os.Stderr, "[!] If your account looks new after this, set \"player_id\" in dn-launcher.json to your previous ID.\n")
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[!] Could not read %s (%v); re-deriving the identity.\n", fpath, err)
	}

	// Deterministic on purpose -- see the note on loadOrCreatePlayerID. Adding
	// entropy here is what made a lost player.json unrecoverable.
	hostname, _ := os.Hostname()
	username := os.Getenv("USERNAME")
	if username == "" {
		username = os.Getenv("USER")
	}
	seed := sha256.Sum256([]byte("dreadnought-ps:" + hostname + ":" + username))
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
	SkipOnboarding bool   `json:"skip_onboarding"`
	// AllowSteam drops the -NoSteam switch, letting the client keep its Steam
	// online subsystem alive. See the note where the switch is built.
	AllowSteam bool `json:"allow_steam"`
	// PlayerID pins the account identity. Leave empty to derive it from this
	// machine and user; set it to carry an account to another machine, or to
	// recover one after the identity file was lost.
	PlayerID string `json:"player_id"`
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
	configPath := filepath.Join(exeDir, "dn-launcher.json")
	//nolint:gosec // Path is the launcher's sibling config file in its own installation directory.
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			// A config that exists but does not parse silently reverted every
			// setting to its default, which is indistinguishable from having
			// no config at all. Say so.
			fmt.Fprintf(os.Stderr, "[!] %s is not valid JSON (%v); using defaults for everything.\n",
				configPath, err)
			return cfg
		}
		fmt.Printf("[+] Config: %s\n", configPath)
	default:
		// Not an error -- the defaults are a working configuration -- but the
		// operator who just edited a file somewhere else needs to know this is
		// the path that counts.
		fmt.Printf("[+] Config: none at %s, using defaults\n", configPath)
	}
	// DN_VERBOSE_LOG lets verbose logging be toggled per-launch (e.g. for a
	// one-off debugging session) without editing dn-launcher.json.
	if v := strings.TrimSpace(os.Getenv("DN_VERBOSE_LOG")); v != "" && v != "0" {
		cfg.VerboseLogging = true
	}
	// DN_SKIP_ONBOARDING is a debugging escape hatch that jumps straight to the
	// hangar. Onboarding is on by default so new players get the same first-run
	// experience they had on the live servers.
	if v := strings.TrimSpace(os.Getenv("DN_SKIP_ONBOARDING")); v != "" && v != "0" {
		cfg.SkipOnboarding = true
	}
	if v := strings.TrimSpace(os.Getenv("DN_ALLOW_STEAM")); v != "" && v != "0" {
		cfg.AllowSteam = true
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

	// Sign in with a real account when one is available, and fall back to the
	// derived machine identity otherwise.
	//
	// The fallback is what shipped before: an id derived from the machine and
	// user, hashed into a Steam ticket, which the server auto-registers on first
	// sight. It cannot move between PCs, cannot be shared, and quietly strands
	// the old save whenever the derivation changes. An account removes all of
	// that, so it is preferred whenever the player has one -- but existing
	// installs keep working untouched until they choose to sign in.
	var (
		jwtToken string
		username string
	)
	creds, haveCreds := loadCredentials()
	// A saved token is only worth reusing while it is still alive. It used to be
	// replayed unconditionally, so once it aged out every restart rewrote the
	// SAME dead token and the game failed with an opaque 401 -- and restarting
	// the launcher, the obvious thing to try, changed nothing. There is no
	// password or refresh token saved, so the only honest recovery is to ask the
	// player to sign in again.
	credsUsable := haveCreds && !signOutRequested() && !launcherTokenExpired(creds.Token)

	if strings.TrimSpace(cfg.PlayerID) != "" || os.Getenv("DN_PLAYER_ID") != "" {
		// An explicitly pinned identity wins, which is how an account is moved
		// or recovered.
		jwtToken, username = authenticateWithDerivedIdentity(cfg)
	} else if credsUsable {
		fmt.Printf("[*] Signed in as %s.\n", creds.Username)
		jwtToken, username = creds.Token, creds.Username
	} else {
		if signOutRequested() {
			clearCredentials()
		} else if haveCreds {
			fmt.Printf("[*] Your saved sign-in for %s has expired; please sign in again.\n", creds.Username)
			clearCredentials()
		}
		fmt.Println("[*] Opening the sign-in window...")
		creds, signInErr := runSignInUI(cfg.AuthURL)
		if signInErr != nil {
			fatalf("[!] Sign-in failed: %v", signInErr)
		}
		if saveErr := saveCredentials(creds); saveErr != nil {
			fmt.Printf("[!] Could not remember this sign-in (%v); you will be asked again next time.\n", saveErr)
		}
		fmt.Printf("[+] Signed in as %s.\n", creds.Username)
		jwtToken, username = creds.Token, creds.Username
	}
	_ = username

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
		//
		// SUSPECTED COST, and the reason for allow_steam: the player's name and
		// avatar come from Steam, not from us. There is no name field anywhere
		// in the mmog protocol -- YA_PlayerGet has none, and
		// YA_GetPlayersInformation's is exactly
		// infos/DisplayInfo/UnlockedFleetType/Elite/Rank -- so nothing we send
		// can fill them. A client log from an operator with Steam running shows
		// the subsystem loading, receiving stats for the real persona, and then
		// shutting down:
		//
		//	STEAM: Loading Steam SDK 1.32
		//	STEAM: FOnlineAsyncEventSteamStatsReceived bWasSuccessful: 1
		//	       User: [DC-Lan Party] DARKACE
		//	STEAM: OnlineSubsystemSteam::Shutdown()
		//
		// If the UI reads the persona through that subsystem, this switch is why
		// the name and the avatar are blank. Unproven -- hence opt-in rather
		// than a default change, because the timeout above is a real cost.
		// Set allow_steam in dn-launcher.json, or DN_ALLOW_STEAM=1.
	}
	if cfg.AllowSteam {
		fmt.Println("[+] Steam: leaving the client's Steam subsystem enabled (allow_steam)")
	} else {
		fmt.Println("[+] Steam: passing -NoSteam. Set allow_steam in dn-launcher.json, " +
			"or DN_ALLOW_STEAM=1, to keep it enabled.")
		args = append(args, "-NoSteam")
	}

	if cfg.SkipOnboarding {
		// UYDreadnoughtLocalPlayer's constructor reads
		// m_noOnboarding = FParse::Param(FCommandLine::Get(), "noonboarding"),
		// and every onboarding gate — including the tutorial check that
		// UUI_LoginGateScreen::EnterGame runs before it will leave the loading
		// screen — short-circuits to "satisfied" when it is set.
		//
		// This is off by default on purpose: new players are supposed to go
		// through onboarding, exactly as they did on the live servers. The
		// server persists the client's own onboarding save blob (YA_SaveGame ->
		// the SGD field of YA_PlayerGet), so progress sticks across logins
		// without skipping anything. Use this only to get straight to the
		// hangar while debugging.
		args = append(args, "-noonboarding")
		fmt.Println("[*] Onboarding disabled (DN_SKIP_ONBOARDING / skip_onboarding) — the tutorial gate is bypassed.")
	}

	if cfg.VerboseLogging {
		// Bumps every UE4 log category (LogNet, LogHTTP, LogOnline,
		// LogYMmogbrain, LogWebServicesPlugin, etc.) to Verbose, and forces
		// the log file to flush after every line so nothing is lost if the
		// client crashes mid-session. Substantially larger log files —
		// intended for one-off debugging, not routine play.
		//
		// LogYComVOComponent is deliberately held down: raising it past Verbose
		// makes the client crash. UYComVOComponent::PlayVoiceLineInternal
		// (YComVOComponent.cpp:461) logs "%s with %s" from two UObject names,
		// and it does so before validating them -- by the time the tutorial's
		// intro movie ends one of those objects has already been destroyed, so
		// its NamePrivate slot holds a recycled heap pointer and FName::ToString
		// indexes the name pool with garbage.
		//
		// Confirmed from a full-memory dump of that crash: access violation at
		// FName::ToString, the "FName" read from object+0x18 was the pointer
		// 0x000001f898b6f300, and the category's live verbosity byte was 7.
		// The guard is `if (5 < verbosity)`, so anything up to Verbose (5) is
		// safe and VeryVerbose (6) or All (7) is not.
		args = append(args, `-LogCmds=global verbose, LogYComVOComponent log`, "-forcelogflush")
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

// authenticateWithDerivedIdentity is the pre-account path: derive this
// machine's id and exchange it for a token. Kept for installs that pin
// player_id, and as the route by which an old account is recovered.
func authenticateWithDerivedIdentity(cfg Config) (jwtToken, username string) {
	fmt.Println("[*] Loading player identity...")
	playerID, err := loadOrCreatePlayerID(cfg.PlayerID)
	if err != nil {
		fatalf("[!] Failed to load player identity: %v", err)
	}
	fmt.Printf("[*] Player ID: %s\n", playerID)

	fmt.Printf("[*] Authenticating with %s ...\n", cfg.AuthURL)
	jwtToken, username, err = getJWT(cfg.AuthURL, playerID)
	if err != nil {
		fatalf("[!] Auth failed: %v", err)
	}
	fmt.Printf("[+] Authenticated as: %s\n", username)
	return jwtToken, username
}

// signOutRequested reports whether the player asked to switch accounts, via
// --sign-out on the command line or DN_SIGN_OUT in the environment.
func signOutRequested() bool {
	if strings.TrimSpace(os.Getenv("DN_SIGN_OUT")) != "" {
		return true
	}
	for _, arg := range os.Args[1:] {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--sign-out", "-sign-out", "/signout", "--logout":
			return true
		}
	}
	return false
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
