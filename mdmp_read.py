import bisect
from minidump.minidumpfile import MinidumpFile

DUMP = "/root/projects/DreadGame-Win64-Shipping.DMP"
mf = MinidumpFile.parse(DUMP)
f = open(DUMP, "rb")

segs = sorted(mf.memory_segments_64.memory_segments, key=lambda s: s.start_virtual_address)
starts = [s.start_virtual_address for s in segs]
base_rva = mf.memory_segments_64.memory_segments[0].__dict__.get('rva', None)

# compute file offset base for memory64 list (rva field per-segment already given by lib as .rva if present)
def seg_file_offset(s):
    return s.start_file_address

def read(addr, size):
    out = b""
    remaining = size
    cur = addr
    while remaining > 0:
        i = bisect.bisect_right(starts, cur) - 1
        if i < 0 or i >= len(segs):
            break
        s = segs[i]
        if not (s.start_virtual_address <= cur < s.start_virtual_address + s.size):
            break
        off_in_seg = cur - s.start_virtual_address
        avail = s.size - off_in_seg
        take = min(avail, remaining)
        foff = seg_file_offset(s) + off_in_seg
        f.seek(foff)
        chunk = f.read(take)
        out += chunk
        cur += take
        remaining -= take
        if len(chunk) < take:
            break
    return out

def exe_base():
    for m in mf.modules.modules:
        if "DreadGame-Win64-Shipping.exe" in m.name:
            return m.baseaddress
    return None

if __name__ == "__main__":
    print("segments:", len(segs))
    print("exe base:", hex(exe_base()))
