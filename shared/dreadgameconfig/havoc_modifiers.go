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

// HavocModifier represents a Havoc modifier from DN_HavocModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocModifier struct {
	Title           string `json:"m_title"`
	Subtitle        string `json:"m_subtitle"`
	Description     string `json:"m_description"`
	IconPath        string `json:"m_iconPath"`
	Excludes        string `json:"m_excludes"`
	Feats           string `json:"m_feats"`
	MinWave         int32  `json:"m_minWave"`
	MaxWave         int32  `json:"m_maxWave"`
	Weight          int32  `json:"m_weight"`
	Impact          int32  `json:"m_impact"`
	IsAlwaysLoaded  bool   `json:"m_isAlwaysLoadedForUI"`
	AffectedTeam    string `json:"m_affectedTeam"`
	RowName         string `json:"-"` // The row name from the DataTable
	
	// Parsed fields for easier access
	FeatList       []string `json:"-"` // Parsed from m_feats (split by semicolon)
	ExcludeList    []string `json:"-"` // Parsed from m_excludes (split by semicolon)
}

// havocModifiersData holds the loaded Havoc modifier data
var (
	havocModifiers     []HavocModifier
	havocModifiersMu   sync.RWMutex
	havocModifiersOnce sync.Once
	havocModifiersLoaded bool
)

// LoadHavocModifiers loads Havoc modifier data from DN_HavocModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocModifiers() error {
	var loadErr error
	havocModifiersOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "Havoc", "DN_HavocModifiers_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocModifiers_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocModifiers_DT.json: %w", err)
			return
		}

		// Parse rows into HavocModifier structs
		modifiers := make([]HavocModifier, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			modifier := HavocModifier{
				Title:        rowData.GetString("m_title"),
				Subtitle:     rowData.GetString("m_subtitle"),
				Description:  rowData.GetString("m_description"),
				IconPath:     rowData.GetString("m_iconPath"),
				Excludes:     rowData.GetString("m_excludes"),
				Feats:        rowData.GetString("m_feats"),
				MinWave:      rowData.GetInt32("m_minWave"),
				MaxWave:      rowData.GetInt32("m_maxWave"),
				Weight:       rowData.GetInt32("m_weight"),
				Impact:       rowData.GetInt32("m_impact"),
				IsAlwaysLoaded: rowData.GetBool("m_isAlwaysLoadedForUI"),
				AffectedTeam: rowData.GetString("m_affectedTeam"),
				RowName:      rowName,
			}

			// Parse feats and excludes
			if modifier.Feats != "" {
				modifier.FeatList = strings.Split(modifier.Feats, ";")
			}
			if modifier.Excludes != "" {
				modifier.ExcludeList = strings.Split(modifier.Excludes, ";")
			}

			modifiers = append(modifiers, modifier)
		}

		if len(modifiers) == 0 {
			loadErr = fmt.Errorf("no Havoc modifiers found in DN_HavocModifiers_DT.json")
			return
		}

		havocModifiers = modifiers
		havocModifiersLoaded = true
		log.Printf("Loaded %d Havoc modifiers from DN_HavocModifiers_DT.json", len(modifiers))
	})

	return loadErr
}

// AllHavocModifiers returns all loaded Havoc modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocModifiers() []HavocModifier {
	havocModifiersMu.RLock()
	defer havocModifiersMu.RUnlock()
	
	if !havocModifiersLoaded {
		if err := LoadHavocModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc modifiers: %v", err)
			return nil
		}
	}

	return havocModifiers
}

// HavocModifierByRowName returns the Havoc modifier with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocModifierByRowName(rowName string) (HavocModifier, bool) {
	havocModifiersMu.RLock()
	defer havocModifiersMu.RUnlock()
	
	if !havocModifiersLoaded {
		if err := LoadHavocModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc modifiers: %v", err)
			return HavocModifier{}, false
		}
	}

	for _, modifier := range havocModifiers {
		if modifier.RowName == rowName {
			return modifier, true
		}
	}
	return HavocModifier{}, false
}

// HavocModifierCount returns the total number of Havoc modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocModifierCount() int {
	havocModifiersMu.RLock()
	defer havocModifiersMu.RUnlock()
	
	if !havocModifiersLoaded {
		if err := LoadHavocModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc modifiers: %v", err)
			return 0
		}
	}

	return len(havocModifiers)
}

// HavocModifierRowNames returns all row names of Havoc modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocModifierRowNames() []string {
	havocModifiersMu.RLock()
	defer havocModifiersMu.RUnlock()
	
	if !havocModifiersLoaded {
		if err := LoadHavocModifiers(); err != nil {
			log.Printf("Warning: Failed to load Havoc modifiers: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocModifiers))
	for i, modifier := range havocModifiers {
		names[i] = modifier.RowName
	}
	return names
}