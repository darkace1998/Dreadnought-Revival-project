# Contributing

Thanks for helping keep Dreadnought playable.

This is an unusual project: we are writing a server for a client we cannot change and did not write, whose protocol is undocumented. Most of the work is not "write a feature" — it is "find out what the client actually expects, then send exactly that". The rules below are what experience has taught us, and most of them exist because breaking them cost somebody a week.

Setup, build and run instructions live in [README.md](README.md). This file is about how to work on the code.

---

## The one rule: never invent data

**Everything the server sends must be traceable to the client's own data or to the client binary.** If you cannot point at where a value came from, do not send it.

This is the single most expensive mistake in this project's history. Plausible-looking invented values have repeatedly cost days:

- map names that no map file has ever matched (`Charon`, `Medusa`, `Procyon`, …), which made matchmaking look broken for reasons unrelated to matchmaking;
- "legacy" item ids that were never legacy anything;
- ship names invented to fill a gap in a table (`Leipzig`, `Trieste`), which then propagated into other tables as if they were real;
- a `-dedicatedserver` switch that does not occur anywhere in the executable.

Invented data is worse than missing data, because it *looks* right. A missing field produces a clear failure; a fabricated one produces a plausible wrong result that gets built on.

If you genuinely cannot determine a value, say so — in a comment, in the code, at the point where the guess lives. Write `// GUESS:` and explain what evidence is missing. A flagged guess is a lead; an unflagged one is a trap.

## Let the client tell you

When something does not work, the productive order is:

1. **Read the client's log.** It is far more specific than it looks. `Invalid tech tree item type: 33489262` names the exact rejected value.
2. **Find the string in the binary, then the function that emits it.** The Ghidra project has the whole client. A log line has one or two xrefs; that is your entry point.
3. **Read the parser, not the symptom.** Work out what the function *reads*, in what order, and what makes it bail out.
4. **Only then** form a hypothesis about the payload.

Symptom-first debugging has a bad record here. The tech tree screen was empty for months; the cause was one field (`ClassId`) failing one gate (`(id >> 24) & 0xff` had to be 1 or 3). No amount of reasoning about the *screen* would have found that — reading the store gate did, in an afternoon.

**A missing log line is not a missing execution.** MSVC splits functions: cold and unlikely paths get moved far away and receive their own `.pdata` record chained back to the primary. A literal that lives in one of those chunks sits *past* the early-return guards, so the function can run to completion without ever reaching it. Check which chunk a literal is in — `python .claude/skills/dreadnought-rva/scripts/pdata.py <rva>` resolves the chain — before concluding anything from its absence. This has now cost three separate diagnoses across both halves of the project: a tune handler reported as "never called", `UpdateWeaponSettings` read as a state when it was a transient, and `S12`'s retracted claim about NPC sets. The inverse holds too: a line that *does* print proves only that its own chunk ran.

**A default that cannot be told apart from a real value is not a default, it is a hidden failure.** Its companion: the cold-chunk rule is about a line that never printed, this is about a line that printed something plausible. Five cases, on both halves of the project — `FindCachedDataEntry`'s catch-all made every module classify as `SHIP_CLASS`; a client-side `shipClass = 6` made a missed id look like a Dreadnought Medium on all 52 hangar transitions; `C29`'s untimestamped log let an 88-second gap read as fast execution; `appendMmogShipProgression` hardcoded `"tier": "1"`, which is byte-identical to a real Tier 1 hull; and `purchasePriceForItem`'s flat `1000` charged that for an Athos the store advertised at 400,000. Every one was invisible *because* the fallback was plausible. The shipped client does it too, so expect it when reading its logs: `CallOnPlayerSpawned`'s guard (`0x572520`, cold chunk `0x5725CA`) prints `Pawn does not belong to a world` for **both** a null pawn and a world-less one, and the wording points at the branch that did not fire.

