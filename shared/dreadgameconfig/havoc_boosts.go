package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// HavocBoost represents a Havoc boost from DN_HavocBoosts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocBoost struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	Feats       string `json:"m_feats"`
	IconPath    string `json:"m_iconPath"`
	Weight      int32  `json:"m_weight"`
	Cost        int32  `json:"m_cost"`
	Category    string `json:"m_category"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// havocBoostsData holds the loaded Havoc boost data
var (
	havocBoosts     []HavocBoost
	havocBoostsMu   sync.RWMutex
	havocBoostsOnce sync.Once
	havocBoostsLoaded bool
)

// LoadHavocBoosts loads Havoc boost data from DN_HavocBoosts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocBoosts() error {
	var loadErr error
	havocBoostsOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "Progression", "Havoc", "DN_HavocBoosts_DT.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocBoosts_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocBoosts_DT.json: %w", err)
			return
		}

		// Parse rows into HavocBoost structs
		boosts := make([]HavocBoost, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			boost := HavocBoost{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				Feats:       rowData.GetString("m_feats"),
				IconPath:    rowData.GetString("m_iconPath"),
				Weight:      rowData.GetInt32("m_weight"),
				Cost:        rowData.GetInt32("m_cost"),
				Category:    rowData.GetString("m_category"),
				RowName:     rowName,
			}
			boosts = append(boosts, boost)
		}

		if len(boosts) == 0 {
			loadErr = fmt.Errorf("no Havoc boosts found in DN_HavocBoosts_DT.json")
			return
		}

		havocBoosts = boosts
		havocBoostsLoaded = true
		log.Printf("Loaded %d Havoc boosts from DN_HavocBoosts_DT.json", len(boosts))
	})

	return loadErr
}

// AllHavocBoosts returns all loaded Havoc boosts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocBoosts() []HavocBoost {
	havocBoostsMu.RLock()
	defer havocBoostsMu.RUnlock()
	
	if !havocBoostsLoaded {
		if err := LoadHavocBoosts(); err != nil {
			log.Printf("Warning: Failed to load Havoc boosts: %v", err)
			return nil
		}
	}

	return havocBoosts
}

// HavocBoostByRowName returns the Havoc boost with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBoostByRowName(rowName string) (HavocBoost, bool) {
	havocBoostsMu.RLock()
	defer havocBoostsMu.RUnlock()
	
	if !havocBoostsLoaded {
		if err := LoadHavocBoosts(); err != nil {
			log.Printf("Warning: Failed to load Havoc boosts: %v", err)
			return HavocBoost{}, false
		}
	}

	for _, boost := range havocBoosts {
		if boost.RowName == rowName {
			return boost, true
		}
	}
	return HavocBoost{}, false
}

// HavocBoostCount returns the total number of Havoc boosts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBoostCount() int {
	havocBoostsMu.RLock()
	defer havocBoostsMu.RUnlock()
	
	if !havocBoostsLoaded {
		if err := LoadHavocBoosts(); err != nil {
			log.Printf("Warning: Failed to load Havoc boosts: %v", err)
			return 0
		}
	}

	return len(havocBoosts)
}

// HavocBoostRowNames returns all row names of Havoc boosts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBoostRowNames() []string {
	havocBoostsMu.RLock()
	defer havocBoostsMu.RUnlock()
	
	if !havocBoostsLoaded {
		if err := LoadHavocBoosts(); err != nil {
			log.Printf("Warning: Failed to load Havoc boosts: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocBoosts))
	for i, boost := range havocBoosts {
		names[i] = boost.RowName
	}
	return names
}