package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// HavocBossWave represents a Havoc boss wave from DN_HavocBossWaves_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocBossWave struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	IconPath    string `json:"m_iconPath"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// havocBossWavesData holds the loaded Havoc boss wave data
var (
	havocBossWaves     []HavocBossWave
	havocBossWavesMu   sync.RWMutex
	havocBossWavesOnce sync.Once
	havocBossWavesLoadErr error
	havocBossWavesLoaded bool
)

// LoadHavocBossWaves loads Havoc boss wave data from DN_HavocBossWaves_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocBossWaves() error {
	havocBossWavesOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "Havoc", "DN_HavocBossWaves_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			havocBossWavesLoadErr = fmt.Errorf("failed to read DN_HavocBossWaves_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			havocBossWavesLoadErr = fmt.Errorf("failed to parse DN_HavocBossWaves_DT.json: %w", err)
			return
		}

		// Parse rows into HavocBossWave structs
		waves := make([]HavocBossWave, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			wave := HavocBossWave{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				IconPath:    rowData.GetString("m_iconPath"),
				RowName:     rowName,
			}
			waves = append(waves, wave)
		}

		if len(waves) == 0 {
			havocBossWavesLoadErr = fmt.Errorf("no Havoc boss waves found in DN_HavocBossWaves_DT.json")
			return
		}

		havocBossWaves = waves
		havocBossWavesLoaded = true
		log.Printf("Loaded %d Havoc boss waves from DN_HavocBossWaves_DT.json", len(waves))
	})

	return havocBossWavesLoadErr
}

// AllHavocBossWaves returns all loaded Havoc boss waves
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocBossWaves() []HavocBossWave {
	havocBossWavesMu.RLock()
	defer havocBossWavesMu.RUnlock()
	
	if !havocBossWavesLoaded {
		if err := LoadHavocBossWaves(); err != nil {
			log.Printf("Warning: Failed to load Havoc boss waves: %v", err)
			return nil
		}
	}

	return havocBossWaves
}

// HavocBossWaveByRowName returns the Havoc boss wave with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBossWaveByRowName(rowName string) (HavocBossWave, bool) {
	havocBossWavesMu.RLock()
	defer havocBossWavesMu.RUnlock()
	
	if !havocBossWavesLoaded {
		if err := LoadHavocBossWaves(); err != nil {
			log.Printf("Warning: Failed to load Havoc boss waves: %v", err)
			return HavocBossWave{}, false
		}
	}

	for _, wave := range havocBossWaves {
		if wave.RowName == rowName {
			return wave, true
		}
	}
	return HavocBossWave{}, false
}

// HavocBossWaveCount returns the total number of Havoc boss waves
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBossWaveCount() int {
	havocBossWavesMu.RLock()
	defer havocBossWavesMu.RUnlock()
	
	if !havocBossWavesLoaded {
		if err := LoadHavocBossWaves(); err != nil {
			log.Printf("Warning: Failed to load Havoc boss waves: %v", err)
			return 0
		}
	}

	return len(havocBossWaves)
}

// HavocBossWaveRowNames returns all row names of Havoc boss waves
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocBossWaveRowNames() []string {
	havocBossWavesMu.RLock()
	defer havocBossWavesMu.RUnlock()
	
	if !havocBossWavesLoaded {
		if err := LoadHavocBossWaves(); err != nil {
			log.Printf("Warning: Failed to load Havoc boss waves: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocBossWaves))
	for i, wave := range havocBossWaves {
		names[i] = wave.RowName
	}
	return names
}