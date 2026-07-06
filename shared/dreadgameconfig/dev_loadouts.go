package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// DevLoadout represents a development loadout definition from LoadoutDevelopmentTable.json
// Contains weapon, ability, and perk slot ItemIDs for hero ships
type DevLoadout struct {
	ID               string `json:"ID"`
	ShipID           int32  `json:"ShipID"`
	Name             string `json:"Name"`
	Class            int32  `json:"Class"`
	WeaponPrimary    int32  `json:"WeaponPrimary"`
	WeaponSecondary  int32  `json:"WeaponSecondary"`
	AbilityPrimary   int32  `json:"AbilityPrimary"`
	AbilitySecondary int32  `json:"AbilitySecondary"`
	AbilityPerimeter int32  `json:"AbilityPerimeter"`
	AbilityInternal  int32  `json:"AbilityInternal"`
	PerkCom          int32  `json:"PerkCom"`
	PerkWeapon       int32  `json:"PerkWeapon"`
	PerkNavigation   int32  `json:"PerkNavigation"`
	PerkEngineer     int32  `json:"PerkEngineer"`
	DisplayInfo      string `json:"DisplayInfo"`
}

// devLoadouts stores all loaded development loadouts
var (
	devLoadouts     []DevLoadout
	devLoadoutsMu   sync.RWMutex
	devLoadoutsOnce sync.Once
)

// LoadDevLoadouts loads the LoadoutDevelopmentTable.json file
func LoadDevLoadouts() error {
	var loadErr error
	devLoadoutsOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "loadouts", "LoadoutDevelopmentTable.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read LoadoutDevelopmentTable.json: %w", err)
			return
		}

		var table struct {
			LoadoutTable []DevLoadout `json:"LoadoutTable"`
		}
		
		if err := json.Unmarshal(data, &table); err != nil {
			loadErr = fmt.Errorf("failed to parse LoadoutDevelopmentTable.json: %w", err)
			return
		}

		devLoadoutsMu.Lock()
		devLoadouts = table.LoadoutTable
		devLoadoutsMu.Unlock()

		log.Printf("Loaded %d development loadouts from LoadoutDevelopmentTable.json", len(devLoadouts))
	})

	return loadErr
}

// GetAllDevLoadouts returns all development loadouts
func GetAllDevLoadouts() []DevLoadout {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	result := make([]DevLoadout, len(devLoadouts))
	copy(result, devLoadouts)
	return result
}

// GetDevLoadoutByShipID returns the development loadout for a specific ship
func GetDevLoadoutByShipID(shipID int32) (*DevLoadout, bool) {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	for i := range devLoadouts {
		if devLoadouts[i].ShipID == shipID {
			return &devLoadouts[i], true
		}
	}
	return nil, false
}

// GetDevLoadoutByID returns the development loadout by its ID
func GetDevLoadoutByID(id string) (*DevLoadout, bool) {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	for i := range devLoadouts {
		if devLoadouts[i].ID == id {
			return &devLoadouts[i], true
		}
	}
	return nil, false
}

// GetDevLoadoutCount returns the total number of development loadouts
func GetDevLoadoutCount() int {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	return len(devLoadouts)
}

// GetAllDevLoadoutShipIDs returns all ship IDs that have development loadouts
func GetAllDevLoadoutShipIDs() []int32 {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	shipIDs := make([]int32, len(devLoadouts))
	for i := range devLoadouts {
		shipIDs[i] = devLoadouts[i].ShipID
	}
	return shipIDs
}

// HasDevLoadout checks if a ship has a development loadout
func HasDevLoadout(shipID int32) bool {
	_, exists := GetDevLoadoutByShipID(shipID)
	return exists
}

// GetDevLoadoutsByClass returns all development loadouts for a specific ship class
func GetDevLoadoutsByClass(class int32) []DevLoadout {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	var result []DevLoadout
	for _, loadout := range devLoadouts {
		if loadout.Class == class {
			result = append(result, loadout)
		}
	}
	return result
}
