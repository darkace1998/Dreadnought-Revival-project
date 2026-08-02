"""Answer "is this RVA a real function entry?" from the PE exception directory.

Run this before hooking anything. Byte heuristics get it wrong: scanning
backwards for INT3 padding finds 0xCC bytes that are the last byte of an
ordinary instruction -- `49 8B CC` (mov rcx, r12) ends in 0xCC and looks exactly
like padding. .pdata is a table the linker emitted; it does not guess.

    python pdata.py 370970 382530 0x53C870
    python pdata.py --stats
"""
import sys

from pe import PE


def main(argv):
    pe = PE()
    if not argv or argv[0] in ("-h", "--help"):
        sys.exit(__doc__)

    if argv[0] == "--stats":
        sizes = [e - b for b, e in pe.funcs]
        print("%s" % pe.path)
        print("image base   0x%X" % pe.image_base)
        print(".text        0x%X..0x%X" % (pe.text_rva,
                                           pe.text_rva + (pe.text_end - pe.text_off)))
        print("functions    %d RUNTIME_FUNCTION records" % len(pe.funcs))
        print("smallest     %d bytes" % min(sizes))
        print("largest      %d bytes" % max(sizes))
        print("first entry  0x%X" % pe.funcs[0][0])
        print("last entry   0x%X" % pe.funcs[-1][0])
        return

    for arg in argv:
        rva = int(arg, 16)
        print("0x%-9X %s" % (rva, pe.describe(rva)))


if __name__ == "__main__":
    main(sys.argv[1:])
