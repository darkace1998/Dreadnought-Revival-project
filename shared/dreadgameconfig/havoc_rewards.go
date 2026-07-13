package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// HavocReward represents a Havoc reward from DN_HavocRewards_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
type HavocReward struct {
	Title       string `json:"m_title"`
	Description string `json:"m_description"`
	IconPath    string `json:"m_iconPath"`
	RowName     string `json:"-"` // The row name from the DataTable
}

// havocRewardsData holds the loaded Havoc reward data
var (
	havocRewards     []HavocReward
	havocRewardsMu   sync.RWMutex
	havocRewardsOnce sync.Once
	havocRewardsLoaded bool
)

// LoadHavocRewards loads Havoc reward data from DN_HavocRewards_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func LoadHavocRewards() error {
	var loadErr error
	havocRewardsOnce.Do(func() {
		filePath := DataTablePath(filepath.Join("Progression", "Havoc", "DN_HavocRewards_DT.json"))
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			loadErr = fmt.Errorf("failed to read DN_HavocRewards_DT.json: %w", err)
			return
		}

		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			loadErr = fmt.Errorf("failed to parse DN_HavocRewards_DT.json: %w", err)
			return
		}

		// Parse rows into HavocReward structs
		rewards := make([]HavocReward, 0, len(dt.Rows))
		for rowName, rowData := range dt.Rows {
			reward := HavocReward{
				Title:       rowData.GetString("m_title"),
				Description: rowData.GetString("m_description"),
				IconPath:    rowData.GetString("m_iconPath"),
				RowName:     rowName,
			}
			rewards = append(rewards, reward)
		}

		if len(rewards) == 0 {
			loadErr = fmt.Errorf("no Havoc rewards found in DN_HavocRewards_DT.json")
			return
		}

		havocRewards = rewards
		havocRewardsLoaded = true
		log.Printf("Loaded %d Havoc rewards from DN_HavocRewards_DT.json", len(rewards))
	})

	return loadErr
}

// AllHavocRewards returns all loaded Havoc rewards
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func AllHavocRewards() []HavocReward {
	havocRewardsMu.RLock()
	defer havocRewardsMu.RUnlock()
	
	if !havocRewardsLoaded {
		if err := LoadHavocRewards(); err != nil {
			log.Printf("Warning: Failed to load Havoc rewards: %v", err)
			return nil
		}
	}

	return havocRewards
}

// HavocRewardByRowName returns the Havoc reward with the specified row name
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocRewardByRowName(rowName string) (HavocReward, bool) {
	havocRewardsMu.RLock()
	defer havocRewardsMu.RUnlock()
	
	if !havocRewardsLoaded {
		if err := LoadHavocRewards(); err != nil {
			log.Printf("Warning: Failed to load Havoc rewards: %v", err)
			return HavocReward{}, false
		}
	}

	for _, reward := range havocRewards {
		if reward.RowName == rowName {
			return reward, true
		}
	}
	return HavocReward{}, false
}

// HavocRewardCount returns the total number of Havoc rewards
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocRewardCount() int {
	havocRewardsMu.RLock()
	defer havocRewardsMu.RUnlock()
	
	if !havocRewardsLoaded {
		if err := LoadHavocRewards(); err != nil {
			log.Printf("Warning: Failed to load Havoc rewards: %v", err)
			return 0
		}
	}

	return len(havocRewards)
}

// HavocRewardRowNames returns all row names of Havoc rewards
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func HavocRewardRowNames() []string {
	havocRewardsMu.RLock()
	defer havocRewardsMu.RUnlock()
	
	if !havocRewardsLoaded {
		if err := LoadHavocRewards(); err != nil {
			log.Printf("Warning: Failed to load Havoc rewards: %v", err)
			return nil
		}
	}

	names := make([]string, len(havocRewards))
	for i, reward := range havocRewards {
		names[i] = reward.RowName
	}
	return names
}