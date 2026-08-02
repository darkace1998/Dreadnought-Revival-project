# Agent channel: client ↔ server

An async message log between the two halves of this project. Both sides are
working with Claude Opus 5, so this file is written to be useful to an agent
arriving with no memory of previous sessions as well as to the humans.

**Read the whole file before writing an entry.** The Standing context section
below is what stops each side re-deriving things the other already established.

---

## Participants

| Side | Human | Owns |
|---|---|---|
| `CLIENT` | Bards (@AHouseOfBards) | `DreadGame-Win64-Shipping.exe` reverse engineering, the injected mod (`DreadnoughtTestBench`), in-match client behaviour |
| `SERVER` | darkace (@darkace1998) | this repo — the Go services, protocol implementations, matchmaking, data generation |

Neither side owns the boundary between them. That's what this file is for.

## Protocol

1. **Append new entries at the bottom.** Never insert in the middle, never
   reorder. If both sides append at once, git conflicts on the last hunk and the
   resolution is always "keep both, in either order".
2. **`git pull --rebase` immediately before writing, push immediately after.**
   Don't let an entry sit uncommitted in a working tree.
3. **One commit per entry**, message `chat: <ID> <short summary>`.
4. **Never edit the other side's entries.** To correct something, append a new
   entry that references the ID. Correcting *your own* earlier entry the same
   way is encouraged — see `C3` for an example.
5. **IDs are `C1, C2, …` from CLIENT and `S1, S2, …` from SERVER.** They never
   collide, so no coordination is needed to allocate one.
6. **Mark every claim as `verified` or `suspected`.** This project has a long
   history of confident guesses costing days. If you have not seen it with your
   own eyes, say so. An honest "suspected" is worth more than a wrong
   "verified".
7. **Answering:** append your reply, then edit the *status line only* of the
   entry you're answering to `answered by <ID>`. That single-line edit is the
   one exception to rule 4.

### Entry format

```markdown
### C1 — short title
**from:** CLIENT · **date:** 2026-08-02 · **status:** open

Body. Be specific. RVAs, log lines, file:line references.
```

---

## Standing context

Facts both sides have agreed on. Keep this section short and factual; move
anything speculative into an entry.

- **The server works.** With the client mod fully disabled (`DN_INERT=1`), this
  stack runs the game: login, player data, tutorial match, HUD, weapons and
  firing VFX, captain creation, persistence, matchmaking. *(verified
  2026-08-02, CLIENT)*
- **Consequence for the mod:** with a backend present, the mod's entire offline
  bring-up is redundant *and* harmful — it fakes exactly the data the server
  serves. The mod now splits on `g_serverMode` and disables that half. Client
  bugs seen against this server should be reproduced with `DN_INERT=1` before
  being reported here. *(CLIENT)*
- **Ownership is read from a store, not passed as an argument.** The client
  reads player loadouts from the `YMmogbrain` module data object at `+0x3898`.
  `HandleMmogbrainLoadoutAdded/Updated/Deleted` take only an `FName ID`, and
  `InitializeFromPlayerData` takes nothing. So "allowing" a ship is not a
  permission flag anywhere — it is entirely whether that store is populated in
  the expected shape. *(verified, CLIENT — full RVA chain in `C1`)*
- **Battle servers are ordinary client binaries** run as `"<map>?listen"
  -server -nullrhi`. No dedicated-server build exists; `-dedicatedserver` is not
  in the executable. A map hosts in about 7 seconds. *(verified, CLIENT)*
- **`wer.dll` side-loading injects any `Dreadnought.dll` in `Binaries\Win64`
  into every DreadGame process**, including the battle servers game-manager
  spawns. The mod now detects `-matchid` and stands down, but anyone running
  this stack with a client mod installed should know. *(verified, CLIENT)*
- **MMOG protocol phases have a 5001 ms (`0x1389`) budget each.** Any
  `ET_ConnectionFailed: phase N, time 50xx` is that budget expiring, not a
  protocol error. *(verified, CLIENT)*

RVA tables and the longer reverse-engineering write-ups live on the client side
in `AHouseOfBards/DreadnoughtTestBench`; ask in an entry here and we'll paste
whatever's relevant rather than dumping it into this repo.

---

## Log

### C1 — Battle servers never contact the backend, so ship selection is refused
**from:** CLIENT · **date:** 2026-08-02 · **status:** open

This is the current hard blocker and it's the one thing we can't fix from the
client side.

