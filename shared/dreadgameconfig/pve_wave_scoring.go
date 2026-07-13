package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEWaveScoring represents a PvE wave scoring entry from PvEWaveScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEWaveScoring struct {
	StarterWaveScore int32 `json:"m_starterWaveScore"`
	DeductionTime    int32 `json:"m_deductionTime"`
	ScoreToDeduct    int32 `json:"m_scoreToDeduct"`
	RowName          string `json:"-"` // The row name from the DataTable
}

// pveWaveScoringData holds the loaded PvE wave scoring data
var (
	pveWaveScoring     []PvEWaveScoring
	pveWaveScoringMu   sync.RWMutex
	pveWaveScoringOnce sync.Once
	pveWaveScoringLoaded bool
)

// LoadPvEWaveScoring loads PvE wave scoring data from PvEWaveScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEWaveScoring() error {
	var loadErr error
	pveWaveScoringOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEWaveScoring.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvEWaveScoring.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvEWaveScoring.json: %w", err)
			return
		}

		// Parse rows into PvEWaveScoring structs
		scorings := make([]PvEWaveScoring, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEWaveScoring{
				StarterWaveScore: rowData.GetInt32("m_starterWaveScore"),
				DeductionTime:    rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:    rowData.GetInt32("m_scoreToDeduct"),
				RowName:          rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE wave scorings found in PvEWaveScoring.json")
			return
		}

		pveWaveScoring = scorings
		pveWaveScoringLoaded = true
		log.Printf("Loaded %d PvE wave scorings from PvEWaveScoring.json", len(scorings))
	})

	return loadErr
}

// AllPvEWaveScorings returns all loaded PvE wave scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEWaveScorings() []PvEWaveScoring {
	pveWaveScoringMu.RLock()
	defer pveWaveScoringMu.RUnlock()
	
	if !pveWaveScoringLoaded {
		if err := LoadPvEWaveScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE wave scorings: %v", err)
			return nil
		}
	}

	return pveWaveScoring
}

// PvEWaveScoringByRowName returns the PvE wave scoring with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEWaveScoringByRowName(rowName string) (PvEWaveScoring, bool) {
	pveWaveScoringMu.RLock()
	defer pveWaveScoringMu.RUnlock()
	
	if !pveWaveScoringLoaded {
		if err := LoadPvEWaveScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE wave scorings: %v", err)
			return PvEWaveScoring{}, false
		}
	}

	for _, scoring := range pveWaveScoring {
		if scoring.RowName == rowName {
			return scoring, true
		}
	}
	return PvEWaveScoring{}, false
}

// PvEWaveScoringCount returns the total number of PvE wave scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEWaveScoringCount() int {
	pveWaveScoringMu.RLock()
	defer pveWaveScoringMu.RUnlock()
	
	if !pveWaveScoringLoaded {
		if err := LoadPvEWaveScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE wave scorings: %v", err)
			return 0
		}
	}

	return len(pveWaveScoring)
}