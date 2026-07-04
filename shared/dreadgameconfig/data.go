package dreadgameconfig

import (
	"fmt"
	"log"
	"strings"
)

const (
	ItemTypeShip    = "ship"
	ItemTypeLoadout = "loadout"
	ItemTypeWeapon  = "weapon"
	ItemTypeAbility = "ability"
	ItemTypePerk    = "perk"

	starterShipDisplayNameAssaultMediumT1     = "Assault Medium T1"
	starterShipDisplayNameDreadnoughtMediumT1 = "Dreadnought Medium T1"
	starterShipDisplayNameSniperMediumT1      = "Sniper Medium T1"
	starterShipDisplayNameSupportMediumT1     = "Support Medium T1"
	sharedVanityDefaultDecalAssetPath         = "/VanityItems/_Shared/Decal/VAN_DCL_Default_DA"
	sharedVanityDefaultEmblemAssetPath        = "/VanityItems/_Shared/Emblems/Default/VAN_EMB_Default_DA"
	progressionTableCategoryUnlockCurrency    = "YProgressionUnlockContainerCurrency"
	sharedCharacterVanityAssetRoot            = "/Game/Generic/VanityItems/_Shared/Characters"
	unlockContainerTablesExportPath           = "Source/Programs/mmogbrain/instances/dreadnought/UnlockContainerTables.cfg"

	SlotWeaponPrimary    = "weaponPrimary"
	SlotWeaponSecondary  = "weaponSecondary"
	SlotAbilityPrimary   = "abilityPrimary"
	SlotAbilitySecondary = "abilitySecondary"
	SlotAbilityPerimeter = "abilityPerimeter"
	SlotAbilityInternal  = "abilityInternal"
	SlotPerkCom          = "perkCom"
	SlotPerkWeapon       = "perkWeapon"
	SlotPerkNavigation   = "perkNavigation"
	SlotPerkEngineer     = "perkEngineer"

	TableCategoryShip    = "YPawn"
	TableCategoryLoadout = "YShipLoadoutPrecast"
	TableCategoryWeapon  = "YWeapon"
	TableCategoryAbility = "YAbility"
	TableCategoryPerk    = "YPerk"

	CatalogBucketShips   = "Heroships"
	CatalogBucketWeapons = "Weapons"
	CatalogBucketModules = "Modules"
	CatalogBucketPerks   = "Officer Briefings"
)

type ItemMetadata struct {
	ItemID        int32
	DisplayName   string
	ItemType      string
	TableCategory string
	CatalogBucket string
	AssetPath     string
}

type LoadoutSlot struct {
	SlotName string
	ItemID   int32
}

type StarterLoadout struct {
	ShipName  string
	ShipID    int32
	LoadoutID int32
	Slots     []LoadoutSlot
}

type StarterInventoryItem struct {
	Item      ItemMetadata
	ShipName  string
	ShipID    int32
	LoadoutID int32
	SlotName  string
}

type InstallerStarterPackage struct {
	ClassKey    string
	DisplayName string
	ShipID      int32
	LoadoutID   int32
	ChunkAssets []string
}

type FleetEligibility struct {
	Token                 string
	DisplayName           string
	FleetType             int32
	AllowedTiers          []int32
	NumShipsToUnlockFleet int32
	BaseMaintenanceCost   int32
	MaintenanceTime       int32
	FleetRatingMin        float64
	FleetRatingCost       int32
}

type ProgressionTaxonomy struct {
	TableCategory string
	CategoryID    int32
	AssetRoots    []string
}

type ExportSurface struct {
	TableCategory string
	ExportPath    string
}

