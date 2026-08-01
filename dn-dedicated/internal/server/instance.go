// Package server launches and supervises headless Dreadnought battle servers.
package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"dn-dedicated/internal/gamedata"
	"dn-dedicated/internal/ids"
)

// LaunchConfig is everything needed to start one battle server.
type LaunchConfig struct {
	GameBinary string // absolute path to DreadGame-Win64-Shipping.exe
	WineExe    string // "" or "none" to exec directly (the Windows path)
	Map        gamedata.Map
	GameMode   string // canonical client mode name, e.g. "TDM"
	Port       int
	MaxPlayers int
	Players    []string // player ids the match was formed for; informational
	ExtraArgs  []string // appended verbatim, after the standard argv
	URLOptions []string // appended to the map URL as ?k=v, after ?listen

	// LogPath is where this process writes the battle server's captured output.
	// See newLogWriter for why the log is captured here rather than delegated
	// to the engine's own -ABSLOG switch.
	LogPath string

	// AllowMock restores game-manager's behaviour of recording an instance even
	// when no process could be started. Off by default; see Launch.
	AllowMock bool

	// ShowWindow leaves the engine's game window visible. Off by default: a
	// dedicated server should not put a window on screen. See
	// hidewindow_windows.go for why -nullrhi alone does not achieve this.
	ShowWindow bool

	// Verbose forwards every line of the child's output instead of only lines
	// that look like errors or state changes.
	Verbose bool
	// LogTo receives the child's forwarded output. Defaults to os.Stderr.
	LogTo io.Writer
}

// mockMatchDuration is how long a mock instance pretends to run before it
// reports itself finished, matching game-manager's spawner.
const mockMatchDuration = 30 * time.Minute

// Instance is one running battle server process.
type Instance struct {
	ID         string
	MatchID    string
	Port       int
	GameMode   string
	MapName    string
	MapPath    string
	MaxPlayers int
	Players    []string
	StartedAt  time.Time

	// ServerID is assigned by master-server on registration, empty if the
	// instance was never registered.
	ServerID string

	// LogPath is where the engine was told to write its own log.
	LogPath string

	// Mock is true when no real process backs this instance.
	Mock bool

	cmd       *exec.Cmd
	log       io.Writer
	done      chan struct{} // closed when the process has exited
	ready     chan struct{} // closed when the engine reports it is hosting
	readyOnce sync.Once
	stopCh    chan struct{} // mock only: wakes the fake match early
	stopOnce  sync.Once
	mu        sync.Mutex
	err       error // exit error, readable after done is closed
}

// markReady closes the ready channel exactly once.
func (i *Instance) markReady() {
	i.readyOnce.Do(func() { close(i.ready) })
}

// ErrNoBinary reports that the configured game binary does not exist. It is
// returned before any process is created, so the caller can distinguish
// "misconfigured" from "the engine died on startup".
var ErrNoBinary = errors.New("game binary not found")

