package main

import "testing"

// The client crashes with EXCEPTION_STACK_OVERFLOW (C00000FD) when the operator
// clicks any module on any ship. Diagnosed 2026-08-08 from a 3.1 GB full-memory
// dump plus the debugger's first-chance report:
//
//	ExceptionCode: C00000FD (EXCEPTION_STACK_OVERFLOW)
//	ExceptionAddress: ntdll.00007FFF27CA13F8
//
// The dump's main thread holds 16,382 frames returning to 0x3F49A0 -- the
// instruction after FUN_3F4880's call to ITSELF at 0x3F499B -- interleaved with
// 16,383 returning to 0x3E9A00. Two return sites, 32,765 frames, one cycle.
//
// The recursion is gated only by "does an item with this id exist":
//
//	03F4976  mov  rax, [rbp+0x77]   ; the current item
//	03F4981  mov  edx, [rax+0x28]   ; its ClassId
//	03F4984  call 0x3f51a0          ; find the item whose Id == that -> al
//	03F498B  je   0x3f49a0          ; not found -> stop
//	03F499B  call 0x3f4880          ; found -> recurse into it
//
// FUN_3F51A0 confirms +0x20 is Id (it compares [rbx+0x20] against the query)
// and copies +0x20..+0x40 out on a hit, including the ProxyType byte at +0x3c.
//
// So an item whose ClassId is its own Id finds itself and never terminates.
// Every hull node we emitted did exactly that, on the since-disproved belief
// that ClassId had to equal the hull's own id for its modules to resolve. That
// is why the crash was universal rather than tied to one item: clicking a module
// walks one correct hop to its hull, and the hull then walks to itself.
//
// FIXED and VERIFIED LIVE 2026-08-08 with the operator's client: 0 crashes, 12
// module detail panels opened, 0 "Modules not found for ship id", clean exit.
// The conflict was imaginary -- the per-ship record is created and filled by the
// MODULE entries, each of which still carries its hull's id, so the hull node's
// own ClassId was never what made modules resolve.
//
// Not self-referencing is now the DEFAULT. DN_TECHTREE_SELF_CLASSID=1 restores
// the crashing shape and exists only to re-measure it.

func techTreeSelfReferencingHulls(t *testing.T) (selfRef int, total int) {
	t.Helper()
	items := append(techTreeBaseItems(), techTreeHeroItems()...)
	ids := map[int32]bool{}
	for _, it := range items {
		ids[it.id] = true
	}
	for _, it := range items {
		// Only a self-reference that RESOLVES recurses; one naming a
		// non-existent id makes FUN_3F51A0 return 0 and the walk stops.
		if it.classID == it.id && ids[it.classID] {
			selfRef++
		}
	}
	return selfRef, len(items)
}

// NOTHING may name itself by default. This is the crash, and it is a hard fail.
func TestNothingSelfReferencesByDefault(t *testing.T) {
	selfRef, total := techTreeSelfReferencingHulls(t)
	t.Logf("%d of %d tech tree items have ClassId == their own resolvable Id", selfRef, total)
	if selfRef != 0 {
		t.Errorf("%d items name themselves via ClassId; the client recurses into ClassId "+
			"(FUN_3F4880 -> FUN_3F51A0 -> FUN_3F4880) and will stack-overflow on any "+
			"module click, which is the crash fixed on 2026-08-08", selfRef)
	}
}

// The A/B switch must still reproduce the crashing shape, or we cannot re-measure it.
func TestSelfClassIDSwitchRestoresTheCrashingShape(t *testing.T) {
	t.Setenv("DN_TECHTREE_SELF_CLASSID", "1")
	selfRef, total := techTreeSelfReferencingHulls(t)
	t.Logf("with DN_TECHTREE_SELF_CLASSID=1: %d of %d items self-reference", selfRef, total)
	if selfRef == 0 {
		t.Error("the A/B switch no longer reproduces the self-reference, so the crash " +
			"cannot be re-measured; either wire it back up or delete it")
	}
	// Only hull nodes, never modules -- a self-referencing module would be a
	// different bug.
	for _, it := range append(techTreeBaseItems(), techTreeHeroItems()...) {
		if it.classID == it.id && it.module {
			t.Errorf("module %d self-references via ClassId; only hull nodes ever did", it.id)
		}
	}
}

// Breaking the self-loop is worthless if it leaves a longer cycle behind.
func TestClassIDChainTerminates(t *testing.T) {
	items := append(techTreeBaseItems(), techTreeHeroItems()...)
	next := map[int32]int32{}
	for _, it := range items {
		if _, seen := next[it.id]; !seen {
			next[it.id] = it.classID
		}
	}
	for start := range next {
		seen := map[int32]bool{}
		for cur := start; ; {
			if seen[cur] {
				t.Fatalf("following ClassId from %d revisits %d: still a cycle", start, cur)
			}
			seen[cur] = true
			n, ok := next[cur]
			if !ok {
				break // unresolvable: FUN_3F51A0 returns 0 and the client stops
			}
			cur = n
		}
	}
	t.Logf("%d ClassId chains, all terminate", len(next))
}

// The price of the fix, pinned so it cannot grow quietly.
//
// The loader gate drops any item whose ClassId is <= 0:
//
//	MOVSXD R15,[RBP-0x78]   ; ClassId
//	TEST R15D,R15D / JLE skip
//
// A line root has no prerequisite to point at and a hero has no line at all, so
// both go out with 0 and are NOT stored. That is a real cost and it is accepted
// on purpose: the alternative is the self-reference, which crashes the client on
// every module click. Verified live 2026-08-08 that the tree still works with
// these dropped -- 12 module panels opened, 0 crashes, and none of the symptoms
// dropped nodes used to cause ("Could not find a manufacturer with id",
// "TreeWidgetList of length 0") appeared in the client log.
//
// The open refinement is to give roots and heroes a category-1 id that is NOT a
// tree row: it would pass the gate, keep the node stored, and still terminate
// the walk because FUN_3F51A0 would not find it. Nothing has established which
// id the shipped data used, so that is not invented here.
func TestLineRootsAndHeroesAreTheOnlyDroppedNodes(t *testing.T) {
	items := append(techTreeBaseItems(), techTreeHeroItems()...)
	dropped := 0
	for _, it := range items {
		if it.classID > 0 {
			continue
		}
		dropped++
		if it.module {
			t.Errorf("MODULE %d has ClassId %d and will be dropped by the loader gate; "+
				"only hull line roots and heroes may be", it.id, it.classID)
		}
	}
	t.Logf("%d of %d items are dropped by the ClassId <= 0 gate", dropped, len(items))

	// 15 line roots (five classes x three sizes) plus the heroes. If this
	// moves, the roster changed or the fix regressed -- either way, look.
	const want = 63
	if dropped != want {
		t.Errorf("%d nodes dropped, want %d. If the tech tree roster genuinely changed, "+
			"update this number and say why; if it did not, something is emitting "+
			"ClassId 0 that should not be.", dropped, want)
	}
}
