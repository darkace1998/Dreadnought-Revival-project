package dreadgameconfig

import (
	"log"
	"sync"
	"time"
)

// GameBalanceConfig holds game balance values that can be used by matchmaking and other systems
// G5: Expose tuning values for use by matchmaking and game balance calculations
type GameBalanceConfig struct {
	// AFK and session timing
	AFKTimer time.Duration
	
	// Visibility and detection ranges
	RangeToViewTargetMarkerForClassReveal float64
	
	// Projectile physics
	ProjectileCloseInProjectileSpeedModifier float64
	
	// Energy shield configurations by ship class
	EnergyShieldModifiers map[string]EnergyShieldStats
}

// gameBalanceConfig holds the computed game balance configuration
var (
	gameBalanceConfig     GameBalanceConfig
	gameBalanceConfigLock sync.RWMutex
	gameBalanceConfigLoaded bool
)

// LoadGameBalanceConfig loads and computes game balance values from tuning data
// G5: Expose tuning values for use by matchmaking and game balance calculations
func LoadGameBalanceConfig() {
	gameBalanceConfigLock.Lock()
	defer gameBalanceConfigLock.Unlock()

	if gameBalanceConfigLoaded {
		return
	}

	// Load energy shields if not already loaded
	if !energyShieldsLoaded {
		LoadEnergyShields()
	}

	// Load global tuning if not already loaded  
	if !globalTuningLoaded {
		LoadGlobalTuningValues()
	}

	// Get the default global tuning values
	defaultTuning, exists := GlobalTuningByName("Default")
	if !exists {
		// Fallback to default values if tuning not found
		defaultTuning = GlobalTuning{
			RangeToViewTargetMarkerForClassReveal:    20000.0,
			ProjectileCloseInProjectileSpeedModifier: 0.5,
			AFKTimer:                                      119.5,
			TuningName:                                   "Default",
		}
	}

	// Build energy shield modifiers by ship class
	shieldModifiers := make(map[string]EnergyShieldStats)
	allShields := AllEnergyShields()
	for _, shield := range allShields {
		shieldModifiers[shield.ShipClass] = shield
	}

	// Convert AFK timer from float64 (seconds) to time.Duration
	afkDuration := time.Duration(defaultTuning.AFKTimer * float64(time.Second))

	gameBalanceConfig = GameBalanceConfig{
		AFKTimer:                              afkDuration,
		RangeToViewTargetMarkerForClassReveal: defaultTuning.RangeToViewTargetMarkerForClassReveal,
		ProjectileCloseInProjectileSpeedModifier: defaultTuning.ProjectileCloseInProjectileSpeedModifier,
		EnergyShieldModifiers:                shieldModifiers,
	}

	gameBalanceConfigLoaded = true
	log.Printf("Loaded game balance config: AFKTimer=%v, RangeToView=%.0f, ProjectileSpeedModifier=%.2f, ShieldClasses=%d",
		afkDuration, defaultTuning.RangeToViewTargetMarkerForClassReveal, 
		defaultTuning.ProjectileCloseInProjectileSpeedModifier, len(shieldModifiers))
}

// GetGameBalanceConfig returns the loaded game balance configuration
func GetGameBalanceConfig() GameBalanceConfig {
	gameBalanceConfigLock.RLock()
	defer gameBalanceConfigLock.RUnlock()
	
	if !gameBalanceConfigLoaded {
		LoadGameBalanceConfig()
	}
	return gameBalanceConfig
}

// GetAFKDuration returns the AFK timer as a time.Duration for use by session management
func GetAFKDuration() time.Duration {
	config := GetGameBalanceConfig()
	return config.AFKTimer
}

// GetRangeToViewTargetMarker returns the range to view target marker for class reveal
func GetRangeToViewTargetMarker() float64 {
	config := GetGameBalanceConfig()
	return config.RangeToViewTargetMarkerForClassReveal
}

// GetProjectileCloseInSpeedModifier returns the projectile close-in speed modifier
func GetProjectileCloseInSpeedModifier() float64 {
	config := GetGameBalanceConfig()
	return config.ProjectileCloseInProjectileSpeedModifier
}

// GetEnergyShieldModifier returns the energy shield modifier for a specific ship class
func GetEnergyShieldModifier(shipClass string) (EnergyShieldStats, bool) {
	config := GetGameBalanceConfig()
	modifier, exists := config.EnergyShieldModifiers[shipClass]
	return modifier, exists
}

// GetEnergyShieldDamageModifier returns the damage modifier for a specific ship class
func GetEnergyShieldDamageModifier(shipClass string) float64 {
	modifier, exists := GetEnergyShieldModifier(shipClass)
	if exists {
		return modifier.DamageModifier
	}
	// Return default damage modifier if ship class not found
	return 0.0
}

// GetEnergyShieldPassThrough returns the pass-through factor for a specific ship class
func GetEnergyShieldPassThrough(shipClass string) float64 {
	modifier, exists := GetEnergyShieldModifier(shipClass)
	if exists {
		return modifier.DamagePassThrough
	}
	// Return default pass-through factor if ship class not found
	return 0.0
}