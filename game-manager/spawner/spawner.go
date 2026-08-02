package spawner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Instance represents a running game engine process.
//
// Instance is copied by value in places (e.g. List()), so it must not embed
// a sync primitive directly (go vet correctly flags that as unsafe). stopCh
// itself is a reference type and safe to copy; idempotent-close is instead
// guarded by Spawner.mu in signalStop, keyed off stopSignaled.
type Instance struct {
	ID        string
	ServerID  string // ID returned by master-server on registration
	Port      int
	GameMode  string
	Map       string
	MatchID   string
	Players   []string
	Cmd       *exec.Cmd
	ConfigDir string
	StartedAt time.Time

	// stopCh lets Stop() wake monitor()'s mock-mode wait (no real process
	// to Wait() on) early instead of blocking it for the full 30-minute
	// fallback duration.
	stopCh       chan struct{}
	stopSignaled bool
}

// Spawner manages Wine + DreadGame-Win64-Shipping.exe dedicated server instances.
type Spawner struct {
	mu          sync.RWMutex
	instances   map[string]*Instance
	gameBinary  string
	wineExe     string
	masterURL   string
	internalKey string
	serverIP    string
	releasePort func(int)
	log         *logrus.Logger
	httpClient  *http.Client
}

// New creates a Spawner.
//
//	gameBinary:  path to DreadGame-Win64-Shipping.exe
//	wineExe:     path to wine executable (e.g. "wine")
//	masterURL:   base URL of master-server (e.g. "http://127.0.0.1:8084")
//	serverIP:    public IP that clients will connect to
//	internalKey: shared secret sent as X-Internal-Key on register/deregister
//	             calls to master-server's now-authenticated write endpoints
func New(gameBinary, wineExe, masterURL, serverIP, internalKey string, log *logrus.Logger, releasePort func(int)) *Spawner {
	return &Spawner{
		instances:   make(map[string]*Instance),
		gameBinary:  gameBinary,
		wineExe:     wineExe,
		masterURL:   masterURL,
		internalKey: internalKey,
		serverIP:    serverIP,
		releasePort: releasePort,
		log:         log,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			},
		},
	}
}

