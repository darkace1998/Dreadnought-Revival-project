package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvETutorialObjective represents a tutorial objective from TutorialObjectives_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvETutorialObjective struct {
	ID              string `json:"m_id"`
	Type            string `json:"m_type"`
	State           string `json:"m_state"`
	MainObjective   bool   `json:"m_mainObjective"`
	Message         string `json:"m_message"`
	MarkerText      string `json:"m_markerText"`
	AmountToComplete int32  `json:"m_amountToComplete"`
	CurrentAmount   int32  `json:"m_currentAmount"`
	RowName         string `json:"-"` // The row name from the DataTable
}

// pveTutorialObjectivesData holds the loaded PvE tutorial objectives data
var (
	pveTutorialObjectives     []PvETutorialObjective
	pveTutorialObjectivesMu   sync.RWMutex
	pveTutorialObjectivesOnce sync.Once
	pveTutorialObjectivesLoadErr error
	pveTutorialObjectivesLoaded bool
)

// LoadPvETutorialObjectives loads PvE tutorial objectives data from TutorialObjectives_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvETutorialObjectives() error {
	pveTutorialObjectivesOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "TutorialObjectives_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			pveTutorialObjectivesLoadErr = fmt.Errorf("failed to read TutorialObjectives_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			pveTutorialObjectivesLoadErr = fmt.Errorf("failed to parse TutorialObjectives_DT.json: %w", err)
			return
		}

		// Parse rows into PvETutorialObjective structs
		objectives := make([]PvETutorialObjective, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			objective := PvETutorialObjective{
				ID:            rowData.GetString("m_id"),
				Type:          rowData.GetString("m_type"),
				State:         rowData.GetString("m_state"),
				MainObjective: rowData.GetBool("m_mainObjective"),
				Message:       rowData.GetString("m_message"),
				MarkerText:    rowData.GetString("m_markerText"),
				AmountToComplete: rowData.GetInt32("m_amountToComplete"),
				CurrentAmount:   rowData.GetInt32("m_currentAmount"),
				RowName:         rowName,
			}
			objectives = append(objectives, objective)
		}

		if len(objectives) == 0 {
			pveTutorialObjectivesLoadErr = fmt.Errorf("no PvE tutorial objectives found in TutorialObjectives_DT.json")
			return
		}

		pveTutorialObjectives = objectives
		pveTutorialObjectivesLoaded = true
		log.Printf("Loaded %d PvE tutorial objectives from TutorialObjectives_DT.json", len(objectives))
	})

	return pveTutorialObjectivesLoadErr
}

// AllPvETutorialObjectives returns all loaded PvE tutorial objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvETutorialObjectives() []PvETutorialObjective {
	pveTutorialObjectivesMu.RLock()
	defer pveTutorialObjectivesMu.RUnlock()
	
	if !pveTutorialObjectivesLoaded {
		if err := LoadPvETutorialObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE tutorial objectives: %v", err)
			return nil
		}
	}

	return pveTutorialObjectives
}

// PvETutorialObjectiveByRowName returns the PvE tutorial objective with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvETutorialObjectiveByRowName(rowName string) (PvETutorialObjective, bool) {
	pveTutorialObjectivesMu.RLock()
	defer pveTutorialObjectivesMu.RUnlock()
	
	if !pveTutorialObjectivesLoaded {
		if err := LoadPvETutorialObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE tutorial objectives: %v", err)
			return PvETutorialObjective{}, false
		}
	}

	for _, objective := range pveTutorialObjectives {
		if objective.RowName == rowName {
			return objective, true
		}
	}
	return PvETutorialObjective{}, false
}

// PvETutorialObjectiveCount returns the total number of PvE tutorial objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvETutorialObjectiveCount() int {
	pveTutorialObjectivesMu.RLock()
	defer pveTutorialObjectivesMu.RUnlock()
	
	if !pveTutorialObjectivesLoaded {
		if err := LoadPvETutorialObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE tutorial objectives: %v", err)
			return 0
		}
	}

	return len(pveTutorialObjectives)
}