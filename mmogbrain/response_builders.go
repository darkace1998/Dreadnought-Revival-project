package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
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
	// NOTE (issue #50): the client's YA_UserLogin "ok" handler (FUN_142a3af90)
	// reads result.LoginStreak.loginstreak, and only when loginstreak > 0
	// also LoginStreak.credits/freexp/gp (that day's streak-bonus reward) —
	// it does not appear to read the flat result.credits/etc above. Left
	// unchanged pending further verification: TestUserLoginPayloadKeepsEconomyFieldsOnResult
	// asserts today's top-level placement deliberately, and there's no
	// persisted daily-login-streak tracking yet to populate the nested
	// fields with anyway, so nesting them now would have no observable
	// effect either way.
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

	// Client's matchmaking request builder (FUN_142a196e0) sends the mode
	// selection as "GameType" (single mode) or "GameTypes" (multi-mode) —
	// confirmed via decompile, neither of which was previously checked here.
	gameMode := protocol.FirstNonEmptyString(payload, "GameType", "GameTypes", "GameMode", "gameMode", "Mode", "mode", "matchmaking")
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
	// Capitalized "Messages" is unread by the real client parser (confirmed
	// via decompile) but kept as a harmless empty array rather than removed,
	// since removing it isn't required for the fix below and other code may
	// depend on its presence.
	b, stack = protocol.AppendArrayStart(b, stack, "Messages")
	b, stack = protocol.AppendObjectEnd(b, stack)
	// Real client parser (YMmogChat.cpp / FUN_142a21cf0) reads the lowercase
	// "messages" array with per-entry sender/recpt/type/subtype/text/duration
	// — confirmed field names via decompile, so this is a pure data gap, not
	// a wire-format bug. type/subtype/duration go through the same
	// int32-blind scalar union documented elsewhere in this file, so send
	// them as numeric strings. recpt has no per-message recipient concept in
	// this schema (channel/broadcast chat only) — sent empty. type/subtype/
	// duration default to "0" (best-effort "normal message" values; not
	// independently confirmed against any real client-sent example).
	b, stack = protocol.AppendArrayStart(b, stack, "messages")
	for _, msg := range recentMmogChatMessages(channel, 50) {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "sender", msg.senderID)
		b = protocol.AppendStringField(b, "recpt", "")
		b = protocol.AppendStringField(b, "type", "0")
		b = protocol.AppendStringField(b, "subtype", "0")
		b = protocol.AppendStringField(b, "text", msg.content)
		b = protocol.AppendStringField(b, "duration", "0")
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
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

type mmogChatMessage struct {
	senderID string
	content  string
}

// recentMmogChatMessages returns up to limit most-recent messages for a
// channel, oldest first (chronological display order).
func recentMmogChatMessages(channel string, limit int) []mmogChatMessage {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	rows, err := database.Query(`SELECT sender_id, content FROM chat_messages WHERE channel=? ORDER BY sent_at DESC, id DESC LIMIT ?`, channel, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var messages []mmogChatMessage
	for rows.Next() {
		var msg mmogChatMessage
		if err := rows.Scan(&msg.senderID, &msg.content); err != nil {
			continue
		}
		messages = append(messages, msg)
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages
}

// --- Fleet serialization ---

// int32SliceToStrings converts each value to its decimal string form. The
// client's "Fleets" array entry parser (FUN_142a77910 in the decompile) only
// recognizes wire types double/int64/string for its numeric fields — plain
// int32 (wire tag 0x56) silently falls through to a default of 0 for every
// such field, per commit 731a3f3 (which fixed this for Type/Name only). The
// client converts numeric strings back to an integer via _wtoi, so this is
// the correct wire representation for every affected field, not just those
// two.
func int32SliceToStrings(values []int32) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.Itoa(int(v))
	}
	return out
}

func appendMmogFleetRawFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b = protocol.AppendInt32Field(b, "fleet id", fleet.fleetID)
	// FleetType, shipIds, FlagShipID, FlagShipLoadoutID/Index: see
	// int32SliceToStrings doc comment — these are read by the same
	// int32-blind parser as Type/Name and must be sent as numeric strings.
	b = protocol.AppendStringField(b, "FleetType", strconv.Itoa(int(fleet.fleetType)))
	b, stack = protocol.AppendStringArrayField(b, stack, "shipIds", int32SliceToStrings(fleet.shipIDs()))
	b, stack = protocol.AppendBoolArrayField(b, stack, "ShipTechTreeComplete", fleet.shipTechTreeComplete())
	b = protocol.AppendStringField(b, "FlagShipID", strconv.Itoa(int(fleet.flagshipShipID)))
	b = protocol.AppendStringField(b, "FlagShipLoadoutID", strconv.Itoa(int(fleet.flagshipLoadoutID)))
	b = protocol.AppendStringField(b, "FlagShipLoadoutIndex", strconv.Itoa(int(fleet.flagshipLoadoutIndex)))
	return b, stack
}

func appendMmogFleetRuntimeFields(b []byte, fleet mmogFleetSeed) []byte {
	// AutoRepair is a genuine bool UPROPERTY client-side and parses
	// correctly as-is. Maintenance is NOT — despite the semantically
	// boolean value, the client reads it through the same int32-blind
	// numeric union as LastWinTime/ChargingBeginTime/ChargingCharges/Rating
	// (see int32SliceToStrings doc comment), so it must go out as a numeric
	// string too, not a bool field.
	b = protocol.AppendBoolField(b, "AutoRepair", false)
	b = protocol.AppendStringField(b, "Maintenance", "0")
	b = protocol.AppendStringField(b, "LastWinTime", "0")
	b = protocol.AppendStringField(b, "ChargingBeginTime", "0")
	b = protocol.AppendStringField(b, "ChargingCharges", "1")
	b = protocol.AppendStringField(b, "Rating", "0")
	return b
}

func appendMmogFleetBackendFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	// This helper is shared by both the YA_PlayerFleets/Fleets-array entry
	// path and the top-level YA_PlayerGet fleet-summary section. Now
	// decompile-confirmed (FUN_14071d4f0): m_fleetId/m_flagshipIndex/
	// m_fleetType/m_loadoutList are native FIntProperty/FArrayProperty
	// UPROPERTYs reflected on a completely separate native class
	// ("YLocalServerPlayerDataInformation"), not fields read by the
	// Fleets-array JSON parser (FUN_142a77910, which doesn't reference any
	// of these names at all) — so they're inert there either way. Genuinely
	// native int32 properties, correctly left as int32.
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
	b = protocol.AppendStringField(b, "shipCount", strconv.Itoa(len(fleet.shipLoadouts)))
	b = appendMmogFleetRuntimeFields(b, fleet)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b = protocol.AppendStringField(b, "flagshipShipId", strconv.Itoa(int(fleet.flagshipShipID)))
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

