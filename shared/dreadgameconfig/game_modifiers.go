package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// GameModifier represents a game mode modifier from DN_GameModifiers_DT.json
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
type GameModifier struct {
	GameModeName   string `json:"m_gameModeName"`
	Excludes       string `json:"m_excludes"`
	Feats          string `json:"m_feats"`
	AffectedTeam   string `json:"m_affectedTeam"`
	
	// Parsed fields for easier access
	FeatList       []string `json:"-"` // Parsed from m_feats (split by semicolon)
	ExcludeList    []string `json:"-"` // Parsed from m_excludes (split by semicolon)
}

// gameModifiersData holds the loaded game modifier data
var (
	gameModifiers     []GameModifier
	gameModifiersMu   sync.RWMutex
	gameModifiersOnce sync.Once
	gameModifiersLoaded bool
)

// LoadGameModifiers loads game modifier data from DN_GameModifiers_DT.json
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func LoadGameModifiers() error {
	var loadErr error
	gameModifiersOnce.Do(func() {
		filePath := filepath.Join("..", "..", "data", "datatables", "Progression", "GameModifiers", "DN_GameModifiers_DT.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_GameModifiers_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_GameModifiers_DT.json: %w", err)
			return
		}

		gameModifiersMu.Lock()
		defer gameModifiersMu.Unlock()
		
		gameModifiers = make([]GameModifier, 0, len(dt.Rows))
		for rowName, row := range dt.Rows {
			// Skip the config row
			if rowName == "DN_GameModifiers_DT.cfg" {
				continue
			}
			
			modifier := GameModifier{
				GameModeName: row.GetString("m_gameModeName"),
				Excludes:    row.GetString("m_excludes"),
				Feats:       row.GetString("m_feats"),
				AffectedTeam: row.GetString("m_affectedTeam"),
			}
			
			// Parse feat list (split by semicolon)
			if modifier.Feats != "" {
				modifier.FeatList = strings.Split(modifier.Feats, ";")
			}
			
			// Parse exclude list (split by semicolon)
			if modifier.Excludes != "" {
				modifier.ExcludeList = strings.Split(modifier.Excludes, ";")
			}
			
			gameModifiers = append(gameModifiers, modifier)
		}

		gameModifiersLoaded = true
		log.Printf("Loaded %d game modifiers from DN_GameModifiers_DT.json", len(gameModifiers))
	})

	return loadErr
}

// AllGameModifiers returns all loaded game modifiers
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func AllGameModifiers() []GameModifier {
	gameModifiersMu.RLock()
	defer gameModifiersMu.RUnlock()
	
	if !gameModifiersLoaded {
		if err := LoadGameModifiers(); err != nil {
			log.Printf("Warning: Failed to load game modifiers: %v", err)
			return nil
		}
	}

	result := make([]GameModifier, len(gameModifiers))
	copy(result, gameModifiers)
	return result
}

// GameModifierByName returns the game modifier with the specified name
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func GameModifierByName(name string) (GameModifier, bool) {
	gameModifiersMu.RLock()
	defer gameModifiersMu.RUnlock()
	
	if !gameModifiersLoaded {
		if err := LoadGameModifiers(); err != nil {
			log.Printf("Warning: Failed to load game modifiers: %v", err)
			return GameModifier{}, false
		}
	}

	for _, modifier := range gameModifiers {
		if modifier.GameModeName == name {
			return modifier, true
		}
	}
	return GameModifier{}, false
}

// GameModifierCount returns the total number of game modifiers
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func GameModifierCount() int {
	gameModifiersMu.RLock()
	defer gameModifiersMu.RUnlock()
	
	if !gameModifiersLoaded {
		if err := LoadGameModifiers(); err != nil {
			log.Printf("Warning: Failed to load game modifiers: %v", err)
			return 0
		}
	}

	return len(gameModifiers)
}

// GameModifierFeats returns all feat names from all game modifiers
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func GameModifierFeats() []string {
	gameModifiersMu.RLock()
	defer gameModifiersMu.RUnlock()
	
	if !gameModifiersLoaded {
		if err := LoadGameModifiers(); err != nil {
			log.Printf("Warning: Failed to load game modifiers: %v", err)
			return nil
		}
	}

	// Collect all unique feats
	featMap := make(map[string]bool)
	for _, modifier := range gameModifiers {
		for _, feat := range modifier.FeatList {
			featMap[feat] = true
		}
	}

	// Convert to slice
	feats := make([]string, 0, len(featMap))
	for feat := range featMap {
		feats = append(feats, feat)
	}

	return feats
}

// HasGameModifierFeat checks if a specific feat is used in any game modifier
// J2: Load DN_GameModifiers_DT.json — game mode tuning values
func HasGameModifierFeat(feat string) bool {
	feats := GameModifierFeats()
	for _, f := range feats {
		if f == feat {
			return true
		}
	}
	return false
}
