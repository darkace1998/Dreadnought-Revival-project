package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// HavocUnlockable represents a Havoc unlockable from DN_HavocUnlockables_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocUnlockable struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	IconPath    string `json:"m_iconPath"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// havocUnlockablesData holds the loaded Havoc unlockable data
var (
	havocUnlockables     []HavocUnlockable
	havocUnlockablesMu   sync.RWMutex
	havocUnlockablesOnce sync.Once
	havocUnlockablesLoaded bool
)

// LoadHavocUnlockables loads Havoc unlockable data from DN_HavocUnlockables_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocUnlockables() error {
	var loadErr error
	havocUnlockablesOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "Progression", "Havoc", "DN_HavocUnlockables_DT.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocUnlockables_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocUnlockables_DT.json: %w", err)
			return
		}

		// Parse rows into HavocUnlockable structs
		unlockables := make([]HavocUnlockable, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			unlockable := HavocUnlockable{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				IconPath:    rowData.GetString("m_iconPath"),
				RowName:     rowName,
			}
			unlockables = append(unlockables, unlockable)
		}

		if len(unlockables) == 0 {
			loadErr = fmt.Errorf("no Havoc unlockables found in DN_HavocUnlockables_DT.json")
			return
		}

		havocUnlockables = unlockables
		havocUnlockablesLoaded = true
		log.Printf("Loaded %d Havoc unlockables from DN_HavocUnlockables_DT.json", len(unlockables))
	})

	return loadErr
}

// AllHavocUnlockables returns all loaded Havoc unlockables
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocUnlockables() []HavocUnlockable {
	havocUnlockablesMu.RLock()
	defer havocUnlockablesMu.RUnlock()
	
	if !havocUnlockablesLoaded {
		if err := LoadHavocUnlockables(); err != nil {
			log.Printf("Warning: Failed to load Havoc unlockables: %v", err)
			return nil
		}
	}

	return havocUnlockables
}

// HavocUnlockableByRowName returns the Havoc unlockable with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocUnlockableByRowName(rowName string) (HavocUnlockable, bool) {
	havocUnlockablesMu.RLock()
	defer havocUnlockablesMu.RUnlock()
	
	if !havocUnlockablesLoaded {
		if err := LoadHavocUnlockables(); err != nil {
			log.Printf("Warning: Failed to load Havoc unlockables: %v", err)
			return HavocUnlockable{}, false
		}
	}

	for _, unlockable := range havocUnlockables {
		if unlockable.RowName == rowName {
			return unlockable, true
		}
	}
	return HavocUnlockable{}, false
}

// HavocUnlockableCount returns the total number of Havoc unlockables
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocUnlockableCount() int {
	havocUnlockablesMu.RLock()
	defer havocUnlockablesMu.RUnlock()
	
	if !havocUnlockablesLoaded {
		if err := LoadHavocUnlockables(); err != nil {
			log.Printf("Warning: Failed to load Havoc unlockables: %v", err)
			return 0
		}
	}

	return len(havocUnlockables)
}

// HavocUnlockableRowNames returns all row names of Havoc unlockables
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocUnlockableRowNames() []string {
	havocUnlockablesMu.RLock()
	defer havocUnlockablesMu.RUnlock()
	
	if !havocUnlockablesLoaded {
		if err := LoadHavocUnlockables(); err != nil {
			log.Printf("Warning: Failed to load Havoc unlockables: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocUnlockables))
	for i, unlockable := range havocUnlockables {
		names[i] = unlockable.RowName
	}
	return names
}