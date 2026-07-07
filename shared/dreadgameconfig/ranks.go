package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Rank represents a player rank from DN_Ranks_Player.json
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
type Rank struct {
	RankID   int32  `json:"-"`           // Numeric rank ID (0-50)
	RankName string `json:"m_rankName"` // Display name of the rank
}

// ranksData holds the loaded rank data
var (
	ranks     []Rank
	ranksMu   sync.RWMutex
	ranksOnce sync.Once
	ranksLoaded bool
)

// LoadRanks loads rank data from DN_Ranks_Player.json
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
func LoadRanks() error {
	var loadErr error
	ranksOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "Progression", "Ranks", "DN_Ranks_Player.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_Ranks_Player.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_Ranks_Player.json: %w", err)
			return
		}

		ranksMu.Lock()
		defer ranksMu.Unlock()
		
		ranks = make([]Rank, 0, len(dt.Rows))
		for rowName, row := range dt.Rows {
			// Parse rank ID from row name
			var rankID int32
			if _, err := fmt.Sscanf(rowName, "%d", &rankID); err != nil {
				// If rowName is not a number, skip or use a default
				continue
			}
			
			rank := Rank{
				RankID:   rankID,
				RankName: row.GetString("m_rankName"),
			}
			ranks = append(ranks, rank)
		}

		// Sort ranks by RankID to ensure consistent ordering
		for i := 0; i < len(ranks)-1; i++ {
			for j := i + 1; j < len(ranks); j++ {
				if ranks[i].RankID > ranks[j].RankID {
					ranks[i], ranks[j] = ranks[j], ranks[i]
				}
			}
		}

		ranksLoaded = true
		log.Printf("Loaded %d ranks from DN_Ranks_Player.json", len(ranks))
	})

	return loadErr
}

// AllRanks returns all loaded ranks
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
func AllRanks() []Rank {
	ranksMu.RLock()
	defer ranksMu.RUnlock()
	
	if !ranksLoaded {
		if err := LoadRanks(); err != nil {
			log.Printf("Warning: Failed to load ranks: %v", err)
			return nil
		}
	}

	result := make([]Rank, len(ranks))
	copy(result, ranks)
	return result
}

// RankByID returns the rank with the specified ID
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
func RankByID(rankID int32) (Rank, bool) {
	ranksMu.RLock()
	defer ranksMu.RUnlock()
	
	if !ranksLoaded {
		if err := LoadRanks(); err != nil {
			log.Printf("Warning: Failed to load ranks: %v", err)
			return Rank{}, false
		}
	}

	for _, rank := range ranks {
		if rank.RankID == rankID {
			return rank, true
		}
	}
	return Rank{}, false
}

// RankByName returns the rank with the specified name
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
func RankByName(name string) (Rank, bool) {
	ranksMu.RLock()
	defer ranksMu.RUnlock()
	
	if !ranksLoaded {
		if err := LoadRanks(); err != nil {
			log.Printf("Warning: Failed to load ranks: %v", err)
			return Rank{}, false
		}
	}

	for _, rank := range ranks {
		if rank.RankName == name {
			return rank, true
		}
	}
	return Rank{}, false
}

// RankCount returns the total number of ranks
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
func RankCount() int {
	ranksMu.RLock()
	defer ranksMu.RUnlock()
	
	if !ranksLoaded {
		if err := LoadRanks(); err != nil {
			log.Printf("Warning: Failed to load ranks: %v", err)
			return 0
		}
	}

	return len(ranks)
}

// RankXPThreshold returns the XP threshold for a given rank
// J1: Load DN_Ranks_Player.json — replace hardcoded 51-rank ladder with extracted data
// Note: XP thresholds are not in the extracted data, so we use the existing hardcoded values
// This function can be updated when proper XP threshold data is available
func RankXPThreshold(rank int32) int32 {
	// These thresholds are based on the existing hardcoded values in mmogbrain/handlers/handlers.go
	// They can be replaced with loaded data when available
	if rank < 2 {
		return 0
	}
	if rank <= 5 {
		return 1000
	}
	if rank <= 10 {
		return 2000
	}
	if rank <= 20 {
		return 3500
	}
	if rank <= 30 {
		return 5000
	}
	if rank <= 40 {
		return 7500
	}
	if rank <= 50 {
		return 10000
	}
	return 15000
}
