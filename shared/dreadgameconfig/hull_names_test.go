package dreadgameconfig

import "testing"

// The four hulls ItemIDConversionTable names from the previous build.
//
// These are not a guess: the client-side half of the project cross-checked all
// four against a live client (AGENT-CHAT.md, C5) and predicted the mechanism
// before this fix existed, and the precast blueprints then said the same thing.
// Each expected name is the string inside the blueprint the client actually
// loads.
func TestRenamedHullsResolveToTheCurrentName(t *testing.T) {
	for _, tc := range []struct {
		slot      string
		precastID int32
		want      string
		legacy    string
	}{
		{"ScoutLight T3", 33489276, "Machias", "Lerwick"},
		{"ScoutLight T5", 33489305, "Nevis", "Bakar"},
		{"AssaultHeavy T3", 33489271, "Dola", "Kama"},
		{"ScoutHeavy T4", 33489289, "Stribog", "Perun"},
	} {
		got, ok := AuthoritativeItemName(tc.precastID)
		if !ok {
			t.Errorf("%s (%d): no name at all", tc.slot, tc.precastID)
			continue
		}
		if got == tc.legacy {
			t.Errorf("%s (%d) = %q, the previous build's name; want %q. "+
				"The hull-name overlay is not being applied.", tc.slot, tc.precastID, got, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%s (%d) = %q, want %q", tc.slot, tc.precastID, got, tc.want)
		}
	}
}

// A hull the table already names correctly must not move. The overlay corrects
// four names; it must not become a second, competing naming authority that
// quietly disagrees with the table everywhere else.
func TestUnrenamedHullsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		slot      string
		precastID int32
		want      string
	}{
		{"SniperMedium T1", 33489263, "Rurik"},
		{"AssaultMedium T1", 33489262, "Agosta"},
		{"SupportMedium T1", 33489264, "Cerberus"},
	} {
		got, ok := AuthoritativeItemName(tc.precastID)
		if !ok || got != tc.want {
			t.Errorf("%s (%d) = %q (ok=%v), want %q", tc.slot, tc.precastID, got, ok, tc.want)
		}
	}
}

// All 52 player hulls, and only those. The generator refuses to write the file
// unless every subclass matches the class in its own filename, so a short count
// here means the file was hand-edited or the extraction silently lost assets.
func TestHullNamesCoverEveryPlayerHull(t *testing.T) {
	hulls := GetAllHullNames()
	if len(hulls) != 52 {
		t.Fatalf("got %d hulls, want 52 (T1..T5 under /Game/Generic/Loadouts/Precast)", len(hulls))
	}
	byTier := map[int]int{}
	for _, hull := range hulls {
		if hull.Name == "" || hull.Asset == "" || hull.Subclass == "" {
			t.Errorf("incomplete entry: %+v", hull)
		}
		byTier[hull.Tier]++
	}
	for tier, want := range map[int]int{1: 4, 2: 6, 3: 12, 4: 15, 5: 15} {
		if byTier[tier] != want {
			t.Errorf("tier %d has %d hulls, want %d", tier, byTier[tier], want)
		}
	}
}

// Simargl has no ItemIDConversionTable row at all, so the table could never have
// named it. The blueprint can, which is a small side benefit of joining on the
// asset path rather than through the table.
func TestHullWithNoConversionRowStillResolves(t *testing.T) {
	name, ok := AuthoritativeNameForAssetPath("/Game/Generic/Loadouts/Precast/T1/VH_DreadnoughtMedium_T1_PrecastLoadout_BP")
	if !ok || name != "Simargl" {
		t.Errorf("DreadnoughtMedium T1 = %q (ok=%v), want %q", name, ok, "Simargl")
	}
}
