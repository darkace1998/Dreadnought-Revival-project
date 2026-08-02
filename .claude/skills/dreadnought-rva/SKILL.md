---
name: dreadnought-rva
description: Locate and verify functions in DreadGame-Win64-Shipping.exe. Use when you need to answer "where does the client do X?" or "what does the client actually expect here?" - finding the code behind a log message, confirming an RVA is a real function entry before hooking it, finding a function's callers, or batch-decompiling with headless Ghidra. Also use before trusting any RVA that came from a previous session, another agent, or a comment.
---

# Finding functions in the Dreadnought client

The game is a stripped 47.9 MB UE4 shipping binary with no symbols. Three
techniques cover almost everything, and they compose in a fixed order.

**Why this is in a server repository.** The recurring hard question here is
*"what does the client actually expect?"* — the shape of a response, whether a
field is a string or an int32, why a value that looks right is ignored. That is
usually answered by inference from wire behaviour, which is slow and sometimes
wrong. It can be read directly out of the binary instead. `authoritative_names.go`
is a good example of the kind of reconstruction this shortens.

**Set `DREADGAME_EXE` to your copy of the executable.** No game content ships
with this skill; every script reads the binary you already own. Scripts live in
`scripts/` and are run with `python` from that directory — pure stdlib, no
dependencies, and platform-independent, so they work fine on Linux against a
Windows executable. Only the optional Ghidra step needs anything installed.

## Rule zero: .pdata decides what a function is

The PE exception directory holds a `RUNTIME_FUNCTION` record for every function
the linker emitted — 224,934 of them in this binary. It is the authority for
"is this RVA a function entry?" Nothing else is.

```console
$ python pdata.py 370970 370980
0x370970    ENTRY of 0x370970-0x370A1D (size 173)
0x370980    inside 0x370970-0x370A1D, +0x10 from entry
```

**Do not scan bytes backwards looking for `0xCC` padding to find a function
start.** It looks like it works and it is wrong: `49 8B CC` is `mov rcx, r12`,
an ordinary instruction whose last byte is `0xCC`. A previous session lost hours
to a "function entry" that was the middle of an instruction. `python pdata.py
--stats` prints the table summary if you want to sanity-check the parse.

Run `pdata.py` on any RVA before hooking it, and on any RVA you inherited from a
comment, a memory file, or another agent. Cheap, and it has caught real errors.

### The one exception: leaf functions

x64 lets a **leaf** function — no stack allocation, no calls, no non-volatile
register saves — carry no unwind record at all. So `NO UNWIND RECORD` means
"this is not a function *with unwind data*", which is not the same as "this is
not code":

```console
$ python pdata.py 322C30
0x322C30    NO UNWIND RECORD (gap 0x322C2C..0x322C40, 20 bytes) -- padding, data, or a leaf/tail-call stub
```

That address is real code — a 12-byte argument-shuffling stub that tail-calls
the actual function. When you get this result, look at the bytes before
concluding anything. A short run ending in `E9 rel32` (jmp) is a tail-call stub;
follow the jump. `CC CC CC` really is padding.

That specific stub is `mov eax,edx; mov rdx,rcx; mov ecx,eax; jmp 0x543BE0` —
it **swaps its two arguments** before tail-calling the real function. Reading it
as the destination would have you reading an item id as an object pointer. When
a chain looks one hop shorter than it is, this is usually why. The
`dreadnought-hooks` skill has the full case.

## 1. String → function

A log literal is the one part of a stripped binary that still says what the
surrounding code is for. Most of the RVA map was found this way.

```console
$ python strxref.py "Active Loadout not found"
[utf-16le] str 0x2E8CD0C <- LEA 0x38256C (target 0x2E8CCD0)
    func:    inside 0x382530-0x382603, +0x3C from entry
    literal: AYGameMode::SpawnDefaultPawn: Active Loadout not found. Can't spawn
```

`0x382530` is `SpawnDefaultPawn`. Note the literal includes the class and method
name — UE4 log messages usually name their own function, so one hit often gives
you the symbol for free.

How it works: it indexes every RIP-relative `LEA` target in `.text` in one pass,
then reports any whose target lands in a window ending at the literal.

**The window is the part that trips people up.** UE4 references a literal at its
**first character**, so searching for text from the middle of a message finds
the bytes but no `LEA` pointing at them — you get `no LEA within 400 bytes`. Use
an early fragment of the message, or widen with `--window 800`.

Both UTF-16LE and ASCII are searched. UE4 log literals are nearly always
UTF-16LE.

## 2. Function → its callers

```console
$ python callers.py 370970
=== direct callers of 0x370970 ===
    (ENTRY of 0x370970-0x370A1D (size 173))
  func 0x3827D0-0x382854  (calls at 0x382824)
  func 0x382870-0x382A23  (calls at 0x3829E9)
```

Scans for direct `E8 rel32` calls. `none` is a real answer, not a failure — it
means the function is reached only through a vtable.

**Use this to find the choke point.** The function that logs an error is rarely
where you want to intervene; it is just where the complaint is printed. Walk
outward until several paths converge on one function, and hook that.

Worked example, the in-match spawn fix:

1. `strxref.py "Active Loadout not found"` → `0x382530`, `SpawnDefaultPawn`.
2. That function only *reports* the null. Walking outward found two callers,
   `0x3827D0` and `0x382870`, both routing through one loadout lookup at
   `0x370970`.
3. Hooking `0x370970` covered both paths with one hook. Hooking the logger would
   have covered neither.

## 3. Batch decompile with headless Ghidra

`DumpFuncs.java` decompiles **and** disassembles a list of RVAs in one run.
Both matter: the decompiler invents locals and loses field offsets, while the
raw disassembly keeps `[rcx+0x3898]` intact.

```console
analyzeHeadless.bat <project_dir> <project_name> \
  -process "DreadGame-Win64-Shipping.exe" -noanalysis \
  -scriptPath <path>/scripts -postScript DumpFuncs 370970 382530
```

Writes `dump_<RVA>.txt` per function into `./ghidra_output`, or
`$GHIDRA_DUMP_DIR`. `-noanalysis` reuses the existing analysis — full analysis
of this binary takes a long time, so let it finish once and never repeat it.

## Reporting what you find

State whether a claim is **verified** (you saw it in the binary or in a live
run) or **suspected** (it follows from something else). This project has a long
history of confident guesses costing days; an honest "suspected" is worth more
than a wrong "verified".

When you record an RVA anywhere durable, record the `.pdata` range with it —
`0x370970-0x370A1D`, not `0x370970`. The range is checkable later; a bare
address is not.
