package dreadgameconfig

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
)

// DynamicItemCatalog represents the item catalog built from loaded asset tables
// This replaces the hardcoded itemCatalog with data from loaded tables
type DynamicItemCatalog struct {
	ItemsByID          map[int32]ItemMetadata
	ItemsByAssetPath   map[string]ItemMetadata
	ItemsByTypeAndName map[string]ItemMetadata
	AllItems           []ItemMetadata
}

var (
	dynamicItemCatalog     *DynamicItemCatalog
	dynamicItemCatalogOnce sync.Once
	dynamicItemCatalogMu   sync.RWMutex
)

// BuildDynamicItemCatalog builds the item catalog from loaded asset tables
// This replaces the hardcoded itemCatalog with data from:
// - ItemIDTable: for category information
// - ItemIDRegister: for itemID->assetPath mappings
// - CatalogIDTable: for catalog bucket information
// - ItemIDConversionTable: for item ID conversions
func BuildDynamicItemCatalog() *DynamicItemCatalog {
	dynamicItemCatalogOnce.Do(func() {
		// Ensure all required tables are loaded
		LoadItemIDTable()
		LoadItemIDRegister()
		LoadCatalogIDTable()
		LoadItemIDConversionTable()

		// Build the dynamic catalog
		catalog := &DynamicItemCatalog{
			ItemsByID:          make(map[int32]ItemMetadata),
			ItemsByAssetPath:   make(map[string]ItemMetadata),
			ItemsByTypeAndName: make(map[string]ItemMetadata),
			AllItems:           make([]ItemMetadata, 0),
		}

		// Get all item IDs and their categories from ItemIDTable
		allCategories := GetAllCategories()

		// Get all item registry entries for asset paths
		allRegistryEntries := GetAllRegistryEntries()

		// Create a map of itemID -> category for quick lookup
		itemIDToCategory := make(map[int32]string)
		itemIDToCategoryName := make(map[int32]string)
		for _, category := range allCategories {
			for _, itemID := range category.ItemIDs {
				itemIDToCategory[int32(itemID)] = category.CategoryName
				itemIDToCategoryName[int32(itemID)] = category.CategoryName
			}
		}

		// Create a map of itemID -> asset path
		itemIDToAssetPath := make(map[int32]string)
		for _, entry := range allRegistryEntries {
			itemIDToAssetPath[int32(entry.ItemID)] = entry.Path
		}

		// Create a map of itemID -> catalog bucket
		itemIDToCatalogBucket := make(map[int32]string)
		allBuckets := GetAllCatalogBuckets()
		for _, bucket := range allBuckets {
			for _, itemIDInterface := range bucket.ItemIDs {
				// Handle CatalogItemID types
				var itemID int32
				switch v := itemIDInterface.Value.(type) {
				case int64:
					if v <= 0x7FFFFFFF { // fits in int32
						itemID = int32(v)
					} else {
						continue // skip IDs that don't fit in int32
					}
				case int32:
					itemID = v
				case int:
					if v <= 0x7FFFFFFF {
						itemID = int32(v)
					} else {
						continue
					}
				default:
					continue // skip string IDs and other types
				}
				itemIDToCatalogBucket[itemID] = bucket.BucketName
			}
		}

		// Build items from ItemIDRegister entries that have valid categories
		for _, entry := range allRegistryEntries {
			itemID := int32(entry.ItemID)

			// Skip if item ID is invalid
			if itemID <= 0 {
				continue
			}

			// Get category for this item ID
			categoryName, hasCategory := itemIDToCategory[itemID]
			if !hasCategory {
				// Try to determine category from asset path
				categoryName = determineCategoryFromAssetPath(entry.Path)
				if categoryName == "" {
					continue // Skip items without category
				}
			}

			// Determine item type from category
			itemType := determineItemTypeFromCategory(categoryName)

			// Determine table category
			tableCategory := determineTableCategoryFromItemType(itemType)

			// Get catalog bucket
			catalogBucket := itemIDToCatalogBucket[itemID]
			if catalogBucket == "" {
				// Try to determine from category
				catalogBucket = determineCatalogBucketFromCategory(categoryName)
			}

			// Extract display name from asset path
			displayName := extractDisplayNameFromAssetPath(entry.Path)

			// Create the item metadata
			item := ItemMetadata{
				ItemID:        itemID,
				DisplayName:   displayName,
				ItemType:      itemType,
				TableCategory: tableCategory,
				CatalogBucket: catalogBucket,
				AssetPath:     entry.Path,
			}

			// Add to catalog
			catalog.ItemsByID[itemID] = item
			catalog.ItemsByAssetPath[entry.Path] = item

			// Create key for type+name lookup (lowercase for consistency)
			typeNameKey := fmt.Sprintf("%s_%s", strings.ToLower(itemType), strings.ToLower(displayName))
			catalog.ItemsByTypeAndName[typeNameKey] = item

			catalog.AllItems = append(catalog.AllItems, item)
		}

		// Also include items from the hardcoded catalog that might not be in the registry
		// This ensures backward compatibility
		addHardcodedFallbackItems(catalog)

		dynamicItemCatalog = catalog

		log.Printf("Built dynamic item catalog with %d items from loaded asset tables", len(catalog.AllItems))
	})

	return dynamicItemCatalog
}

