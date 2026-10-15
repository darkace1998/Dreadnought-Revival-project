#!/usr/bin/env python3
"""cycle -- record a test cycle as a results bundle that outlives the session.

Every conclusion on this project comes from a test cycle, and until now a
cycle's evidence was whatever happened to be in a 70 MB log when someone next
grepped it: offsets lost, switches unrecorded, the binary unknown, the
screenshot never taken. A cycle bundle pins all of it:

    run/cycles/<YYYYmmdd-HHMMSS>-<label>/
      meta.json        git HEAD + dirty files, binary stamps, LIVE DN_ switches
                       (from /proc), log offsets, expectations, note
      <log>.log        each service log, sliced to exactly this cycle
      battle-*.log     battle-host logs created during the cycle
      shots/NN-*.png   screenshots (headless), + .txt OCR of each
      contact.png      every shot on one sheet -- one image read, not N
      summary.md       milestones, expectation verdicts, error digest (bounded)

Two ways in:

  operator cycle (the human plays):   cycle.py begin LABEL --expect RX [...]
                                      ... operator tests ...
                                      cycle.py end
  headless (nobody plays):            cycle.py headless LABEL --expect RX [...]
                                      runs the client on its own display (sway+Xwayland :97)
                                      under a separate account and prefix

  cycle.py list | show [ID] | grid ID

--expect RX   the cycle's discriminator: PASS needs a hit in some slice.
              Repeatable; one per hypothesis outcome you stated up front.
--reject RX   FAIL if it appears anywhere (e.g. ACCESS_VIOLATION).
"""
import argparse
import datetime as dt
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path

REPO = Path(os.environ.get("DN_REPO", Path.home() / "Projects/dreadnought"))
RUN = REPO / "run"
CYCLES = RUN / "cycles"
HEADLESS = RUN / "headless"
EXE = Path(os.environ.get("DREADGAME_EXE", Path.home() /
           ".local/share/Steam/steamapps/common/Dreadnought/DreadGame/DreadGame/"
           "Binaries/Win64/DreadGame-Win64-Shipping.exe"))
GAME_DIR = EXE.parent
PROTON = Path(os.environ.get("DN_PROTON", Path.home() /
              ".steam/root/compatibilitytools.d/GE-Proton10-34/files/bin"))
# gpu: rootful Xwayland on a headless sway -- GL on the real GPU (glamor).
# xvfb: llvmpipe software GL; the client's RenderThread abort()s (rc=3) ~30-60s
# into the hangar load there.
BACKEND = os.environ.get("DN_HEADLESS_BACKEND", "gpu")
DISPLAY = os.environ.get("DN_HEADLESS_DISPLAY", ":97" if BACKEND == "gpu" else ":98")  # :99 = battle hosts
RES = (1280, 720)
# The X screen is 16 rows taller than the game window on purpose. When a
# fullscreen-sized window covers the whole root, Xwayland 24 present-FLIPS its
# buffers straight to the compositor and the root pixmap x11grab reads goes
# stale -- a replay shot the ship picker for 2 min while the client
# log showed ~10 fps, spawn and battle. A window smaller than the root is
# copied, never flipped. Shots and clicks are relative to the game window.
SCREEN = (RES[0], RES[1] + 16)
# Empty by default: under taskset Wwise init fails ("Audiokinetic Audio Device
# initialization failed") and the client page-faults at startup. nice 15 still yields to everything else.
CLIENT_CPUS = os.environ.get("DN_HEADLESS_CPUS", "")
SLICE_CAP = 25_000_000  # bytes per log slice kept in a bundle

LOGS = {
    "mmog": REPO / "mmogbrain.log",          # repo root, NOT run/
    "client": RUN / "client.log",            # the operator's client (headless writes its own)
    "dedicated": RUN / "dn-dedicated.log",
    "gateway": RUN / "gateway.log",
    "auth": RUN / "auth-server.log",
    "mod": GAME_DIR / "dn_host_loadout.log",
}

# Client log milestones, in the order a healthy login produces them.
MILESTONES = [
    ("title", r"LogLoad: Took [\d.]+ seconds to LoadMap\(/Game/Maps/Launch_P\)"),
    ("firmament", r"Firmament Connection Established"),
    ("mmog", r"Mmog Connection Established"),
    ("playerdata", r"Player Data Received"),
    ("hangar", r"Entering Hangar"),
    ("fleets", r"UpdatePlayerFleetData \| Fleet Data Updated"),
    ("hangar-ship", r"ActivateLevel /Game/Maps/Menu/MN_HGR_"),   # hangar actually drawn
    ("battle-map", r"LoadMap\(/Game/Maps/(MP|Havoc|PVE)/"),
    # ship chosen, orbit -> arena under way: the in-battle discriminator
    ("orbit", r"Orbit sequence has started for player"),
    # the opposite: in a match with no pawn (seen when a client was sent back
    # into a match its previous, killed instance still held)
    ("NO-PAWN", r"No Pawn Owner in the UYPlayerOrbitComponent"),
]
CRASH = (r"ACCESS_VIOLATION|Unhandled Exception|Fatal error|appError|=== Critical error ===|"
         r"Unhandled (page fault|exception)|err:seh|wine: .*(crash|Unimplemented)|Assertion failed")


def now():
    return dt.datetime.now()


