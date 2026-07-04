package dreadgameconfig

import (
	"log"
)

// ProjectileStatsFromRow creates a ProjectileStats struct from a Row
func ProjectileStatsFromRow(row Row) ProjectileStats {
	return ProjectileStats{
		DamageHigh:                row.GetInt32("m_damageHigh"),
		DamageHighRange:           row.GetInt32("m_damageHighRange"),
		DamageMedium:              row.GetInt32("m_damageMedium"),
		DamageMediumRange:         row.GetInt32("m_damageMediumRange"),
		DamageLow:                 row.GetInt32("m_damageLow"),
		AmplifiedDamageHigh:       row.GetInt32("m_amplifiedDamageHigh"),
		AmplifiedDamageMedium:     row.GetInt32("m_amplifiedDamageMedium"),
		AmplifiedDamageLow:        row.GetInt32("m_amplifiedDamageLow"),
		MaxTravelDistance:         row.GetFloat64("m_maxTravelDistance"),
		MaxRange:                  row.GetFloat64("m_maxRange"),
		DamageRadius:              row.GetInt32("m_damageRadius"),
		IgnoreShields:             row.GetBool("m_ignoreShields"),
		FriendlyFire:              row.GetBool("m_friendlyFire"),
		ExactHitDetection:         row.GetBool("m_exactHitDetection"),
		InstigatorIgnoreTime:      row.GetFloat64("m_instigatorIgnoreTime"),
		InitialSpeed:              row.GetFloat64("m_initialSpeed"),
		MaxFlightSpeed:            row.GetFloat64("m_maxFlightSpeed"),
		GravityScale:              row.GetFloat64("m_gravityScale"),
		ProximityDistance:         row.GetFloat64("m_proximityDistance"),
		ProximityAgainstCreeps:    row.GetBool("m_proximityAgainstCreeps"),
		HitImpactForce:            row.GetFloat64("m_hitImpactForce"),
		FireRecoilForce:           row.GetFloat64("m_fireRecoilForce"),
		HitzoneDamageMultiplier:   row.GetFloat64("m_hitzoneDamageMultiplier"),
		HitzoneHitPenetrationDistance: row.GetFloat64("m_hitzoneHitPenetrationDistance"),
		AbilityDamage:             row.GetFloat64("m_abilityDamage"),
		InheritStartVelocity:      row.GetBool("m_inheritStartVelocity"),
		DetonateOnProximity:       row.GetBool("m_detonateOnProximity"),
		Mass:                      row.GetFloat64("m_mass"),
		TargetInitialPosition:     row.GetBool("m_targetInitialPosition"),
		TargetLastPositionOnTargetDeath: row.GetBool("m_targetLastPositionOnTargetDeath"),
		IncreaseUpdateRateWhenClosingIn:    row.GetBool("m_increaseUpdateRateWhenClosingIn"),
		IncreasedUpdateRateMinMassFactor:   row.GetFloat64("m_increasedUpdateRateMinMassFactor"),
		ThrusterStartDelay:        row.GetFloat64("m_thrusterStartDelay"),
		InitialRotationDelay:      row.GetFloat64("m_initialRotationDelay"),
		SteeringStartDelay:        row.GetFloat64("m_steeringStartDelay"),
		ThrusterDuration:          row.GetFloat64("m_thrusterDuration"),
		SteeringDuration:          row.GetFloat64("m_steeringDuration"),
		ThrusterForce:             row.GetFloat64("m_thrusterForce"),
		InitialRotationInterpolationSpeed: row.GetFloat64("m_initialRotationInterpolationSpeed"),
		InitialRotationConstantOffsetPitch: row.GetFloat64("m_initialRotationConstantOffsetPitch"),
		InitialRotationConstantOffsetRoll:  row.GetFloat64("m_initialRotationConstantOffsetRoll"),
		InitialRotationConstantOffsetYaw:   row.GetFloat64("m_initialRotationConstantOffsetYaw"),
		InitialRotationMaxDeviation:        row.GetFloat64("m_initialRotationMaxDeviation"),
		RotationInterpolationSpeed:         row.GetFloat64("m_rotationInterpolationSpeed"),
		DeviationDuration:         row.GetFloat64("m_deviationDuration"),
		MaxRotDeviation:           row.GetFloat64("m_maxRotDeviation"),
		RotationInterpolationSpeedThrustersOff: row.GetFloat64("m_rotationInterpolationSpeedThrustersOff"),
		StartVelocityRotationOffsetPitch: row.GetFloat64("m_startVelocityRotationOffsetPitch"),
		StartVelocityRotationOffsetRoll:  row.GetFloat64("m_startVelocityRotationOffsetRoll"),
		StartVelocityRotationOffsetYaw:   row.GetFloat64("m_startVelocityRotationOffsetYaw"),
		Health:                    row.GetFloat64("m_health"),
		OldDataTable:              row.GetString("m_oldDataTable"),
	}
}

