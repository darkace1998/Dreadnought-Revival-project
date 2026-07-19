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

// DevLoadout represents a development loadout definition from LoadoutDevelopmentTable.json
// Contains weapon, ability, and perk slot ItemIDs for hero ships
type DevLoadout struct {
	ID               string `json:"ID"`
	ShipID           int32  `json:"ShipID"`
	Name             string `json:"Name"`
	Class            int32  `json:"Class"`
	WeaponPrimary    int32  `json:"WeaponPrimary"`
	WeaponSecondary  int32  `json:"WeaponSecondary"`
	AbilityPrimary   int32  `json:"AbilityPrimary"`
	AbilitySecondary int32  `json:"AbilitySecondary"`
	AbilityPerimeter int32  `json:"AbilityPerimeter"`
	AbilityInternal  int32  `json:"AbilityInternal"`
	PerkCom          int32  `json:"PerkCom"`
	PerkWeapon       int32  `json:"PerkWeapon"`
	PerkNavigation   int32  `json:"PerkNavigation"`
	PerkEngineer     int32  `json:"PerkEngineer"`
	DisplayInfo      string `json:"DisplayInfo"`
}

// devLoadouts stores all loaded development loadouts
var (
	devLoadouts     []DevLoadout
	devLoadoutsMu   sync.RWMutex
	devLoadoutsOnce sync.Once
	devLoadoutsLoadErr error
)