def sh(*a, **kw):
    return subprocess.run(a, capture_output=True, text=True, **kw)


def current_id():
    p = CYCLES / "CURRENT"
    return p.read_text().strip() if p.exists() else None


def live_switches():
    r = sh("pgrep", "-f", "run/mmogbrain$")
    pid = r.stdout.split()[0] if r.stdout.split() else None
    if not pid:
        return {"_mmogbrain": "NOT RUNNING"}
    try:
        env = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
    except OSError:
        return {"_mmogbrain": "environ unreadable"}
    out = {"_mmogbrain_pid": pid}
    for e in env:
        k, _, v = e.decode(errors="replace").partition("=")
        if k.startswith("DN_") or k in ("PLAYERS_PER_MATCH",):
            out[k] = v
    return out


def stamps():
    d = RUN / ".stamps"
    return {f.name: f.read_text().split()[-2:] for f in sorted(d.glob("*"))} if d.exists() else {}


# ---------------------------------------------------------------------------
def begin(label, expects, rejects, note="", mode="operator", extra=None):
    CYCLES.mkdir(parents=True, exist_ok=True)
    cid = f"{now():%Y%m%d-%H%M%S}-{re.sub(r'[^A-Za-z0-9_.-]+', '-', label)[:40]}"
    d = CYCLES / cid
    d.mkdir()
    head = sh("git", "-C", str(REPO), "rev-parse", "--short", "HEAD").stdout.strip()
    dirty = sh("git", "-C", str(REPO), "status", "--porcelain", "-uno").stdout.split("\n")
    meta = {
        "id": cid, "label": label, "mode": mode, "note": note,
        "started": now().isoformat(timespec="seconds"),
        "git_head": head, "git_dirty": [l[3:] for l in dirty if l.strip()],
        "stamps": stamps(), "switches": live_switches(),
        "offsets": {k: (p.stat().st_size if p.exists() else 0) for k, p in LOGS.items()},
        "battle_logs_before": sorted(p.name for p in (RUN / "battle-logs").glob("*.log")),
        "expect": expects, "reject": rejects,
    }
    if extra:
        meta.update(extra)
    (d / "meta.json").write_text(json.dumps(meta, indent=1))
    (CYCLES / "CURRENT").write_text(cid)
    sw = {k: v for k, v in meta["switches"].items() if not k.startswith("_")}
    print(f"cycle {cid} begun  head={head}{' +dirty' if meta['git_dirty'] else ''}  "
          f"switches={sw or '{}'}")
    if "_mmogbrain" in meta["switches"]:
        print(f"  !! mmogbrain {meta['switches']['_mmogbrain']} -- switches unrecorded")
    stale = sh(str(REPO / "scripts/dn"), "status").stdout
    for line in stale.splitlines():
        if "STALE" in line or "RUNNING OLD" in line or "DEPLOYED wer.dll" in line:
            print(f"  !! {line.strip()}")
    return d


def slice_log(path, start, dst):
    if not path.exists():
        return 0
    size = path.stat().st_size
    if size < start:      # rotated/truncated since begin: keep what exists
        start = 0
    start = max(start, size - SLICE_CAP)
    with open(path, "rb") as f, open(dst, "wb") as o:
        f.seek(start)
        shutil.copyfileobj(f, o)
    return size - start


def grep_lines(path, rx, limit=None):
    out = []
    try:
        with open(path, "r", errors="replace") as f:
            for line in f:
                if rx.search(line):
                    out.append(line.rstrip("\n"))
                    if limit and len(out) >= limit:
                        break
    except OSError:
        pass
    return out


def end(cid=None, client_log=None, quiet=False):
    cid = cid or current_id()
    if not cid:
        sys.exit("cycle: no current cycle (cycle.py begin LABEL first)")
    d = CYCLES / cid
    meta = json.loads((d / "meta.json").read_text())
    if meta.get("ended"):  # closed already: re-analyse the bundle, never re-slice
        summary, verdict = analyse(d, meta)
        meta["verdict"] = verdict
        (d / "meta.json").write_text(json.dumps(meta, indent=1))
        (d / "summary.md").write_text(summary)
        if not quiet:
            print(summary)
        return d, verdict
    meta["ended"] = now().isoformat(timespec="seconds")
    sizes = {}
    for k, p in LOGS.items():
        if k == "client" and client_log:
            continue  # headless: the run wrote its own client log into the bundle
        n = slice_log(p, meta["offsets"].get(k, 0), d / f"{k}.log")
        if n:
            sizes[k] = n
        else:
            (d / f"{k}.log").unlink(missing_ok=True)
    if client_log:
        sizes["client"] = Path(client_log).stat().st_size if Path(client_log).exists() else 0
    for p in sorted((RUN / "battle-logs").glob("*.log")):
        if p.name not in meta["battle_logs_before"] or p.stat().st_mtime > \
                dt.datetime.fromisoformat(meta["started"]).timestamp():
            if p.name in meta["battle_logs_before"]:
                continue  # appended-to old host log: too ambiguous, skip
            shutil.copy2(p, d / p.name)
            sizes[p.name] = p.stat().st_size
    meta["slice_bytes"] = sizes
    summary, verdict = analyse(d, meta)
    meta["verdict"] = verdict
    (d / "meta.json").write_text(json.dumps(meta, indent=1))
    (d / "summary.md").write_text(summary)
    if (CYCLES / "CURRENT").exists() and current_id() == cid:
        (CYCLES / "CURRENT").unlink()
    if not quiet:
        print(summary)
    return d, verdict


