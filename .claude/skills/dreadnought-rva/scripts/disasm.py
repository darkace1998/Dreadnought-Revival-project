"""Linear-disassemble bytes at an RVA in the DreadGame binary.

    python disasm.py <rva> [len] [--vex]

Annotates each operand's memory address with what pdata says about it and any
UTF-16/ASCII string that starts there in the binary -- the answer is usually
"which field name is this LEA loading".

Requires capstone: ~/.local/oci-venv/bin/pip install capstone
Run with ~/.local/oci-venv/bin/python (or any python that has it).

Set DREADGAME_EXE to your copy of the executable.
"""
import sys

import capstone

from pe import PE


def describe(pe, addr):
    rva = addr - pe.image_base
    parts = []
    # string?
    off = pe.rva2off(rva)
    if off is not None:
        data = pe.data[off:off + 160]
        for enc in ("utf-16-le", "ascii"):
            try:
                s = data.decode(enc)
            except UnicodeDecodeError:
                continue
            end = s.find("\0")
            if end > (2 if enc == "utf-16-le" else 3):
                s = s[:end].replace("\n", " ")[:64]
                printable = sum(c.isprintable() for c in s) / max(len(s), 1)
                if printable > 0.9:
                    parts.append('"%s"' % s)
            break
    what = pe.describe(rva)
    if "outside the table" not in what:
        parts.append(what.strip())
    return " ; ".join(parts)


def main(argv):
    rva = int(argv[0].replace("0x", ""), 16)
    n = int(argv[1], 0) if len(argv) > 1 else 128
    pe = PE()
    off = pe.rva2off(rva)
    code = pe.data[off:off + n]

    md = capstone.Cs(capstone.CS_ARCH_X86, capstone.CS_MODE_64)
    md.detail = False
    for ins in md.disasm(code, pe.image_base + rva):
        print("0x%x:\t%s\t%s%s" % (ins.address - pe.image_base, ins.mnemonic,
                                   ins.op_str,
                                   _annotate(pe, ins)))
        if ins.mnemonic in ("jmp",) and ins.op_str.startswith("0x"):
            # unconditional jmp to a fixed target: maybe show, but keep going
            pass


def _annotate(pe, ins):
    txt = ""
    if not ins.mnemonic.startswith("j"):
        import re
        for m in re.finditer(r"rip \+ (0x[0-9a-f]+)", ins.op_str):
            # rip-relative: rip = address of the NEXT instruction
            addr = ins.address + ins.size + int(m.group(1), 16)
            d = describe(pe, addr)
            if d:
                txt += "\n        ; rip-> 0x%x %s" % (addr - pe.image_base, d)
        for tok in ins.op_str.replace(",", " ").split():
            if tok.startswith("0x"):
                try:
                    addr = int(tok, 16)
                except ValueError:
                    continue
                d = describe(pe, addr)
                if d:
                    txt += "\n        ; %s %s" % (tok, d)
        return txt
    return txt


if __name__ == "__main__":
    main(sys.argv[1:])