// Launch spawns a new dedicated game server instance and registers it with the master server.
func (s *Spawner) Launch(gameMode, mapName, mapPath string, port int, players []string) (*Instance, error) {
	instID := uuid.New().String()
	configDir := filepath.Join(os.TempDir(), "dn-instance-"+instID)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	inst := &Instance{
		ID:        instID,
		Port:      port,
		GameMode:  gameMode,
		Map:       mapName,
		MatchID:   uuid.New().String(),
		Players:   players,
		ConfigDir: configDir,
		StartedAt: time.Now(),
		stopCh:    make(chan struct{}),
	}

	// How the engine is actually told to host, verified by running it by hand:
	//
	//	wine <exe> "<mapPath>?listen" -server -port=N -nullrhi -unattended ...
	//	-> LogGameMode: Match State Changed from EnteringMap to WaitingToStart
	//	-> WaitingToStart to InProgress
	//	-> UDP 0.0.0.0:7777 open, owned by DreadGame-Win64
	//
	// The previous argv could not have worked:
	//   * the map went as "-Map=<name>", which the engine ignores. A map is a
	//     POSITIONAL URL, and "?listen" on it is what starts the net driver.
	//   * "-dedicatedserver" is not a switch this build has -- the string does
	//     not occur in the executable at all. UE's switch is "-server", and true
	//     dedicated mode would need a Server target binary, which does not exist
	//     here. A listen server out of the normal build is what works.
	//   * without "-nullrhi" it needs a GPU and died during early init.
	//   * "-log=<unix path>" is meaningless to a Windows binary under Wine,
	//     which is why the instance log never appeared. The engine writes to its
	//     own Saved/Logs directory instead.
	args := []string{
		s.gameBinary,
		battleServerMapURL(mapPath, mapName, gameMode),
		"-server",
		fmt.Sprintf("-port=%d", port),
		"-maxplayers=10",
		fmt.Sprintf("-GameMode=%s", gameMode),
		fmt.Sprintf("-MatchID=%s", inst.MatchID),
		"-nullrhi",
		"-unattended",
		"-nosplash",
		"-nop4",
		"-nosound",
		"-noeac",
		"-NoSteam",
	}

	var cmd *exec.Cmd
	// Use Wine if the binary is a Windows executable; fall back to direct exec on Windows
	if s.wineExe != "" && s.wineExe != "none" {
		//nolint:gosec // Inputs are operator-configured binary paths and explicit argv entries, not shell-expanded.
		cmd = exec.Command(s.wineExe, args...)
	} else {
		//nolint:gosec // Inputs are operator-configured binary paths and explicit argv entries, not shell-expanded.
		cmd = exec.Command(args[0], args[1:]...)
	}
	cmd.Env = battleServerEnv(configDir)
	// Run from the executable's own directory. The engine resolves its
	// co-located DLLs (libcef.dll and friends) relative to the working
	// directory, so launching from anywhere else -- e.g. the game-manager's cwd
	// -- fails with "LogWindows:Error: libcef.dll" and the process exits with
	// status 3 within seconds. The SAME argv from the binary's directory reaches
	// "Match State ... InProgress". Wine translates this to the child's Windows
	// cwd, which is what the loader consults.
	if s.gameBinary != "" {
		cmd.Dir = filepath.Dir(s.gameBinary)
	}
	// Surface the child's early output. Without this the process's stdout/stderr
	// go nowhere, so a launch that dies during init -- the libcef case above --
	// leaves no trace beyond "exit status 3". The engine's own detailed log
	// still goes to its Saved/Logs directory; this is just enough to see a
	// startup failure.
	cmd.Stdout = newInstanceLogWriter(s.log, inst.ID, "stdout")
	cmd.Stderr = newInstanceLogWriter(s.log, inst.ID, "stderr")

	if err := cmd.Start(); err != nil {
		// A failed spawn used to be a warning, after which the instance was
		// recorded as a mock and handed back as if it were real. Matchmaking
		// then formed the match and pushed the client an address nothing was
		// listening on, so the player sat on "match starting" forever with no
		// error on either side. Reported from a clean install, where it hid a
		// simple mistyped GAME_BINARY for an entire debugging session.
		//
		// Mock instances are still useful for exercising the stack without the
		// game, but that has to be asked for (DN_ALLOW_MOCK_INSTANCES=1), not
		// be the silent fallback for a real launch error.
		if os.Getenv("DN_ALLOW_MOCK_INSTANCES") == "" {
			s.log.WithError(err).WithField("binary", s.gameBinary).
				Error("battle server launch failed; failing the match rather than recording a mock instance")
			return nil, fmt.Errorf("launch battle server %q: %w", s.gameBinary, err)
		}
		s.log.WithError(err).Warn("game binary launch failed; DN_ALLOW_MOCK_INSTANCES is set, recording instance as mock")
	}

	inst.Cmd = cmd

	// Register with master server
	serverID, err := s.registerWithMaster(inst)
	if err != nil {
		s.log.WithError(err).Warn("failed to register with master server")
	}
	inst.ServerID = serverID

	s.mu.Lock()
	s.instances[inst.ID] = inst
	s.mu.Unlock()

	s.log.WithFields(logrus.Fields{
		"instance_id": inst.ID,
		"port":        port,
		"game_mode":   gameMode,
		"map":         mapName,
	}).Info("game instance launched")

	// Monitor the process in the background
	go s.monitor(inst)

	return inst, nil
}

// monitor waits for the process to exit and deregisters it.
func (s *Spawner) monitor(inst *Instance) {
	if inst.Cmd != nil && inst.Cmd.Process != nil {
		if err := inst.Cmd.Wait(); err != nil {
			s.log.WithError(err).WithField("instance_id", inst.ID).Warn("game instance wait")
		}
	} else {
		// Mock mode (no real process — e.g. game binary not present): wait
		// a reasonable match duration, but wake immediately if Stop() signals.
		select {
		case <-time.After(30 * time.Minute):
		case <-inst.stopCh:
		}
	}

	s.log.WithField("instance_id", inst.ID).Info("game instance exited")

	// Deregister from master server
	if inst.ServerID != "" {
		req, err := http.NewRequest(http.MethodDelete,
			fmt.Sprintf("%s/servers/%s", s.masterURL, inst.ServerID), nil)
		if err != nil {
			s.log.WithError(err).WithField("instance_id", inst.ID).Warn("build deregister request")
		} else {
			req.Header.Set("X-Internal-Key", s.internalKey)
			resp, err := s.httpClient.Do(req)
			if err != nil {
				s.log.WithError(err).WithField("instance_id", inst.ID).Warn("deregister instance")
			} else {
				_ = resp.Body.Close()
			}
		}
	}

	s.mu.Lock()
	delete(s.instances, inst.ID)
	s.mu.Unlock()

	if s.releasePort != nil {
		s.releasePort(inst.Port)
	}
	s.cleanupInstance(inst)
}

