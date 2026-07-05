package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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

// LoadPerks loads perk data from the ItemIDRegister entries that match perk patterns
// F4: Define Go struct `Perk` for perk DataTable fields
// This function would load perks from a DataTable file if one exists, or from ItemIDRegister
func LoadPerks() {
	perksLock.Lock()
	defer perksLock.Unlock()

	if perksLoaded {
		return
	}

	// For now, we'll load perks from ItemIDRegister entries that contain "Perk" in their path
	// This is a placeholder for when we have actual perk DataTable files
	perksLoaded = true
	log.Printf("Perk loading placeholder - would load from DataTable when available")
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
