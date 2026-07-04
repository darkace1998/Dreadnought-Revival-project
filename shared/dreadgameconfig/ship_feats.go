package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ShipFeat represents a ship feat/ability effect from the DataTable
type ShipFeat struct {
	Enabling       string `json:"m_enabling"`
	Triggers       string `json:"m_triggers"`
	Effects        string `json:"m_effects"`
	StackOnAdding  bool   `json:"m_stackOnAdding"`
	IsPerkFeat     bool   `json:"m_isPerkFeat"`
	// Parsed DSL components
	ParsedEffects []FeatEffect `json:"-"`
}

// FeatEffect represents a parsed effect from the DSL
type FeatEffect struct {
	EffectType    string   `json:"effect_type"`
	ModifierType string   `json:"modifier_type"`
	Value        float64  `json:"value"`
	Duration     float64  `json:"duration"`
	Stacks       int      `json:"stacks"`
	BuffType     string   `json:"buff_type"`
	Conditions   []string `json:"conditions"`
}

// FeatDSLParser handles parsing of ship feat DSL strings
// Currently unused but kept for future expansion
type FeatDSLParser struct{}

// shipFeatsData holds the loaded ship feat data
var (
	shipFeats     = make(map[string]ShipFeat)
	shipFeatsLock sync.RWMutex
	shipFeatsLoaded bool
)

// LoadShipFeats loads all ship feat data from DataTables
func LoadShipFeats() error {
	shipFeatsLock.Lock()
	defer shipFeatsLock.Unlock()
	
	if shipFeatsLoaded {
		return nil
	}
	
	// Find all ship feat files
	featDir := DataTablePath(filepath.Join("ShipFeats"))
	entries, err := os.ReadDir(featDir)
	if err != nil {
		return fmt.Errorf("read ShipFeats directory: %w", err)
	}
	
	loadedCount := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		
		filePath := filepath.Join(featDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read ship feat file %s: %w", entry.Name(), err)
		}
		
		var dt DataTable
		if err := json.Unmarshal(data, &dt); err != nil {
			return fmt.Errorf("parse ship feat file %s: %w", entry.Name(), err)
		}
		
		for rowName, row := range dt.Rows {
			feat, err := parseShipFeatRow(row)
			if err != nil {
				return fmt.Errorf("parse ship feat row %s in %s: %w", rowName, entry.Name(), err)
			}
			fullName := fmt.Sprintf("%s_%s", filepath.Base(entry.Name()[:len(entry.Name())-5]), rowName)
			// Remove _OTS_DT or _DT suffix from filename
			fullName = strings.ReplaceAll(fullName, "_OTS_DT_", "_")
			fullName = strings.ReplaceAll(fullName, "_DT_", "_")
			shipFeats[fullName] = feat
			loadedCount++
		}
	}
	
	shipFeatsLoaded = true
	log.Printf("Loaded %d ship feats", loadedCount)
	return nil
}

// parseShipFeatRow parses a single ship feat row from DataTable
func parseShipFeatRow(row Row) (ShipFeat, error) {
	var feat ShipFeat
	
	// Marshal and unmarshal to handle the dynamic structure
	rowData, err := json.Marshal(row)
	if err != nil {
		return feat, fmt.Errorf("marshal row: %w", err)
	}
	
	if err := json.Unmarshal(rowData, &feat); err != nil {
		return feat, fmt.Errorf("unmarshal row: %w", err)
	}
	
	// Parse the effects DSL
	feat.ParsedEffects = ParseFeatEffects(feat.Effects)
	
	return feat, nil
}

// ShipFeatByName returns a ship feat by its full name
func ShipFeatByName(name string) (ShipFeat, bool) {
	shipFeatsLock.RLock()
	defer shipFeatsLock.RUnlock()
	
	feat, ok := shipFeats[name]
	return feat, ok
}

// AllShipFeats returns all loaded ship feats
func AllShipFeats() map[string]ShipFeat {
	shipFeatsLock.RLock()
	defer shipFeatsLock.RUnlock()
	
	// Return a copy to avoid race conditions
	featsCopy := make(map[string]ShipFeat, len(shipFeats))
	for k, v := range shipFeats {
		featsCopy[k] = v
	}
	return featsCopy
}

// ParseFeatEffects parses the effects DSL string into structured FeatEffect objects
func ParseFeatEffects(effects string) []FeatEffect {
	if effects == "" {
		return nil
	}

	var parsedEffects []FeatEffect

	// Split effects by semicolon to get individual effect statements
	effectStatements := strings.Split(effects, ";")

	for _, statement := range effectStatements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}

		effect := parseFeatEffectStatement(statement)
		if effect != nil {
			parsedEffects = append(parsedEffects, *effect)
		}
	}

	return parsedEffects
}

