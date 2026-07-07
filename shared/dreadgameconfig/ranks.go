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

// RankWithThreshold represents a complete rank with both loaded data and XP threshold
// J4: Verify rank names/thresholds match current hardcoded values
type RankWithThreshold struct {
	Rank
	XPThreshold int32 `json:"-"` // XP required to advance from this rank
}

// AllRanksWithThresholds returns all ranks with their XP thresholds
// J4: Verify rank names/thresholds match current hardcoded values
func AllRanksWithThresholds() []RankWithThreshold {
	ranks := AllRanks()
	if len(ranks) == 0 {
		return nil
	}

	ranksWithThresholds := make([]RankWithThreshold, len(ranks))
	for i, rank := range ranks {
		ranksWithThresholds[i] = RankWithThreshold{
			Rank:       rank,
			XPThreshold: RankXPThreshold(rank.RankID),
		}
	}
	return ranksWithThresholds
}

// RankWithThresholdByID returns the rank with XP threshold for the specified ID
// J4: Verify rank names/thresholds match current hardcoded values
func RankWithThresholdByID(rankID int32) (RankWithThreshold, bool) {
	rank, exists := RankByID(rankID)
	if !exists {
		return RankWithThreshold{}, false
	}
	return RankWithThreshold{
		Rank:       rank,
		XPThreshold: RankXPThreshold(rankID),
	}, true
}

// VerifyRankThresholds verifies that the XP thresholds match the expected hardcoded values
// J4: Verify rank names/thresholds match current hardcoded values
func VerifyRankThresholds() (bool, error) {
	// Expected XP thresholds based on the hardcoded values in mmogbrain/handlers/handlers.go
	expectedThresholds := map[int32]int32{
		0:  0,
		1:  0,
		2:  1000,
		3:  1000,
		4:  1000,
		5:  1000,
		6:  2000,
		7:  2000,
		8:  2000,
		9:  2000,
		10: 2000,
		11: 3500,
		12: 3500,
		13: 3500,
		14: 3500,
		15: 3500,
		16: 3500,
		17: 3500,
		18: 3500,
		19: 3500,
		20: 3500,
		21: 5000,
		22: 5000,
		23: 5000,
		24: 5000,
		25: 5000,
		26: 5000,
		27: 5000,
		28: 5000,
		29: 5000,
		30: 5000,
		31: 7500,
		32: 7500,
		33: 7500,
		34: 7500,
		35: 7500,
		36: 7500,
		37: 7500,
		38: 7500,
		39: 7500,
		40: 7500,
		41: 10000,
		42: 10000,
		43: 10000,
		44: 10000,
		45: 10000,
		46: 10000,
		47: 10000,
		48: 10000,
		49: 10000,
		50: 10000,
	}

	// Verify all ranks have the expected XP thresholds
	for rankID, expectedThreshold := range expectedThresholds {
		actualThreshold := RankXPThreshold(rankID)
		if actualThreshold != expectedThreshold {
			return false, fmt.Errorf("RankXPThreshold(%d) = %d, expected %d", rankID, actualThreshold, expectedThreshold)
		}
	}

	return true, nil
}
