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
	mapURL := mapPath
	if mapURL == "" {
		mapURL = mapName
	}
	args := []string{
		s.gameBinary,
		mapURL + "?listen",
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
	cmd.Env = append(os.Environ(),
		"WINEDEBUG=-all",
		fmt.Sprintf("WINEPREFIX=%s", filepath.Join(configDir, "wine")),
	)

	if err := cmd.Start(); err != nil {
		s.log.WithError(err).Warn("game binary launch failed (binary may not be present); recording instance as mock")
		// Continue in mock mode so the rest of the stack can be tested without the game binary
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
