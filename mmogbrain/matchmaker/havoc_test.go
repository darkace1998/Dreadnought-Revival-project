package matchmaker

import "testing"

// The client queues its Havoc tile as "HAVOC"; it must reach the Havoc host
// mode on a Havoc map, not Onslaught (the old alias). From PR #80.
func TestHavocRouting(t *testing.T) {
	for _, in := range []string{"HAVOC", "PvE_Havoc"} {
		if !ValidGameMode(in) {
			t.Fatalf("%s not accepted", in)
		}
		if got := NormalizeGameMode(in); got != HavocGameMode {
			t.Fatalf("%s -> %s, want %s", in, got, HavocGameMode)
		}
	}
	if maps := mapsByGameMode[HavocGameMode]; len(maps) != 2 {
		t.Fatalf("havoc maps = %v", maps)
	}
	if matchTeam(HavocGameMode, 1) != 1 {
		t.Fatal("havoc is co-operative: every player on team 1")
	}
	if got := runnableGameMode(HavocGameMode); got != HavocGameMode {
		t.Fatalf("havoc runs as %s", got)
	}
	t.Setenv("DN_HAVOC", "")
	for _, c := range GameModeConfigs() {
		if c.Name == havocClientName {
			t.Fatal("HAVOC listed without DN_HAVOC=1")
		}
	}
	t.Setenv("DN_HAVOC", "1")
	found := false
	for _, c := range GameModeConfigs() {
		found = found || c.Name == havocClientName
	}
	if !found {
		t.Fatal("HAVOC not listed with DN_HAVOC=1")
	}
}
