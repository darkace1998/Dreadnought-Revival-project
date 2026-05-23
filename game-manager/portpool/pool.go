package portpool

import (
	"fmt"
	"sync"
)

// Pool manages a range of UDP ports for game server instances.
type Pool struct {
	mu    sync.Mutex
	ports map[int]bool
	start int
	end   int
}

// New creates a port pool from start to end (inclusive).
func New(start, end int) *Pool {
	p := &Pool{
		ports: make(map[int]bool),
		start: start,
		end:   end,
	}
	return p
}

// Acquire returns a free port from the pool and marks it in-use.
func (p *Pool) Acquire() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.start; port <= p.end; port++ {
		if !p.ports[port] {
			p.ports[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free ports in range %d-%d", p.start, p.end)
}

// Release marks a port as available again.
func (p *Pool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ports, port)
}

// InUse returns the number of currently allocated ports.
func (p *Pool) InUse() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ports)
}

// Capacity returns the total number of ports in the pool.
func (p *Pool) Capacity() int {
	return p.end - p.start + 1
}
