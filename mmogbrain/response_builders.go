package main

import (
	"database/sql"
	"strconv"
	"time"

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
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "FID", fleet.token)
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendStringField(b, "FleetID", fleet.token)
	b = protocol.AppendStringField(b, "Name", fleet.displayName)
	b = protocol.AppendInt32Field(b, "Type", fleet.fleetType)
	b = protocol.AppendBoolField(b, "Unlocked", fleet.active || len(fleet.shipLoadouts) > 0)
	b = protocol.AppendInt32Field(b, "FleetType", fleet.fleetType)
	b = protocol.AppendInt32Field(b, "shipCount", int32(len(fleet.shipLoadouts)))
	b = appendMmogFleetRuntimeFields(b, fleet)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b = protocol.AppendInt32Field(b, "flagshipShipId", fleet.flagshipShipID)
	b = protocol.AppendInt32Field(b, "flagshipLoadoutID", fleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "flagshipLoadoutIndex", fleet.flagshipLoadoutIndex)
	b = protocol.AppendInt32Field(b, "flagshipID", fleet.flagshipLoadoutID)
	b, stack = appendMmogFleetBackendFields(b, stack, fleet)
	b = protocol.AppendBoolField(b, "bIsActive", fleet.active)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogFleetUnlockEntry(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendInt32Field(b, "Type", fleet.fleetType)
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
		fleets = state.activeFleets()
	}

	b = protocol.AppendStringField(b, "RT", "YA_PlayerFleets")
	b = protocol.AppendStringField(b, "FID", "PlayerFleets")
	b = protocol.AppendStringField(b, "PID", normalizedPlayerStatePID(playerPID))
	b = protocol.AppendStringField(b, "Name", "PlayerFleets")
	b = protocol.AppendInt32Field(b, "PlayedMatches", 0)
	b, stack = protocol.AppendArrayStart(b, stack, "Fleets")
	for _, fleet := range fleets {
		b, stack = appendMmogFleetUnlockEntry(b, stack, fleet)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "result")
	for _, fleet := range fleets {
		b, stack = appendMmogPlayerFleetEntry(b, stack, playerPID, fleet)
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
	b = protocol.AppendInt32Field(b, "ShipID", fleetShipID)
	b = protocol.AppendInt32Field(b, "shipID", fleetShipID)
	b = protocol.AppendInt32Field(b, "LoadoutID", loadoutID)
	b = protocol.AppendInt32Field(b, "loadoutID", loadoutID)
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
	var b []byte
	var stack []int // Track nesting for objects/arrays

	// Add routing tag (command name)
	b = protocol.AppendStringField(b, "RT", "YA_GetSeasonProgress")

	// Start the "result" object
	b, stack = protocol.AppendObjectStart(b, stack, "result")

	// Add empty arrays for season data
	b, stack = protocol.AppendArrayStart(b, stack, "EventScores")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "EventRewards")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "SeasonRewards")
	b, stack = protocol.AppendObjectEnd(b, stack)

	// Close the "result" object
	b, stack = protocol.AppendObjectEnd(b, stack)

	// Validate stack is empty (all objects/arrays closed)
	if len(stack) != 0 {
		return nil // or panic("Unbalanced MMoG payload")
	}

	return b
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
	b = protocol.AppendStringField(b, "fleetId", starterFleet.token)
	b = protocol.AppendInt32Field(b, "fleet id", starterFleet.fleetID)
	b = protocol.AppendInt32Field(b, "FleetType", starterFleet.fleetType)
	b = protocol.AppendInt32Field(b, "shipId", starterFleet.flagshipShipID)
	b, stack = protocol.AppendInt32ArrayField(b, stack, "shipIds", starterFleet.shipIDs())
	b, stack = protocol.AppendBoolArrayField(b, stack, "ShipTechTreeComplete", starterFleet.shipTechTreeComplete())
	b = protocol.AppendInt32Field(b, "FlagShipID", starterFleet.flagshipShipID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutID", starterFleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "FlagShipLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b = protocol.AppendInt32Field(b, "selectedLoadoutID", starterFleet.flagshipLoadoutID)
	b = protocol.AppendInt32Field(b, "selectedLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b = protocol.AppendInt32Field(b, "flagshipID", starterFleet.flagshipLoadoutID)
	b, stack = appendMmogFleetBackendFields(b, stack, starterFleet)
	b, stack = protocol.AppendArrayStart(b, stack, "FactionReputation")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Officers")
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "ShipLoadouts")
	for _, loadout := range state.shipLoadouts() {
		b, stack = appendMmogShipLoadout(b, stack, playerPID, loadout)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "Ribbons")
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
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_perkIDs", loadout.perkItemIDs())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_abilityItemIds", loadout.abilityItemIDs())
	b, stack = protocol.AppendInt32ArrayField(b, stack, "m_perkIds", loadout.perkItemIDs())
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
	b = protocol.AppendInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = protocol.AppendInt32Field(b, "loadoutID", loadout.loadoutID())
	b = protocol.AppendInt32Field(b, "m_loadoutID", loadout.loadoutID())
	b = protocol.AppendInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "shipID", loadout.effectiveFleetShipID())
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
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetDailyContractsData")
	b = protocol.AppendInt32Field(b, "DailyContractStateID", 0)
	b = protocol.AppendInt32Field(b, "LastContractsAssignment", int32(time.Now().Unix()))
	b = protocol.AppendInt32Field(b, "DailyContractLastReplaceTime", int32(time.Now().Unix()))
	b, stack = protocol.AppendArrayStart(b, stack, "Quests")
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
	extractedShipIDValcour: 5000,
	extractedShipIDLeipzig: 5000,
	extractedShipIDTrieste: 5000,
	extractedShipIDCeres:   5000,
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
