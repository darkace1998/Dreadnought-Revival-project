package dreadgameconfig

import (
	"testing"
)

func TestWeaponStatsStructFields(t *testing.T) {
	w := WeaponStats{
		SlotType:                          "YST_Primary",
		Class:                             "ASSAULT",
		DamageHigh:                        160,
		DamageHighRange:                   6000,
		DamageMedium:                      160,
		DamageMediumRange:                 12000,
		DamageLow:                         145,
		AmplifiedDamageHigh:               16,
		AmplifiedDamageMedium:             16,
		AmplifiedDamageLow:                14,
		MaxRange:                          35000,
		DamageRadius:                      0,
		IgnoreShields:                     false,
		FriendlyFire:                      false,
		ExactHitDetection:                 false,
		InstigatorIgnoreTime:              -1.0,
		WeaponChargeUpTime:                0.0,
		WeaponCooldownTime:                0.7,
		AmplifiedCoolDownMultiplierTime:   1.333,
		AbilityDamage:                     0.0,
		InitialSpeed:                      18500,
		MaxFlightSpeed:                    100000,
		GravityScale:                      0.0,
		AmmoMagazinSize:                   25,
		AmmoMagazinReloadTime:             1.8,
		AmplifiedAmmoMagazinReloadTime:    1.8,
		SpreadBaseValue:                   0.8,
		SpreadMaxValue:                    1.3,
		SpreadEase:                        1.3,
		SpreadBonusAim:                    0.6,
		SpreadPenaltyMovement:             0.01,
		SpreadPenaltyShooting:             0.15,
		SpreadPenaltyDamageMultiplier:     0.01,
		ProximityDistance:                 0,
		ProximityAgainstCreeps:            false,
		HighHealing:                       0,
		HighHealingRange:                  0,
		MediumHealing:                     0,
		MediumHealingRange:                0,
		LowHealing:                        0,
		MaxHealingRange:                   0,
		HitImpactForce:                    5000,
		FireRecoilForce:                   15000,
		AmplifiedEnergyCost:               0.2,
		HitzoneDamageMultiplier:           0.0,
		HitzoneHitPenetrationDistance:     100.0,
		PreciseAimingRange:                0,
	}

	if w.SlotType != "YST_Primary" {
		t.Errorf("SlotType = %v, want YST_Primary", w.SlotType)
	}
	if w.Class != "ASSAULT" {
		t.Errorf("Class = %v, want ASSAULT", w.Class)
	}
	if w.DamageHigh != 160 {
		t.Errorf("DamageHigh = %v, want 160", w.DamageHigh)
	}
	if w.WeaponCooldownTime != 0.7 {
		t.Errorf("WeaponCooldownTime = %v, want 0.7", w.WeaponCooldownTime)
	}
	if w.AmmoMagazinSize != 25 {
		t.Errorf("AmmoMagazinSize = %v, want 25", w.AmmoMagazinSize)
	}
	if w.IgnoreShields != false {
		t.Errorf("IgnoreShields = %v, want false", w.IgnoreShields)
	}
}

func TestWeaponStatsFromRow(t *testing.T) {
	row := Row{
		"m_slotType":                        "YST_Primary",
		"m_class":                           "ASSAULT",
		"m_damageHigh":                      float64(160),
		"m_damageHighRange":                 float64(6000),
		"m_damageMedium":                    float64(160),
		"m_damageMediumRange":               float64(12000),
		"m_damageLow":                       float64(145),
		"m_amplifiedDamageHigh":             float64(16),
		"m_amplifiedDamageMedium":           float64(16),
		"m_amplifiedDamageLow":              float64(14),
		"m_maxRange":                        float64(35000),
		"m_damageRadius":                    float64(0),
		"m_ignoreShields":                   false,
		"m_friendlyFire":                    false,
		"m_exactHitDetection":               false,
		"m_instigatorIgnoreTime":            float64(-1.0),
		"m_weaponChargeUpTime":              float64(0.0),
		"m_weaponCooldownTime":              float64(0.7),
		"m_amplifiedCoolDownMultiplierTime": float64(1.333),
		"m_abilityDamage":                   float64(0.0),
		"m_initialSpeed":                    float64(18500),
		"m_maxFlightSpeed":                  float64(100000),
		"m_gravityScale":                    float64(0.0),
		"m_ammoMagazinSize":                 float64(25),
		"m_ammoMagazinReloadTime":           float64(1.8),
		"m_amplifiedAmmoMagazinReloadTime":  float64(1.8),
		"m_spreadBaseValue":                 float64(0.8),
		"m_spreadMaxValue":                  float64(1.3),
		"m_spreadEase":                      float64(1.3),
		"m_spreadBonusAim":                  float64(0.6),
		"m_spreadPenaltyMovement":           float64(0.01),
		"m_spreadPenaltyShooting":           float64(0.15),
		"m_spreadPenaltyDamageMultiplier":   float64(0.01),
		"m_proximityDistance":               float64(0),
		"m_proximityAgainstCreeps":          false,
		"m_highHealing":                     float64(0),
		"m_highHealingRange":                float64(0),
		"m_mediumHealing":                   float64(0),
		"m_mediumHealingRange":              float64(0),
		"m_lowHealing":                      float64(0),
		"m_maxHealingRange":                 float64(0),
		"m_hitImpactForce":                  float64(5000),
		"m_fireRecoilForce":                 float64(15000),
		"m_amplifiedEnergyCost":             float64(0.2),
		"m_hitzoneDamageMultiplier":         float64(0.0),
		"m_hitzoneHitPenetrationDistance":   float64(100.0),
		"m_preciseAimingRange":              float64(0),
	}

	w := WeaponStats{
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

	if w.SlotType != "YST_Primary" {
		t.Errorf("SlotType = %v, want YST_Primary", w.SlotType)
	}
	if w.Class != "ASSAULT" {
		t.Errorf("Class = %v, want ASSAULT", w.Class)
	}
	if w.DamageHigh != 160 {
		t.Errorf("DamageHigh = %v, want 160", w.DamageHigh)
	}
	if w.DamageHighRange != 6000 {
		t.Errorf("DamageHighRange = %v, want 6000", w.DamageHighRange)
	}
	if w.WeaponCooldownTime != 0.7 {
		t.Errorf("WeaponCooldownTime = %v, want 0.7", w.WeaponCooldownTime)
	}
	if w.AmmoMagazinSize != 25 {
		t.Errorf("AmmoMagazinSize = %v, want 25", w.AmmoMagazinSize)
	}
	if w.InitialSpeed != 18500 {
		t.Errorf("InitialSpeed = %v, want 18500", w.InitialSpeed)
	}
	if w.SpreadBaseValue != 0.8 {
		t.Errorf("SpreadBaseValue = %v, want 0.8", w.SpreadBaseValue)
	}
	if w.HitImpactForce != 5000 {
		t.Errorf("HitImpactForce = %v, want 5000", w.HitImpactForce)
	}
}