// GetDynamicItemCatalog returns the dynamically built item catalog
func GetDynamicItemCatalog() *DynamicItemCatalog {
	return BuildDynamicItemCatalog()
}

// GetDynamicItemByID returns an item by its ID from the dynamic catalog
func GetDynamicItemByID(itemID int32) (ItemMetadata, bool) {
	catalog := BuildDynamicItemCatalog()
	dynamicItemCatalogMu.RLock()
	defer dynamicItemCatalogMu.RUnlock()

	item, exists := catalog.ItemsByID[itemID]
	return item, exists
}

// GetDynamicItemByAssetPath returns an item by its asset path from the dynamic catalog
func GetDynamicItemByAssetPath(assetPath string) (ItemMetadata, bool) {
	catalog := BuildDynamicItemCatalog()
	dynamicItemCatalogMu.RLock()
	defer dynamicItemCatalogMu.RUnlock()

	item, exists := catalog.ItemsByAssetPath[assetPath]
	return item, exists
}

// GetDynamicItemByTypeAndName returns an item by its type and name from the dynamic catalog
func GetDynamicItemByTypeAndName(itemType, displayName string) (ItemMetadata, bool) {
	catalog := BuildDynamicItemCatalog()
	dynamicItemCatalogMu.RLock()
	defer dynamicItemCatalogMu.RUnlock()

	typeNameKey := fmt.Sprintf("%s_%s", strings.ToLower(itemType), strings.ToLower(displayName))
	item, exists := catalog.ItemsByTypeAndName[typeNameKey]
	return item, exists
}

// GetAllDynamicItems returns all items from the dynamic catalog
func GetAllDynamicItems() []ItemMetadata {
	catalog := BuildDynamicItemCatalog()
	dynamicItemCatalogMu.RLock()
	defer dynamicItemCatalogMu.RUnlock()

	// Return a copy
	items := make([]ItemMetadata, len(catalog.AllItems))
	copy(items, catalog.AllItems)
	return items
}

// GetDynamicItemCount returns the number of items in the dynamic catalog
func GetDynamicItemCount() int {
	catalog := BuildDynamicItemCatalog()
	dynamicItemCatalogMu.RLock()
	defer dynamicItemCatalogMu.RUnlock()

	return len(catalog.AllItems)
}

// determineCategoryFromAssetPath attempts to determine the category from an asset path
func determineCategoryFromAssetPath(assetPath string) string {
	lowerPath := strings.ToLower(assetPath)

	if strings.Contains(lowerPath, "/ships/") || strings.Contains(lowerPath, "/pawn") {
		return "YPawn"
	}
	if strings.Contains(lowerPath, "/loadouts/") || strings.Contains(lowerPath, "precast") {
		return "YShipLoadoutPrecast"
	}
	if strings.Contains(lowerPath, "/weapons/") {
		return "YWeapon"
	}
	if strings.Contains(lowerPath, "/abilities/") {
		return "YAbility"
	}
	if strings.Contains(lowerPath, "/perk/") || strings.Contains(lowerPath, "prk_") {
		return "YPerk"
	}

	return ""
}

