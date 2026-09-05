---
name: dreadnought-mmog-responses
description: How to shape a binary MMOG response the Dreadnought client will actually read. Use before adding or debugging any YA_* response in mmogbrain, and whenever the client "ignores" a payload, logs a default/fallback value, reports empty data, or stalls after a request. Covers the response NAME (request names and reply names differ), the zlib "packed" blob pattern, the mandatory root terminator, the 32768-byte receive ring, container/array encoding traps, and how to read the client's dispatcher to find what a response must contain.
---

# Shaping a response the client will read

The client fails **silently** on a malformed response. It does not error, and it
usually does not stop — it logs a default, or a fallback, or nothing at all, and
keeps running. So "the client ignored our data" is never the finding; it is the
symptom. Every entry below was paid for with a live test cycle.

Work top-down: a response that is never routed cannot be fixed by changing its
contents, and a document whose root never closes cannot be fixed by re-ordering
its children. Both of those cost multiple rounds before being found.

## 1. The reply name is not always the request name

The client sends `YA_Tune` and dispatches the reply on **`YA_TuneReturn`**.
Answering with the request name matches no dispatcher branch and the response is
dropped without a log line.

Only five names carry the suffix: `YA_CheckReturn`, `YA_CustomRoomUserReturn`,
`YA_CustomRoomUserReturnResponse`, `YA_RoomReturn`, `YA_TuneReturn`. Most
responses correlate by request id and reuse the request name — do not rename
anything on a hunch.

**How to check a name before trusting it.** The response dispatcher is one large
function, `0x142a236c2-0x142a31a32`, which is a chain of
`strcmp(incoming_name, "YA_...")`. A name with a branch there is dispatched by
NAME; a name whose only xref is inside a send function is a REQUEST name only:

```console
$ python .claude/skills/dreadnought-rva/scripts/strxref.py "YA_TuneReturn"
```

`YA_Tune` has exactly one xref in the whole image, inside the function
`RequestUpdateFromServer` calls to send. That asymmetry is the tell.

## 2. Some responses carry everything in one zlib blob

Two known responses ignore every field except a single byte array holding a
zlib-compressed document:

| Response | Field | Contents |
| --- | --- | --- |
| `YA_GetTechTree` | `TechTrees` | unnamed array of arrays of items |
| `YA_TuneReturn` | `packed` | `Returning` + `result`, tables inside `Returning` |

Everything else in such a frame is read by nothing. Build with
`compressMmogDocument(...)` and `protocol.AppendBytesField`.

The tell in the disassembly is the byte-array accessor `0x142a14200`
(`[rcx+0x38]` = data, `[rcx+0x40]` = length) applied to the result of a field
lookup (`0x140237c30`). If you see that pair, the field is a blob.

## 3. A packed document MUST terminate its root

`protocol.AppendRootEnd(b)` — the six bytes `00 0e 00 00 00 00` — is not
optional. `BuildResponseFrame` appends it to every frame payload automatically,
so it is easy to forget that a document you build *by hand* and hand to
`AppendBytesField` needs its own.

Symptom of forgetting it, which is worth memorising because it looks like
anything but an encoding bug:

- the named container at the top resolves, so no "empty" error is logged;
- **nothing inside it** resolves — every child reads as absent;
- so a version string comes back empty AND every table lookup misses, at once.

Two rounds of re-ordering children were spent before this was found. If a
container is found but its children are not, check the terminator first.

## 4. Size: the ceiling is the 32768-byte RING, not the 65535 frame limit

The 16-bit frame size field allows 65535. The client's mmog receive ring is
**32768**. A 40,316-byte response is legal by the frame limit and still kills the
client: it logged the request, then produced no further line of any kind, and the
hangar never became interactive. **Oversized frames do not degrade; they stop.**

Budget well under it. `YA_GetTechTree` has shipped at 25,846 bytes, so ~26KB is
proven; 21KB is comfortable; anything over 30KB is gambling with a silent hang.

Compression changes the arithmetic completely — tuning JSON runs about 10:1
(367,829 bytes of tables → 30,258 compressed). Sort your keys before serialising:
sorted data compresses meaningfully better, and it also makes payload sizes
deterministic so a size tripwire can pin them.

## 5. Once the client accepts a payload, it TRUSTS it — so empty is not neutral

This is the trap that follows every success. The moment `YA_TuneReturn` started
parsing, the client stopped consulting its own cooked tables and believed our
empty ones:

```text
LogYTuneManager:Error: LoadWeaponRow() Weapon Data for
  'WP_CreepPrimary01_weapon01_BP' Couldn't be found.
LogYWeaponGroup:Error: Couldn't find OTS data for weapon ... Trying in
  offline datatable.
```

Those errors did not exist before the response started working. An empty table is
worse than no response at all. When you get a new response accepted, either fill
it or turn it off.

## 6. Echo cooked data tables VERBATIM

The client looks tuning rows up by **blueprint asset name**, which is exactly the
key `data/datatables/DN_*_OTS_DT.json` already uses. A builder that reshapes the
table will get all three of these wrong at once, and each alone breaks every
lookup:

- synthesising `RowName` (e.g. `"Weapon_<itemID>"`) — matches nothing;
- keying by player item id — drops rows with no item id (creep weapons, turret
  abilities: 159 of 226 rows survived);
- copying a subset of fields (11 of 47).

`otsTableRowsJSON()` in `shared/dreadgameconfig/tune_json.go` echoes a table
row-for-row, field-for-field, under its own row name. Prefer it. Data that *is*
the client's table cannot drift from what the client expects.

## 7. Encoding traps that predate this file

- **Container length excludes its own 4-byte field but includes the 6-byte
  terminator.** Off by 4 and a container swallows its next sibling.
- **Arrays (tag `0x0d`) discard child names.** A list the client looks up by name
  must be an object with children named `"0"`, `"1"`, … —
  `protocol.AppendIndexedStringListField`.
- **A container with a container sibling AFTER it can have its parsed value tree
  corrupted.** Put containers that must parse last. (Tried twice as a fix for the
  root-terminator bug in §3 and it fixed nothing — it is a real rule, but do not
  reach for it first.)
- **Field names compare case-insensitively on the binary protocol** but not in
  the JSON catalog lookups — `Name` and `name` are both needed there.
- **Top byte of an item id is its `ItemIDTable` CategoryID.** Several client
  gates admit ids by that byte alone.

## 8. Debugging order

1. Read the client log for the subsystem's own lines (`LogYTuneManager`,
   `LogYLoadout`, …). **A missing line is data**: if neither branch of a
   two-branch function logged, the function never ran, and nothing about your
   payload's contents is in question yet.
2. Find the log literal in the binary and read the code around it
   (`dreadnought-rva`).
3. Only then form a theory about the payload.

Never conclude "the client rejected our data" from a fallback value. `Client
synced to server version: backup-data` was read as a rejection for several
rounds; it was the untouched startup default, and `Set()` had never been called.

## 9. Verifying a change without a client

- `cd mmogbrain && go test ./...` — `TestPayloadSizesVerify` pins every response
  size as a tripwire. When a size moves, add a line to its comment saying what
  grew and why (blob-carrying responses have a 64-byte tolerance because
  compressed length shifts).
- Navigate your own document with `extractNamedMmogObject` /
  `protocol.ExtractStringField` in a scratch test. Our parser is more forgiving
  than the client's, so this proves the encoding is *self*-consistent, not that
  the client will read it — but it catches gross errors fast.
- Dump the bytes (`hex.Dump`) and compare the shape against a document the client
  demonstrably parses. That comparison is what found the missing root terminator.
