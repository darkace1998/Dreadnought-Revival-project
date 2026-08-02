package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"dn-dedicated/internal/api"
	"dn-dedicated/internal/gamedata"
	"dn-dedicated/internal/master"
	"dn-dedicated/internal/server"
)

// ---------------------------------------------------------------- run

// engineLogCmdsUsage documents --engine-log-cmds, which is passed straight to
// the engine as -LogCmds.
//
// Never raise LogYComVOComponent past Verbose. Above that the process crashes
// in UYComVOComponent::PlayVoiceLineInternal, which logs two UObject names
// before validating them; by the end of a voice line one of them is already
// destroyed. So a "global" raise has to exempt it:
//
//	--engine-log-cmds "global verbose, LogYComVOComponent log"
const engineLogCmdsUsage = `engine -LogCmds value, e.g. "global verbose, LogYComVOComponent log" (never raise LogYComVOComponent past Verbose -- it crashes)`

func cmdRun(argv []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `dn-dedicated run - launch one headless battle server in the foreground

The process runs until the engine exits or you press Ctrl+C. Nothing is drawn
and no GPU is used (-nullrhi), so this is safe on a headless host.

`)
		fs.PrintDefaults()
	}

	var (
		mapName    = fs.String("map", getenv("DN_MAP", gamedata.DefaultMap), "map name, or a full /Game/... package path")
		mode       = fs.String("mode", getenv("DN_GAME_MODE", gamedata.DefaultGameMode), "game mode (see: dn-dedicated modes)")
		port       = fs.Int("port", getenvInt("DN_PORT", 7777), "UDP port for the battle server")
		maxPlayers = fs.Int("max-players", getenvInt("DN_MAX_PLAYERS", 10), "maximum players")
		gameBinary = fs.String("game-binary", "", "path to "+binaryName+" (default: GAME_BINARY, else auto-detect)")
		wineExe    = fs.String("wine", defaultWineExe(), `wine executable, or "none" to exec directly`)
		serverIP   = fs.String("server-ip", getenv("SERVER_IP", "127.0.0.1"), "address clients connect to; also what is registered")
		register   = fs.Bool("register", false, "register with master-server and send heartbeats")
		masterURL  = fs.String("master-url", getenv("MASTER_URL", "http://127.0.0.1:8084"), "master-server base URL (with --register)")
		key        = fs.String("internal-key", internalKeyFromEnv(), "X-Internal-Key for master-server (with --register)")
		verbose    = fs.Bool("verbose", false, "forward every line of engine output, not just errors and state changes")
		readyWait  = fs.Duration("ready-timeout", 120*time.Second, "how long to wait for the server to report it is hosting")
		logDir     = fs.String("log-dir", defaultLogDir(), "directory for per-instance battle server logs")
		showWindow = fs.Bool("show-window", false, "leave the engine's game window visible (debugging)")
		logCmds    = fs.String("engine-log-cmds", getenv("DN_ENGINE_LOG_CMDS", ""), engineLogCmdsUsage)
	)
	var extraArgs, urlOptions stringList
	fs.Var(&extraArgs, "extra-arg", "extra engine argument, repeatable (advanced)")
	fs.Var(&urlOptions, "url-option", "extra map URL option appended as ?opt, repeatable (advanced)")

	if err := fs.Parse(argv); err != nil {
		return err
	}

	binary, err := findGameBinary(*gameBinary)
	if err != nil {
		return err
	}
	gameMap, err := gamedata.LookupMap(*mapName)
	if err != nil {
		return err
	}
	canonicalMode, err := gamedata.NormalizeGameMode(*mode)
	if err != nil {
		return err
	}

	var masterClient *master.Client
	if *register {
		if placeholderKeys[*key] {
			return fmt.Errorf("--register needs a real --internal-key (or INTERNAL_API_KEY / ADMIN_KEY); it must match master-server's")
		}
		masterClient = master.New(strings.TrimRight(*masterURL, "/"), *key)
	}

	mgr := server.NewManager(server.ManagerConfig{
		GameBinary:    binary,
		WineExe:       *wineExe,
		ServerIP:      *serverIP,
		PortStart:     *port,
		PortEnd:       *port,
		MaxPlayers:    *maxPlayers,
		Master:        masterClient,
		LogDir:        *logDir,
		ShowWindow:    *showWindow,
		Verbose:       *verbose,
		LogTo:         os.Stderr,
		EngineLogCmds: *logCmds,
	})

	fmt.Printf("Dreadnought dedicated server (headless)\n")
	fmt.Printf("  binary   %s\n", binary)
	fmt.Printf("  map      %s  (%s)\n", gameMap.Name, gameMap.Path)
	fmt.Printf("  mode     %s\n", canonicalMode)
	fmt.Printf("  bind     %s:%d/udp\n", *serverIP, *port)
	fmt.Printf("  players  %d max\n", *maxPlayers)
	fmt.Printf("  logs     %s\n", *logDir)
	if *register {
		fmt.Printf("  master   %s (heartbeat every %s)\n", *masterURL, master.HeartbeatInterval)
	}
	fmt.Println()

	inst, err := mgr.Start(server.StartOptions{
		Map:        gameMap,
		GameMode:   canonicalMode,
		Port:       *port,
		MaxPlayers: *maxPlayers,
		// A launched-by-hand server has no pre-formed roster; players join by
		// connecting. The field exists for API-driven launches.
		Players:    nil,
		ExtraArgs:  extraArgs,
		URLOptions: urlOptions,
	})
	if err != nil {
		return err
	}

	fmt.Printf("started instance %s (pid %d), waiting for it to start hosting ...\n", inst.ID, inst.PID())
	if inst.LogPath != "" {
		fmt.Printf("instance log: %s\n", inst.LogPath)
	}

	readyCtx, cancelReady := context.WithTimeout(context.Background(), *readyWait)
	readyErr := inst.WaitReady(readyCtx)
	cancelReady()

	if readyErr != nil {
		// Stop whatever is left so we do not leak a half-started process.
		_ = mgr.Stop(inst.ID)
		hint := "  Check the engine's own log for the reason."
		if inst.LogPath != "" {
			hint = "  Check the engine's own log: " + inst.LogPath
		}
		return fmt.Errorf("server did not come up: %w\n%s\n"+
			"  A libcef.dll error there means the working directory was wrong.", readyErr, hint)
	}

	fmt.Printf("READY - accepting connections on %s:%d/udp\n", *serverIP, inst.Port)
	fmt.Printf("Press Ctrl+C to stop.\n\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case <-inst.Done():
		if err := inst.ExitErr(); err != nil {
			return fmt.Errorf("battle server exited: %w", err)
		}
		fmt.Println("battle server exited.")
	case <-sig:
		fmt.Println("\nstopping ...")
		mgr.Shutdown()
		fmt.Println("stopped.")
	}
	return nil
}