// determineItemTypeFromCategory maps category names to item types
func determineItemTypeFromCategory(categoryName string) string {
	switch categoryName {
	case "YPawn":
		return ItemTypeShip
	case "YShipLoadoutPrecast":
		return ItemTypeLoadout
	case "YWeapon":
		return ItemTypeWeapon
	case "YAbility":
		return ItemTypeAbility
	case "YPerk":
		return ItemTypePerk
	default:
		// Try to determine from category name
		if strings.Contains(categoryName, "Ship") || strings.Contains(categoryName, "Pawn") {
			return ItemTypeShip
		}
		if strings.Contains(categoryName, "Loadout") || strings.Contains(categoryName, "Precast") {
			return ItemTypeLoadout
		}
		if strings.Contains(categoryName, "Weapon") {
			return ItemTypeWeapon
		}
		if strings.Contains(categoryName, "Ability") {
			return ItemTypeAbility
		}
		if strings.Contains(categoryName, "Perk") {
			return ItemTypePerk
		}
		return ItemTypeShip // default
	}
}

// determineTableCategoryFromItemType maps item types to table categories
func determineTableCategoryFromItemType(itemType string) string {
	switch itemType {
	case ItemTypeShip:
		return TableCategoryShip
	case ItemTypeLoadout:
		return TableCategoryLoadout
	case ItemTypeWeapon:
		return TableCategoryWeapon
	case ItemTypeAbility:
		return TableCategoryAbility
	case ItemTypePerk:
		return TableCategoryPerk
	default:
		return TableCategoryShip
	}
}

// determineCatalogBucketFromCategory maps category names to catalog buckets
func determineCatalogBucketFromCategory(categoryName string) string {
	switch categoryName {
	case "YPawn":
		return CatalogBucketShips
	case "YShipLoadoutPrecast":
		return "Loadouts" // Not in the original constants, but logical
	case "YWeapon":
		return CatalogBucketWeapons
	case "YAbility":
		return CatalogBucketModules // Abilities are in Modules bucket
	case "YPerk":
		return CatalogBucketPerks
	default:
		return ""
	}
}

// knownShipNames maps asset paths to known display names
var knownShipNames = map[string]string{
	"/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP":         "Athos",
	"/Game/Generic/Ships/Dreadnought/Medium/VH_DreadM_Pawn_BP":       "Zmey",
	"/Game/Generic/Ships/Support/Medium/VH_SupportM_Pawn_BP":         "Aion",
	"/Game/Generic/Ships/Scout/Light/VH_ScoutL_Pawn_BP":              "Valcour",
	"/Game/Generic/Ships/Sniper/Medium/VH_SniperM_Pawn_BP":           "Svarog",
	"/Game/Generic/Ships/Assault/Medium/T2/VH_AssaultM_Pawn_T2_BP":   "Trafalgar",
	"/Game/Generic/Ships/Dreadnought/Medium/T2/VH_DreadM_Pawn_T2_BP": "Nav",
	"/Game/Generic/Ships/Support/Medium/T3/VH_SupportM_Pawn_T3_BP":   "Ceres",
	"/Game/Generic/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP":   "Agosta",
	"/Game/Generic/Ships/Dreadnought/Medium/T1/VH_DreadM_Pawn_T1_BP": "Simargl",
	"/Game/Generic/Ships/Sniper/Medium/T1/VH_SniperM_Pawn_T1_BP":     "Rurik",
	"/Game/Generic/Ships/Support/Medium/T1/VH_SupportM_Pawn_T1_BP":   "Cerberus",
}

// knownLoadoutNames maps asset paths to known display names
var knownLoadoutNames = map[string]string{
	"/Game/Generic/Loadouts/Precast/VH_AssaultMedium_PrecastLoadout_BP":           "Athos",
	"/Game/Generic/Loadouts/Precast/VH_DreadnoughtMedium_PrecastLoadout_BP":       "Zmey",
	"/Game/Generic/Loadouts/Precast/VH_SupportMedium_PrecastLoadout_BP":           "Aion",
	"/Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP":     "Agosta",
	"/Game/Generic/Loadouts/Precast/T1/VH_DreadnoughtMedium_T1_PrecastLoadout_BP": "Simargl",
	"/Game/Generic/Loadouts/Precast/T1/VH_SniperMedium_T1_PrecastLoadout_BP":      "Rurik",
	"/Game/Generic/Loadouts/Precast/T1/VH_SupportMedium_T1_PrecastLoadout_BP":     "Cerberus",
}

