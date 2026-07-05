package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AbilityStats represents an ability from the DataTable with all its configuration fields
type AbilityStats struct {
	// Core ability identification
	AbilityName string `json:"m_abilityName"`
	TriggerName string `json:"m_triggerName"`
	
	// Item and asset information (E3: Cross-reference with ItemIDRegister)
	ItemID    int32  `json:"-"` // Cross-referenced from ItemIDRegister
	AssetPath string `json:"-"` // Cross-referenced from ItemIDRegister
	
	// Timing and cooldown
	CoolDown        float64 `json:"m_coolDown"`
	CoolDownTime    float64 `json:"m_coolDownTime"`
	ActiveTime      float64 `json:"m_activeTime"`
	RefireCoolDown  float64 `json:"m_refireCoolDown"`
	RefireTime      float64 `json:"m_refireTime"`
	DeploymentTime  float64 `json:"m_deploymentTime"`
	LifeTime        float64 `json:"m_lifeTime"`
	LifetimeAfterOwnerDeath float64 `json:"m_lifetimeAfterOwnerDeath"`
	
	// Damage and healing
	AbilityDamage      float64 `json:"m_abilityDamage"`
	DamageAmount       float64 `json:"m_damageAmount"`
	DamageOnEnemy      float64 `json:"m_damageOnEnemy"`
	PulseDamage        float64 `json:"m_pulseDamage"`
	MaxDamage          float64 `json:"m_maxDamage"`
	HealingRadius      float64 `json:"m_healingRadius"`
	LifePerInterval    float64 `json:"m_lifePerInterval"`
	LifeRegenInterval  float64 `json:"m_lifeRegenInterval"`
	
	// Range and targeting
	FireRange          float64 `json:"m_fireRange"`
	MaxRange           float64 `json:"m_maxRange"`
	MinRange           float64 `json:"m_minRange"`
	MaxDistance        float64 `json:"m_maxDistance"`
	MinDistance        float64 `json:"m_minDistance"`
	MaxTargetDistance  float64 `json:"m_maxTargetDistance"`
	EngageInDistance    float64 `json:"m_engageInDistance"`
	EngageOutDistance   float64 `json:"m_engageOutDistance"`
	ActivateRadius     float64 `json:"m_activateRadius"`
	DamageRadius       float64 `json:"m_damageRadius"`
	ProximityDistance  float64 `json:"m_proximityDistance"`
	OverlapRadius      float64 `json:"m_overlapRadius"`
	
	// Projectile and weapon configuration
	ProjectilesPerShoot    int     `json:"m_projectilesPerShoot"`
	AmmoMagazinSize        int     `json:"m_ammoMagazinSize"`
	MaxFiringWeapons       int     `json:"m_maxFiringWeapons"`
	MaxTargets            int     `json:"m_maxTargets"`
	MaxConcurrentActors   int     `json:"m_maxConcurrentActors"`
	NumberOfAttackRuns    int     `json:"m_numberOfAttackRuns"`
	NumberOfPulses        int     `json:"m_numberOfPulses"`
	
	// Movement and physics
	InitialSpeed           float64 `json:"m_initialSpeed"`
	MaxFlightSpeed        float64 `json:"m_maxFlightSpeed"`
	CruiseTargetSpeed      float64 `json:"m_cruiseTargetSpeed"`
	Mass                  float64 `json:"m_mass"`
	GravityScale          float64 `json:"m_gravityScale"`
	HitImpactForce        float64 `json:"m_hitImpactForce"`
	FireRecoilForce       float64 `json:"m_fireRecoilForce"`
	ThrusterForce         float64 `json:"m_thrusterForce"`
	ThrusterDuration       float64 `json:"m_thrusterDuration"`
	ThrusterStartDelay     float64 `json:"m_thrusterStartDelay"`
	
	// Rotation and steering
	MaxRotDeviation       float64 `json:"m_maxRotDeviation"`
	MaxAngle              float64 `json:"m_maxAngle"`
	RotationInterpolationSpeed float64 `json:"m_rotationInterpolationSpeed"`
	RotationInterpolationSpeedThrustersOff float64 `json:"m_rotationInterpolationSpeedThrustersOff"`
	SteeringDuration       float64 `json:"m_steeringDuration"`
	SteeringFPS           float64 `json:"m_steeringFPS"`
	SteeringStartDelay    float64 `json:"m_steeringStartDelay"`
	
	// Targeting and locking
	TargetingType         string  `json:"m_targetingType"`
	SpecifyTarget        bool    `json:"m_specifyTarget"`
	TargetFriendlies     bool    `json:"m_targetFriendlies"`
	TargetInitialPosition bool   `json:"m_targetInitialPosition"`
	LockOnTime           float64 `json:"m_lockOnTime"`
	TargetLockTime       float64 `json:"m_targetLockTime"`
	MaxLockAngle         float64 `json:"m_maxLockAngle"`
	
	// Area and volume
	MaxTravelDistance    float64 `json:"m_maxTravelDistance"`
	MaxFireDistance      float64 `json:"m_maxFireDistance"`
	VolleyTime           float64 `json:"m_volleyTime"`
	
	// Health and status
	Health              float64 `json:"m_health"`
	MaxHealth           float64 `json:"m_maxHealth"`
	ShieldDisableTime   float64 `json:"m_shieldDisableTime"`
	
	// Projectile timing
	ProjectileFiringDelayMin float64 `json:"m_projectileFiringDelayMin"`
	ProjectileFiringDelayMax float64 `json:"m_projectileFiringDelayMax"`
	InternalFiringDelayMin   float64 `json:"m_internalFiringDelayMin"`
	InternalFiringDelayMax   float64 `json:"m_internalFiringDelayMax"`
	
	// Context and actions
	ContextActionType      string  `json:"m_contextActionType"`
	ContextActionStartTime float64 `json:"m_contextActionStartTime"`
	ContextActionEndTime   float64 `json:"m_contextActionEndTime"`
	
	// Special behavior flags
	CancelOnCollision        bool `json:"m_CancelOnCollision"`
	DetonateOnProximity      bool `json:"m_detonateOnProximity"`
	DisableControls          bool `json:"m_disableControls"`
	DisableStabilisationSystem bool `json:"m_disableStabilisationSystem"`
	NotifyEnemiesOnActivate  bool `json:"m_notifyEnemiesOnActivate"`
	OnlyWarpOnTargets        bool `json:"m_onlyWarpOnTargets"`
	ProximityCheckAgainstCreep bool `json:"m_proximityCheckAgainstCreep"`
	OverwriteRigidBodyErrorCorrection bool `json:"m_overwriteRigidBodyErrorCorrection"`
	TargetLockRequired       bool `json:"m_targetLockRequired"`
	
	// Warp and movement
	WarpOnDirection      bool    `json:"m_warpOnDirection"`
	WarpWarmUpTime       float64 `json:"m_warpWarmUpTime"`
	
	// Boost and scaling
	BoostMultiplyer      float64 `json:"m_boostMultiplyer"`
	
	// Damage thresholds and modifiers
	DamageActivateThreshold float64 `json:"m_damageActivateThreshold"`
	DamageAmountOnPath     float64 `json:"m_damageAmountOnPath"`
	DealDamageOnPath       bool    `json:"m_dealDamageOnPath"`
	PathDamageRadiusModifer float64 `json:"m_pathDamageRadiusModifer"`
	
	// Weapon and firing
	PrimaryWeaponFireAngle float64 `json:"m_primaryWeaponFireAngle"`
	RefreshWeaponDirectionInterval float64 `json:"m_refreshWeaponDirectionInterval"`
	RefreshWeaponDirectionSubinterval float64 `json:"m_refreshWeaponDirectionSubinterval"`
	
	// Drone and AI
	DroneTimeToLive        float64 `json:"m_droneTimeToLive"`
	DroneTimeToLiveOwnerDeath float64 `json:"m_droneTimeToLiveOwnerDeath"`
	
	// Initial rotation
	InitialRotationConstantOffsetPitch float64 `json:"m_initialRotationConstantOffsetPitch"`
	InitialRotationConstantOffsetRoll  float64 `json:"m_initialRotationConstantOffsetRoll"`
	InitialRotationConstantOffsetYaw   float64 `json:"m_initialRotationConstantOffsetYaw"`
	InitialRotationDelay                float64 `json:"m_initialRotationDelay"`
	InitialRotationInterpolationSpeed   float64 `json:"m_initialRotationInterpolationSpeed"`
	InitialRotationMaxDeviation         float64 `json:"m_initialRotationMaxDeviation"`
	
	// Start velocity
	StartVelocityRotationOffsetPitch float64 `json:"m_startVelocityRotationOffsetPitch"`
	StartVelocityRotationOffsetRoll  float64 `json:"m_startVelocityRotationOffsetRoll"`
	StartVelocityRotationOffsetYaw   float64 `json:"m_startVelocityRotationOffsetYaw"`
	InheritStartVelocity             bool    `json:"m_inheritStartVelocity"`
	
	// Spread and search
	SpreadValue            float64 `json:"m_spreadValue"`
	SearchSpreadMultiplier float64 `json:"m_searchSpreadMultiplier"`
	
	// Pulse configuration
	PulseSpeed float64 `json:"m_pulseSpeed"`
	
	// Break distance
	BreakDistanceToPlayer float64 `json:"m_breakDistanceToPlayer"`
	
	// Instigator ignore
	InstigatorIgnoreTime float64 `json:"m_instigatorIgnoreTime"`
	
	// Increase update rate
	IncreaseUpdateRateWhenClosingIn bool `json:"m_increaseUpdateRateWhenClosingIn"`
	IncreasedUpdateRateMinMassFactor float64 `json:"m_increasedUpdateRateMinMassFactor"`
	
	// Impact point interpolation
	ImpactPointInterpolationRate float64 `json:"m_impactPointInterpolationRate"`
	
	// Trigger countdown
	TriggerCountDown float64 `json:"m_triggerCountDown"`
	
	// Waypoint proximity
	WaypointProximity float64 `json:"m_waypointProximity"`
	
	// Top target speed
	TopTargetSpeed float64 `json:"m_topTargetSpeed"`
}