def analyse(d, meta):
    logs = sorted(p for p in d.glob("*.log"))
    L = [f"# cycle {meta['id']}  [{meta['mode']}]", ""]
    L.append(f"{meta['started']} -> {meta.get('ended', '?')}   head {meta['git_head']}"
             f"{' +dirty: ' + ', '.join(meta['git_dirty'][:6]) if meta['git_dirty'] else ''}")
    sw = {k: v for k, v in meta["switches"].items() if not k.startswith("_mmogbrain_pid")}
    L.append(f"switches: {', '.join(f'{k}={v}' for k, v in sorted(sw.items())) or '(none)'}")
    if meta.get("note"):
        L.append(f"note: {meta['note']}")
    if meta.get("account"):
        L.append(f"account: {meta['account']}")
    L.append("slices: " + ", ".join(f"{k} {v // 1024}K" for k, v in meta["slice_bytes"].items()))

    client = d / "client.log"
    if client.exists():
        hits = []
        for name, rx in MILESTONES:
            first = grep_lines(client, re.compile(rx), limit=1)
            if first:
                ts = re.match(r"\[([\d.:-]+)\]", first[0])
                hits.append(f"{name}@{ts.group(1)[11:19] if ts else '?'}")
            else:
                hits.append(f"{name}:--")
        L.append("milestones: " + "  ".join(hits))

    verdict = "NO-EXPECTATIONS"
    if meta["expect"] or meta["reject"]:
        L += ["", "## expectations"]
        ok = True
        for kind in ("expect", "reject"):
            for rx_s in meta[kind]:
                rx = re.compile(rx_s)
                found = [(p.name, l) for p in logs for l in grep_lines(p, rx, limit=3)]
                n = sum(len(grep_lines(p, rx)) for p in logs) if found else 0
                good = bool(found) if kind == "expect" else not found
                ok &= good
                L.append(f"- {'OK ' if good else 'BAD'} {kind} /{rx_s}/  {n} hit(s)")
                for name, line in found[:2]:
                    L.append(f"    {name}: {line.strip()[:220]}")
        verdict = "PASS" if ok else "FAIL"

    crashes = [(p.name, l) for p in logs for l in grep_lines(p, re.compile(CRASH), limit=2)]
    if re.search(r"exited rc=(?!0\b)", meta.get("stop_reason", "")):
        # rc=3 is the MSVC CRT's abort(): no crash report, no log line
        crashes.insert(0, ("headless", f"client {meta['stop_reason'].split('client ', 1)[-1]}"
                                       " (3 = abort(); find the thread with DN_WINEDEBUG=+threadname,+seh)"))
    if crashes:
        L += ["", "## crash markers"] + [f"- {n}: {l.strip()[:200]}" for n, l in crashes[:6]]
        if verdict == "NO-EXPECTATIONS":
            verdict = "CRASH"

    mmog = d / "mmog.log"
    if mmog.exists():
        reqs = {}
        warn = {}
        for line in open(mmog, errors="replace"):
            for m in re.findall(r"\b(YA_[A-Za-z]+)", line):
                reqs[m] = reqs.get(m, 0) + 1
            if re.search(r'level=(warn|error|fatal)|"level":"(warning|error|fatal|panic)"', line):
                m = re.search(r'msg="([^"]{0,90})', line) or re.search(r'"msg":"([^"]{0,90})', line)
                if m:
                    key = re.sub(r"\d+", "N", m.group(1))
                    warn[key] = warn.get(key, 0) + 1
        if reqs:
            top = sorted(reqs.items(), key=lambda kv: -kv[1])[:14]
            L += ["", "mmog YA_*: " + " ".join(f"{k[3:]}x{v}" for k, v in top)]
        if warn:
            L += ["", "## mmog warnings/errors (distinct, digits->N)"]
            L += [f"- {v:4d}  {k}" for k, v in sorted(warn.items(), key=lambda kv: -kv[1])[:8]]

    shots = sorted((d / "shots").glob("*.png")) if (d / "shots").exists() else []
    if shots:
        L += ["", f"## screenshots ({len(shots)})  contact sheet: {d.name}/contact.png"
              + ("" if ocr_ready() else " ")]
        for s in shots:
            txt = s.with_suffix(".txt")
            words = " ".join(txt.read_text(errors="replace").split())[:110] if txt.exists() else ""
            L.append(f"- {s.name}: {words}")
    L += ["", f"VERDICT: {verdict}"]
    return "\n".join(L) + "\n", verdict


