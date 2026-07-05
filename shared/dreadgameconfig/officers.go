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

// OfficerCard represents an officer card from the DataTable with trigger/effect DSL
// Matches DN_Officers_OTS_DT.json structure (F1)
type OfficerCard struct {
	// Core officer fields
	Enabling       string `json:"m_enabling"`
	Triggers       string `json:"m_triggers"`
	Effects        string `json:"m_effects"`
	StackOnAdding  bool   `json:"m_stackOnAdding"`
	IsPerkFeat     bool   `json:"m_isPerkFeat"`
	
	// Officer-specific metadata
	OfficerID   int32  `json:"-"` // Cross-referenced from ItemIDRegister
	OfficerName string `json:"-"` // From ItemIDRegister or derived
	AssetPath   string `json:"-"` // Cross-referenced from ItemIDRegister
	Rarity      string `json:"-"` // Officer rarity (Common, Rare, Epic, Legendary)
	
	// Parsed DSL components (reusing ship feat DSL parser)
	ParsedEffects []FeatEffect `json:"-"`
}

// officersData holds the loaded officer data
var (
	officers     = make(map[string]OfficerCard)
	officersByID = make(map[int32]OfficerCard)
	officersLock sync.RWMutex
	officersLoaded bool
)

// LoadOfficers loads all officer data from DataTables (F2)
func LoadOfficers() error {
	officersLock.Lock()
	defer officersLock.Unlock()
	
	if officersLoaded {
		return nil
	}
	
	// Load ItemIDRegister for cross-referencing
	itemIDRegister, err := loadItemIDRegister()
	if err != nil {
		log.Printf("Warning: Failed to load ItemIDRegister for officers: %v", err)
		// Continue without ItemID cross-referencing
	}
	
	// Load officers DataTable
	officerFile := DataTablePath(filepath.Join("DN_Officers_OTS_DT.json"))
	data, err := os.ReadFile(officerFile)
	if err != nil {
		return fmt.Errorf("read officers file: %w", err)
	}
	
	var dt DataTable
	if err := json.Unmarshal(data, &dt); err != nil {
		return fmt.Errorf("parse officers file: %w", err)
	}
	
	loadedCount := 0
	crossReferencedCount := 0
	
	for rowName, row := range dt.Rows {
		officer, err := parseOfficerRow(row)
		if err != nil {
			return fmt.Errorf("parse officer row %s: %w", rowName, err)
		}
		
		// Cross-reference with ItemIDRegister
		if itemIDRegister != nil {
			assetPath := tryFindOfficerAssetPath(rowName, itemIDRegister)
			if assetPath != "" {
				officer.AssetPath = assetPath
				// Try to find corresponding ItemID and name
				for _, regEntry := range itemIDRegister {
					if regEntry.Path == assetPath {
						officer.OfficerID = regEntry.ItemID
						officer.OfficerName = extractOfficerNameFromPath(assetPath)
						crossReferencedCount++
						break
					}
				}
			}
		}
		
		// Use row name as the key
		officers[rowName] = officer
		
		// Also store by officer ID if available
		if officer.OfficerID != 0 {
			officersByID[officer.OfficerID] = officer
		}
		
		loadedCount++
	}
	
	officersLoaded = true
	log.Printf("Loaded %d officers (%d cross-referenced with ItemIDRegister)", loadedCount, crossReferencedCount)
	return nil
}

// parseOfficerRow parses a single officer row from DataTable
func parseOfficerRow(row Row) (OfficerCard, error) {
	var officer OfficerCard
	
	// Marshal and unmarshal to handle the dynamic structure
	rowData, err := json.Marshal(row)
	if err != nil {
		return officer, fmt.Errorf("marshal row: %w", err)
	}
	
	if err := json.Unmarshal(rowData, &officer); err != nil {
		return officer, fmt.Errorf("unmarshal row: %w", err)
	}
	
	// Parse the effects DSL (reusing ship feat DSL parser)
	officer.ParsedEffects = ParseFeatEffects(officer.Effects)
	
	return officer, nil
}

// tryFindOfficerAssetPath attempts to find the asset path for an officer row name
func tryFindOfficerAssetPath(rowName string, itemIDRegister []ItemIDRegisterEntry) string {
	// Officer row names typically look like: YOfficerCard_OTS_DT_01
	// Asset paths typically look like: /Game/Generic/Officer/.../YOfficerCard_OTS_DT_01
	
	for _, entry := range itemIDRegister {
		if strings.Contains(entry.Path, "Officer") && strings.Contains(entry.Path, rowName) {
			return entry.Path
		}
	}
	return ""
}

// extractOfficerNameFromPath extracts the officer name from the asset path
func extractOfficerNameFromPath(path string) string {
	// Example: /Game/Generic/Officer/Assault/YOfficerCard_Assault_01_BP
	// Extract: "Assault"
	
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if strings.Contains(part, "YOfficerCard_") {
			// Remove the YOfficerCard_ prefix and _01_BP suffix
			name := strings.TrimPrefix(part, "YOfficerCard_")
			name = strings.TrimSuffix(name, "_01_BP")
			name = strings.TrimSuffix(name, "_BP")
			// Split by underscore and take the first part
			nameParts := strings.Split(name, "_")
			if len(nameParts) > 0 {
				return nameParts[0]
			}
			return name
		}
	}
	return "Unknown Officer"
}

// OfficerByID returns an officer by its row name
func OfficerByID(id string) (OfficerCard, bool) {
	officersLock.RLock()
	defer officersLock.RUnlock()
	
	officer, ok := officers[id]
	return officer, ok
}

// OfficerByItemID returns an officer by its ItemID
func OfficerByItemID(itemID int32) (OfficerCard, bool) {
	officersLock.RLock()
	defer officersLock.RUnlock()
	
	officer, ok := officersByID[itemID]
	return officer, ok
}

// AllOfficers returns all loaded officers
func AllOfficers() map[string]OfficerCard {
	officersLock.RLock()
	defer officersLock.RUnlock()
	
	// Return a copy to avoid race conditions
	officersCopy := make(map[string]OfficerCard, len(officers))
	for k, v := range officers {
		officersCopy[k] = v
	}
	return officersCopy
}

// OfficerCount returns the total number of officers loaded
func OfficerCount() int {
	officersLock.RLock()
	defer officersLock.RUnlock()
	
	return len(officers)
}

// OfficerIDs returns all officer IDs that have been loaded
func OfficerIDs() []string {
	officersLock.RLock()
	defer officersLock.RUnlock()
	
	ids := make([]string, 0, len(officers))
	for id := range officers {
		ids = append(ids, id)
	}
	return ids
}