// knownWeaponNames maps asset paths to known display names
var knownWeaponNames = map[string]string{
	"/Game/Generic/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP":           "Repeater Turrets",
	"/Game/Generic/Weapons/Assault/SecShort/BP/T0/WP_AssaultSecShort01_weapon01_T0_BP":     "Flak Turrets",
	"/Game/Generic/Weapons/Dreadnought/Medium/BP/T1/WP_DreadnoughtMPri01_weapon01_T1_BP":   "Heavy Plasma Cannons",
	"/Game/Generic/Weapons/Dreadnought/SecMid/BP/T0/WP_DreadnoughtSecMid01_weapon01_T0_BP": "Repeater Guns",
	"/Game/Generic/Weapons/Sniper/Medium/BP/T1/WP_SniperMPri01_weapon01_T1_BP":             "Heavy Tesla Cannon",
	"/Game/Generic/Weapons/Sniper/SecShort/BP/T0/WP_SniperSecShort01_weapon01_T0_BP":       "Light Flak Turrets",
	"/Game/Generic/Weapons/Support/Medium/BP/T1/WP_SupportMPri01_weapon01_T1_BP":           "Medium Beam Turrets",
	"/Game/Generic/Weapons/Support/SecShort/BP/T0/WP_SupportSecShort01_weapon01_T0_BP":     "Tesla Turrets",
	"/Game/Generic/Weapons/Support/Heavy/BP/WP_SupportHPri01_weapon01_BP":                  "Heavy Repair Beam",
	"/Game/Generic/Weapons/Support/SecMid/BP/WP_SupportSecMid01_weapon01_BP":               "Light Machine Guns",
}

// knownAbilityNames maps asset paths to known display names
var knownAbilityNames = map[string]string{
	"/Game/Generic/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP":          "Tempest Missiles",
	"/Game/Generic/Abilities/Assault/Sec_TorpedoM_Dmg/T0/AB_AS_Sec_TrpM_Ability_T0_BP":                    "Torpedo Salvo",
	"/Game/Generic/Abilities/Assault/Per_Turret_Off/T0/AB_AS_Per_Tur_Off_Ability_T0_BP":                   "Protean Autoguns",
	"/Game/Generic/Abilities/Assault/Int_Buff_AbInc/T0/AB_AS_Int_Buff_AbInc_Ability_T0_BP":                "Module Reboot",
	"/Game/Generic/Abilities/Assault/Sec_Missile_FireDec/AB_AS_Sec_Msl_PwrDec_Ability_BP":                 "Weaponbreaker Missile",
	"/Game/Generic/Abilities/Assault/Per_Turret_DefH/AB_AS_Per_Tur_DefH_Ability_BP":                       "Hell Lasers",
	"/Game/Generic/Abilities/Assault/Int_Warp/AB_AS_Int_Warp_Ability":                                     "Jump Drive",
	"/Game/Generic/Abilities/Dreadnought/Pri_BS_Plasma/T0/AB_DN_Pri_BS_Plasma_Ability_T0_BP":              "Plasma Broadside",
	"/Game/Generic/Abilities/Dreadnought/Sec_MissileH_Dmg/T0/AB_DN_Sec_MslH_Dmg_Ability_T0_BP":            "Vulture Missiles",
	"/Game/Generic/Abilities/Dreadnought/Per_Turret_Def/T0/AB_DN_Per_Tur_Def_Ability_T0_BP":               "Flyswatter AML",
	"/Game/Generic/Abilities/Dreadnought/Int_Warp/T0/AB_DN_Int_Warp_Ability_T0_BP":                        "Warp Jump",
	"/Game/Generic/Abilities/Support/Pri_Drone_HealEsc/AB_SU_Pri_Drone_HealEsc_Ability_BP":                "Repair Drones",
	"/Game/Generic/Abilities/Support/Pri_BeamAmp_Dmg/T0/AB_SU_Pri_BeamAmp_Dmg_Ability_T0_BP":              "Beam Amplifier",
	"/Game/Generic/Abilities/Support/Sec_Depl_Heal/T0/AB_SU_Sec_Depl_HPInc_Ability_T0_BP":                 "Repair Pod",
	"/Game/Generic/Abilities/Support/Per_Turret_Heal/T0/AB_SU_Per_Tur_HPInc_Ability_T0_BP":                "Repair Autobeams",
	"/Game/Generic/Abilities/Support/Int_HealthIncrease/T0/AB_SU_Int_HPinc_Ability_T0_BP":                 "Autorepair",
	"/Game/Generic/Abilities/Sniper/Pri_FireMode_Artillery/T0/AB_SN_Pri_Firemode_Artillery_Ability_T0_BP": "Siege Mode",
	"/Game/Generic/Abilities/Sniper/Sec_MissileL_Dmg/T0/AB_SN_Sec_MslL_Dmg_Ability_T0_BP":                 "Flechette Missiles",
	"/Game/Generic/Abilities/Sniper/Per_Turret_DefL/T0/AB_SN_Per_Tur_DefL_Ability_T0_BP":                  "Anti-Missile Lasers",
	"/Game/Generic/Abilities/Sniper/Int_Cloak_Static/T0/AB_SN_Int_Cloak_Static_Ability_T0_BP":             "Stationary Cloak",
}

