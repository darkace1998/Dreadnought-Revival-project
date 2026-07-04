package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WeaponStats struct {
	ItemID                            int32   `json:"-"`
	SlotType                          string  `json:"m_slotType"`
	Class                             string  `json:"m_class"`
	DamageHigh                        int32   `json:"m_damageHigh"`
	DamageHighRange                   int32   `json:"m_damageHighRange"`
	DamageMedium                      int32   `json:"m_damageMedium"`
	DamageMediumRange                 int32   `json:"m_damageMediumRange"`
	DamageLow                         int32   `json:"m_damageLow"`
	AmplifiedDamageHigh               int32   `json:"m_amplifiedDamageHigh"`
	AmplifiedDamageMedium             int32   `json:"m_amplifiedDamageMedium"`
	AmplifiedDamageLow                int32   `json:"m_amplifiedDamageLow"`
	MaxRange                          int32   `json:"m_maxRange"`
	DamageRadius                      int32   `json:"m_damageRadius"`
	IgnoreShields                     bool    `json:"m_ignoreShields"`
	FriendlyFire                      bool    `json:"m_friendlyFire"`
	ExactHitDetection                 bool    `json:"m_exactHitDetection"`
	InstigatorIgnoreTime              float64 `json:"m_instigatorIgnoreTime"`
	WeaponChargeUpTime                float64 `json:"m_weaponChargeUpTime"`
	WeaponCooldownTime                float64 `json:"m_weaponCooldownTime"`
	AmplifiedCoolDownMultiplierTime   float64 `json:"m_amplifiedCoolDownMultiplierTime"`
	AbilityDamage                     float64 `json:"m_abilityDamage"`
	InitialSpeed                      int32   `json:"m_initialSpeed"`
	MaxFlightSpeed                    int32   `json:"m_maxFlightSpeed"`
	GravityScale                      float64 `json:"m_gravityScale"`
	AmmoMagazinSize                   int32   `json:"m_ammoMagazinSize"`
	AmmoMagazinReloadTime             float64 `json:"m_ammoMagazinReloadTime"`
	AmplifiedAmmoMagazinReloadTime    float64 `json:"m_amplifiedAmmoMagazinReloadTime"`
	SpreadBaseValue                   float64 `json:"m_spreadBaseValue"`
	SpreadMaxValue                    float64 `json:"m_spreadMaxValue"`
	SpreadEase                        float64 `json:"m_spreadEase"`
	SpreadBonusAim                    float64 `json:"m_spreadBonusAim"`
	SpreadPenaltyMovement             float64 `json:"m_spreadPenaltyMovement"`
	SpreadPenaltyShooting             float64 `json:"m_spreadPenaltyShooting"`
	SpreadPenaltyDamageMultiplier     float64 `json:"m_spreadPenaltyDamageMultiplier"`
	ProximityDistance                 int32   `json:"m_proximityDistance"`
	ProximityAgainstCreeps            bool    `json:"m_proximityAgainstCreeps"`
	HighHealing                       int32   `json:"m_highHealing"`
	HighHealingRange                  int32   `json:"m_highHealingRange"`
	MediumHealing                     int32   `json:"m_mediumHealing"`
	MediumHealingRange                int32   `json:"m_mediumHealingRange"`
	LowHealing                        int32   `json:"m_lowHealing"`
	MaxHealingRange                   int32   `json:"m_maxHealingRange"`
	HitImpactForce                    int32   `json:"m_hitImpactForce"`
	FireRecoilForce                   int32   `json:"m_fireRecoilForce"`
	AmplifiedEnergyCost               float64 `json:"m_amplifiedEnergyCost"`
	HitzoneDamageMultiplier           float64 `json:"m_hitzoneDamageMultiplier"`
	HitzoneHitPenetrationDistance     float64 `json:"m_hitzoneHitPenetrationDistance"`
	PreciseAimingRange                int32   `json:"m_preciseAimingRange"`
	projectileRowName                 string  `json:"-"` // Derived from weapon name, private field
}