// ---------------------------------------------------------------- serve

func cmdServe(argv []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `dn-dedicated serve - HTTP control plane for battle servers

Exposes the same routes as the Revival project's game-manager, so mmogbrain's
matchmaker can drive it unchanged:

  POST   /instances       X-Internal-Key   launch a match
  GET    /instances                        list running matches
  DELETE /instances/{id}  X-Internal-Key   stop a match
  GET    /health

`)
		fs.PrintDefaults()
	}

	var (
		addr       = fs.String("addr", getenv("ADDR", ":8085"), "listen address")
		gameBinary = fs.String("game-binary", "", "path to "+binaryName+" (default: GAME_BINARY, else auto-detect)")
		wineExe    = fs.String("wine", defaultWineExe(), `wine executable, or "none" to exec directly`)
		serverIP   = fs.String("server-ip", getenv("SERVER_IP", "127.0.0.1"), "address handed to clients")
		portStart  = fs.Int("port-start", getenvInt("PORT_RANGE_START", 7777), "first UDP port in the pool")
		portEnd    = fs.Int("port-end", getenvInt("PORT_RANGE_END", 7877), "last UDP port in the pool")
		maxPlayers = fs.Int("max-players", getenvInt("DN_MAX_PLAYERS", 10), "default maximum players per match")
		key        = fs.String("internal-key", internalKeyFromEnv(), "required X-Internal-Key for write routes")
		register   = fs.Bool("register", false, "register launched matches with master-server")
		masterURL  = fs.String("master-url", getenv("MASTER_URL", "http://127.0.0.1:8084"), "master-server base URL")
		verbose    = fs.Bool("verbose", false, "forward every line of engine output")
		logDir     = fs.String("log-dir", defaultLogDir(), "directory for per-instance battle server logs")
		allowMock  = fs.Bool("allow-mock", false, "record a mock instance when no game process can be started (game-manager compatibility)")
		showWindow = fs.Bool("show-window", false, "leave the engine's game window visible (debugging)")
		logCmds    = fs.String("engine-log-cmds", getenv("DN_ENGINE_LOG_CMDS", ""), engineLogCmdsUsage)
	)
	var defaultURLOptions stringList
	fs.Var(&defaultURLOptions, "url-option", `map URL option added to every instance, repeatable, e.g. "ylevelvariation=1"`)
	if err := fs.Parse(argv); err != nil {
		return err
	}

	// game-manager refuses to start without a real key, because its write routes
	// can spawn unbounded processes and kill running matches. Same reasoning
	// applies here, so the same refusal.
	if placeholderKeys[*key] {
		return fmt.Errorf("INTERNAL_API_KEY (or ADMIN_KEY, or --internal-key) must be set to a real secret\n" +
			"  The write routes can spawn and kill game processes; they are not safe unauthenticated.")
	}
	if *portEnd < *portStart {
		return fmt.Errorf("--port-end (%d) is below --port-start (%d)", *portEnd, *portStart)
	}

	// With --allow-mock the whole point is to run without a game binary, so a
	// failed lookup is a warning rather than a fatal error.
	binary, err := findGameBinary(*gameBinary)
	if err != nil {
		if !*allowMock {
			return err
		}
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		fmt.Fprintln(os.Stderr, "continuing with --allow-mock: matches will be recorded but nothing will be hosted.")
		binary = ""
	}

	var masterClient *master.Client
	if *register {
		masterClient = master.New(strings.TrimRight(*masterURL, "/"), *key)
	}

	mgr := server.NewManager(server.ManagerConfig{
		GameBinary:        binary,
		WineExe:           *wineExe,
		ServerIP:          *serverIP,
		PortStart:         *portStart,
		PortEnd:           *portEnd,
		MaxPlayers:        *maxPlayers,
		Master:            masterClient,
		LogDir:            *logDir,
		AllowMock:         *allowMock,
		ShowWindow:        *showWindow,
		Verbose:           *verbose,
		LogTo:             os.Stderr,
		EngineLogCmds:     *logCmds,
		DefaultURLOptions: defaultURLOptions,
	})

	apiSrv := &api.Server{
		Manager:     mgr,
		InternalKey: *key,
		ServerIP:    *serverIP,
		Log:         os.Stderr,
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           apiSrv.Handler(),
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("dn-dedicated control plane\n")
	fmt.Printf("  listen   %s\n", *addr)
	if binary == "" {
		fmt.Printf("  binary   (none -- MOCK MODE, no matches will actually be hosted)\n")
	} else {
		fmt.Printf("  binary   %s\n", binary)
	}
	fmt.Printf("  ports    %d-%d (%d slots)\n", *portStart, *portEnd, *portEnd-*portStart+1)
	fmt.Printf("  clients  connect to %s\n", *serverIP)
	fmt.Printf("  logs     %s\n", *logDir)
	if *register {
		fmt.Printf("  master   %s\n", *masterURL)
	}
	fmt.Println()

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	case <-sig:
		fmt.Println("\nshutting down ...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "http shutdown: %v\n", err)
	}
	mgr.Shutdown()
	fmt.Println("stopped.")
	return nil
}