// ProjectileStats represents the statistics for a projectile from DN_Projectile_OTS_DT.json
// This struct contains all the fields that define a projectile's behavior and properties
// in the game, including damage, movement, targeting, and physics characteristics.
type ProjectileStats struct {
	// Damage fields
	DamageHigh                int32     // High damage value
	DamageHighRange           int32     // Range at which high damage is applied
	DamageMedium              int32     // Medium damage value
	DamageMediumRange         int32     // Range at which medium damage is applied
	DamageLow                 int32     // Low damage value
	AmplifiedDamageHigh       int32     // Amplified high damage value
	AmplifiedDamageMedium     int32     // Amplified medium damage value
	AmplifiedDamageLow        int32     // Amplified low damage value
	MaxTravelDistance         float64   // Maximum distance the projectile can travel
	MaxRange                  float64   // Maximum effective range
	DamageRadius              int32     // Radius of area of effect damage
	IgnoreShields             bool      // Whether the projectile ignores shields
	FriendlyFire              bool      // Whether the projectile can damage friendly targets
	ExactHitDetection         bool      // Whether exact hit detection is used
	InstigatorIgnoreTime      float64   // Time during which the instigator is ignored
	AbilityDamage             float64   // Damage dealt by abilities
	HitzoneDamageMultiplier   float64   // Multiplier for hitzone damage
	HitzoneHitPenetrationDistance float64 // Distance for hitzone penetration

	// Movement and physics fields
	InitialSpeed              float64   // Initial speed of the projectile
	MaxFlightSpeed            float64   // Maximum flight speed
	GravityScale              float64   // Scale of gravity effect on the projectile
	Mass                      float64   // Mass of the projectile
	HitImpactForce            float64   // Force applied on hit
	FireRecoilForce           float64   // Recoil force when fired
	InheritStartVelocity      bool      // Whether the projectile inherits the shooter's velocity

	// Proximity and targeting fields
	ProximityDistance         float64   // Distance for proximity detonation
	ProximityAgainstCreeps    bool      // Whether proximity works against creeps
	DetonateOnProximity       bool      // Whether the projectile detonates on proximity
	TargetInitialPosition     bool      // Whether to target the initial position
	TargetLastPositionOnTargetDeath bool // Whether to target last position when target dies
	IncreaseUpdateRateWhenClosingIn bool // Whether to increase update rate when closing in
	IncreasedUpdateRateMinMassFactor float64 // Minimum mass factor for increased update rate

	// Thruster and steering fields
	ThrusterStartDelay        float64   // Delay before thrusters start
	InitialRotationDelay      float64   // Delay before initial rotation starts
	SteeringStartDelay        float64   // Delay before steering starts
	ThrusterDuration           float64   // Duration of thruster operation
	SteeringDuration           float64   // Duration of steering operation
	ThrusterForce             float64   // Force applied by thrusters
	InitialRotationInterpolationSpeed float64 // Speed of initial rotation interpolation
	InitialRotationConstantOffsetPitch float64 // Constant pitch offset for initial rotation
	InitialRotationConstantOffsetRoll float64  // Constant roll offset for initial rotation
	InitialRotationConstantOffsetYaw float64   // Constant yaw offset for initial rotation
	InitialRotationMaxDeviation float64        // Maximum deviation for initial rotation
	RotationInterpolationSpeed float64         // Speed of rotation interpolation
	DeviationDuration          float64         // Duration of deviation
	MaxRotDeviation            float64         // Maximum rotation deviation
	RotationInterpolationSpeedThrustersOff float64 // Rotation speed when thrusters are off
	StartVelocityRotationOffsetPitch float64   // Pitch offset for start velocity rotation
	StartVelocityRotationOffsetRoll float64    // Roll offset for start velocity rotation
	StartVelocityRotationOffsetYaw float64     // Yaw offset for start velocity rotation

	// Health field
	Health                    float64   // Health of the projectile

	// Reference to old data table (for migration purposes)
	OldDataTable              string    // Reference to old data table

	// RowName is the key in the DataTable (e.g., "WP_AssaultMPri01_proj01_BP")
	RowName string
}

// Projectile loading state
var (
	projectilesLoaded bool
	projectilesByRowName map[string]ProjectileStats
)

