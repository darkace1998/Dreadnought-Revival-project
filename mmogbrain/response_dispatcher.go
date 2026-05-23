package main

import (
	"strings"

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
		return buildMmogSeasonProgressPayload()
	case "YA_PlayerGet":
		return buildMmogPlayerGetPayload(playerPID)
	case "YA_GetPlayerStatsCounterData":
		return buildMmogPlayerStatsCounterDataPayload()
	case "YA_GetPlayerProgression":
		return buildMmogPlayerProgressionPayload(playerPID)
	case "YA_GetTechTree":
		return buildMmogTechTreePayload()
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
	case "YA_GetDailyContractsData":
		return buildMmogDailyContractsDataPayload()
	case "YA_GetBoosterData":
		return buildMmogBoosterDataPayload()
	case "YA_GetPlayerScores":
		return buildMmogPlayerScoresPayload()
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
	case "YA_UpdateShipLoadout", "YA_RenameShipLoadout", "YA_AddShipDefaultLoadouts":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Navigation ---
	case "YA_RoomReturn", "YA_PlayAgain":
		return buildMmogRequestSuccessPayload(requestName)

	// --- Analytics ---
	case "YA_AnalyticsEvent", "YA_SaveCtAData", "YA_IncrementPlayerStatsCounter":
		return buildMmogRequestSuccessPayload(requestName)
	case "YA_AnalyticsBeginTransaction":
		transactionId := protocol.ExtractStringField(payload, "transactionId")
		if transactionId == "" {
			return buildMmogErrorPayload("Missing transactionId for YA_AnalyticsBeginTransaction")
		}
		return buildMmogAnalyticsBeginTransactionPayload(transactionId)

	// --- Profile ---
	case "YA_RefreshPlayerProfile":
		return buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", playerPID)
	case "YA_GetPlayersInformation":
		if len(payload) == 0 {
			return buildMmogErrorPayload("Empty payload for YA_GetPlayersInformation")
		}
		return buildMmogPlayersInformationPayload(playerPID, payload)

	// --- Connection ---
	case "YA_Connect":
		return buildMmogConnectPayload(playerPID)

	// --- Default ---
	default:
		logrus.WithField("request", requestName).Warn("unknown MMOG request")
		if strings.HasPrefix(requestName, "YA_Get") {
			return buildMmogErrorPayload("Unknown read command: " + requestName)
		}
		return buildMmogRequestSuccessPayload(requestName)
	}
}