// BuildArgs returns the argv used to launch the engine, excluding the program
// name itself.
//
// This argv is NOT freely editable. It was established by the Revival project by
// running the engine by hand until it reached "Match State Changed ...
// WaitingToStart -> InProgress" with UDP 0.0.0.0:7777 open, and their
// spawner.go records what the earlier, non-working version got wrong:
//
//   - The map must be a POSITIONAL URL with "?listen" appended. "?listen" is
//     what starts the net driver. Passing the map as "-Map=<name>" is ignored by
//     the engine entirely.
//   - "-dedicatedserver" is NOT a switch this build has; the string does not
//     occur in the executable. UE's switch is "-server", and a true dedicated
//     mode would need a Server target binary, which was never shipped for this
//     game. A listen server out of the normal client build is what works.
//   - "-nullrhi" is required. Without it the engine wants a GPU and dies during
//     early init -- which is also precisely what makes this headless.
//   - "-log=<unix path>" is meaningless to a Windows binary under Wine, which is
//     why the Revival project saw no instance log.
//
// # On the engine's log file, and why this tool does not use it
//
// Every process using this install writes to the SAME
// %LOCALAPPDATA%\DreadGame\Saved\Logs\DreadGame.log, including the operator's
// own game client. Running a battle server while the client is open produces one
// interleaved file that cannot be read to diagnose anything -- observed
// directly, with the client at frame 506 and the battle server at frame 323
// writing alternating lines into the same file, each rotating the other's log
// away on startup. dn-launcher passes a bare "-LOG", which selects exactly that
// shared default.
//
// "-ABSLOG=<path>" looks like the fix and is NOT one on this build. Tested
// against the shipping executable: with -ABSLOG the engine stops writing
// DreadGame.log -- so the switch is definitely parsed -- but never creates the
// target file either, leaving no engine log at all. That reproduced with a path
// containing spaces AND with a plain "C:\dnlogs" path, so it is not a quoting
// problem; the raw command line was confirmed to reach the process correctly
// quoted. Passing -ABSLOG would therefore make diagnosis strictly worse than
// passing nothing.
//
// So the engine keeps its default log, and this tool captures the child's
// stdout/stderr into its own per-instance file instead. That stream carries the
// lines that matter (LogGameMode match-state transitions, LogOnline, errors) and
// is fully under this process's control.
func BuildArgs(cfg LaunchConfig, matchID string) []string {
	mapURL := cfg.MapPathOrName()
	mapURL += "?listen"
	for _, opt := range cfg.URLOptions {
		if opt = strings.TrimSpace(opt); opt != "" {
			mapURL += "?" + opt
		}
	}

	maxPlayers := cfg.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = 10
	}

	args := []string{
		mapURL,
		"-server",
		fmt.Sprintf("-port=%d", cfg.Port),
		fmt.Sprintf("-maxplayers=%d", maxPlayers),
		fmt.Sprintf("-GameMode=%s", cfg.GameMode),
		fmt.Sprintf("-MatchID=%s", matchID),
		"-nullrhi",    // no RHI: the switch that actually makes this headless
		"-unattended", // never block on a modal dialog
		"-nosplash",
		"-nop4",
		"-nosound",
		"-noeac",   // EasyAntiCheat would otherwise refuse the unattended launch
		"-NoSteam", // no Steam client required on the host
	}
	// -forcelogflush makes the engine flush every line rather than buffering,
	// so a server that dies mid-startup still leaves the lines that explain it.
	// dn-launcher uses the same switch for the client.
	args = append(args, "-forcelogflush")
	return append(args, cfg.ExtraArgs...)
}

// MapPathOrName returns the package path if there is one, else the bare name.
// A bare name will not resolve as a map URL; this only exists so a caller that
// somehow has no path still produces a launchable-looking command rather than
// an empty positional argument.
func (c LaunchConfig) MapPathOrName() string {
	if c.Map.Path != "" {
		return c.Map.Path
	}
	return c.Map.Name
}

