package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// PlayerMatchStat represents a player match statistic category from DN_PlayerMatchStatistics_DT.json
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
type PlayerMatchStat struct {
	CategoryID string `json:"m_ID"`            // Unique category identifier
	Name       string `json:"m_name"`          // Display name
	Priority   int32  `json:"m_priority"`      // Priority for sorting/display
	
	// Parsed fields for easier access
	ShortName string `json:"-"` // Extracted from CategoryID (e.g., "YPMSCID_Assists" -> "Assists")
}

// playerMatchStatsData holds the loaded player match statistics data
var (
	playerMatchStats     []PlayerMatchStat
	playerMatchStatsMu   sync.RWMutex
	playerMatchStatsOnce sync.Once
	playerMatchStatsLoadErr error
	playerMatchStatsLoaded bool
)

// LoadPlayerMatchStatistics loads player match statistics from DN_PlayerMatchStatistics_DT.json
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func LoadPlayerMatchStatistics() error {
	playerMatchStatsOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "DN_PlayerMatchStatistics_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			playerMatchStatsLoadErr = fmt.Errorf("failed to read DN_PlayerMatchStatistics_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			playerMatchStatsLoadErr = fmt.Errorf("failed to parse DN_PlayerMatchStatistics_DT.json: %w", err)
			return
		}

		playerMatchStatsMu.Lock()
		defer playerMatchStatsMu.Unlock()
		
		playerMatchStats = make([]PlayerMatchStat, 0, len(dt.Rows))
		for rowName, row := range dt.Rows {
			stat := PlayerMatchStat{
				CategoryID: row.GetString("m_ID"),
				Name:       row.GetString("m_name"),
				Priority:   row.GetInt32("m_priority"),
			}
			
			// Extract short name from CategoryID
			// Format: "EYPlayerMatchStatisticsCategoryID::YPMSCID_Assists"
			// We want: "Assists"
			if strings.Contains(stat.CategoryID, "::") {
				parts := strings.Split(stat.CategoryID, "::")
				if len(parts) == 2 {
					stat.ShortName = strings.TrimPrefix(parts[1], "YPMSCID_")
				}
			} else {
				stat.ShortName = rowName
			}
			
			playerMatchStats = append(playerMatchStats, stat)
		}

		// Sort by priority (descending) for consistent ordering
		sort.Slice(playerMatchStats, func(i, j int) bool {
			return playerMatchStats[i].Priority > playerMatchStats[j].Priority
		})

		playerMatchStatsLoaded = true
		log.Printf("Loaded %d player match statistics from DN_PlayerMatchStatistics_DT.json", len(playerMatchStats))
	})

	return playerMatchStatsLoadErr
}

// AllPlayerMatchStatistics returns all loaded player match statistics
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func AllPlayerMatchStatistics() []PlayerMatchStat {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return nil
		}
	}

	result := make([]PlayerMatchStat, len(playerMatchStats))
	copy(result, playerMatchStats)
	return result
}

// PlayerMatchStatByID returns the stat with the specified category ID
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func PlayerMatchStatByID(categoryID string) (PlayerMatchStat, bool) {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return PlayerMatchStat{}, false
		}
	}

	for _, stat := range playerMatchStats {
		if stat.CategoryID == categoryID {
			return stat, true
		}
	}
	return PlayerMatchStat{}, false
}

// PlayerMatchStatByShortName returns the stat with the specified short name
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func PlayerMatchStatByShortName(shortName string) (PlayerMatchStat, bool) {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return PlayerMatchStat{}, false
		}
	}

	for _, stat := range playerMatchStats {
		if stat.ShortName == shortName {
			return stat, true
		}
	}
	return PlayerMatchStat{}, false
}

// PlayerMatchStatCount returns the total number of player match statistics
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func PlayerMatchStatCount() int {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return 0
		}
	}

	return len(playerMatchStats)
}

// PlayerMatchStatPriorities returns all unique priority values from player match statistics
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func PlayerMatchStatPriorities() []int32 {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return nil
		}
	}

	// Collect unique priorities
	priorityMap := make(map[int32]bool)
	for _, stat := range playerMatchStats {
		priorityMap[stat.Priority] = true
	}

	// Convert to slice and sort
	priorities := make([]int32, 0, len(priorityMap))
	for priority := range priorityMap {
		priorities = append(priorities, priority)
	}
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i] < priorities[j]
	})

	return priorities
}

// PlayerMatchStatShortNames returns all short names from player match statistics
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func PlayerMatchStatShortNames() []string {
	playerMatchStatsMu.RLock()
	defer playerMatchStatsMu.RUnlock()
	
	if !playerMatchStatsLoaded {
		if err := LoadPlayerMatchStatistics(); err != nil {
			log.Printf("Warning: Failed to load player match statistics: %v", err)
			return nil
		}
	}

	names := make([]string, len(playerMatchStats))
	for i, stat := range playerMatchStats {
		names[i] = stat.ShortName
	}
	return names
}

// HasPlayerMatchStat checks if a stat with the given short name exists
// J3: Load DN_PlayerMatchStatistics_DT.json — match stat category definitions
func HasPlayerMatchStat(shortName string) bool {
	_, exists := PlayerMatchStatByShortName(shortName)
	return exists
}