var (
	weaponsByItemID map[int32]WeaponStats
	weaponsLoaded   bool
)

func LoadWeapons() error {
	if weaponsLoaded {
		return nil
	}

	weaponsPath := DataTablePath(filepath.Join("DN_Weapons_OTS_DT.json"))
	data, err := os.ReadFile(weaponsPath)
	if err != nil {
		return fmt.Errorf("read weapons datatable: %w", err)
	}

	var dt DataTable
	if err := json.Unmarshal(data, &dt); err != nil {
		return fmt.Errorf("parse weapons datatable: %w", err)
	}

	// Load ItemIDRegister for cross-referencing
	pathToItemID, err := loadItemIDRegisterForType("/Weapons/")
	if err != nil {
		return fmt.Errorf("load item ID register for weapons: %w", err)
	}

	weaponsByItemID = make(map[int32]WeaponStats)
	for rowName, row := range dt.Rows {
		var weaponPath string
		for path := range pathToItemID {
			if strings.HasSuffix(path, "/"+rowName) {
				weaponPath = path
				break
			}
		}

		if weaponPath == "" {
			continue
		}

		itemID := pathToItemID[weaponPath]
		stats := WeaponStats{
			ItemID:                            itemID,
			SlotType:                          row.GetString("m_slotType"),
			Class:                             row.GetString("m_class"),
			DamageHigh:                        row.GetInt32("m_damageHigh"),
			DamageHighRange:                   row.GetInt32("m_damageHighRange"),
			DamageMedium:                      row.GetInt32("m_damageMedium"),
			DamageMediumRange:                 row.GetInt32("m_damageMediumRange"),
			DamageLow:                         row.GetInt32("m_damageLow"),
			AmplifiedDamageHigh:               row.GetInt32("m_amplifiedDamageHigh"),
			AmplifiedDamageMedium:             row.GetInt32("m_amplifiedDamageMedium"),
			AmplifiedDamageLow:                row.GetInt32("m_amplifiedDamageLow"),
			MaxRange:                          row.GetInt32("m_maxRange"),
			DamageRadius:                      row.GetInt32("m_damageRadius"),
			IgnoreShields:                     row.GetBool("m_ignoreShields"),
			FriendlyFire:                      row.GetBool("m_friendlyFire"),
			ExactHitDetection:                 row.GetBool("m_exactHitDetection"),
			InstigatorIgnoreTime:              row.GetFloat64("m_instigatorIgnoreTime"),
			WeaponChargeUpTime:                row.GetFloat64("m_weaponChargeUpTime"),
			WeaponCooldownTime:                row.GetFloat64("m_weaponCooldownTime"),
			AmplifiedCoolDownMultiplierTime:   row.GetFloat64("m_amplifiedCoolDownMultiplierTime"),
			AbilityDamage:                     row.GetFloat64("m_abilityDamage"),
			InitialSpeed:                      row.GetInt32("m_initialSpeed"),
			MaxFlightSpeed:                    row.GetInt32("m_maxFlightSpeed"),
			GravityScale:                      row.GetFloat64("m_gravityScale"),
			AmmoMagazinSize:                   row.GetInt32("m_ammoMagazinSize"),
			AmmoMagazinReloadTime:             row.GetFloat64("m_ammoMagazinReloadTime"),
			AmplifiedAmmoMagazinReloadTime:    row.GetFloat64("m_amplifiedAmmoMagazinReloadTime"),
			projectileRowName:                 deriveProjectileRowName(rowName),
			SpreadBaseValue:                   row.GetFloat64("m_spreadBaseValue"),
			SpreadMaxValue:                    row.GetFloat64("m_spreadMaxValue"),
			SpreadEase:                        row.GetFloat64("m_spreadEase"),
			SpreadBonusAim:                    row.GetFloat64("m_spreadBonusAim"),
			SpreadPenaltyMovement:             row.GetFloat64("m_spreadPenaltyMovement"),
			SpreadPenaltyShooting:             row.GetFloat64("m_spreadPenaltyShooting"),
			SpreadPenaltyDamageMultiplier:     row.GetFloat64("m_spreadPenaltyDamageMultiplier"),
			ProximityDistance:                 row.GetInt32("m_proximityDistance"),
			ProximityAgainstCreeps:            row.GetBool("m_proximityAgainstCreeps"),
			HighHealing:                       row.GetInt32("m_highHealing"),
			HighHealingRange:                  row.GetInt32("m_highHealingRange"),
			MediumHealing:                     row.GetInt32("m_mediumHealing"),
			MediumHealingRange:                row.GetInt32("m_mediumHealingRange"),
			LowHealing:                        row.GetInt32("m_lowHealing"),
			MaxHealingRange:                   row.GetInt32("m_maxHealingRange"),
			HitImpactForce:                    row.GetInt32("m_hitImpactForce"),
			FireRecoilForce:                   row.GetInt32("m_fireRecoilForce"),
			AmplifiedEnergyCost:               row.GetFloat64("m_amplifiedEnergyCost"),
			HitzoneDamageMultiplier:           row.GetFloat64("m_hitzoneDamageMultiplier"),
			HitzoneHitPenetrationDistance:     row.GetFloat64("m_hitzoneHitPenetrationDistance"),
			PreciseAimingRange:                row.GetInt32("m_preciseAimingRange"),
		}
		weaponsByItemID[itemID] = stats
	}

	weaponsLoaded = true
	return nil
}