So when a lookup fails and you must still return something: log that it fell back, and — the half that actually holds — assert in a test that nothing you ship reaches it. A warning in a log nobody reads is not a tripwire. `mmogbrain/silent_fallback_test.go` is the pattern: it fails the build if any owned hull has no derivable tier or any catalog SKU no derivable price. Prefer a `…Checked() (T, bool)` variant beside the wire-path function, so callers that *can* act on "unknown" are not forced to guess.

**A string with no xrefs usually means you found the wrong copy of it.** This binary carries several identical copies of many log strings in `.rdata`, and `img.find()` returns the first, which is often not the one the code references. Walk back to each copy's own null terminator and xref *every* copy before concluding a literal is dead. Caught on 2026-08-08: `IsItemVeteranStatus | Item not owned by player item id` **prints in the live client log** and still reported zero references — it has four copies and the referenced one is the second. That false negative had already been published twice (`S33.4`, `S36.5`) telling the other side to stop looking at real, live functions. If you have watched a line print, the reference count is wrong, not the binary.

Corollary: **count things.** When the client logged twelve errors, the useful question was not "why are these twelve items broken" but "which twelve values are these?" They turned out to be the twelve `Prereq` ids, not twelve items — and that reframing solved it immediately.

## Server-side only

The project's purpose is to run the **unmodified game client**. Patching the executable defeats the point and is not accepted.

The only thing we ask a player to install is `dn-launcher/`, which replaces the original launcher so we can sign in, trust our certificate, and redirect the hostnames.

You will find community guides that solve problems by injecting a DLL and writing into the game's memory. They are useful reading — their reverse-engineering is often excellent — but their *approach* is out of scope here. If a guide says "the arrays are empty because the backend never sent the response", the fix belongs in the response.

**The one exception, and where its boundary is.** `battle-server-mod/` holds an optional DLL loaded by the **battle server** — the same executable run headless by an operator, never by a player. It exists because one thing genuinely cannot be done from the backend: the host's loadout manager is filled only from a login the host never performs, and without it no player can spawn (`docs/battle-server-data-path.md` §1). The test for anything in that directory is whether it *fills a hole the engine left* or *lies to a gate*: registering the precast loadouts the engine would have installed itself qualifies; writing a value into `PlayerController+0x948` to fake an orbit state does not. Read `battle-server-mod/README.md` before adding to it. The rule above is unchanged for everything a player runs.

## Invariants worth knowing before you touch the protocol

These are established, verified, and easy to break by accident.

**The category law.** The top byte of an item id *is* its `ItemIDTable` CategoryID: `(id >> 24) & 0xff`. Verified across every id in every category — 3437 agree, 0 disagree. Key values: 1 = `YShipLoadoutPrecast`, 3 = `YShipLoadoutHero`, 4 = `YAbility`, 5 = `YWeapon`, 6 = `YPerk`, 10 = `YPawn`. Several client gates admit an id only by this byte.

**Container length excludes its own length field.** A container is `<namelen><name><tag><u32 length><contents><0x00 0x0e><u32 back-ref>`, and `length` covers contents plus the 6-byte terminator, measured from *after* the length field. Getting this wrong by +4 makes a container swallow the first bytes of its next sibling, so only the last element of any array survives — a bug that looks like anything except a length bug.

**Arrays lose their children's names.** The parser types tag `0x0d` as an array and `0x0c` as an object; only the object stores child names. A name-less container is then *indexable by any field name*: a lookup resolves to `_wtoi(name)`, which is `0` for anything non-numeric, and returns `child[0]`. Send a list the client also does named lookups on as an object with children named `"0"`, `"1"`, … (`protocol.AppendIndexedStringListField`).

**Case sensitivity differs by protocol.** The binary mmog parser compares field names case-insensitively (it lowercases both sides). The JSON catalog lookups do **not** — `Name` and `name` are two different fields carrying two different values, and both are required.

**`<DNT>[[NotFound]]` means a missing field**, not a failed localization lookup. The JSON reader returns its caller's fallback when the node is absent, and returns a present string verbatim.

Three different enums are called "ship class". Do not mix them: base ship class (0–4), `EYShipClass` (1–15, class × size), and the tech tree's `ClassId` (an item id). See `docs/client-data-reference.md`.