// abilitiesData holds the loaded ability data
var (
	abilities     = make(map[string]AbilityStats)
	abilitiesByType = make(map[string][]AbilityStats)
	abilitiesLock sync.RWMutex
	abilitiesLoaded bool
	abilityCount int
)

// LoadAbilities loads all ability data from DataTables (E2)
// Also cross-references with ItemIDRegister to resolve ItemID→AssetPath (E3)
func LoadAbilities() error {
	abilitiesLock.Lock()
	defer abilitiesLock.Unlock()
	
	if abilitiesLoaded {
		return nil
	}
	
	// Load ItemIDRegister for cross-referencing (E3)
	itemIDRegister, err := loadItemIDRegister()
	if err != nil {
		log.Printf("Warning: Failed to load ItemIDRegister for abilities: %v", err)
		// Continue without ItemID cross-referencing
	}
	
	// Find all ability files
	abilityDir := DataTablePath(filepath.Join("Abilities"))
	entries, err := os.ReadDir(abilityDir)
	if err != nil {
		return fmt.Errorf("read Abilities directory: %w", err)
	}
	
	loadedCount := 0
	fileCount := 0
	crossReferencedCount := 0
	
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		
		fileCount++
		filePath := filepath.Join(abilityDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read ability file %s: %w", entry.Name(), err)
		}
		
		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			return fmt.Errorf("parse ability file %s: %w", entry.Name(), err)
		}
		
		// Extract ability type from filename for categorization
		abilityType := extractAbilityTypeFromFilename(entry.Name())
		
		for rowName, row := range dt.Rows {
			ability, err := parseAbilityRow(row)
			if err != nil {
				return fmt.Errorf("parse ability row %s in %s: %w", rowName, entry.Name(), err)
			}
			
			// Cross-reference with ItemIDRegister (E3)
			if itemIDRegister != nil {
				assetPath := tryFindAbilityAssetPath(rowName, itemIDRegister)
				if assetPath != "" {
					ability.AssetPath = assetPath
					// Try to find corresponding ItemID
					for _, regEntry := range itemIDRegister {
						if regEntry.Path == assetPath {
							ability.ItemID = regEntry.ItemID
							crossReferencedCount++
							break
						}
					}
				}
			}
			
			// Use composite name: filename_rowname
			fullName := fmt.Sprintf("%s_%s", filepath.Base(entry.Name()[:len(entry.Name())-5]), rowName)
			// Remove _OTS_DT or _DT suffix from filename
			fullName = strings.ReplaceAll(fullName, "_OTS_DT_", "_")
			fullName = strings.ReplaceAll(fullName, "_DT_", "_")
			abilities[fullName] = ability
			
			// Categorize by type
			if abilityType != "" {
				abilitiesByType[abilityType] = append(abilitiesByType[abilityType], ability)
			}
			loadedCount++
		}
	}
	
	abilitiesLoaded = true
	abilityCount = loadedCount
	log.Printf("Loaded %d abilities from %d files (%d cross-referenced with ItemIDRegister)", loadedCount, fileCount, crossReferencedCount)
	return nil
}

