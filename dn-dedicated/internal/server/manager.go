package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"dn-dedicated/internal/gamedata"
	"dn-dedicated/internal/master"
)

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	GameBinary string
	WineExe    string
	ServerIP   string // address handed to clients; also what gets registered
	PortStart  int
	PortEnd    int
	MaxPlayers int

	// Master is optional. When nil, instances run without being registered in
	// the server browser -- which is the normal case for a purely local server.
	Master *master.Client

	// LogDir is where each instance's engine log is written. Keeping these out
	// of the engine's default Saved/Logs is what stops a battle server and the
	// operator's own game client from overwriting each other's logs.
	LogDir string

	// AllowMock enables game-manager's mock-instance behaviour. See Launch.
	AllowMock bool

	// ShowWindow leaves the engine's game window visible (debugging only).
	ShowWindow bool

	Verbose bool
	LogTo   io.Writer

	// EngineLogCmds is passed to every battle server as -LogCmds. It raises the
	// ENGINE's category verbosity, which is a different thing from Verbose
	// above -- that only decides how much of the captured stream is echoed to
	// the console. Empty leaves the engine at its defaults.
	EngineLogCmds string
}

// Manager owns the port pool and the set of running instances.
type Manager struct {
	cfg ManagerConfig

	mu        sync.RWMutex
	instances map[string]*Instance
	inUse     map[int]bool
}

// NewManager returns a Manager with an empty pool.
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.PortStart == 0 {
		cfg.PortStart = 7777
	}
	if cfg.PortEnd == 0 {
		cfg.PortEnd = 7877
	}
	if cfg.MaxPlayers == 0 {
		cfg.MaxPlayers = 10
	}
	if cfg.LogTo == nil {
		cfg.LogTo = os.Stderr
	}
	return &Manager{
		cfg:       cfg,
		instances: make(map[string]*Instance),
		inUse:     make(map[int]bool),
	}
}

// Config exposes the manager's configuration (read-only use).
func (m *Manager) Config() ManagerConfig { return m.cfg }

// StartOptions describes one requested battle server.
type StartOptions struct {
	Map        gamedata.Map
	GameMode   string
	Port       int // 0 to allocate from the pool
	MaxPlayers int
	Players    []string
	ExtraArgs  []string
	URLOptions []string
}

// Start launches a battle server and begins supervising it.
//
// The port is verified free before launch. That check is what makes
// Instance.WaitReady's "the port is held, so we are up" heuristic sound: if the
// port was already taken by something else, this fails here instead of
// reporting a foreign process as a healthy battle server.
func (m *Manager) Start(opts StartOptions) (*Instance, error) {
	port, err := m.acquirePort(opts.Port)
	if err != nil {
		return nil, err
	}

	maxPlayers := opts.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = m.cfg.MaxPlayers
	}

	// One log file per instance, named so it sorts by start time and is
	// traceable back to a port. Failure to create the directory is not fatal:
	// LogPath stays empty, the engine falls back to its shared default, and the
	// server still runs.
	logPath := ""
	if m.cfg.LogDir != "" {
		if err := os.MkdirAll(m.cfg.LogDir, 0o750); err != nil {
			fmt.Fprintf(m.cfg.LogTo, "warning: cannot create log dir %s: %v\n", m.cfg.LogDir, err)
		} else {
			name := fmt.Sprintf("battle-%s-port%d.log", time.Now().Format("20060102-150405"), port)
			logPath = filepath.Join(m.cfg.LogDir, name)
		}
	}

	inst, err := Launch(LaunchConfig{
		GameBinary:    m.cfg.GameBinary,
		WineExe:       m.cfg.WineExe,
		Map:           opts.Map,
		GameMode:      opts.GameMode,
		Port:          port,
		MaxPlayers:    maxPlayers,
		Players:       opts.Players,
		ExtraArgs:     opts.ExtraArgs,
		URLOptions:    opts.URLOptions,
		LogPath:       logPath,
		AllowMock:     m.cfg.AllowMock,
		ShowWindow:    m.cfg.ShowWindow,
		Verbose:       m.cfg.Verbose,
		LogTo:         m.cfg.LogTo,
		EngineLogCmds: m.cfg.EngineLogCmds,
	})
	if err != nil {
		m.releasePort(port)
		return nil, err
	}

	m.mu.Lock()
	m.instances[inst.ID] = inst
	m.mu.Unlock()

	if m.cfg.Master != nil {
		m.registerInstance(inst)
	}

	go m.supervise(inst)
	return inst, nil
}

// registerInstance announces the instance to master-server. A failure is logged
// and tolerated: a local server that nothing can browse to is still a working
// server, and refusing to start one because an optional registry is down would
// be worse than the missing listing.
func (m *Manager) registerInstance(inst *Instance) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	serverID, err := m.cfg.Master.Register(ctx, master.RegisterRequest{
		Name:       fmt.Sprintf("Match-%s", shortID(inst.MatchID)),
		IP:         m.cfg.ServerIP,
		Port:       inst.Port,
		GameMode:   inst.GameMode,
		Map:        inst.MapName,
		MaxPlayers: inst.MaxPlayers,
	})
	if err != nil {
		fmt.Fprintf(m.cfg.LogTo, "[%s] master-server registration failed: %v\n", shortID(inst.ID), err)
		return
	}
	inst.ServerID = serverID
	fmt.Fprintf(m.cfg.LogTo, "[%s] registered with master-server as %s\n", shortID(inst.ID), serverID)
}

