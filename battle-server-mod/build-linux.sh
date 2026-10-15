#!/usr/bin/env bash
# Build dn_host_loadout.dll on Linux, no Windows/MSVC needed.
#
# Cross-compiles with clang (LLVM, not system GCC: GCC/MinGW refuses the
# __try/__except SEH keywords in C++ files, clang accepts them under
# -fms-extensions and emits the same .pdata/.xdata tables ntdll unwinds). Target = x86_64 windows-gnu, so
# the CRT imports are the UCRT api-ms-win-crt-* sets -- the game binary itself
# requires those too, so any prefix that runs the game runs this DLL.
#
# Everything else mirrors build.bat: MinHook sources in third_party, static
# CRT, links kernel32/user32/ws2_32. Build and copies are standalone; nothing
# in the Go stack depends on the output.
#
# Usage:   bash build-linux.sh
# Output:  build-linux/dn_host_loadout.dll
#          (rename to wer.dll beside the game exe when deploying, as usual)

set -euo pipefail
cd "$(dirname "$0")"

CLANG=clang++
TARGET=x86_64-pc-windows-gnu
OUT=build-linux
MH=third_party/minhook

command -v "$CLANG" >/dev/null || { echo "ERROR: clang++ not found" >&2; exit 1; }

# A stale DLL must not pass for a fresh one.
mkdir -p "$OUT"
rm -f "$OUT/dn_host_loadout.dll"

echo "Building $OUT/dn_host_loadout.dll ..."
echo "$(date -u '+%Y-%m-%d %H:%M:%SZ') clang $(clang --version | head -1)" | tee "$OUT/build.log"

set -x
"$CLANG" --target="$TARGET" -fms-extensions \
    -shared -O2 -std=c++17 \
    -D_CRT_SECURE_NO_WARNINGS -DWIN32_LEAN_AND_MEAN \
    -I"$MH/include" \
    -Wno-everything \
    -o "$OUT/dn_host_loadout.dll" \
    src/dn_host_loadout.cpp \
    "$MH/src/buffer.c" "$MH/src/hook.c" "$MH/src/trampoline.c" \
    "$MH/src/hde/hde64.c" \
    -lkernel32 -luser32 -lws2_32 -static
set +x

test -f "$OUT/dn_host_loadout.dll"
echo "OK: $OUT/dn_host_loadout.dll"