// tryFindAbilityAssetPath attempts to find the asset path for an ability row name
func tryFindAbilityAssetPath(rowName string, itemIDRegister []ItemIDRegisterEntry) string {
	// Try to match the row name with asset paths in ItemIDRegister
	// Ability row names typically look like: AB_AS_Int_Buff_AbInc_Ability_T5_BP
	// Asset paths typically look like: /Game/Generic/Abilities/Assault/Int_Buff_AbInc/T5/AB_AS_Int_Buff_AbInc_Ability_T5_BP
	
	for _, entry := range itemIDRegister {
		if strings.Contains(entry.Path, "Abilities") && strings.Contains(entry.Path, rowName) {
			return entry.Path
		}
	}
	return ""
}

// extractAbilityTypeFromFilename extracts the ability type from the filename
// Examples: "DN_AbilityBroadside_OTS_DT.json" -> "AbilityBroadside"
//           "DN_Projectile_OTS_DT.json" -> "Projectile"
//           "DN_WeaponBroadside_OTS_DT.json" -> "WeaponBroadside"
func extractAbilityTypeFromFilename(filename string) string {
	// Remove extension
	baseName := strings.TrimSuffix(filename, ".json")
	
	// Remove common prefixes and suffixes
	baseName = strings.TrimPrefix(baseName, "DN_")
	baseName = strings.TrimSuffix(baseName, "_OTS_DT")
	baseName = strings.TrimSuffix(baseName, "_DT")
	
	return baseName
}

