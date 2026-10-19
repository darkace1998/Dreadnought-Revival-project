package gamedata

import "testing"

// "HAVOC" used to alias Onslaught, and alias matching is case-insensitive, so
// it also swallowed "Havoc" itself: an HVC map was hosted as ?game=Onslaught.
func TestHavocGameMode(t *testing.T) {
	for _, in := range []string{"Havoc", "HAVOC", "PvE_Havoc"} {
		got, err := NormalizeGameMode(in)
		if err != nil || got != "Havoc" {
			t.Fatalf("NormalizeGameMode(%q) = %q, %v; want Havoc", in, got, err)
		}
	}
	if got := GameOption("Havoc"); got != "/Game/Generic/GameModes/Horde/Havoc/GameMode_Havoc_BP.GameMode_Havoc_BP_C" {
		t.Fatalf("GameOption(Havoc) = %q", got)
	}
	if got, _ := NormalizeGameMode("Onslaught"); got != "Onslaught" {
		t.Fatalf("Onslaught -> %q", got)
	}
}