// buildMmogFleetUpdatePush builds a YA_FleetUpdate push carrying the same
// Fleets array shape as YA_PlayerFleets.
//
// Live debugging (x64dbg, hardware breakpoint on the readiness byte) proved
// UYFleetManager's internal readiness bitmask (this+0x110) is written exactly
// once — during FleetManager::Initialize, right at "Mmog Connection
// Established" — and never written again for the rest of the session, no
// matter how long the client runs. A software breakpoint on
// HandleMmogbrainFleetUpdated (the delegate that's supposed to complete the
// remaining bits) recorded zero hits across an entire session that included a
// full YA_PlayerFleets round trip. That delegate's own "data not ready"
// fallback (FUN_14035a1a0) explicitly re-sends a YA_PlayerFleets *request* —
// strong evidence it's normally satisfied by a server-pushed fleet-update
// notification, not by data embedded in the request/response the client
// already receives. "YA_FleetUpdate" is a distinct RT name (present in the
// client's own string table, and already recognized as an inbound ack case
// in this dispatcher) that is a near-exact name match for
// HandleMmogbrainFleetUpdated. This mirrors the confirmed YA_UpdateGameModes
// fix: push a dedicated message under the RT the client's delegate listens
// for, rather than assuming embedded response data is enough.
func buildMmogFleetUpdatePush(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)
	fleets := state.fleets
	if len(fleets) == 0 {
		fleets = []mmogFleetSeed{starterFleetState()}
	}

	b = protocol.AppendStringField(b, "RT", "YA_FleetUpdate")
	b, stack = protocol.AppendArrayStart(b, stack, "Fleets")
	for _, fleet := range fleets {
		b, stack = appendMmogPlayerFleetEntry(b, stack, playerPID, fleet)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendMmogStaticFleetTypeEntry(b []byte, stack []int, eligibility dreadconfig.FleetEligibility) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	// Scalar int32 fields on this array entry hit the same restrictive
	// double/int64/string-only tagged union documented elsewhere in this
	// file (Fleets, ShipLoadouts, Ribbons, TechTree rows) — convert to
	// numeric strings. FleetRatingMin (below) was already converted.
	b = protocol.AppendStringField(b, "ID", strconv.Itoa(int(eligibility.FleetType)))
	b = protocol.AppendStringField(b, "ShipsToUnlock", strconv.Itoa(int(eligibility.NumShipsToUnlockFleet)))
	b = protocol.AppendStringField(b, "BaseMaintenanceCost", strconv.Itoa(int(eligibility.BaseMaintenanceCost)))
	b = protocol.AppendStringField(b, "FleetRatingMin", strconv.FormatFloat(eligibility.FleetRatingMin, 'f', 1, 64))
	b = protocol.AppendStringField(b, "FleetRatingCost", strconv.Itoa(int(eligibility.FleetRatingCost)))
	b = protocol.AppendStringField(b, "ChargeTime", strconv.Itoa(int(eligibility.MaintenanceTime)))
	b = protocol.AppendStringField(b, "ChargeCost", strconv.Itoa(0))
	b = protocol.AppendStringField(b, "AvailableCharges", strconv.Itoa(1))
	// Confirmed via decompile (FUN_142a78790): Tiers entries are read through
	// the same restrictive type-1/2/3/4-only union as every sibling scalar in
	// this FleetTypes entry (ID/ShipsToUnlock/etc, already sent as numeric
	// strings above) — AppendUnnamedInt32Field's wire tag 0x56 falls through
	// to the union's default and is silently read as 0.
	b, stack = protocol.AppendArrayStart(b, stack, "Tiers")
	for _, tier := range eligibility.AllowedTiers {
		b = protocol.AppendUnnamedStringField(b, strconv.Itoa(int(tier)))
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
	// Scalar fields here are suspected (not decompile-confirmed — see tracking
	// issue #3) to hit the same int32-blind tagged union as every other
	// array-entry struct fixed this session (Fleets/ShipLoadouts/Ribbons/
	// TechTree) — send numeric strings.
	b = protocol.AppendStringField(b, "ShipID", strconv.Itoa(int(fleetShipID)))
	b = protocol.AppendStringField(b, "LoadoutID", strconv.Itoa(int(loadoutID)))
	b = protocol.AppendStringField(b, "Position", strconv.Itoa(int(loadout.position)))
	b = protocol.AppendBoolField(b, "bIsFlagship", fleetShipID == flagshipShipID)
	b = protocol.AppendStringField(b, "Status", strconv.Itoa(0))
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

// mmogCurrentSeasonID must match the "Name" of whichever entry in
// mmogSeasonDataSeasonsJSON has "m_active":true.
const mmogCurrentSeasonID = "PVE_Season1"

const mmogSeasonDataSeasonsJSON = `[{"Name":"PVE_Season1","m_active":true,"m_name":"Miner Inconvenience","m_descShort":"Season 1 short Description","m_descLong":"Season 1 long Description","m_imageLarge":"None","m_imageSmall":"None","m_rewardLevels":[]}]`

const mmogSeasonDataEventsJSON = `[{"Name":"PVE_S1E1","m_name":"Incident Management","m_descShort":"Miner Inconvenience - Incident Management","m_descLong":"Jupiter Arms installations on the surface of Io have been under attack for weeks by raiding parties using hit and run tactics to wear down the corp's spread out defenses. The megacorp is now contacting mercenary captains directly to assist their forces and protect Jupiter Arms assets, hoping to finally put an end to these costly attacks.","m_map":"None","m_mapParameters":"","m_gameMode":"YGMT_HORDE","m_color":{"r":160,"g":144,"b":131,"a":255},"m_imageSmall":"None","m_imageLarge":"None","m_rewardLevels":[],"m_startDate":"2018.05.16-16.00.00","m_endDate":"2018.05.16-16.19.59","m_season":"PVE_Season1"}]`

func buildMmogSeasonDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetSeasonData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "Events", mmogSeasonDataEventsJSON)
	b = protocol.AppendStringField(b, "Seasons", mmogSeasonDataSeasonsJSON)
	// CurrentSeason must reflect the season marked m_active in Seasons —
	// SetActiveEventAndSeason takes an early "clear active season" branch
	// whenever this is empty, overriding the Seasons blob's own m_active
	// flag and hiding season UI even though a season is actually active.
	b = protocol.AppendStringField(b, "CurrentSeason", mmogCurrentSeasonID)
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
	// This entry lives inside YA_PlayerGet's SeasonProgress array, parsed by
	// the same restrictive int32-blind tagged union confirmed for the rest
	// of that payload (Officers, FactionReputation) — send numeric strings.
	b = protocol.AppendStringField(b, "XP", strconv.Itoa(int(progress.xp)))
	b = protocol.AppendStringField(b, "Level", strconv.Itoa(int(progress.level)))
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
	state := mmogPlayerStateForPID(playerPID)
	starterFleet := state.activeFleet()

	b = protocol.AppendStringField(b, "RT", rt)
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendStringField(b, "SID", "local_session")
	// tll/tpl/tc/rep/repXX_X/ReputationGoalID/Membership.ExpireTime/
	// DailyContract*/FreeXp: the client's YA_PlayerGet parser (FUN_142a70da0)
	// reads every one of these through FUN_1402380b0 or FUN_140238000, which
	// only recognize tagged-union type 1/2 (double), 3 (int64), or 4
	// (string-then-_wtoi) — any other tag, including our int32 wire tag
	// (0x56), returns 0 silently. This is the same int32-blindness already
	// confirmed and fixed for fleet/loadout array entries and SeasonProgress;
	// it had never been audited for these top-level PlayerGet scalars before.
	// Send numeric strings so the client's _wtoi path actually parses them.
	// "tc" (account/character creation time) was previously missing entirely
	// — the client reads it unconditionally, so send it even though we don't
	// track real account-creation time yet.
	b = protocol.AppendStringField(b, "tll", "1")
	b = protocol.AppendStringField(b, "tpl", "1")
	b = protocol.AppendStringField(b, "tc", "1")
	b = protocol.AppendInt32Field(b, "gl", state.softCurrency)
	b = protocol.AppendInt32Field(b, "ob", state.premiumCurrency)
	b = protocol.AppendStringField(b, "rep", "0")
	b = protocol.AppendStringField(b, "repDN_L", "0")
	b = protocol.AppendStringField(b, "repDN_M", "0")
	b = protocol.AppendStringField(b, "repDN_H", "0")
	b = protocol.AppendStringField(b, "repAS_L", "0")
	b = protocol.AppendStringField(b, "repAS_M", "0")
	b = protocol.AppendStringField(b, "repAS_H", "0")
	b = protocol.AppendStringField(b, "repSC_L", "0")
	b = protocol.AppendStringField(b, "repSC_M", "0")
	b = protocol.AppendStringField(b, "repSC_H", "0")
	b = protocol.AppendStringField(b, "repSN_L", "0")
	b = protocol.AppendStringField(b, "repSN_M", "0")
	b = protocol.AppendStringField(b, "repSN_H", "0")
	b = protocol.AppendStringField(b, "repSU_L", "0")
	b = protocol.AppendStringField(b, "repSU_M", "0")
	b = protocol.AppendStringField(b, "repSU_H", "0")
	b = protocol.AppendStringField(b, "ReputationGoalID", "0")
	b = protocol.AppendStringField(b, "disp", "")
	b = protocol.AppendStringField(b, "motto", "")
	b = protocol.AppendStringField(b, "SGD", "")
	b = protocol.AppendStringField(b, "SCtA", "")
	b = protocol.AppendStringField(b, "LGVersion", "0")
	b, stack = protocol.AppendObjectStart(b, stack, "Membership")
	// 0 (unset/expired) is correct for players who never bought elite —
	// previously this always reported "active for one more year" regardless
	// of purchase history.
	b = protocol.AppendStringField(b, "ExpireTime", strconv.Itoa(int(membershipExpiresAt(playerPID))))
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "DailyContractStateID", strconv.Itoa(dailyContractState(playerPID)))
	b = protocol.AppendStringField(b, "LastContractsAssignment", strconv.Itoa(int(now)))
	b = protocol.AppendStringField(b, "DailyContractLastReplaceTime", strconv.Itoa(int(now)))
	b = protocol.AppendStringField(b, "FreeXp", strconv.Itoa(int(state.freeXP)))
	b, stack = protocol.AppendArrayStart(b, stack, "ShipXps")
	b, stack = protocol.AppendObjectEnd(b, stack)

	// Add season progress data
	seasonProgress := loadPlayerSeasonProgress(playerPID)
	b, stack = protocol.AppendArrayStart(b, stack, "SeasonProgress")
	for _, progress := range seasonProgress {
		b, stack = appendMmogSeasonProgressEntry(b, stack, progress)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	// ServerTime/ClientTime: same int32-blind parser as tll/tpl/tc above.
	b = protocol.AppendStringField(b, "ServerTime", strconv.Itoa(int(now)))
	b = protocol.AppendStringField(b, "ClientTime", strconv.Itoa(int(now)))
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
	// NOTE: unlike appendMmogFleetRawFields/appendMmogPlayerFleetEntry (the
	// YA_PlayerFleets/Fleets-array entry versions, which the client parses
	// through FUN_142a77910's restrictive int32-blind union and therefore
	// need numeric strings), this top-level "current fleet summary" section
	// embedded directly in YA_PlayerGet's result object is a separate
	// assignment — per existing, deliberate test coverage
	// (TestFleetStateIsConsistentAcrossResponses) confirming at least
	// FlagShipLoadoutIndex here is read correctly as int32. Do not convert
	// these to strings without decompiled confirmation that this top-level
	// section goes through the same restrictive parser as the array entries
	// — it may not.
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
	// The client's per-entry Officers parser (FUN_142a70b10) reads type/disp/rep,
	// not the m_enabling/m_triggers/m_effects DSL fields — those describe the
	// officer's ability, not its roster identity. rep has no server-side data
	// model yet (no per-officer reputation-tier concept exists), so it is sent
	// as 0 until one is added.
	for _, officer := range dreadconfig.AllOfficers() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		// type/rep go through the client's int32-blind parser (FUN_142a70b10,
		// same restriction as tll/tpl/tc/etc) — numeric strings, not int32.
		b = protocol.AppendStringField(b, "type", strconv.Itoa(int(officer.OfficerID)))
		b = protocol.AppendStringField(b, "disp", officer.OfficerName)
		b = protocol.AppendStringField(b, "rep", "0")
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
	// tslm: same int32-blind parser as tll/tpl/tc/ServerTime/ClientTime above.
	b = protocol.AppendStringField(b, "tslm", "0")
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
	// precastLoadout is left as int32 (unread/defaults to 0) deliberately:
	// the client's ShipLoadouts entry parser (FUN_142a6f9f0 in the
	// decompile) treats it as a fallback key onto the SAME struct offset as
	// shipID. Fixing its wire type here too would let a stale loadout id
	// leak into the ship-id slot if it were ever read before shipID; since
	// shipID below is now fixed and always sent after this field, leaving
	// this one broken is the safe choice, not an oversight.
	b = protocol.AppendInt32Field(b, "precastLoadout", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = protocol.AppendBoolField(b, "m_isActiveLoadout", loadout.active)
	b = protocol.AppendStringField(b, "name", loadout.loadoutName)
	b = protocol.AppendStringField(b, "m_loadoutName", loadout.loadoutName)
	// shipID, class, weaponPrimary/Secondary, abilityPrimary/Secondary/
	// Perimeter/Internal, perkCom/Weapon/Navigation/Engineer: read by
	// FUN_142a6f9f0 through the same restrictive double/int64/string-only
	// tagged union as the Fleets-array parser (see int32SliceToStrings' doc
	// comment) — plain int32 silently defaults every one of these to 0.
	b = protocol.AppendStringField(b, "shipID", strconv.Itoa(int(loadout.effectiveFleetShipID())))
	b = protocol.AppendInt32Field(b, "m_shipId", loadout.effectiveFleetShipID())
	b = protocol.AppendStringField(b, "class", strconv.Itoa(int(loadout.ship.shipClass)))
	b = protocol.AppendStringField(b, "m_name", loadout.loadoutName)
	b = protocol.AppendInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = protocol.AppendStringField(b, "displayInfo", loadout.displayInfo())
	b = protocol.AppendStringField(b, "m_displayInfo", loadout.displayInfo())
	b = protocol.AppendInt32Field(b, "m_loadoutTier", 1)
	b = protocol.AppendBoolField(b, "m_loadoutComplete", loadout.complete())
	b = protocol.AppendStringField(b, "weaponPrimary", strconv.Itoa(int(loadout.weaponPrimaryItemID())))
	b = protocol.AppendStringField(b, "weaponSecondary", strconv.Itoa(int(loadout.weaponSecondaryItemID())))
	b = protocol.AppendStringField(b, "abilityPrimary", strconv.Itoa(int(loadout.abilityItemID(0))))
	b = protocol.AppendStringField(b, "abilitySecondary", strconv.Itoa(int(loadout.abilityItemID(1))))
	b = protocol.AppendStringField(b, "abilityPerimeter", strconv.Itoa(int(loadout.abilityItemID(2))))
	b = protocol.AppendStringField(b, "abilityInternal", strconv.Itoa(int(loadout.abilityItemID(3))))
	b = protocol.AppendStringField(b, "perkCom", strconv.Itoa(int(loadout.perkItemID(0))))
	b = protocol.AppendStringField(b, "perkWeapon", strconv.Itoa(int(loadout.perkItemID(1))))
	b = protocol.AppendStringField(b, "perkNavigation", strconv.Itoa(int(loadout.perkItemID(2))))
	b = protocol.AppendStringField(b, "perkEngineer", strconv.Itoa(int(loadout.perkItemID(3))))
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
	// This nested object (embedded in each YA_GetTechTree row) has no
	// alternate plain-cased field to fall back on the way
	// appendMmogShipLoadoutEntry does, so every scalar here must itself use
	// the numeric-string form to survive the same restrictive tagged union
	// (see int32SliceToStrings' doc comment).
	b = protocol.AppendStringField(b, "LoadoutID", strconv.Itoa(int(loadout.loadoutID())))
	b = protocol.AppendStringField(b, "m_loadoutID", strconv.Itoa(int(loadout.loadoutID())))
	b = protocol.AppendStringField(b, "precastLoadoutID", strconv.Itoa(int(loadout.precastLoadoutID)))
	b = protocol.AppendStringField(b, "m_precastLoadoutID", strconv.Itoa(int(loadout.precastLoadoutID)))
	b = protocol.AppendStringField(b, "ShipID", strconv.Itoa(int(loadout.effectiveFleetShipID())))
	b = protocol.AppendStringField(b, "m_shipId", strconv.Itoa(int(loadout.effectiveFleetShipID())))
	b = protocol.AppendStringField(b, "loadoutIndex", strconv.Itoa(int(loadout.loadoutIndex)))
	b = protocol.AppendStringField(b, "m_shipClass", strconv.Itoa(int(loadout.ship.shipClass)))
	b = protocol.AppendStringField(b, "m_displayInfo", loadout.displayInfo())
	b = protocol.AppendStringField(b, "m_loadoutTier", strconv.Itoa(1))
	b = protocol.AppendBoolField(b, "m_loadoutComplete", loadout.complete())
	b = protocol.AppendStringField(b, "m_primaryWeaponItemId", strconv.Itoa(int(loadout.weaponPrimaryItemID())))
	b = protocol.AppendStringField(b, "m_secondaryWeaponItemId", strconv.Itoa(int(loadout.weaponSecondaryItemID())))
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
	ships := realShipsOnly(playerOwnedTechTreeShips(playerPID))

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerProgression")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "PID", playerPID)
	// Every numeric scalar in this family of responses (PlayerGet, Officers,
	// Fleets, TechTree rows) has been independently confirmed to go through
	// the client's int32-blind parser (double/int64/string recognized, int32
	// silently reads as 0) — applying the same fix here on the strength of
	// that established, repeatedly-confirmed pattern.
	b = protocol.AppendStringField(b, "CurrentXP", strconv.Itoa(int(state.currentXP)))
	b = protocol.AppendStringField(b, "CurrentRank", strconv.Itoa(int(state.currentRank)))
	b = protocol.AppendStringField(b, "RankXP", strconv.Itoa(int(state.rankXP)))
	b = protocol.AppendStringField(b, "XPToNextRank", strconv.Itoa(int(handlers.RankXPThreshold(state.currentRank+1))))
	b = protocol.AppendStringField(b, "NumUnlockedShips", strconv.Itoa(countOwnedShips(ships)))
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
	// Same int32-blind parser as the rest of this payload family.
	b = protocol.AppendStringField(b, "shipID", strconv.Itoa(int(ship.id)))
	b = protocol.AppendStringField(b, "xp", "0")
	b = protocol.AppendStringField(b, "tier", "1")
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
	// NodeID, ShipID/m_shipId, ParentID, NodeType, Tier, UnlockCost,
	// PrereqID1/2, ShipClass, Weight, m_currentBaseClass/m_currentShipClass,
	// m_shipTier, m_weight: same restrictive double/int64/string-only tagged
	// union as the Fleets/ShipLoadouts/Ribbons array-entry parsers (see
	// int32SliceToStrings' doc comment) — plain int32 silently defaults each
	// of these to 0, which is why entries never resolve to unlocked ships.
	b = protocol.AppendStringField(b, "NodeID", strconv.Itoa(int(ship.nodeID)))
	// ShipID/shipID and m_shipID/m_shipId each collide case-insensitively as
	// UE4 FNames (see commit 8f72937) — keep one canonical form of each.
	b = protocol.AppendStringField(b, "ShipID", strconv.Itoa(int(ship.id)))
	b = protocol.AppendStringField(b, "m_shipId", strconv.Itoa(int(ship.id)))
	b = protocol.AppendStringField(b, "ParentID", strconv.Itoa(int(ship.parentID)))
	b = protocol.AppendStringField(b, "Name", ship.name)
	b = protocol.AppendStringField(b, "m_name", ship.name)
	b = protocol.AppendStringField(b, "NodeType", strconv.Itoa(int(ship.nodeType)))
	b = protocol.AppendStringField(b, "Tier", strconv.Itoa(1))
	b = protocol.AppendStringField(b, "UnlockCost", strconv.Itoa(int(ship.unlockCost)))
	b = protocol.AppendStringField(b, "PrereqID1", strconv.Itoa(int(ship.prereqID1)))
	b = protocol.AppendStringField(b, "PrereqID2", strconv.Itoa(int(ship.prereqID2)))
	b = protocol.AppendBoolField(b, "bIsUnlocked", ship.owned)
	b = protocol.AppendBoolField(b, "bIsPurchased", ship.owned)
	b = protocol.AppendBoolField(b, "bIsNew", ship.bIsNew)
	b = protocol.AppendStringField(b, "ShipClass", strconv.Itoa(int(ship.shipClass)))
	b = protocol.AppendStringField(b, "Weight", strconv.Itoa(int(ship.weight)))
	b = protocol.AppendStringField(b, "m_currentBaseClass", strconv.Itoa(int(ship.shipClass)))
	b = protocol.AppendStringField(b, "m_currentShipClass", strconv.Itoa(int(ship.shipClass)))
	b = protocol.AppendStringField(b, "m_shipTier", strconv.Itoa(1))
	b = protocol.AppendStringField(b, "m_weight", strconv.Itoa(int(ship.weight)))
	if loadout, ok := starterLoadoutByShipID(ship.id); ok {
		b = protocol.AppendStringField(b, "m_precastLoadoutID", strconv.Itoa(int(loadout.precastLoadoutID)))
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
	// Same int32-blind parser as the rest of the m_-prefixed fields in this
	// TechTree/moduleUiData family (see appendMmogShipLoadoutInfoFields).
	b = protocol.AppendStringField(b, "m_realCurrency", "0")
	b = protocol.AppendStringField(b, "m_hardCurrency", "0")
	b = protocol.AppendStringField(b, "m_softCurrency", "0")
	b = protocol.AppendStringField(b, "m_freeXP", "0")
	b = protocol.AppendStringField(b, "m_shipXP", "0")
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
	// Scalar int32 fields on this array entry hit the same restrictive
	// double/int64/string-only tagged union documented on int32SliceToStrings
	// — convert to numeric strings, matching the fix already applied to the
	// sibling YA_GetTechTree row fields this entry is nested under.
	b = protocol.AppendStringField(b, "m_techTreeItemState", strconv.Itoa(4))
	b = protocol.AppendStringField(b, "m_index", strconv.Itoa(int(module.index)))
	b = protocol.AppendStringField(b, "m_priceCurrency", strconv.Itoa(0))
	b = protocol.AppendStringField(b, "m_priceAmount", strconv.Itoa(0))
	b = protocol.AppendStringField(b, "m_originalPriceCurrency", strconv.Itoa(0))
	b = protocol.AppendStringField(b, "m_originalPriceAmount", strconv.Itoa(0))
	b = protocol.AppendStringField(b, "m_moduleTexturePath", "")
	b = protocol.AppendStringField(b, "m_iconTexturePath", "")
	b = protocol.AppendStringField(b, "m_tier", strconv.Itoa(1))
	b = protocol.AppendBoolField(b, "m_shouldShowTierIcon", true)
	b = protocol.AppendBoolField(b, "m_isOwned", module.owned)
	b = protocol.AppendBoolField(b, "m_isOnSale", false)
	b = protocol.AppendBoolField(b, "m_isNew", false)
	b = protocol.AppendBoolField(b, "m_isEquipped", module.equipped)
	b = protocol.AppendStringField(b, "m_itemId", strconv.Itoa(int(module.itemID)))
	if weapon, ok := dreadconfig.WeaponByID(module.itemID); ok {
		b = protocol.AppendStringField(b, "m_damageHigh", strconv.Itoa(int(weapon.DamageHigh)))
		b = protocol.AppendStringField(b, "m_damageMedium", strconv.Itoa(int(weapon.DamageMedium)))
		b = protocol.AppendStringField(b, "m_damageLow", strconv.Itoa(int(weapon.DamageLow)))
		b = protocol.AppendStringField(b, "m_weaponCooldownTime", strconv.FormatFloat(weapon.WeaponCooldownTime, 'f', 3, 64))
		b = protocol.AppendStringField(b, "m_ammoMagazinSize", strconv.Itoa(int(weapon.AmmoMagazinSize)))
		b = protocol.AppendStringField(b, "m_spreadBaseValue", strconv.FormatFloat(weapon.SpreadBaseValue, 'f', 2, 64))
		b = protocol.AppendStringField(b, "m_spreadMaxValue", strconv.FormatFloat(weapon.SpreadMaxValue, 'f', 2, 64))
		b = protocol.AppendStringField(b, "m_maxRange", strconv.Itoa(int(weapon.MaxRange)))
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

// buildMmogUpdateGameModesPayload builds the YA_UpdateGameModes message the
// client's MatchmakingInterpreter uses to populate its playable-mode list.
//
// The client registers the game-modes handler (FUN_142a4ca40, logs
// "GetGameModesData: Game modes list contains <N> items") under response type
// "YA_UpdateGameModes" when it sends YA_GetGameConfigData — it does NOT read the
// GameModes array nested inside the YA_GetGameConfigData "result" object.
// FUN_142a4ca40 reads a *top-level* "GameModes" array (sibling of "RT") whose
// entries carry "Name"/"TeamSize"; the interpreter then walks m_gameModes to
// build the hangar Play UI. Without this frame m_gameModes stays empty, which is
// what DreadGame.log shows ("Received possible game modes from mmogbrain:" with
// no entries). GameModes therefore lives at the message root here, not under
// "result".
func buildMmogUpdateGameModesPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_UpdateGameModes")
	b, stack = protocol.AppendArrayStart(b, stack, "GameModes")
	for _, mode := range matchmaker.GameModeConfigs() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "Name", mode.Name)
		b = protocol.AppendInt32Field(b, "TeamSize", mode.TeamSize)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
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

	b = protocol.AppendStringField(b, "RT", "YA_GetScoringData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "YScoringDataTableRow", dreadconfig.MedalScoringTuneJSON())
	b = protocol.AppendStringField(b, "m_defendScoringDataTable", dreadconfig.DefendScoringTuneJSON())
	b = protocol.AppendStringField(b, "m_remainingPlayerScoringDataTable", dreadconfig.RemainingPlayerScoringTuneJSON())
	b = protocol.AppendStringField(b, "m_killScoringDataTable", dreadconfig.KillScoringTuneJSON())
	b = protocol.AppendStringField(b, "m_waveScoringDataTable", dreadconfig.WaveScoringTuneJSON())
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
			clientFieldName := "m_" + toLowerCamelCase(fieldName)
			
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
			clientFieldName := "m_" + toLowerCamelCase(fieldName)
			
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
			clientFieldName := "m_" + toLowerCamelCase(fieldName)
			
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

// toLowerCamelCase lowercases the leading run of capitals in a Go exported
// field name (e.g. "DamageHigh" -> "damageHigh"), matching the client's
// real m_<camelCase> DataTable field convention — confirmed via the
// decompiled projectile-row parser (YTuneManager::FindOrLoadProjectileRow
// reads m_damageHigh, m_maxTravelDistance, etc., no underscores) and the
// real extracted DN_Projectile_OTS_DT.json using the same camelCase keys.
func toLowerCamelCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
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
	for _, booster := range standardBoosterSeeds() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		// Client's booster-entry parser (FUN_142a66500) reads "ID" (uppercase),
		// not "id" — confirmed by decoding the indirect FName string in the
		// shipping binary.
		b = protocol.AppendInt32Field(b, "ID", booster.id)
		b = protocol.AppendBoolField(b, "active", booster.active)
		b = protocol.AppendInt32Field(b, "type", booster.boosterType)
		b, stack = protocol.AppendArrayStart(b, stack, "effects")
		for _, eff := range booster.effects {
			b, stack = protocol.AppendUnnamedObjectStart(b, stack)
			b = protocol.AppendInt32Field(b, "type", eff.effectType)
			b = protocol.AppendInt32Field(b, "target", eff.target)
			b, stack = protocol.AppendArrayStart(b, stack, "appliesToCreditsPool")
			for _, pool := range eff.creditsPools {
				b, stack = protocol.AppendUnnamedObjectStart(b, stack)
				b = protocol.AppendInt32Field(b, "pool", pool)
				b, stack = protocol.AppendObjectEnd(b, stack)
			}
			b, stack = protocol.AppendObjectEnd(b, stack)
			b, stack = protocol.AppendArrayStart(b, stack, "appliesToReputationPool")
			for _, pool := range eff.repPools {
				b, stack = protocol.AppendUnnamedObjectStart(b, stack)
				b = protocol.AppendInt32Field(b, "pool", pool)
				b, stack = protocol.AppendObjectEnd(b, stack)
			}
			b, stack = protocol.AppendObjectEnd(b, stack)
			b = protocol.AppendStringField(b, "multiplier", eff.multiplier)
			b, stack = protocol.AppendArrayStart(b, stack, "multiplierAdditives")
			b, stack = protocol.AppendObjectEnd(b, stack)
			b, stack = protocol.AppendObjectEnd(b, stack)
		}
		b, stack = protocol.AppendObjectEnd(b, stack)
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

type boosterEffectSeed struct {
	effectType int32
	target     int32
	creditsPools []int32
	repPools   []int32
	multiplier string
}

type boosterSeed struct {
	id          int32
	active      bool
	boosterType int32
	effects     []boosterEffectSeed
}

func standardBoosterSeeds() []boosterSeed {
	return []boosterSeed{
		{id: 520028165, active: false, boosterType: 0, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{5}, repPools: []int32{3}, multiplier: "1.5"},
		}},
		{id: 520028166, active: false, boosterType: 1, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{6}, repPools: []int32{4}, multiplier: "2.0"},
		}},
		{id: 536805377, active: false, boosterType: 2, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{7}, repPools: []int32{5}, multiplier: "1.5"},
			{effectType: 0, target: 2, creditsPools: []int32{8}, repPools: []int32{6}, multiplier: "1.25"},
		}},
		{id: 520028167, active: false, boosterType: 3, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{9}, repPools: []int32{7}, multiplier: "1.25"},
		}},
		{id: 520028169, active: false, boosterType: 4, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{10}, repPools: []int32{8}, multiplier: "1.5"},
		}},
		{id: 520028170, active: false, boosterType: 5, effects: []boosterEffectSeed{
			{effectType: 0, target: 1, creditsPools: []int32{11}, repPools: []int32{9}, multiplier: "1.75"},
		}},
	}
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

