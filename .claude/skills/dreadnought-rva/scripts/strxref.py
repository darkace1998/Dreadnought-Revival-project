"""Find the function that logs a given string.

The workhorse. A log literal is the one piece of a stripped binary that still
says what the code around it is for, so almost every function in the RVA map was
found this way.

Indexes every RIP-relative LEA target in .text in a single pass, then reports
any LEA whose target lands in a window ending at the literal. The window matters:
UE4 references a literal at its FIRST character, so searching for text from the
middle of a message finds the bytes but no LEA pointing at them.

    python strxref.py "Active Loadout not found"
    python strxref.py --window 800 "Invalid player data"

Searches UTF-16LE and ASCII. UE4 log literals are almost always UTF-16LE.
"""
import bisect
import struct
import sys

from pe import PE

DEFAULT_WINDOW = 400


def build_lea_index(pe, verbose=True):
    """target RVA -> [RVAs of instructions pointing at it]"""
    data = pe.data
    targets = {}
    o = pe.text_off
    end = pe.text_end - 7
    if verbose:
        print("indexing LEAs in .text (%.1f MB)..."
              % ((pe.text_end - pe.text_off) / 1e6), file=sys.stderr)
    while o < end:
        # REX.W + 8D + modrm with mod=00 rm=101 -> lea reg, [rip+disp32]
        if data[o + 1] == 0x8D and 0x48 <= data[o] <= 0x4F and (data[o + 2] & 0xC7) == 0x05:
            disp = struct.unpack_from("<i", data, o + 3)[0]
            nxt = pe.text_off2rva(o + 7)
            targets.setdefault(nxt + disp, []).append(pe.text_off2rva(o))
        o += 1
    if verbose:
        print("  %d distinct LEA targets" % len(targets), file=sys.stderr)
    return targets


def literal_at(pe, rva, encoding):
    off = pe.rva2off(rva)
    if off is None:
        return ""
    raw = pe.data[off:off + 260]
    if encoding == "utf-16le":
        return raw.decode("utf-16le", "ignore").split("\0")[0]
    return raw.split(b"\0")[0].decode("latin1")


def main(argv):
    window = DEFAULT_WINDOW
    if len(argv) >= 2 and argv[0] == "--window":
        window = int(argv[1])
        argv = argv[2:]
    if not argv:
        sys.exit(__doc__)
    needle = argv[0]

    pe = PE()
    targets = build_lea_index(pe)
    keys = sorted(targets)

    hits = 0
    for encname, pat in (("utf-16le", needle.encode("utf-16le")),
                         ("ascii", needle.encode("ascii", "ignore"))):
        if not pat:
            continue
        pos = 0
        while True:
            pos = pe.data.find(pat, pos)
            if pos < 0:
                break
            srva = pe.off2rva(pos)
            if srva is None:
                pos += 1
                continue
            found = False
            lo = bisect.bisect_left(keys, srva - window)
            hi = bisect.bisect_right(keys, srva)
            for t in keys[lo:hi]:
                lit = literal_at(pe, t, encname)
                # The window can straddle unrelated neighbouring literals, so
                # only report the one that actually contains the needle.
                if needle not in lit:
                    continue
                for irva in targets[t]:
                    print("\n[%s] str 0x%X <- LEA 0x%X (target 0x%X)"
                          % (encname, srva, irva, t))
                    print("    func:    %s" % pe.describe(irva))
                    print("    literal: %s"
                          % lit[:200].encode("ascii", "replace").decode())
                    found = True
                    hits += 1
            if not found:
                print("\n[%s] str 0x%X : no LEA within %d bytes"
                      % (encname, srva, window))
            pos += 1

    if not hits:
        print("\nno function found. try a shorter or earlier fragment of the "
              "message, or --window 800 -- the literal is referenced at its "
              "first character.")


if __name__ == "__main__":
    main(sys.argv[1:])
