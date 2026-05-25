#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
out_dir="${repo_root}/bin/wer-proxy"
cc="${CC:-x86_64-w64-mingw32-gcc}"

mkdir -p "${out_dir}"

"${cc}" \
  -D_WIN32_WINNT=0x0601 \
  -Wall \
  -Wextra \
  -Werror \
  -O2 \
  -static-libgcc \
  -shared \
  "${script_dir}/wer_proxy.c" \
  "${script_dir}/wer_proxy.def" \
  -o "${out_dir}/wer.dll"

printf 'Built %s\n' "${out_dir}/wer.dll"
