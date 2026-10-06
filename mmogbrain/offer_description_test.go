package main

import (
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Every offer has a description of at least two characters: the Market's
// offer converter (0x142A7D7E0, from 0x142A5B960) reads "Description" and
// shows "<DNT> Invalid Description Field in Json" for anything shorter -- what
// every cosmetic showed (operator, 2026-10-06). "description" and
// "Description" are one key to the client, and the localized object wins.
func TestEveryOfferHasADescription(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	setCredits(t, pid, 0)
	seeds := append(gatewayItemCatalogSeeds(pid), gatewayBundleCatalogSeeds()...)
	for _, seed := range seeds {
		e := gatewayMarketEntity(seed, true)
		text := ""
		if loc, ok := e["description"].(dreadconfig.Localized); ok {
			text = loc["en"]
		} else if s, ok := e["Description"].(string); ok {
			text = s
		}
		if len([]rune(text)) < 2 {
			t.Errorf("offer %d (%s): description %q", seed.itemID, seed.displayName, text)
		}
	}
	// A head says what the game says about it.
	for _, seed := range seeds {
		if seed.itemID == 872349774 { // Head_Male_A02
			if d := seed.localizedDescription["en"]; len(d) < 20 {
				t.Errorf("head A02 description %q, want the game's own text", d)
			}
		}
	}
}
