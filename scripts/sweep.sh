#!/bin/bash
# sweep.sh LIST [--secs N] [--steps FILE] -- pinned headless matches back to back, one line each.
#
# LIST: one pin per line, `LABEL PIN` (PIN = MODE[:MAP] or JSON, see
# `dn headless --pin`); # comments. Every match replays pg-battle.txt (hangar
# -> PLAY -> pick ship -> fly); the pin swaps the mode/map server-side, so the
# client flow is the same for every mode. Results: run/cycles/sweep-<ts>.txt
# (verdict, milestones reached, battle-host crash), full bundles per match;
# each run's dn headless output in run/cycles/sweep-<ts>.log (a driver that
# dies mid-run otherwise leaves nothing behind).
# Waits for :443 + health first, so it can be queued before the stack is up.
set -u
cd "$(dirname "$0")/.."
list=$1; shift; secs=420; steps=scripts/headless-steps/pg-battle.txt
# --steps: PvE/Havoc waves wait for ReadyUp after spawn (havoc-ready.txt)
while [[ $# -ge 2 ]]; do
  case $1 in --secs) secs=$2 ;; --steps) steps=$2 ;; *) echo "sweep: unknown option $1" >&2; exit 2 ;; esac
  shift 2
done
out=run/cycles/sweep-$(date +%Y%m%d-%H%M%S).txt
until ss -ltn | grep -q ':443 ' && [[ $(curl -s -m2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8083/health) == 200 ]]; do sleep 10; done
# a host left over from an earlier sweep (or a chained one still in its own
# end wait) takes the first login back into its match. Same wait as between runs.
wait_no_host() {
  for _ in $(seq 36); do
    [[ $(curl -s -m2 http://127.0.0.1:8085/instances | grep -o '"count":[0-9]*') == '"count":0' ]] && return
    sleep 5
  done
}
wait_no_host
echo "# sweep $list $(date +%T)" > "$out"
grep -vE '^\s*(#|$)' "$list" | while read -r label pin; do
  t0=$(date +%Y%m%d-%H%M%S)
  ./scripts/dn headless "sw-$label" --steps "$steps" --pin "$pin" --secs "$secs" >> "${out%.txt}.log" 2>&1 < /dev/null
  # only a bundle this run made: an older one with the same label would hand
  # its verdict to a run that never started
  d=$(ls -d run/cycles/*-sw-"$label" 2>/dev/null | sort | tail -1)
  if [[ -z $d || ${d#run/cycles/} < $t0 || ! -f $d/summary.md ]]; then
    printf '%-14s %-60s %s\n' "$label" "$pin" "NO BUNDLE (see ${out%.txt}.log)" >> "$out"
  else
  v=$(grep -m1 '^VERDICT' "$d/summary.md"); m=$(grep -m1 '^milestones' "$d/summary.md" | grep -oE '[A-Za-z-]+@' | tr -d @ | tr '\n' ' ')
  # a battle-*.log line under "crash markers" = the host died, not the client
  hc=$(grep -qE '^- battle-[^:]*: .*(Fatal error|ACCESS_VIOLATION|Critical error)' "$d/summary.md" && echo HOST-CRASH)
  printf '%-14s %-60s %s | %s | %s\n' "$label" "$pin" "${v#VERDICT: }" "$m" "${hc:-no host crash}" >> "$out"
  fi
  # A killed client is only dropped at the engine's ~60 s net timeout (+10 s
  # reap); log in before that and mmogbrain sends the client back into the
  # old match with no UI or pawn -- every run after the first was that
  #. Wait until no host is left, up to 3 min.
  wait_no_host
done
echo "# done $(date +%T)" >> "$out"
