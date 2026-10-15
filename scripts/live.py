#!/usr/bin/env python3
"""live -- drive one headless client interactively, one command per call.

The client keeps running between calls; every action is timestamped into the
cycle bundle and `stop` turns the action log into a steps file that
`dn headless LABEL --steps` replays unattended.

    dn live start LABEL [--no-hangar] [--note ..] [headless client flags]
                                  # boots, passes the title, waits until the
                                  # hangar is ON SCREEN, prints a shot path
    dn live click X Y             # act, wait for the screen to settle,
    dn live key Return [..]       #   print shot path + new notable log lines
    dn live move X Y
    dn live hold w 3              # hold a key (flight)
    dn live shot [NAME]           # just look
    dn live wait RX [SECS]        # block until RX appears in the client log
    dn live settle [SECS]         # block until the screen changes and holds
    dn live log [N]               # last N notable client log lines
    dn live status
    dn live stop                  # kill client, analyse bundle, write steps.txt

Coordinates are screen pixels of the 1280x720 shots -- read them straight
off the PNG. Action flags: --no-shot, --wait SECS (fixed delay instead of
settle detection).
"""
import argparse
import json
import math
import re
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import cycle  # noqa: E402
import xdrive  # noqa: E402

STATE = cycle.HEADLESS / "live.json"
# client log lines worth showing after an action (the rest is UE4 noise)
NOTABLE = re.compile(r"ActivateLevel|Matchmak|LogNet:|Travel|Error|Fatal|Critical|"
                     r"Warning: .*(fail|Fail|denied|invalid)|Disconnect|Browse|YA_|"
                     r"Welcome|Join|Spawn|Victory|Defeat|Exit|Popup|Dialog", re.I)
NOISE = re.compile(r"LogYItemIDList|vanity slot|LoadoutxId|FleetId:|GetAchievement|CheckCompletedInitialization|LogUMG|LogWebServicesPlugin: [{\[]|Can't find file|Failed to (load|find object)|LoadProtocolImage|LogWebServicesPlugin: (Headers|X-Request|User-Agent|Authorization|Process|\*)")


def load():
    if not STATE.exists():
        sys.exit("live: no session (dn live start LABEL)")
    st = json.loads(STATE.read_text())
    st["dir"] = Path(st["dir"])
    return st


def save(st):
    STATE.write_text(json.dumps({**st, "dir": str(st["dir"])}, indent=1))


def alive():
    return bool(cycle.headless_pids())


def record(st, *lines):
    # Only time the game needed goes in (settle/wait durations), never the
    # wall-clock gap between calls -- that is the driver thinking, and the
    # first recording replayed it as `sleep 107`.
    with open(st["dir"] / "actions.txt", "a") as f:
        f.writelines(l + "\n" for l in lines)


def waited(t0):
    dur = time.time() - t0
    return [f"sleep {math.ceil(dur)}"] if dur >= 1 else []


def snap(st, name):
    st["shot_n"] = st.get("shot_n", 0) + 1
    return cycle.shoot(st["dir"], st["shot_n"], re.sub(r"[^\w.-]+", "-", name)[:40])


def new_log(st, limit=12):
    """Notable client log lines since the last call, deduplicated."""
    p = st["dir"] / "client.log"
    with open(p, "rb") as f:
        f.seek(st.get("log_off", 0))
        data = f.read()
        st["log_off"] = f.tell()
    seen, out = set(), []
    for raw in data.decode(errors="replace").splitlines():
        line = re.sub(r"^\[[^]]*\]\[[ \d]*\]", "", raw).strip()
        if not line or NOISE.search(line) or not NOTABLE.search(line):
            continue
        key = re.sub(r"\d+", "N", line)[:120]
        if key not in seen:
            seen.add(key)
            out.append(line[:200])
    return out[-limit:]


def report(st, shot=None):
    if shot:
        print(f"shot: {shot}")
    for l in new_log(st):
        print(f"  log: {l}")
    hc = cycle.host_crash(st["t0"])
    if hc and not st.get("host_crash"):
        st["host_crash"] = hc
        print(f"live: BATTLE HOST CRASHED -- {hc}")
    if not alive():
        print("live: CLIENT IS GONE -- `dn live stop` to close the bundle")


def cmd_start(a):
    if STATE.exists() and alive():
        sys.exit(f"live: session already running ({load()['cycle']}); dn live stop first")
    if cycle.headless_pids():
        sys.exit(f"live: a headless client is already running: {cycle.headless_pids()}")
    if cycle.sh("curl", "-s", "-m", "2", "-o", "/dev/null", "-w", "%{http_code}",
                "http://127.0.0.1:8083/health").stdout != "200":
        sys.exit("live: stack is down (dn restart)")
    cycle.ensure_display()
    if not cycle.ensure_token(a.account, force=a.remint):
        sys.exit(3)
    d = cycle.begin(a.label, a.expect, a.reject, a.note, mode="live",
                    extra={"account": a.account, "onboarding": a.onboarding})
    log = open(d / "client.log", "wb")
    proc = cycle.launch_client(a, d, log)
    st = {"cycle": d.name, "dir": d, "t0": time.time(), "pid": proc.pid, "shot_n": 0}
    save(st)
    print(f"live: {d.name} client pid {proc.pid}")
    disp = xdrive.Display(cycle.DISPLAY)
    if cycle.pass_title(disp, d, threading.Event()):
        sys.exit("live: no client window")
    if a.no_hangar:
        save(st)
        report(st, snap(st, "start"))
        return
    ok = cycle.wait_hangar(d)
    record(st, "wait-hangar", "shot hangar")
    print(f"live: hangar {'on screen' if ok else 'NOT confirmed (timeout)'} after {time.time() - st['t0']:.0f}s")
    report(st, snap(st, "hangar"))
    save(st)


