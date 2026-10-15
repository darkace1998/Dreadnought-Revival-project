#!/usr/bin/env python3
"""Minimal UE4 pak (v3, unencrypted) lister/extractor.

  pakx.py <paks-dir> <regex> [outdir]   # outdir omitted -> list matches only

Matches the regex (case-insensitive) against mount point + entry name and
extracts to outdir/<path relative to the mount point>.
"""
import glob, os, re, struct, sys, zlib


def fstring(f):
    n = struct.unpack('<i', f.read(4))[0]
    if n == 0:
        return ''
    if n < 0:
        return f.read(-n * 2).decode('utf-16-le').rstrip('\0')
    return f.read(n).decode('latin-1').rstrip('\0')


def entry(f):
    off, size, usize, comp = struct.unpack('<qqqi', f.read(28))
    f.read(20)  # sha1
    blocks = []
    if comp:
        n = struct.unpack('<i', f.read(4))[0]
        blocks = [struct.unpack('<qq', f.read(16)) for _ in range(n)]
    enc = f.read(1)[0]
    bsize = struct.unpack('<I', f.read(4))[0]
    return off, size, usize, comp, blocks, enc, bsize


def index(path):
    f = open(path, 'rb')
    f.seek(-44, 2)
    magic, ver, ioff, isize = struct.unpack('<IIqq', f.read(24))
    assert magic == 0x5A6F12E1, path
    f.seek(ioff)
    mount = fstring(f)
    n = struct.unpack('<i', f.read(4))[0]
    for _ in range(n):
        name = fstring(f)
        yield f, mount, name, entry(f)


def read(pak, e):
    off, size, usize, comp, blocks, enc, bsize = e
    assert not enc
    pak.seek(off)
    entry(pak)  # the record repeats its own header
    if not comp:
        return pak.read(size)
    out = bytearray()
    for s, t in blocks:  # absolute offsets in v3
        pak.seek(s)
        out += zlib.decompress(pak.read(t - s))
    return bytes(out)


def main():
    d, pat = sys.argv[1], re.compile(sys.argv[2], re.I)
    outdir = sys.argv[3] if len(sys.argv) > 3 else None
    for p in sorted(glob.glob(os.path.join(d, '*.pak'))):
        data = open(p, 'rb')
        for f, mount, name, e in index(p):
            full = mount + name
            if not pat.search(full):
                continue
            if not outdir:
                print(os.path.basename(p), full, e[2])
                continue
            rel = re.sub(r'^(\.\./)+', '', full)
            dst = os.path.join(outdir, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            open(dst, 'wb').write(read(data, e))
            print(dst)


if __name__ == '__main__':
    main()