// ---------------------------------------------------------------- client commands

// controlFlags are the flags every client command shares.
type controlFlags struct {
	addr *string
	key  *string
}

func addControlFlags(fs *flag.FlagSet) controlFlags {
	return controlFlags{
		addr: fs.String("addr", getenv("DN_DEDICATED_URL", "http://127.0.0.1:8085"), "control plane base URL"),
		key:  fs.String("internal-key", internalKeyFromEnv(), "X-Internal-Key for write routes"),
	}
}

func cmdStart(argv []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	ctl := addControlFlags(fs)
	var (
		mapName    = fs.String("map", gamedata.DefaultMap, "map name")
		mode       = fs.String("mode", gamedata.DefaultGameMode, "game mode")
		maxPlayers = fs.Int("max-players", 0, "maximum players (0 = server default)")
	)
	var players stringList
	fs.Var(&players, "player", "player id, repeatable (at least one is required)")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	gameMap, err := gamedata.LookupMap(*mapName)
	if err != nil {
		return err
	}
	canonicalMode, err := gamedata.NormalizeGameMode(*mode)
	if err != nil {
		return err
	}
	if len(players) == 0 {
		// The API requires a non-empty roster (game-manager does too). For a
		// hand-started server there is no real roster, so name the operator
		// rather than silently fabricating a player id that looks real.
		players = stringList{"operator"}
	}

	body := map[string]interface{}{
		"game_mode": canonicalMode,
		"map":       gameMap.Name,
		"map_path":  gameMap.Path,
		"players":   []string(players),
	}
	if *maxPlayers > 0 {
		body["max_players"] = *maxPlayers
	}

	var out map[string]interface{}
	if err := callAPI(http.MethodPost, *ctl.addr+"/instances", *ctl.key, body, &out); err != nil {
		return err
	}
	fmt.Printf("started instance %v\n", out["instance_id"])
	fmt.Printf("  match  %v\n", out["match_id"])
	fmt.Printf("  map    %v (%v)\n", out["map"], out["map_path"])
	fmt.Printf("  mode   %v\n", out["game_mode"])
	fmt.Printf("  addr   %v:%v/udp\n", out["ip"], formatNumber(out["port"]))
	return nil
}