// Launch starts a battle server process and begins supervising it.
//
// By default a missing or unstartable binary is a HARD ERROR, which is the one
// deliberate behavioural break from the Revival project's spawner. That code
// logs a warning and records a "mock" instance instead, reporting a healthy
// match that does not exist -- misleading in a tool whose only job is to run a
// server.
//
// Mock instances are still supported, behind AllowMock, because they are a
// documented workflow: game-manager's mock mode is how the matchmaking pipeline
// is tested end to end without a game binary. The difference is that it must be
// asked for rather than being what happens when something is broken.
func Launch(cfg LaunchConfig) (*Instance, error) {
	if cfg.GameBinary == "" {
		if cfg.AllowMock {
			return newMockInstance(cfg, "no game binary configured"), nil
		}
		return nil, fmt.Errorf("%w: no path configured (set --game-binary or GAME_BINARY)", ErrNoBinary)
	}
	if _, err := os.Stat(cfg.GameBinary); err != nil {
		if cfg.AllowMock {
			return newMockInstance(cfg, "game binary not present"), nil
		}
		return nil, fmt.Errorf("%w: %s", ErrNoBinary, cfg.GameBinary)
	}

	logTo := cfg.LogTo
	if logTo == nil {
		logTo = os.Stderr
	}

	inst := &Instance{
		ID:         ids.New(),
		MatchID:    ids.New(),
		Port:       cfg.Port,
		GameMode:   cfg.GameMode,
		MapName:    cfg.Map.Name,
		MapPath:    cfg.Map.Path,
		MaxPlayers: cfg.MaxPlayers,
		Players:    append([]string(nil), cfg.Players...),
		StartedAt:  time.Now(),
		LogPath:    cfg.LogPath,
		log:        logTo,
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
	}

	args := BuildArgs(cfg, inst.MatchID)

	var cmd *exec.Cmd
	if useWine(cfg.WineExe) {
		cmd = exec.Command(cfg.WineExe, append([]string{cfg.GameBinary}, args...)...) //nolint:gosec // operator-configured paths, explicit argv, no shell
	} else {
		cmd = exec.Command(cfg.GameBinary, args...) //nolint:gosec // operator-configured paths, explicit argv, no shell
		// On Windows, override the raw command line so -Key=Value switches
		// survive a path containing spaces. See rawcmdline_windows.go.
		applyRawCmdLine(cmd, cfg.GameBinary, args)
		if !cfg.ShowWindow {
			configureHidden(cmd)
		}
	}

	// Run from the executable's own directory. The engine resolves its
	// co-located DLLs (libcef.dll and friends) relative to the working
	// directory; launching from anywhere else fails with
	// "LogWindows:Error: libcef.dll" and exits with status 3 within seconds.
	// The identical argv from the binary's own directory reaches InProgress.
	cmd.Dir = filepath.Dir(cfg.GameBinary)
	cmd.Env = buildEnv(cfg)

	// A log file that cannot be created is reported and then tolerated: losing
	// the log is much less bad than refusing to host the match.
	var logFile *os.File
	if cfg.LogPath != "" {
		f, err := os.Create(cfg.LogPath)
		if err != nil {
			fmt.Fprintf(logTo, "warning: cannot create instance log %s: %v\n", cfg.LogPath, err)
			inst.LogPath = ""
		} else {
			logFile = f
			fmt.Fprintf(f, "# dn-dedicated instance %s\n# %s\n# argv: %v\n\n",
				inst.ID, time.Now().Format(time.RFC3339), args)
		}
	}

	var fileOut io.Writer
	if logFile != nil {
		fileOut = logFile
	}
	writer := newLogWriter(logTo, fileOut, inst.ID, cfg.Verbose, inst.markReady)
	cmd.Stdout = writer
	cmd.Stderr = writer

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		if cfg.AllowMock {
			fmt.Fprintf(logTo, "warning: launch failed (%v); recording a mock instance\n", err)
			return newMockInstance(cfg, "launch failed: "+err.Error()), nil
		}
		return nil, fmt.Errorf("start battle server: %w", err)
	}
	inst.cmd = cmd

	if !cfg.ShowWindow {
		go inst.suppressWindow()
	}

	go func() {
		err := cmd.Wait()
		inst.mu.Lock()
		inst.err = err
		inst.mu.Unlock()
		if logFile != nil {
			fmt.Fprintf(logFile, "\n# exited at %s (err: %v)\n", time.Now().Format(time.RFC3339), err)
			_ = logFile.Close()
		}
		close(inst.done)
	}()

	return inst, nil
}

// suppressWindow hides the engine's game window as soon as it appears, and
// keeps watching in case it is recreated.
//
// Polling is necessary rather than ugly: the window is created during engine
// init, seconds after the process starts, so hiding once at launch finds
// nothing. The sweep is cheap (one EnumWindows pass) and stops as soon as the
// process exits.
//
// After the first successful hide the interval backs off, because the common
// case is one window that stays hidden; the slow poll only exists to catch the
// engine recreating it, e.g. on a resolution or fullscreen change during map
// load.
func (i *Instance) suppressWindow() {
	deadline := time.After(3 * time.Minute)
	interval := 250 * time.Millisecond
	everHid := false

	for {
		select {
		case <-i.done:
			return
		case <-deadline:
			return
		case <-time.After(interval):
			if pid := i.PID(); pid != 0 {
				if n := hideProcessWindows(pid); n > 0 && !everHid {
					everHid = true
					interval = 2 * time.Second
					fmt.Fprintf(i.log, "[%s] hid %d engine window(s); running headless\n", shortID(i.ID), n)
				}
			}
		}
	}
}