func WeaponByID(itemID int32) (WeaponStats, bool) {
	if !weaponsLoaded {
		if err := LoadWeapons(); err != nil {
			return WeaponStats{}, false
		}
	}
	w, ok := weaponsByItemID[itemID]
	return w, ok
}



func AllWeapons() map[int32]WeaponStats {
	if !weaponsLoaded {
		if err := LoadWeapons(); err != nil {
			return make(map[int32]WeaponStats)
		}
	}
	result := make(map[int32]WeaponStats, len(weaponsByItemID))
	for k, v := range weaponsByItemID {
		result[k] = v
	}
	return result
}

// ProjectileRowName returns the projectile row name for this weapon
func (w WeaponStats) ProjectileRowName() string {
	return w.projectileRowName
}

// deriveProjectileRowName maps weapon names to their corresponding projectile names
func deriveProjectileRowName(weaponRowName string) string {
	// Implement mapping logic from weapon names to projectile names
	// Example pattern: "WP_AssaultHPri01_weapon01_BP" -> "WP_AssaultHPri01_proj01_BP"
	// Example pattern: "WP_AssaultHPri01_weapon01_T3_BP" -> "WP_AssaultHPri01_proj01_T3_BP"

	// Check if this weapon has a projectile
	if !strings.Contains(weaponRowName, "_weapon") {
		return ""
	}

	// Extract tier suffix if present
	tier := ""
	if strings.Contains(weaponRowName, "_T") {
		parts := strings.Split(weaponRowName, "_T")
		if len(parts) > 1 {
			tierPart := parts[1]
			if len(tierPart) > 0 && tierPart[0] >= '0' && tierPart[0] <= '9' {
				tier = "_T" + string(tierPart[0])
			}
		}
	}

	// Extract the base weapon name (remove tier suffix and _BP if present)
	baseName := strings.TrimSuffix(weaponRowName, "_BP")
	if strings.Contains(baseName, "_T") {
		baseName = strings.Split(baseName, "_T")[0]
	}

	// Map weapon names to projectile names based on observed patterns
	weaponToProjectile := map[string]string{
		"WP_AssaultHPri01_weapon01":   "WP_AssaultHPri01_proj01",
		"WP_AssaultMPri01_weapon01":   "WP_AssaultMPri01_proj01",
		"WP_AssaultSPri01_weapon01":   "WP_AssaultSPri01_proj01",
		"WP_AssaultMSec01_weapon01":   "WP_AssaultMSec01_proj01",
		// Add more mappings as needed
	}

	projectileBase, ok := weaponToProjectile[baseName]
	if !ok {
		return ""
	}

	// Return projectile with tier suffix
	return projectileBase + tier + "_BP"
}