// LoadProjectiles loads the projectile data from DN_Projectile_OTS_DT.json
func LoadProjectiles() error {
	if projectilesLoaded {
		return nil
	}

	projectilesPath := DataTablePath("Projectiles/DN_Projectile_OTS_DT.json")
	dt, err := LoadDataTable(projectilesPath)
	if err != nil {
		return err
	}

	projectilesByRowName = make(map[string]ProjectileStats)

	for rowName, row := range dt.Rows {
		projectile := ProjectileStats{
			DamageHigh:                row.GetInt32("m_damageHigh"),
			DamageHighRange:           row.GetInt32("m_damageHighRange"),
			DamageMedium:              row.GetInt32("m_damageMedium"),
			DamageMediumRange:         row.GetInt32("m_damageMediumRange"),
			DamageLow:                 row.GetInt32("m_damageLow"),
			AmplifiedDamageHigh:       row.GetInt32("m_amplifiedDamageHigh"),
			AmplifiedDamageMedium:     row.GetInt32("m_amplifiedDamageMedium"),
			AmplifiedDamageLow:        row.GetInt32("m_amplifiedDamageLow"),
			MaxTravelDistance:         row.GetFloat64("m_maxTravelDistance"),
			MaxRange:                  row.GetFloat64("m_maxRange"),
			DamageRadius:              row.GetInt32("m_damageRadius"),
			IgnoreShields:             row.GetBool("m_ignoreShields"),
			FriendlyFire:              row.GetBool("m_friendlyFire"),
			ExactHitDetection:         row.GetBool("m_exactHitDetection"),
			InstigatorIgnoreTime:      row.GetFloat64("m_instigatorIgnoreTime"),
			InitialSpeed:              row.GetFloat64("m_initialSpeed"),
			MaxFlightSpeed:            row.GetFloat64("m_maxFlightSpeed"),
			GravityScale:              row.GetFloat64("m_gravityScale"),
			ProximityDistance:         row.GetFloat64("m_proximityDistance"),
			ProximityAgainstCreeps:    row.GetBool("m_proximityAgainstCreeps"),
			HitImpactForce:            row.GetFloat64("m_hitImpactForce"),
			FireRecoilForce:           row.GetFloat64("m_fireRecoilForce"),
			HitzoneDamageMultiplier:   row.GetFloat64("m_hitzoneDamageMultiplier"),
			HitzoneHitPenetrationDistance: row.GetFloat64("m_hitzoneHitPenetrationDistance"),
			AbilityDamage:             row.GetFloat64("m_abilityDamage"),
			InheritStartVelocity:      row.GetBool("m_inheritStartVelocity"),
			DetonateOnProximity:       row.GetBool("m_detonateOnProximity"),
			Mass:                      row.GetFloat64("m_mass"),
			ThrusterStartDelay:        row.GetFloat64("m_thrusterStartDelay"),
			InitialRotationDelay:      row.GetFloat64("m_initialRotationDelay"),
			SteeringStartDelay:        row.GetFloat64("m_steeringStartDelay"),
			ThrusterDuration:           row.GetFloat64("m_thrusterDuration"),
			SteeringDuration:           row.GetFloat64("m_steeringDuration"),
			ThrusterForce:             row.GetFloat64("m_thrusterForce"),
			InitialRotationInterpolationSpeed: row.GetFloat64("m_initialRotationInterpolationSpeed"),
			InitialRotationConstantOffsetPitch: row.GetFloat64("m_initialRotationConstantOffsetPitch"),
			InitialRotationConstantOffsetRoll:  row.GetFloat64("m_initialRotationConstantOffsetRoll"),
			InitialRotationConstantOffsetYaw:   row.GetFloat64("m_initialRotationConstantOffsetYaw"),
			InitialRotationMaxDeviation:        row.GetFloat64("m_initialRotationMaxDeviation"),
			RotationInterpolationSpeed:         row.GetFloat64("m_rotationInterpolationSpeed"),
			DeviationDuration:          row.GetFloat64("m_deviationDuration"),
			MaxRotDeviation:            row.GetFloat64("m_maxRotDeviation"),
			IncreaseUpdateRateWhenClosingIn:    row.GetBool("m_increaseUpdateRateWhenClosingIn"),
			IncreasedUpdateRateMinMassFactor:   row.GetFloat64("m_increasedUpdateRateMinMassFactor"),
			RotationInterpolationSpeedThrustersOff: row.GetFloat64("m_rotationInterpolationSpeedThrustersOff"),
			TargetInitialPosition:     row.GetBool("m_targetInitialPosition"),
			TargetLastPositionOnTargetDeath: row.GetBool("m_targetLastPositionOnTargetDeath"),
			Health:                    row.GetFloat64("m_health"),
			OldDataTable:              row.GetString("m_oldDataTable"),
			RowName:                   rowName,
		}

		projectilesByRowName[rowName] = projectile
	}

	projectilesLoaded = true
	log.Printf("Loaded %d projectiles from %s", len(projectilesByRowName), projectilesPath)
	return nil
}

// ProjectileByRowName returns the projectile stats for a given row name
func ProjectileByRowName(rowName string) (ProjectileStats, bool) {
	if !projectilesLoaded {
		if err := LoadProjectiles(); err != nil {
			log.Printf("Failed to load projectiles: %v", err)
			return ProjectileStats{}, false
		}
	}

	projectile, ok := projectilesByRowName[rowName]
	return projectile, ok
}

// AllProjectiles returns all loaded projectiles
func AllProjectiles() map[string]ProjectileStats {
	if !projectilesLoaded {
		if err := LoadProjectiles(); err != nil {
			log.Printf("Failed to load projectiles: %v", err)
			return make(map[string]ProjectileStats)
		}
	}

	// Return a copy to prevent modification
	copy := make(map[string]ProjectileStats)
	for k, v := range projectilesByRowName {
		copy[k] = v
	}
	return copy
}