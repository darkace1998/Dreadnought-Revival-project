package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEObjective represents a PvE objective from PvEObjectives.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEObjective struct {
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

// pveObjectivesData holds the loaded PvE objectives data
var (
	pveObjectives     []PvEObjective
	pveObjectivesMu   sync.RWMutex
	pveObjectivesOnce sync.Once
	pveObjectivesLoadErr error
	pveObjectivesLoaded bool
)

// LoadPvEObjectives loads PvE objectives data from PvEObjectives.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEObjectives() error {
	pveObjectivesOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "PvEObjectives.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			pveObjectivesLoadErr = fmt.Errorf("failed to read PvEObjectives.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			pveObjectivesLoadErr = fmt.Errorf("failed to parse PvEObjectives.json: %w", err)
			return
		}

		// Parse rows into PvEObjective structs
		objectives := make([]PvEObjective, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			objective := PvEObjective{
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
			pveObjectivesLoadErr = fmt.Errorf("no PvE objectives found in PvEObjectives.json")
			return
		}

		// Was previously an unsynchronized write racing against readers'
		// pveObjectivesMu.RLock() below — a genuine data race under
		// concurrent load+read (go test -race would flag it), which could
		// hand a reader a torn/partial slice header.
		pveObjectivesMu.Lock()
		pveObjectives = objectives
		pveObjectivesLoaded = true
		pveObjectivesMu.Unlock()
		log.Printf("Loaded %d PvE objectives from PvEObjectives.json", len(objectives))
	})

	return pveObjectivesLoadErr
}

// AllPvEObjectives returns all loaded PvE objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEObjectives() []PvEObjective {
	pveObjectivesMu.RLock()
	loaded := pveObjectivesLoaded
	pveObjectivesMu.RUnlock()
	if !loaded {
		if err := LoadPvEObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE objectives: %v", err)
			return nil
		}
	}
	pveObjectivesMu.RLock()
	defer pveObjectivesMu.RUnlock()

	return pveObjectives
}

// PvEObjectiveByRowName returns the PvE objective with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEObjectiveByRowName(rowName string) (PvEObjective, bool) {
	pveObjectivesMu.RLock()
	loaded := pveObjectivesLoaded
	pveObjectivesMu.RUnlock()
	if !loaded {
		if err := LoadPvEObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE objectives: %v", err)
			return PvEObjective{}, false
		}
	}
	pveObjectivesMu.RLock()
	defer pveObjectivesMu.RUnlock()

	for _, objective := range pveObjectives {
		if objective.RowName == rowName {
			return objective, true
		}
	}
	return PvEObjective{}, false
}

// PvEObjectiveCount returns the total number of PvE objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEObjectiveCount() int {
	pveObjectivesMu.RLock()
	loaded := pveObjectivesLoaded
	pveObjectivesMu.RUnlock()
	if !loaded {
		if err := LoadPvEObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE objectives: %v", err)
			return 0
		}
	}
	pveObjectivesMu.RLock()
	defer pveObjectivesMu.RUnlock()

	return len(pveObjectives)
}

// PvEObjectiveByID returns the PvE objective with the specified ID
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEObjectiveByID(id string) (PvEObjective, bool) {
	pveObjectivesMu.RLock()
	loaded := pveObjectivesLoaded
	pveObjectivesMu.RUnlock()
	if !loaded {
		if err := LoadPvEObjectives(); err != nil {
			log.Printf("Warning: Failed to load PvE objectives: %v", err)
			return PvEObjective{}, false
		}
	}
	pveObjectivesMu.RLock()
	defer pveObjectivesMu.RUnlock()

	for _, objective := range pveObjectives {
		if objective.ID == id {
			return objective, true
		}
	}
	return PvEObjective{}, false
}