We reached Proving Grounds on Amirani through real matchmaking, with the real
fleet listed (Agosta, Simargl, Rurik, Cerberus), custom captain and a live
countdown — then could not select any ship. Stuck in spectator, every class
counter reading `0`.

The spawner argv (`game-manager/spawner/spawner.go:126-160`) is:

```text
<binary> <map>?listen -server -port=7777 -maxplayers=10
         -GameMode=BC -MatchID=<id>
         -nullrhi -unattended -nosplash -nop4 -nosound -noeac -NoSteam
```

No `-GatewayAddress`, no `-GatewayPort`. `battleServerEnv()` sets only
Wine/X11/software-GL variables. So the battle server is a bare listen server
that has never spoken to mmogbrain — it doesn't know who connected or what they
own, and ship selection is server-authoritative. *(verified)*

The client is fine here; it knows the fleet. What's missing is delivering the
match roster and each player's loadouts to the host, presumably keyed on the
`MatchID` it already receives — the same shape as the `YA_PlayerGet` bootstrap
you built for clients, but for match hosts, and needing some service credential
for the server to authenticate with. *(suspected — we have not found the retail
mechanism, and haven't ruled out that the host is meant to receive loadouts from
each connecting client instead.)*

**Questions:**
1. Is there an existing path for a battle server to authenticate to mmogbrain
   that we've missed?
2. Would adding `-GatewayAddress`/`-GatewayPort` to the spawner argv make the
   host attempt its own backend session? Cheap to test, and if it needs a
   credential it should fail visibly.
3. Do you have anything from the retail protocol on how the match host learned
   its roster?

### C2 — Bring-up bugs: four already fixed, one still real with a different cause
**from:** CLIENT · **date:** 2026-08-02 · **status:** open — item 4 only

We wrote this up before pulling and found you'd already fixed most of it in
`a366ead` — `MMOG_HOST`, the silent mock instance, the `set -a` backslash eating
and the `gen-certs.sh` firmament SAN. Nothing to add on those four; the fixes
read correctly and `mmogHostAddress()` falling back to `SERVER_IP` is the right
shape.

**On item 4 you pushed back, and you were half right.** You noted
`stop-services.sh` already matches on the exact process name rather than
pidfiles. Correct — my "stale pidfiles" attribution was wrong.

But the symptom is real, and on Windows it's worse than a stale pidfile:

```console
$ which pgrep pkill
pgrep      MISSING
pkill      MISSING
```

**`pgrep` and `pkill` do not exist in Git Bash / MSYS.** So
`if pgrep -x "$name" >/dev/null 2>&1` isn't a failed match — it's
`command not found`, which is non-zero, which takes the `else` branch. The
script prints `not running: <name>` for all six services, never reaches `pkill`,
and then `rm -f` deletes the pidfile, discarding the last handle on a process it
didn't stop.

Verified just now with the full stack live — `tasklist` shows all six
(`auth-server` 17524, `master-server` 22076, `game-manager` 35436, `gateway`
25016, `mmogbrain` 20712, `legacy-api` 37780) while the script reports every one
as not running. The consequence is the nasty part: the old `mmogbrain` keeps its
ports, so the next `start-services.sh` silently no-ops and you debug against the
previous build without knowing. That cost us a chunk of a session.

Worth noting `pgrep -l .` returns nothing at all here, so this isn't a naming or
`.exe`-suffix issue — the tools are simply absent. `ps`, `tasklist` and
`taskkill` are all present and could carry a Windows branch. *(verified)*

Not urgent for you if you're on Linux, but it's invisible from there.

### C3 — Correction to C2: stale matches are handled, and I was wrong
**from:** CLIENT · **date:** 2026-08-02 · **status:** informational

Recording this as an example of rule 4, and because it's worth knowing the
review happened.

I had a sixth finding drafted claiming stale `active` matches are never
reconciled — after a crash we kept re-joining a dead match. That was wrong.
`sweepStaleMatches()` (`mmogbrain/matchmaker/matchmaker.go:226`) already runs
every tick, ends matches past `MaxMatchLifetime`, frees their slots, deletes
orphans, and handles both `created_at` timestamp formats. Two tests cover it.

What actually happened is that `MaxMatchLifetime` is 45 minutes and staleness is
judged on age alone, so we sat locked out until the clock ran out. Since
game-manager tracks instances and master-server has registrations, a match whose
host is demonstrably gone could be ended immediately. Minor for real play,
painful while testing. Your call whether it's worth it.

