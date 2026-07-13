package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEKillScoring represents a PvE kill scoring entry from PvEKillScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEKillScoring struct {
	StarterKillScore int32 `json:"m_starterKillScore"`
	DeductionTime    int32 `json:"m_deductionTime"`
	ScoreToDeduct    int32 `json:"m_scoreToDeduct"`
	RowName          string `json:"-"` // The row name from the DataTable
}

// pveKillScoringData holds the loaded PvE kill scoring data
var (
	pveKillScoring     []PvEKillScoring
	pveKillScoringMu   sync.RWMutex
	pveKillScoringOnce sync.Once
	pveKillScoringLoaded bool
)

// LoadPvEKillScoring loads PvE kill scoring data from PvEKillScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEKillScoring() error {
	var loadErr error
	pveKillScoringOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEKillScoring.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvEKillScoring.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvEKillScoring.json: %w", err)
			return
		}

		// Parse rows into PvEKillScoring structs
		scorings := make([]PvEKillScoring, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEKillScoring{
				StarterKillScore: rowData.GetInt32("m_starterKillScore"),
				DeductionTime:    rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:    rowData.GetInt32("m_scoreToDeduct"),
				RowName:          rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE kill scorings found in PvEKillScoring.json")
			return
		}

		pveKillScoring = scorings
		pveKillScoringLoaded = true
		log.Printf("Loaded %d PvE kill scorings from PvEKillScoring.json", len(scorings))
	})

	return loadErr
}

// AllPvEKillScorings returns all loaded PvE kill scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEKillScorings() []PvEKillScoring {
	pveKillScoringMu.RLock()
	defer pveKillScoringMu.RUnlock()
	
	if !pveKillScoringLoaded {
		if err := LoadPvEKillScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE kill scorings: %v", err)
			return nil
		}
	}

	return pveKillScoring
}

// PvEKillScoringByRowName returns the PvE kill scoring with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEKillScoringByRowName(rowName string) (PvEKillScoring, bool) {
	pveKillScoringMu.RLock()
	defer pveKillScoringMu.RUnlock()
	
	if !pveKillScoringLoaded {
		if err := LoadPvEKillScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE kill scorings: %v", err)
			return PvEKillScoring{}, false
		}
	}

	for _, scoring := range pveKillScoring {
		if scoring.RowName == rowName {
			return scoring, true
		}
	}
	return PvEKillScoring{}, false
}

// PvEKillScoringCount returns the total number of PvE kill scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEKillScoringCount() int {
	pveKillScoringMu.RLock()
	defer pveKillScoringMu.RUnlock()
	
	if !pveKillScoringLoaded {
		if err := LoadPvEKillScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE kill scorings: %v", err)
			return 0
		}
	}

	return len(pveKillScoring)
}