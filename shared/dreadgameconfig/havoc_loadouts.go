package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// HavocLoadout represents a Havoc loadout from DN_HavocLoadouts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocLoadout struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	IconPath    string `json:"m_iconPath"`
	ShipID      string `json:"m_shipID"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// havocLoadoutsData holds the loaded Havoc loadout data
var (
	havocLoadouts     []HavocLoadout
	havocLoadoutsMu   sync.RWMutex
	havocLoadoutsOnce sync.Once
	havocLoadoutsLoaded bool
)

// LoadHavocLoadouts loads Havoc loadout data from DN_HavocLoadouts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocLoadouts() error {
	var loadErr error
	havocLoadoutsOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "Havoc", "DN_HavocLoadouts_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocLoadouts_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocLoadouts_DT.json: %w", err)
			return
		}

		// Parse rows into HavocLoadout structs
		loadouts := make([]HavocLoadout, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			loadout := HavocLoadout{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				IconPath:    rowData.GetString("m_iconPath"),
				ShipID:      rowData.GetString("m_shipID"),
				RowName:     rowName,
			}
			loadouts = append(loadouts, loadout)
		}

		if len(loadouts) == 0 {
			loadErr = fmt.Errorf("no Havoc loadouts found in DN_HavocLoadouts_DT.json")
			return
		}

		havocLoadouts = loadouts
		havocLoadoutsLoaded = true
		log.Printf("Loaded %d Havoc loadouts from DN_HavocLoadouts_DT.json", len(loadouts))
	})

	return loadErr
}

// AllHavocLoadouts returns all loaded Havoc loadouts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocLoadouts() []HavocLoadout {
	havocLoadoutsMu.RLock()
	defer havocLoadoutsMu.RUnlock()
	
	if !havocLoadoutsLoaded {
		if err := LoadHavocLoadouts(); err != nil {
			log.Printf("Warning: Failed to load Havoc loadouts: %v", err)
			return nil
		}
	}

	return havocLoadouts
}

// HavocLoadoutByRowName returns the Havoc loadout with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocLoadoutByRowName(rowName string) (HavocLoadout, bool) {
	havocLoadoutsMu.RLock()
	defer havocLoadoutsMu.RUnlock()
	
	if !havocLoadoutsLoaded {
		if err := LoadHavocLoadouts(); err != nil {
			log.Printf("Warning: Failed to load Havoc loadouts: %v", err)
			return HavocLoadout{}, false
		}
	}

	for _, loadout := range havocLoadouts {
		if loadout.RowName == rowName {
			return loadout, true
		}
	}
	return HavocLoadout{}, false
}

// HavocLoadoutCount returns the total number of Havoc loadouts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocLoadoutCount() int {
	havocLoadoutsMu.RLock()
	defer havocLoadoutsMu.RUnlock()
	
	if !havocLoadoutsLoaded {
		if err := LoadHavocLoadouts(); err != nil {
			log.Printf("Warning: Failed to load Havoc loadouts: %v", err)
			return 0
		}
	}

	return len(havocLoadouts)
}

// HavocLoadoutRowNames returns all row names of Havoc loadouts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocLoadoutRowNames() []string {
	havocLoadoutsMu.RLock()
	defer havocLoadoutsMu.RUnlock()
	
	if !havocLoadoutsLoaded {
		if err := LoadHavocLoadouts(); err != nil {
			log.Printf("Warning: Failed to load Havoc loadouts: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocLoadouts))
	for i, loadout := range havocLoadouts {
		names[i] = loadout.RowName
	}
	return names
}