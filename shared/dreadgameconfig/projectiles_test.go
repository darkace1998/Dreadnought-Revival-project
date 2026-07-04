package dreadgameconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectileStatsStructFields(t *testing.T) {
	p := ProjectileStats{
		DamageHigh:                160,
		DamageHighRange:           6000,
		DamageMedium:              160,
		DamageMediumRange:         12000,
		DamageLow:                 145,
		AmplifiedDamageHigh:       16,
		AmplifiedDamageMedium:     16,
		AmplifiedDamageLow:        14,
		MaxTravelDistance:         35000.0,
		MaxRange:                  36000.0,
		DamageRadius:              0,
		IgnoreShields:             false,
		FriendlyFire:              false,
		ExactHitDetection:         false,
		InstigatorIgnoreTime:      -1.0,
		InitialSpeed:              18500.0,
		MaxFlightSpeed:            100000.0,
		GravityScale:              0.0,
		Mass:                      1.0,
		HitImpactForce:            5000.0,
		FireRecoilForce:           15000.0,
		HitzoneDamageMultiplier:   0.0,
		HitzoneHitPenetrationDistance: 100.0,
		AbilityDamage:             0.0,
		InheritStartVelocity:      false,
		DetonateOnProximity:       false,
		ProximityDistance:         0.0,
		ProximityAgainstCreeps:    false,
		TargetInitialPosition:     false,
		TargetLastPositionOnTargetDeath: false,
		IncreaseUpdateRateWhenClosingIn: true,
		IncreasedUpdateRateMinMassFactor: 0.25,
		ThrusterStartDelay:        0.0,
		InitialRotationDelay:      0.0,
		SteeringStartDelay:        0.0,
		ThrusterDuration:          120.0,
		SteeringDuration:          120.0,
		ThrusterForce:             2000.0,
		InitialRotationInterpolationSpeed: 45.0,
		InitialRotationConstantOffsetPitch: 0.0,
		InitialRotationConstantOffsetRoll:  0.0,
		InitialRotationConstantOffsetYaw:   0.0,
		InitialRotationMaxDeviation: 20.0,
		RotationInterpolationSpeed: 45.0,
		DeviationDuration:         0.75,
		MaxRotDeviation:           10.0,
		RotationInterpolationSpeedThrustersOff: 0.0,
		StartVelocityRotationOffsetPitch: 0.0,
		StartVelocityRotationOffsetRoll:  0.0,
		StartVelocityRotationOffsetYaw:   0.0,
		Health:                    100.0,
		OldDataTable:              "oldDT-Abilities/DN_ProjectileMissile_DT",
		RowName:                   "WP_AssaultMPri01_proj01_BP",
	}

	if p.DamageHigh != 160 {
		t.Errorf("DamageHigh = %v, want %d", p.DamageHigh, 160)
	}
	if p.InitialSpeed != 18500.0 {
		t.Errorf("InitialSpeed = %v, want %f", p.InitialSpeed, 18500.0)
	}
	if p.Mass != 1.0 {
		t.Errorf("Mass = %v, want %f", p.Mass, 1.0)
	}
	if p.Health != 100.0 {
		t.Errorf("Health = %v, want %f", p.Health, 100.0)
	}
	if p.RowName != "WP_AssaultMPri01_proj01_BP" {
		t.Errorf("RowName = %v, want %s", p.RowName, "WP_AssaultMPri01_proj01_BP")
	}
}