// newMockInstance records an instance with no process behind it, mirroring
// game-manager's spawner: it reports ready immediately and finishes after
// mockMatchDuration, or as soon as Stop wakes it.
func newMockInstance(cfg LaunchConfig, reason string) *Instance {
	logTo := cfg.LogTo
	if logTo == nil {
		logTo = os.Stderr
	}
	inst := &Instance{
		ID:         ids.New(),
		MatchID:    ids.New(),
		Port:       cfg.Port,
		GameMode:   cfg.GameMode,
		MapName:    cfg.Map.Name,
		MapPath:    cfg.Map.Path,
		MaxPlayers: cfg.MaxPlayers,
		Players:    append([]string(nil), cfg.Players...),
		StartedAt:  time.Now(),
		Mock:       true,
		log:        logTo,
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
		stopCh:     make(chan struct{}),
	}
	// Nothing will ever emit a match-state line for a mock, so readiness is
	// declared up front rather than left to time out.
	inst.markReady()

	fmt.Fprintf(logTo, "[%s] MOCK instance (%s) -- no game process is running\n", shortID(inst.ID), reason)

	go func() {
		select {
		case <-time.After(mockMatchDuration):
		case <-inst.stopCh:
		}
		close(inst.done)
	}()
	return inst
}

// useWine reports whether the binary should be run through Wine. On Windows the
// exe runs directly; "none" is honoured on every platform for parity with
// game-manager's WINE_EXE=none.
func useWine(wineExe string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return wineExe != "" && wineExe != "none"
}

// buildEnv assembles the child environment.
//
// On Windows this is just the inherited environment: there is no Wine prefix and
// no software-GL workaround to apply, because -nullrhi already means no RHI is
// created. The Wine-specific handling below is carried over from the Revival
// project's battleServerEnv and only applies when actually launching via Wine.
func buildEnv(cfg LaunchConfig) []string {
	env := os.Environ()
	if !useWine(cfg.WineExe) {
		return env
	}

	// The prefix is inherited, never manufactured. The engine needs a CONFIGURED
	// Wine prefix (registry, DLL overrides, DX11 setup); pointing it at a fresh
	// empty prefix makes wine fail before the engine starts -- observed as exit
	// status 3 seconds after launch, while the same argv against a configured
	// prefix reaches InProgress. GAME_WINEPREFIX overrides WINEPREFIX if the
	// operator wants battle servers on a separate prefix.
	if prefix := os.Getenv("GAME_WINEPREFIX"); prefix != "" {
		env = append(env, "WINEPREFIX="+prefix)
	}
	env = append(env, "WINEDEBUG=-all")

	// These matter even with -nullrhi: without them the shipping build
	// page-faults during RHI init under Wine on a box with no GPU. Each is only
	// added when unset, so an operator with real hardware can override any of
	// them.
	for k, v := range map[string]string{
		"LIBGL_ALWAYS_SOFTWARE":      "1",
		"GALLIUM_DRIVER":             "llvmpipe",
		"MESA_GL_VERSION_OVERRIDE":   "4.5",
		"MESA_GLSL_VERSION_OVERRIDE": "450",
	} {
		if os.Getenv(k) == "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Done returns a channel closed when the process exits.
func (i *Instance) Done() <-chan struct{} { return i.done }

// ExitErr returns the process's exit error. Only meaningful after Done is
// closed; returns nil while still running.
func (i *Instance) ExitErr() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.err
}

// Running reports whether the process is still alive.
func (i *Instance) Running() bool {
	select {
	case <-i.done:
		return false
	default:
		return true
	}
}

// PID returns the OS process id, or 0 if there is no live process.
func (i *Instance) PID() int {
	if i.cmd == nil || i.cmd.Process == nil {
		return 0
	}
	return i.cmd.Process.Pid
}

// WaitReady blocks until the server is hosting, the process exits, or ctx is
// done.
//
// Readiness comes from the engine's own output: the line
//
//	LogGameMode:Display: Match State Changed from EnteringMap to WaitingToStart
//
// (and the InProgress transition after it) is emitted on stdout, which this
// process captures. That signal is verified -- it is what a healthy launch
// actually prints.
//
// An earlier version instead probed the UDP port, treating "we cannot bind it"
// as "the engine has it". That silently never fires on Windows: unlike Linux,
// Windows permits a second UDP bind to the same address and port unless the
// first socket set SO_EXCLUSIVEADDRUSE, which the engine does not. The probe
// therefore always reported the port as free, and WaitReady blocked until the
// process exited even though the server was up and bound -- confirmed against a
// running instance holding 0.0.0.0:7801. The port check is kept only as a
// secondary trigger, since it is sound on Linux, but it is no longer the primary
// signal on any platform.
func (i *Instance) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-i.ready:
			return nil
		case <-i.done:
			if err := i.ExitErr(); err != nil {
				return fmt.Errorf("battle server exited during startup: %w", err)
			}
			return errors.New("battle server exited during startup")
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for battle server on port %d: %w", i.Port, ctx.Err())
		case <-ticker.C:
			if portExclusivelyHeld(i.Port) {
				return nil
			}
		}
	}
}