// knownPerkNames maps asset paths to known display names
var knownPerkNames = map[string]string{
	"/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP":          "Communications 101",
	"/Game/Generic/Officer/Perk/PRK_WPN_RldInc_HPLow_BP":            "Survival Instinct",
	"/Game/Generic/Officer/Perk/PRK_NAV_SpdInc_Passive_BP":          "Navigation 101",
	"/Game/Generic/Officer/Perk/PRK_ENG_DmgResInc_Passive_BP":       "Engineering 101",
	"/Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP":          "Module Recycler",
	"/Game/Generic/Officer/Perk/PRK_WPN_AbiDmgInc_EWWeapon_BP":      "Module Amper",
	"/Game/Generic/Officer/Perk/PRK_NAV_EnInc_EWThruster_BP":        "Navigation Expert",
	"/Game/Generic/Officer/Perk/PRK_ENG_HPInc_EWOff_BP":             "Mr. Fixit",
	"/Game/Generic/Officer/Perk/PRK_COM_EnInc_AbiUse_BP":            "Feedback Loop",
	"/Game/Generic/Officer/Perk/PRK_WPN_DmgInc_HPHigh_BP":           "Glass Cannon",
	"/Game/Generic/Officer/Perk/PRK_NAV_DmgResIncSpdDec_Passive_BP": "Slow and Steady",
	"/Game/Generic/Officer/Perk/PRK_ENG_HPIncAbiDec_Passive_BP":     "Reinforced",
}

// extractDisplayNameFromAssetPath extracts a display name from an asset path
func extractDisplayNameFromAssetPath(assetPath string) string {
	if assetPath == "" {
		return "Unknown"
	}

	// The client's own conversion table wins over any table maintained here --
	// that is what keeps an invented name (see authoritative_names.go) from
	// reaching the player.
	if displayName, ok := AuthoritativeNameForAssetPath(assetPath); ok {
		return displayName
	}

	// Check if this is a known item with a specific display name
	if displayName, exists := knownShipNames[assetPath]; exists {
		return displayName
	}
	if displayName, exists := knownLoadoutNames[assetPath]; exists {
		return displayName
	}
	if displayName, exists := knownWeaponNames[assetPath]; exists {
		return displayName
	}
	if displayName, exists := knownAbilityNames[assetPath]; exists {
		return displayName
	}
	if displayName, exists := knownPerkNames[assetPath]; exists {
		return displayName
	}

	// Default extraction for unknown items
	// Remove the leading "/Game/Generic/" part
	name := strings.TrimPrefix(assetPath, "/Game/Generic/")

	// Remove common prefixes
	prefixes := []string{
		"Ships/",
		"Loadouts/",
		"Weapons/",
		"Abilities/",
		"Officer/Perk/",
		"VanityItems/",
	}

	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}

	// Remove file extensions and suffixes
	name = strings.TrimSuffix(name, "_BP")
	name = strings.TrimSuffix(name, ".Default__")
	name = strings.TrimSuffix(name, "_C")
	name = strings.TrimSuffix(name, "_DA")

	// Remove path separators and underscores
	name = strings.ReplaceAll(name, "/", " ")
	name = strings.ReplaceAll(name, "_", " ")

	// Clean up multiple spaces
	for strings.Contains(name, "  ") {
		name = strings.ReplaceAll(name, "  ", " ")
	}

	// Trim and capitalize
	name = strings.TrimSpace(name)
	if name != "" {
		// Convert to title case
		parts := strings.Split(name, " ")
		for i, part := range parts {
			if len(part) > 0 {
				parts[i] = strings.ToUpper(string(part[0])) + strings.ToLower(part[1:])
			}
		}
		name = strings.Join(parts, " ")
	}

	// Fallback to original filename if we ended up with something too short
	if len(name) <= 2 {
		// Extract the last part of the path
		base := filepath.Base(assetPath)
		base = strings.TrimSuffix(base, "_BP")
		base = strings.TrimSuffix(base, ".Default__")
		base = strings.TrimSuffix(base, "_C")
		base = strings.TrimSuffix(base, "_DA")
		base = strings.ReplaceAll(base, "_", " ")

		// Convert to title case
		parts := strings.Split(base, " ")
		for i, part := range parts {
			if len(part) > 0 {
				parts[i] = strings.ToUpper(string(part[0])) + strings.ToLower(part[1:])
			}
		}
		name = strings.Join(parts, " ")
	}

	return name
}