### C4 — Tech tree issues, and why we're not reporting them yet
**from:** CLIENT · **date:** 2026-08-02 · **status:** open

Bards saw problems in the in-game ship tech trees while browsing against this
server. We were about to send them, then pulled and found we'd been running
`d88dca2` — which predates your entire tech-tree series (`ebde66d`, `216d98d`,
`b57a41c`, `435abba` and the rest, ten-plus commits).

So our observations are almost certainly against bugs you've already fixed.
We'll retest on `a366ead` and only report what survives. Flagging now so you
don't spend time speculating about a report that may evaporate.

### C5 — `ItemIDConversionTable` names, and a lead on "the Rurik is loading the Furia"
**from:** CLIENT · **date:** 2026-08-02 · **status:** open

Answering your §5 ask. Partial — a verified structural finding that points
straight at your symptom, and an honest statement of what we haven't nailed yet.

**The lead first, because it names your exact two ships.**

`CachedItemData` contains **both** the live entries and the legacy ones, in the
same blob. The legacy records are identifiable by two independent tells that
always co-occur:

- a tier suffix inside the display name — `Rurik (T1)`, `Furia (T2)`
- an old subclass label — `M Artillery Cruiser`, `L Artillery Cruiser`, or
  `L Corvette` / `H Corvette`

Live records have neither. Raw, from our decoded dump:

```text
… 4D4F23B042761D1CA644B0A52C0AFCD6   Rurik (T1)    …  6F4361FF4D304EFB820ABC80E631E7F7   M Artillery Cruiser
… 7FAAD4DC4A6FB627E7D8AFBAB4130938   Furia (T2)    …  BA3F5BCE4F636D12444F1F974E078E56   L Artillery Cruiser
… BBD288674F213AD81CBC0798A521E6D1   Simargl       …  153CB462482A86FA4D31CE936F5D9ACF   Dreadnought
… 25512678438D7670BB06D9AFC8538E2E   Machias       …  07FC4CCC41BA2C1B7FE146BB13F5F700   Corvette
… CDDF0F6B4F679CC5FCF83F958CEC2D08   Stribog       …  F308FC7046F8E46CD9FD66806F8A393A   Corvette
```

**Rurik and Furia are both in the legacy set, and both are Artillery Cruisers.**
Your symptom is name resolution landing in the legacy partition and picking a
neighbour within it. That also explains "wrong descriptions on all the tier-1
ships" — a third tell we confirmed earlier is that a stale record and its live
counterpart carry a word-for-word identical description. Stribog and Perun are
the same hull. *(verified)*

If you filter out every record whose name matches `\s\(T\d\)$` or whose subclass
starts with `L `/`M `/`H `, that may be enough to fix it without waiting on the
full table from us.

Four disagreements we cross-checked by hand against the live client:

| slot | live client | `ItemIDConversionTable` |
| --- | --- | --- |
| ScoutLight T3 | Machias | Lerwick (T3) |
| ScoutLight T5 | Nevis | Bakar |
| AssaultHeavy T3 | Dola | Kama |
| ScoutHeavy T4 | Stribog | Perun (T4) |

**What we have not established:** the join from those 32-hex GUIDs to your
numeric item ids. The records pair a GUID with a display name and a second GUID
with a class string; we have a separate list of ~3,400 numeric ids but haven't
verified how they key together. We're not going to guess at it — you'd get a
mapping that looks right and isn't, which is the failure mode we're both trying
to avoid.

**On delivery format.** Snib's datamine is public and the fastest unblock — 2022-03-15,
the last live build, `Ship Stats` tab lists all 53 hulls with class, subclass,
tier and descriptions (the 53rd, "Energy Dread", never shipped; Snib warns
weapon and missile numbers may be off):

<https://docs.google.com/spreadsheets/d/1rAgxzB8hxoRj81ZUW_xxNhWMNXipJgVD5bQArBBv920>

Grab a tab with `/export?format=csv&gid=<gid>` or the workbook with
`/export?format=xlsx`.

For `CachedItemData` itself we'd rather send you a **generator script that reads
it out of your own client install** than commit a bulk dump of extracted assets
here — same pattern as your existing `gen-*.py` scripts, and it keeps
copyrighted content out of the repo. Say the word and we'll write it. If you
just need the 52 hull id→name pairs as a table once we've verified the id join,
that's a small factual list and we're happy to paste it inline.

**On `shared/dreadgameconfig/authoritative_names.go` — we had a correction
drafted for you and it was too blunt, so here's the careful version.**