func TestProjectileStatsFromRow(t *testing.T) {
	row := Row{
		"m_damageHigh":                float64(160),
		"m_damageHighRange":           float64(6000),
		"m_damageMedium":              float64(160),
		"m_damageMediumRange":         float64(12000),
		"m_damageLow":                 float64(145),
		"m_amplifiedDamageHigh":       float64(16),
		"m_amplifiedDamageMedium":     float64(16),
		"m_amplifiedDamageLow":        float64(14),
		"m_maxTravelDistance":         float64(35000.0),
		"m_maxRange":                  float64(36000.0),
		"m_damageRadius":              float64(0),
		"m_ignoreShields":             false,
		"m_friendlyFire":              false,
		"m_exactHitDetection":         false,
		"m_instigatorIgnoreTime":      float64(-1.0),
		"m_initialSpeed":              float64(18500.0),
		"m_maxFlightSpeed":            float64(100000.0),
		"m_gravityScale":              float64(0.0),
		"m_proximityDistance":         float64(0.0),
		"m_proximityAgainstCreeps":    false,
		"m_hitImpactForce":            float64(5000.0),
		"m_fireRecoilForce":           float64(15000.0),
		"m_hitzoneDamageMultiplier":   float64(0.0),
		"m_hitzoneHitPenetrationDistance": float64(100.0),
		"m_abilityDamage":             float64(0.0),
		"m_inheritStartVelocity":      false,
		"m_detonateOnProximity":       false,
		"m_mass":                      float64(1.0),
		"m_thrusterStartDelay":        float64(0.0),
		"m_initialRotationDelay":      float64(0.0),
		"m_steeringStartDelay":        float64(0.0),
		"m_thrusterDuration":          float64(120.0),
		"m_steeringDuration":          float64(120.0),
		"m_thrusterForce":             float64(2000.0),
		"m_initialRotationInterpolationSpeed": float64(45.0),
		"m_initialRotationConstantOffsetPitch": float64(0.0),
		"m_initialRotationConstantOffsetRoll":  float64(0.0),
		"m_initialRotationConstantOffsetYaw":   float64(0.0),
		"m_initialRotationMaxDeviation":        float64(20.0),
		"m_rotationInterpolationSpeed":         float64(45.0),
		"m_deviationDuration":         float64(0.75),
		"m_maxRotDeviation":           float64(10.0),
		"m_increaseUpdateRateWhenClosingIn":    true,
		"m_increasedUpdateRateMinMassFactor":   float64(0.25),
		"m_rotationInterpolationSpeedThrustersOff": float64(0.0),
		"m_targetInitialPosition":     false,
		"m_targetLastPositionOnTargetDeath": false,
		"m_health":                    float64(100.0),
		"m_oldDataTable":              "oldDT-Abilities/DN_ProjectileMissile_DT",
		"m_startVelocityRotationOffsetPitch": float64(0.0),
		"m_startVelocityRotationOffsetRoll":  float64(0.0),
		"m_startVelocityRotationOffsetYaw":   float64(0.0),
	}

	p := ProjectileStatsFromRow(row)
	p.RowName = "WP_AssaultMPri01_proj01_BP"

	if p.DamageHigh != 160 {
		t.Errorf("DamageHigh = %v, want %d", p.DamageHigh, 160)
	}
	if p.DamageHighRange != 6000 {
		t.Errorf("DamageHighRange = %v, want %d", p.DamageHighRange, 6000)
	}
	if p.InitialSpeed != 18500.0 {
		t.Errorf("InitialSpeed = %v, want %f", p.InitialSpeed, 18500.0)
	}
	if p.Mass != 1.0 {
		t.Errorf("Mass = %v, want %f", p.Mass, 1.0)
	}
	if p.Health != 100.0 {
		t.Errorf("Health = %v, want %f", p.Health, 100.0)
	}
}

func TestLoadProjectiles(t *testing.T) {
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()

	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	projectilesLoaded = false
	projectilesByRowName = nil

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("LoadProjectiles() error = %v", err)
	}

	if !projectilesLoaded {
		t.Error("projectilesLoaded should be true after LoadProjectiles()")
	}

	if len(projectilesByRowName) == 0 {
		t.Error("projectilesByRowName should not be empty after LoadProjectiles()")
	}

	if len(projectilesByRowName) != 393 {
		t.Errorf("loaded %d projectiles, want %d", len(projectilesByRowName), 393)
	}
}

func TestProjectileByRowName(t *testing.T) {
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()

	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	projectilesLoaded = false
	projectilesByRowName = nil

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("LoadProjectiles() error = %v", err)
	}

	// Test with a known projectile row name
	testRowName := "WP_AssaultMPri01_proj01_BP"
	p, ok := ProjectileByRowName(testRowName)
	if !ok {
		t.Errorf("ProjectileByRowName(%q) returned false, want true", testRowName)
	}

	if p.RowName != testRowName {
		t.Errorf("ProjectileByRowName(%q).RowName = %q, want %q", testRowName, p.RowName, testRowName)
	}

	if p.DamageHigh <= 0 {
		t.Errorf("Projectile %q DamageHigh = %d, want positive value", testRowName, p.DamageHigh)
	}

	// Test with non-existent projectile
	_, ok = ProjectileByRowName("NonExistentProjectile")
	if ok {
		t.Error("ProjectileByRowName(\"NonExistentProjectile\") returned true, want false")
	}
}

func TestAllProjectiles(t *testing.T) {
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()

	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	projectilesLoaded = false
	projectilesByRowName = nil

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("LoadProjectiles() error = %v", err)
	}

	allProjectiles := AllProjectiles()
	if len(allProjectiles) != 393 {
		t.Errorf("AllProjectiles() returned %d projectiles, want %d", len(allProjectiles), 393)
	}

	for rowName, p := range allProjectiles {
		if p.RowName != rowName {
			t.Errorf("AllProjectiles()[%q].RowName = %q, want %q", rowName, p.RowName, rowName)
		}
	}
}

func TestLoadProjectilesIdempotent(t *testing.T) {
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()

	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	projectilesLoaded = false
	projectilesByRowName = nil

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("First LoadProjectiles() error = %v", err)
	}

	firstCount := len(projectilesByRowName)

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("Second LoadProjectiles() error = %v", err)
	}

	secondCount := len(projectilesByRowName)

	if firstCount != secondCount {
		t.Errorf("LoadProjectiles() not idempotent: first=%d, second=%d", firstCount, secondCount)
	}
}