package handlers

// OnBalanceChanged, when set, is called with the user id after an admin
// operation changed an account's balances. The owner is mmogbrain's main
// package, which holds the live client connections and turns this into a
// fresh YA_RewardCurrencies push; without it a connected client keeps showing
// its login-time figures until it restarts.
//
// Defaults to a no-op so the handlers package is usable (and testable)
// without a connection registry.
var OnBalanceChanged = func(userID string) {}