// K3: Replace hardcoded Havoc modifier data with loaded table data
func havocModifiers() []dreadconfig.HavocModifier {
	return dreadconfig.AllHavocModifiers()
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
	for _, mod := range havocModifiers() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "ModifierID", mod.RowName)
		b = protocol.AppendStringField(b, "Name", mod.Title)
		b = protocol.AppendStringField(b, "Description", mod.Description)
		b = protocol.AppendInt32Field(b, "WaveStart", mod.MinWave)
		// EffectType and EffectValue not available in loaded data - use defaults
		b = protocol.AppendStringField(b, "EffectType", "unknown")
		b = protocol.AppendStringField(b, "EffectValue", "1.0")
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
	return buildMmogPlayerScoresPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogPlayerScoresPayloadForPlayer(playerPID string) []byte {
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
	// The client's per-entry parser (FUN_142a6f280) reads PID/Rank/SID/Score,
	// not UserName (which it never looks up). The decompile suggests PID is
	// read there as int64, but this protocol has no confirmed int64 wire tag
	// anywhere (parser.go only decodes string/bool/int32/object/array), and
	// playerPID is a string identifier elsewhere in this codebase (e.g. the
	// "PID" field in buildMmogPlayerDataPayload) — sending it as a string
	// here matches that established, working convention rather than
	// guessing at an unconfirmed wire type.
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendInt32Field(b, "Rank", 0)
	b = protocol.AppendStringField(b, "SID", "local_session")
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
	// YTuneManager::Set() reads Returning.MetaData.Version (nested), not a
	// flat Returning.Version — confirmed by decompiling FUN_1403d5160 and
	// decoding the "MetaData" FName it looks up before fetching Version. A
	// flat Version here means the client's cached-version comparison never
	// changes, so the whole WeaponsTune/AbilitiesTune/etc. block below is
	// never actually applied client-side.
	b, stack = protocol.AppendObjectStart(b, stack, "MetaData")
	b = protocol.AppendStringField(b, "Version", "1.0.0")
	b, stack = protocol.AppendObjectEnd(b, stack)
	// CRITICAL frame-size constraint: mmog frames are delimited by a 16-bit size
	// field (protocol.BuildResponseFrame / ParseAppFrames), so a single response
	// MUST stay under 65535 bytes. Previously these fields embedded the full
	// tuning tables (~368KB total), which overflowed that field — the client
	// (and our own ParseAppFrames) then read a truncated/mis-delimited frame and
	// desynced the entire mmog stream, so every frame after YA_Tune (including
	// YA_PlayerGet) became garbage and player data never arrived, stalling the
	// hangar (DreadGame.log: tune requested, never applied; fell back to backup
	// asset tables). The client already sync-loads its shipped tuning via
	// YTuneManager::LoadBackupDataTablesFromAssets(), so sending empty override
	// tables here is functionally correct for the frontend and keeps the frame
	// small. If server-authored tuning is ever needed, it must be chunked across
	// multiple <64KB frames, not stuffed into one.
	b = protocol.AppendStringField(b, "WeaponsTune", `[]`)
	b = protocol.AppendStringField(b, "BattleReadyTune", `[]`)
	b = protocol.AppendStringField(b, "ProjectilesTune", `[]`)
	b = protocol.AppendStringField(b, "AbilitiesTune", `[]`)
	b = protocol.AppendStringField(b, "OfficersTune", `[]`)
	b = protocol.AppendStringField(b, "FeatsTune", `[]`)
	b = protocol.AppendStringField(b, "HavocTune", `[]`)
	b = protocol.AppendStringField(b, "GameModifiersTune", `[]`)
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
	// Same restrictive tagged-union bug class documented elsewhere in this
	// file — send numeric strings, not int32.
	b = protocol.AppendStringField(b, "Value", strconv.Itoa(int(value)))
	b = protocol.AppendStringField(b, "value", strconv.Itoa(int(value)))
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
	// Same restrictive tagged-union bug class documented elsewhere in this
	// file — send numeric strings, not int32.
	b = protocol.AppendStringField(b, "Rank", strconv.Itoa(int(state.currentRank)))
	b = protocol.AppendStringField(b, "UnlockedFleetType", strconv.Itoa(1))
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
	// These 4 were previously labeled "Abilities"/"Perks" — cross-referenced
	// against the authoritative data/assets/ItemIDTable.json CategoryName,
	// all four are actually YWeapon (higher-tier weapon-turret variants of
	// the entries above), not abilities or perks.
	100597788: 1500, // Dreadnought Heavy Primary weapon turret
	100598590: 1500, // YWeapon per ItemIDTable.json; not found in ItemIDRegister for an asset path
	100597790: 2000, // Dreadnought Heavy Primary weapon turret, T5
	100597776: 1000, // Assault Medium Primary weapon turret, T5
	100598567: 1000, // Support Secondary-Short weapon turret, T2
	100597778: 1200, // Assault Secondary-Long weapon turret, T4
	100598569: 1200, // Dreadnought Secondary-Mid weapon turret, T1
	// Classified YWeapon in ItemIDTable.json, but its asset path lives under
	// an Abilities/ folder with an AB_ prefix — lower-confidence than the
	// others above, flagged in issue #36 rather than independently resolved.
	100598592: 2000, // /Game/Generic/Abilities/Dreadnought/Pri_BS_Plasma/T0/AB_DN_Pri_BS_Plasma_Weapon_T0_BP
}

