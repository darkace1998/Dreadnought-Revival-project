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

// HavocEnemyModifier represents a Havoc enemy modifier from DN_HavocPermanentEnemyModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocEnemyModifier struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	IconPath    string `json:"m_iconPath"`
	Feats       string `json:"m_feats"`
	RowName     string `json:"-"` // The row name from the DataTable
	
	// Parsed fields for easier access
	FeatList       []string `json:"-"` // Parsed from m_feats (split by semicolon)
}

// havocEnemyModifiersData holds the loaded Havoc enemy modifier data
var (
	havocEnemyModifiers     []HavocEnemyModifier
	havocEnemyModifiersMu   sync.RWMutex
	havocEnemyModifiersOnce sync.Once
	havocEnemyModifiersLoaded bool
)

// LoadHavocEnemyModifiers loads Havoc enemy modifier data from DN_HavocPermanentEnemyModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocEnemyModifiers() error {
	var loadErr error
	havocEnemyModifiersOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "Havoc", "DN_HavocPermanentEnemyModifiers_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocPermanentEnemyModifiers_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocPermanentEnemyModifiers_DT.json: %w", err)
			return
		}

		// Parse rows into HavocEnemyModifier structs
		modifiers := make([]HavocEnemyModifier, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			modifier := HavocEnemyModifier{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				IconPath:    rowData.GetString("m_iconPath"),
				Feats:       rowData.GetString("m_feats"),
				RowName:     rowName,
			}

			// Parse feats
			if modifier.Feats != "" {
				modifier.FeatList = strings.Split(modifier.Feats, ";")
			}

			modifiers = append(modifiers, modifier)
		}

		if len(modifiers) == 0 {
			loadErr = fmt.Errorf("no Havoc enemy modifiers found in DN_HavocPermanentEnemyModifiers_DT.json")
			return
		}

		havocEnemyModifiers = modifiers
		havocEnemyModifiersLoaded = true
		log.Printf("Loaded %d Havoc enemy modifiers from DN_HavocPermanentEnemyModifiers_DT.json", len(modifiers))
	})

	return loadErr
}

// AllHavocEnemyModifiers returns all loaded Havoc enemy modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocEnemyModifiers() []HavocEnemyModifier {
	havocEnemyModifiersMu.RLock()
	defer havocEnemyModifiersMu.RUnlock()
	
	if !havocEnemyModifiersLoaded {
		if err := LoadHavocEnemyModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc enemy modifiers: %v", err)
			return nil
		}
	}

	return havocEnemyModifiers
}

// HavocEnemyModifierByRowName returns the Havoc enemy modifier with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocEnemyModifierByRowName(rowName string) (HavocEnemyModifier, bool) {
	havocEnemyModifiersMu.RLock()
	defer havocEnemyModifiersMu.RUnlock()
	
	if !havocEnemyModifiersLoaded {
		if err := LoadHavocEnemyModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc enemy modifiers: %v", err)
			return HavocEnemyModifier{}, false
		}
	}

	for _, modifier := range havocEnemyModifiers {
		if modifier.RowName == rowName {
			return modifier, true
		}
	}
	return HavocEnemyModifier{}, false
}

// HavocEnemyModifierCount returns the total number of Havoc enemy modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocEnemyModifierCount() int {
	havocEnemyModifiersMu.RLock()
	defer havocEnemyModifiersMu.RUnlock()
	
	if !havocEnemyModifiersLoaded {
		if err := LoadHavocEnemyModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc enemy modifiers: %v", err)
			return 0
		}
	}

	return len(havocEnemyModifiers)
}

// HavocEnemyModifierRowNames returns all row names of Havoc enemy modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocEnemyModifierRowNames() []string {
	havocEnemyModifiersMu.RLock()
	defer havocEnemyModifiersMu.RUnlock()
	
	if !havocEnemyModifiersLoaded {
		if err := LoadHavocEnemyModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc enemy modifiers: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocEnemyModifiers))
	for i, modifier := range havocEnemyModifiers {
		names[i] = modifier.RowName
	}
	return names
}