func cmdStop(argv []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	ctl := addControlFlags(fs)
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: dn-dedicated stop <instance-id>")
	}
	if err := callAPI(http.MethodDelete, *ctl.addr+"/instances/"+fs.Arg(0), *ctl.key, nil, nil); err != nil {
		return err
	}
	fmt.Printf("stopped %s\n", fs.Arg(0))
	return nil
}

func cmdList(argv []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	ctl := addControlFlags(fs)
	if err := fs.Parse(argv); err != nil {
		return err
	}

	var out struct {
		Instances []struct {
			ID        string   `json:"id"`
			MatchID   string   `json:"match_id"`
			Port      int      `json:"port"`
			GameMode  string   `json:"game_mode"`
			Map       string   `json:"map"`
			Players   []string `json:"players"`
			StartedAt string   `json:"started_at"`
			PID       int      `json:"pid"`
		} `json:"instances"`
		Count     int `json:"count"`
		PortsUsed int `json:"ports_used"`
	}
	if err := callAPI(http.MethodGet, *ctl.addr+"/instances", *ctl.key, nil, &out); err != nil {
		return err
	}
	if out.Count == 0 {
		fmt.Println("no battle servers running")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tPID\tPORT\tMODE\tMAP\tPLAYERS\tSTARTED")
	for _, i := range out.Instances {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%d\t%s\n",
			i.ID, i.PID, i.Port, i.GameMode, i.Map, len(i.Players), i.StartedAt)
	}
	_ = tw.Flush()
	fmt.Printf("\n%d running, %d ports in use\n", out.Count, out.PortsUsed)
	return nil
}

func cmdStatus(argv []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	ctl := addControlFlags(fs)
	if err := fs.Parse(argv); err != nil {
		return err
	}
	var out map[string]interface{}
	if err := callAPI(http.MethodGet, *ctl.addr+"/health", *ctl.key, nil, &out); err != nil {
		return err
	}
	fmt.Printf("service    %v\n", out["service"])
	fmt.Printf("status     %v\n", out["status"])
	fmt.Printf("instances  %v\n", formatNumber(out["instances"]))
	fmt.Printf("ports      %v/%v in use\n", formatNumber(out["ports_used"]), formatNumber(out["capacity"]))
	return nil
}

