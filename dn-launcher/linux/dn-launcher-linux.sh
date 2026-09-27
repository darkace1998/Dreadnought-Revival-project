#!/usr/bin/env bash
# Dreadnought private server launcher for Linux.
#
# Dreadnought is a Windows game, so on Linux it runs under Wine or Proton, and
# everything the launcher sets up for it -- the server certificate the game
# must trust, the sign-in token it reads from its registry, its start
# arguments -- has to live INSIDE that same Wine/Proton prefix. So this script
# does not reimplement the launcher: it finds the game and its prefix and runs
# the Windows launcher (dn-launcher.exe, next to this script) there.
#
#   ./dn-launcher-linux.sh                 start (sign-in opens in your browser)
#   ./dn-launcher-linux.sh --install       add a menu entry with the icon
#   ./dn-launcher-linux.sh --sign-out      forget the saved sign-in
#
# Settings (environment, or ~/.config/dreadnought-launcher/config):
#   DN_GAME_DIR   the Dreadnought folder (the one containing DreadGame/);
#                 default: searched in your Steam libraries
#   DN_RUNNER     proton | wine; default: Proton when the game has a Proton
#                 prefix (it was started through Steam once), else wine
#   DN_PROTON     the Proton folder to use; default: the newest one found
#   WINE          the wine binary (default: wine)
#   WINEPREFIX    the Wine prefix (default: ~/.local/share/dreadnought-wine)
set -euo pipefail

HERE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/dreadnought-launcher"
# shellcheck disable=SC1091
[ -f "$CONFIG_DIR/config" ] && . "$CONFIG_DIR/config"

EXE="$HERE/dn-launcher.exe"
APP_ID=835860

die() { echo "dn-launcher: $*" >&2; exit 1; }
say() { echo "dn-launcher: $*"; }

if [ "${1:-}" = "--install" ]; then
  apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
  mkdir -p "$apps"
  cat > "$apps/dreadnought-private-server.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Dreadnought (private server)
Comment=Sign in and play on the Dreadnought private server
Exec="$HERE/dn-launcher-linux.sh"
Icon=$HERE/icon.png
Terminal=false
Categories=Game;
EOF
  say "menu entry installed: $apps/dreadnought-private-server.desktop"
  exit 0
fi

[ -f "$EXE" ] || die "dn-launcher.exe not found next to this script ($HERE)"

# --- Steam libraries --------------------------------------------------------
steam_roots() {
  for r in "$HOME/.steam/steam" "$HOME/.local/share/Steam" \
           "$HOME/.var/app/com.valvesoftware.Steam/data/Steam"; do
    [ -d "$r/steamapps" ] && readlink -f "$r"
  done | sort -u
}

steam_libraries() {
  local root vdf
  steam_roots | while IFS= read -r root; do
    echo "$root"
    vdf="$root/steamapps/libraryfolders.vdf"
    if [ -f "$vdf" ]; then sed -n 's/^[[:space:]]*"path"[[:space:]]*"\(.*\)"/\1/p' "$vdf"; fi
  done | sort -u
}

game_exe_in() {
  local exe="$1/DreadGame/DreadGame/Binaries/Win64/DreadGame-Win64-Shipping.exe"
  [ -f "$exe" ] && echo "$exe"
}

find_game() {
  if [ -n "${DN_GAME_DIR:-}" ]; then
    game_exe_in "$DN_GAME_DIR" || die "no DreadGame-Win64-Shipping.exe under DN_GAME_DIR=$DN_GAME_DIR"
    return
  fi
  local lib d found=""
  while IFS= read -r lib; do
    for d in "$lib"/steamapps/common/*/; do
      if found="$(game_exe_in "${d%/}")"; then echo "$found"; return; fi
    done
  done < <(steam_libraries)
  die "Dreadnought not found in your Steam libraries. Set DN_GAME_DIR to the folder containing DreadGame/ (or put it in $CONFIG_DIR/config)."
}

GAME_EXE="$(find_game)"
# The launcher runs inside Wine, so it gets the Windows path (Z: is /).
export DN_GAME_PATH="Z:$(echo "$GAME_EXE" | sed 's#/#\\#g')"
say "game: $GAME_EXE"

# --- Proton or Wine -----------------------------------------------------------
compat_data() {
  local lib
  while IFS= read -r lib; do
    if [ -d "$lib/steamapps/compatdata/$APP_ID/pfx" ]; then echo "$lib/steamapps/compatdata/$APP_ID"; return; fi
  done < <(steam_libraries)
}

find_proton() {
  if [ -n "${DN_PROTON:-}" ]; then echo "$DN_PROTON"; return; fi
  local lib root p
  {
    while IFS= read -r lib; do
      for p in "$lib"/steamapps/common/Proton*; do [ -x "$p/proton" ] && echo "$p"; done
    done < <(steam_libraries)
    while IFS= read -r root; do
      for p in "$root"/compatibilitytools.d/*; do [ -x "$p/proton" ] && echo "$p"; done
    done < <(steam_roots)
  } | sort -V | tail -1
}

RUNNER="${DN_RUNNER:-}"
COMPAT="$(compat_data || true)"
PROTON="$(find_proton || true)"
if [ -z "$RUNNER" ]; then
  if [ -n "$COMPAT" ] && [ -n "$PROTON" ]; then RUNNER=proton; else RUNNER=wine; fi
fi

cd "$HERE"
case "$RUNNER" in
  proton)
    [ -n "$PROTON" ] || die "no Proton found (set DN_PROTON)"
    [ -n "$COMPAT" ] || die "the game has no Proton prefix yet: start Dreadnought once from Steam, or use DN_RUNNER=wine"
    STEAM_ROOT="$(steam_roots | head -1)"
    say "using Proton: $PROTON (prefix $COMPAT/pfx)"
    export STEAM_COMPAT_DATA_PATH="$COMPAT"
    export STEAM_COMPAT_CLIENT_INSTALL_PATH="$STEAM_ROOT"
    exec "$PROTON/proton" run "$EXE" "$@"
    ;;
  wine)
    command -v "${WINE:-wine}" >/dev/null || die "wine is not installed (or set WINE, or install Proton via Steam)"
    export WINEPREFIX="${WINEPREFIX:-$HOME/.local/share/dreadnought-wine}"
    if [ ! -d "$WINEPREFIX" ]; then
      say "creating the Wine prefix $WINEPREFIX (first start only)"
      WINEDEBUG=-all "${WINE:-wine}" wineboot --init >/dev/null 2>&1 || true
    fi
    say "using Wine: $(command -v "${WINE:-wine}") (prefix $WINEPREFIX)"
    exec "${WINE:-wine}" "$EXE" "$@"
    ;;
  *) die "DN_RUNNER must be proton or wine, not '$RUNNER'" ;;
esac
