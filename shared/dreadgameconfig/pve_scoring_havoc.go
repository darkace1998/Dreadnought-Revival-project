package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEKillScoringHavoc represents a PvE kill scoring entry for Havoc from PvEKillScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEKillScoringHavoc struct {
	StarterKillScore int32 `json:"m_starterKillScore"`
	DeductionTime    int32 `json:"m_deductionTime"`
	ScoreToDeduct    int32 `json:"m_scoreToDeduct"`
	RowName          string `json:"-"` // The row name from the DataTable
}

// pveKillScoringHavocData holds the loaded PvE kill scoring Havoc data
var (
	pveKillScoringHavoc     []PvEKillScoringHavoc
	pveKillScoringHavocMu   sync.RWMutex
	pveKillScoringHavocOnce sync.Once
	pveKillScoringHavocLoaded bool
)

// LoadPvEKillScoringHavoc loads PvE kill scoring Havoc data from PvEKillScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEKillScoringHavoc() error {
	var loadErr error
	pveKillScoringHavocOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEKillScoring_Havoc.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvEKillScoring_Havoc.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvEKillScoring_Havoc.json: %w", err)
			return
		}

		// Parse rows into PvEKillScoringHavoc structs
		scorings := make([]PvEKillScoringHavoc, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEKillScoringHavoc{
				StarterKillScore: rowData.GetInt32("m_starterKillScore"),
				DeductionTime:    rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:    rowData.GetInt32("m_scoreToDeduct"),
				RowName:          rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE kill scorings Havoc found in PvEKillScoring_Havoc.json")
			return
		}

		pveKillScoringHavoc = scorings
		pveKillScoringHavocLoaded = true
		log.Printf("Loaded %d PvE kill scorings Havoc from PvEKillScoring_Havoc.json", len(scorings))
	})

	return loadErr
}

// AllPvEKillScoringsHavoc returns all loaded PvE kill scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEKillScoringsHavoc() []PvEKillScoringHavoc {
	pveKillScoringHavocMu.RLock()
	defer pveKillScoringHavocMu.RUnlock()
	
	if !pveKillScoringHavocLoaded {
		if err := LoadPvEKillScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE kill scorings Havoc: %v", err)
			return nil
		}
	}

	return pveKillScoringHavoc
}

// PvEKillScoringHavocCount returns the total number of PvE kill scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEKillScoringHavocCount() int {
	pveKillScoringHavocMu.RLock()
	defer pveKillScoringHavocMu.RUnlock()
	
	if !pveKillScoringHavocLoaded {
		if err := LoadPvEKillScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE kill scorings Havoc: %v", err)
			return 0
		}
	}

	return len(pveKillScoringHavoc)
}

// PvEWaveScoringHavoc represents a PvE wave scoring entry for Havoc from PvEWaveScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEWaveScoringHavoc struct {
	StarterWaveScore int32 `json:"m_starterWaveScore"`
	DeductionTime    int32 `json:"m_deductionTime"`
	ScoreToDeduct    int32 `json:"m_scoreToDeduct"`
	RowName          string `json:"-"` // The row name from the DataTable
}

// pveWaveScoringHavocData holds the loaded PvE wave scoring Havoc data
var (
	pveWaveScoringHavoc     []PvEWaveScoringHavoc
	pveWaveScoringHavocMu   sync.RWMutex
	pveWaveScoringHavocOnce sync.Once
	pveWaveScoringHavocLoaded bool
)

// LoadPvEWaveScoringHavoc loads PvE wave scoring Havoc data from PvEWaveScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEWaveScoringHavoc() error {
	var loadErr error
	pveWaveScoringHavocOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEWaveScoring_Havoc.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read PvEWaveScoring_Havoc.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse PvEWaveScoring_Havoc.json: %w", err)
			return
		}

		// Parse rows into PvEWaveScoringHavoc structs
		scorings := make([]PvEWaveScoringHavoc, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			scoring := PvEWaveScoringHavoc{
				StarterWaveScore: rowData.GetInt32("m_starterWaveScore"),
				DeductionTime:    rowData.GetInt32("m_deductionTime"),
				ScoreToDeduct:    rowData.GetInt32("m_scoreToDeduct"),
				RowName:          rowName,
			}
			scorings = append(scorings, scoring)
		}

		if len(scorings) == 0 {
			loadErr = fmt.Errorf("no PvE wave scorings Havoc found in PvEWaveScoring_Havoc.json")
			return
		}

		pveWaveScoringHavoc = scorings
		pveWaveScoringHavocLoaded = true
		log.Printf("Loaded %d PvE wave scorings Havoc from PvEWaveScoring_Havoc.json", len(scorings))
	})

	return loadErr
}

// AllPvEWaveScoringsHavoc returns all loaded PvE wave scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEWaveScoringsHavoc() []PvEWaveScoringHavoc {
	pveWaveScoringHavocMu.RLock()
	defer pveWaveScoringHavocMu.RUnlock()
	
	if !pveWaveScoringHavocLoaded {
		if err := LoadPvEWaveScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE wave scorings Havoc: %v", err)
			return nil
		}
	}

	return pveWaveScoringHavoc
}

// PvEWaveScoringHavocCount returns the total number of PvE wave scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEWaveScoringHavocCount() int {
	pveWaveScoringHavocMu.RLock()
	defer pveWaveScoringHavocMu.RUnlock()
	
	if !pveWaveScoringHavocLoaded {
		if err := LoadPvEWaveScoringHavoc(); err != nil {
			log.Printf("Warning: Failed to load PvE wave scorings Havoc: %v", err)
			return 0
		}
	}

	return len(pveWaveScoringHavoc)
}