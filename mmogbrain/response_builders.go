package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dreadnought-ps/mmogbrain/handlers"
	"github.com/dreadnought-ps/mmogbrain/matchmaker"
	"github.com/dreadnought-ps/mmogbrain/protocol"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/google/uuid"
)

// --- Frame builders ---

func buildMmogLoginSuccessFrame(requestID [16]byte, requestType uint16, playerPID ...string) []byte {
	payload := buildMmogLoginSuccessPayload(playerPID...)
	return protocol.BuildResponseFrame(requestID, requestType, payload)
}

func buildMmogRequestResponseFrame(requestID [16]byte, requestType uint16, requestName string, playerPID string, reqPayload []byte) []byte {
	payload := buildMmogRequestResponsePayload(requestName, playerPID, reqPayload)
	return protocol.BuildResponseFrame(requestID, requestType, payload)
}

// --- Login payloads ---

func buildMmogLoginSuccessPayload(playerPID ...string) []byte {
	var b []byte
	var stack []int
	pid := defaultMmogPlayerPID
	if len(playerPID) > 0 {
		pid = playerPID[0]
	}
	state := mmogPlayerStateForPID(pid)

	b = protocol.AppendStringField(b, "RT", "YA_UserLogin")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "credits", state.softCurrency)
	b = protocol.AppendInt32Field(b, "premiumCurrency", state.premiumCurrency)
	b = protocol.AppendInt32Field(b, "freexp", state.freeXP)
	b = protocol.AppendInt32Field(b, "xp", state.currentXP)
	b, stack = protocol.AppendObjectStart(b, stack, "LoginStreak")
	b = protocol.AppendInt32Field(b, "loginstreak", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogRequestSuccessPayload(requestName string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// --- Matchmaking ---

type mmogMatchmakingStatus struct {
	entryID    string
	state      string
	gameMode   string
	mapName    string
	matchID    string
	serverIP   string
	serverPort int32
}

func buildMmogEnterMatchmakingPayload(requestName string, playerPID string, payload []byte) []byte {
	pid := normalizedPlayerStatePID(playerPID)
	status := currentMmogMatchmakingStatus(pid)
	if status.state == "matched" {
		return buildMmogMatchmakingPayload(requestName, status)
	}

	gameMode := protocol.FirstNonEmptyString(payload, "GameMode", "gameMode", "Mode", "mode", "matchmaking")
	if gameMode == "" || gameMode == "*matchmaking" {
		gameMode = "TDM"
	}
	if !matchmaker.ValidGameMode(gameMode) {
		return buildMmogMatchmakingErrorPayload(requestName, 2, "unsupported game mode")
	}
	gameMode = matchmaker.NormalizeGameMode(gameMode)
	tierMin := protocol.FirstInt32Field(payload, 1, "TierMin", "tierMin", "minTier", "MinTier")
	tierMax := protocol.FirstInt32Field(payload, 5, "TierMax", "tierMax", "maxTier", "MaxTier")

	entryID := uuid.New().String()
	database := currentMmogPlayerStateDB()
	if database != nil {
		_, _ = database.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, pid)
		if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,status) VALUES(?,?,?,?,?,'waiting')`,
			entryID, pid, gameMode, tierMin, tierMax); err != nil {
			return buildMmogMatchmakingErrorPayload(requestName, 2, "queue insert failed")
		}
	}

	return buildMmogMatchmakingPayload(requestName, mmogMatchmakingStatus{
		entryID:  entryID,
		state:    "waiting",
		gameMode: gameMode,
	})
}

func buildMmogLeaveMatchmakingPayload(requestName string, playerPID string) []byte {
	pid := normalizedPlayerStatePID(playerPID)
	if database := currentMmogPlayerStateDB(); database != nil {
		if _, err := database.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, pid); err != nil {
			return buildMmogMatchmakingErrorPayload(requestName, 2, "queue leave failed")
		}
	}
	return buildMmogMatchmakingPayload(requestName, mmogMatchmakingStatus{state: "left"})
}

func currentMmogMatchmakingStatus(playerPID string) mmogMatchmakingStatus {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return mmogMatchmakingStatus{state: "idle"}
	}

	var matched mmogMatchmakingStatus
	err := database.QueryRow(`
		SELECT m.id,m.server_ip,m.server_port,m.game_mode,m.map
		FROM match_slots ms
		JOIN matches m ON ms.match_id=m.id
		WHERE ms.user_id=? AND m.status='active'
		ORDER BY ms.joined_at DESC
		LIMIT 1
	`, playerPID).Scan(&matched.matchID, &matched.serverIP, &matched.serverPort, &matched.gameMode, &matched.mapName)
	if err == nil {
		matched.state = "matched"
		return matched
	}
	if err != sql.ErrNoRows {
		return mmogMatchmakingStatus{state: "idle"}
	}

	var queued mmogMatchmakingStatus
	err = database.QueryRow(`
		SELECT id,game_mode FROM queue_entries
		WHERE user_id=? AND status='waiting'
		ORDER BY queued_at DESC
		LIMIT 1
	`, playerPID).Scan(&queued.entryID, &queued.gameMode)
	if err == nil {
		queued.state = "waiting"
		return queued
	}
	return mmogMatchmakingStatus{state: "idle"}
}

func buildMmogMatchmakingPayload(requestName string, status mmogMatchmakingStatus) []byte {
	var b []byte
	var stack []int
	if status.state == "" {
		status.state = "ok"
	}
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendStringField(b, "matchmakingStatus", status.state)
	b = protocol.AppendStringField(b, "state", status.state)
	b = protocol.AppendInt32Field(b, "Code", 0)
	if status.entryID != "" {
		b = protocol.AppendStringField(b, "queueId", status.entryID)
		b = protocol.AppendStringField(b, "entry_id", status.entryID)
	}
	if status.gameMode != "" {
		b = protocol.AppendStringField(b, "gameMode", status.gameMode)
		b = protocol.AppendStringField(b, "GameMode", status.gameMode)
	}
	if status.matchID != "" {
		b = protocol.AppendStringField(b, "matchId", status.matchID)
		b = protocol.AppendStringField(b, "MatchID", status.matchID)
	}
	if status.serverIP != "" {
		b = protocol.AppendStringField(b, "serverIP", status.serverIP)
		b = protocol.AppendStringField(b, "serverHost", status.serverIP)
	}
	if status.serverPort != 0 {
		b = protocol.AppendInt32Field(b, "serverPort", status.serverPort)
	}
	if status.mapName != "" {
		b = protocol.AppendStringField(b, "map", status.mapName)
		b = protocol.AppendStringField(b, "Map", status.mapName)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogErrorPayload(requestName string, message string) []byte {
	var b []byte
	var stack []int
	if requestName != "" {
		b = protocol.AppendStringField(b, "RT", requestName)
	}
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "error")
	b = protocol.AppendStringField(b, "message", message)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogMatchmakingErrorPayload(requestName string, code int32, message string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "error")
	b = protocol.AppendInt32Field(b, "Code", code)
	b = protocol.AppendStringField(b, "message", message)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// --- Rooms ---

func buildMmogQueryRoomsPayload() []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_QueryRooms")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b, stack = protocol.AppendArrayStart(b, stack, "Rooms")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "rooms")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogRoomSuccessPayload(requestName string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b, stack = protocol.AppendObjectStart(b, stack, "Room")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func mmogRoomResponseName(requestName string) string {
	switch requestName {
	case "YA_CustomRoomCreate":
		return "YA_CustomRoomCreateResponse"
	case "YA_CustomRoomStartMatch":
		return "YA_CustomRoomStartMatchResponse"
	case "YA_CustomRoomUserJoin":
		return "YA_CustomRoomUserJoinResponse"
	case "YA_CustomRoomUserLeave":
		return "YA_CustomRoomUserLeaveResponse"
	case "YA_CustomRoomUserReturn":
		return "YA_CustomRoomUserReturnResponse"
	case "YA_CustomRoomUserSwitchTeam":
		return "YA_CustomRoomUserSwitchTeamResponse"
	case "YA_CustomRoomChangeHost":
		return "YA_CustomRoomChangeHostResponse"
	case "YA_CustomRoomChangeSettings":
		return "YA_CustomRoomChangeSettingsResponse"
	case "YA_CustomRoomUpdate":
		return "YA_CustomRoomUpdateResponse"
	case "YA_CustomRoomEnterFleetSelect":
		return "YA_CustomRoomEnterFleetSelectResponse"
	case "YA_CustomRoomExitFleetSelect":
		return "YA_CustomRoomExitFleetSelectResponse"
	default:
		return requestName
	}
}

// --- Squads ---

func buildMmogSquadPayload(requestName string, playerPID string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b = protocol.AppendStringField(b, "PID", normalizedPlayerStatePID(playerPID))
	b, stack = protocol.AppendArrayStart(b, stack, "Squad")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Members")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// --- Chat ---

func buildMmogChatPayload(requestName string, playerPID string, payload []byte) []byte {
	channel := protocol.FirstNonEmptyString(payload, "channelName", "Channel", "channel")
	if channel == "" {
		channel = "global"
	}
	message := protocol.FirstNonEmptyString(payload, "message", "Message", "content", "Content", "text", "Text")
	if message != "" {
		persistMmogChatMessage(normalizedPlayerStatePID(playerPID), channel, message)
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b = protocol.AppendStringField(b, "channelName", channel)
	b, stack = protocol.AppendArrayStart(b, stack, "Messages")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "messages")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func persistMmogChatMessage(playerPID string, channel string, message string) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	_, _ = database.Exec(`INSERT INTO chat_messages(id,channel,sender_id,content) VALUES(?,?,?,?)`,
		uuid.New().String(), channel, playerPID, message)
}

// --- Fleet serialization ---

func appendMmogFleetRawFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b = protocol.AppendInt32Field(b, "fleet id", fleet.fleetID)
	b = protocol.AppendInt32Field(b, "FleetType", fleet.fleetType)
	b, stack = protocol.AppendInt32ArrayField(b, stack, "shipIds", fleet.shipIDs())
	b, stack = protocol.AppendBoolArrayField(b, stack, "ShipTechTreeComplete", fleet.shipTechTreeComplete())
	b = protocol.AppendInt32Field(b, "FlagShipID", fleet.flagshipShipID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutID", fleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutIndex", fleet.flagshipLoadoutIndex)
	return b, stack
}

func appendMmogFleetRuntimeFields(b []byte, fleet mmogFleetSeed) []byte {
	b = protocol.AppendBoolField(b, "AutoRepair", false)
	b = protocol.AppendBoolField(b, "Maintenance", false)
	b = protocol.AppendInt32Field(b, "LastWinTime", 0)
	b = protocol.AppendInt32Field(b, "ChargingBeginTime", 0)
	b = protocol.AppendInt32Field(b, "ChargingCharges", 1)
	b = protocol.AppendInt32Field(b, "Rating", 0)
	return b
}

func appendMmogFleetBackendFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b = protocol.AppendInt32Field(b, "m_fleetId", fleet.fleetID)
	b = protocol.AppendInt32Field(b, "m_flagshipIndex", fleet.flagshipIndex())
	b = protocol.AppendInt32Field(b, "m_fleetType", fleet.fleetType)
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_loadoutList", fleet.loadoutIDs())
	return b, stack
}

func appendMmogPlayerFleetEntry(b []byte, stack []int, playerPID string, fleet mmogFleetSeed) ([]byte, []int) {
	// IMPORTANT: UE4 FName comparison is case-insensitive, so field names that differ
	// only in case (e.g. "FlagShipID" vs "flagshipID") collide in the parsed object's
	// name table. When two such fields carry different values, the second overwrites
	// the first, which corrupted FlagShipID with the loadout ID and made the client's
	// fleet validator drop every entry ("Invalid fleet data, fleet array is empty").
	// Keep exactly one canonical field per logical attribute.
	// 
	// IMPORTANT: The client parser only handles field types 1-4 (double, double, int64, string).
	// int32 fields (protocol type 0x56) fall through to default=0, causing fleet type
	// validation to fail with 'Invalid fleet data received'. Use string fields for Type/Name.
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "Type", strconv.Itoa(int(fleet.fleetType)))
	b = protocol.AppendStringField(b, "FID", fleet.token)
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendStringField(b, "FleetID", fleet.token)
	b = protocol.AppendStringField(b, "Name", strconv.Itoa(int(fleet.fleetType)))
	b = protocol.AppendStringField(b, "DisplayName", fleet.displayName)
	b = protocol.AppendBoolField(b, "Unlocked", fleet.active || len(fleet.shipLoadouts) > 0)
	b = protocol.AppendInt32Field(b, "shipCount", int32(len(fleet.shipLoadouts)))
	b = appendMmogFleetRuntimeFields(b, fleet)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b = protocol.AppendInt32Field(b, "flagshipShipId", fleet.flagshipShipID)
	b, stack = appendMmogFleetBackendFields(b, stack, fleet)
	b = protocol.AppendBoolField(b, "bIsActive", fleet.active)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogFleetUnlockEntry(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "Type", strconv.Itoa(int(fleet.fleetType)))
	b = protocol.AppendBoolField(b, "Unlocked", fleet.active || len(fleet.shipLoadouts) > 0)
	b = protocol.AppendStringField(b, "Name", fleet.displayName)
	b = protocol.AppendStringField(b, "FleetID", fleet.token)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayerFleetsPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)
	fleets := state.fleets
	if len(fleets) == 0 {
		fleets = []mmogFleetSeed{starterFleetState()}
	}

	b = protocol.AppendStringField(b, "RT", "YA_PlayerFleets")
	b = protocol.AppendStringField(b, "FID", "PlayerFleets")
	b = protocol.AppendStringField(b, "PID", normalizedPlayerStatePID(playerPID))
	b = protocol.AppendStringField(b, "Name", "PlayerFleets")
	b = protocol.AppendInt32Field(b, "PlayedMatches", 0)
	b, stack = protocol.AppendArrayStart(b, stack, "Fleets")
	for _, fleet := range fleets {
		b, stack = appendMmogPlayerFleetEntry(b, stack, playerPID, fleet)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Items")
	for _, fleet := range fleets {
		b, stack = appendMmogFleetUnlockEntry(b, stack, fleet)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogStaticFleetTypeEntry(b []byte, stack []int, eligibility dreadconfig.FleetEligibility) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendInt32Field(b, "ID", eligibility.FleetType)
	b = protocol.AppendInt32Field(b, "ShipsToUnlock", eligibility.NumShipsToUnlockFleet)
	b = protocol.AppendInt32Field(b, "BaseMaintenanceCost", eligibility.BaseMaintenanceCost)
	b = protocol.AppendStringField(b, "FleetRatingMin", strconv.FormatFloat(eligibility.FleetRatingMin, 'f', 1, 64))
	b = protocol.AppendInt32Field(b, "FleetRatingCost", eligibility.FleetRatingCost)
	b = protocol.AppendInt32Field(b, "ChargeTime", eligibility.MaintenanceTime)
	b = protocol.AppendInt32Field(b, "ChargeCost", 0)
	b = protocol.AppendInt32Field(b, "AvailableCharges", 1)
	b, stack = protocol.AppendArrayStart(b, stack, "Tiers")
	for _, tier := range eligibility.AllowedTiers {
		b = protocol.AppendUnnamedInt32Field(b, tier)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetMaintenanceConfig(b []byte, stack []int) ([]byte, []int) {
	b, stack = protocol.AppendObjectStart(b, stack, "Maintenance")
	b = protocol.AppendStringField(b, "EliteCostMultiplier", "1.0")
	b = protocol.AppendStringField(b, "NonEliteCostMultiplier", "1.0")
	b = protocol.AppendInt32Field(b, "TopPlayerCount", 0)
	b = protocol.AppendStringField(b, "TopPlayerCostMultiplier", "1.0")
	b = protocol.AppendStringField(b, "NonTopPlayerCostMultiplier", "1.0")
	b = protocol.AppendStringField(b, "WinningCostMultiplier", "1.0")
	b = protocol.AppendStringField(b, "LoosingCostMultiplier", "1.0")
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetSlotEntry(b []byte, stack []int, loadout mmogShipLoadoutSeed, flagshipShipID int32) ([]byte, []int) {
	loadoutID := loadout.loadoutID()
	fleetShipID := loadout.effectiveFleetShipID()
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	// UE4 FName lookup is case-insensitive, so emit each canonical name once.
	b = protocol.AppendInt32Field(b, "ShipID", fleetShipID)
	b = protocol.AppendInt32Field(b, "LoadoutID", loadoutID)
	b = protocol.AppendInt32Field(b, "Position", loadout.position)
	b = protocol.AppendBoolField(b, "bIsFlagship", fleetShipID == flagshipShipID)
	b = protocol.AppendInt32Field(b, "Status", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetEntry(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "FID", fleet.token)
	b = protocol.AppendStringField(b, "FleetID", fleet.token)
	b = protocol.AppendStringField(b, "Name", fleet.displayName)
	b = protocol.AppendBoolField(b, "bIsActive", fleet.active)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b, stack = appendMmogFleetBackendFields(b, stack, fleet)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipSlots")
	for _, loadout := range fleet.shipLoadouts {
		b, stack = appendMmogStaticFleetSlotEntry(b, stack, loadout, fleet.flagshipShipID)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogStaticFleetDataPayload() []byte {
	return buildMmogStaticFleetDataPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogStaticFleetDataPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)

	b = protocol.AppendStringField(b, "RT", "YA_RequestStaticFleetData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "FleetTypes")
	for _, eligibility := range configBackedFleetEligibilities() {
		b, stack = appendMmogStaticFleetTypeEntry(b, stack, eligibility)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = appendMmogStaticFleetMaintenanceConfig(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Fleets")
	for _, fleet := range state.activeFleets() {
		b, stack = appendMmogStaticFleetEntry(b, stack, fleet)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipLoadouts")
	for _, loadout := range state.shipLoadouts() {
		b, stack = appendMmogStaticShipLoadout(b, stack, loadout)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// --- Season Data ---

const mmogSeasonDataSeasonsJSON = `[{"Name":"PVE_Season1","m_active":true,"m_name":"Miner Inconvenience","m_descShort":"Season 1 short Description","m_descLong":"Season 1 long Description","m_imageLarge":"None","m_imageSmall":"None","m_rewardLevels":[]}]`

const mmogSeasonDataEventsJSON = `[{"Name":"PVE_S1E1","m_name":"Incident Management","m_descShort":"Miner Inconvenience - Incident Management","m_descLong":"Jupiter Arms installations on the surface of Io have been under attack for weeks by raiding parties using hit and run tactics to wear down the corp's spread out defenses. The megacorp is now contacting mercenary captains directly to assist their forces and protect Jupiter Arms assets, hoping to finally put an end to these costly attacks.","m_map":"None","m_mapParameters":"","m_gameMode":"YGMT_HORDE","m_color":{"r":160,"g":144,"b":131,"a":255},"m_imageSmall":"None","m_imageLarge":"None","m_rewardLevels":[],"m_startDate":"2018.05.16-16.00.00","m_endDate":"2018.05.16-16.19.59","m_season":"PVE_Season1"}]`

func buildMmogSeasonDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetSeasonData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "Events", mmogSeasonDataEventsJSON)
	b = protocol.AppendStringField(b, "Seasons", mmogSeasonDataSeasonsJSON)
	b = protocol.AppendStringField(b, "CurrentSeason", "")
	b = protocol.AppendStringField(b, "ActiveEvent", "")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogSeasonProgressPayload() []byte {
	return buildMmogSeasonProgressPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogSeasonProgressPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetSeasonProgress")
	b, stack = protocol.AppendObjectStart(b, stack, "result")

	// Load actual season progress from database
	seasonProgress := loadPlayerSeasonProgress(playerPID)

	// EventScores array - contains player's progress in each event
	b, stack = protocol.AppendArrayStart(b, stack, "EventScores")
	for _, progress := range seasonProgress {
		b, stack = appendMmogEventScoreEntry(b, stack, progress)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	// EventRewards array - contains rewards claimed for events
	b, stack = protocol.AppendArrayStart(b, stack, "EventRewards")
	b, stack = protocol.AppendObjectEnd(b, stack)

	// SeasonRewards array - contains rewards claimed for season
	b, stack = protocol.AppendArrayStart(b, stack, "SeasonRewards")
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, stack = protocol.AppendObjectEnd(b, stack)

	if len(stack) != 0 {
		return nil
	}

	return b
}

type playerSeasonProgress struct {
	seasonID string
	xp       int32
	level    int32
}

func loadPlayerSeasonProgress(playerPID string) []playerSeasonProgress {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT season_id, xp, level FROM player_season_progress WHERE user_id=? ORDER BY season_id`, playerPID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var progress []playerSeasonProgress
	for rows.Next() {
		var p playerSeasonProgress
		if err := rows.Scan(&p.seasonID, &p.xp, &p.level); err != nil {
			continue
		}
		progress = append(progress, p)
	}
	return progress
}

func appendMmogEventScoreEntry(b []byte, stack []int, progress playerSeasonProgress) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "SeasonID", progress.seasonID)
	b = protocol.AppendInt32Field(b, "Score", progress.xp)
	b = protocol.AppendInt32Field(b, "Level", progress.level)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogSeasonProgressEntry(b []byte, stack []int, progress playerSeasonProgress) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "SeasonID", progress.seasonID)
	b = protocol.AppendInt32Field(b, "XP", progress.xp)
	b = protocol.AppendInt32Field(b, "Level", progress.level)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// --- Player Data ---

