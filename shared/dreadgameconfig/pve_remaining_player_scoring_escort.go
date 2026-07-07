package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvERemainingPlayerScoringEscort represents a PvE remaining player scoring entry for Escort from PvERemainingPlayerScoring_Escort.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvERemainingPlayerScoringEscort struct {
	StarterRemainingPlayerScore int32 `json:"m_starterRemainingPlayerScore"`
	DeductionTime               int32 `json:"m_deductionTime"`
	ScoreToDeduct               int32 `json:"m_scoreToDeduct"`
	RowName                     string `json:"-"` // The row name from the DataTable
}

// pveRemainingPlayerScoringEscortData holds the loaded PvE remaining player scoring Escort data
var (
	pveRemainingPlayerScoringEscort     []PvERemainingPlayerScoringEscort
	pveRemainingPlayerScoringEscortMu   sync.RWMutex
	pveRemainingPlayerScoringEscortOnce sync.Once
	pveRemainingPlayerScoringEscortLoaded bool
)

// LoadPvERemainingPlayerScoringEscort loads PvE remaining player scoring Escort data from PvERemainingPlayerScoring_Escort.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvERemainingPlayerScoringEscort() error {
	var loadErr error
	pveRemainingPlayerScoringEscortOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "PVE", "PvERemainingPlayerScoring_Escort.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvERemainingPlayerScoring_Escort.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvERemainingPlayerScoring_Escort.json: %w", err)
			return
		}

		// Parse rows into PvERemainingPlayerScoringEscort structs
		scorings := make([]PvERemainingPlayerScoringEscort, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvERemainingPlayerScoringEscort{
				StarterRemainingPlayerScore: rowData.GetInt32("m_starterRemainingPlayerScore"),
				DeductionTime:               rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:               rowData.GetInt32("m_scoreToDeduct"),
				RowName:                     rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE remaining player scorings Escort found in PvERemainingPlayerScoring_Escort.json")
			return
		}

		pveRemainingPlayerScoringEscort = scorings
		pveRemainingPlayerScoringEscortLoaded = true
		log.Printf("Loaded %d PvE remaining player scorings Escort from PvERemainingPlayerScoring_Escort.json", len(scorings))
	})

	return loadErr
}

// AllPvERemainingPlayerScoringsEscort returns all loaded PvE remaining player scorings Escort
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvERemainingPlayerScoringsEscort() []PvERemainingPlayerScoringEscort {
	pveRemainingPlayerScoringEscortMu.RLock()
	defer pveRemainingPlayerScoringEscortMu.RUnlock()
	
	if !pveRemainingPlayerScoringEscortLoaded {
		if err := LoadPvERemainingPlayerScoringEscort(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings Escort: %v", err)
			return nil
		}
	}

	return pveRemainingPlayerScoringEscort
}

// PvERemainingPlayerScoringEscortCount returns the total number of PvE remaining player scorings Escort
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvERemainingPlayerScoringEscortCount() int {
	pveRemainingPlayerScoringEscortMu.RLock()
	defer pveRemainingPlayerScoringEscortMu.RUnlock()
	
	if !pveRemainingPlayerScoringEscortLoaded {
		if err := LoadPvERemainingPlayerScoringEscort(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings Escort: %v", err)
			return 0
		}
	}

	return len(pveRemainingPlayerScoringEscort)
}