## Code style

Match the surrounding code. Beyond that:

**Comments explain *why*, with evidence.** This codebase's comments are unusually dense and that is deliberate — they carry reverse-engineering findings that exist nowhere else. Cite the function address when a behaviour comes from the binary:

```go
// Visible gates the whole item: a falsy value makes the loader jump past the
// rest of the entry, so the item is never stored. The truthiness test is:
//   type < 1            -> skip
//   type < 4 (bool/num) -> truthy = numeric slot != 0
//   type == 4 (string)  -> truthy = (length - 1) > 0
// This deliberately uses the STRING branch with a two-character value.
```

**Correct the record when you disprove something.** If a comment turns out to be wrong, say so in place rather than silently deleting it — "an earlier version of this comment claimed X; that is wrong, because …". A future reader who finds the old claim elsewhere needs to know it was tested.

**No dead-reckoned magic numbers.** Derive mappings from asset paths rather than hand-writing tables; hand-written tables are what produced the development-blueprint bug.

## Tests

Run the suites before opening a pull request:

```bash
go test ./mmogbrain/... ./shared/... ./auth-server/... ./legacy-api/... ./gateway/...
go vet ./mmogbrain/...          # per module; the workspace has no unified ./...
```

`go vet` is clean across every module and should stay that way.

`golangci-lint run` is worth running but does **not** currently pass: it reports 11 pre-existing findings in `mmogbrain` (nine `unused`, one `ineffassign`, one `staticcheck`). Check that your change does not *add* to that list rather than expecting a clean run. Clearing the backlog is welcome as its own pull request — separate from a behaviour change, so the diff stays readable.

Two things about this suite in particular:

**`TestPayloadSizesVerify` is a deliberate tripwire.** It pins the exact byte size of every response. When your change moves one, *do not just update the number* — add a line to the comment above it explaining what grew or shrank and why. That comment block is a changelog of every payload decision, and it has caught real regressions.

**Tests encode client behaviour, so a failing test may be the correct one.** If a test blocks your fix, work out whether it is asserting a genuine client requirement or a belief that has since been disproved. Both happen. When it is the latter, update the test *and* write down why the old assertion was wrong — twice now a test has faithfully encoded a mistaken theory and then defended it.

Where practical, pin a finding with a test that would have caught the original bug (`TestTechTreeClassIdsPassTheManagerStoreGate` is the model: it states the gate, in the terms the binary states it).

## Commits and pull requests

Subjects are lowercase, scoped where useful, and say what changed in plain terms:

```
techtree: ClassId must be an item id, not the EYShipClass ordinal
market: restore the display Name field on catalog entities
docs: make setup, run and README match reality
```

Bodies matter more than usual. For anything protocol-related, record the evidence: which function, which offset, what the client logged before and after. A future contributor hitting the same wall should be able to find your commit and skip the whole investigation.

Say plainly what you did **not** verify. "Not yet confirmed against a running client" is useful information, and this project moves fast enough that unverified-but-reasoned changes are welcome — as long as they are labelled.

## Reporting a problem

A good report includes:

- the client log (`%LOCALAPPDATA%\DreadGame\Saved\Logs\DreadGame.log`) — the whole file, not an excerpt, since the useful line is often nowhere near the visible symptom;
- the relevant `run/<service>.log`;
- what you saw versus expected;
- your `SERVER_IP`, and whether client and server are on the same machine.

Please scrub tokens from logs before posting.

## Scope and legal

This project distributes **no game code or assets**. You need your own copy of Dreadnought.

`data/` holds extracted tables (item ids, names, loadouts) used to make the server's responses match what the client expects. Those are derived from the game and are **not** covered by this repository's licence — they belong to their original owner and are included on a fan-preservation basis. Do not add game binaries, cooked content, or bulk asset dumps.

Contributions are accepted under the [Apache License 2.0](LICENSE). By submitting a pull request you agree your work is licensed that way, and that you have the right to license it.
