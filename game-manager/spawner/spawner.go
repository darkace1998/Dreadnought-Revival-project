package spawner

import (
	"bytes"
	"encoding/json"
	"fmt"
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
type Instance struct {
	ID        string
	ServerID  string // ID returned by master-server on registration
	Port      int
	GameMode  string
	Map       string
	MatchID   string
	Players   []string
	Cmd       *exec.Cmd
	StartedAt time.Time
}

// Spawner manages Wine + DreadGame-Win64-Shipping.exe dedicated server instances.
type Spawner struct {
	mu         sync.RWMutex
	instances  map[string]*Instance
	gameBinary string
	wineExe    string
	masterURL  string
	serverIP   string
	log        *logrus.Logger
}

// New creates a Spawner.
//
//	gameBinary: path to DreadGame-Win64-Shipping.exe
//	wineExe:    path to wine executable (e.g. "wine")
//	masterURL:  base URL of master-server (e.g. "http://127.0.0.1:8084")
//	serverIP:   public IP that clients will connect to
func New(gameBinary, wineExe, masterURL, serverIP string, log *logrus.Logger) *Spawner {
	return &Spawner{
		instances:  make(map[string]*Instance),
		gameBinary: gameBinary,
		wineExe:    wineExe,
		masterURL:  masterURL,
		serverIP:   serverIP,
		log:        log,
	}
}

// Launch spawns a new dedicated game server instance and registers it with the master server.
func (s *Spawner) Launch(gameMode, mapName string, port int, players []string) (*Instance, error) {
	inst := &Instance{
		ID:        uuid.New().String(),
		Port:      port,
		GameMode:  gameMode,
		Map:       mapName,
		MatchID:   uuid.New().String(),
		Players:   players,
		StartedAt: time.Now(),
	}

	// Write a per-instance config file for the game engine
	configDir := filepath.Join(os.TempDir(), "dn-instance-"+inst.ID)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	args := []string{
		s.gameBinary,
		"-dedicatedserver",
		fmt.Sprintf("-port=%d", port),
		"-maxplayers=10",
		fmt.Sprintf("-GameMode=%s", gameMode),
		fmt.Sprintf("-Map=%s", mapName),
		fmt.Sprintf("-MatchID=%s", inst.MatchID),
		"-nop4",
		"-nosound",
		"-noeac",
		"-NoSteam",
		fmt.Sprintf("-log=%s", filepath.Join(configDir, "server.log")),
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
		// Mock mode: wait a reasonable match duration
		time.Sleep(30 * time.Minute)
	}

	s.log.WithField("instance_id", inst.ID).Info("game instance exited")

	// Deregister from master server
	if inst.ServerID != "" {
		req, err := http.NewRequest(http.MethodDelete,
			fmt.Sprintf("%s/servers/%s", s.masterURL, inst.ServerID), nil)
		if err != nil {
			s.log.WithError(err).WithField("instance_id", inst.ID).Warn("build deregister request")
		} else if _, err := http.DefaultClient.Do(req); err != nil {
			s.log.WithError(err).WithField("instance_id", inst.ID).Warn("deregister instance")
		}
	}

	s.mu.Lock()
	delete(s.instances, inst.ID)
	s.mu.Unlock()
}

// Stop terminates a running instance by ID.
func (s *Spawner) Stop(id string) error {
	s.mu.RLock()
	inst, ok := s.instances[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	if inst.Cmd != nil && inst.Cmd.Process != nil {
		return inst.Cmd.Process.Kill()
	}
	s.mu.Lock()
	delete(s.instances, id)
	s.mu.Unlock()
	return nil
}

// List returns all running instances.
func (s *Spawner) List() []*Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		list = append(list, inst)
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

	resp, err := http.Post(
		fmt.Sprintf("%s/servers/register", s.masterURL),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode register response: %w", err)
	}
	return result["id"], nil
}
