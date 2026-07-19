package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// PvESeason represents a PvE season from DN_Seasons_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
type PvESeason struct {
	Name        string `json:"m_name"`
	DescShort   string `json:"m_descShort"`
	DescLong    string `json:"m_descLong"`
	Map         string `json:"m_map"`
	MapParameters string `json:"m_mapParameters"`
	GameMode    string `json:"m_gameMode"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// pveSeasonsData holds the loaded PvE seasons data
var (
	pveSeasons     []PvESeason
	pveSeasonsMu   sync.RWMutex
	pveSeasonsOnce sync.Once
	pveSeasonsLoadErr error
	pveSeasonsLoaded bool
)

// LoadPvESeasons loads PvE seasons data from DN_Seasons_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func LoadPvESeasons() error {
	pveSeasonsOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("PVE", "DN_Seasons_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			pveSeasonsLoadErr = fmt.Errorf("failed to read DN_Seasons_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			pveSeasonsLoadErr = fmt.Errorf("failed to parse DN_Seasons_DT.json: %w", err)
			return
		}

		// Parse rows into PvESeason structs
		seasons := make([]PvESeason, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			season := PvESeason{
				Name:        rowData.GetString("m_name"),
				DescShort:   rowData.GetString("m_descShort"),
				DescLong:    rowData.GetString("m_descLong"),
				Map:         rowData.GetString("m_map"),
				MapParameters: rowData.GetString("m_mapParameters"),
				GameMode:    rowData.GetString("m_gameMode"),
				RowName:     rowName,
			}
			seasons = append(seasons, season)
		}

		if len(seasons) == 0 {
			pveSeasonsLoadErr = fmt.Errorf("no PvE seasons found in DN_Seasons_DT.json")
			return
		}

		pveSeasons = seasons
		pveSeasonsLoaded = true
		log.Printf("Loaded %d PvE seasons from DN_Seasons_DT.json", len(seasons))
	})

	return pveSeasonsLoadErr
}

// AllPvESeasons returns all loaded PvE seasons
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func AllPvESeasons() []PvESeason {
	pveSeasonsMu.RLock()
	defer pveSeasonsMu.RUnlock()
	
	if !pveSeasonsLoaded {
		if err := LoadPvESeasons(); err != nil {
			log.Printf("Warning: Failed to load PvE seasons: %v", err)
			return nil
		}
	}

	return pveSeasons
}

// PvESeasonByRowName returns the PvE season with the specified row name
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvESeasonByRowName(rowName string) (PvESeason, bool) {
	pveSeasonsMu.RLock()
	defer pveSeasonsMu.RUnlock()
	
	if !pveSeasonsLoaded {
		if err := LoadPvESeasons(); err != nil {
			log.Printf("Warning: Failed to load PvE seasons: %v", err)
			return PvESeason{}, false
		}
	}

	for _, season := range pveSeasons {
		if season.RowName == rowName {
			return season, true
		}
	}
	return PvESeason{}, false
}

// PvESeasonCount returns the total number of PvE seasons
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func PvESeasonCount() int {
	pveSeasonsMu.RLock()
	defer pveSeasonsMu.RUnlock()
	
	if !pveSeasonsLoaded {
		if err := LoadPvESeasons(); err != nil {
			log.Printf("Warning: Failed to load PvE seasons: %v", err)
			return 0
		}
	}

	return len(pveSeasons)
}