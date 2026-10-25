//go:build windows

package main

// The original launcher's look -- its background, logo, fonts and icon font --
// read at run time from the player's OWN copy of the original launcher,
// DreadnoughtLauncher.exe, which every Dreadnought install still has beside
// the DreadGame folder. Nothing of Grey Box's is copied into this repository
// or into dn-launcher.exe: the fonts are licensed for that launcher only, and
// the art is theirs. Without the file the page falls back to its own styling
// with the same layout.
//
// The original is a Solid State Networks DIRECT 5 host (README.md in the
// archive: "Dreadnought Launcher for DIRECT 5", skin 5.1.11.3). Its skin --
// main.html, main.css, images/, fonts/ -- is a ZIP appended to the exe
// (end-of-central-directory at the very end, the archive starting at
// 0x14F800), every entry encrypted with traditional PKWARE encryption
// (general-purpose flag bit 0) under one password the exe carries as a plain
// string. That password is not written here: originalSkinPassword finds it the
// way it was found in the first place, by trying the exe's own strings against
// one entry's encryption header and confirming with the entry's CRC.

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// originalLauncherExe is the original launcher's file name in an install.
const originalLauncherExe = "DreadnoughtLauncher.exe"

// originalAssetFiles are the skin entries the page uses, by the name the page
// asks for. Measured from the archive's central directory (sizes in bytes,
// uncompressed): launcher_bg.jpg 1063150, dn_logo_tm_white.png 70330,
// teko-light.woff2 15736, Planer-Regular-webfont.woff2 34825,
// dn-fonts.woff 22228.
var originalAssetFiles = map[string]struct{ entry, mime string }{
	"background": {"images/launcher_bg.jpg", "image/jpeg"},
	"logo":       {"images/dn_logo_tm_white.png", "image/png"},
	"teko":       {"fonts/teko/teko-light.woff2", "font/woff2"},
	"planer":     {"fonts/planer/Planer-Regular-webfont.woff2", "font/woff2"},
	"icons":      {"fonts/dn-fonts/dn-fonts.woff", "font/woff"},
}

var (
	originalAssetsMu    sync.Mutex
	originalAssetsCache = map[string]map[string]string{}
)

// originalAssets returns the skin files as data: URLs, read from the first
// DreadnoughtLauncher.exe found in dirs. found is the exe used ("" if none).
func originalAssets(dirs ...string) (assets map[string]string, found string, err error) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, originalLauncherExe)
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		originalAssetsMu.Lock()
		cached, ok := originalAssetsCache[path]
		originalAssetsMu.Unlock()
		if ok {
			return cached, path, nil
		}
		assets, err = readOriginalAssets(path)
		if err != nil {
			return nil, path, err
		}
		originalAssetsMu.Lock()
		originalAssetsCache[path] = assets
		originalAssetsMu.Unlock()
		return assets, path, nil
	}
	return nil, "", nil
}

func readOriginalAssets(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// archive/zip finds an archive with data in front of it: it locates the
	// end record from the back and corrects the offsets by the prefix size.
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s: no launcher skin inside (%w)", originalLauncherExe, err)
	}
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	password, err := originalSkinPassword(raw, zr)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, want := range originalAssetFiles {
		f := entries[want.entry]
		if f == nil {
			return nil, fmt.Errorf("%s: skin has no %s", originalLauncherExe, want.entry)
		}
		data, err := readEncryptedEntry(f, password)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", originalLauncherExe, want.entry, err)
		}
		out[name] = "data:" + want.mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}

