package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEDefendScoring represents a PvE defend scoring entry from PvEDefendScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEDefendScoring struct {
	StarterDefendScore int32 `json:"m_starterDefendScore"`
	DeductionTime      int32 `json:"m_deductionTime"`
	ScoreToDeduct      int32 `json:"m_scoreToDeduct"`
	RowName            string `json:"-"` // The row name from the DataTable
}

// pveDefendScoringData holds the loaded PvE defend scoring data
var (
	pveDefendScoring     []PvEDefendScoring
	pveDefendScoringMu   sync.RWMutex
	pveDefendScoringOnce sync.Once
	pveDefendScoringLoaded bool
)

// LoadPvEDefendScoring loads PvE defend scoring data from PvEDefendScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEDefendScoring() error {
	var loadErr error
	pveDefendScoringOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEDefendScoring.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvEDefendScoring.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvEDefendScoring.json: %w", err)
			return
		}

		// Parse rows into PvEDefendScoring structs
		scorings := make([]PvEDefendScoring, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEDefendScoring{
				StarterDefendScore: rowData.GetInt32("m_starterDefendScore"),
				DeductionTime:      rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:      rowData.GetInt32("m_scoreToDeduct"),
				RowName:            rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE defend scorings found in PvEDefendScoring.json")
			return
		}

		pveDefendScoring = scorings
		pveDefendScoringLoaded = true
		log.Printf("Loaded %d PvE defend scorings from PvEDefendScoring.json", len(scorings))
	})

	return loadErr
}

// AllPvEDefendScorings returns all loaded PvE defend scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEDefendScorings() []PvEDefendScoring {
	pveDefendScoringMu.RLock()
	defer pveDefendScoringMu.RUnlock()
	
	if !pveDefendScoringLoaded {
		if err := LoadPvEDefendScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE defend scorings: %v", err)
			return nil
		}
	}

	return pveDefendScoring
}

// PvEDefendScoringByRowName returns the PvE defend scoring with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEDefendScoringByRowName(rowName string) (PvEDefendScoring, bool) {
	pveDefendScoringMu.RLock()
	defer pveDefendScoringMu.RUnlock()
	
	if !pveDefendScoringLoaded {
		if err := LoadPvEDefendScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE defend scorings: %v", err)
			return PvEDefendScoring{}, false
		}
	}

	for _, scoring := range pveDefendScoring {
		if scoring.RowName == rowName {
			return scoring, true
		}
	}
	return PvEDefendScoring{}, false
}

// PvEDefendScoringCount returns the total number of PvE defend scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEDefendScoringCount() int {
	pveDefendScoringMu.RLock()
	defer pveDefendScoringMu.RUnlock()
	
	if !pveDefendScoringLoaded {
		if err := LoadPvEDefendScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE defend scorings: %v", err)
			return 0
		}
	}

	return len(pveDefendScoring)
}