package dreadgameconfig

import "testing"

func TestStarterIdentityListsPreserveCanonicalRosterOrder(t *testing.T) {
	wantShipIDs := []int32{184483982, 184484170, 184483950, 184484202}
	gotShipIDs := StarterInventoryShipIDs()
	if len(gotShipIDs) != len(wantShipIDs) {
		t.Fatalf("starter ship id count = %d, want %d", len(gotShipIDs), len(wantShipIDs))
	}
	for idx, wantID := range wantShipIDs {
		if gotShipIDs[idx] != wantID {
			t.Fatalf("starter ship id[%d] = %d, want %d", idx, gotShipIDs[idx], wantID)
		}
	}

	wantLoadoutIDs := []int32{33489262, 33489423, 33489263, 33489264}
	gotLoadoutIDs := StarterInventoryLoadoutIDs()
	if len(gotLoadoutIDs) != len(wantLoadoutIDs) {
		t.Fatalf("starter loadout id count = %d, want %d", len(gotLoadoutIDs), len(wantLoadoutIDs))
	}
	for idx, wantID := range wantLoadoutIDs {
		if gotLoadoutIDs[idx] != wantID {
			t.Fatalf("starter loadout id[%d] = %d, want %d", idx, gotLoadoutIDs[idx], wantID)
		}
	}
}

func TestStarterInventoryLoadoutsExposeAuthoritativeSlots(t *testing.T) {
	loadouts := StarterInventoryLoadouts()
	if len(loadouts) != 4 {
		t.Fatalf("starter loadout count = %d, want 4", len(loadouts))
	}

	rurik, ok := StarterInventoryLoadoutByShipName(starterShipDisplayNameSniperMediumT1)
	if !ok {
		t.Fatalf("missing %s starter loadout", starterShipDisplayNameSniperMediumT1)
	}
	if rurik.LoadoutID != 33489263 {
		t.Fatalf("Rurik loadout id = %d, want 33489263", rurik.LoadoutID)
	}
	if len(rurik.Slots) != 6 {
		t.Fatalf("Rurik slot count = %d, want 6", len(rurik.Slots))
	}
	if rurik.Slots[0].SlotName != SlotWeaponPrimary || rurik.Slots[0].ItemID != 100597987 {
		t.Fatalf("Rurik primary slot = %+v, want %s/100597987", rurik.Slots[0], SlotWeaponPrimary)
	}
	if rurik.Slots[5].SlotName != SlotAbilityInternal || rurik.Slots[5].ItemID != 83820764 {
		t.Fatalf("Rurik internal ability slot = %+v, want %s/83820764", rurik.Slots[5], SlotAbilityInternal)
	}
}

func TestStarterInventoryItemsExposeCanonicalInventoryIdentities(t *testing.T) {
	items := StarterInventoryItems()
	if len(items) != 32 {
		t.Fatalf("starter inventory item count = %d, want 32", len(items))
	}

	if items[0].Item.ItemType != ItemTypeShip || items[0].Item.ItemID != 184483982 {
		t.Fatalf("first starter inventory item = %+v, want %s ship", items[0], starterShipDisplayNameAssaultMediumT1)
	}
	if items[1].Item.ItemType != ItemTypeLoadout || items[1].Item.AssetPath != "/Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP" {
		t.Fatalf("second starter inventory item = %+v, want Agosta loadout asset identity", items[1])
	}

	var haveSupportPrimary bool
	for _, item := range items {
		if item.LoadoutID == 33489264 && item.SlotName == SlotAbilityPrimary {
			haveSupportPrimary = true
			if item.Item.ItemID != 83820857 {
				t.Fatalf("Cerberus primary ability id = %d, want 83820857", item.Item.ItemID)
			}
			if item.Item.AssetPath != "/Game/Generic/Abilities/Support/Pri_BeamAmp_Dmg/T0/AB_SU_Pri_BeamAmp_Dmg_Ability_T0_BP" {
				t.Fatalf("Cerberus primary ability asset path = %q", item.Item.AssetPath)
			}
		}
	}
	if !haveSupportPrimary {
		t.Fatal("missing Cerberus primary ability starter inventory identity")
	}
}

