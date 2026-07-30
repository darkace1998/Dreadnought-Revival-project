package dreadgameconfig

import "testing"

// The hand-written name maps are only a fallback now, but a fallback that
// disagrees with the game is still a lie waiting to be shown to a player --
// "Leipzig" and "Trieste" sat in knownShipNames for exactly that reason. This
// asserts every hand-written entry the client can adjudicate actually matches.
func TestHandWrittenNamesAgreeWithTheClientTable(t *testing.T) {
	for _, table := range []struct {
		label   string
		entries map[string]string
	}{
		{"knownShipNames", knownShipNames},
		{"knownLoadoutNames", knownLoadoutNames},
	} {
		for assetPath, name := range table.entries {
			authoritative, ok := AuthoritativeNameForAssetPath(assetPath)
			if !ok {
				continue // the client's table has no say on this one
			}
			if name != authoritative {
				t.Errorf("%s[%q] = %q, but the client calls it %q", table.label, assetPath, name, authoritative)
			}
		}
	}
}

// Every item the server hands out gets a name, so a starter hull whose name is
// really its class descriptor ("Assault Medium T1") would reach the player as
// one. Ship pawns carry no name of their own, so this also pins the
// pawn -> precast-loadout resolution that supplies it.
func TestStarterShipsCarryTheirRealNames(t *testing.T) {
	want := map[int32]string{
		184483982: "Agosta",
		184483950: "Rurik",
		184484202: "Cerberus",
	}
	for _, item := range StarterInventoryItems() {
		if item.Item.ItemType != ItemTypeShip {
			continue
		}
		expected, checked := want[item.Item.ItemID]
		if !checked {
			continue
		}
		got, ok := AuthoritativeShipName(item.Item.ItemID)
		if !ok {
			t.Errorf("ship %d (%s) resolves to no authoritative name", item.Item.ItemID, item.Item.AssetPath)
			continue
		}
		if got != expected {
			t.Errorf("ship %d authoritative name = %q, want %q", item.Item.ItemID, got, expected)
		}
		if item.Item.DisplayName != expected {
			t.Errorf("ship %d DisplayName = %q, want %q", item.Item.ItemID, item.Item.DisplayName, expected)
		}
	}
	// 33489423, the Dreadnought Medium T1 loadout, is the one starter hull the
	// conversion table omits; its name is corroborated elsewhere, not here.
	if _, ok := AuthoritativeItemName(33489423); ok {
		t.Error("33489423 now has a conversion-table name; assert it directly instead of exempting it")
	}
}
