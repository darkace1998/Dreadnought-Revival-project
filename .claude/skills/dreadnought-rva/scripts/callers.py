"""List every direct CALL (E8 rel32) to a target RVA.

Use after strxref.py has named a function, to walk outward and find the choke
point -- the single place several paths converge, which is usually where a hook
belongs. The spawn fix came from exactly this: two SpawnDefaultPawn callers both
routed through one loadout lookup, so one hook covered both.

    python callers.py 370970 336C90

Reports "none" for functions reached only through a vtable; that is a real
answer, not a failure. Ghidra's xref list is fine interactively, but this needs
no project lock and agrees with pdata.py about what a function is.
"""
import struct
import sys

from pe import PE


def direct_callers(pe, target):
    data = pe.data
    seen = {}
    o = pe.text_off
    end = pe.text_end - 5
    while o < end:
        if data[o] == 0xE8:
            rel = struct.unpack_from("<i", data, o + 1)[0]
            if pe.text_off2rva(o + 5) + rel == target:
                irva = pe.text_off2rva(o)
                seen.setdefault(pe.enclosing(irva) or ("orphan", irva), []).append(irva)
        o += 1
    return seen


def main(argv):
    if not argv:
        sys.exit(__doc__)
    pe = PE()
    for arg in argv:
        target = int(arg, 16)
        print("\n=== direct callers of 0x%X ===" % target)
        print("    (%s)" % pe.describe(target))
        seen = direct_callers(pe, target)
        if not seen:
            print("  none -- called only through a vtable, or never called")
            continue
        for key, sites in sorted(seen.items(), key=lambda kv: str(kv[0])):
            where = ", ".join("0x%X" % x for x in sites)
            if isinstance(key[0], str):
                print("  CALL at 0x%X -- NOT IN ANY FUNCTION" % key[1])
            else:
                print("  func 0x%X-0x%X  (calls at %s)" % (key[0], key[1], where))


if __name__ == "__main__":
    main(sys.argv[1:])