// originalSkinPassword tries every run of 16-64 letters and digits in the
// exe's code and data (the part in front of the archive) on the skin's
// smallest encrypted entry. The 12-byte encryption header's last byte must
// equal the entry's CRC high byte (1 in 256 wrong passwords pass that), and the
// decrypted, inflated entry must then match its CRC.
func originalSkinPassword(raw []byte, zr *zip.Reader) ([]byte, error) {
	var probe *zip.File
	for _, f := range zr.File {
		if f.Flags&1 == 0 || f.CompressedSize64 < 12 {
			continue
		}
		if probe == nil || f.CompressedSize64 < probe.CompressedSize64 {
			probe = f
		}
	}
	if probe == nil {
		return nil, errors.New(originalLauncherExe + ": skin is not encrypted as expected")
	}
	header, err := rawEntry(probe)
	if err != nil {
		return nil, err
	}
	prefix := raw
	if off, err := probe.DataOffset(); err == nil && off > 0 && off < int64(len(raw)) {
		prefix = raw[:off]
	}
	isWord := func(c byte) bool {
		return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
	}
	for i := 0; i < len(prefix); {
		if !isWord(prefix[i]) {
			i++
			continue
		}
		j := i
		for j < len(prefix) && isWord(prefix[j]) {
			j++
		}
		if n := j - i; n >= 16 && n <= 64 {
			candidate := prefix[i:j]
			if zipCryptoHeaderOK(candidate, header, probe) {
				if _, err := decryptEntry(probe, header, candidate); err == nil {
					return append([]byte(nil), candidate...), nil
				}
			}
		}
		i = j
	}
	return nil, errors.New(originalLauncherExe + ": skin password not found")
}

func rawEntry(f *zip.File) ([]byte, error) {
	r, err := f.OpenRaw()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func readEncryptedEntry(f *zip.File, password []byte) ([]byte, error) {
	data, err := rawEntry(f)
	if err != nil {
		return nil, err
	}
	return decryptEntry(f, data, password)
}

// zipCrypto is the traditional PKWARE stream cipher (APPNOTE.TXT 6.1).
type zipCrypto struct{ k0, k1, k2 uint32 }

func newZipCrypto(password []byte) *zipCrypto {
	z := &zipCrypto{0x12345678, 0x23456789, 0x34567890}
	for _, c := range password {
		z.update(c)
	}
	return z
}

func (z *zipCrypto) update(c byte) {
	z.k0 = crc32Byte(z.k0, c)
	z.k1 = (z.k1+z.k0&0xff)*134775813 + 1
	z.k2 = crc32Byte(z.k2, byte(z.k1>>24))
}

func (z *zipCrypto) decrypt(c byte) byte {
	t := z.k2 | 2
	p := c ^ byte((t*(t^1))>>8)
	z.update(p)
	return p
}

// crc32Byte is one step of the CRC-32 the cipher's key schedule uses (the
// raw register update, without crc32.Update's pre- and post-inversion).
func crc32Byte(crc uint32, c byte) uint32 {
	return crc32.IEEETable[byte(crc)^c] ^ crc>>8
}

// checkByte is what the header's last byte must decrypt to: the CRC's high
// byte, or the modification time's when the sizes follow in a data descriptor.
func checkByte(f *zip.File) byte {
	if f.Flags&0x8 != 0 {
		return byte(f.ModifiedTime >> 8)
	}
	return byte(f.CRC32 >> 24)
}

func zipCryptoHeaderOK(password, data []byte, f *zip.File) bool {
	z := newZipCrypto(password)
	var last byte
	for _, c := range data[:12] {
		last = z.decrypt(c)
	}
	return last == checkByte(f)
}

func decryptEntry(f *zip.File, data, password []byte) ([]byte, error) {
	if len(data) < 12 {
		return nil, errors.New("entry too short")
	}
	z := newZipCrypto(password)
	plain := make([]byte, len(data))
	for i, c := range data {
		plain[i] = z.decrypt(c)
	}
	if plain[11] != checkByte(f) {
		return nil, errors.New("wrong password")
	}
	body := plain[12:]
	switch f.Method {
	case zip.Store:
	case zip.Deflate:
		inflated, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, err
		}
		body = inflated
	default:
		return nil, fmt.Errorf("compression method %d", f.Method)
	}
	if crc32.ChecksumIEEE(body) != f.CRC32 {
		return nil, errors.New("CRC mismatch")
	}
	return body, nil
}