// addHardcodedFallbackItems adds items from the original hardcoded catalog
// that might not be found in the loaded tables, ensuring backward compatibility
func addHardcodedFallbackItems(catalog *DynamicItemCatalog) {
	// List of hardcoded items that should be preserved for backward compatibility
	hardcodedItems := []ItemMetadata{
		// Ships
		{ItemID: 184484177, DisplayName: "Athos", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP"},
		{ItemID: 184484173, DisplayName: "Zmey", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Dreadnought/Medium/VH_DreadM_Pawn_BP"},
		{ItemID: 184484171, DisplayName: "Aion", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Support/Medium/VH_SupportM_Pawn_BP"},
		{ItemID: 184484180, DisplayName: "Valcour", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Scout/Light/VH_ScoutL_Pawn_BP"},
		{ItemID: 184484184, DisplayName: "Svarog", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Sniper/Medium/VH_SniperM_Pawn_BP"},
		{ItemID: 184483981, DisplayName: "Trafalgar", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Assault/Medium/T2/VH_AssaultM_Pawn_T2_BP"},
		{ItemID: 184483972, DisplayName: "Nav", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Dreadnought/Medium/T2/VH_DreadM_Pawn_T2_BP"},
		{ItemID: 184484148, DisplayName: "Ceres", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Support/Medium/T3/VH_SupportM_Pawn_T3_BP"},

		// Loadouts
		{ItemID: 33489315, DisplayName: "Athos", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_AssaultMedium_PrecastLoadout_BP"},
		{ItemID: 33489318, DisplayName: "Zmey", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_DreadnoughtMedium_PrecastLoadout_BP"},
		{ItemID: 33489331, DisplayName: "Aion", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_SupportMedium_PrecastLoadout_BP"},

		// Weapons
		{ItemID: 100597772, DisplayName: "Repeater Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP"},
		{ItemID: 100598563, DisplayName: "Flak Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Assault/SecShort/BP/T0/WP_AssaultSecShort01_weapon01_T0_BP"},

		// Abilities
		{ItemID: 83820574, DisplayName: "Tempest Missiles", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP"},
		{ItemID: 83820606, DisplayName: "Torpedo Salvo", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Sec_TorpedoM_Dmg/T0/AB_AS_Sec_TrpM_Ability_T0_BP"},

		// Perks
		{ItemID: 117374979, DisplayName: "Communications 101", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP"},
		{ItemID: 117374997, DisplayName: "Survival Instinct", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_WPN_RldInc_HPLow_BP"},
	}

	// Add hardcoded items if they don't already exist
	for _, item := range hardcodedItems {
		if _, exists := catalog.ItemsByID[item.ItemID]; !exists {
			catalog.ItemsByID[item.ItemID] = item
			catalog.ItemsByAssetPath[item.AssetPath] = item
			typeNameKey := fmt.Sprintf("%s_%s", strings.ToLower(item.ItemType), strings.ToLower(item.DisplayName))
			catalog.ItemsByTypeAndName[typeNameKey] = item
			catalog.AllItems = append(catalog.AllItems, item)
		}
	}
}