func TestInstallerStarterPackagesPreserveFourInstallerClasses(t *testing.T) {
	packages := InstallerStarterPackages()
	if len(packages) != 4 {
		t.Fatalf("installer starter package count = %d, want 4", len(packages))
	}
	for _, classKey := range []string{"assault", "dreadnought", "sniper", "support"} {
		found := false
		for _, pkg := range packages {
			if pkg.ClassKey == classKey {
				found = true
				if pkg.ShipID == 0 || pkg.LoadoutID == 0 {
					t.Fatalf("%s package missing identity ids: %+v", classKey, pkg)
				}
				if len(pkg.ChunkAssets) < 10 {
					t.Fatalf("%s package should preserve starter chunk assets, got %d", classKey, len(pkg.ChunkAssets))
				}
			}
		}
		if !found {
			t.Fatalf("missing installer starter package %q", classKey)
		}
	}
}

func TestFleetEligibilityPreservesRecruitVeteranLegendary(t *testing.T) {
	legendary, ok := FleetEligibilityByToken("Legendary")
	if !ok {
		t.Fatal("missing Legendary fleet eligibility")
	}
	if legendary.FleetType != 3 {
		t.Fatalf("Legendary fleet type = %d, want 3", legendary.FleetType)
	}
	if legendary.MaintenanceTime != 1200 {
		t.Fatalf("Legendary maintenance time = %d, want 1200", legendary.MaintenanceTime)
	}
	if len(legendary.AllowedTiers) != 2 || legendary.AllowedTiers[0] != 4 || legendary.AllowedTiers[1] != 5 {
		t.Fatalf("Legendary allowed tiers = %v, want [4 5]", legendary.AllowedTiers)
	}
}

func TestProgressionTaxonomiesExposeStarterAndUnlockContainers(t *testing.T) {
	var haveLoadouts bool
	var haveShips bool
	var haveUnlockCurrency bool
	for _, taxonomy := range ProgressionTaxonomies() {
		switch taxonomy.TableCategory {
		case "YShipLoadoutPrecast":
			haveLoadouts = len(taxonomy.AssetRoots) == 1 && taxonomy.AssetRoots[0] == "/Game/Generic/Loadouts/Precast"
		case "YPawn":
			haveShips = len(taxonomy.AssetRoots) == 2 && taxonomy.AssetRoots[0] == "/Game/Generic/Ships"
		case progressionTableCategoryUnlockCurrency:
			haveUnlockCurrency = len(taxonomy.AssetRoots) == 1 && taxonomy.AssetRoots[0] == "/Game/DevGroup/Meta/UnlockContainer/Currency"
		}
	}
	if !haveLoadouts || !haveShips || !haveUnlockCurrency {
		t.Fatalf("progression taxonomies missing starter surfaces: loadouts=%v ships=%v unlockCurrency=%v", haveLoadouts, haveShips, haveUnlockCurrency)
	}

	surfaces := UnlockContainerExportSurfaces()
	if len(surfaces) != 2 {
		t.Fatalf("unlock export surface count = %d, want 2", len(surfaces))
	}
	for _, surface := range surfaces {
		if surface.ExportPath != unlockContainerTablesExportPath {
			t.Fatalf("unexpected export path %q", surface.ExportPath)
		}
	}
}

func TestItemLookupReturnsSharedMetadata(t *testing.T) {
	item, ok := ItemByID(117374979)
	if !ok {
		t.Fatal("missing Communications 101 metadata")
	}
	if item.CatalogBucket != CatalogBucketPerks {
		t.Fatalf("perk bucket = %q, want %q", item.CatalogBucket, CatalogBucketPerks)
	}
	if item.AssetPath != "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP" {
		t.Fatalf("perk asset path = %q", item.AssetPath)
	}

	ship, ok := ItemByTypeAndDisplayName(ItemTypeShip, "Athos")
	if !ok {
		t.Fatal("missing Athos ship metadata")
	}
	if ship.ItemID != 184484177 {
		t.Fatalf("Athos ship id = %d, want 184484177", ship.ItemID)
	}

	ability, ok := ItemByAssetPath("/Game/Generic/Abilities/Sniper/Int_Cloak_Static/T0/AB_SN_Int_Cloak_Static_Ability_T0_BP")
	if !ok {
		t.Fatal("missing Rurik internal ability asset path metadata")
	}
	if ability.ItemID != 83820764 {
		t.Fatalf("Rurik internal ability id = %d, want 83820764", ability.ItemID)
	}
}
