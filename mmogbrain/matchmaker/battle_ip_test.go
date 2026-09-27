package matchmaker

import "testing"

// Players are never sent a battle-server NAME: the game has refused names in
// two other address fields.
func TestBattleServerAddressIsResolvedToIPv4(t *testing.T) {
	for in, want := range map[string]string{
		"localhost":   "127.0.0.1",
		"203.0.113.9": "203.0.113.9",
		"":            "",
	} {
		if got := battleServerIPv4(in, nil); got != want {
			t.Errorf("battleServerIPv4(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchTeams(t *testing.T) {
	for _, c := range []struct {
		mode string
		i    int
		want int
	}{
		{"TDM", 0, 1}, {"TDM", 1, 2}, {"TDM", 2, 1},
		{"BC", 0, 1}, {"BC", 1, 1}, {"Onslaught", 3, 1}, {"TM", 1, 1},
	} {
		if got := matchTeam(c.mode, c.i); got != c.want {
			t.Errorf("matchTeam(%s, %d) = %d, want %d", c.mode, c.i, got, c.want)
		}
	}
}
