package main

import (
	"sync"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Stale balances, fixed at the source.
//
// The client learns its credit/GP figures exactly once per session — the
// YA_RewardCurrencies push after YA_PlayerGet — and that push was never seen
// to re-fire mid-session. Everything that moves money afterwards (battle
// rewards, purchases, claims, conversions, contracts, admin grants) wrote to
// the database while the connected client kept showing the login-time
// figures: the store displayed 0 credits and refused purchases the server
// would have approved (verified live 2026-09-28, fixed by a client restart).
//
// So every balance mutation marks the account dirty, and the per-connection
// frame loop (pushMatchProgress, which already runs every pass and on every
// read timeout) drains the flag into a fresh YA_RewardCurrencies push. The
// handler assigns rather than adds, so a repeated push is idempotent.
var currencyDirty = struct {
	sync.Mutex
	at map[string]time.Time
}{at: make(map[string]time.Time)}

// currencyDirtyTTL bounds how long a stale flag survives a player who never
// comes back: the next YA_PlayerGet pushes the balance anyway, so an old
// flag is only memory.
const currencyDirtyTTL = 30 * time.Minute

// markCurrencyDirty records that pid's displayed balance is stale. Safe for
// pids that never had one: the flag simply never matches a connection.
func markCurrencyDirty(pid string) {
	pid = protocol.NormalizePlayerPID(pid)
	if pid == "" {
		return
	}
	currencyDirty.Lock()
	defer currencyDirty.Unlock()
	for k, t := range currencyDirty.at {
		if time.Since(t) > currencyDirtyTTL {
			delete(currencyDirty.at, k)
		}
	}
	currencyDirty.at[pid] = time.Now()
}

// consumeCurrencyDirty reports whether pid needs a fresh YA_RewardCurrencies
// push, clearing the flag. Expired flags report false: the balance they
// guarded is pushed at the next login regardless.
func consumeCurrencyDirty(pid string) bool {
	pid = protocol.NormalizePlayerPID(pid)
	if pid == "" {
		return false
	}
	currencyDirty.Lock()
	defer currencyDirty.Unlock()
	t, ok := currencyDirty.at[pid]
	if !ok {
		return false
	}
	delete(currencyDirty.at, pid)
	return time.Since(t) <= currencyDirtyTTL
}