// parseAbilityRow parses a single ability row from DataTable
func parseAbilityRow(row Row) (AbilityStats, error) {
	var ability AbilityStats
	
	// Marshal and unmarshal to handle the dynamic structure
	rowData, err := json.Marshal(row)
	if err != nil {
		return ability, fmt.Errorf("marshal row: %w", err)
	}
	
	if err := json.Unmarshal(rowData, &ability); err != nil {
		return ability, fmt.Errorf("unmarshal row: %w", err)
	}
	
	return ability, nil
}

// AbilityByID returns an ability by its full name (E4)
func AbilityByID(id string) (AbilityStats, bool) {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	ability, ok := abilities[id]
	return ability, ok
}

// AllAbilities returns all loaded abilities (E4)
func AllAbilities() map[string]AbilityStats {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	// Return a copy to avoid race conditions
	abilitiesCopy := make(map[string]AbilityStats, len(abilities))
	for k, v := range abilities {
		abilitiesCopy[k] = v
	}
	return abilitiesCopy
}

// AbilitiesByType returns all abilities of a specific type (E2 enhancement)
func AbilitiesByType(abilityType string) []AbilityStats {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	// Return a copy to avoid race conditions
	if abilities, ok := abilitiesByType[abilityType]; ok {
		abilitiesCopy := make([]AbilityStats, len(abilities))
		copy(abilitiesCopy, abilities)
		return abilitiesCopy
	}
	return nil
}

// AllAbilityTypes returns all ability types that have been loaded
func AllAbilityTypes() []string {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	types := make([]string, 0, len(abilitiesByType))
	for abilityType := range abilitiesByType {
		types = append(types, abilityType)
	}
	return types
}

// AbilityCount returns the total number of abilities loaded
func AbilityCount() int {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	return abilityCount
}

// AbilityByItemID returns an ability by its ItemID (E3)
func AbilityByItemID(itemID int32) (AbilityStats, bool) {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	for _, ability := range abilities {
		if ability.ItemID == itemID {
			return ability, true
		}
	}
	return AbilityStats{}, false
}

// AbilityAssetPathByID returns the asset path for an ability by its composite ID
func AbilityAssetPathByID(id string) (string, bool) {
	ability, ok := AbilityByID(id)
	if !ok {
		return "", false
	}
	return ability.AssetPath, ability.AssetPath != ""
}

// AbilityIDs returns all ability IDs that have been loaded
func AbilityIDs() []string {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	ids := make([]string, 0, len(abilities))
	for id := range abilities {
		ids = append(ids, id)
	}
	return ids
}

// FilterAbilitiesByName returns abilities whose names contain the given substring (case-insensitive)
func FilterAbilitiesByName(nameSubstring string) []AbilityStats {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	var result []AbilityStats
	lowerSubstring := strings.ToLower(nameSubstring)
	
	for _, ability := range abilities {
		if strings.Contains(strings.ToLower(ability.AbilityName), lowerSubstring) {
			result = append(result, ability)
		}
	}
	return result
}

// FilterAbilitiesByCooldown returns abilities with cooldown within the specified range
func FilterAbilitiesByCooldown(minCooldown, maxCooldown float64) []AbilityStats {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	var result []AbilityStats
	
	for _, ability := range abilities {
		if ability.CoolDown >= minCooldown && ability.CoolDown <= maxCooldown {
			result = append(result, ability)
		}
	}
	return result
}

// FilterAbilitiesByDamage returns abilities with damage within the specified range
func FilterAbilitiesByDamage(minDamage, maxDamage float64) []AbilityStats {
	abilitiesLock.RLock()
	defer abilitiesLock.RUnlock()
	
	var result []AbilityStats
	
	for _, ability := range abilities {
		// Check various damage fields
		damage := ability.AbilityDamage
		if damage == 0 {
			damage = ability.DamageAmount
		}
		if damage == 0 {
			damage = ability.MaxDamage
		}
		if damage >= minDamage && damage <= maxDamage {
			result = append(result, ability)
		}
	}
	return result
}