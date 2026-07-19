package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvEEvent represents a PvE event from DN_Events_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvEEvent struct {
	Name          string `json:"m_name"`
	DescShort     string `json:"m_descShort"`
	DescLong      string `json:"m_descLong"`
	Map           string `json:"m_map"`
	MapParameters string `json:"m_mapParameters"`
	GameMode      string `json:"m_gameMode"`
	RowName       string `json:"-"` // The row name from the DataTable
}

// pveEventsData holds the loaded PvE events data
var (
	pveEvents     []PvEEvent
	pveEventsMu   sync.RWMutex
	pveEventsOnce sync.Once
	pveEventsLoadErr error
	pveEventsLoaded bool
)

// LoadPvEEvents loads PvE events data from DN_Events_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvEEvents() error {
	pveEventsOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "DN_Events_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			pveEventsLoadErr = fmt.Errorf("failed to read DN_Events_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			pveEventsLoadErr = fmt.Errorf("failed to parse DN_Events_DT.json: %w", err)
			return
		}

		// Parse rows into PvEEvent structs
		events := make([]PvEEvent, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			event := PvEEvent{
				Name:          rowData.GetString("m_name"),
				DescShort:     rowData.GetString("m_descShort"),
				DescLong:      rowData.GetString("m_descLong"),
				Map:           rowData.GetString("m_map"),
				MapParameters: rowData.GetString("m_mapParameters"),
				GameMode:      rowData.GetString("m_gameMode"),
				RowName:       rowName,
			}
			events = append(events, event)
		}

		if len(events) == 0 {
			pveEventsLoadErr = fmt.Errorf("no PvE events found in DN_Events_DT.json")
			return
		}

		pveEvents = events
		pveEventsLoaded = true
		log.Printf("Loaded %d PvE events from DN_Events_DT.json", len(events))
	})

	return pveEventsLoadErr
}

// AllPvEEvents returns all loaded PvE events
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvEEvents() []PvEEvent {
	pveEventsMu.RLock()
	defer pveEventsMu.RUnlock()
	
	if !pveEventsLoaded {
		if err := LoadPvEEvents(); err != nil {
			log.Printf("Warning: Failed to load PvE events: %v", err)
			return nil
		}
	}

	return pveEvents
}

// PvEEventByRowName returns the PvE event with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEEventByRowName(rowName string) (PvEEvent, bool) {
	pveEventsMu.RLock()
	defer pveEventsMu.RUnlock()
	
	if !pveEventsLoaded {
		if err := LoadPvEEvents(); err != nil {
			log.Printf("Warning: Failed to load PvE events: %v", err)
			return PvEEvent{}, false
		}
	}

	for _, event := range pveEvents {
		if event.RowName == rowName {
			return event, true
		}
	}
	return PvEEvent{}, false
}

// PvEEventCount returns the total number of PvE events
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvEEventCount() int {
	pveEventsMu.RLock()
	defer pveEventsMu.RUnlock()
	
	if !pveEventsLoaded {
		if err := LoadPvEEvents(); err != nil {
			log.Printf("Warning: Failed to load PvE events: %v", err)
			return 0
		}
	}

	return len(pveEvents)
}