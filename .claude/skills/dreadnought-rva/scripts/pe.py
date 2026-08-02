"""Minimal PE reader for the DreadGame shipping binary.

Shared by the other scripts in this skill so they all agree on what a function
is. That agreement is the point: the authority for "is this RVA a function?" is
the PE exception directory (.pdata), never a byte pattern.

Set DREADGAME_EXE to point at your own copy. No game content ships with this
skill -- every script reads the executable you already own.
"""
import bisect
import os
import struct
import sys

DEFAULT_EXE = (r"D:\Dreadnought\DreadnoughtFullGame\Dreadnought\DreadGame"
               r"\DreadGame\Binaries\Win64\DreadGame-Win64-Shipping.exe")


class PE:
    def __init__(self, path=None):
        self.path = path or os.environ.get("DREADGAME_EXE", DEFAULT_EXE)
        if not os.path.exists(self.path):
            sys.exit("executable not found: %s\n"
                     "set DREADGAME_EXE to your own copy" % self.path)
        with open(self.path, "rb") as f:
            self.data = data = f.read()

        pe = struct.unpack_from("<I", data, 0x3C)[0]
        nsec = struct.unpack_from("<H", data, pe + 6)[0]
        optsz = struct.unpack_from("<H", data, pe + 20)[0]
        self.image_base = struct.unpack_from("<Q", data, pe + 24 + 24)[0]

        # Data directory entry 3 is the exception directory.
        dd = pe + 24 + 112
        exc_rva, exc_sz = struct.unpack_from("<II", data, dd + 3 * 8)

        self.sections = []
        so = pe + 24 + optsz
        for i in range(nsec):
            o = so + i * 40
            name = data[o:o + 8].rstrip(b"\0").decode("latin1")
            vsz, va, rsz, ptr = struct.unpack_from("<IIII", data, o + 8)
            self.sections.append((name, va, vsz, ptr, rsz))

        self._by_off = sorted((ptr, ptr + rsz, va)
                              for _, va, _, ptr, rsz in self.sections if rsz)
        self._off_keys = [s[0] for s in self._by_off]

        # RUNTIME_FUNCTION records: begin, end, unwind -- 12 bytes each.
        po = self.rva2off(exc_rva)
        self.funcs = []
        for i in range(exc_sz // 12):
            b, e, _ = struct.unpack_from("<III", data, po + i * 12)
            if b == 0 and e == 0:
                break
            self.funcs.append((b, e))
        self.funcs.sort()
        self._starts = [b for b, _ in self.funcs]
        self._entries = set(self._starts)

        text = next(s for s in self.sections if s[0] == ".text")
        self.text_off = text[3]
        self.text_end = text[3] + text[4]
        self.text_rva = text[1]

    def rva2off(self, rva):
        for _, va, vsz, ptr, rsz in self.sections:
            if va <= rva < va + max(vsz, rsz):
                return ptr + (rva - va)
        return None

    def off2rva(self, off):
        i = bisect.bisect_right(self._off_keys, off) - 1
        if i >= 0 and self._by_off[i][0] <= off < self._by_off[i][1]:
            return self._by_off[i][2] + (off - self._by_off[i][0])
        return None

    def text_off2rva(self, off):
        return self.text_rva + (off - self.text_off)

    def is_entry(self, rva):
        """True only for an exact .pdata function start."""
        return rva in self._entries

    def enclosing(self, rva):
        """The (begin, end) of the function containing rva, or None."""
        i = bisect.bisect_right(self._starts, rva) - 1
        if i >= 0 and self.funcs[i][0] <= rva < self.funcs[i][1]:
            return self.funcs[i]
        return None

    def gap_around(self, rva):
        """The (prev_end, next_start) hole an unrecorded RVA sits in."""
        i = bisect.bisect_right(self._starts, rva) - 1
        prev_end = self.funcs[i][1] if i >= 0 else None
        j = bisect.bisect_right(self._starts, rva)
        next_start = self.funcs[j][0] if j < len(self.funcs) else None
        return prev_end, next_start

    def describe(self, rva):
        if self.is_entry(rva):
            b, e = self.enclosing(rva)
            return "ENTRY of 0x%X-0x%X (size %d)" % (b, e, e - b)
        fn = self.enclosing(rva)
        if fn:
            return "inside 0x%X-0x%X, +0x%X from entry" % (fn[0], fn[1], rva - fn[0])
        # x64 permits LEAF functions -- no stack allocation, no calls, no
        # non-volatile register saves -- to carry no unwind record at all. So a
        # miss is not proof this isn't code. Report the hole and let the caller
        # look at the bytes.
        prev_end, next_start = self.gap_around(rva)
        span = ("gap 0x%X..0x%X, %d bytes" % (prev_end, next_start, next_start - prev_end)
                if prev_end and next_start else "outside the table")
        return "NO UNWIND RECORD (%s) -- padding, data, or a leaf/tail-call stub" % span