// callAPI performs a control-plane request. The key is only sent when set, so
// read-only calls work against a control plane without knowing its secret.
func callAPI(method, url, key string, in, out interface{}) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("X-Internal-Key", key)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w\n  Is `dn-dedicated serve` running?", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%s: %s (set --internal-key to match the control plane)", resp.Status, e.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// formatNumber renders a JSON number without the float64 ".0" suffix.
func formatNumber(v interface{}) string {
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", v)
}

// ---------------------------------------------------------------- informational

func cmdMaps(argv []string) error {
	fs := flag.NewFlagSet("maps", flag.ExitOnError)
	if err := fs.Parse(argv); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MAP\tTYPE\tPACKAGE PATH")
	for _, m := range gamedata.Maps() {
		kind := "multiplayer"
		if m.PvE {
			kind = "pve"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Name, kind, m.Path)
	}
	_ = tw.Flush()
	fmt.Print(`
These are the entries flagged available in the client's own GlobalUI.uasset
table. A full /Game/... package path is also accepted by --map, for maps this
table does not list (night variants, Havoc). Names not in the client's data --
Charon, Medusa, Procyon, Iapetus, Kalyke -- are not maps this game has.
`)
	return nil
}

func cmdModes(argv []string) error {
	fs := flag.NewFlagSet("modes", flag.ExitOnError)
	if err := fs.Parse(argv); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODE\tTEAM SIZE")
	for _, name := range gamedata.GameModeNames() {
		fmt.Fprintf(tw, "%s\t%d\n", name, gamedata.TeamSize(name))
	}
	_ = tw.Flush()

	fmt.Println("\nAliases accepted:")
	tw = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, pair := range gamedata.GameModeAliases() {
		fmt.Fprintf(tw, "  %s\t->  %s\n", pair[0], pair[1])
	}
	_ = tw.Flush()
	return nil
}

// cmdArgs prints the argv `run` would use. Useful for reproducing a launch by
// hand, which is how the working argv was found in the first place.
func cmdArgs(argv []string) error {
	fs := flag.NewFlagSet("args", flag.ExitOnError)
	var (
		mapName    = fs.String("map", gamedata.DefaultMap, "map name or /Game/... path")
		mode       = fs.String("mode", gamedata.DefaultGameMode, "game mode")
		port       = fs.Int("port", 7777, "UDP port")
		maxPlayers = fs.Int("max-players", 10, "maximum players")
		gameBinary = fs.String("game-binary", "", "path to "+binaryName)
	)
	if err := fs.Parse(argv); err != nil {
		return err
	}

	gameMap, err := gamedata.LookupMap(*mapName)
	if err != nil {
		return err
	}
	canonicalMode, err := gamedata.NormalizeGameMode(*mode)
	if err != nil {
		return err
	}
	// Tolerate a missing binary here: this command is for inspecting the argv,
	// which is useful even on a machine with no game installed.
	binary, err := findGameBinary(*gameBinary)
	if err != nil {
		binary = filepath.Join("<path to>", binaryName)
	}

	args := server.BuildArgs(server.LaunchConfig{
		Map:        gameMap,
		GameMode:   canonicalMode,
		Port:       *port,
		MaxPlayers: *maxPlayers,
	}, "<match-uuid>")

	fmt.Printf("working directory: %s\n\n", filepath.Dir(binary))
	fmt.Printf("%s \\\n", quoteIfNeeded(binary))
	for i, a := range args {
		sep := " \\"
		if i == len(args)-1 {
			sep = ""
		}
		fmt.Printf("  %s%s\n", quoteIfNeeded(a), sep)
	}
	fmt.Print(`
The working directory matters: the engine resolves libcef.dll and its other
co-located DLLs relative to it, and launching from anywhere else exits with
status 3 within seconds.
`)
	return nil
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