func buildMmogPlayerGetPayload(playerPID string) []byte {
	return buildMmogPlayerDataPayload("YA_PlayerGet", playerPID)
}

// buildMmogPlayerDataPayload builds the full player data payload with a configurable RT field.
// Used by both YA_PlayerGet and YA_RefreshPlayerProfile (which must echo back the correct RT).
func buildMmogPlayerDataPayload(rt string, playerPID string) []byte {
	var b []byte
	var stack []int
	now := int32(time.Now().Unix())
	membershipExpiresAt := now + 31536000
	state := mmogPlayerStateForPID(playerPID)
	starterFleet := state.activeFleet()

	b = protocol.AppendStringField(b, "RT", rt)
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendStringField(b, "SID", "local_session")
	b = protocol.AppendInt32Field(b, "tll", 1)
	b = protocol.AppendInt32Field(b, "tpl", 1)
	b = protocol.AppendInt32Field(b, "gl", state.softCurrency)
	b = protocol.AppendInt32Field(b, "ob", state.premiumCurrency)
	b = protocol.AppendInt32Field(b, "rep", 0)
	b = protocol.AppendInt32Field(b, "repDN_L", 0)
	b = protocol.AppendInt32Field(b, "repDN_M", 0)
	b = protocol.AppendInt32Field(b, "repDN_H", 0)
	b = protocol.AppendInt32Field(b, "repAS_L", 0)
	b = protocol.AppendInt32Field(b, "repAS_M", 0)
	b = protocol.AppendInt32Field(b, "repAS_H", 0)
	b = protocol.AppendInt32Field(b, "repSC_L", 0)
	b = protocol.AppendInt32Field(b, "repSC_M", 0)
	b = protocol.AppendInt32Field(b, "repSC_H", 0)
	b = protocol.AppendInt32Field(b, "repSN_L", 0)
	b = protocol.AppendInt32Field(b, "repSN_M", 0)
	b = protocol.AppendInt32Field(b, "repSN_H", 0)
	b = protocol.AppendInt32Field(b, "repSU_L", 0)
	b = protocol.AppendInt32Field(b, "repSU_M", 0)
	b = protocol.AppendInt32Field(b, "repSU_H", 0)
	b = protocol.AppendInt32Field(b, "ReputationGoalID", 0)
	b = protocol.AppendStringField(b, "disp", "")
	b = protocol.AppendStringField(b, "motto", "")
	b = protocol.AppendStringField(b, "SGD", "")
	b = protocol.AppendStringField(b, "SCtA", "")
	b = protocol.AppendStringField(b, "LGVersion", "0")
	b, stack = protocol.AppendObjectStart(b, stack, "Membership")
	b = protocol.AppendInt32Field(b, "ExpireTime", membershipExpiresAt)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendInt32Field(b, "DailyContractStateID", int32(dailyContractState(playerPID)))
	b = protocol.AppendInt32Field(b, "LastContractsAssignment", int32(now))
	b = protocol.AppendInt32Field(b, "DailyContractLastReplaceTime", int32(now))
	b = protocol.AppendInt32Field(b, "FreeXp", state.freeXP)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipXps")
	b, stack = protocol.AppendObjectEnd(b, stack)

	// Add season progress data
	seasonProgress := loadPlayerSeasonProgress(playerPID)
	b, stack = protocol.AppendArrayStart(b, stack, "SeasonProgress")
	for _, progress := range seasonProgress {
		b, stack = appendMmogSeasonProgressEntry(b, stack, progress)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b = protocol.AppendInt32Field(b, "ServerTime", now)
	b = protocol.AppendInt32Field(b, "ClientTime", now)
	b = protocol.AppendStringField(b, "PublicIP", "")
	b = protocol.AppendStringField(b, "Country", "")
	b = protocol.AppendStringField(b, "Platform", "steam")
	b, stack = protocol.AppendObjectStart(b, stack, "CustomRoom")
	b = protocol.AppendStringField(b, "roomId", "")
	b = protocol.AppendStringField(b, "hostPid", "")
	b, stack = protocol.AppendArrayStart(b, stack, "teams")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "settings")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "supportedModes")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "gameMode", "")
	b = protocol.AppendStringField(b, "mapName", "")
	b, stack = protocol.AppendArrayStart(b, stack, "supportedMaps")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "chatRoomId", "")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "FleetID", starterFleet.token)
	b = protocol.AppendInt32Field(b, "fleet id", starterFleet.fleetID)
	b = protocol.AppendInt32Field(b, "FleetType", starterFleet.fleetType)
	b = protocol.AppendInt32Field(b, "shipId", starterFleet.flagshipShipID)
	b, stack = protocol.AppendInt32ArrayField(b, stack, "shipIds", starterFleet.shipIDs())
	b, stack = protocol.AppendBoolArrayField(b, stack, "ShipTechTreeComplete", starterFleet.shipTechTreeComplete())
	// FName comparison is case-insensitive in UE4, so "FlagShipID" and "flagshipID"
	// collide. Sending both with different values (ship ID vs loadout ID) used to
	// overwrite the ship ID with the loadout ID and break fleet validation in the
	// client. Keep one canonical FlagShipID(=ship) field and a distinct
	// flagshipShipId camelCase alias.
	b = protocol.AppendInt32Field(b, "FlagShipID", starterFleet.flagshipShipID)
	b = protocol.AppendInt32Field(b, "flagshipShipId", starterFleet.flagshipShipID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutID", starterFleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b = protocol.AppendInt32Field(b, "selectedLoadoutID", starterFleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "selectedLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b, stack = appendMmogFleetBackendFields(b, stack, starterFleet)
	b, stack = protocol.AppendArrayStart(b, stack, "FactionReputation")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Officers")
	// F3: Wire officer data into YA_PlayerGet Officers array
	for _, officer := range dreadconfig.AllOfficers() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "m_enabling", officer.Enabling)
		b = protocol.AppendStringField(b, "m_triggers", officer.Triggers)
		b = protocol.AppendStringField(b, "m_effects", officer.Effects)
		b = protocol.AppendBoolField(b, "m_stackOnAdding", officer.StackOnAdding)
		b = protocol.AppendBoolField(b, "m_isPerkFeat", officer.IsPerkFeat)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipLoadouts")
	for _, loadout := range state.shipLoadouts() {
		b, stack = appendMmogShipLoadout(b, stack, playerPID, loadout)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Ribbons")
	for _, ribbon := range loadPlayerRibbons(playerPID) {
		b, stack = appendMmogRibbonEntry(b, stack, ribbon)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Medals")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Friends")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "Squad")
	b = protocol.AppendStringField(b, "PID", "")
	b = protocol.AppendStringField(b, "PIDLeader", "")
	b, stack = protocol.AppendArrayStart(b, stack, "Users")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "GameMode", "")
	b = protocol.AppendInt32Field(b, "State", 0)
	b = protocol.AppendInt32Field(b, "FleetType", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "PPF", "")
	b = protocol.AppendInt32Field(b, "tslm", 0)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// --- Loadout serialization ---

