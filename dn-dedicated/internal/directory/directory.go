// Package directory registers this cluster at a master-master directory
// server (the public server browser) and keeps it updated with a heartbeat.
//
// One POST does both jobs: /clusters/register upserts by cluster name, so a
// restart keeps its identity instead of littering the directory, and every
// call refreshes the heartbeat timestamp. A cluster that stops calling
// vanishes from the browser after the directory's staleness timeout.
//
// Opt-out: if NoMasterFile exists (run/dn-no-master-server.txt by default)
// the cluster deregisters (best effort) and stays silent until the file goes
// away. That is the whole "unlisted cluster" story: invisible in the browser,
// joinable only by manual IP.
package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dn-dedicated/internal/server"
)

// heartbeatInterval is how often the cluster re-registers. The directory
// marks clusters stale after 120 seconds, so four missed beats hide us.
const heartbeatInterval = 30 * time.Second

// Config for one cluster's directory presence. Empty MasterURL disables
// everything: a cluster that does not know a directory cannot join one.
type Config struct {
	MasterURL string // e.g. http://directory.example.org:8091, "" = disabled
	Name      string // cluster name shown in the browser
	WebURL    string // public https URL players authenticate against
	BattleIP  string // address handed to clients for battle servers (SERVER_IP)
	Version   string // server version string shown in the browser
	MOTD      string // message of the day shown in the browser
	CAFile    string // cluster CA cert (certs/ca.crt): uploaded so browsers can TOFU it
	// Email is required to list: the directory operator mails the account-sync
	// secret there by hand. AgentURL is this host's sync-agent endpoint
	// (https://host:8093, may be empty: then no automatic secret delivery).
	Email    string
	AgentURL string
	// NoMasterFile, when present, opts the cluster out (run/dn-no-master-server.txt).
	NoMasterFile string
	Log          io.Writer
}

// Registrar runs the heartbeat loop. Create with New, then Start/Stop.
type Registrar struct {
	cfg    Config
	mgr    *server.Manager
	client *http.Client

	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	// clusterID is the directory's id for us (from the register reply),
	// needed for deregistration. Empty until the first success.
	clusterID string
}

func New(cfg Config, mgr *server.Manager) *Registrar {
	if cfg.Log == nil {
		cfg.Log = os.Stderr
	}
	return &Registrar{
		cfg:    cfg,
		mgr:    mgr,
		client: &http.Client{Timeout: 10 * time.Second},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (r *Registrar) logf(format string, args ...interface{}) {
	fmt.Fprintf(r.cfg.Log, format+"\n", args...)
}

// Enabled reports whether registration is configured at all. Directory URL,
// name and contact email are all required: without an address the operator
// cannot mail the sync secret, and the listing stays silent rather than
// erroring every 30 seconds.
func (r *Registrar) Enabled() bool {
	return r.cfg.MasterURL != "" && r.cfg.Name != "" && r.cfg.Email != ""
}

// optedOut reports whether the opt-out file exists right now. Checked every
// beat (not just at startup) so creating or deleting the file takes effect
// without a restart.
func (r *Registrar) optedOut() bool {
	if r.cfg.NoMasterFile == "" {
		return false
	}
	_, err := os.Stat(r.cfg.NoMasterFile)
	return err == nil
}

// Start begins heartbeating in the background. No-op when disabled.
// Safe to call once; Stop is safe without Start and twice.
func (r *Registrar) Start() {
	if !r.Enabled() {
		r.logf("directory: disabled (set MASTER_MASTER_URL, CLUSTER_NAME and CLUSTER_EMAIL to list this cluster)")
		return
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	if r.optedOut() {
		r.logf("directory: opted out (%s present) — this cluster stays unlisted", r.cfg.NoMasterFile)
	}
	go r.loop()
}

// Stop deregisters (best effort) and waits for the loop to finish.
func (r *Registrar) Stop() {
	r.mu.Lock()
	started := r.started
	r.started = false
	r.mu.Unlock()
	if started {
		close(r.stopCh)
		<-r.doneCh
	}
	r.deregister()
}

func (r *Registrar) loop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	r.beat()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.beat()
		}
	}
}

// counts sums live battle servers and their players. Mock instances host
// nothing and are excluded so the browser never advertises empty air.
func (r *Registrar) counts() (servers, players int) {
	for _, inst := range r.mgr.List() {
		if inst.Mock {
			continue
		}
		servers++
		n, _, _ := inst.PlayerState()
		players += n
	}
	return servers, players
}

func (r *Registrar) beat() {
	if r.optedOut() {
		// Was listed, now opted out: say goodbye so the row vanishes at
		// once instead of aging out over the staleness timeout.
		if r.clusterID != "" {
			r.deregister()
			r.clusterID = ""
		}
		return
	}
	ca, err := os.ReadFile(r.cfg.CAFile)
	if err != nil {
		// No CA file is not fatal: register without one, and browsers reach
		// the cluster through system roots (public certificate) or ask for a
		// pasted CA. A wrong path looks identical, so keep shouting about it.
		r.logf("directory: cannot read CA file %s: %v (registering without a CA)", r.cfg.CAFile, err)
		ca = nil
	}
	servers, players := r.counts()
	body, _ := json.Marshal(map[string]interface{}{
		"name":          r.cfg.Name,
		"web_url":       r.cfg.WebURL,
		"battle_ip":     r.cfg.BattleIP,
		"version":       r.cfg.Version,
		"motd":          r.cfg.MOTD,
		"ca_cert":       string(ca),
		"contact_email": r.cfg.Email,
		"agent_url":     r.cfg.AgentURL,
		"players":       players,
		"servers":       servers,
	})
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(r.cfg.MasterURL, "/")+"/clusters/register", bytes.NewReader(body))
	if err != nil {
		r.logf("directory: heartbeat request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		r.logf("directory: heartbeat to %s failed: %v", r.cfg.MasterURL, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		r.logf("directory: heartbeat rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(detail)))
		return
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" {
		r.logf("directory: heartbeat answered without an id")
		return
	}
	if r.clusterID == "" {
		r.logf("directory: listed as %q at %s (%s)", r.cfg.Name, r.cfg.MasterURL, out.ID)
	}
	r.clusterID = out.ID
}

func (r *Registrar) deregister() {
	if r.clusterID == "" {
		return
	}
	req, err := http.NewRequest(http.MethodDelete,
		strings.TrimRight(r.cfg.MasterURL, "/")+"/clusters/"+r.clusterID, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := r.client.Do(req.WithContext(ctx))
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	r.logf("directory: deregistered %q", r.cfg.Name)
	r.clusterID = ""
}