def digest(path, start_rx=None, last=40, width=170):
    """Distinct client-log lines in order of first appearance, with counts.

    UE4 spam (60 'FindLoadoutByID' variants, a fleet poll every frame) makes
    raw tails useless; this collapses lines that differ only in numbers/ids
    and drops the frame/timestamp prefix, keeping the first time each shape
    appeared."""
    norm = re.compile(r"0x[0-9a-fA-F]+|[0-9a-f]{32}|\d+")
    seen, order = {}, []
    started = start_rx is None
    with open(path, "r", errors="replace") as f:
        for line in f:
            if not started:
                started = bool(re.search(start_rx, line))
                if not started:
                    continue
            line = line.rstrip("\n")
            m = re.match(r"\[[\d.:-]+\]\[\s*\d+\]", line)
            ts = line[12:20] if m else ""
            body = line[m.end():] if m else line
            if not body.strip() or "fixme:" in body or "CheckCompletedInitialization" in body:
                continue
            # UE4 glues a following line's category onto the end; cut there
            body = re.split(r"(?<=[.!)\]>])(?=Log[A-Z]\w+:)", body)[0]
            key = norm.sub("N", re.sub(r"Default__\w+", "<asset>", body))[:120]
            if key in seen:
                seen[key][1] += 1
            else:
                seen[key] = [ts, 1, body]
                order.append(key)
    out = []
    for k in order[-last:]:
        ts, n, body = seen[k]
        out.append(f"{ts} {'x%-4d' % n if n > 1 else '     '} {body.strip()[:width]}")
    return out


def cmd_list(_):
    if not CYCLES.exists():
        print("(no cycles)")
        return
    cur = current_id()
    for d in sorted(p for p in CYCLES.iterdir() if p.is_dir())[-25:]:
        try:
            m = json.loads((d / "meta.json").read_text())
        except (OSError, ValueError):
            continue
        state = "OPEN" if d.name == cur else m.get("verdict", "?")
        shots = len(list((d / "shots").glob("*.png"))) if (d / "shots").exists() else 0
        print(f"  {d.name:44s} {m['mode']:8s} {state:15s} {m['git_head']}"
              f"{'  shots=' + str(shots) if shots else ''}")


def cmd_show(a):
    cid = a.id or current_id() or sorted(p.name for p in CYCLES.iterdir() if p.is_dir())[-1]
    matches = [p for p in CYCLES.iterdir() if p.is_dir() and p.name.startswith(cid)] \
        if not (CYCLES / cid).exists() else [CYCLES / cid]
    if not matches:
        sys.exit(f"cycle: no such cycle {cid}")
    d = sorted(matches)[-1]
    s = d / "summary.md"
    print(s.read_text() if s.exists() else f"{d.name}: still open (cycle.py end)")


# ---------------------------------------------------------------------------
# headless client
# ---------------------------------------------------------------------------
def wine_env():
    env = dict(os.environ)
    # fixme-all, not -all: -all would also hide wine's err:seh / page-fault
    # reports, the only crash evidence a client without a minidump leaves.
    # Audio goes to a private null sink, never the operator's speakers. It must
    # be a real device: with none at all, "Audiokinetic Audio Device
    # initialization failed" is followed by a page fault on execute at startup
    #.
    env.update(DISPLAY=DISPLAY, WINEPREFIX=str(HEADLESS / "pfx"), WINEDEBUG=os.environ.get("DN_WINEDEBUG", "fixme-all"),
               WINEDLLOVERRIDES="mscoree,mshtml=", PULSE_SINK=ensure_null_sink())
    return env


NULL_SINK = "dn_headless"
STEAM_ROOT = Path.home() / ".local/share/Steam"
OPERATOR_COMPAT = STEAM_ROOT / "steamapps/compatdata/835860"


def proton_env(dxvk=False):
    """The env `proton runinprefix` needs. Going through Proton rather than
    its wine binary is what makes the run match the operator's client: Proton
    adds the DLL overrides (xaudio*, d3d*, ...) and DXVK/FAudio setup. Raw wine
    on the same prefix fails Wwise init and page-faults at startup."""
    for f in ("version", "config_info", "tracked_files"):
        if not (HEADLESS / f).exists() and (OPERATOR_COMPAT / f).exists():
            shutil.copy2(OPERATOR_COMPAT / f, HEADLESS / f)
    env = wine_env()
    env.pop("WINEPREFIX", None)
    # runinprefix skips Proton's prefix setup, which is where the DXVK "=n"
    # overrides come from (proton ~L1254), so wined3d renders. That MATCHES the
    # operator: their client reports "ATI Radeon HD 5600 Series" / 0x68d8,
    # wined3d's card-table name for the real GPU over GL. Forcing DXVK
    # (--dxvk) dies at device creation.
    if dxvk:
        env["WINEDLLOVERRIDES"] = "d3d11,d3d10core,d3d9,dxgi,d3d12,d3d12core=n"
    env.update(STEAM_COMPAT_DATA_PATH=str(HEADLESS), STEAM_COMPAT_CLIENT_INSTALL_PATH=str(STEAM_ROOT),
               SteamAppId="835860", SteamGameId="835860")
    return env


def ensure_null_sink():
    if NULL_SINK not in sh("pactl", "list", "short", "sinks").stdout:
        sh("pactl", "load-module", "module-null-sink", f"sink_name={NULL_SINK}",
           f"sink_properties=device.description={NULL_SINK}")
    return NULL_SINK


