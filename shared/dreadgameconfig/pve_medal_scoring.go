package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEMedalScoring represents a PvE medal scoring entry from PvEMedalScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEMedalScoring struct {
	MedalScore int32 `json:"m_medalScore"`
	RowName    string `json:"-"` // The row name from the DataTable
}

// pveMedalScoringData holds the loaded PvE medal scoring data
var (
	pveMedalScoring     []PvEMedalScoring
	pveMedalScoringMu   sync.RWMutex
	pveMedalScoringOnce sync.Once
	pveMedalScoringLoadErr error
	pveMedalScoringLoaded bool
)

// LoadPvEMedalScoring loads PvE medal scoring data from PvEMedalScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEMedalScoring() error {
	pveMedalScoringOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEMedalScoring.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			pveMedalScoringLoadErr = fmt.Errorf("failed to read PvEMedalScoring.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			pveMedalScoringLoadErr = fmt.Errorf("failed to parse PvEMedalScoring.json: %w", err)
			return
		}

		// Parse rows into PvEMedalScoring structs
		scorings := make([]PvEMedalScoring, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEMedalScoring{
				MedalScore: rowData.GetInt32("m_medalScore"),
				RowName:    rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			pveMedalScoringLoadErr = fmt.Errorf("no PvE medal scorings found in PvEMedalScoring.json")
			return
		}

		pveMedalScoring = scorings
		pveMedalScoringLoaded = true
		log.Printf("Loaded %d PvE medal scorings from PvEMedalScoring.json", len(scorings))
	})

	return pveMedalScoringLoadErr
}

// AllPvEMedalScorings returns all loaded PvE medal scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEMedalScorings() []PvEMedalScoring {
	pveMedalScoringMu.RLock()
	defer pveMedalScoringMu.RUnlock()
	
	if !pveMedalScoringLoaded {
		if err := LoadPvEMedalScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE medal scorings: %v", err)
			return nil
		}
	}

	return pveMedalScoring
}

// PvEMedalScoringByRowName returns the PvE medal scoring with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEMedalScoringByRowName(rowName string) (PvEMedalScoring, bool) {
	pveMedalScoringMu.RLock()
	defer pveMedalScoringMu.RUnlock()
	
	if !pveMedalScoringLoaded {
		if err := LoadPvEMedalScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE medal scorings: %v", err)
			return PvEMedalScoring{}, false
		}
	}

	for _, scoring := range pveMedalScoring {
		if scoring.RowName == rowName {
			return scoring, true
		}
	}
	return PvEMedalScoring{}, false
}

// PvEMedalScoringCount returns the total number of PvE medal scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEMedalScoringCount() int {
	pveMedalScoringMu.RLock()
	defer pveMedalScoringMu.RUnlock()
	
	if !pveMedalScoringLoaded {
		if err := LoadPvEMedalScoring(); err != nil {
			log.Printf("Warning: Failed to load PvE medal scorings: %v", err)
			return 0
		}
	}

	return len(pveMedalScoring)
}