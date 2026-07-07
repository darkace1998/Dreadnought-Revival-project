package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvERemainingPlayerScoring represents a PvE remaining player scoring entry from PvERemainingPlayerScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvERemainingPlayerScoring struct {
	StarterRemainingPlayerScore int32 `json:"m_starterRemainingPlayerScore"`
	DeductionTime               int32 `json:"m_deductionTime"`
	ScoreToDeduct               int32 `json:"m_scoreToDeduct"`
	RowName                     string `json:"-"` // The row name from the DataTable
}

// pveRemainingPlayerScoringData holds the loaded PvE remaining player scoring data
var (
	pveRemainingPlayerScoring     []PvERemainingPlayerScoring
	pveRemainingPlayerScoringMu   sync.RWMutex
	pveRemainingPlayerScoringOnce sync.Once
	pveRemainingPlayerScoringLoaded bool
)

// LoadPvERemainingPlayerScoring loads PvE remaining player scoring data from PvERemainingPlayerScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvERemainingPlayerScoring() error {
	var loadErr error
	pveRemainingPlayerScoringOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "PVE", "PvERemainingPlayerScoring.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvERemainingPlayerScoring.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvERemainingPlayerScoring.json: %w", err)
			return
		}

		// Parse rows into PvERemainingPlayerScoring structs
		scorings := make([]PvERemainingPlayerScoring, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvERemainingPlayerScoring{
				StarterRemainingPlayerScore: rowData.GetInt32("m_starterRemainingPlayerScore"),
				DeductionTime:               rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:               rowData.GetInt32("m_scoreToDeduct"),
				RowName:                     rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE remaining player scorings found in PvERemainingPlayerScoring.json")
			return
		}

		pveRemainingPlayerScoring = scorings
		pveRemainingPlayerScoringLoaded = true
		log.Printf("Loaded %d PvE remaining player scorings from PvERemainingPlayerScoring.json", len(scorings))
	})

	return loadErr
}

// AllPvERemainingPlayerScorings returns all loaded PvE remaining player scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvERemainingPlayerScorings() []PvERemainingPlayerScoring {
	pveRemainingPlayerScoringMu.RLock()
	defer pveRemainingPlayerScoringMu.RUnlock()
	
	if !pveRemainingPlayerScoringLoaded {
		if err := LoadPvERemainingPlayerScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings: %v", err)
			return nil
		}
	}

	return pveRemainingPlayerScoring
}

// PvERemainingPlayerScoringByRowName returns the PvE remaining player scoring with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvERemainingPlayerScoringByRowName(rowName string) (PvERemainingPlayerScoring, bool) {
	pveRemainingPlayerScoringMu.RLock()
	defer pveRemainingPlayerScoringMu.RUnlock()
	
	if !pveRemainingPlayerScoringLoaded {
		if err := LoadPvERemainingPlayerScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings: %v", err)
			return PvERemainingPlayerScoring{}, false
		}
	}

	for _, scoring := range pveRemainingPlayerScoring {
		if scoring.RowName == rowName {
			return scoring, true
		}
	}
	return PvERemainingPlayerScoring{}, false
}

// PvERemainingPlayerScoringCount returns the total number of PvE remaining player scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvERemainingPlayerScoringCount() int {
	pveRemainingPlayerScoringMu.RLock()
	defer pveRemainingPlayerScoringMu.RUnlock()
	
	if !pveRemainingPlayerScoringLoaded {
		if err := LoadPvERemainingPlayerScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings: %v", err)
			return 0
		}
	}

	return len(pveRemainingPlayerScoring)
}