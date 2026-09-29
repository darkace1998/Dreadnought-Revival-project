package directory

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"dn-dedicated/internal/server"
)

type stubDirectory struct {
	t         *testing.T
	registers int32
	deletes   int32
	last      map[string]interface{}
}

func (s *stubDirectory) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/clusters/register":
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.last = body
		atomic.AddInt32(&s.registers, 1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"cluster-1","status":"registered"}`))
	case r.Method == http.MethodDelete:
		atomic.AddInt32(&s.deletes, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deregistered"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func testRegistrar(t *testing.T, stub *stubDirectory, extra func(*Config)) (*Registrar, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("fake-pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		MasterURL: srv.URL, Name: "Test Cluster", WebURL: "https://play.example.org",
		BattleIP: "203.0.113.7", Version: "1.0", MOTD: "hi",
		CAFile: ca, Email: "owner@example.org", Log: io.Discard,
	}
	if extra != nil {
		extra(&cfg)
	}
	mgr := server.NewManager(server.ManagerConfig{})
	return New(cfg, mgr), srv
}

func TestDisabledWithoutConfig(t *testing.T) {
	stub := &stubDirectory{t: t}
	r, _ := testRegistrar(t, stub, func(c *Config) { c.MasterURL = "" })
	if r.Enabled() {
		t.Fatal("empty MasterURL must disable registration")
	}
	r2, _ := testRegistrar(t, stub, func(c *Config) { c.Email = "" })
	if r2.Enabled() {
		t.Fatal("empty contact email must disable registration")
	}
	r.Start() // must not contact anything
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&stub.registers) != 0 {
		t.Fatal("disabled registrar must not register")
	}
	r.Stop()
}

func TestRegistersAndHeartbeats(t *testing.T) {
	stub := &stubDirectory{t: t}
	r, _ := testRegistrar(t, stub, nil)
	if !r.Enabled() {
		t.Fatal("full config must enable registration")
	}
	r.beat()
	r.beat()
	if got := atomic.LoadInt32(&stub.registers); got != 2 {
		t.Fatalf("registers = %d, want 2", got)
	}
	if stub.last["name"] != "Test Cluster" || stub.last["battle_ip"] != "203.0.113.7" {
		t.Errorf("payload wrong: %v", stub.last)
	}
	if _, ok := stub.last["ca_cert"]; !ok {
		t.Error("ca_cert missing from payload")
	}
	r.Stop()
}

func TestOptOutSuppressesAndDeregisters(t *testing.T) {
	stub := &stubDirectory{t: t}
	flag := filepath.Join(t.TempDir(), "dn-no-master-server.txt")
	var r *Registrar
	r, _ = testRegistrar(t, stub, func(c *Config) { c.NoMasterFile = flag })
	r.beat()
	if got := atomic.LoadInt32(&stub.registers); got != 1 {
		t.Fatalf("registers = %d, want 1", got)
	}
	// Opt out: next beat deregisters instead of registering.
	if err := os.WriteFile(flag, []byte("unlisted"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.beat()
	if got := atomic.LoadInt32(&stub.deletes); got != 1 {
		t.Fatalf("deletes = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&stub.registers); got != 1 {
		t.Fatalf("registers = %d after opt-out, want still 1", got)
	}
	// Back in: resumes.
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	r.beat()
	if got := atomic.LoadInt32(&stub.registers); got != 2 {
		t.Fatalf("registers = %d after re-list, want 2", got)
	}
	r.Stop()
}

func TestMissingCAFileRegistersWithoutCA(t *testing.T) {
	stub := &stubDirectory{t: t}
	r, _ := testRegistrar(t, stub, func(c *Config) { c.CAFile = filepath.Join(t.TempDir(), "nope.crt") })
	r.beat()
	if got := atomic.LoadInt32(&stub.registers); got != 1 {
		t.Fatalf("registers = %d, want 1 (without CA)", got)
	}
	if ca, _ := stub.last["ca_cert"].(string); ca != "" {
		t.Errorf("ca_cert = %q, want empty", ca)
	}
	r.Stop()
}
