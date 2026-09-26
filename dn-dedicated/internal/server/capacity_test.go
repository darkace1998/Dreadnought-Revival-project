package server

import (
	"errors"
	"io"
	"testing"
)

// At the cap, Start refuses before touching the port pool or launching anything.
func TestStartRefusesAtCapacity(t *testing.T) {
	m := NewManager(ManagerConfig{MaxInstances: 1, LogTo: io.Discard})
	m.instances["running"] = &Instance{ID: "running"}
	if !m.AtCapacity() {
		t.Fatal("one running instance with MaxInstances=1 is not reported at capacity")
	}
	if _, err := m.Start(StartOptions{}); !errors.Is(err, ErrAtCapacity) {
		t.Fatalf("Start at capacity = %v, want ErrAtCapacity", err)
	}
	if m.PortsInUse() != 0 {
		t.Fatal("Start at capacity took a port")
	}
}

func TestNoCapWhenZero(t *testing.T) {
	m := NewManager(ManagerConfig{LogTo: io.Discard})
	for i := 0; i < 50; i++ {
		m.instances[string(rune('a'+i))] = &Instance{}
	}
	if m.AtCapacity() {
		t.Fatal("MaxInstances=0 must mean no cap")
	}
}

// On this box (16 GB): (16 GB - 2 GB) / 1.6 GB = 8.
func TestDefaultMaxInstancesFromMemory(t *testing.T) {
	total := totalMemoryBytes()
	if total <= 0 {
		t.Skip("memory not readable here")
	}
	n := DefaultMaxInstances()
	if n < 1 {
		t.Fatalf("DefaultMaxInstances = %d with %d bytes of memory", n, total)
	}
	t.Logf("MemTotal %.1f GB -> %d battle servers", float64(total)/(1<<30), n)
}