// LoadDevLoadouts loads the LoadoutDevelopmentTable.json file
func LoadDevLoadouts() error {
	devLoadoutsOnce.Do(func() {
		filePath := LoadoutPath(filepath.Join("LoadoutDevelopmentTable.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			devLoadoutsLoadErr = fmt.Errorf("failed to read LoadoutDevelopmentTable.json: %w", err)
			return
		}

		var table struct {
			LoadoutTable []DevLoadout `json:"LoadoutTable"`
		}
		
		if err := json.Unmarshal(data, &table); err != nil {
			devLoadoutsLoadErr = fmt.Errorf("failed to parse LoadoutDevelopmentTable.json: %w", err)
			return
		}

		devLoadoutsMu.Lock()
		devLoadouts = table.LoadoutTable
		devLoadoutsMu.Unlock()

		log.Printf("Loaded %d development loadouts from LoadoutDevelopmentTable.json", len(devLoadouts))
	})

	return devLoadoutsLoadErr
}

// GetAllDevLoadouts returns all development loadouts
func GetAllDevLoadouts() []DevLoadout {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	result := make([]DevLoadout, len(devLoadouts))
	copy(result, devLoadouts)
	return result
}

// GetDevLoadoutByShipID returns the development loadout for a specific ship
func GetDevLoadoutByShipID(shipID int32) (*DevLoadout, bool) {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	for i := range devLoadouts {
		if devLoadouts[i].ShipID == shipID {
			return &devLoadouts[i], true
		}
	}
	return nil, false
}

// GetDevLoadoutByID returns the development loadout by its ID
func GetDevLoadoutByID(id string) (*DevLoadout, bool) {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	for i := range devLoadouts {
		if devLoadouts[i].ID == id {
			return &devLoadouts[i], true
		}
	}
	return nil, false
}

// GetDevLoadoutCount returns the total number of development loadouts
func GetDevLoadoutCount() int {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	return len(devLoadouts)
}

// GetAllDevLoadoutShipIDs returns all ship IDs that have development loadouts
func GetAllDevLoadoutShipIDs() []int32 {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	shipIDs := make([]int32, len(devLoadouts))
	for i := range devLoadouts {
		shipIDs[i] = devLoadouts[i].ShipID
	}
	return shipIDs
}

// HasDevLoadout checks if a ship has a development loadout
func HasDevLoadout(shipID int32) bool {
	_, exists := GetDevLoadoutByShipID(shipID)
	return exists
}

// GetDevLoadoutsByClass returns all development loadouts for a specific ship class
func GetDevLoadoutsByClass(class int32) []DevLoadout {
	devLoadoutsMu.RLock()
	defer devLoadoutsMu.RUnlock()
	
	var result []DevLoadout
	for _, loadout := range devLoadouts {
		if loadout.Class == class {
			result = append(result, loadout)
		}
	}
	return result
}

// DevLoadoutSlotValidation represents the validation result for a loadout's slot ItemIDs
// I2: Cross-reference loadout slot ItemIDs with weapon/ability/perk data from phases B/E
// This struct holds the validation results for each slot in a loadout
type DevLoadoutSlotValidation struct {
	LoadoutID        string
	ShipID          int32
	LoadoutName     string
	ShipClass       int32
	
	// Weapon slot validation
	WeaponPrimaryValid    bool
	WeaponPrimaryItemID   int32
	WeaponPrimaryName     string
	WeaponSecondaryValid  bool
	WeaponSecondaryItemID int32
	WeaponSecondaryName   string
	
	// Ability slot validation
	AbilityPrimaryValid   bool
	AbilityPrimaryItemID  int32
	AbilityPrimaryName    string
	AbilitySecondaryValid bool
	AbilitySecondaryItemID int32
	AbilitySecondaryName  string
	AbilityPerimeterValid bool
	AbilityPerimeterItemID int32
	AbilityPerimeterName  string
	AbilityInternalValid  bool
	AbilityInternalItemID int32
	AbilityInternalName   string
	
	// Perk slot validation
	PerkComValid        bool
	PerkComItemID       int32
	PerkComName         string
	PerkWeaponValid     bool
	PerkWeaponItemID    int32
	PerkWeaponName      string
	PerkNavigationValid bool
	PerkNavigationItemID int32
	PerkNavigationName  string
	PerkEngineerValid   bool
	PerkEngineerItemID  int32
	PerkEngineerName    string
	
	// Summary statistics
	TotalSlots      int
	ValidSlots      int
	InvalidSlots    int
	InvalidSlotList []string
}

// ValidateLoadoutSlots validates all ItemIDs in a loadout against known weapons, abilities, and perks
// Returns a validation struct with detailed results for each slot
func ValidateLoadoutSlots(loadout DevLoadout) DevLoadoutSlotValidation {
	validation := DevLoadoutSlotValidation{
		LoadoutID:    loadout.ID,
		ShipID:      loadout.ShipID,
		LoadoutName: loadout.Name,
		ShipClass:   loadout.Class,
		TotalSlots:  10, // 2 weapon + 4 ability + 4 perk slots
	}

	// Validate weapon slots
	if loadout.WeaponPrimary != 0 {
		if weapon, exists := WeaponByID(loadout.WeaponPrimary); exists {
			validation.WeaponPrimaryValid = true
			validation.WeaponPrimaryItemID = loadout.WeaponPrimary
			validation.WeaponPrimaryName = weapon.SlotType
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("WeaponPrimary(%d)", loadout.WeaponPrimary))
		}
	} else {
		// ItemID 0 means no weapon equipped - this is valid
		validation.WeaponPrimaryValid = true
		validation.ValidSlots++
	}

	if loadout.WeaponSecondary != 0 {
		if weapon, exists := WeaponByID(loadout.WeaponSecondary); exists {
			validation.WeaponSecondaryValid = true
			validation.WeaponSecondaryItemID = loadout.WeaponSecondary
			validation.WeaponSecondaryName = weapon.SlotType
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("WeaponSecondary(%d)", loadout.WeaponSecondary))
		}
	} else {
		validation.WeaponSecondaryValid = true
		validation.ValidSlots++
	}

	// Validate ability slots
	if loadout.AbilityPrimary != 0 {
		if ability, exists := AbilityByItemID(loadout.AbilityPrimary); exists {
			validation.AbilityPrimaryValid = true
			validation.AbilityPrimaryItemID = loadout.AbilityPrimary
			validation.AbilityPrimaryName = ability.AbilityName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("AbilityPrimary(%d)", loadout.AbilityPrimary))
		}
	} else {
		validation.AbilityPrimaryValid = true
		validation.ValidSlots++
	}

	if loadout.AbilitySecondary != 0 {
		if ability, exists := AbilityByItemID(loadout.AbilitySecondary); exists {
			validation.AbilitySecondaryValid = true
			validation.AbilitySecondaryItemID = loadout.AbilitySecondary
			validation.AbilitySecondaryName = ability.AbilityName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("AbilitySecondary(%d)", loadout.AbilitySecondary))
		}
	} else {
		validation.AbilitySecondaryValid = true
		validation.ValidSlots++
	}

	if loadout.AbilityPerimeter != 0 {
		if ability, exists := AbilityByItemID(loadout.AbilityPerimeter); exists {
			validation.AbilityPerimeterValid = true
			validation.AbilityPerimeterItemID = loadout.AbilityPerimeter
			validation.AbilityPerimeterName = ability.AbilityName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("AbilityPerimeter(%d)", loadout.AbilityPerimeter))
		}
	} else {
		validation.AbilityPerimeterValid = true
		validation.ValidSlots++
	}

	if loadout.AbilityInternal != 0 {
		if ability, exists := AbilityByItemID(loadout.AbilityInternal); exists {
			validation.AbilityInternalValid = true
			validation.AbilityInternalItemID = loadout.AbilityInternal
			validation.AbilityInternalName = ability.AbilityName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("AbilityInternal(%d)", loadout.AbilityInternal))
		}
	} else {
		validation.AbilityInternalValid = true
		validation.ValidSlots++
	}

	// Validate perk slots
	if loadout.PerkCom != 0 {
		if perk, exists := PerkByID(loadout.PerkCom); exists {
			validation.PerkComValid = true
			validation.PerkComItemID = loadout.PerkCom
			validation.PerkComName = perk.PerkName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("PerkCom(%d)", loadout.PerkCom))
		}
	} else {
		validation.PerkComValid = true
		validation.ValidSlots++
	}

	if loadout.PerkWeapon != 0 {
		if perk, exists := PerkByID(loadout.PerkWeapon); exists {
			validation.PerkWeaponValid = true
			validation.PerkWeaponItemID = loadout.PerkWeapon
			validation.PerkWeaponName = perk.PerkName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("PerkWeapon(%d)", loadout.PerkWeapon))
		}
	} else {
		validation.PerkWeaponValid = true
		validation.ValidSlots++
	}

	if loadout.PerkNavigation != 0 {
		if perk, exists := PerkByID(loadout.PerkNavigation); exists {
			validation.PerkNavigationValid = true
			validation.PerkNavigationItemID = loadout.PerkNavigation
			validation.PerkNavigationName = perk.PerkName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("PerkNavigation(%d)", loadout.PerkNavigation))
		}
	} else {
		validation.PerkNavigationValid = true
		validation.ValidSlots++
	}

	if loadout.PerkEngineer != 0 {
		if perk, exists := PerkByID(loadout.PerkEngineer); exists {
			validation.PerkEngineerValid = true
			validation.PerkEngineerItemID = loadout.PerkEngineer
			validation.PerkEngineerName = perk.PerkName
			validation.ValidSlots++
		} else {
			validation.InvalidSlots++
			validation.InvalidSlotList = append(validation.InvalidSlotList, fmt.Sprintf("PerkEngineer(%d)", loadout.PerkEngineer))
		}
	} else {
		validation.PerkEngineerValid = true
		validation.ValidSlots++
	}

	return validation
}