// supervise runs the heartbeat loop for a registered instance and performs
// teardown exactly once when the process exits.
//
// Teardown lives here and ONLY here. game-manager's spawner documents why: doing
// the port release in Stop() as well caused a double release, which could hand a
// port to a new instance while the old process was still shutting down, leaving
// two servers bound to the same port and the pool's accounting corrupt. Stop()
// signals; supervise() cleans up.
func (m *Manager) supervise(inst *Instance) {
	ticker := time.NewTicker(master.HeartbeatInterval)
	defer ticker.Stop()

	for running := true; running; {
		select {
		case <-inst.Done():
			running = false
		case <-ticker.C:
			if m.cfg.Master != nil && inst.ServerID != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				// current_players is reported as 0: the engine does not expose a
				// player count to this process, and inventing one would be worse
				// than an honest zero. The value only affects the browser's
				// display, not whether the server is considered online.
				if err := m.cfg.Master.Heartbeat(ctx, inst.ServerID, 0); err != nil {
					fmt.Fprintf(m.cfg.LogTo, "[%s] heartbeat failed: %v\n", shortID(inst.ID), err)
				}
				cancel()
			}
		}
	}

	if err := inst.ExitErr(); err != nil {
		fmt.Fprintf(m.cfg.LogTo, "[%s] battle server exited: %v\n", shortID(inst.ID), err)
	} else {
		fmt.Fprintf(m.cfg.LogTo, "[%s] battle server exited cleanly\n", shortID(inst.ID))
	}

	if m.cfg.Master != nil && inst.ServerID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := m.cfg.Master.Deregister(ctx, inst.ServerID); err != nil {
			fmt.Fprintf(m.cfg.LogTo, "[%s] deregister failed: %v\n", shortID(inst.ID), err)
		}
		cancel()
	}

	m.mu.Lock()
	delete(m.instances, inst.ID)
	m.mu.Unlock()
	m.releasePort(inst.Port)
}

// Stop signals an instance to terminate. Cleanup happens in supervise().
func (m *Manager) Stop(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	return inst.Stop(10 * time.Second)
}

// Shutdown stops every running instance and waits for teardown to finish.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	list := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		list = append(list, inst)
	}
	m.mu.RUnlock()

	for _, inst := range list {
		if err := inst.Stop(10 * time.Second); err != nil {
			fmt.Fprintf(m.cfg.LogTo, "[%s] stop during shutdown: %v\n", shortID(inst.ID), err)
		}
	}
	// Give supervise() a moment to deregister and release ports.
	deadline := time.After(12 * time.Second)
	for {
		m.mu.RLock()
		remaining := len(m.instances)
		m.mu.RUnlock()
		if remaining == 0 {
			return
		}
		select {
		case <-deadline:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Get returns one instance by id.
func (m *Manager) Get(id string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	return inst, ok
}

// List returns running instances, oldest first.
func (m *Manager) List() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		list = append(list, inst)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt.Before(list[j].StartedAt) })
	return list
}

// PortsInUse returns how many ports the pool has allocated.
func (m *Manager) PortsInUse() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.inUse)
}

// PortCapacity returns the pool size.
func (m *Manager) PortCapacity() int { return m.cfg.PortEnd - m.cfg.PortStart + 1 }

// acquirePort reserves a port. An explicit non-zero port is honoured if not
// already reserved; otherwise the first unreserved port in the range is taken.
//
// This also tries a real bind, to catch a port held by a program outside this
// manager -- for example a battle server left over from a previous run. That
// check is only meaningful on Linux: Windows allows a duplicate UDP bind, so
// portExclusivelyHeld reports nothing there and the pool's own bookkeeping is
// the only protection. Do not read a successful acquire on Windows as proof the
// port is unused.
func (m *Manager) acquirePort(explicit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if explicit != 0 {
		if m.inUse[explicit] {
			return 0, fmt.Errorf("port %d is already in use by this manager", explicit)
		}
		if portExclusivelyHeld(explicit) {
			return 0, fmt.Errorf("port %d is already bound by another process", explicit)
		}
		m.inUse[explicit] = true
		return explicit, nil
	}

	for port := m.cfg.PortStart; port <= m.cfg.PortEnd; port++ {
		if m.inUse[port] || portExclusivelyHeld(port) {
			continue
		}
		m.inUse[port] = true
		return port, nil
	}
	return 0, fmt.Errorf("no free ports in range %d-%d", m.cfg.PortStart, m.cfg.PortEnd)
}

func (m *Manager) releasePort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.inUse, port)
}
