#!/usr/bin/env python3
"""vtslot.py -- which vtable slots point at a function.

  python vtslot.py <rva> [<rva> ...]

A native that has no direct callers is usually virtual: an RPC's
_Implementation, an OnRep, an engine override. The exec thunk then calls it as
`(**(code **)(*param_1 + SLOT))(...)`, and this prints SLOT for each vtable
holding the function, so you can grep the decompile for `+ SLOT))(` and find
the thunk -- whose registration (`thunk_FUN_140d03140(.., "Name", thunk)`)
names it.

The build has no RTTI, so a vtable is found as the run of .text pointers around
the hit: it walks back over consecutive pointers into .text and reports the
offset from there. A table that starts with a non-code entry would make the
start (and so the slot) come out short; two hits in one "table" usually mean
two adjacent vtables (a class and a subclass) merged by the walk. Cross-check
the slot against the thunk before trusting it.

Example (2026-10-16): 0x5AFEA0 has no callers; it sits at slot 0x698 of the
PRI vtable, and the thunk calling `*param_1 + 0x698` is registered as
"ClientEventResult" -- so 0x5AFEA0 is ClientEventResult_Implementation.

Pure stdlib. Uses DREADGAME_EXE like the other scripts.
"""
import os
import struct
import sys

from pe import DEFAULT_EXE  # noqa: F401  (same default path as the other scripts)


def main():
    exe = os.environ.get("DREADGAME_EXE") or DEFAULT_EXE
    d = open(exe, "rb").read()
    pe = struct.unpack_from("<I", d, 0x3C)[0]
    nsec = struct.unpack_from("<H", d, pe + 6)[0]
    opt = struct.unpack_from("<H", d, pe + 20)[0]
    base = struct.unpack_from("<Q", d, pe + 24 + 24)[0]
    secs = []
    o = pe + 24 + opt
    for _ in range(nsec):
        name = d[o:o + 8].rstrip(b"\0").decode()
        vs, va, rs, ra = struct.unpack_from("<IIII", d, o + 8)
        secs.append((name, va, vs, ra, rs))
        o += 40
    text = next(s for s in secs if s[0] == ".text")
    lo, hi = base + text[1], base + text[1] + text[2]

    def off2rva(off):
        for _, va, _, ra, rs in secs:
            if ra <= off < ra + rs:
                return va + off - ra
        return None

    for arg in sys.argv[1:]:
        rva = int(arg, 16)
        pat = struct.pack("<Q", base + rva)
        h = d.find(pat)
        if h == -1:
            print(f"0x{rva:X}: no pointer to it anywhere in the file (not virtual)")
        while h != -1:
            st = h
            while lo <= struct.unpack_from("<Q", d, st - 8)[0] < hi:
                st -= 8
            n = 0
            while lo <= struct.unpack_from("<Q", d, st + n * 8)[0] < hi:
                n += 1
            print(f"0x{rva:X}: vtable at rva 0x{off2rva(st):X} ({n} entries), slot 0x{h - st:X}")
            h = d.find(pat, h + 1)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    main()
