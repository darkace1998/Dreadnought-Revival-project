package main

import (
	"github.com/dreadnought-ps/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

func buildMmogRequestResponsePayload(requestName string, playerPID string, payload []byte) []byte {
	switch requestName {
	// --- Progression & Career ---
	case "YA_PlayerFleets":
		return buildMmogPlayerFleetsPayload(playerPID)
	case "YA_RequestStaticFleetData":
		return buildMmogStaticFleetDataPayloadForPlayer(playerPID)
	case "YA_GetSeasonData":
		return buildMmogSeasonDataPayload()
	case "YA_GetSeasonProgress":
		return buildMmogSeasonProgressPayloadForPlayer(playerPID)
	// issues #44/#56: these 10 request names have ZERO occurrences anywhere
	// in the real shipping client binary (verified byte-level, both ASCII
	// and UTF-16LE) — the real client never sends any of them. PvE/Havoc
	// balance data (waves, boss types, AI difficulty, modifiers, reward
	// tiers) ships as local cooked DataTable assets parsed client-side, not
	// fetched over the network; ship-bonus/AI-preference data likely has a
	// similar local-only or different-real-endpoint source. Kept (not
	// removed) since they're harmless unreachable code and the payload
	// logic may be useful reference if a real trigger is ever identified —
	// but do not spend further effort on these schemas, they have no effect
	// on real client behavior.
	case "YA_GetPvEProgress":
		return buildMmogPvEProgressPayload(playerPID)
	case "YA_GetBossKills":
		return buildMmogBossKillsPayload(playerPID)
	case "YA_GetAIPreferences":
		return buildMmogAIPreferencesPayload(playerPID)
	case "YA_SetAIPreferences":
		return buildMmogSetAIPreferencesPayload(playerPID, payload)
	case "YA_GetHavocWaves":
		return buildMmogHavocWavesPayload()
	case "YA_GetBossTypes":
		return buildMmogBossTypesPayload()
	case "YA_GetAIDifficultyLevels":
		return buildMmogAIDifficultyLevelsPayload()
	case "YA_GetHavocModifiers":
		return buildMmogHavocModifiersPayload()
	case "YA_GetPvERewardTiers":
		return buildMmogPvERewardTiersPayload()
	case "YA_PlayerGet":
		return buildMmogPlayerGetPayload(playerPID)
	case "YA_GetRibbons":
		return buildMmogRibbonsPayload(playerPID)
	case "YA_GetPlayerStatsCounterData":
		return buildMmogPlayerStatsCounterDataPayload()
	case "YA_GetPlayerProgression":
		return buildMmogPlayerProgressionPayload(playerPID)
	case "YA_GetTechTree":
		return buildMmogTechTreePayload(playerPID)
	case "YA_GetCareerProgression":
		return buildMmogCareerProgressionPayload()
	case "YA_GetStaticCareerData":
		return buildMmogStaticCareerDataPayload() // No playerPID needed
	case "YA_GetFeatureToggle":
		return buildMmogFeatureTogglePayload()
	case "YA_GetGameConfigData":
		return buildMmogGameConfigDataPayload() // No playerPID needed
	case "YA_GetProgressionData":
		return buildMmogProgressionDataPayload()
	case "YA_GetPlayerPurchases":
		return buildMmogPlayerPurchasesPayloadForPlayer(playerPID) // Fixed: Added playerPID
	case "YA_GetScoringData":
		return buildMmogScoringDataPayload()
	case "YA_GetProjectileData":
		return buildMmogProjectileDataPayload()
	case "YA_GetShipFeats":
		return BuildYAGetShipFeatsPayload()
	case "YA_GetAbilities":
		return BuildYAGetAbilitiesPayload()
	case "YA_GetDailyContractsData":
		return buildMmogDailyContractsDataPayloadForPlayer(playerPID)
	case "YA_GetBoosterData":
		return buildMmogBoosterDataPayload()
	case "YA_GetPlayerScores":
		return buildMmogPlayerScoresPayloadForPlayer(playerPID)
	case "YA_GetPlayerStatistics":
		return buildMmogPlayerStatisticsPayload()
	case "YA_FleetEligibility":
		return buildMmogFleetEligibilityPayload()
	case "YA_Tune":
		return buildMmogTunePayload()

	// --- Matchmaking & Rooms ---
	case "YA_EnterMatchmaking", "YA_SquadEnterMatchmaking":
		return buildMmogEnterMatchmakingPayload(requestName, playerPID, payload)
	case "YA_LeaveMatchmaking":
		return buildMmogLeaveMatchmakingPayload(requestName, playerPID)
	case "YA_QueryRooms":
		return buildMmogQueryRoomsPayload()
	case "YA_RoomStart", "YA_CustomRoomCreate", "YA_CustomRoomStartMatch",
		"YA_CustomRoomStartMatchCountdown", "YA_CustomRoomCancelMatchCountdown",
		"YA_CustomRoomUserJoin", "YA_CustomRoomUserLeave", "YA_CustomRoomUserReturn",
		"YA_CustomRoomUserRemove", "YA_CustomRoomUserSwitchTeam",
		"YA_CustomRoomChangeHost", "YA_CustomRoomChangeSettings", "YA_CustomRoomUpdate",
		"YA_CustomRoomInvite", "YA_CustomRoomAnalyticsInvite",
		"YA_CustomRoomEnterFleetSelect", "YA_CustomRoomExitFleetSelect",
		"YA_RequeuingRoomStart":
		return buildMmogRoomSuccessPayload(mmogRoomResponseName(requestName))

	// --- Squads ---
	case "YA_SquadInvite", "YA_SquadAccept", "YA_SquadLeave", "YA_SquadEliteStatusUpdate":
		return buildMmogSquadPayload(requestName, playerPID)

	// --- Chat ---
	case "YA_Chat", "YA_GlobalChat", "YA_LanguageChat", "YA_ChatStatus",
		"YA_ChatMergeRequest", "YA_ChatJoinRequest", "YA_ChatAwayRemovalRequest",
		"YA_ChatAwayChange":
		return buildMmogChatPayload(requestName, playerPID, payload)

	// --- Fleet/Loadout Modifications ---
	case "YA_AddToFleet", "YA_RemoveFromFleet", "YA_SetFleetFlagship",
		"YA_ChargeFleet", "YA_RepairFleet", "YA_FleetUpdate",
		"YA_FleetAutoRepair", "YA_UpdateFleetMaintenance":
		return buildMmogRequestSuccessPayload(requestName)
	// issue #56: same confirmed-dead status as the PvE/Havoc cases above —
	// "YA_GetShipBonuses" has zero occurrences anywhere in the client binary.
	case "YA_GetShipBonuses":
		return buildMmogShipBonusesPayload(requestName, playerPID, payload)
	case "YA_UpdateShipLoadout", "YA_RenameShipLoadout", "YA_AddShipDefaultLoadouts":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Market / Purchases ---
	case "YA_PurchaseItem", "YA_BuyItem", "YA_Purchase", "YA_Buy":
		return buildMmogPurchasePayload(requestName, playerPID, payload)
	case "YA_BuyEliteStatus", "YA_BuyDaypass", "YA_ActivateElite":
		return buildMmogElitePurchasePayload(requestName, playerPID, payload)
	case "YA_ConvertXPToCredits", "YA_ExchangeXP":
		return buildMmogXPConversionPayload(requestName, playerPID, payload)
	case "YA_CompleteContract", "YA_ClaimContract":
		return buildMmogContractCompletionPayload(requestName, playerPID, payload)
	case "YA_RerollContract", "YA_RefreshContract":
		return buildMmogContractRerollPayload(requestName, playerPID, payload)

	// --- Navigation ---
	case "YA_CheckReturn":
		return buildMmogCheckReturnPayload()
	case "YA_RoomReturn", "YA_PlayAgain":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Analytics ---
	case "YA_AnalyticsEvent", "YA_SaveCtAData", "YA_IncrementPlayerStatsCounter",
		"YA_AnalyticsEndTransaction", "YA_AnalyticsUpdateTransaction":
		return buildMmogRequestSuccessPayload(requestName)
	case "YA_AnalyticsBeginTransaction":
		transactionId := protocol.ExtractStringField(payload, "transactionId")
		if transactionId == "" {
			return buildMmogErrorPayload(requestName, "Missing transactionId for YA_AnalyticsBeginTransaction")
		}
		return buildMmogAnalyticsBeginTransactionPayload(transactionId)

	// --- Profile ---
	case "YA_RefreshPlayerProfile":
		return buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", playerPID)
	case "YA_GetPlayersInformation":
		if len(payload) == 0 {
			return buildMmogErrorPayload(requestName, "Empty payload for YA_GetPlayersInformation")
		}
		return buildMmogPlayersInformationPayload(playerPID, payload)

	// --- Connection ---
	case "YA_Connect":
		return buildMmogConnectPayload(playerPID)
	case "YA_PlayerStateInHangar", "YA_UserLogout", "YA_ReconnectJoinChannels":
		return buildMmogRequestSuccessPayload(requestName)
	case "YA_UnlockItem", "YA_ClaimItem", "YA_AddItems", "YA_RemoveItems",
		"YA_ContractReplace", "YA_ContractRemove":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Server→Client Notifications ---
	case "YA_UserOnline":
		return buildMmogUserOnlinePayload()
	case "YA_UserStatus":
		return buildMmogRequestSuccessPayload(requestName)
	case "YA_OnFleetCharged", "YA_AchievementsUpdated":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Default ---
	default:
		// Previously any unrecognized request whose name didn't start with
		// "YA_Get" was answered with an unconditional fake success — the
		// client would believe a typo'd/unimplemented mutation (equip,
		// purchase, claim, etc.) had taken effect when the server did
		// nothing at all. Every request type this server intentionally
		// treats as a safe no-op is already handled by an explicit case
		// above; anything reaching here is genuinely unrecognized, so it
		// should surface as an error like unknown reads already do.
		logrus.WithField("request", requestName).Warn("unknown MMOG request")
		return buildMmogErrorPayload(requestName, "Unknown command: "+requestName)
	}
}