var itemCatalog = []ItemMetadata{
	{ItemID: 184484177, DisplayName: "Athos", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP"},
	{ItemID: 184484173, DisplayName: "Zmey", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Dreadnought/Medium/VH_DreadM_Pawn_BP"},
	{ItemID: 184484171, DisplayName: "Aion", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Support/Medium/VH_SupportM_Pawn_BP"},
	{ItemID: 184484180, DisplayName: "Valcour", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Scout/Light/VH_ScoutL_Pawn_BP"},
	{ItemID: 184484184, DisplayName: "Svarog", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Sniper/Medium/VH_SniperM_Pawn_BP"},
	{ItemID: 184483981, DisplayName: "Leipzig", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Assault/Medium/T2/VH_AssaultM_Pawn_T2_BP"},
	{ItemID: 184483972, DisplayName: "Trieste", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Dreadnought/Medium/T2/VH_DreadM_Pawn_T2_BP"},
	{ItemID: 184484148, DisplayName: "Ceres", ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Support/Medium/T3/VH_SupportM_Pawn_T3_BP"},
	{ItemID: 184483982, DisplayName: starterShipDisplayNameAssaultMediumT1, ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP"},
	{ItemID: 184484170, DisplayName: starterShipDisplayNameDreadnoughtMediumT1, ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Dreadnought/Medium/T1/VH_DreadM_Pawn_T1_BP"},
	{ItemID: 184483950, DisplayName: starterShipDisplayNameSniperMediumT1, ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Sniper/Medium/T1/VH_SniperM_Pawn_T1_BP"},
	{ItemID: 184484202, DisplayName: starterShipDisplayNameSupportMediumT1, ItemType: ItemTypeShip, TableCategory: TableCategoryShip, CatalogBucket: CatalogBucketShips, AssetPath: "/Game/Generic/Ships/Support/Medium/T1/VH_SupportM_Pawn_T1_BP"},

	{ItemID: 33489315, DisplayName: "Athos", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_AssaultMedium_PrecastLoadout_BP"},
	{ItemID: 33489318, DisplayName: "Zmey", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_DreadnoughtMedium_PrecastLoadout_BP"},
	{ItemID: 33489331, DisplayName: "Aion", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/VH_SupportMedium_PrecastLoadout_BP"},
	{ItemID: 33489262, DisplayName: "Agosta", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP"},
	{ItemID: 33489423, DisplayName: "Simargl", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/T1/VH_DreadnoughtMedium_T1_PrecastLoadout_BP"},
	{ItemID: 33489263, DisplayName: "Rurik", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/T1/VH_SniperMedium_T1_PrecastLoadout_BP"},
	{ItemID: 33489264, DisplayName: "Cerberus", ItemType: ItemTypeLoadout, TableCategory: TableCategoryLoadout, AssetPath: "/Game/Generic/Loadouts/Precast/T1/VH_SupportMedium_T1_PrecastLoadout_BP"},

	{ItemID: 100597772, DisplayName: "Repeater Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP"},
	{ItemID: 100598563, DisplayName: "Flak Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Assault/SecShort/BP/T0/WP_AssaultSecShort01_weapon01_T0_BP"},
	{ItemID: 100598595, DisplayName: "Heavy Plasma Cannons", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Dreadnought/Medium/BP/T1/WP_DreadnoughtMPri01_weapon01_T1_BP"},
	{ItemID: 100598596, DisplayName: "Repeater Guns", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Dreadnought/SecMid/BP/T0/WP_DreadnoughtSecMid01_weapon01_T0_BP"},
	{ItemID: 100597987, DisplayName: "Heavy Tesla Cannon", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Sniper/Medium/BP/T1/WP_SniperMPri01_weapon01_T1_BP"},
	{ItemID: 100598570, DisplayName: "Light Flak Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Sniper/SecShort/BP/T0/WP_SniperSecShort01_weapon01_T0_BP"},
	{ItemID: 100597870, DisplayName: "Medium Beam Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Support/Medium/BP/T1/WP_SupportMPri01_weapon01_T1_BP"},
	{ItemID: 100598573, DisplayName: "Tesla Turrets", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Support/SecShort/BP/T0/WP_SupportSecShort01_weapon01_T0_BP"},
	{ItemID: 100597862, DisplayName: "Heavy Repair Beam", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Support/Heavy/BP/WP_SupportHPri01_weapon01_BP"},
	{ItemID: 100597877, DisplayName: "Light Machine Guns", ItemType: ItemTypeWeapon, TableCategory: TableCategoryWeapon, CatalogBucket: CatalogBucketWeapons, AssetPath: "/Game/Generic/Weapons/Support/SecMid/BP/WP_SupportSecMid01_weapon01_BP"},

	{ItemID: 83820574, DisplayName: "Tempest Missiles", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP"},
	{ItemID: 83820606, DisplayName: "Torpedo Salvo", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Sec_TorpedoM_Dmg/T0/AB_AS_Sec_TrpM_Ability_T0_BP"},
	{ItemID: 83820565, DisplayName: "Protean Autoguns", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Per_Turret_Off/T0/AB_AS_Per_Tur_Off_Ability_T0_BP"},
	{ItemID: 83820550, DisplayName: "Module Reboot", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Int_Buff_AbInc/T0/AB_AS_Int_Buff_AbInc_Ability_T0_BP"},
	{ItemID: 83820594, DisplayName: "Weaponbreaker Missile", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Sec_Missile_FireDec/AB_AS_Sec_Msl_PwrDec_Ability_BP"},
	{ItemID: 83820560, DisplayName: "Hell Lasers", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Per_Turret_DefH/AB_AS_Per_Tur_DefH_Ability_BP"},
	{ItemID: 83820556, DisplayName: "Jump Drive", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Assault/Int_Warp/AB_AS_Int_Warp_Ability"},
	{ItemID: 83821082, DisplayName: "Plasma Broadside", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Dreadnought/Pri_BS_Plasma/T0/AB_DN_Pri_BS_Plasma_Ability_T0_BP"},
	{ItemID: 83825291, DisplayName: "Vulture Missiles", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Dreadnought/Sec_MissileH_Dmg/T0/AB_DN_Sec_MslH_Dmg_Ability_T0_BP"},
	{ItemID: 83821084, DisplayName: "Flyswatter AML", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Dreadnought/Per_Turret_Def/T0/AB_DN_Per_Tur_Def_Ability_T0_BP"},
	{ItemID: 83821076, DisplayName: "Warp Jump", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Dreadnought/Int_Warp/T0/AB_DN_Int_Warp_Ability_T0_BP"},
	{ItemID: 83820879, DisplayName: "Repair Drones", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Support/Pri_Drone_HealEsc/AB_SU_Pri_Drone_HealEsc_Ability_BP"},
	{ItemID: 83820857, DisplayName: "Beam Amplifier", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Support/Pri_BeamAmp_Dmg/T0/AB_SU_Pri_BeamAmp_Dmg_Ability_T0_BP"},
	{ItemID: 83820882, DisplayName: "Repair Pod", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Support/Sec_Depl_Heal/T0/AB_SU_Sec_Depl_HPInc_Ability_T0_BP"},
	{ItemID: 83820851, DisplayName: "Repair Autobeams", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Support/Per_Turret_Heal/T0/AB_SU_Per_Tur_HPInc_Ability_T0_BP"},
	{ItemID: 83820839, DisplayName: "Autorepair", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Support/Int_HealthIncrease/T0/AB_SU_Int_HPinc_Ability_T0_BP"},
	{ItemID: 83820799, DisplayName: "Siege Mode", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Sniper/Pri_FireMode_Artillery/T0/AB_SN_Pri_Firemode_Artillery_Ability_T0_BP"},
	{ItemID: 83820830, DisplayName: "Flechette Missiles", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Sniper/Sec_MissileL_Dmg/T0/AB_SN_Sec_MslL_Dmg_Ability_T0_BP"},
	{ItemID: 83820781, DisplayName: "Anti-Missile Lasers", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Sniper/Per_Turret_DefL/T0/AB_SN_Per_Tur_DefL_Ability_T0_BP"},
	{ItemID: 83820764, DisplayName: "Stationary Cloak", ItemType: ItemTypeAbility, TableCategory: TableCategoryAbility, CatalogBucket: CatalogBucketModules, AssetPath: "/Game/Generic/Abilities/Sniper/Int_Cloak_Static/T0/AB_SN_Int_Cloak_Static_Ability_T0_BP"},

	{ItemID: 117374979, DisplayName: "Communications 101", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP"},
	{ItemID: 117374997, DisplayName: "Survival Instinct", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_WPN_RldInc_HPLow_BP"},
	{ItemID: 117374991, DisplayName: "Navigation 101", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_NAV_SpdInc_Passive_BP"},
	{ItemID: 117374982, DisplayName: "Engineering 101", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_ENG_DmgResInc_Passive_BP"},
	{ItemID: 117374977, DisplayName: "Module Recycler", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP"},
	{ItemID: 117374993, DisplayName: "Module Amper", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_WPN_AbiDmgInc_EWWeapon_BP"},
	{ItemID: 117374989, DisplayName: "Navigation Expert", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_NAV_EnInc_EWThruster_BP"},
	{ItemID: 117374985, DisplayName: "Mr. Fixit", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_ENG_HPInc_EWOff_BP"},
	{ItemID: 117374980, DisplayName: "Feedback Loop", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_COM_EnInc_AbiUse_BP"},
	{ItemID: 117374994, DisplayName: "Glass Cannon", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_WPN_DmgInc_HPHigh_BP"},
	{ItemID: 117374988, DisplayName: "Slow and Steady", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_NAV_DmgResIncSpdDec_Passive_BP"},
	{ItemID: 117374986, DisplayName: "Reinforced", ItemType: ItemTypePerk, TableCategory: TableCategoryPerk, CatalogBucket: CatalogBucketPerks, AssetPath: "/Game/Generic/Officer/Perk/PRK_ENG_HPIncAbiDec_Passive_BP"},
}

var starterInventoryLoadouts = []StarterLoadout{
	{
		ShipName:  starterShipDisplayNameAssaultMediumT1,
		ShipID:    184483982,
		LoadoutID: 33489262,
		Slots: []LoadoutSlot{
			{SlotName: SlotWeaponPrimary, ItemID: 100597772},
			{SlotName: SlotWeaponSecondary, ItemID: 100598563},
			{SlotName: SlotAbilityPrimary, ItemID: 83820574},
			{SlotName: SlotAbilitySecondary, ItemID: 83820606},
			{SlotName: SlotAbilityPerimeter, ItemID: 83820565},
			{SlotName: SlotAbilityInternal, ItemID: 83820550},
		},
	},
	{
		ShipName:  starterShipDisplayNameDreadnoughtMediumT1,
		ShipID:    184484170,
		LoadoutID: 33489423,
		Slots: []LoadoutSlot{
			{SlotName: SlotWeaponPrimary, ItemID: 100598595},
			{SlotName: SlotWeaponSecondary, ItemID: 100598596},
			{SlotName: SlotAbilityPrimary, ItemID: 83821082},
			{SlotName: SlotAbilitySecondary, ItemID: 83825291},
			{SlotName: SlotAbilityPerimeter, ItemID: 83821084},
			{SlotName: SlotAbilityInternal, ItemID: 83821076},
		},
	},
	{
		ShipName:  starterShipDisplayNameSniperMediumT1,
		ShipID:    184483950,
		LoadoutID: 33489263,
		Slots: []LoadoutSlot{
			{SlotName: SlotWeaponPrimary, ItemID: 100597987},
			{SlotName: SlotWeaponSecondary, ItemID: 100598570},
			{SlotName: SlotAbilityPrimary, ItemID: 83820799},
			{SlotName: SlotAbilitySecondary, ItemID: 83820830},
			{SlotName: SlotAbilityPerimeter, ItemID: 83820781},
			{SlotName: SlotAbilityInternal, ItemID: 83820764},
		},
	},
	{
		ShipName:  starterShipDisplayNameSupportMediumT1,
		ShipID:    184484202,
		LoadoutID: 33489264,
		Slots: []LoadoutSlot{
			{SlotName: SlotWeaponPrimary, ItemID: 100597870},
			{SlotName: SlotWeaponSecondary, ItemID: 100598573},
			{SlotName: SlotAbilityPrimary, ItemID: 83820857},
			{SlotName: SlotAbilitySecondary, ItemID: 83820882},
			{SlotName: SlotAbilityPerimeter, ItemID: 83820851},
			{SlotName: SlotAbilityInternal, ItemID: 83820839},
		},
	},
}

var installerStarterPackages = []InstallerStarterPackage{
	{
		ClassKey:    "assault",
		DisplayName: starterShipDisplayNameAssaultMediumT1,
		ShipID:      184483982,
		LoadoutID:   33489262,
		ChunkAssets: []string{
			"/Abilities/Assault/Int_Buff_AbInc/T0/AB_AS_Int_Buff_AbInc_Ability_T0_BP",
			"/Abilities/Assault/Per_Turret_Off/T0/AB_AS_Per_Tur_Off_Ability_T0_BP",
			"/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP",
			"/Abilities/Assault/Sec_TorpedoM_Dmg/T0/AB_AS_Sec_TrpM_Ability_T0_BP",
			"/UI/ships/Tiers/AssaultM_T1",
			sharedVanityDefaultDecalAssetPath,
			sharedVanityDefaultEmblemAssetPath,
			"/VanityItems/Heroships/Assault/Medium/Default/VAN_H_AssaultM_Bridge_Default_DA",
			"/VanityItems/Heroships/Assault/Medium/Default/VAN_H_AssaultM_Forecastle_Default_DA",
			"/VanityItems/Heroships/Assault/Medium/Default/VAN_H_AssaultM_Hull_Default_DA",
			"/VanityItems/Heroships/Assault/Medium/Default/VAN_H_AssaultM_Stern_Default_DA",
			"/VanityItems/_Shared/Paints/VAN_PN_Manufacturer_Jupiter_DA",
			"/VanityItems/Patterns/Assault/Medium/VAN_PTN_AssaultM_Default_DA",
			"/AI/AIPawns/VH_AssaultM_AI_Pawn_T1_BP",
			"/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP",
			"/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP",
			"/Weapons/Assault/SecShort/BP/T0/WP_AssaultSecShort01_weapon01_T0_BP",
		},
	},
	{
		ClassKey:    "dreadnought",
		DisplayName: starterShipDisplayNameDreadnoughtMediumT1,
		ShipID:      184484170,
		LoadoutID:   33489423,
		ChunkAssets: []string{
			"/Abilities/Dreadnought/Int_Warp/T0/AB_DN_Int_Warp_Ability_T0_BP",
			"/Abilities/Dreadnought/Per_Turret_Def/T0/AB_DN_Per_Tur_Def_Ability_T0_BP",
			"/Abilities/Dreadnought/Pri_BS_Plasma/T0/AB_DN_Pri_BS_Plasma_Ability_T0_BP",
			"/Abilities/Dreadnought/Sec_MissileH_Dmg/T0/AB_DN_Sec_MslH_Dmg_Ability_T0_BP",
			"/UI/ships/Tiers/DreadnoughtM_T2",
			sharedVanityDefaultDecalAssetPath,
			sharedVanityDefaultEmblemAssetPath,
			"/VanityItems/Heroships/Dreadnought/Medium/Default/VAN_H_DreadM_Bridge_Default_DA",
			"/VanityItems/Heroships/Dreadnought/Medium/Default/VAN_H_DreadM_Forecastle_Default_DA",
			"/VanityItems/Heroships/Dreadnought/Medium/Default/VAN_H_DreadM_Hull_Default_DA",
			"/VanityItems/Heroships/Dreadnought/Medium/Default/VAN_H_DreadM_Stern_Default_DA",
			"/VanityItems/_Shared/Paints/VAN_PN_Manufacturer_Akula_Green_DA",
			"/VanityItems/Patterns/Dread/Medium/VAN_PTN_DreadM_Default_DA",
			"/AI/AIPawns/VH_DreadM_AI_Pawn_T1_BP",
			"/Ships/Dreadnought/Medium/T1/VH_DreadM_Pawn_T1_BP",
			"/Weapons/Dreadnought/Medium/BP/T1/WP_DreadnoughtMPri01_weapon01_T1_BP",
			"/Weapons/Dreadnought/SecMid/BP/T0/WP_DreadnoughtSecMid01_weapon01_T0_BP",
		},
	},
	{
		ClassKey:    "sniper",
		DisplayName: starterShipDisplayNameSniperMediumT1,
		ShipID:      184483950,
		LoadoutID:   33489263,
		ChunkAssets: []string{
			"/Abilities/Sniper/Int_Cloak_Static/T0/AB_SN_Int_Cloak_Static_Ability_T0_BP",
			"/Abilities/Sniper/Per_Turret_DefL/T0/AB_SN_Per_Tur_DefL_Ability_T0_BP",
			"/Abilities/Sniper/Pri_FireMode_Artillery/T0/AB_SN_Pri_Firemode_Artillery_Ability_T0_BP",
			"/Abilities/Sniper/Sec_MissileL_Dmg/T0/AB_SN_Sec_MslL_Dmg_Ability_T0_BP",
			"/UI/ships/Tiers/SniperM_T1",
			sharedVanityDefaultDecalAssetPath,
			sharedVanityDefaultEmblemAssetPath,
			"/VanityItems/Heroships/Sniper/Medium/Default/VAN_H_SniperM_Bridge_Default_DA",
			"/VanityItems/Heroships/Sniper/Medium/Default/VAN_H_SniperM_Forecastle_Default_DA",
			"/VanityItems/Heroships/Sniper/Medium/Default/VAN_H_SniperM_Hull_Default_DA",
			"/VanityItems/Heroships/Sniper/Medium/Default/VAN_H_SniperM_Stern_Default_DA",
			"/VanityItems/_Shared/Paints/VAN_PN_Manufacturer_Akula_Green_DA",
			"/VanityItems/Patterns/Sniper/Medium/VAN_PTN_SniperM_Default_DA",
			"/AI/AIPawns/VH_SniperM_AI_Pawn_T1_BP",
			"/Ships/Sniper/Medium/T1/VH_SniperM_Pawn_T1_BP",
			"/Weapons/Sniper/Medium/BP/T1/WP_SniperMPri01_weapon01_T1_BP",
			"/Weapons/Sniper/SecShort/BP/T0/WP_SniperSecShort01_weapon01_T0_BP",
		},
	},
	{
		ClassKey:    "support",
		DisplayName: starterShipDisplayNameSupportMediumT1,
		ShipID:      184484202,
		LoadoutID:   33489264,
		ChunkAssets: []string{
			"/Abilities/Support/Int_HealthIncrease/T0/AB_SU_Int_HPinc_Ability_T0_BP",
			"/Abilities/Support/Per_Turret_Heal/T0/AB_SU_Per_Tur_HPInc_Ability_T0_BP",
			"/Abilities/Support/Pri_BeamAmp_Dmg/T0/AB_SU_Pri_BeamAmp_Dmg_Ability_T0_BP",
			"/Abilities/Support/Sec_Depl_Heal/T0/AB_SU_Sec_Depl_HPInc_Ability_T0_BP",
			"/UI/ships/Tiers/SupportM_T1",
			sharedVanityDefaultDecalAssetPath,
			sharedVanityDefaultEmblemAssetPath,
			"/VanityItems/Heroships/Support/Medium/Default/VAN_H_SupportM_Bridge_Default_DA",
			"/VanityItems/Heroships/Support/Medium/Default/VAN_H_SupportM_Forecastle_Default_DA",
			"/VanityItems/Heroships/Support/Medium/Default/VAN_H_SupportM_Hull_Default_DA",
			"/VanityItems/Heroships/Support/Medium/Default/VAN_H_SupportM_Stern_Default_DA",
			"/VanityItems/_Shared/Paints/VAN_PN_Manufacturer_Ober_DA",
			"/VanityItems/Patterns/Support/Medium/VAN_PTN_SupportM_Default_DA",
			"/AI/AIPawns/VH_SupportM_AI_Pawn_T1_BP",
			"/Ships/Support/Medium/T1/VH_SupportM_Pawn_T1_BP",
			"/Weapons/Support/Medium/BP/T1/WP_SupportMPri01_weapon01_T1_BP",
			"/Weapons/Support/SecShort/BP/T0/WP_SupportSecShort01_weapon01_T0_BP",
		},
	},
}

var progressionTaxonomies = []ProgressionTaxonomy{
	{TableCategory: "YShipLoadoutPrecast", CategoryID: 1, AssetRoots: []string{"/Game/Generic/Loadouts/Precast"}},
	{TableCategory: "YWeapon", CategoryID: 2, AssetRoots: []string{"/Game/Generic/Weapons", "/Game/Generic/Abilities"}},
	{TableCategory: "YPerk", CategoryID: 3, AssetRoots: []string{"/Game/Generic/Officer"}},
	{TableCategory: "YAbility", CategoryID: 4, AssetRoots: []string{"/Game/Generic/Abilities"}},
	{TableCategory: "YPawn", CategoryID: 5, AssetRoots: []string{"/Game/Generic/Ships", "/Game/SP"}},
	{TableCategory: "YShipLoadoutTrader", CategoryID: 6, AssetRoots: []string{"/Game/Generic/Loadouts/Trader"}},
	{TableCategory: "YShipLoadoutHero", CategoryID: 7, AssetRoots: []string{"/Game/Generic/Loadouts/Hero"}},
	{TableCategory: "YShipVanityMeshPart", CategoryID: 20, AssetRoots: []string{"/Game/Generic/VanityItems/Heroships"}},
	{TableCategory: "YShipVanityEmblem", CategoryID: 21, AssetRoots: []string{"/Game/Generic/VanityItems/_Shared/Emblems"}},
	{TableCategory: "YShipVanityPaint", CategoryID: 22, AssetRoots: []string{"/Game/Generic/VanityItems/_Shared/Paints"}},
	{TableCategory: "YShipVanityPattern", CategoryID: 23, AssetRoots: []string{"/Game/Generic/VanityItems/_Shared/Pattern", "/Game/Generic/VanityItems/Patterns"}},
	{TableCategory: "YShipVanityDecal", CategoryID: 24, AssetRoots: []string{"/Game/Generic/VanityItems/_Shared/Decal"}},
	{TableCategory: "YBoosterAssetBase", CategoryID: 30, AssetRoots: []string{"/Game/DevGroup/Meta/Booster", "/Game/DevGroup/Meta/CommonBooster"}},
	{TableCategory: "YGoldMembership", CategoryID: 31, AssetRoots: []string{"/Game/DevGroup/Meta/GoldMembership"}},
	{TableCategory: "YProgressionUnlockContainerBlank", CategoryID: 32, AssetRoots: []string{"/Game/DevGroup/Meta/UnlockContainer/Blank"}},
	{TableCategory: progressionTableCategoryUnlockCurrency, CategoryID: 33, AssetRoots: []string{"/Game/DevGroup/Meta/UnlockContainer/Currency"}},
	{TableCategory: "YProgressionUnlockContainerFunction", CategoryID: 34, AssetRoots: []string{"/Game/DevGroup/Meta/UnlockContainer/Function"}},
	{TableCategory: "YCharacterCustomizationMaterial", CategoryID: 50, AssetRoots: []string{sharedCharacterVanityAssetRoot, "/Game/Generic/Characters"}},
	{TableCategory: "YCharacterCustomizationMesh", CategoryID: 51, AssetRoots: []string{sharedCharacterVanityAssetRoot, "/Game/Generic/Officer/Assets/MeshCustomization", "/Game/Generic/Characters/Attachments"}},
	{TableCategory: "YCharacterCustomizationGender", CategoryID: 52, AssetRoots: []string{sharedCharacterVanityAssetRoot}},
	{TableCategory: "YMenuNavigationItem", CategoryID: 80, AssetRoots: []string{"/Game/Generic/GameModes/Menu/MainMenu"}},
	{TableCategory: "YMenuNavigationSection", CategoryID: 81, AssetRoots: []string{"/Game/Generic/GameModes/Menu/Customization"}},
	{TableCategory: "YMenuNavigationSlotBase", CategoryID: 82, AssetRoots: []string{"/Game/Generic/GameModes/Menu/Customization"}},
	{TableCategory: "YGameMode", CategoryID: 83, AssetRoots: []string{"/Game/Generic/GameModes"}},
}

var unlockContainerExportSurfaces = []ExportSurface{
	{TableCategory: progressionTableCategoryUnlockCurrency, ExportPath: unlockContainerTablesExportPath},
	{TableCategory: "YProgressionUnlockContainerFunction", ExportPath: unlockContainerTablesExportPath},
}

var fleetEligibilityValues = []FleetEligibility{
	{Token: "Recruit", DisplayName: "Recruit Fleet", FleetType: 1, AllowedTiers: []int32{1, 2}, NumShipsToUnlockFleet: 0, BaseMaintenanceCost: 1, MaintenanceTime: 0, FleetRatingMin: 1.0, FleetRatingCost: 1},
	{Token: "Veteran", DisplayName: "Veteran Fleet", FleetType: 2, AllowedTiers: []int32{2, 3}, NumShipsToUnlockFleet: 2, BaseMaintenanceCost: 1000, MaintenanceTime: 600, FleetRatingMin: 2.0, FleetRatingCost: 200},
	{Token: "Legendary", DisplayName: "Legendary Fleet", FleetType: 3, AllowedTiers: []int32{4, 5}, NumShipsToUnlockFleet: 3, BaseMaintenanceCost: 2000, MaintenanceTime: 1200, FleetRatingMin: 4.0, FleetRatingCost: 500},
}

var (
	itemsByID               map[int32]ItemMetadata
	itemsByAssetPath        map[string]ItemMetadata
	itemsByTypeAndName      map[string]ItemMetadata
	starterLoadoutsByShip   map[string]StarterLoadout
	fleetEligibilityByToken map[string]FleetEligibility
)

func init() {
	itemsByID = make(map[int32]ItemMetadata, len(itemCatalog))
	itemsByAssetPath = make(map[string]ItemMetadata, len(itemCatalog))
	itemsByTypeAndName = make(map[string]ItemMetadata, len(itemCatalog))
	for _, item := range itemCatalog {
		if _, exists := itemsByID[item.ItemID]; exists {
			panic(fmt.Sprintf("duplicate dreadgame item id %d", item.ItemID))
		}
		itemsByID[item.ItemID] = item
		if key := assetPathLookupKey(item.AssetPath); key != "" {
			if _, exists := itemsByAssetPath[key]; exists {
				panic(fmt.Sprintf("duplicate dreadgame item asset path %q", item.AssetPath))
			}
			itemsByAssetPath[key] = item
		}
		key := itemLookupKey(item.ItemType, item.DisplayName)
		if _, exists := itemsByTypeAndName[key]; exists {
			panic(fmt.Sprintf("duplicate dreadgame item key %q", key))
		}
		itemsByTypeAndName[key] = item
	}
	
	// Load ship feats data
	if err := LoadShipFeats(); err != nil {
		log.Printf("Warning: Failed to load ship feats: %v", err)
	}

	starterLoadoutsByShip = make(map[string]StarterLoadout, len(starterInventoryLoadouts))
	for _, loadout := range starterInventoryLoadouts {
		starterLoadoutsByShip[strings.ToLower(loadout.ShipName)] = cloneStarterLoadout(loadout)
	}

	fleetEligibilityByToken = make(map[string]FleetEligibility, len(fleetEligibilityValues))
	for _, eligibility := range fleetEligibilityValues {
		fleetEligibilityByToken[strings.ToLower(eligibility.Token)] = cloneFleetEligibility(eligibility)
	}
}

func itemLookupKey(itemType string, displayName string) string {
	return strings.ToLower(strings.TrimSpace(itemType)) + "\x00" + strings.ToLower(strings.TrimSpace(displayName))
}

func assetPathLookupKey(assetPath string) string {
	return strings.ToLower(strings.TrimSpace(assetPath))
}

func cloneLoadoutSlots(slots []LoadoutSlot) []LoadoutSlot {
	return append([]LoadoutSlot(nil), slots...)
}

func cloneStarterLoadout(loadout StarterLoadout) StarterLoadout {
	loadout.Slots = cloneLoadoutSlots(loadout.Slots)
	return loadout
}

func cloneInstallerStarterPackage(pkg InstallerStarterPackage) InstallerStarterPackage {
	pkg.ChunkAssets = append([]string(nil), pkg.ChunkAssets...)
	return pkg
}

func cloneFleetEligibility(eligibility FleetEligibility) FleetEligibility {
	eligibility.AllowedTiers = append([]int32(nil), eligibility.AllowedTiers...)
	return eligibility
}

func cloneProgressionTaxonomy(taxonomy ProgressionTaxonomy) ProgressionTaxonomy {
	taxonomy.AssetRoots = append([]string(nil), taxonomy.AssetRoots...)
	return taxonomy
}

func ItemByID(itemID int32) (ItemMetadata, bool) {
	item, ok := itemsByID[itemID]
	return item, ok
}

func ItemByAssetPath(assetPath string) (ItemMetadata, bool) {
	item, ok := itemsByAssetPath[assetPathLookupKey(assetPath)]
	return item, ok
}

func ItemByTypeAndDisplayName(itemType string, displayName string) (ItemMetadata, bool) {
	item, ok := itemsByTypeAndName[itemLookupKey(itemType, displayName)]
	return item, ok
}

func MustItemByTypeAndDisplayName(itemType string, displayName string) ItemMetadata {
	item, ok := ItemByTypeAndDisplayName(itemType, displayName)
	if !ok {
		panic(fmt.Sprintf("missing dreadgame item %s:%s", itemType, displayName))
	}
	return item
}

func mustItemByID(itemID int32) ItemMetadata {
	item, ok := ItemByID(itemID)
	if !ok {
		panic(fmt.Sprintf("missing dreadgame item id %d", itemID))
	}
	return item
}

func StarterInventoryLoadouts() []StarterLoadout {
	loadouts := make([]StarterLoadout, 0, len(starterInventoryLoadouts))
	for _, loadout := range starterInventoryLoadouts {
		loadouts = append(loadouts, cloneStarterLoadout(loadout))
	}
	return loadouts
}

func StarterInventoryItems() []StarterInventoryItem {
	items := make([]StarterInventoryItem, 0, len(starterInventoryLoadouts)*12)
	for _, loadout := range starterInventoryLoadouts {
		items = append(items,
			StarterInventoryItem{
				Item:     mustItemByID(loadout.ShipID),
				ShipName: loadout.ShipName,
				ShipID:   loadout.ShipID,
			},
			StarterInventoryItem{
				Item:      mustItemByID(loadout.LoadoutID),
				ShipName:  loadout.ShipName,
				ShipID:    loadout.ShipID,
				LoadoutID: loadout.LoadoutID,
			},
		)
		for _, slot := range loadout.Slots {
			items = append(items, StarterInventoryItem{
				Item:      mustItemByID(slot.ItemID),
				ShipName:  loadout.ShipName,
				ShipID:    loadout.ShipID,
				LoadoutID: loadout.LoadoutID,
				SlotName:  slot.SlotName,
			})
		}
	}
	return items
}

func StarterInventoryLoadoutByShipName(shipName string) (StarterLoadout, bool) {
	loadout, ok := starterLoadoutsByShip[strings.ToLower(strings.TrimSpace(shipName))]
	if !ok {
		return StarterLoadout{}, false
	}
	return cloneStarterLoadout(loadout), true
}

func MustStarterInventoryLoadoutByShipName(shipName string) StarterLoadout {
	loadout, ok := StarterInventoryLoadoutByShipName(shipName)
	if !ok {
		panic(fmt.Sprintf("missing starter loadout for ship %q", shipName))
	}
	return loadout
}

func StarterInventoryShipIDs() []int32 {
	ids := make([]int32, 0, len(starterInventoryLoadouts))
	for _, loadout := range starterInventoryLoadouts {
		ids = append(ids, loadout.ShipID)
	}
	return ids
}

func StarterInventoryLoadoutIDs() []int32 {
	ids := make([]int32, 0, len(starterInventoryLoadouts))
	for _, loadout := range starterInventoryLoadouts {
		ids = append(ids, loadout.LoadoutID)
	}
	return ids
}

func InstallerStarterPackages() []InstallerStarterPackage {
	packages := make([]InstallerStarterPackage, 0, len(installerStarterPackages))
	for _, pkg := range installerStarterPackages {
		packages = append(packages, cloneInstallerStarterPackage(pkg))
	}
	return packages
}

func FleetEligibilityValues() []FleetEligibility {
	values := make([]FleetEligibility, 0, len(fleetEligibilityValues))
	for _, eligibility := range fleetEligibilityValues {
		values = append(values, cloneFleetEligibility(eligibility))
	}
	return values
}

func FleetEligibilityByToken(token string) (FleetEligibility, bool) {
	eligibility, ok := fleetEligibilityByToken[strings.ToLower(strings.TrimSpace(token))]
	if !ok {
		return FleetEligibility{}, false
	}
	return cloneFleetEligibility(eligibility), true
}

func ProgressionTaxonomies() []ProgressionTaxonomy {
	taxonomies := make([]ProgressionTaxonomy, 0, len(progressionTaxonomies))
	for _, taxonomy := range progressionTaxonomies {
		taxonomies = append(taxonomies, cloneProgressionTaxonomy(taxonomy))
	}
	return taxonomies
}

func UnlockContainerExportSurfaces() []ExportSurface {
	return append([]ExportSurface(nil), unlockContainerExportSurfaces...)
}
