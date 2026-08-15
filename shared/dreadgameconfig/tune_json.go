package dreadgameconfig

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
)

// sortedMapKeys returns a map's keys in a stable order.
//
// Every builder below used to walk its map directly, so the emitted JSON came
// out in a different order on every process start. That is not cosmetic: the
// tables ship compressed in YA_TuneReturn, so the payload SIZE moved run to run
// (measured 17851 / 17894 / 17988 for identical data), which no size tripwire
// can pin and which makes a real regression indistinguishable from noise.
func sortedMapKeys[K cmp.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

var (
	tuneJSONOnce      sync.Once
	weaponsTuneJSON    string
	projectilesTuneJSON string
	abilitiesTuneJSON  string
	officersTuneJSON   string
	featsTuneJSON      string
	gameModifiersTuneJSON string
	killScoringTuneJSON string
	waveScoringTuneJSON string
	defendScoringTuneJSON string
	remainingPlayerScoringTuneJSON string
	medalScoringTuneJSON string
)


// otsTableRowsJSON echoes a cooked OTS data table back to the client verbatim:
// every row, every field, under the row's OWN name.
//
// This replaced a builder that reshaped the table into something the client
// could not use. Measured against a live client 2026-08-15, on every weapon of
// every pawn:
//
//	LogYTuneManager:Error: LoadWeaponRow() Weapon Data for
//	  'WP_CreepPrimary01_weapon01_BP' Couldn't be found.
//
// The client looks rows up by BLUEPRINT ASSET NAME, which is exactly the key
// DN_Weapons_OTS_DT.json already uses. The old builder was wrong three ways at
// once, and each one alone would have caused that error:
//
//  1. RowName was synthesised as "Weapon_<itemID>", matching nothing.
//  2. It walked AllWeapons(), which is keyed by player item id, so of the
//     table's 226 rows only 159 survived -- creep weapons and turret abilities
//     have no item id and vanished entirely.
//  3. It copied 11 of the row's 47 fields.
//
// Echoing the file needs none of that to be got right, and cannot drift from
// the client's expectations, because it IS the client's table.
func otsTableRowsJSON(fileName string) string {
	data, err := os.ReadFile(DataTablePath(fileName))
	if err != nil {
		return `[]`
	}
	var table struct {
		Rows map[string]map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(data, &table); err != nil {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(table.Rows))
	for _, name := range sortedMapKeys(table.Rows) {
		row := make(map[string]any, len(table.Rows[name])+1)
		for field, value := range table.Rows[name] {
			row[field] = value
		}
		row["RowName"] = name
		rows = append(rows, row)
	}
	out, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(out)
}

func buildTuneJSONCache() {
	weaponsTuneJSON = otsTableRowsJSON("DN_Weapons_OTS_DT.json")
	projectilesTuneJSON = buildProjectilesTuneJSON()
	abilitiesTuneJSON = buildAbilitiesTuneJSON()
	officersTuneJSON = otsTableRowsJSON("DN_Officers_OTS_DT.json")
	featsTuneJSON = buildFeatsTuneJSON()
	gameModifiersTuneJSON = buildGameModifiersTuneJSON()
	killScoringTuneJSON = buildKillScoringTuneJSON()
	waveScoringTuneJSON = buildWaveScoringTuneJSON()
	defendScoringTuneJSON = buildDefendScoringTuneJSON()
	remainingPlayerScoringTuneJSON = buildRemainingPlayerScoringTuneJSON()
	medalScoringTuneJSON = buildMedalScoringTuneJSON()
}

func ensureTuneJSONCache() {
	tuneJSONOnce.Do(buildTuneJSONCache)
}

func WeaponsTuneJSON() string {
	ensureTuneJSONCache()
	return weaponsTuneJSON
}

func ProjectilesTuneJSON() string {
	ensureTuneJSONCache()
	return projectilesTuneJSON
}

func AbilitiesTuneJSON() string {
	ensureTuneJSONCache()
	return abilitiesTuneJSON
}

func OfficersTuneJSON() string {
	ensureTuneJSONCache()
	return officersTuneJSON
}

func FeatsTuneJSON() string {
	ensureTuneJSONCache()
	return featsTuneJSON
}

func GameModifiersTuneJSON() string {
	ensureTuneJSONCache()
	return gameModifiersTuneJSON
}

func KillScoringTuneJSON() string {
	ensureTuneJSONCache()
	return killScoringTuneJSON
}

func WaveScoringTuneJSON() string {
	ensureTuneJSONCache()
	return waveScoringTuneJSON
}

func DefendScoringTuneJSON() string {
	ensureTuneJSONCache()
	return defendScoringTuneJSON
}

func RemainingPlayerScoringTuneJSON() string {
	ensureTuneJSONCache()
	return remainingPlayerScoringTuneJSON
}

func MedalScoringTuneJSON() string {
	ensureTuneJSONCache()
	return medalScoringTuneJSON
}


func buildProjectilesTuneJSON() string {
	projectiles := AllProjectiles()
	if len(projectiles) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(projectiles))
	for _, rowName := range sortedMapKeys(projectiles) {
		row := map[string]any{
			"RowName": rowName,
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(data)
}

func buildAbilitiesTuneJSON() string {
	abilities := AllAbilities()
	if len(abilities) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(abilities))
	for _, id := range sortedMapKeys(abilities) {
		a := abilities[id]
		row := map[string]any{
			"RowName":      id,
			"m_abilityName": a.AbilityName,
			"m_coolDown":   a.CoolDown,
			"m_activeTime": a.ActiveTime,
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(data)
}


func buildFeatsTuneJSON() string {
	feats := AllShipFeats()
	if len(feats) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(feats))
	for _, name := range sortedMapKeys(feats) {
		f := feats[name]
		row := map[string]any{
			"RowName":    name,
			"m_enabling": f.Enabling,
			"m_triggers": f.Triggers,
			"m_effects":  f.Effects,
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(data)
}

func buildGameModifiersTuneJSON() string {
	modifiers := AllGameModifiers()
	if len(modifiers) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(modifiers))
	for i, m := range modifiers {
		row := map[string]any{
			"RowName":        fmt.Sprintf("GameModifier_%d", i),
			"m_gameModeName": m.GameModeName,
			"m_excludes":     m.Excludes,
			"m_feats":        m.Feats,
			"m_affectedTeam": m.AffectedTeam,
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(data)
}

func buildKillScoringTuneJSON() string {
	scorings := AllPvEKillScorings()
	if len(scorings) == 0 {
		return "[]"
	}
	rows := make([]map[string]any, 0, len(scorings))
	for _, s := range scorings {
		rows = append(rows, map[string]any{
			"RowName":            s.RowName,
			"m_starterKillScore": s.StarterKillScore,
			"m_deductionTime":    s.DeductionTime,
			"m_scoreToDeduct":    s.ScoreToDeduct,
		})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func buildWaveScoringTuneJSON() string {
	scorings := AllPvEWaveScorings()
	if len(scorings) == 0 {
		return "[]"
	}
	rows := make([]map[string]any, 0, len(scorings))
	for _, s := range scorings {
		rows = append(rows, map[string]any{
			"RowName":            s.RowName,
			"m_starterWaveScore": s.StarterWaveScore,
			"m_deductionTime":    s.DeductionTime,
			"m_scoreToDeduct":    s.ScoreToDeduct,
		})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func buildDefendScoringTuneJSON() string {
	scorings := AllPvEDefendScorings()
	if len(scorings) == 0 {
		return "[]"
	}
	rows := make([]map[string]any, 0, len(scorings))
	for _, s := range scorings {
		rows = append(rows, map[string]any{
			"RowName":              s.RowName,
			"m_starterDefendScore": s.StarterDefendScore,
			"m_deductionTime":      s.DeductionTime,
			"m_scoreToDeduct":      s.ScoreToDeduct,
		})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func buildRemainingPlayerScoringTuneJSON() string {
	scorings := AllPvERemainingPlayerScorings()
	if len(scorings) == 0 {
		return "[]"
	}
	rows := make([]map[string]any, 0, len(scorings))
	for _, s := range scorings {
		rows = append(rows, map[string]any{
			"RowName":                       s.RowName,
			"m_starterRemainingPlayerScore": s.StarterRemainingPlayerScore,
			"m_deductionTime":               s.DeductionTime,
			"m_scoreToDeduct":               s.ScoreToDeduct,
		})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func buildMedalScoringTuneJSON() string {
	scorings := AllPvEMedalScorings()
	if len(scorings) == 0 {
		return "[]"
	}
	rows := make([]map[string]any, 0, len(scorings))
	for _, s := range scorings {
		rows = append(rows, map[string]any{
			"RowName":      s.RowName,
			"m_medalScore": s.MedalScore,
		})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}