func appendMmogShipLoadoutEntry(b []byte, stack []int, playerPID string, loadout mmogShipLoadoutSeed, includePID bool) ([]byte, []int) {
	loadoutID := loadout.loadoutID()
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "ID", loadout.entryID())
	if includePID {
		b = protocol.AppendStringField(b, "PID", playerPID)
	}
	b = protocol.AppendInt32Field(b, "LoadoutID", loadoutID)
	b = protocol.AppendInt32Field(b, "m_loadoutID", loadoutID)
	b = protocol.AppendInt32Field(b, "precastLoadout", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendBoolField(b, "m_isActiveLoadout", loadout.active)
	b = protocol.AppendStringField(b, "name", loadout.loadoutName)
	b = protocol.AppendStringField(b, "m_loadoutName", loadout.loadoutName)
	b = protocol.AppendInt32Field(b, "shipID", loadout.effectiveFleetShipID())
	b = protocol.AppendInt32Field(b, "m_shipId", loadout.effectiveFleetShipID())
	b = protocol.AppendInt32Field(b, "class", loadout.ship.shipClass)
	b = protocol.AppendStringField(b, "m_name", loadout.loadoutName)
	b = protocol.AppendInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = protocol.AppendStringField(b, "displayInfo", loadout.displayInfo())
	b = protocol.AppendStringField(b, "m_displayInfo", loadout.displayInfo())
	b = protocol.AppendInt32Field(b, "m_loadoutTier", 1)
	b = protocol.AppendBoolField(b, "m_loadoutComplete", loadout.complete())
	b = protocol.AppendInt32Field(b, "weaponPrimary", loadout.weaponPrimaryItemID())
	b = protocol.AppendInt32Field(b, "weaponSecondary", loadout.weaponSecondaryItemID())
	b = protocol.AppendInt32Field(b, "abilityPrimary", loadout.abilityItemID(0))
	b = protocol.AppendInt32Field(b, "abilitySecondary", loadout.abilityItemID(1))
	b = protocol.AppendInt32Field(b, "abilityPerimeter", loadout.abilityItemID(2))
	b = protocol.AppendInt32Field(b, "abilityInternal", loadout.abilityItemID(3))
	b = protocol.AppendInt32Field(b, "perkCom", loadout.perkItemID(0))
	b = protocol.AppendInt32Field(b, "perkWeapon", loadout.perkItemID(1))
	b = protocol.AppendInt32Field(b, "perkNavigation", loadout.perkItemID(2))
	b = protocol.AppendInt32Field(b, "perkEngineer", loadout.perkItemID(3))
	b = protocol.AppendInt32Field(b, "m_primaryWeaponItemId", loadout.weaponPrimaryItemID())
	b = protocol.AppendInt32Field(b, "m_secondaryWeaponItemId", loadout.weaponSecondaryItemID())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_weaponIDs", loadout.weaponIDs())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_abilityIDs", loadout.abilityItemIDs())
	// m_perkIDs and m_perkIds collapse to the same FName, so emit once.
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_perkIDs", loadout.perkItemIDs())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_abilityItemIds", loadout.abilityItemIDs())
	b, stack = protocol.AppendStringArrayField(b, stack, "m_perkNames", loadout.perkNames())
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogShipLoadout(b []byte, stack []int, playerPID string, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogShipLoadoutEntry(b, stack, playerPID, loadout, true)
}

func appendMmogStaticShipLoadout(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogShipLoadoutEntry(b, stack, "", loadout, false)
}

