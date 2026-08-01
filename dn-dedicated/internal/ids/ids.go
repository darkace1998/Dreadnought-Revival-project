// Package ids generates RFC 4122 version 4 UUIDs.
//
// This exists only so the module can avoid a google/uuid dependency; the output
// format is identical (36 characters, hyphenated, lowercase hex).
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random UUIDv4 string, e.g. "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9".
//
// It reads from crypto/rand and panics if the system entropy source fails,
// which on every supported platform means the process is already unrecoverable.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("dn-dedicated: crypto/rand unavailable: " + err.Error())
	}
	// Set the version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
