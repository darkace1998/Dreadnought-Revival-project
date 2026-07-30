package main

import "testing"

// TestAllSimplePayloadsHaveNoFNameCollisions extends
// TestPlayerPayloadsHaveNoFNameCollisions (which only covers 4 payload types)
// to every payload-building function that takes no arguments or just a
// playerPID, using the same assertMmogPayloadHasNoSiblingFNameCollisions
// walker. Many fields were added to these payloads across phases 2-11
// (weapon stats, abilities, officers, perks, loadouts, seasons, PvE, market)
// after the original FName-collision fix (commit 8f72937), and none of that
// work re-ran the collision check against the newly touched payloads.
func TestAllSimplePayloadsHaveNoFNameCollisions(t *testing.T) {
	const pid = defaultMmogPlayerPID

	noArg := map[string]func() []byte{
		"YA_QueryRooms":            buildMmogQueryRoomsPayload,
		"YA_StaticFleetData":       buildMmogStaticFleetDataPayload,
		"YA_GetSeasonData":         buildMmogSeasonDataPayload,
		"YA_GetSeasonProgress":     buildMmogSeasonProgressPayload,
		"YA_PlayerStatsCounter":    func() []byte { return buildMmogPlayerStatsCounterDataPayload() },
		"YA_GetProgressionData":    buildMmogProgressionDataPayload,
		"YA_GetTechTree":           func() []byte { return buildMmogTechTreePayload() },
		"YA_GetCareerProgression":  func() []byte { return buildMmogCareerProgressionPayload(defaultMmogPlayerPID) },
		"YA_GetGameConfigData":     buildMmogGameConfigDataPayload,
		"YA_GetFeatureToggle":      buildMmogFeatureTogglePayload,
		"YA_GetPlayerPurchases":    buildMmogPlayerPurchasesPayload,
		"YA_GetStaticCareerData":   buildMmogStaticCareerDataPayload,
		"YA_GetScoringData":        buildMmogScoringDataPayload,
		"YA_GetDailyContractsData": buildMmogDailyContractsDataPayload,
		"YA_GetProjectileData":     buildMmogProjectileDataPayload,
		"YA_GetBoosterData":        buildMmogBoosterDataPayload,
		"YA_GetHavocWaves":         buildMmogHavocWavesPayload,
		"YA_GetBossTypes":          buildMmogBossTypesPayload,
		"YA_GetAIDifficultyLevels": buildMmogAIDifficultyLevelsPayload,
		"YA_GetHavocModifiers":     buildMmogHavocModifiersPayload,
		"YA_GetPvERewardTiers":     buildMmogPvERewardTiersPayload,
		"YA_GetPlayerScores":       buildMmogPlayerScoresPayload,
		"YA_FleetEligibility":      buildMmogFleetEligibilityPayload,
		"YA_Tune":                  buildMmogTunePayload,
		"YA_GetPlayerStatistics":   buildMmogPlayerStatisticsPayload,
		"YA_UserOnline":            buildMmogUserOnlinePayload,
		"YA_CheckReturn":           buildMmogCheckReturnPayload,
		"YA_UserLogin":             func() []byte { return buildMmogLoginSuccessPayload(pid) },
	}
	for name, fn := range noArg {
		t.Run(name, func(t *testing.T) {
			assertMmogPayloadHasNoSiblingFNameCollisions(t, name, fn())
		})
	}

	withPID := map[string]func(string) []byte{
		"YA_PlayerFleets":              buildMmogPlayerFleetsPayload,
		"YA_RequestStaticFleetDataFor": buildMmogStaticFleetDataPayloadForPlayer,
		"YA_GetSeasonProgressFor":      buildMmogSeasonProgressPayloadForPlayer,
		"YA_PlayerGet":                 buildMmogPlayerGetPayload,
		"YA_GetPlayerProgression":      buildMmogPlayerProgressionPayload,
		"YA_GetPlayerPurchasesFor":     buildMmogPlayerPurchasesPayloadForPlayer,
		"YA_GetDailyContractsDataFor":  buildMmogDailyContractsDataPayloadForPlayer,
		"YA_GetPvEProgress":            buildMmogPvEProgressPayload,
		"YA_GetBossKills":              buildMmogBossKillsPayload,
		"YA_GetAIPreferences":          buildMmogAIPreferencesPayload,
		"YA_Connect":                   buildMmogConnectPayload,
		"YA_GetRibbons":                buildMmogRibbonsPayload,
	}
	for name, fn := range withPID {
		t.Run(name, func(t *testing.T) {
			assertMmogPayloadHasNoSiblingFNameCollisions(t, name, fn(pid))
		})
	}

	assertMmogPayloadHasNoSiblingFNameCollisions(t, "YA_RefreshPlayerProfile", buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", pid))
}