func appendMmogShipLoadoutInfoFields(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b = protocol.AppendStringField(b, "ID", loadout.entryID())
	b = protocol.AppendStringField(b, "m_loadoutName", loadout.loadoutName)
	// LoadoutID/loadoutID and shipID/ShipID collide under FName comparison; keep
	// the canonical Pascal-case form and a distinct m_-prefixed alias.
	b = protocol.AppendInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = protocol.AppendInt32Field(b, "m_loadoutID", loadout.loadoutID())
	b = protocol.AppendInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "ShipID", loadout.effectiveFleetShipID())
	b = protocol.AppendInt32Field(b, "m_shipId", loadout.effectiveFleetShipID())
	b = protocol.AppendInt32Field(b, "loadoutIndex", loadout.loadoutIndex)
	b = protocol.AppendInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = protocol.AppendStringField(b, "m_displayInfo", loadout.displayInfo())
	b = protocol.AppendInt32Field(b, "m_loadoutTier", 1)
	b = protocol.AppendBoolField(b, "m_loadoutComplete", loadout.complete())
	b = protocol.AppendInt32Field(b, "m_primaryWeaponItemId", loadout.weaponPrimaryItemID())
	b = protocol.AppendInt32Field(b, "m_secondaryWeaponItemId", loadout.weaponSecondaryItemID())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_abilityItemIds", loadout.abilityItemIDs())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_perkIds", loadout.perkItemIDs())
	b, stack = protocol.AppendStringArrayField(b, stack, "m_perkNames", loadout.perkNames())
	return b, stack
}

// --- Owned Inventory ---

// --- Tech Tree ---

// --- Stats / Progression ---

func buildMmogPlayerStatsCounterDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerStatsCounterData")
	b, stack = protocol.AppendArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntry(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntry(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogStatsCounterEntry(b []byte, stack []int) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendInt32Field(b, "counterId", 0)
	b = protocol.AppendInt32Field(b, "subId", 0)
	b = protocol.AppendInt32Field(b, "counterSubId", 0)
	b = protocol.AppendInt32Field(b, "m_counterSubId", 0)
	b = protocol.AppendInt32Field(b, "counterValue", 0)
	b = protocol.AppendInt32Field(b, "value", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayerProgressionPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)
	ships := playerOwnedTechTreeShips(playerPID)

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerProgression")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendInt32Field(b, "CurrentXP", state.currentXP)
	b = protocol.AppendInt32Field(b, "CurrentRank", state.currentRank)
	b = protocol.AppendInt32Field(b, "RankXP", state.rankXP)
	b = protocol.AppendInt32Field(b, "XPToNextRank", handlers.RankXPThreshold(state.currentRank+1))
	b = protocol.AppendInt32Field(b, "NumUnlockedShips", int32(countOwnedShips(ships)))
	b, stack = protocol.AppendArrayStart(b, stack, "shipProgressionUiData")
	for _, ship := range ships {
		b, stack = appendMmogShipProgression(b, stack, ship)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogShipProgression(b []byte, stack []int, ship mmogShipSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendInt32Field(b, "shipID", ship.id)
	b = protocol.AppendInt32Field(b, "xp", 0)
	b = protocol.AppendInt32Field(b, "tier", 1)
	b = protocol.AppendBoolField(b, "owned", ship.owned)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogProgressionDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetProgressionData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "ProgressionData")
	for _, shipID := range starterShipIDs() {
		b = protocol.AppendUnnamedInt32Field(b, shipID)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogTechTreePayload(playerPID ...string) []byte {
	var b []byte
	var stack []int
	pid := defaultMmogPlayerPID
	if len(playerPID) > 0 {
		pid = playerPID[0]
	}
	ships := playerOwnedTechTreeShips(pid)

	b = protocol.AppendStringField(b, "RT", "YA_GetTechTree")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendInt32Field(b, "techTreeRowCount", int32(len(ships)))
	b, stack = protocol.AppendArrayStart(b, stack, "techTreeRow")
	for _, ship := range ships {
		b, stack = appendMmogTechTreeRow(b, stack, ship)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "moduleUiData")
	for _, module := range starterModuleUIDataSeeds() {
		b, stack = appendMmogModuleUIDataEntry(b, stack, module)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogTechTreeRow(b []byte, stack []int, ship mmogShipSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendInt32Field(b, "NodeID", ship.nodeID)
	b = protocol.AppendInt32Field(b, "ShipID", ship.id)
	b = protocol.AppendInt32Field(b, "shipID", ship.id)
	b = protocol.AppendInt32Field(b, "m_shipID", ship.id)
	b = protocol.AppendInt32Field(b, "m_shipId", ship.id)
	b = protocol.AppendInt32Field(b, "ParentID", ship.parentID)
	b = protocol.AppendStringField(b, "Name", ship.name)
	b = protocol.AppendStringField(b, "m_name", ship.name)
	b = protocol.AppendInt32Field(b, "NodeType", ship.nodeType)
	b = protocol.AppendInt32Field(b, "Tier", 1)
	b = protocol.AppendInt32Field(b, "UnlockCost", ship.unlockCost)
	b = protocol.AppendInt32Field(b, "PrereqID1", ship.prereqID1)
	b = protocol.AppendInt32Field(b, "PrereqID2", ship.prereqID2)
	b = protocol.AppendBoolField(b, "bIsUnlocked", ship.owned)
	b = protocol.AppendBoolField(b, "bIsPurchased", ship.owned)
	b = protocol.AppendBoolField(b, "bIsNew", ship.bIsNew)
	b = protocol.AppendInt32Field(b, "ShipClass", ship.shipClass)
	b = protocol.AppendInt32Field(b, "Weight", ship.weight)
	b = protocol.AppendInt32Field(b, "m_currentBaseClass", ship.shipClass)
	b = protocol.AppendInt32Field(b, "m_currentShipClass", ship.shipClass)
	b = protocol.AppendInt32Field(b, "m_shipTier", 1)
	b = protocol.AppendInt32Field(b, "m_weight", ship.weight)
	if loadout, ok := starterLoadoutByShipID(ship.id); ok {
		b = protocol.AppendInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
		b, stack = protocol.AppendObjectStart(b, stack, "m_shipLoadoutInfo")
		b, stack = appendMmogShipLoadoutInfoFields(b, stack, loadout)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogItemPriceDataFields(b []byte) []byte {
	b = protocol.AppendBoolField(b, "m_hasPriceChanged", false)
	b = protocol.AppendStringField(b, "m_currencyCode", "")
	b = protocol.AppendInt32Field(b, "m_realCurrency", 0)
	b = protocol.AppendInt32Field(b, "m_hardCurrency", 0)
	b = protocol.AppendInt32Field(b, "m_softCurrency", 0)
	b = protocol.AppendInt32Field(b, "m_freeXP", 0)
	b = protocol.AppendInt32Field(b, "m_shipXP", 0)
	return b
}

func appendMmogModuleUIDataEntry(b []byte, stack []int, module mmogModuleUIDataSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "m_techTreePurchasePrice")
	b = appendMmogItemPriceDataFields(b)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "m_techTreeResearchPrice")
	b = appendMmogItemPriceDataFields(b)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendInt32Field(b, "m_techTreeItemState", 4)
	b = protocol.AppendInt32Field(b, "m_index", module.index)
	b = protocol.AppendInt32Field(b, "m_priceCurrency", 0)
	b = protocol.AppendInt32Field(b, "m_priceAmount", 0)
	b = protocol.AppendInt32Field(b, "m_originalPriceCurrency", 0)
	b = protocol.AppendInt32Field(b, "m_originalPriceAmount", 0)
	b = protocol.AppendStringField(b, "m_moduleTexturePath", "")
	b = protocol.AppendStringField(b, "m_iconTexturePath", "")
	b = protocol.AppendInt32Field(b, "m_tier", 1)
	b = protocol.AppendBoolField(b, "m_shouldShowTierIcon", true)
	b = protocol.AppendBoolField(b, "m_isOwned", module.owned)
	b = protocol.AppendBoolField(b, "m_isOnSale", false)
	b = protocol.AppendBoolField(b, "m_isNew", false)
	b = protocol.AppendBoolField(b, "m_isEquipped", module.equipped)
	b = protocol.AppendInt32Field(b, "m_itemId", module.itemID)
	if weapon, ok := dreadconfig.WeaponByID(module.itemID); ok {
		b = protocol.AppendInt32Field(b, "m_damageHigh", weapon.DamageHigh)
		b = protocol.AppendInt32Field(b, "m_damageMedium", weapon.DamageMedium)
		b = protocol.AppendInt32Field(b, "m_damageLow", weapon.DamageLow)
		b = protocol.AppendStringField(b, "m_weaponCooldownTime", strconv.FormatFloat(weapon.WeaponCooldownTime, 'f', 3, 64))
		b = protocol.AppendInt32Field(b, "m_ammoMagazinSize", weapon.AmmoMagazinSize)
		b = protocol.AppendStringField(b, "m_spreadBaseValue", strconv.FormatFloat(weapon.SpreadBaseValue, 'f', 2, 64))
		b = protocol.AppendStringField(b, "m_spreadMaxValue", strconv.FormatFloat(weapon.SpreadMaxValue, 'f', 2, 64))
		b = protocol.AppendInt32Field(b, "m_maxRange", weapon.MaxRange)
		b = protocol.AppendStringField(b, "m_slotType", weapon.SlotType)
		b = protocol.AppendStringField(b, "m_class", weapon.Class)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogCareerProgressionPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetCareerProgression")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "m_categories")
	b, stack = appendMmogProgressionCategories(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogGameConfigDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetGameConfigData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendInt32Field(b, "MaxSquadSize", 5)
	b = protocol.AppendBoolField(b, "banned", false)
	b, stack = protocol.AppendArrayStart(b, stack, "GameModes")
	for _, mode := range matchmaker.GameModeConfigs() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "Name", mode.Name)
		b = protocol.AppendInt32Field(b, "TeamSize", mode.TeamSize)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogFeatureTogglePayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetFeatureToggle")
	b = protocol.AppendBoolField(b, "isEnabled", true)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendBoolField(b, "isEnabled", true)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogPlayerPurchasesPayload() []byte {
	return buildMmogPlayerPurchasesPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogPlayerPurchasesPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerPurchases")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "PurchasesData")
	for _, itemID := range persistedMmogPlayerPurchaseItemIDs(playerPID) {
		b = protocol.AppendUnnamedInt32Field(b, itemID)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogStaticCareerDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetStaticCareerData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "m_categoryDTPath", configBackedProgressionCategoryDataTablePath())
	b, stack = protocol.AppendArrayStart(b, stack, "m_categories")
	b, stack = appendMmogProgressionCategories(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogScoringDataPayload() []byte {
	var b []byte
	var stack []int
	const emptyTableJSON = `[]`

	b = protocol.AppendStringField(b, "RT", "YA_GetScoringData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "YScoringDataTableRow", emptyTableJSON)
	b = protocol.AppendStringField(b, "m_defendScoringDataTable", emptyTableJSON)
	b = protocol.AppendStringField(b, "m_remainingPlayerScoringDataTable", emptyTableJSON)
	b = protocol.AppendStringField(b, "m_killScoringDataTable", emptyTableJSON)
	b = protocol.AppendStringField(b, "m_waveScoringDataTable", emptyTableJSON)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogDailyContractsDataPayload() []byte {
	return buildMmogDailyContractsDataPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogProjectileDataPayload() []byte {
	var b []byte
	var stack []int
	projectiles := dreadconfig.AllProjectiles()

	b = protocol.AppendStringField(b, "RT", "YA_GetProjectileData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "Projectiles")
	
	for rowName, projectile := range projectiles {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "RowName", rowName)
		
		// Use reflection to add all projectile fields
		val := reflect.ValueOf(projectile)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			
			// Convert field name to the format expected by the client
			clientFieldName := "m_" + toSnakeCase(fieldName)
			
			switch field.Kind() {
			case reflect.Float32, reflect.Float64:
				b = protocol.AppendStringField(b, clientFieldName, fmt.Sprintf("%.6f", field.Float()))
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Int()))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Uint()))
			case reflect.Bool:
				b = protocol.AppendBoolField(b, clientFieldName, field.Bool())
			case reflect.String:
				b = protocol.AppendStringField(b, clientFieldName, field.String())
			}
		}
		
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// BuildYAGetShipFeatsPayload builds the payload for YA_GetShipFeats
func BuildYAGetShipFeatsPayload() []byte {
	var b []byte
	var stack []int
	shipFeats := dreadconfig.AllShipFeats()

	b = protocol.AppendStringField(b, "RT", "YA_GetShipFeats")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "ShipFeats")
	
	for compositeName, feat := range shipFeats {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "CompositeName", compositeName)
		
		// Use reflection to add all ship feat fields
		val := reflect.ValueOf(feat)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			
			// Convert field name to the format expected by the client
			clientFieldName := "m_" + toSnakeCase(fieldName)
			
			switch field.Kind() {
			case reflect.Float32, reflect.Float64:
				b = protocol.AppendStringField(b, clientFieldName, fmt.Sprintf("%.6f", field.Float()))
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Int()))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Uint()))
			case reflect.Bool:
				b = protocol.AppendBoolField(b, clientFieldName, field.Bool())
			case reflect.String:
				b = protocol.AppendStringField(b, clientFieldName, field.String())
			}
		}
		
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// BuildYAGetAbilitiesPayload builds the payload for YA_GetAbilities (E5)
func BuildYAGetAbilitiesPayload() []byte {
	var b []byte
	var stack []int
	abilities := dreadconfig.AllAbilities()

	b = protocol.AppendStringField(b, "RT", "YA_GetAbilities")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "Abilities")
	
	for compositeName, ability := range abilities {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "CompositeName", compositeName)
		
		// Use reflection to add all ability fields
		val := reflect.ValueOf(ability)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			
			// Convert field name to the format expected by the client
			clientFieldName := "m_" + toSnakeCase(fieldName)
			
			switch field.Kind() {
			case reflect.Float32, reflect.Float64:
				b = protocol.AppendStringField(b, clientFieldName, fmt.Sprintf("%.6f", field.Float()))
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Int()))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				b = protocol.AppendInt32Field(b, clientFieldName, int32(field.Uint()))
			case reflect.Bool:
				b = protocol.AppendBoolField(b, clientFieldName, field.Bool())
			case reflect.String:
				b = protocol.AppendStringField(b, clientFieldName, field.String())
			}
		}
		
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteRune('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func buildMmogDailyContractsDataPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetDailyContractsData")
	b = protocol.AppendInt32Field(b, "DailyContractStateID", int32(dailyContractState(playerPID)))
	b = protocol.AppendInt32Field(b, "LastContractsAssignment", int32(time.Now().Unix()))
	b = protocol.AppendInt32Field(b, "DailyContractLastReplaceTime", int32(time.Now().Unix()))

	// Load contracts from database
	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	
	b, stack = protocol.AppendArrayStart(b, stack, "Quests")
	if database != nil {
		rows, err := database.Query(`SELECT contract_id, payload, progress, state FROM player_contracts WHERE user_id=? AND state='active' ORDER BY created_at LIMIT 3`, pid)
		if err == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var contractID, payloadJSON, state string
				var progress int32
				if err := rows.Scan(&contractID, &payloadJSON, &progress, &state); err == nil {
					b, stack = protocol.AppendUnnamedObjectStart(b, stack)
					b = protocol.AppendStringField(b, "ContractID", contractID)
					b = protocol.AppendInt32Field(b, "Progress", progress)
					b = protocol.AppendStringField(b, "State", state)
					
					// Parse payload to include contract details
					var contractData map[string]interface{}
					if json.Unmarshal([]byte(payloadJSON), &contractData) == nil {
						if name, ok := contractData["name"].(string); ok {
							b = protocol.AppendStringField(b, "Name", name)
						}
						if desc, ok := contractData["description"].(string); ok {
							b = protocol.AppendStringField(b, "Description", desc)
						}
						if targetKills, ok := contractData["targetKills"].(float64); ok {
							b = protocol.AppendInt32Field(b, "TargetKills", int32(targetKills))
						}
						if targetScore, ok := contractData["targetScore"].(float64); ok {
							b = protocol.AppendInt32Field(b, "TargetScore", int32(targetScore))
						}
						if rewardXP, ok := contractData["rewardXP"].(float64); ok {
							b = protocol.AppendInt32Field(b, "RewardXP", int32(rewardXP))
						}
						if rewardGP, ok := contractData["rewardGP"].(float64); ok {
							b = protocol.AppendInt32Field(b, "RewardGP", int32(rewardGP))
						}
					}
					b, stack = protocol.AppendObjectEnd(b, stack)
				}
			}
		}
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, stack = protocol.AppendArrayStart(b, stack, "Contracts")
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "Contracts")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogBoosterDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetBoosterData")
	b, stack = protocol.AppendArrayStart(b, stack, "BoosterTable")
	for _, booster := range havocBoosters {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "BoosterID", booster.id)
		b = protocol.AppendStringField(b, "BoosterName", booster.name)
		b = protocol.AppendInt32Field(b, "Cost", booster.cost)
		b = protocol.AppendStringField(b, "Description", booster.desc)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "GoldMembershipTable")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