def ensure_display():
    """Start the X server for DISPLAY if it isn't up. Both stay running between
    runs (cheap); stop with: dn headless-stop"""
    if BACKEND == "gpu":
        if sh("pgrep", "-f", f"Xwayland {DISPLAY} ").returncode == 0:
            return
        conf = HEADLESS / "dn-sway.conf"
        conf.write_text(f"output HEADLESS-1 resolution {SCREEN[0]}x{SCREEN[1]}\nxwayland disable\n"
                        "swaybg_command -\ndefault_border none\n")
        swaylog = HEADLESS / "sway.log"
        if sh("pgrep", "-f", str(conf)).returncode != 0:
            subprocess.Popen(["sway", "-V", "-c", str(conf)], stdout=open(swaylog, "w"), stderr=subprocess.STDOUT,
                             env=dict(os.environ, WLR_BACKENDS="headless", WLR_LIBINPUT_NO_DEVICES="1"),
                             start_new_session=True)
        sock = None
        time.sleep(0.5)
        for _ in range(20):
            m = re.search(r"Running compositor on wayland display '([^']+)'", swaylog.read_text(errors="replace"))
            if m:
                sock = m.group(1)
                break
            time.sleep(0.5)
        if not sock:
            sys.exit(f"headless: sway did not come up, see {swaylog}")
        # -fullscreen: otherwise sway's tiling gives the root window 1276x691 and
        # every x11grab of 1280x720 fails ("outside the screen size")
        subprocess.Popen(["Xwayland", DISPLAY, "-geometry", f"{SCREEN[0]}x{SCREEN[1]}", "-fullscreen", "-noreset"],
                         stdout=open(HEADLESS / "xwayland.log", "w"), stderr=subprocess.STDOUT,
                         env=dict(os.environ, WAYLAND_DISPLAY=sock), start_new_session=True)
        for _ in range(20):
            time.sleep(0.5)
            if sh("xdpyinfo", "-display", DISPLAY).returncode == 0:
                return
        sys.exit(f"headless: Xwayland {DISPLAY} did not come up, see {HEADLESS / 'xwayland.log'}")
    if sh("pgrep", "-f", f"Xvfb {DISPLAY} ").returncode != 0:
        subprocess.Popen(["Xvfb", DISPLAY, "-screen", "0", f"{SCREEN[0]}x{SCREEN[1]}x24", "-nolisten", "tcp"],
                         stdout=open(HEADLESS / "xvfb.log", "w"), stderr=subprocess.STDOUT,
                         start_new_session=True)
        time.sleep(2)


def stop_display():
    """Stop the headless X server(s) (pkill -f on a bare pattern would also
    match the shell that runs it -- anchor on the argv start)."""
    for pat in (f"^Xwayland {DISPLAY} ", f"^sway -V -c {HEADLESS / 'dn-sway.conf'}", f"^Xvfb {DISPLAY} "):
        sh("pkill", "-f", pat)
    for _ in range(20):
        if all(sh("pgrep", "-f", pat).returncode != 0
               for pat in (f"^Xwayland {DISPLAY} ", f"^sway -V -c {HEADLESS / 'dn-sway.conf'}")):
            break
        time.sleep(0.25)
    print(f"headless display {DISPLAY} stopped")


def ensure_token(account, force=False):
    """Mint a JWT for `account` into the headless prefix (never the operator's).

    The launcher copy's dn-launcher.json points game_path at nothing, so it
    writes the token and stops instead of starting a second client."""
    stamp = HEADLESS / "token.json"
    if not force and stamp.exists():
        t = json.loads(stamp.read_text())
        if t.get("account") == account and time.time() - t.get("minted", 0) < 20 * 3600:
            return True
    env = wine_env()
    env["DN_PLAYER_ID"] = account
    r = subprocess.run([str(PROTON / "wine"), "./dn-launcher.exe"], cwd=HEADLESS / "launcher",
                       env=env, capture_output=True, text=True, timeout=120, input="\n")
    out = (r.stdout + r.stderr).replace("\r", "")
    (HEADLESS / "mint.log").write_text(out)
    if "Auth token written" not in out:
        bad = [l for l in out.splitlines() if "[!]" in l]
        print(f"headless: token mint FAILED for {account}: {bad[:2]}  (run/headless/mint.log)")
        if "443" in out and "refused" in out:
            print("  gateway is not on :443 -- `dn status` (setcap after a gateway rebuild?)")
        return False
    stamp.write_text(json.dumps({"account": account, "minted": time.time()}))
    return True


def headless_pids():
    """DreadGame processes running in OUR prefix -- never the operator's
    client or a battle host, which share the exe name."""
    pfx = str(HEADLESS / "pfx").encode()
    out = []
    for p in Path("/proc").iterdir():
        if not p.name.isdigit():
            continue
        try:
            if b"DreadGame" not in (p / "cmdline").read_bytes():
                continue
            if b"WINEPREFIX=" + pfx in (p / "environ").read_bytes():
                out.append(int(p.name))
        except OSError:
            pass
    return out


def stop_client(proc):
    for pid in headless_pids():
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass
    if proc and proc.poll() is None:
        os.killpg(proc.pid, signal.SIGKILL)
    subprocess.run([str(PROTON / "wineserver"), "-k"], env=wine_env(), capture_output=True)


def ocr_ready():
    """tesseract with English data (pacman: tesseract-data-eng)."""
    if not shutil.which("tesseract"):
        return False
    return "eng" in sh("tesseract", "--list-langs").stdout.split()


def host_crash(since):
    """First crash line of a battle-host log written since `since` (epoch),
    or None. The host dies on its own (exit 3) while the client sits in a dead
    match until UE's 180 s connection timeout -- check this instead of waiting
    that out."""
    rx = re.compile(r"Fatal error|Unhandled Exception|exited at .*err: exit status")
    for p in (RUN / "battle-logs").glob("*.log"):
        if p.stat().st_mtime >= since:
            hit = grep_lines(p, rx, limit=1)
            if hit:
                return f"{p.name}: {hit[0].strip()[:160]}"
    return None


