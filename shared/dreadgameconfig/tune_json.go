package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"sync"
)

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

func buildTuneJSONCache() {
	weaponsTuneJSON = buildWeaponsTuneJSON()
	projectilesTuneJSON = buildProjectilesTuneJSON()
	abilitiesTuneJSON = buildAbilitiesTuneJSON()
	officersTuneJSON = buildOfficersTuneJSON()
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

func buildWeaponsTuneJSON() string {
	weapons := AllWeapons()
	if len(weapons) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(weapons))
	for itemID, w := range weapons {
		row := map[string]any{
			"RowName":            fmt.Sprintf("Weapon_%d", itemID),
			"m_slotType":         w.SlotType,
			"m_class":            w.Class,
			"m_damageHigh":       w.DamageHigh,
			"m_damageMedium":     w.DamageMedium,
			"m_damageLow":        w.DamageLow,
			"m_maxRange":         w.MaxRange,
			"m_weaponCooldownTime": w.WeaponCooldownTime,
			"m_ammoMagazinSize":  w.AmmoMagazinSize,
			"m_spreadBaseValue":  w.SpreadBaseValue,
			"m_spreadMaxValue":   w.SpreadMaxValue,
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `[]`
	}
	return string(data)
}

func buildProjectilesTuneJSON() string {
	projectiles := AllProjectiles()
	if len(projectiles) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(projectiles))
	for rowName := range projectiles {
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
	for id, a := range abilities {
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

func buildOfficersTuneJSON() string {
	officers := AllOfficers()
	if len(officers) == 0 {
		return `[]`
	}
	rows := make([]map[string]any, 0, len(officers))
	for id, o := range officers {
		row := map[string]any{
			"RowName":         id,
			"m_enabling":      o.Enabling,
			"m_triggers":      o.Triggers,
			"m_effects":       o.Effects,
			"m_stackOnAdding": o.StackOnAdding,
			"m_isPerkFeat":    o.IsPerkFeat,
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
	for name, f := range feats {
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