var havocBoosters = []struct {
	id   int32
	name string
	cost int32
	desc string
}{
	{1, "Damage Boost", 500, "Increases weapon damage by 25% for one wave"},
	{2, "Shield Boost", 400, "Increases shield absorption by 30% for one wave"},
	{3, "Speed Boost", 300, "Increases movement speed by 20% for one wave"},
	{4, "Repair Boost", 600, "Repairs 50% hull damage instantly"},
	{5, "Energy Boost", 350, "Refills energy to maximum"},
	{6, "XP Boost", 750, "Doubles XP earned for one wave"},
}

// AI Difficulty Levels
var aiDifficultyLevels = []struct {
	name         string
	difficultyID int32
	spawnRate    float32
	healthMult   float32
	damageMult   float32
	xpMult       float32
	creditMult   float32
	description  string
}{
	{"Easy", 1, 0.8, 0.8, 0.8, 0.8, 0.8, "Reduced enemy count and stats"},
	{"Normal", 2, 1.0, 1.0, 1.0, 1.0, 1.0, "Standard difficulty"},
	{"Hard", 3, 1.2, 1.3, 1.3, 1.3, 1.3, "Increased enemy count and stats"},
	{"Very Hard", 4, 1.5, 1.6, 1.6, 1.6, 1.6, "Significantly increased challenge"},
	{"Nightmare", 5, 2.0, 2.0, 2.0, 2.0, 2.0, "Maximum difficulty for elite players"},
}

// Boss Types with Phase Mechanics
var bossTypes = []struct {
	bossID      string
	name        string
	shipClass   string
	phaseCount  int32
	difficulty  int32
	rewardXP    int32
	rewardGP    int32
	description string
}{
	{"boss_raider_captain", "Raider Captain", "Corvette", 2, 1, 500, 1000, "Light corvette with hit-and-run tactics"},
	{"boss_destroyer_commander", "Destroyer Commander", "Destroyer", 3, 2, 1000, 2000, "Heavy destroyer with broadside attacks"},
	{"boss_battlecruiser_admiral", "Battlecruiser Admiral", "Battlecruiser", 4, 3, 2000, 4000, "Armored battlecruiser with shield phases"},
	{"boss_dreadnought_titan", "Dreadnought Titan", "Dreadnought", 5, 4, 3500, 7000, "Massive dreadnought with multiple weapon systems"},
	{"boss_carrier_overlord", "Carrier Overlord", "Carrier", 5, 5, 5000, 10000, "Carrier that spawns fighter squadrons"},
	{"boss_artillery_fortress", "Artillery Fortress", "Artillery", 3, 3, 1500, 3000, "Long-range artillery platform"},
	{"boss_stealth_phantom", "Stealth Phantom", "Stealth", 4, 4, 2500, 5000, "Cloaked ship with ambush tactics"},
	{"boss_support_nexus", "Support Nexus", "Support", 3, 2, 1200, 2400, "Support ship that heals allies"},
	{"boss_tactical_strategist", "Tactical Strategist", "Tactical", 4, 3, 1800, 3600, "Tactical ship with area denial"},
	{"boss_experimental_prototype", "Experimental Prototype", "Experimental", 5, 5, 4000, 8000, "Unstable prototype with random abilities"},
	{"boss_pirate_lord", "Pirate Lord", "Pirate", 4, 4, 3000, 6000, "Pirate flagship with boarding parties"},
	{"boss_mining_goliath", "Mining Goliath", "Industrial", 3, 2, 1400, 2800, "Heavy mining vessel with drills"},
	{"boss_research_vessel", "Research Vessel", "Science", 3, 3, 1600, 3200, "Science ship with experimental weapons"},
	{"boss_colony_ship", "Colony Ship", "Transport", 4, 3, 2200, 4400, "Large transport with defensive turrets"},
	{"boss_flagship_leviathan", "Flagship Leviathan", "Flagship", 5, 5, 6000, 12000, "Ultimate boss with all ship capabilities"},
}

