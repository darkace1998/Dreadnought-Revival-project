//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The original launcher's skin comes out of a real DreadnoughtLauncher.exe:
// the password is found among the exe's own strings, and every file decrypts,
// inflates and matches its CRC. Needs the exe: DN_ORIGINAL_LAUNCHER_DIR, or
// the repo's sibling src/Dreadnought (read-only game files).
func TestOriginalAssetsFromLauncherExe(t *testing.T) {
	dir := os.Getenv("DN_ORIGINAL_LAUNCHER_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "src", "Dreadnought")
	}
	if _, err := os.Stat(filepath.Join(dir, originalLauncherExe)); err != nil {
		t.Skipf("no %s in %s", originalLauncherExe, dir)
	}
	assets, path, err := originalAssets(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, originalLauncherExe) {
		t.Fatalf("used %q", path)
	}
	magic := map[string][]byte{
		"background": {0xFF, 0xD8, 0xFF},
		"logo":       []byte("\x89PNG"),
		"teko":       []byte("wOF2"),
		"planer":     []byte("wOF2"),
		"icons":      []byte("wOFF"),
	}
	for name, want := range magic {
		url := assets[name]
		_, b64, ok := strings.Cut(url, ";base64,")
		if !ok {
			t.Fatalf("%s: not a data URL", name)
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || !bytes.HasPrefix(data, want) {
			t.Fatalf("%s: %d bytes, starts %q, want %q (%v)", name, len(data), data[:min(len(data), 4)], want, err)
		}
	}
	// No exe: no assets and no error -- the page keeps its own look.
	if a, p, err := originalAssets(t.TempDir()); a != nil || p != "" || err != nil {
		t.Fatalf("empty folder: %v %q %v", a != nil, p, err)
	}
}

func TestZipCryptoRejectsWrongPassword(t *testing.T) {
	// The check byte of an encryption header made under one password does not
	// survive another (it does for 1 in 256, hence the CRC check after it).
	z := newZipCrypto([]byte("right"))
	header := make([]byte, 12)
	for i := range header {
		// Encrypt: c = p ^ keystream, then update with p.
		t2 := z.k2 | 2
		p := byte(i)
		header[i] = p ^ byte((t2*(t2^1))>>8)
		z.update(p)
	}
	z = newZipCrypto([]byte("right"))
	for i, c := range header {
		if got := z.decrypt(c); got != byte(i) {
			t.Fatalf("byte %d: %d", i, got)
		}
	}
	z = newZipCrypto([]byte("wrong"))
	same := true
	for i, c := range header {
		if z.decrypt(c) != byte(i) {
			same = false
		}
	}
	if same {
		t.Fatal("a wrong password decrypted the header")
	}
}