// portExclusivelyHeld reports whether a UDP port cannot be bound.
//
// Reliable on Linux; on Windows a duplicate UDP bind succeeds by default, so
// this returns false even for a port the engine is actively using. Do not treat
// a false result as "nothing is listening".
func portExclusivelyHeld(port int) bool {
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}

// Stop terminates the process and waits up to timeout for it to exit.
//
// There is no graceful path. The engine is a Windows GUI-subsystem binary with
// no console handler and, under -unattended, no UI to close; a request to exit
// has nowhere to arrive. Killing it is safe because a battle server holds no
// state worth flushing -- match results are the backend's business, not the
// engine's.
func (i *Instance) Stop(timeout time.Duration) error {
	if !i.Running() {
		return nil
	}
	// A mock has no process to kill; wake its fake match instead so cleanup
	// runs now rather than in half an hour.
	if i.Mock {
		i.stopOnce.Do(func() { close(i.stopCh) })
		select {
		case <-i.done:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("mock instance %s did not finish within %s", i.ID, timeout)
		}
	}
	if i.cmd == nil || i.cmd.Process == nil {
		return nil
	}
	if err := i.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill instance %s: %w", i.ID, err)
	}
	select {
	case <-i.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("instance %s did not exit within %s", i.ID, timeout)
	}
}

// logWriter forwards the child's output line by line, tagged with the instance
// id. Unless verbose is set it only passes lines that look like errors or state
// changes, so a healthy server does not flood the log while a failing one still
// shows why.
// logWriter splits the child's output two ways: EVERY line goes to the
// per-instance file, while the console gets only lines that look like errors or
// state changes (or everything, under verbose).
//
// The file is the reason this type exists. The engine's own log cannot be
// separated per process on this build (see BuildArgs), so this captured stream
// is the only per-instance log that exists. It is therefore written unfiltered:
// a filter that hides the one line explaining a failure is worse than a large
// file.
type logWriter struct {
	out        io.Writer
	file       io.Writer
	instanceID string
	verbose    bool
	onReady    func()
	mu         sync.Mutex
	buf        []byte
}

func newLogWriter(out, file io.Writer, instanceID string, verbose bool, onReady func()) *logWriter {
	return &logWriter{out: out, file: file, instanceID: instanceID, verbose: verbose, onReady: onReady}
}

// readyMarkers are the engine lines that mean the map is loaded and the match is
// hosting. "WaitingToStart" is the earliest point the server is live; the
// InProgress transition follows it and is matched too so a caller that attaches
// late still sees readiness.
var readyMarkers = []string{
	"Match State Changed from EnteringMap to WaitingToStart",
	"Match State Changed from WaitingToStart to InProgress",
}

func isReadyLine(line string) bool {
	for _, marker := range readyMarkers {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf[:idx], "\r"))
		w.buf = w.buf[idx+1:]
		if w.file != nil {
			fmt.Fprintf(w.file, "%s %s\n", time.Now().Format("15:04:05.000"), line)
		}
		if w.verbose || interesting(line) {
			fmt.Fprintf(w.out, "[%s] %s\n", shortID(w.instanceID), line)
		}
		if w.onReady != nil && isReadyLine(line) {
			w.onReady()
		}
	}
	// Bound the buffer so a stream with no newlines cannot grow without limit.
	if len(w.buf) > 8192 {
		w.buf = w.buf[len(w.buf)-8192:]
	}
	return len(p), nil
}

// interesting matches the marker set the Revival project settled on, plus
// "Fatal" and the libcef signature that identifies a wrong working directory.
func interesting(line string) bool {
	for _, marker := range []string{
		"Error", "error", "Fatal", "Warning: Failed", "Match State",
		"libcef", "Assertion", "Exception", "UDP", "listen",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