// Havoc Mode Wave Configuration
var havocWaveConfig = []struct {
	waveNumber     int32
	enemyCount     int32
	eliteCount     int32
	bossWave       bool
	bossID         string
	timeLimit      int32
	rewardXP       int32
	rewardGP       int32
	description    string
}{
	{1, 5, 0, false, "", 120, 100, 200, "Initial wave - light enemies"},
	{2, 8, 1, false, "", 120, 150, 300, "Second wave - increased count"},
	{3, 10, 2, false, "", 120, 200, 400, "Third wave - elite enemies appear"},
	{4, 12, 3, false, "", 120, 250, 500, "Fourth wave - heavy opposition"},
	{5, 15, 4, false, "", 120, 300, 600, "Fifth wave - overwhelming force"},
	{6, 1, 0, true, "boss_raider_captain", 180, 500, 1000, "Boss wave - Raider Captain"},
	{7, 18, 5, false, "", 120, 400, 800, "Seventh wave - maximum enemies"},
	{8, 20, 6, false, "", 120, 500, 1000, "Eighth wave - elite swarm"},
	{9, 25, 8, false, "", 120, 600, 1200, "Ninth wave - survival test"},
	{10, 1, 0, true, "boss_destroyer_commander", 240, 1000, 2000, "Boss wave - Destroyer Commander"},
	{11, 30, 10, false, "", 120, 800, 1600, "Eleventh wave - endless swarm"},
	{12, 35, 12, false, "", 120, 1000, 2000, "Twelfth wave - final stand"},
	{13, 1, 0, true, "boss_dreadnought_titan", 300, 3500, 7000, "Final boss - Dreadnought Titan"},
}

// Havoc Mode Modifiers
var havocModifiers = []struct {
	modifierID   string
	name         string
	description  string
	waveStart    int32
	effectType   string
	effectValue  float32
}{
	{"mod_enemy_shields", "Enemy Shield Boost", "Enemies gain 50% shield strength", 3, "shield", 1.5},
	{"mod_enemy_damage", "Enemy Damage Increase", "Enemies deal 30% more damage", 5, "damage", 1.3},
	{"mod_enemy_speed", "Enemy Speed Boost", "Enemies move 25% faster", 7, "speed", 1.25},
	{"mod_spawn_rate", "Rapid Spawn", "Enemies spawn 50% faster", 9, "spawn", 1.5},
	{"mod_boss_health", "Boss Health Increase", "Bosses gain 100% health", 6, "boss_health", 2.0},
	{"mod_elite_frequency", "Elite Surge", "Elite enemies appear more frequently", 4, "elite", 2.0},
	{"mod_player_regen", "Reduced Regen", "Player regeneration reduced by 50%", 8, "regen", 0.5},
	{"mod_wave_time", "Time Pressure", "Wave time limit reduced by 25%", 10, "time", 0.75},
}

// PvE Reward Tiers
var pveRewardTiers = []struct {
	tierName     string
	minWave      int32
	bossKillsReq int32
	rewardXP     int32
	rewardGP     int32
	rewardItem   string
	description  string
}{
	{"Bronze", 5, 0, 1000, 2000, "", "Complete 5 waves"},
	{"Silver", 10, 1, 2500, 5000, "", "Complete 10 waves with 1 boss kill"},
	{"Gold", 13, 3, 5000, 10000, "", "Complete all waves with 3 boss kills"},
	{"Platinum", 13, 5, 10000, 20000, "havoc_paint_gold", "Perfect run with 5 boss kills"},
	{"Diamond", 13, 7, 20000, 40000, "havoc_emblem_diamond", "Elite performance with 7 boss kills"},
}

// PvE Progress Tracking Functions

type pveProgress struct {
	mode         string
	highestWave  int32
	totalWaves   int32
	bossKills    int32
	totalKills   int32
	bestScore    int32
}

func loadPlayerPvEProgress(playerPID string) []pveProgress {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT mode, highest_wave, total_waves, boss_kills, total_kills, best_score 
		FROM player_pve_progress WHERE user_id=? ORDER BY mode`, playerPID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var progress []pveProgress
	for rows.Next() {
		var p pveProgress
		if err := rows.Scan(&p.mode, &p.highestWave, &p.totalWaves, &p.bossKills, &p.totalKills, &p.bestScore); err != nil {
			continue
		}
		progress = append(progress, p)
	}
	return progress
}

type bossKillRecord struct {
	bossID    string
	killCount int32
	firstKill string
	lastKill  string
}

func loadPlayerBossKills(playerPID string) []bossKillRecord {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT boss_id, kill_count, first_kill, last_kill 
		FROM player_boss_kills WHERE user_id=? ORDER BY kill_count DESC`, playerPID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var kills []bossKillRecord
	for rows.Next() {
		var k bossKillRecord
		var firstKill, lastKill sql.NullString
		if err := rows.Scan(&k.bossID, &k.killCount, &firstKill, &lastKill); err != nil {
			continue
		}
		if firstKill.Valid {
			k.firstKill = firstKill.String
		}
		if lastKill.Valid {
			k.lastKill = lastKill.String
		}
		kills = append(kills, k)
	}
	return kills
}

type aiPreferences struct {
	difficulty     string
	aiBehavior     string
	spawnRate      float32
	bossFrequency  float32
}

func loadPlayerAIPreferences(playerPID string) aiPreferences {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return aiPreferences{
			difficulty:    "Normal",
			aiBehavior:    "Balanced",
			spawnRate:     1.0,
			bossFrequency: 1.0,
		}
	}

	var prefs aiPreferences
	err := db.QueryRow(`SELECT difficulty, ai_behavior, spawn_rate, boss_frequency 
		FROM player_ai_preferences WHERE user_id=?`, playerPID).
		Scan(&prefs.difficulty, &prefs.aiBehavior, &prefs.spawnRate, &prefs.bossFrequency)
	if err != nil {
		return aiPreferences{
			difficulty:    "Normal",
			aiBehavior:    "Balanced",
			spawnRate:     1.0,
			bossFrequency: 1.0,
		}
	}
	return prefs
}

func savePlayerAIPreferences(db *sql.DB, pid string, prefs aiPreferences) {
	_, _ = db.Exec(`INSERT OR REPLACE INTO player_ai_preferences(user_id, difficulty, ai_behavior, spawn_rate, boss_frequency, updated_at) 
		VALUES(?, ?, ?, ?, ?, datetime('now'))`, pid, prefs.difficulty, prefs.aiBehavior, prefs.spawnRate, prefs.bossFrequency)
}

// PvE MMOG Payload Builders