// ValidateAllLoadoutSlots validates all loadouts and returns a summary of validation results
// I2: Cross-reference loadout slot ItemIDs with weapon/ability/perk data
func ValidateAllLoadoutSlots() []DevLoadoutSlotValidation {
	loadouts := GetAllDevLoadouts()
	validations := make([]DevLoadoutSlotValidation, len(loadouts))

	for i, loadout := range loadouts {
		validations[i] = ValidateLoadoutSlots(loadout)
	}

	return validations
}

// GetLoadoutSlotValidationSummary returns a summary of validation results across all loadouts
// I2: Cross-reference loadout slot ItemIDs with weapon/ability/perk data
func GetLoadoutSlotValidationSummary() (totalLoadouts int, totalSlots int, validSlots int, invalidSlots int, invalidLoadouts []string) {
	validations := ValidateAllLoadoutSlots()

	totalLoadouts = len(validations)
	invalidLoadouts = []string{}

	for _, validation := range validations {
		totalSlots += validation.TotalSlots
		validSlots += validation.ValidSlots
		invalidSlots += validation.InvalidSlots
		
		if validation.InvalidSlots > 0 {
			invalidLoadouts = append(invalidLoadouts, fmt.Sprintf("%s (ShipID: %d, Invalid: %s)",
				validation.LoadoutName, validation.ShipID, strings.Join(validation.InvalidSlotList, ", ")))
		}
	}

	return totalLoadouts, totalSlots, validSlots, invalidSlots, invalidLoadouts
}
