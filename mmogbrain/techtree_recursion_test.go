package main

import (
	"os"
	"testing"
)

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
// Every hull node we emit does exactly that, deliberately -- ClassId doubles as
// the per-ship record key and modules only resolve when it equals the hull's own
// id. That is why the crash is universal rather than tied to one item: clicking
// a module walks one correct hop to its hull, and the hull then walks to itself.
//
// This test does not assert the self-reference away, because the conflicting
// requirement is real and unresolved. It pins the measurement so the count
// cannot drift silently, and asserts the escape hatch actually works.

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

// The default still self-references. If this number ever changes, the crash
// surface changed with it and somebody should know why.
func TestHullNodesStillSelfReferenceByDefault(t *testing.T) {
	if os.Getenv("DN_TECHTREE_NO_SELF_CLASSID") == "1" {
		t.Skip("escape hatch is on; TestNoSelfClassIDSwitchBreaksTheCycle covers that")
	}
	selfRef, total := techTreeSelfReferencingHulls(t)
	t.Logf("%d of %d tech tree items have ClassId == their own resolvable Id", selfRef, total)
	if selfRef == 0 {
		t.Fatal("no item self-references any more -- if that was deliberate, delete this test " +
			"and the DN_TECHTREE_NO_SELF_CLASSID hatch; if it was not, the tech tree changed shape")
	}
	// Every one of them is a hull node, not a module. A self-referencing MODULE
	// would be a different bug and is worth failing on.
	items := append(techTreeBaseItems(), techTreeHeroItems()...)
	for _, it := range items {
		if it.classID == it.id && it.module {
			t.Errorf("module %d self-references via ClassId; only hull nodes should", it.id)
		}
	}
}

// The escape hatch has to actually break the cycle, or the A/B measures nothing.
func TestNoSelfClassIDSwitchBreaksTheCycle(t *testing.T) {
	t.Setenv("DN_TECHTREE_NO_SELF_CLASSID", "1")
	selfRef, total := techTreeSelfReferencingHulls(t)
	t.Logf("with the hatch on: %d of %d items self-reference", selfRef, total)
	if selfRef != 0 {
		t.Errorf("%d items still name themselves with DN_TECHTREE_NO_SELF_CLASSID=1; "+
			"the client would still recurse forever", selfRef)
	}
}

// And with the hatch on, following ClassId as a chain must terminate -- breaking
// the self-loop is worthless if it leaves a longer cycle behind.
func TestClassIDChainTerminatesWithTheHatchOn(t *testing.T) {
	t.Setenv("DN_TECHTREE_NO_SELF_CLASSID", "1")
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
