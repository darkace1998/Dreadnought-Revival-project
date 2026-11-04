package matchmaker

import "testing"

// The Custom Game tile is listed only with DN_CUSTOM_GAME_TILE=1.
func TestCustomGameTileSwitch(t *testing.T) {
	listed := func() bool {
		for _, m := range GameModeConfigs() {
			if m.Name == CustomGameClientName {
				return true
			}
		}
		return false
	}
	t.Setenv("DN_CUSTOM_GAME_TILE", "")
	if listed() {
		t.Fatal("CustomGame listed without the switch")
	}
	t.Setenv("DN_CUSTOM_GAME_TILE", "1")
	if !listed() {
		t.Fatal("CustomGame not listed with DN_CUSTOM_GAME_TILE=1")
	}
}