// purchasedItemType derives the item_type recorded for a purchase from the
// authoritative ItemIDTable.json category (via dreadconfig.GetCategoryForItemID,
// which covers the full ~6600-item table, not just the small hardcoded
// catalogItems slice). Previously this was unconditionally "ship" for every
// purchase regardless of what was actually bought — see issue #36's finding
// that several catalogPrices entries were also mislabeled by category.
func purchasedItemType(itemID int32) string {
	category, ok := dreadconfig.GetCategoryForItemID(itemID)
	if !ok {
		return dreadconfig.ItemTypeShip
	}
	switch category {
	case "YWeapon":
		return dreadconfig.ItemTypeWeapon
	case "YAbility":
		return dreadconfig.ItemTypeAbility
	case "YPerk":
		return dreadconfig.ItemTypePerk
	case "YShipLoadoutPrecast", "YShipLoadoutHero":
		return dreadconfig.ItemTypeLoadout
	default:
		return dreadconfig.ItemTypeShip
	}
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
	itemType := purchasedItemType(itemID)
	currency := protocol.FirstNonEmptyString(payload, "currency", "Currency")
	if currency == "" {
		currency = "gp"
	}

	// Check-then-update was a TOCTOU race: two concurrent purchase requests
	// could both read a sufficient balance before either committed its
	// deduction, allowing double-spend / negative balance. Make the whole
	// sequence atomic instead: a transaction with a conditional UPDATE
	// (guarded by the same balance check in the WHERE clause, so the read
	// and the write can't be interleaved by another request) and an
	// INSERT OR IGNORE whose affected-row-count reports "already owned"
	// atomically via the table's (user_id,item_id) primary key, instead of
	// a separate racy pre-check.
	tx, err := database.Begin()
	if err != nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var softCurrency, premiumCurrency int32
	if err := tx.QueryRow(`SELECT soft_currency, premium_currency FROM player_state WHERE user_id=?`, pid).
		Scan(&softCurrency, &premiumCurrency); err != nil {
		return buildMmogErrorPayload(requestName, "player state unavailable")
	}

	deductResult, err := tx.Exec(`UPDATE player_state SET soft_currency=soft_currency-?, updated_at=datetime('now') WHERE user_id=? AND soft_currency>=?`, price, pid, price)
	if err != nil {
		return buildMmogErrorPayload(requestName, "currency deduction failed")
	}
	if rows, _ := deductResult.RowsAffected(); rows == 0 {
		return buildMmogErrorPayload(requestName, "insufficient credits")
	}

	insertResult, err := tx.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,?,?,?)`, pid, itemID, itemType, price, currency)
	if err != nil {
		return buildMmogErrorPayload(requestName, "purchase record failed")
	}
	if rows, _ := insertResult.RowsAffected(); rows == 0 {
		// Already owned — rollback (via defer) undoes the currency deduction above.
		return buildMmogErrorPayload(requestName, "item already owned")
	}

	if err := tx.Commit(); err != nil {
		return buildMmogErrorPayload(requestName, "purchase commit failed")
	}
	committed = true

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

	// Currency deduction and membership extension must commit or fail
	// together — otherwise a currency deduction that "succeeds" but is
	// followed by a failed membership write would take the player's
	// currency for nothing.
	tx, err := database.Begin()
	if err != nil {
		return buildMmogErrorPayload(requestName, "database unavailable")
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Atomic conditional deduction — see buildMmogPurchasePayload's comment
	// for why a separate check-then-update is unsafe under concurrent
	// requests.
	result, err := tx.Exec(`UPDATE player_state SET premium_currency=premium_currency-?, updated_at=datetime('now') WHERE user_id=? AND premium_currency>=?`, price, pid, price)
	if err != nil {
		return buildMmogErrorPayload(requestName, "currency deduction failed")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return buildMmogErrorPayload(requestName, "insufficient elite currency")
	}

	newExpiry, err := extendMembershipTx(tx, pid, durationDays, int32(time.Now().Unix()))
	if err != nil {
		return buildMmogErrorPayload(requestName, "membership persistence failed")
	}
	if err := tx.Commit(); err != nil {
		return buildMmogErrorPayload(requestName, "commit failed")
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", requestName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "eliteDays", durationDays)
	b = protocol.AppendInt32Field(b, "premiumCurrency", premiumCurrency-price)
	b = protocol.AppendInt32Field(b, "ExpireTime", newExpiry)
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

	// Reroll costs 100 credits. Atomic conditional deduction — see
	// buildMmogPurchasePayload's comment for why check-then-update is
	// unsafe under concurrent requests.
	rerollCost := int32(100)

	// Deduct reroll cost
	result, err := database.Exec(`UPDATE player_state SET soft_currency=soft_currency-?, updated_at=datetime('now') WHERE user_id=? AND soft_currency>=?`, rerollCost, pid, rerollCost)
	if err != nil {
		return buildMmogErrorPayload(requestName, "currency deduction failed")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return buildMmogErrorPayload(requestName, "insufficient credits for reroll")
	}

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

// minContractCompletionAge is a rough anti-farming heuristic: the server
// has no real progress tracking tying "kills"/"score" objectives to actual
// match events (see tracked issue — contracts can be claimed with zero
// gameplay), so completion currently can't be validated against genuine
// progress. This isn't a real fix — it only stops literal zero-delay
// complete-and-reseed scripting loops — but it's cheap and honest about
// its limits pending real per-objective progress tracking.
const minContractCompletionAge = 120 // seconds

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

	// Mark contract as completed — the age check and state='active' guard
	// are both in this single atomic UPDATE so a duplicate/concurrent
	// completion request for the same contract can't double-pay (mirrors
	// the atomic-conditional-UPDATE pattern used for currency deductions).
	result, err := db.Exec(`UPDATE player_contracts SET state='completed', progress=100, completed_at=datetime('now'), updated_at=datetime('now')
		WHERE user_id=? AND contract_id=? AND state='active' AND datetime(created_at,?) <= datetime('now')`,
		pid, contractID, fmt.Sprintf("+%d seconds", minContractCompletionAge))
	if err != nil {
		return 0, 0, false
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
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

	// Calculate credits (10 XP = 1 credit)
	creditsGained = xpAmount / 10
	if creditsGained <= 0 {
		return 0, false
	}

	// Atomic conditional deduction, guarded on free_xp>=xpAmount in the same
	// UPDATE — see buildMmogPurchasePayload's comment for why a separate
	// check-then-update is unsafe under concurrent requests.
	result, err := db.Exec(`UPDATE player_state SET free_xp=free_xp-?, soft_currency=soft_currency+?, updated_at=datetime('now') WHERE user_id=? AND free_xp>=?`, xpAmount, creditsGained, pid, xpAmount)
	if err != nil {
		return 0, false
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return 0, false
	}

	return creditsGained, true
}

func convertXPToPremiumCredits(db *sql.DB, pid string, xpAmount int32) (premiumCreditsGained int32, success bool) {
	if xpAmount <= 0 {
		return 0, false
	}

	// Calculate premium credits (100 XP = 1 premium credit)
	premiumCreditsGained = xpAmount / 100
	if premiumCreditsGained <= 0 {
		return 0, false
	}

	// Atomic conditional deduction, guarded on free_xp>=xpAmount in the same
	// UPDATE — see buildMmogPurchasePayload's comment for why a separate
	// check-then-update is unsafe under concurrent requests.
	result, err := db.Exec(`UPDATE player_state SET free_xp=free_xp-?, premium_currency=premium_currency+?, updated_at=datetime('now') WHERE user_id=? AND free_xp>=?`, xpAmount, premiumCreditsGained, pid, xpAmount)
	if err != nil {
		return 0, false
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
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
	// The client's actual Ribbons-array entry parser (FUN_142a73070 in the
	// decompile) reads fields named "ID" and "amt" — confirmed against the
	// literal UTF-16 strings in the shipping binary's .rdata — not "Type"/
	// "Count". It reads them through the same restrictive double/int64/
	// string-only tagged union as the Fleets/ShipLoadouts array parsers
	// (see int32SliceToStrings' doc comment), so amt must be a numeric
	// string too. Keep Type/Count/Name as well in case anything else still
	// keys off them; ID/amt are the ones that actually make it client-side.
	b = protocol.AppendStringField(b, "ID", ribbon.ribbonType)
	b = protocol.AppendStringField(b, "amt", strconv.Itoa(int(ribbon.count)))
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