func buildMmogPvEProgressPayload(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetPvEProgress")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	progress := loadPlayerPvEProgress(playerPID)
	b, stack = protocol.AppendArrayStart(b, stack, "PvEProgress")
	for _, p := range progress {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "Mode", p.mode)
		b = protocol.AppendInt32Field(b, "HighestWave", p.highestWave)
		b = protocol.AppendInt32Field(b, "TotalWaves", p.totalWaves)
		b = protocol.AppendInt32Field(b, "BossKills", p.bossKills)
		b = protocol.AppendInt32Field(b, "TotalKills", p.totalKills)
		b = protocol.AppendInt32Field(b, "BestScore", p.bestScore)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogBossKillsPayload(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetBossKills")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	kills := loadPlayerBossKills(playerPID)
	b, stack = protocol.AppendArrayStart(b, stack, "BossKills")
	for _, k := range kills {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "BossID", k.bossID)
		b = protocol.AppendInt32Field(b, "KillCount", k.killCount)
		if k.firstKill != "" {
			b = protocol.AppendStringField(b, "FirstKill", k.firstKill)
		}
		if k.lastKill != "" {
			b = protocol.AppendStringField(b, "LastKill", k.lastKill)
		}
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogAIPreferencesPayload(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetAIPreferences")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	prefs := loadPlayerAIPreferences(playerPID)
	b = protocol.AppendStringField(b, "Difficulty", prefs.difficulty)
	b = protocol.AppendStringField(b, "AIBehavior", prefs.aiBehavior)
	b = protocol.AppendStringField(b, "SpawnRate", fmt.Sprintf("%.2f", prefs.spawnRate))
	b = protocol.AppendStringField(b, "BossFrequency", fmt.Sprintf("%.2f", prefs.bossFrequency))

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogSetAIPreferencesPayload(playerPID string, payload []byte) []byte {
	difficulty := protocol.ExtractStringField(payload, "Difficulty")
	aiBehavior := protocol.ExtractStringField(payload, "AIBehavior")
	spawnRateStr := protocol.ExtractStringField(payload, "SpawnRate")
	bossFreqStr := protocol.ExtractStringField(payload, "BossFrequency")

	// Parse float values
	spawnRate := float32(1.0)
	if spawnRateStr != "" {
		if val, err := strconv.ParseFloat(spawnRateStr, 32); err == nil {
			spawnRate = float32(val)
		}
	}
	bossFreq := float32(1.0)
	if bossFreqStr != "" {
		if val, err := strconv.ParseFloat(bossFreqStr, 32); err == nil {
			bossFreq = float32(val)
		}
	}

	// Validate difficulty
	validDifficulty := false
	for _, level := range aiDifficultyLevels {
		if level.name == difficulty {
			validDifficulty = true
			break
		}
	}
	if !validDifficulty {
		difficulty = "Normal"
	}

	prefs := aiPreferences{
		difficulty:    difficulty,
		aiBehavior:    aiBehavior,
		spawnRate:     spawnRate,
		bossFrequency: bossFreq,
	}

	db := currentMmogPlayerStateDB()
	if db != nil {
		savePlayerAIPreferences(db, playerPID, prefs)
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_SetAIPreferences")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogHavocWavesPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetHavocWaves")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	b, stack = protocol.AppendArrayStart(b, stack, "Waves")
	for _, wave := range havocWaveConfig {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "WaveNumber", wave.waveNumber)
		b = protocol.AppendInt32Field(b, "EnemyCount", wave.enemyCount)
		b = protocol.AppendInt32Field(b, "EliteCount", wave.eliteCount)
		b = protocol.AppendBoolField(b, "BossWave", wave.bossWave)
		if wave.bossID != "" {
			b = protocol.AppendStringField(b, "BossID", wave.bossID)
		}
		b = protocol.AppendInt32Field(b, "TimeLimit", wave.timeLimit)
		b = protocol.AppendInt32Field(b, "RewardXP", wave.rewardXP)
		b = protocol.AppendInt32Field(b, "RewardGP", wave.rewardGP)
		b = protocol.AppendStringField(b, "Description", wave.description)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogBossTypesPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetBossTypes")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	b, stack = protocol.AppendArrayStart(b, stack, "BossTypes")
	for _, boss := range bossTypes {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "BossID", boss.bossID)
		b = protocol.AppendStringField(b, "Name", boss.name)
		b = protocol.AppendStringField(b, "ShipClass", boss.shipClass)
		b = protocol.AppendInt32Field(b, "PhaseCount", boss.phaseCount)
		b = protocol.AppendInt32Field(b, "Difficulty", boss.difficulty)
		b = protocol.AppendInt32Field(b, "RewardXP", boss.rewardXP)
		b = protocol.AppendInt32Field(b, "RewardGP", boss.rewardGP)
		b = protocol.AppendStringField(b, "Description", boss.description)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogAIDifficultyLevelsPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetAIDifficultyLevels")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	b, stack = protocol.AppendArrayStart(b, stack, "DifficultyLevels")
	for _, level := range aiDifficultyLevels {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "Name", level.name)
		b = protocol.AppendInt32Field(b, "DifficultyID", level.difficultyID)
		b = protocol.AppendStringField(b, "SpawnRate", fmt.Sprintf("%.2f", level.spawnRate))
		b = protocol.AppendStringField(b, "HealthMult", fmt.Sprintf("%.2f", level.healthMult))
		b = protocol.AppendStringField(b, "DamageMult", fmt.Sprintf("%.2f", level.damageMult))
		b = protocol.AppendStringField(b, "XPMult", fmt.Sprintf("%.2f", level.xpMult))
		b = protocol.AppendStringField(b, "CreditMult", fmt.Sprintf("%.2f", level.creditMult))
		b = protocol.AppendStringField(b, "Description", level.description)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogHavocModifiersPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetHavocModifiers")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	b, stack = protocol.AppendArrayStart(b, stack, "Modifiers")
	for _, mod := range havocModifiers {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "ModifierID", mod.modifierID)
		b = protocol.AppendStringField(b, "Name", mod.name)
		b = protocol.AppendStringField(b, "Description", mod.description)
		b = protocol.AppendInt32Field(b, "WaveStart", mod.waveStart)
		b = protocol.AppendStringField(b, "EffectType", mod.effectType)
		b = protocol.AppendStringField(b, "EffectValue", fmt.Sprintf("%.2f", mod.effectValue))
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogPvERewardTiersPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetPvERewardTiers")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")

	b, stack = protocol.AppendArrayStart(b, stack, "RewardTiers")
	for _, tier := range pveRewardTiers {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "TierName", tier.tierName)
		b = protocol.AppendInt32Field(b, "MinWave", tier.minWave)
		b = protocol.AppendInt32Field(b, "BossKillsReq", tier.bossKillsReq)
		b = protocol.AppendInt32Field(b, "RewardXP", tier.rewardXP)
		b = protocol.AppendInt32Field(b, "RewardGP", tier.rewardGP)
		if tier.rewardItem != "" {
			b = protocol.AppendStringField(b, "RewardItem", tier.rewardItem)
		}
		b = protocol.AppendStringField(b, "Description", tier.description)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogPlayerScoresPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerScores")
	b = protocol.AppendStringField(b, "modename", "TeamElimination")
	b = protocol.AppendInt32Field(b, "fleettier", 1)
	b = protocol.AppendStringField(b, "timespan", "alltime")
	b = protocol.AppendBoolField(b, "prevweek", false)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "leaderboard")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "playerrank")
	b = protocol.AppendStringField(b, "UserName", "Local")
	b = protocol.AppendInt32Field(b, "Rank", 0)
	b = protocol.AppendInt32Field(b, "Score", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogFleetEligibilityPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_FleetEligibility")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, stack = protocol.AppendArrayStart(b, stack, "fleet_eligibility")
	for _, eligibility := range configBackedFleetEligibilities() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "FleetType", eligibility.FleetType)
		b = protocol.AppendBoolField(b, "Eligible", true)
		b = protocol.AppendBoolField(b, "isEligible", true)
		b = protocol.AppendStringField(b, "Reason", "")
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogTunePayload() []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_Tune")
	b, stack = protocol.AppendObjectStart(b, stack, "Returning")
	b, stack = protocol.AppendObjectStart(b, stack, "MetaData")
	b = protocol.AppendStringField(b, "Version", "1.0.0")
	b, stack = protocol.AppendObjectEnd(b, stack)
	for _, section := range []string{
		"WeaponsTune",
		"BattleReadyTune",
		"ProjectilesTune",
		"AbilitiesTune",
		"OfficersTune",
		"FeatsTune",
		"HavocTune",
		"GameModifiersTune",
	} {
		b, stack = protocol.AppendObjectStart(b, stack, section)
		b, stack = protocol.AppendObjectStart(b, stack, "rows")
		b, stack = protocol.AppendObjectEnd(b, stack)
		b = protocol.AppendInt32Field(b, "row_count", 0)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogPlayerStatisticsPayload() []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerStatistics")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, stack = protocol.AppendArrayStart(b, stack, "stats")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogUserOnlinePayload() []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_UserOnline")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogConnectPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_Connect")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendStringField(b, "PID", playerPID)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogAnalyticsBeginTransactionPayload(transactionID string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_AnalyticsBeginTransaction")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendStringField(b, "transactionId", transactionID)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogCheckReturnPayload() []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_CheckReturn")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "status", "ok")
	b = protocol.AppendBoolField(b, "CanReturnToMatch", false)
	b = protocol.AppendBoolField(b, "canReturnToMatch", false)
	b = protocol.AppendBoolField(b, "ReturnValue", false)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogShipBonusesPayload(requestName string, playerPID string, payload []byte) []byte {
	shipID := protocol.FirstInt32Field(payload, 0, "shipID", "ShipID", "shipId")
	if shipID == 0 {
		shipID = mmogPlayerStateForPID(playerPID).activeFleet().flagshipShipID
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "shipID", shipID)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipBonuses")
	b, stack = appendMmogShipBonusEntry(b, stack, "Health", 0)
	b, stack = appendMmogShipBonusEntry(b, stack, "Damage", 0)
	b, stack = appendMmogShipBonusEntry(b, stack, "Speed", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "shipBonuses")
	b, stack = appendMmogShipBonusEntry(b, stack, "Health", 0)
	b, stack = appendMmogShipBonusEntry(b, stack, "Damage", 0)
	b, stack = appendMmogShipBonusEntry(b, stack, "Speed", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogShipBonusEntry(b []byte, stack []int, name string, value int32) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "Name", name)
	b = protocol.AppendStringField(b, "name", name)
	b = protocol.AppendInt32Field(b, "Value", value)
	b = protocol.AppendInt32Field(b, "value", value)
	b = protocol.AppendBoolField(b, "IsPercent", false)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayersInformationPayload(playerPID string, payload []byte) []byte {
	var b []byte
	var stack []int
	playerIDs := requestedMmogPlayerInfoIDs(playerPID, payload)

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayersInformation")
	b = protocol.AppendStringField(b, "result", "ok")
	b, stack = protocol.AppendArrayStart(b, stack, "infos")
	for _, pid := range playerIDs {
		state := mmogPlayerStateForPID(pid)
		b, stack = appendMmogPlayerDisplayInfoEntry(b, stack, pid, state)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func requestedMmogPlayerInfoIDs(playerPID string, payload []byte) []string {
	ids := protocol.ExtractStringFields(payload, "ID", "PID", "PlayerID", "playerID", "")
	if len(ids) == 0 {
		ids = []string{playerPID}
	}
	requesterPID := normalizedPlayerStatePID(playerPID)
	seen := map[string]struct{}{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		pid := protocol.NormalizePlayerPID(id)
		if pid == "" {
			pid = requesterPID
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		result = append(result, pid)
	}
	return result
}

func appendMmogPlayerDisplayInfoEntry(b []byte, stack []int, playerPID string, state mmogPlayerState) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "ID", normalizedPlayerStatePID(playerPID))
	b = protocol.AppendStringField(b, "DisplayInfo", state.displayInfo)
	b = protocol.AppendInt32Field(b, "Rank", state.currentRank)
	b = protocol.AppendInt32Field(b, "UnlockedFleetType", 1)
	b = protocol.AppendBoolField(b, "Elite", false)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// --- Market / Purchases ---

var catalogPrices = map[int32]int32{
	// Ships
	extractedShipIDValcour: 5000,
	extractedShipIDLeipzig: 5000,
	extractedShipIDTrieste: 5000,
	extractedShipIDCeres:   5000,
	// Weapons
	100597772: 2000, // Repeater Turrets
	100598563: 2000, // Laser Turrets
	100598595: 2500, // Plasma Cannon
	100598596: 2500, // Rail Gun
	100597987: 3000, // Artillery Cannon
	100598570: 3000, // Howitzer
	100597870: 3500, // Missile Launcher
	100598573: 3500, // Torpedo Launcher
	// Abilities
	100597788: 1500, // Repair Beam
	100598590: 1500, // Shield Boost
	100597790: 2000, // EMP Blast
	100598592: 2000, // Turbo Boost
	// Perks
	100597776: 1000, // Armor Plating
	100598567: 1000, // Energy Shield
	100597778: 1200, // Targeting Computer
	100598569: 1200, // Engine Upgrade
}

// Daily contract seeds
var dailyContractSeeds = []struct {
	id, name, description    string
	targetKills, targetScore int32
	rewardXP, rewardGP       int32
}{
	{"contract_kills_5", "Get 5 Kills", "Eliminate 5 enemy ships", 5, 0, 200, 400},
	{"contract_kills_10", "Get 10 Kills", "Eliminate 10 enemy ships", 10, 0, 500, 1000},
	{"contract_wins_1", "Win a Match", "Win 1 match", 0, 0, 300, 600},
	{"contract_score_500", "Score 500 Points", "Earn 500 score in matches", 0, 500, 250, 500},
}

func buildMmogPurchasePayload(requestName string, playerPID string, payload []byte) []byte {
	itemID := protocol.FirstInt32Field(payload, 0, "ItemID", "itemID", "itemId", "ItemId")
	if itemID == 0 {
		itemID = itemIDFromPurchaseOffer(protocol.FirstNonEmptyString(payload, "offer", "Offer", "sku", "Sku", "external_id", "ExternalID"))
	}
	if itemID == 0 {
		return buildMmogErrorPayload(requestName, "missing ItemID for purchase")
	}

	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}

	quantity := protocol.FirstInt32Field(payload, 1, "quantity", "Quantity")
	if quantity <= 0 {
		quantity = 1
	}
	price, ok := catalogPrices[itemID]
	if !ok {
		price = 1000
	}
	price *= quantity

	var owned int
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_id=?`, pid, itemID).Scan(&owned)
	if owned > 0 {
		return buildMmogErrorPayload(requestName, "item already owned")
	}

	var softCurrency, premiumCurrency int32
	_ = database.QueryRow(`SELECT soft_currency, premium_currency FROM player_state WHERE user_id=?`, pid).
		Scan(&softCurrency, &premiumCurrency)

	if softCurrency < price {
		return buildMmogErrorPayload(requestName, "insufficient credits")
	}

	if _, err := database.Exec(`UPDATE player_state SET soft_currency=soft_currency-?, updated_at=datetime('now') WHERE user_id=?`, price, pid); err != nil {
		return buildMmogErrorPayload(requestName, "currency deduction failed")
	}
	itemType := "ship"
	currency := protocol.FirstNonEmptyString(payload, "currency", "Currency")
	if currency == "" {
		currency = "gp"
	}
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,?,?,?)`, pid, itemID, itemType, price, currency); err != nil {
		return buildMmogErrorPayload(requestName, "purchase record failed")
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "itemID", itemID)
	b = protocol.AppendInt32Field(b, "quantity", quantity)
	b = protocol.AppendInt32Field(b, "pricePaid", price)
	b = protocol.AppendStringField(b, "currency", currency)
	b = protocol.AppendInt32Field(b, "softCurrency", softCurrency-price)
	b = protocol.AppendInt32Field(b, "premiumCurrency", premiumCurrency)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func itemIDFromPurchaseOffer(offer string) int32 {
	if offer == "" {
		return 0
	}
	for _, ship := range allT1Ships() {
		if offer == extractedMarketItemExternalID(ship.id, "") || offer == strconv.FormatInt(int64(ship.id), 10) {
			return ship.id
		}
	}
	for _, item := range starterOwnedInventorySeeds() {
		if offer == item.externalID || offer == extractedMarketItemExternalID(item.itemID, "") || offer == strconv.FormatInt(int64(item.itemID), 10) {
			return item.itemID
		}
	}
	return 0
}

func buildMmogElitePurchasePayload(requestName string, playerPID string, payload []byte) []byte {
	durationDays := protocol.FirstInt32Field(payload, 30, "Duration", "duration", "Days", "days")
	if durationDays <= 0 {
		durationDays = 30
	}
	price := durationDays * 50

	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}

	var premiumCurrency int32
	_ = database.QueryRow(`SELECT premium_currency FROM player_state WHERE user_id=?`, pid).Scan(&premiumCurrency)
	if premiumCurrency < price {
		return buildMmogErrorPayload(requestName, "insufficient elite currency")
	}

	_, _ = database.Exec(`UPDATE player_state SET premium_currency=premium_currency-?, updated_at=datetime('now') WHERE user_id=?`, price, pid)

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "eliteDays", durationDays)
	b = protocol.AppendInt32Field(b, "premiumCurrency", premiumCurrency-price)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogXPConversionPayload(requestName string, playerPID string, payload []byte) []byte {
	xpAmount := protocol.FirstInt32Field(payload, 0, "XPAmount", "xpAmount", "xp", "XP")
	if xpAmount <= 0 {
		return buildMmogErrorPayload(requestName, "invalid XP amount")
	}

	convertTo := protocol.FirstNonEmptyString(payload, "convertTo", "ConvertTo", "currency", "Currency")
	if convertTo == "" {
		convertTo = "credits"
	}

	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}

	var creditsGained, premiumCreditsGained int32
	var success bool

	if convertTo == "premium" || convertTo == "elite" {
		premiumCreditsGained, success = convertXPToPremiumCredits(database, pid, xpAmount)
		if !success {
			return buildMmogErrorPayload(requestName, "XP conversion failed")
		}
	} else {
		creditsGained, success = convertXPToCredits(database, pid, xpAmount)
		if !success {
			return buildMmogErrorPayload(requestName, "XP conversion failed")
		}
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "xpConverted", xpAmount)
	b = protocol.AppendInt32Field(b, "creditsGained", creditsGained)
	b = protocol.AppendInt32Field(b, "premiumCreditsGained", premiumCreditsGained)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogContractCompletionPayload(requestName string, playerPID string, payload []byte) []byte {
	contractID := protocol.FirstNonEmptyString(payload, "ContractID", "contractID", "contract_id", "id")
	if contractID == "" {
		return buildMmogErrorPayload(requestName, "missing contract ID")
	}

	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}

	rewardXP, rewardGP, success := completeContract(database, pid, contractID)
	if !success {
		return buildMmogErrorPayload(requestName, "contract completion failed")
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendStringField(b, "contractID", contractID)
	b = protocol.AppendInt32Field(b, "rewardXP", rewardXP)
	b = protocol.AppendInt32Field(b, "rewardGP", rewardGP)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func buildMmogContractRerollPayload(requestName string, playerPID string, payload []byte) []byte {
	contractID := protocol.FirstNonEmptyString(payload, "ContractID", "contractID", "contract_id", "id")
	if contractID == "" {
		return buildMmogErrorPayload(requestName, "missing contract ID")
	}

	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}

	// Reroll costs 100 credits
	rerollCost := int32(100)
	var softCurrency int32
	_ = database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&softCurrency)
	if softCurrency < rerollCost {
		return buildMmogErrorPayload(requestName, "insufficient credits for reroll")
	}

	// Deduct reroll cost
	_, _ = database.Exec(`UPDATE player_state SET soft_currency=soft_currency-?, updated_at=datetime('now') WHERE user_id=?`, rerollCost, pid)

	// Mark old contract as rerolled
	_, _ = database.Exec(`UPDATE player_contracts SET state='rerolled', updated_at=datetime('now') WHERE user_id=? AND contract_id=?`, pid, contractID)

	// Seed new contracts
	seedDailyContractsForPlayer(database, pid)

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendStringField(b, "contractID", contractID)
	b = protocol.AppendInt32Field(b, "rerollCost", rerollCost)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// Contract and XP conversion functions (moved from handlers package)

func seedDailyContractsForPlayer(db *sql.DB, pid string) {
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM player_contracts WHERE user_id=? AND state='active'`, pid).Scan(&count)
	if count >= 3 {
		return
	}
	for i := 0; i < 3 && i < len(dailyContractSeeds); i++ {
		seed := dailyContractSeeds[i]
		payload, _ := json.Marshal(map[string]interface{}{
			"id":          seed.id,
			"name":        seed.name,
			"description": seed.description,
			"targetKills": seed.targetKills,
			"targetScore": seed.targetScore,
			"rewardXP":    seed.rewardXP,
			"rewardGP":    seed.rewardGP,
		})
		_, _ = db.Exec(`INSERT OR IGNORE INTO player_contracts(user_id,contract_id,state,progress,payload) VALUES(?,?,'active',0,?)`, pid, seed.id, string(payload))
	}
}

func completeContract(db *sql.DB, pid, contractID string) (rewardXP, rewardGP int32, success bool) {
	// Get contract details
	var payload string
	err := db.QueryRow(`SELECT payload FROM player_contracts WHERE user_id=? AND contract_id=? AND state='active'`, pid, contractID).Scan(&payload)
	if err != nil {
		return 0, 0, false
	}

	// Parse payload to get rewards
	var contractData struct {
		RewardXP int32 `json:"rewardXP"`
		RewardGP int32 `json:"rewardGP"`
	}
	if err := json.Unmarshal([]byte(payload), &contractData); err != nil {
		return 0, 0, false
	}

	// Mark contract as completed
	_, err = db.Exec(`UPDATE player_contracts SET state='completed', progress=100, completed_at=datetime('now'), updated_at=datetime('now') WHERE user_id=? AND contract_id=?`, pid, contractID)
	if err != nil {
		return 0, 0, false
	}

	// Award rewards
	if contractData.RewardXP > 0 {
		_, _ = db.Exec(`UPDATE player_state SET current_xp=current_xp+?, updated_at=datetime('now') WHERE user_id=?`, contractData.RewardXP, pid)
	}
	if contractData.RewardGP > 0 {
		_, _ = db.Exec(`UPDATE player_state SET soft_currency=soft_currency+?, updated_at=datetime('now') WHERE user_id=?`, contractData.RewardGP, pid)
	}

	// Seed new contract to replace completed one
	seedDailyContractsForPlayer(db, pid)

	return contractData.RewardXP, contractData.RewardGP, true
}

func convertXPToCredits(db *sql.DB, pid string, xpAmount int32) (creditsGained int32, success bool) {
	if xpAmount <= 0 {
		return 0, false
	}

	// Check if player has enough free XP
	var freeXP int32
	err := db.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP)
	if err != nil || freeXP < xpAmount {
		return 0, false
	}

	// Calculate credits (10 XP = 1 credit)
	creditsGained = xpAmount / 10
	if creditsGained <= 0 {
		return 0, false
	}

	// Deduct XP and add credits
	_, err = db.Exec(`UPDATE player_state SET free_xp=free_xp-?, soft_currency=soft_currency+?, updated_at=datetime('now') WHERE user_id=?`, xpAmount, creditsGained, pid)
	if err != nil {
		return 0, false
	}

	return creditsGained, true
}

func convertXPToPremiumCredits(db *sql.DB, pid string, xpAmount int32) (premiumCreditsGained int32, success bool) {
	if xpAmount <= 0 {
		return 0, false
	}

	// Check if player has enough free XP
	var freeXP int32
	err := db.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP)
	if err != nil || freeXP < xpAmount {
		return 0, false
	}

	// Calculate premium credits (100 XP = 1 premium credit)
	premiumCreditsGained = xpAmount / 100
	if premiumCreditsGained <= 0 {
		return 0, false
	}

	// Deduct XP and add premium credits
	_, err = db.Exec(`UPDATE player_state SET free_xp=free_xp-?, premium_currency=premium_currency+?, updated_at=datetime('now') WHERE user_id=?`, xpAmount, premiumCreditsGained, pid)
	if err != nil {
		return 0, false
	}

	return premiumCreditsGained, true
}

func dailyContractState(pid string) int {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return 0
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM player_contracts WHERE user_id=? AND state='active'`, pid).Scan(&count)
	if count > 0 {
		return count
	}
	return 0
}

