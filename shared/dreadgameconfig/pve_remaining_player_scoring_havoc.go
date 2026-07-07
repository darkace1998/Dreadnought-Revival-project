package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvERemainingPlayerScoringHavoc represents a PvE remaining player scoring entry for Havoc from vERemainingPlayerScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvERemainingPlayerScoringHavoc struct {
	StarterRemainingPlayerScore int32 `json:"m_starterRemainingPlayerScore"`
	DeductionTime               int32 `json:"m_deductionTime"`
	ScoreToDeduct               int32 `json:"m_scoreToDeduct"`
	RowName                     string `json:"-"` // The row name from the DataTable
}

// pveRemainingPlayerScoringHavocData holds the loaded PvE remaining player scoring Havoc data
var (
	pveRemainingPlayerScoringHavoc     []PvERemainingPlayerScoringHavoc
	pveRemainingPlayerScoringHavocMu   sync.RWMutex
	pveRemainingPlayerScoringHavocOnce sync.Once
	pveRemainingPlayerScoringHavocLoaded bool
)

// LoadPvERemainingPlayerScoringHavoc loads PvE remaining player scoring Havoc data from vERemainingPlayerScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvERemainingPlayerScoringHavoc() error {
	var loadErr error
	pveRemainingPlayerScoringHavocOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "PVE", "vERemainingPlayerScoring_Havoc.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read vERemainingPlayerScoring_Havoc.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse vERemainingPlayerScoring_Havoc.json: %w", err)
			return
		}

		// Parse rows into PvERemainingPlayerScoringHavoc structs
		scorings := make([]PvERemainingPlayerScoringHavoc, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvERemainingPlayerScoringHavoc{
				StarterRemainingPlayerScore: rowData.GetInt32("m_starterRemainingPlayerScore"),
				DeductionTime:               rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:               rowData.GetInt32("m_scoreToDeduct"),
				RowName:                     rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE remaining player scorings Havoc found in vERemainingPlayerScoring_Havoc.json")
			return
		}

		pveRemainingPlayerScoringHavoc = scorings
		pveRemainingPlayerScoringHavocLoaded = true
		log.Printf("Loaded %d PvE remaining player scorings Havoc from vERemainingPlayerScoring_Havoc.json", len(scorings))
	})

	return loadErr
}

// AllPvERemainingPlayerScoringsHavoc returns all loaded PvE remaining player scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvERemainingPlayerScoringsHavoc() []PvERemainingPlayerScoringHavoc {
	pveRemainingPlayerScoringHavocMu.RLock()
	defer pveRemainingPlayerScoringHavocMu.RUnlock()
	
	if !pveRemainingPlayerScoringHavocLoaded {
		if err := LoadPvERemainingPlayerScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings Havoc: %v", err)
			return nil
		}
	}

	return pveRemainingPlayerScoringHavoc
}

// PvERemainingPlayerScoringHavocCount returns the total number of PvE remaining player scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvERemainingPlayerScoringHavocCount() int {
	pveRemainingPlayerScoringHavocMu.RLock()
	defer pveRemainingPlayerScoringHavocMu.RUnlock()
	
	if !pveRemainingPlayerScoringHavocLoaded {
		if err := LoadPvERemainingPlayerScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE remaining player scorings Havoc: %v", err)
			return 0
		}
	}

	return len(pveRemainingPlayerScoringHavoc)
}