def grab_input():
    """x11grab input spec for the game window (whole RES area from 0,0 before
    the window exists). Own X connection per call: shots come from several
    threads and Xlib connections are not thread-safe."""
    sys.path.insert(0, str(Path(__file__).parent))
    import xdrive
    disp = xdrive.Display(DISPLAY)
    try:
        w = disp.find("^DreadGame")
    finally:
        disp.close()
    x, y = (min(max(w[1], 0), SCREEN[0] - RES[0]), min(max(w[2], 0), SCREEN[1] - RES[1])) if w else (0, 0)
    return f"{DISPLAY}+{x},{y}"


def shoot(d, n, name):
    sd = d / "shots"
    sd.mkdir(exist_ok=True)
    png = sd / f"{n:02d}-{name}.png"
    r = sh("ffmpeg", "-loglevel", "error", "-y", "-f", "x11grab", "-video_size",
           f"{RES[0]}x{RES[1]}", "-i", grab_input(), "-frames:v", "1", str(png))
    if r.returncode == 0 and ocr_ready():
        sh("tesseract", str(png), str(png.with_suffix("")), "--psm", "11")
    return png


def contact_sheet(d):
    shots = sorted((d / "shots").glob("*.png"))
    if not shots:
        return
    cols = 3 if len(shots) > 4 else 2
    rows = -(-len(shots) // cols)
    # each tile 480x270 with its file name burnt in, so the sheet is self-indexing
    inputs, filt = [], []
    for i, s in enumerate(shots):
        inputs += ["-i", str(s)]
        filt.append(f"[{i}]scale=480:270,drawtext=text='{s.stem}':x=4:y=4:fontsize=16:"
                    f"fontcolor=yellow:box=1:boxcolor=black@0.6[t{i}]")
    pad = cols * rows - len(shots)
    for j in range(pad):
        filt.append(f"color=black:480x270:d=1[p{j}]")
    tiles = "".join(f"[t{i}]" for i in range(len(shots))) + "".join(f"[p{j}]" for j in range(pad))
    layout = "|".join(f"{(k % cols) * 480}_{(k // cols) * 270}" for k in range(cols * rows))
    filt.append(f"{tiles}xstack=inputs={cols * rows}:layout={layout}")
    r = sh("ffmpeg", "-loglevel", "error", "-y", *inputs, "-filter_complex", ";".join(filt),
           "-frames:v", "1", str(d / "contact.png"))
    if r.returncode != 0:  # drawtext needs a font build; fall back to plain tiles
        filt = [f.split(",drawtext")[0] + f"[t{i}]" if f.startswith(f"[{i}]") else f
                for i, f in enumerate(filt)]
        filt = [re.sub(r"\[(\d+)\]scale=480:270.*", lambda m: f"[{m.group(1)}]scale=480:270[t{m.group(1)}]", f)
                for f in filt]
        sh("ffmpeg", "-loglevel", "error", "-y", *inputs, "-filter_complex", ";".join(filt),
           "-frames:v", "1", str(d / "contact.png"))


def launch_client(a, d, log):
    """Start the client in the headless prefix; returns the Popen. Own session,
    so it outlives the caller (live mode relies on that)."""
    cmd = (["taskset", "-c", CLIENT_CPUS] if CLIENT_CPUS else []) + ["nice", "-n", "15", str(PROTON / "wine"), str(EXE),
           f"/Game/Maps/Launch_P?PlayerName={a.account}",
           "-GatewayAddress=127.0.0.1", "-GatewayPort=65443",
           "-YFirmamentAddress=127.0.0.1", "-YFirmamentPort=48843",
           "-noeac", "-NoSteam", "-nop4", "-nosplash", "-windowed",  # sound on: null sink, see wine_env
           f"-ResX={RES[0]}", f"-ResY={RES[1]}",
           "-stdout", "-AllowStdOutLogVerbosity", "-forcelogflush"]
    if not a.onboarding:
        # client-side gate bypass (dn-launcher's skip_onboarding): a fresh
        # test account otherwise lands in the S01E00 tutorial, not the hangar
        cmd.append("-noonboarding")
    if a.log_cmds:
        cmd.append(f"-LogCmds={a.log_cmds}")
    cmd = [c for c in cmd if c not in a.drop_arg] + a.client_arg
    if not a.raw_wine:
        i = cmd.index(str(PROTON / "wine"))
        cmd[i:i + 1] = ["python3", str(PROTON.parent.parent / "proton"), "runinprefix"]
    env = wine_env() if a.raw_wine else proton_env(a.dxvk)
    # Proton defaults DXVK_LOG_LEVEL=none; with --dxvk its logs land in the bundle
    env.update(DXVK_LOG_PATH=str(d), DXVK_LOG_LEVEL=os.environ.get("DXVK_LOG_LEVEL", "info"))
    return subprocess.Popen(cmd, cwd=GAME_DIR, env=env, stdout=log, stderr=subprocess.STDOUT,
                            start_new_session=True)


def frame_sig():
    """32x18 grey thumbnail of the display, for cheap change detection."""
    r = subprocess.run(["ffmpeg", "-loglevel", "error", "-f", "x11grab", "-video_size", f"{RES[0]}x{RES[1]}",
                        "-i", grab_input(), "-frames:v", "1", "-vf", "scale=32:18,format=gray",
                        "-f", "rawvideo", "-"], capture_output=True)
    return r.stdout if len(r.stdout) == 576 else None


def frame_diff(a, b):
    return sum(abs(x - y) for x, y in zip(a, b)) / 576 if a and b else 0


def wait_change(timeout=60, stop=None, base=None):
    """Block until the screen changes from `base` (default: now) and then holds
    still for one poll. Returns True on change. The client presents nothing
    while it loads a level, so a frozen frame is the normal loading state."""
    base = base or frame_sig()
    end_t, prev = time.time() + timeout, None
    while time.time() < end_t and not (stop and stop.is_set()):
        time.sleep(1.5)
        cur = frame_sig()
        if frame_diff(cur, base) > 12 and prev and frame_diff(cur, prev) < 4:
            return True
        prev = cur
    return False


def wait_hangar(d, stop=None, timeout=240):
    """Hangar loaded AND on screen: the ActivateLevel line comes 20-40s before
    the first hangar frame."""
    rx = re.compile(r"ActivateLevel /Game/Maps/Menu/MN_HGR_")
    end_t = time.time() + timeout
    while not grep_lines(d / "client.log", rx, limit=1):
        if time.time() > end_t or (stop and stop.is_set()):
            return False
        time.sleep(2)
    # On a warm start the hangar is already presented when the line lands
    # (replay-pg2: 13 s after the title) and no change ever follows -- waiting
    # out the full timeout there cost 4 min. 60 s covers the cold 20-40 s.
    if not wait_change(timeout=60, stop=stop):
        return not (stop and stop.is_set())
    return True


def pass_title(disp, d, stop):
    """Wait for the window and press through "press any key". True = aborted."""
    end_t = time.time() + 120
    while time.time() < end_t and not stop.is_set() and not disp.find("^DreadGame"):
        time.sleep(1)
    if stop.wait(25):  # title screen finishes loading before it takes input
        return True
    shoot(d, 0, "title")
    disp.key("space")
    time.sleep(0.5)
    disp.key("Return")
    return False


PIN_FILE = HEADLESS / "match-pin.json"  # mmogbrain DN_MATCH_PIN_FILE (start-services.sh)


def write_pin(spec, d):
    """Pin the next match: 'MODE[:MAP]' or a JSON object (matchmaker/matchpin.go).
    Consumed by the match it pins; removed at exit so a leftover can never
    hijack the operator's next match."""
    import atexit
    if spec.lstrip().startswith("{"):
        pin = json.loads(spec)
    else:
        mode, _, mp = spec.partition(":")
        pin = {"mode": mode, "map": mp}
    PIN_FILE.write_text(json.dumps(pin))
    atexit.register(lambda: PIN_FILE.unlink(missing_ok=True))
    meta = json.loads((d / "meta.json").read_text())
    meta["pin"] = pin
    (d / "meta.json").write_text(json.dumps(meta, indent=1))
    print(f"headless: next match pinned to {pin}")


def cmd_headless(a):
    if sh("curl", "-s", "-m", "2", "-o", "/dev/null", "-w", "%{http_code}",
          "http://127.0.0.1:8083/health").stdout != "200":
        sys.exit("headless: stack is down (dn restart)")
    if headless_pids():
        sys.exit(f"headless: a headless client is already running: {headless_pids()}")
    (HEADLESS / "pfx").exists() or sys.exit("headless: no prefix at run/headless/pfx (see dreadnought-verify skill)")
    ensure_display()
    if not ensure_token(a.account, force=a.remint):
        sys.exit(3)
    # a steps file ends the run itself; the hangar default would cut it short
    if a.until is None:
        a.until = "" if a.steps else HANGAR_RX
    d = begin(a.label, a.expect, a.reject, a.note, mode="headless",
              extra={"account": a.account, "secs": a.secs, "steps": a.steps,
                     "until": a.until, "onboarding": a.onboarding})
    if a.steps:
        shutil.copy(a.steps, d / "steps.txt")
    if a.pin:
        write_pin(a.pin, d)
    sys.path.insert(0, str(Path(__file__).parent))
    import xdrive  # noqa: E402  (same directory)

    log = open(d / "client.log", "wb")
    t0 = time.time()
    proc = launch_client(a, d, log)
    shot_n = [0]
    lock = threading.Lock()
    done = threading.Event()
    steps_done = threading.Event()

    def snap(name):
        with lock:
            shot_n[0] += 1
            shoot(d, shot_n[0], name)

    def periodic():
        while not done.wait(a.shot_every):
            snap(f"t{int(time.time() - t0):03d}")

    def driver():
        try:
            disp = xdrive.Display(DISPLAY)
            if pass_title(disp, d, done):
                return
            if a.steps:
                for line in open(a.steps):
                    line = line.split("#", 1)[0].strip()
                    if not line or done.is_set():
                        continue
                    if line.startswith("shot "):
                        snap(line.split(None, 1)[1].replace(" ", "-"))
                    elif line.startswith("wait-log "):
                        rx = re.compile(line.split(None, 1)[1])
                        while not done.is_set() and not grep_lines(d / "client.log", rx, limit=1):
                            time.sleep(2)
                    elif line == "wait-hangar":
                        wait_hangar(d, stop=done)
                    else:
                        xdrive.run_cmd(disp, line.split())
        except Exception as e:  # never let the driver take the run down
            (d / "driver-error.txt").write_text(repr(e))
        finally:
            steps_done.set()
    threading.Thread(target=driver, daemon=True).start()
    if a.shot_every:
        threading.Thread(target=periodic, daemon=True).start()
    until = re.compile(a.until) if a.until else None
    reason = "timeout"
    try:
        while time.time() - t0 < a.secs:
            if proc.poll() is not None and not headless_pids():
                reason = f"client exited rc={proc.returncode}"
                break
            hc = host_crash(t0)
            if hc:
                reason = f"battle host crashed ({hc})"
                done.wait(5)
                break
            if a.steps and steps_done.is_set():
                reason = "steps finished"
                break
            if until and grep_lines(d / "client.log", until, limit=1):
                reason = f"--until matched; {a.grace}s grace"
                done.wait(a.grace)
                break
            time.sleep(2)
        snap("final")
    finally:
        done.set()
        stop_client(proc)
        log.close()
    print(f"headless: stopped after {int(time.time() - t0)}s ({reason})")
    contact_sheet(d)
    meta = json.loads((d / "meta.json").read_text())
    meta["stop_reason"] = reason
    (d / "meta.json").write_text(json.dumps(meta, indent=1))
    _, verdict = end(d.name, client_log=d / "client.log")
    sys.exit(0 if verdict in ("PASS", "NO-EXPECTATIONS") else 1)


HANGAR_RX = r"ActivateLevel /Game/Maps/Menu/MN_HGR_"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    def exp(p):
        p.add_argument("--expect", action="append", default=[], metavar="RX")
        p.add_argument("--reject", action="append", default=[], metavar="RX")
        p.add_argument("--note", default="")

    b = sub.add_parser("begin")
    b.add_argument("label")
    exp(b)
    e = sub.add_parser("end")
    e.add_argument("id", nargs="?")
    sub.add_parser("list")
    s = sub.add_parser("show")
    s.add_argument("id", nargs="?")
    g = sub.add_parser("digest", help="distinct log lines with counts")
    g.add_argument("file", help="a log path, or a cycle id (its client.log)")
    g.add_argument("--from", dest="start", help="start at the first line matching RX")
    g.add_argument("--last", type=int, default=40)
    sub.add_parser("headless-stop", help="stop the headless X server (sway+Xwayland or Xvfb)")
    h = sub.add_parser("headless")
    h.add_argument("label")
    exp(h)
    h.add_argument("--secs", type=int, default=240, help="hard cap (default 240)")
    # "Entering Hangar" is only the hangar LOADING screen; fleets arrive
    # DN_CONNECT_PUSH_DELAY (45s) later and the ship level activates after that.
    h.add_argument("--until", default=None,
                   help="stop early once the client log matches (default: hangar ship level active, "
                        "or none with --steps: the run ends when the steps do); '' = run to --secs")
    h.add_argument("--grace", type=int, default=25,
                   help="seconds to keep running after --until")
    h.add_argument("--onboarding", action="store_true",
                   help="do NOT pass -noonboarding: test the new-player tutorial flow")
    h.add_argument("--pin", help="play the next match in MODE[:MAP] (or a JSON pin) whatever "
                                 "the steps queue for -- e.g. TE:Gorge; needs DN_MATCH_PIN_FILE")
    h.add_argument("--steps", help="xdrive steps file run after the title screen "
                                   "(key/click/move/sleep/shot NAME/wait-log RX)")
    h.add_argument("--shot-every", type=int, default=30, help="periodic screenshots, 0=off")
    h.add_argument("--account", default="claude-headless-01",
                   help="test account (auto-registered); use a new id for a fresh-player run")
    h.add_argument("--remint", action="store_true", help="force a new token")
    h.add_argument("--raw-wine", action="store_true",
                   help="run Proton's wine binary directly (no Proton env; Wwise needs -nosound then)")
    h.add_argument("--dxvk", action="store_true",
                   help="force DXVK (the operator's client runs wined3d; this crashes at device creation)")
    h.add_argument("--client-arg", action="append", default=[], metavar="ARG",
                   help="extra client argument (repeatable)")
    h.add_argument("--drop-arg", action="append", default=[], metavar="ARG",
                   help="remove a default client argument, e.g. --drop-arg=-nosound")
    h.add_argument("--log-cmds", default="LogYMmogbrain Verbose, LogYLoadout Verbose, LogYFleetManager Log",
                   help="client -LogCmds")
    a = ap.parse_args()
    if a.cmd == "begin":
        begin(a.label, a.expect, a.reject, a.note)
    elif a.cmd == "end":
        end(a.id)
    elif a.cmd == "list":
        cmd_list(a)
    elif a.cmd == "show":
        cmd_show(a)
    elif a.cmd == "digest":
        f = Path(a.file)
        if not f.exists():
            hits = sorted(p for p in CYCLES.iterdir() if p.name.startswith(a.file) or a.file in p.name)
            f = hits[-1] / "client.log" if hits else f
        print("\n".join(digest(f, a.start, a.last)))
    elif a.cmd == "headless":
        cmd_headless(a)
    elif a.cmd == "headless-stop":
        stop_display()


if __name__ == "__main__":
    main()