func (s *Spawner) cleanupInstance(inst *Instance) {
	if inst.ConfigDir != "" {
		if err := os.RemoveAll(inst.ConfigDir); err != nil {
			s.log.WithError(err).WithField("instance_id", inst.ID).Warn("cleanup config dir")
		}
	}
}

// Stop terminates a running instance by ID. Returns nil on success.
//
// Stop is idempotent with respect to process liveness (M12): if the instance
// ID is not tracked at all, that's a genuine "not found" error. If the
// instance IS tracked but its underlying OS process has already exited
// (e.g. it crashed moments before the delete request arrived), Kill()
// returns os.ErrProcessDone, which is treated as success rather than
// surfacing an "already finished" error to the caller.
//
// Stop only signals termination — it deliberately does NOT delete from
// s.instances, release the port, or run cleanupInstance itself. Those all
// happen exactly once, in monitor(), which is the only code path that's
// actually certain the process has exited (Cmd.Wait() returning). Doing
// them here too was a double-release bug: this method's own release could
// hand the port to a newly-launched instance while the killed process (or
// monitor()'s subsequent release of the SAME port) was still in flight,
// corrupting the pool's in-use accounting and potentially causing two
// instances to bind the same port.
func (s *Spawner) Stop(id string) error {
	s.mu.RLock()
	inst, ok := s.instances[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	if inst.Cmd != nil && inst.Cmd.Process != nil {
		if err := inst.Cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill instance %s: %w", id, err)
		}
	} else {
		// Mock mode — no real process to Kill()/Wait() on. Wake monitor()'s
		// sleep early so it can run cleanup now instead of after 30 minutes.
		s.signalStop(inst)
	}
	return nil
}

// signalStop closes inst.stopCh exactly once; safe to call multiple times
// (e.g. concurrent Stop() calls for the same instance). Guarded by s.mu
// since Instance itself can't safely embed a sync primitive (it's copied
// by value in List()).
func (s *Spawner) signalStop(inst *Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !inst.stopSignaled {
		inst.stopSignaled = true
		close(inst.stopCh)
	}
}

// Shutdown kills all running instances.
func (s *Spawner) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, inst := range s.instances {
		if inst.Cmd != nil && inst.Cmd.Process != nil {
			if err := inst.Cmd.Process.Kill(); err != nil {
				s.log.WithError(err).WithField("instance_id", id).Warn("kill instance")
			}
		}
		if s.releasePort != nil {
			s.releasePort(inst.Port)
		}
		s.cleanupInstance(inst)
	}
	s.instances = make(map[string]*Instance)
	s.log.WithField("count", len(s.instances)).Info("spawner shutdown")
}

// List returns snapshots of all running instances.
func (s *Spawner) List() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		list = append(list, *inst)
	}
	return list
}

func (s *Spawner) registerWithMaster(inst *Instance) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"name":        fmt.Sprintf("Match-%s", inst.MatchID[:8]),
		"ip":          s.serverIP,
		"port":        inst.Port,
		"game_mode":   inst.GameMode,
		"map":         inst.Map,
		"max_players": 10,
	})
	if err != nil {
		return "", fmt.Errorf("marshal register request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/servers/register", s.masterURL), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", s.internalKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("master server returned HTTP %d", resp.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode register response: %w", err)
	}
	return result["id"], nil
}