def act(a, st, argv, label):
    disp = xdrive.Display(cycle.DISPLAY)
    base = cycle.frame_sig()
    xdrive.run_cmd(disp, argv)
    t0 = time.time()
    if a.no_shot:
        record(st, " ".join(argv))
        report(st)
        return
    if a.wait is not None:
        time.sleep(a.wait)
    else:
        changed = cycle.wait_change(timeout=a.settle, base=base)
        if not changed:
            print(f"live: screen unchanged after {a.settle}s")
    record(st, " ".join(argv), *waited(t0), f"shot {label}")
    report(st, snap(st, label))


def cmd_stop(_a):
    st = load()
    cycle.stop_client(None)
    cycle.contact_sheet(st["dir"])
    acts = st["dir"] / "actions.txt"
    if acts.exists():
        (st["dir"] / "steps.txt").write_text(
            "# recorded by dn live; replay: dn headless LABEL --steps <this file>\n" + acts.read_text())
    meta = json.loads((st["dir"] / "meta.json").read_text())
    meta["stop_reason"] = "live stop"
    (st["dir"] / "meta.json").write_text(json.dumps(meta, indent=1))
    STATE.unlink()
    cycle.end(st["cycle"], client_log=st["dir"] / "client.log")
    if acts.exists():
        print(f"steps: {st['dir'] / 'steps.txt'}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("start")
    s.add_argument("label")
    s.add_argument("--no-hangar", action="store_true", help="return after the title, don't wait for the hangar")
    s.add_argument("--expect", action="append", default=[], metavar="RX")
    s.add_argument("--reject", action="append", default=[], metavar="RX")
    s.add_argument("--note", default="")
    s.add_argument("--account", default="claude-headless-01")
    s.add_argument("--remint", action="store_true")
    s.add_argument("--onboarding", action="store_true")
    s.add_argument("--log-cmds", default="LogYMmogbrain Verbose, LogYLoadout Verbose, LogYFleetManager Log")
    s.add_argument("--client-arg", action="append", default=[])
    s.add_argument("--drop-arg", action="append", default=[])
    s.add_argument("--raw-wine", action="store_true")
    s.add_argument("--dxvk", action="store_true")
    for name, nargs in (("click", "+"), ("key", "+"), ("move", "+"), ("hold", "+")):
        p = sub.add_parser(name)
        p.add_argument("args", nargs=nargs)
        p.add_argument("--no-shot", action="store_true")
        p.add_argument("--wait", type=float, help="fixed delay before the shot instead of settle detection")
        p.add_argument("--settle", type=float, default=8, help="max seconds to wait for the screen to settle")
    sh_ = sub.add_parser("shot")
    sh_.add_argument("name", nargs="?", default="look")
    w = sub.add_parser("wait")
    w.add_argument("rx")
    w.add_argument("secs", nargs="?", type=float, default=120)
    se = sub.add_parser("settle")
    se.add_argument("secs", nargs="?", type=float, default=60)
    lg = sub.add_parser("log")
    lg.add_argument("n", nargs="?", type=int, default=20)
    sub.add_parser("status")
    sub.add_parser("stop")
    a = ap.parse_args()

    if a.cmd == "start":
        return cmd_start(a)
    if a.cmd == "stop":
        return cmd_stop(a)
    st = load()
    try:
        if a.cmd in ("click", "key", "move", "hold"):
            act(a, st, [a.cmd] + a.args, f"{a.cmd}-{'-'.join(a.args)}")
        elif a.cmd == "shot":
            record(st, f"shot {a.name}")
            report(st, snap(st, a.name))
        elif a.cmd == "wait":
            rx, end_t = re.compile(a.rx), time.time() + a.secs
            hit = []
            while time.time() < end_t and alive():
                hit = cycle.grep_lines(st["dir"] / "client.log", rx, limit=1)
                if hit:
                    break
                time.sleep(2)
            # a timed-out wait replays as the time it cost, not as a wait
            # that would block the replay until its stop condition
            record(st, f"wait-log {a.rx}" if hit else f"sleep {a.secs:.0f}")
            print(f"wait: {'HIT ' + hit[0].strip()[:200] if hit else 'TIMEOUT'}")
            report(st)
        elif a.cmd == "settle":
            t0 = time.time()
            ok = cycle.wait_change(timeout=a.secs)
            record(st, *waited(t0), "shot settled")
            print(f"settle: {'changed' if ok else 'no change'}")
            report(st, snap(st, "settled"))
        elif a.cmd == "log":
            st["log_off"] = max(0, (st["dir"] / "client.log").stat().st_size - 400_000)
            for l in new_log(st, a.n):
                print(l)
        elif a.cmd == "status":
            print(f"{st['cycle']}  up {time.time() - st['t0']:.0f}s  client {'alive' if alive() else 'GONE'}  "
                  f"shots {st.get('shot_n', 0)}")
    finally:
        save(st)


if __name__ == "__main__":
    main()
