package main

import (
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
)

func TestZZTechTreeDupes(t *testing.T) {
	d, err := db.Open("/tmp/claude-0/-root-projects/c70be35b-3f2a-463a-bba4-35cb4c4a971e/scratchpad/mmog-copy.db")
	if err != nil { t.Fatal(err) }
	setMmogPlayerStateDB(d)
	for _, pid := range []string{"fea9903d49d841bcbd679d96806b9fb7", "650dd79476a1484b8adcd01ac2f17354"} {
		ships := playerOwnedTechTreeShips(pid)
		ids := map[int32]int{}
		for _, s := range ships { ids[s.id]++ }
		dup := 0
		for _, n := range ids { if n > 1 { dup++ } }
		base := map[int32]bool{}
		for _, it := range append(techTreeBaseItems(), techTreeHeroItems()...) { base[it.id] = true }
		alsoInTree := 0
		for _, s := range ships { if base[s.id] { alsoInTree++ } }
		t.Logf("%s: techTreeRows=%d distinct=%d dupIDs=%d rowsAlsoTreeItems=%d treeItems=%d doc=%d", pid[:8], len(ships), len(ids), dup, alsoInTree, len(base), len(buildMmogTechTreeDocument()))
	}
}