// ribbonThresholds defines the 12 ribbon types and their unlock conditions
var ribbonThresholds = map[string]struct {
	name      string
	minKills  int32
	minDeaths int32
}{
	"combat_efficiency": {"Combat Efficiency", 3, 0},
	"kill_streak":       {"Kill Streak", 5, 0},
	"unstoppable":       {"Unstoppable", 10, 0},
	"survivor":          {"Survivor", 0, 0},
	"first_blood":       {"First Blood", 1, 0},
	"avenger":           {"Avenger", 1, 1},
	"team_player":       {"Team Player", 2, 0},
	"marksman":          {"Marksman", 4, 0},
	"close_quarters":    {"Close Quarters", 3, 0},
	"support_star":      {"Support Star", 1, 0},
	"defender":          {"Defender", 2, 0},
	"berserker":         {"Berserker", 6, 0},
}

type playerRibbon struct {
	ribbonType string
	count      int32
}

func loadPlayerRibbons(playerPID string) []playerRibbon {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT ribbon_type, count FROM player_ribbons WHERE user_id=? AND count > 0 ORDER BY ribbon_type`, playerPID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var ribbons []playerRibbon
	for rows.Next() {
		var r playerRibbon
		if err := rows.Scan(&r.ribbonType, &r.count); err != nil {
			continue
		}
		ribbons = append(ribbons, r)
	}
	return ribbons
}

func appendMmogRibbonEntry(b []byte, stack []int, ribbon playerRibbon) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "Type", ribbon.ribbonType)
	b = protocol.AppendInt32Field(b, "Count", ribbon.count)
	if info, ok := ribbonThresholds[ribbon.ribbonType]; ok {
		b = protocol.AppendStringField(b, "Name", info.name)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogRibbonsPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_GetRibbons")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "Ribbons")
	for _, ribbon := range loadPlayerRibbons(playerPID) {
		b, stack = appendMmogRibbonEntry(b, stack, ribbon)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}
