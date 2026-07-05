package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Perk represents a perk from the perk DataTable
// Perks are similar to officers but typically have different field structures
type Perk struct {
	// Core perk fields - these would match the DataTable structure
	// Based on the pattern from officers and ship feats, perks likely have:
	Enabling      string `json:"m_enabling,omitempty"`
	Triggers      string `json:"m_triggers,omitempty"`
	Effects       string `json:"m_effects,omitempty"`
	StackOnAdding bool   `json:"m_stackOnAdding,omitempty"`
	IsPerkFeat    bool   `json:"m_isPerkFeat,omitempty"`

	// Perk-specific metadata (cross-referenced from ItemIDRegister)
	PerkID    int32  `json:"-"` // Cross-referenced from ItemIDRegister
	PerkName  string `json:"-"` // From ItemIDRegister or derived from path
	AssetPath string `json:"-"` // Cross-referenced from ItemIDRegister
	Category  string `json:"-"` // Perk category (COM, ENG, NAV, WEAPON, etc.)

	// Parsed DSL components (reusing ship feat DSL parser)
	ParsedEffects []FeatEffect `json:"-"`
}

// perksData holds the loaded perk data
var (
	perks     = make(map[string]Perk)
	perksByID = make(map[int32]Perk)
	perksLock sync.RWMutex
	perksLoaded bool
)

// LoadPerks loads perk data from ItemIDRegister entries that match perk patterns
// F5: Load perk data from ItemIDTable category YPerk entries
// Since ItemIDRegister doesn't have explicit Category fields, we identify perks by their path
func LoadPerks() {
	perksLock.Lock()
	defer perksLock.Unlock()

	if perksLoaded {
		return
	}

	// F5: Load perk data from ItemIDRegister entries with "Perk" in their path
	loadedCount := 0
	crossReferencedCount := 0

	for _, item := range itemCatalog {
		// Identify perks by their asset path containing "/Perk/"
		if isPerkPath(item.AssetPath) {
			perk := createPerkFromItem(item)
			
			// Extract perk name from the path
			perkName := extractPerkNameFromPath(item.AssetPath)
			perk.PerkName = perkName
			perk.AssetPath = item.AssetPath
			perk.PerkID = item.ItemID
			
			// Determine category from perk name prefix
			perk.Category = extractPerkCategory(perkName)
			
			// Store in maps
			perks[perkName] = perk
			perksByID[item.ItemID] = perk
			loadedCount++
			
			// Count as cross-referenced since we're using ItemIDRegister
			crossReferencedCount++
		}
	}

	perksLoaded = true
	log.Printf("Loaded %d perks (%d cross-referenced with ItemIDRegister)", loadedCount, crossReferencedCount)
}

// isPerkPath checks if an asset path belongs to a perk
func isPerkPath(path string) bool {
	// Check if path contains "/Perk/" which indicates it's a perk
	return strings.Contains(path, "/Perk/")
}

// extractPerkNameFromPath extracts the perk name from its asset path
func extractPerkNameFromPath(path string) string {
	// Path format: /Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP
	// We want: PRK_COM_AbiInc_AbiKill_BP
	if path == "" {
		return ""
	}
	
	// Find the last '/' and take everything after it
	lastSlash := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			lastSlash = i
			break
		}
	}
	
	if lastSlash >= 0 && lastSlash < len(path)-1 {
		return path[lastSlash+1:]
	}
	
	return path
}

// extractPerkCategory extracts the category from perk name
// Perk names follow pattern: PRK_{CATEGORY}_{effect}_BP
func extractPerkCategory(perkName string) string {
	// Remove the PRK_ prefix and _BP suffix if present
	name := perkName
	if len(name) > 4 && name[:4] == "PRK_" {
		name = name[4:]
	}
	if len(name) > 3 && name[len(name)-3:] == "_BP" {
		name = name[:len(name)-3]
	}
	
	// Find the first underscore to get the category
	for i, char := range name {
		if char == '_' {
			return name[:i]
		}
	}
	
	return "UNKNOWN"
}

// createPerkFromItem creates a Perk struct from an ItemIDRegister entry
// F5: Load perk data from ItemIDTable category YPerk entries
func createPerkFromItem(item ItemMetadata) Perk {
	// Create a perk with default values for DataTable fields
	// When we have the actual perk DataTable, we can populate these properly
	perk := Perk{
		// Default DataTable field values
		// These would be populated from actual DataTable when available
		Enabling:      "OnAcquire()", // Common default for perks
		Triggers:      "OnEnable()",  // Common default for perks
		Effects:       "",            // Will be populated from DataTable
		StackOnAdding: true,          // Common default for perks
		IsPerkFeat:    true,          // All perks are perk feats
		
		// Metadata from ItemIDRegister
		PerkID:    item.ItemID,
		PerkName:  "", // Will be set by caller
		AssetPath: "", // Will be set by caller
		Category:  "", // Will be set by caller
		ParsedEffects: []FeatEffect{},
	}
	
	return perk
}

// PerkByID returns a perk by its PerkID
func PerkByID(id int32) (Perk, bool) {
	perksLock.RLock()
	defer perksLock.RUnlock()

	perk, exists := perksByID[id]
	return perk, exists
}

// PerkByName returns a perk by its row name
func PerkByName(name string) (Perk, bool) {
	perksLock.RLock()
	defer perksLock.RUnlock()

	perk, exists := perks[name]
	return perk, exists
}

// AllPerks returns all loaded perks
func AllPerks() []Perk {
	perksLock.RLock()
	defer perksLock.RUnlock()

	perksList := make([]Perk, 0, len(perks))
	for _, perk := range perks {
		perksList = append(perksList, perk)
	}
	return perksList
}

// PerkCount returns the number of loaded perks
func PerkCount() int {
	perksLock.RLock()
	defer perksLock.RUnlock()
	return len(perks)
}

// AllPerkIDs returns all perk IDs
func AllPerkIDs() []int32 {
	perksLock.RLock()
	defer perksLock.RUnlock()

	ids := make([]int32, 0, len(perksByID))
	for id := range perksByID {
		ids = append(ids, id)
	}
	return ids
}

// LoadPerksFromDataTable would load perks from a DataTable file
// This is a placeholder for when we have the actual perk DataTable
func LoadPerksFromDataTable(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var dataTable struct {
		Rows map[string]struct {
			Enabling      string `json:"m_enabling"`
			Triggers      string `json:"m_triggers"`
			Effects       string `json:"m_effects"`
			StackOnAdding bool   `json:"m_stackOnAdding"`
			IsPerkFeat    bool   `json:"m_isPerkFeat"`
		} `json:"rows"`
	}

	if err := json.Unmarshal(data, &dataTable); err != nil {
		return err
	}

	perksLock.Lock()
	defer perksLock.Unlock()

	for rowName, rowData := range dataTable.Rows {
		perk := Perk{
			Enabling:      rowData.Enabling,
			Triggers:      rowData.Triggers,
			Effects:       rowData.Effects,
			StackOnAdding: rowData.StackOnAdding,
			IsPerkFeat:    rowData.IsPerkFeat,
			PerkName:      rowName, // Use row name as default name
			AssetPath:     filepath.Join("Game/Generic/Officer/Perk", rowName),
		}

		// Parse effects using the same DSL parser as ship feats
		if rowData.Effects != "" {
			perk.ParsedEffects = ParseFeatEffects(rowData.Effects)
		}

		perks[rowName] = perk
	}

	log.Printf("Loaded %d perks from %s", len(dataTable.Rows), filepath.Base(filePath))
	return nil
}