// battleServerEnv builds the environment for a spawned battle server.
//
// The previous version forced WINEPREFIX to a brand-new per-instance directory
// (configDir/wine). That never worked: the game needs a CONFIGURED Wine prefix
// (registry, DLL overrides, the DX11 setup the client relies on), and an empty
// one makes wine fail before the engine even starts -- observed as the instance
// exiting with status 3 a few seconds after launch while the SAME argv run by
// hand against the operator's prefix reaches "Match State ... InProgress".
//
// So the prefix is inherited, not manufactured. WINEPREFIX passes straight
// through from the game-manager's own environment (set it in the service
// environment, the way the client harness uses /root/.wine); GAME_WINEPREFIX
// overrides it if the operator wants the battle servers on a different prefix.
//
// The software-GL variables matter even with -nullrhi: without them the shipping
// build page-faults during RHI init under Wine on a box with no GPU. They are
// the exact set the working client harness uses, and each is only added when the
// environment does not already set it, so an operator with real hardware or a
// different driver can override any of them.
// battleServerMapURL builds the positional map URL the engine is launched with.
//
// The game mode belongs HERE, not on the command line. "-GameMode=X" is not
// something UE4 reads: it selects the mode from the URL's "game" option and
// otherwise falls back to the map's World Settings default. Confirmed live -- a
// match requested as BC came up running Derelict's own default:
//
//	LogNet: Welcomed by server (Level: /Game/Maps/MP/Derelict/MP_Derelict_P,
//	        Game: /Game/Generic/GameModes/TurboTDM/GameMode_Turbo_TDM_BP...)
//
// The short name is all that is needed. DefaultGame.ini's
// [/Script/Engine.GameMode] section registers GameModeClassAliases for exactly
// the names the matchmaker uses -- TDM, PodTDM, TE, TM, Onslaught, Territory,
// TER, Bootcamp, BC, TMBasic, TurboTDM, plus Benchmark/Tutorial/Demo/
// VisualAttraction. "GameModeClassAliases" is present in the shipping binary,
// so the table is live at runtime, and every target blueprint is cooked into
// the paks. BC and Bootcamp both resolve to
// /Game/Generic/GameModes/BC/GameInfo_BC_BP.GameInfo_BC_BP_C.
//
// gameMode is already constrained to ^[A-Za-z0-9_]+$ by game-manager's request
// validation, so it cannot smuggle in further URL options.
func battleServerMapURL(mapPath, mapName, gameMode string) string {
	mapURL := mapPath
	if mapURL == "" {
		mapURL = mapName
	}
	mapURL += "?listen"
	if gameMode != "" {
		mapURL += "?game=" + gameMode
	}
	return mapURL
}

func battleServerEnv(configDir string) []string {
	env := os.Environ()

	// GAME_WINEPREFIX wins; otherwise WINEPREFIX is inherited as-is. Only when
	// NEITHER is set do we fall back to a per-instance prefix, which preserves
	// the old behaviour for a caller that has genuinely configured nothing.
	if prefix := os.Getenv("GAME_WINEPREFIX"); prefix != "" {
		env = append(env, "WINEPREFIX="+prefix)
	} else if os.Getenv("WINEPREFIX") == "" {
		env = append(env, "WINEPREFIX="+filepath.Join(configDir, "wine"))
	}

	env = append(env, "WINEDEBUG=-all")

	// The shipping binary links WebBrowserWidget, so CEF initialises even with
	// -nullrhi -unattended. CEF creates a real window and aborts the process
	// ("FATAL:hwnd_util.cc(67): Invalid window handle") when there is no X
	// display, which surfaces as a libcef.dll call stack and an immediate exit.
	// GAME_DISPLAY overrides; otherwise an inherited DISPLAY is kept; otherwise
	// fall back to the Xvfb display the harness runs.
	if display := os.Getenv("GAME_DISPLAY"); display != "" {
		env = append(env, "DISPLAY="+display)
	} else if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=:99")
	}

	// Proven software-GL defaults, each skipped if already set.
	defaults := map[string]string{
		"LIBGL_ALWAYS_SOFTWARE":      "1",
		"GALLIUM_DRIVER":             "llvmpipe",
		"MESA_GL_VERSION_OVERRIDE":   "4.5",
		"MESA_GLSL_VERSION_OVERRIDE": "450",
	}
	for k, v := range defaults {
		if os.Getenv(k) == "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// instanceLogWriter forwards a spawned battle server's output to the service
// log, one line at a time, tagged with the instance id and stream. It only logs
// lines that look like errors or state changes, so a healthy server does not
// flood the log while a failing one still shows why.
type instanceLogWriter struct {
	log        *logrus.Logger
	instanceID string
	stream     string
	buf        []byte
}

func newInstanceLogWriter(log *logrus.Logger, instanceID, stream string) *instanceLogWriter {
	return &instanceLogWriter{log: log, instanceID: instanceID, stream: stream}
}

func (w *instanceLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf[:idx], "\r"))
		w.buf = w.buf[idx+1:]
		if w.interesting(line) {
			w.log.WithFields(logrus.Fields{
				"instance_id": w.instanceID,
				"stream":      w.stream,
			}).Info("battle server: " + line)
		}
	}
	// Bound the buffer so a stream with no newlines cannot grow without limit.
	if len(w.buf) > 8192 {
		w.buf = w.buf[len(w.buf)-8192:]
	}
	return len(p), nil
}

func (w *instanceLogWriter) interesting(line string) bool {
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