// parseFeatEffectStatement parses a single effect statement like "AM(PawnDamageModifier +75%) :Stacks(1): D(10.0) : Buff(FirepowerIncrease)"
func parseFeatEffectStatement(statement string) *FeatEffect {
	effect := &FeatEffect{
		Conditions: make([]string, 0),
	}

	// Extract conditions (CC(...) patterns)
	ccPattern := regexp.MustCompile(`CC\(([^)]+)\)`)
	ccMatches := ccPattern.FindAllStringSubmatch(statement, -1)
	for _, match := range ccMatches {
		if len(match) > 1 {
			effect.Conditions = append(effect.Conditions, match[1])
		}
	}

	// Remove conditions from the statement for further parsing
	statement = ccPattern.ReplaceAllString(statement, "")

	// Extract stacks
	stacksPattern := regexp.MustCompile(`:\s*Stacks\((\d+)\)`)
	stacksMatch := stacksPattern.FindStringSubmatch(statement)
	if len(stacksMatch) > 1 {
		if val, err := strconv.Atoi(stacksMatch[1]); err == nil {
			effect.Stacks = val
		}
		statement = stacksPattern.ReplaceAllString(statement, "")
	}

	// Extract duration
	durationPattern := regexp.MustCompile(`:\s*D\(([\d.]+)\)`)
	durationMatch := durationPattern.FindStringSubmatch(statement)
	if len(durationMatch) > 1 {
		if val, err := strconv.ParseFloat(durationMatch[1], 64); err == nil {
			effect.Duration = val
		}
		statement = durationPattern.ReplaceAllString(statement, "")
	}

	// Extract buff type
	buffPattern := regexp.MustCompile(`:\s*Buff\(([^)]+)\)`)
	buffMatch := buffPattern.FindStringSubmatch(statement)
	if len(buffMatch) > 1 {
		effect.BuffType = buffMatch[1]
		statement = buffPattern.ReplaceAllString(statement, "")
	}

	// Parse the main effect (AM, RM, DFS, PCFS patterns)
	statement = strings.TrimSpace(statement)
	if strings.HasPrefix(statement, "AM(") {
		effect.EffectType = "AM" // Additive Modifier
		parseAMEffect(statement, effect)
	} else if strings.HasPrefix(statement, "RM(") {
		effect.EffectType = "RM" // Remove Modifier
		parseRMEffect(statement, effect)
	} else if strings.HasPrefix(statement, "DFS(") {
		effect.EffectType = "DFS" // Disable Feat Stack
		parseDFSEffect(statement, effect)
	} else if strings.HasPrefix(statement, "PCFS(") {
		effect.EffectType = "PCFS" // Per-Condition Feat Stack
		parsePCFSEffect(statement, effect)
	}

	return effect
}

// parseAMEffect parses Additive Modifier effects like "AM(PawnDamageModifier +75%)"
func parseAMEffect(statement string, effect *FeatEffect) {
	// Extract content inside AM()
	content := extractParenthesesContent(statement, "AM")
	if content == "" {
		return
	}

	// Find the sign and split accordingly
	signIndex := strings.IndexAny(content, "+-")
	if signIndex == -1 {
		return
	}

	// Extract modifier type (everything before the sign)
	effect.ModifierType = strings.TrimSpace(content[:signIndex])
	
	// Extract value (sign and everything after)
	valueStr := strings.TrimSpace(content[signIndex:])
	if val, err := parsePercentageValue(valueStr); err == nil {
		effect.Value = val
	}
}

// parseRMEffect parses Remove Modifier effects like "RM(Energy InitialEnergyCosts -0)"
func parseRMEffect(statement string, effect *FeatEffect) {
	content := extractParenthesesContent(statement, "RM")
	if content == "" {
		return
	}

	// Split into modifier type and value
	parts := strings.Fields(content)
	if len(parts) >= 2 {
		effect.ModifierType = parts[0]
		valueStr := strings.Join(parts[1:], " ")
		if val, err := strconv.ParseFloat(valueStr, 64); err == nil {
			effect.Value = val
		}
	}
}

// parseDFSEffect parses Disable Feat Stack effects like "DFS(EnergyOnShield)"
func parseDFSEffect(statement string, effect *FeatEffect) {
	content := extractParenthesesContent(statement, "DFS")
	if content != "" {
		effect.BuffType = "Disable:" + content
	}
}

// parsePCFSEffect parses Per-Condition Feat Stack effects like "PCFS(#Energy_To_Engine)"
func parsePCFSEffect(statement string, effect *FeatEffect) {
	content := extractParenthesesContent(statement, "PCFS")
	if content != "" {
		effect.BuffType = "Condition:" + content
	}
}

// extractParenthesesContent extracts content inside the first set of parentheses after prefix
func extractParenthesesContent(s, prefix string) string {
	startIdx := strings.Index(s, prefix+"(")
	if startIdx == -1 {
		return ""
	}

	startIdx += len(prefix) + 1
	depth := 1
	for i := startIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[startIdx:i]
			}
		}
	}
	return ""
}

// parsePercentageValue parses a percentage value string like "+75%" or "-30%"
func parsePercentageValue(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		s = strings.TrimSuffix(s, "%")
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		return val / 100.0, nil
	}
	return strconv.ParseFloat(s, 64)
}