package main

import (
	"strconv"
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// One hull, one tier, whichever screen asks. The progression payload hardcoded
// "tier": "1" for every ship, so six of the fourteen a starter account owns
// disagreed with the store and the tech tree about the same hull.
func TestProgressionReportsTheSameTierAsEverythingElse(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	const pid = "00000000000000000000000000000001"
	checked := 0
	for _, ship := range realShipsOnly(playerOwnedTechTreeShips(pid)) {
		want, ok := derivedShipTier(ship.id)
		if !ok {
			tier, hullOK := dreadconfig.HullTierForItemID(ship.id)
			if !hullOK {
				continue // nothing states a tier for this id; the fallback is all there is
			}
			want = tier
		}
		checked++
		if got := shipTierForID(ship.id); got != int32(want) {
			name, _ := dreadconfig.AuthoritativeShipName(ship.id)
			t.Errorf("%s (%d): progression says tier %d, its asset says %d", name, ship.id, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no owned ship had a derivable tier; the test is not exercising anything")
	}
	t.Logf("checked %d owned ships", checked)
}

// And the payload itself must carry it, not just the helper.
func TestProgressionPayloadCarriesRealTiers(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	const pid = "00000000000000000000000000000001"
	payload := string(buildMmogPlayerProgressionPayload(pid))

	// Trafalgar is Tier 2 and is owned by a starter account. Before the fix the
	// payload contained no tier other than "1".
	var wantTier int32 = 2
	found := false
	for _, ship := range realShipsOnly(playerOwnedTechTreeShips(pid)) {
		if shipTierForID(ship.id) == wantTier {
			found = true
			break
		}
	}
	if !found {
		t.Skip("no tier-2 ship in the starter roster any more")
	}
	if countWireStringField(payload, "tier", strconv.Itoa(int(wantTier))) == 0 {
		t.Errorf("no ship in the progression payload reports tier %d; the tier is hardcoded again", wantTier)
	}
}

// countWireStringField counts <name> string fields carrying exactly want.
func countWireStringField(payload, name, want string) int {
	n := 0
	for i := 0; i+len(name)+5 < len(payload); i++ {
		if payload[i:i+len(name)] != name || payload[i+len(name)] != 0x09 {
			continue
		}
		j := i + len(name) + 1
		size := int(uint32(payload[j]) | uint32(payload[j+1])<<8 |
			uint32(payload[j+2])<<16 | uint32(payload[j+3])<<24)
		if size > 0 && j+4+size <= len(payload) && payload[j+4:j+4+size] == want {
			n++
		}
	}
	return n
}
