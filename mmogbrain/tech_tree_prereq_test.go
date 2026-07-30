package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Prereq entries are matched against other items' Id, so they have to live in
// the same id space the rows are keyed on. They used to be ship-pawn ids while
// Id was the precast loadout id, which made every prerequisite dangling -- and
// a pawn id (category 10) could never be admitted anyway, since the gate only
// accepts YShipLoadoutPrecast (1) and YShipLoadoutHero (3).
func TestTechTreePrereqsNameRowsInTheSameDocument(t *testing.T) {
	document := inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	// Field names are length-prefixed, so anchor on \x02Id\x09 -- a bare "Id"
	// also matches the tail of "ClassId", which would let a prereq "pass" by
	// colliding with a class number.
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\x02Id\x09.{4}(\d+)`).FindAllStringSubmatch(string(document), -1) {
		ids[match[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("no item ids found in the tech tree document; the scan is not working")
	}

	// Every id in the document, prereq or row, must be a loadout or hero id.
	prereqs := 0
	for _, match := range regexp.MustCompile(`\x06Prereq\x0d.{4}(.{0,64})`).FindAllStringSubmatch(string(document), -1) {
		for _, candidate := range regexp.MustCompile(`\x00\x09.{4}(\d+)`).FindAllStringSubmatch(match[1], -1) {
			prereqs++
			id, err := strconv.Atoi(candidate[1])
			if err != nil {
				t.Fatalf("prereq %q is not numeric", candidate[1])
			}
			if category := (id >> 24) & 0xff; category != 1 && category != 3 {
				t.Errorf("prereq %d is category %d; the gate only admits 1 (precast) and 3 (hero)", id, category)
			}
			if !ids[candidate[1]] {
				t.Errorf("prereq %s names no row in the document (rows: %s)", candidate[1], strings.Join(keysOf(ids), ", "))
			}
		}
	}
	if prereqs == 0 {
		t.Fatal("no prereq entries were checked; the tree carries none, so this test proves nothing")
	}
	t.Logf("checked %d prereq entries against %d rows", prereqs, len(ids))
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
