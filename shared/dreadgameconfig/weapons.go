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

	itemIDRegisterPath := AssetPath("ItemIDRegister.json")
	registerData, err := os.ReadFile(itemIDRegisterPath)
	if err != nil {
		return fmt.Errorf("read item ID register: %w", err)
	}

	var register struct {
		ItemIDRegister []struct {
			ItemID int32  `json:"ItemID"`
			Path   string `json:"Path"`
		} `json:"ItemIDRegister"`
	}
	if err := json.Unmarshal(registerData, &register); err != nil {
		return fmt.Errorf("parse item ID register: %w", err)
	}

	pathToItemID := make(map[string]int32)
	for _, entry := range register.ItemIDRegister {
		if strings.Contains(entry.Path, "/Weapons/") {
			pathToItemID[entry.Path] = entry.ItemID
		}
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
