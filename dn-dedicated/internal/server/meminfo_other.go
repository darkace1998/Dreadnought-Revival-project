//go:build !linux

package server

// totalMemoryBytes is not implemented off Linux; the cap then defaults to none.
func totalMemoryBytes() int64 { return 0 }
