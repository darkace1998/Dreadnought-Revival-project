package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// EnergyShieldStats represents an energy shield configuration from EnergyShields_DT.json
// G1: Define Go struct `EnergyShieldStats` matching `DN_EnergyShields_DT.json` fields
type EnergyShieldStats struct {
	// Core shield fields from DataTable (per-class shield damage modifiers, pass-through factors)
	StaticMesh          string  `json:"m_staticMesh"`
	DamageModifier     float64 `json:"m_damageModifier"`
	DamagePassThrough  float64 `json:"m_damagePassThroughFactor"`

	// Metadata
	ShieldName string `json:"-"` // Row name from DataTable
	ShipClass  string `json:"-"` // Extracted ship class from name
}

// energyShieldsData holds the loaded energy shield data
var (
	energyShields     = make(map[string]EnergyShieldStats)
	energyShieldsByID = make(map[string]EnergyShieldStats) // Keyed by shield name
	energyShieldsLock sync.RWMutex
	energyShieldsLoaded bool
)

// LoadEnergyShields loads energy shield data from EnergyShields_DT.json
// G1: Define Go structs for energy shield DataTable fields
func LoadEnergyShields() error {
	energyShieldsLock.Lock()
	defer energyShieldsLock.Unlock()

	if energyShieldsLoaded {
		return nil
	}

	// Path to the energy shields DataTable
	shieldsPath := DataTablePath(filepath.Join("EnergyShields_DT.json"))
	
	data, err := os.ReadFile(shieldsPath)
	if err != nil {
		log.Printf("Warning: Failed to load energy shields: %v", err)
		return err
	}

	var dataTable struct {
		Rows map[string]struct {
			StaticMesh         string  `json:"m_staticMesh"`
			DamageModifier    float64 `json:"m_damageModifier"`
			DamagePassThrough float64 `json:"m_damagePassThroughFactor"`
		} `json:"rows"`
		RowCount int `json:"row_count"`
	}

	if err := json.Unmarshal(data, &dataTable); err != nil {
		log.Printf("Warning: Failed to parse energy shields: %v", err)
		return err
	}

	loadedCount := 0
	for rowName, rowData := range dataTable.Rows {
		shield := EnergyShieldStats{
			StaticMesh:         rowData.StaticMesh,
			DamageModifier:    rowData.DamageModifier,
			DamagePassThrough: rowData.DamagePassThrough,
			ShieldName:        rowName,
			ShipClass:         extractShipClassFromShieldName(rowName),
		}

		energyShields[rowName] = shield
		energyShieldsByID[rowName] = shield
		loadedCount++
	}

	energyShieldsLoaded = true
	log.Printf("Loaded %d energy shields from %s", loadedCount, filepath.Base(shieldsPath))
	return nil
}

// extractShipClassFromShieldName extracts the ship class from shield name
func extractShipClassFromShieldName(name string) string {
	// Shield names follow pattern: {ShipClass}{Size} or {ShipType}
	// Examples: AssaultH, DreadM, ScoutL, SniperH, SupportM, TitanCarrier, CargoShip_Escort, DreadE
	
	// Handle special cases first
	switch name {
	case "TitanCarrier":
		return "TitanCarrier"
	case "CargoShip_Escort":
		return "CargoShip"
	case "DreadE":
		return "DreadnoughtE"
	}
	
	// Extract ship class by removing size suffix
	// Size suffixes: H (Heavy), M (Medium), L (Light)
	if len(name) > 1 {
		lastChar := name[len(name)-1]
		if lastChar == 'H' || lastChar == 'M' || lastChar == 'L' {
			baseName := name[:len(name)-1]
			// Special case for Dread -> Dreadnought
			if baseName == "Dread" {
				return "Dreadnought"
			}
			return baseName
		}
	}
	
	return name
}

// EnergyShieldByName returns an energy shield by its row name
func EnergyShieldByName(name string) (EnergyShieldStats, bool) {
	energyShieldsLock.RLock()
	defer energyShieldsLock.RUnlock()

	shield, exists := energyShields[name]
	return shield, exists
}

// AllEnergyShields returns all loaded energy shields
func AllEnergyShields() []EnergyShieldStats {
	energyShieldsLock.RLock()
	defer energyShieldsLock.RUnlock()

	shields := make([]EnergyShieldStats, 0, len(energyShields))
	for _, shield := range energyShields {
		shields = append(shields, shield)
	}
	return shields
}

// EnergyShieldCount returns the number of loaded energy shields
func EnergyShieldCount() int {
	energyShieldsLock.RLock()
	defer energyShieldsLock.RUnlock()
	return len(energyShields)
}

// AllEnergyShieldNames returns all energy shield names
func AllEnergyShieldNames() []string {
	energyShieldsLock.RLock()
	defer energyShieldsLock.RUnlock()

	names := make([]string, 0, len(energyShields))
	for name := range energyShields {
		names = append(names, name)
	}
	return names
}

// EnergyShieldsForShipClass returns all energy shields for a specific ship class
func EnergyShieldsForShipClass(shipClass string) []EnergyShieldStats {
	energyShieldsLock.RLock()
	defer energyShieldsLock.RUnlock()

	var shields []EnergyShieldStats
	for _, shield := range energyShields {
		if shield.ShipClass == shipClass {
			shields = append(shields, shield)
		}
	}
	return shields
}