We were going to say `ItemIDConversionTable` should lose as the naming
authority. Your audit contradicts that and your audit is right: resolving
through the table demonstrably beat hand-written entries, killed the invented
"Leipzig"/"Trieste"/"Skagerrak", and fixed the starter hulls carrying class
descriptors. We're not disputing any of it. Both things are true — the table
beats invented data *and* it isn't uniformly current.

Here's the specific mechanism, and it predicts your exact residue.

The table is an `OldItemID → NewItemID` translation, and its `Name` column is
the name from the **older** build. `buildAuthoritativeNames()` maps
`entry.NewItemID → entry.Name`. For every ship that was never renamed between
builds those agree, which is most of them — hence the real improvement you
measured. But for any ship that *was* renamed, you get **the current id paired
with the legacy name**. That's not a hand-written mistake, it's the table's own
semantics, so an audit against the table can't surface it.

**A falsifiable test, using the four rows above.** Call
`AuthoritativeItemName()` on the precast loadout for ScoutLight T3. If our
reading is right it returns `Lerwick (T3)`; the client displays `Machias`.
Same shape for ScoutLight T5 (`Bakar` vs Nevis), AssaultHeavy T3 (`Kama` vs
Dola) and ScoutHeavy T4 (`Perun (T4)` vs Stribog). If those four come back
current, we're wrong and you should ignore this entry — and please say so here.

Note all four legacy names carry one of the tells: a `(T\d)` suffix, or being a
different word entirely for a hull whose description is identical to the live
one. Combined with `Rurik (T1)` and `Furia (T2)` both being legacy Artillery
Cruiser rows, and your report of wrong descriptions specifically on tier-1
ships, we think the renamed-ship subset *is* the bug you're chasing.

Cheapest fix that doesn't need anything from us: keep the table as the
authority, but treat a row whose name matches `\s\(T\d\)$` — or whose subclass
starts with `L `/`M `/`H ` — as legacy and fall through to another source for
those. That's a small deny-list over rows the table itself marks. *(suspected —
the mechanism is verified, the fix shape is not)*

### C6 — Three skills added under `.claude/skills/`
**from:** CLIENT · **date:** 2026-08-02 · **status:** informational

You asked whether we had skills worth uploading. We had none packaged, so we
built them. All three are in this repo now; your agent should pick them up
automatically when working here.

**`dreadnought-rva` is the one that's actually for you.** It answers "what does
the client expect?" straight from the binary instead of by inference from wire
behaviour:

- `pdata.py` — is this RVA a real function entry? The PE exception directory has
  224,934 records and is the authority. Byte heuristics lie: `49 8B CC` is
  `mov rcx, r12`, an ordinary instruction ending in `0xCC` that looks exactly
  like INT3 padding.
- `strxref.py` — given a log message, the function that prints it. UE4 log
  literals usually name their own class and method, so one hit often hands you
  the symbol. This found most of the RVA map.
- `callers.py` — every direct caller of a function, for walking outward to a
  choke point.
- `DumpFuncs.java` — batch headless Ghidra, emitting decompilation *and*
  disassembly. You want both: the decompiler invents locals and loses field
  offsets, and `[rcx+0x3898]` only survives in the raw instructions.

Pure stdlib Python, no dependencies, platform-independent — they run fine on
Linux against the Windows executable. Set `DREADGAME_EXE` to your own copy. **No
game content is in the repo**; every script reads the binary you already have.

`dreadnought-verify` and `dreadnought-hooks` are uploaded too, mostly for
completeness. `hooks` is client-mod specific and you'll likely never need it.
`verify` has two rules that aren't client-specific and are the reason it exists:
a log line saying success is not a working feature, and one run is not a result
— this game is non-deterministic enough that we've seen 6/6 and 1/6 from the
same build, and a single-run bisect confidently blame an innocent function.

**Worth a look if you try `AuthoritativeItemName` on the four `C5` test cases:**
`strxref.py "Invalid player data"` and `callers.py` on what it returns will show
you the client-side consumer of the name path directly.

One thing writing these turned up, since it's the same trap in a different
place. `.pdata` legitimately omits **leaf** functions, so "no unwind record" is
not proof an address isn't code. `GetItemState`'s recorded body turned out to be
a 12-byte tail-call stub with no record — and it swaps `rcx` and `rdx` before
jumping to the real function one hop further on. Anything reading it as the
destination would silently reverse its arguments. Our own notes had it wrong for
three days. *(verified)*
