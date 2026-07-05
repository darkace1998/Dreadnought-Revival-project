package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// GlobalTuningValue represents a global tuning value from DN_GlobalTuningValues_DT.json
type GlobalTuningValue struct {
	// Core tuning fields from DataTable
	RangeToViewTargetMarkerForClassReveal    float64 `json:"m_rangeToViewTargetMarkerForClassReveal"`
	ProjectileCloseInProjectileSpeedModifier float64 `json:"m_projectileCloseInProjectileSpeedModifier"`
	AFKTimer                                      float64 `json:"m_afkTimer"`

	// Metadata
	TuningName string `json:"-"` // Row name from DataTable
}

// globalTuningData holds the loaded global tuning data
var (
	globalTuningValues     = make(map[string]GlobalTuningValue)
	globalTuningValuesLock sync.RWMutex
	globalTuningLoaded     bool
)

// LoadGlobalTuningValues loads global tuning values from DN_GlobalTuningValues_DT.json
// G1: Define Go structs for global tuning DataTable fields
func LoadGlobalTuningValues() error {
	globalTuningValuesLock.Lock()
	defer globalTuningValuesLock.Unlock()

	if globalTuningLoaded {
		return nil
	}

	// Path to the global tuning values DataTable
	tuningPath := filepath.Join("..", "..", "data", "datatables", "DN_GlobalTuningValues_DT.json")
	
	data, err := os.ReadFile(tuningPath)
	if err != nil {
		log.Printf("Warning: Failed to load global tuning values: %v", err)
		return err
	}

	var dataTable struct {
		Rows map[string]struct {
			RangeToViewTargetMarkerForClassReveal    float64 `json:"m_rangeToViewTargetMarkerForClassReveal"`
			ProjectileCloseInProjectileSpeedModifier float64 `json:"m_projectileCloseInProjectileSpeedModifier"`
			AFKTimer                                      float64 `json:"m_afkTimer"`
		} `json:"rows"`
		RowCount int `json:"row_count"`
	}

	if err := json.Unmarshal(data, &dataTable); err != nil {
		log.Printf("Warning: Failed to parse global tuning values: %v", err)
		return err
	}

	loadedCount := 0
	for rowName, rowData := range dataTable.Rows {
		tuning := GlobalTuningValue{
			RangeToViewTargetMarkerForClassReveal:    rowData.RangeToViewTargetMarkerForClassReveal,
			ProjectileCloseInProjectileSpeedModifier: rowData.ProjectileCloseInProjectileSpeedModifier,
			AFKTimer:                                      rowData.AFKTimer,
			TuningName:                                   rowName,
		}

		globalTuningValues[rowName] = tuning
		loadedCount++
	}

	globalTuningLoaded = true
	log.Printf("Loaded %d global tuning values from %s", loadedCount, filepath.Base(tuningPath))
	return nil
}

// GlobalTuningByName returns a global tuning value by its row name
func GlobalTuningByName(name string) (GlobalTuningValue, bool) {
	globalTuningValuesLock.RLock()
	defer globalTuningValuesLock.RUnlock()

	tuning, exists := globalTuningValues[name]
	return tuning, exists
}

// AllGlobalTuningValues returns all loaded global tuning values
func AllGlobalTuningValues() []GlobalTuningValue {
	globalTuningValuesLock.RLock()
	defer globalTuningValuesLock.RUnlock()

	tunings := make([]GlobalTuningValue, 0, len(globalTuningValues))
	for _, tuning := range globalTuningValues {
		tunings = append(tunings, tuning)
	}
	return tunings
}

// GlobalTuningCount returns the number of loaded global tuning values
func GlobalTuningCount() int {
	globalTuningValuesLock.RLock()
	defer globalTuningValuesLock.RUnlock()
	return len(globalTuningValues)
}

// AllGlobalTuningNames returns all global tuning value names
func AllGlobalTuningNames() []string {
	globalTuningValuesLock.RLock()
	defer globalTuningValuesLock.RUnlock()

	names := make([]string, 0, len(globalTuningValues))
	for name := range globalTuningValues {
		names = append(names, name)
	}
	return names
}

// GetRangeToViewTargetMarkerForClassReveal returns the range to view target marker for class reveal
func GetRangeToViewTargetMarkerForClassReveal() float64 {
	if tuning, exists := GlobalTuningByName("Default"); exists {
		return tuning.RangeToViewTargetMarkerForClassReveal
	}
	return 20000.0 // Default value from the DataTable
}

// GetProjectileCloseInProjectileSpeedModifier returns the projectile close-in speed modifier
func GetProjectileCloseInProjectileSpeedModifier() float64 {
	if tuning, exists := GlobalTuningByName("Default"); exists {
		return tuning.ProjectileCloseInProjectileSpeedModifier
	}
	return 0.5 // Default value from the DataTable
}

// GetAFKTimer returns the AFK timer value
func GetAFKTimer() float64 {
	if tuning, exists := GlobalTuningByName("Default"); exists {
		return tuning.AFKTimer
	}
	return 119.5 // Default value from the DataTable
}