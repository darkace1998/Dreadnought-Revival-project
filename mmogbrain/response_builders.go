package main

import (
	"bytes"
	"compress/zlib"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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

// dailyLoginStreakCap bounds how many consecutive days count toward the
// streak bonus, so the reward doesn't grow unbounded.
const dailyLoginStreakCap = 7

// applyDailyLoginStreak advances a player's login-streak counter at most once
// per calendar day (UTC) and returns that day's streak plus the bonus to grant
// on the FIRST login of the day. On any later login the same day it returns all
// zeros.
//
// Returning zero for the streak is deliberate and is what stops the daily-bonus
// screen appearing on every launch. The client's YA_UserLogin handler
// (FUN_142a3af90) does:
//
//	streak = LoginStreak.loginstreak
//	if (0 < streak) { read credits/freexp/gp; *(byte*)(this+0x4148) = 1 }
//
// and that byte is the "show the login bonus" flag. It is set purely on the
// streak being positive -- the reward values are not consulted. So the earlier
// behaviour of returning the stored streak with zeroed rewards, on the theory
// that the popup could then show the count harmlessly, still armed the flag and
// showed the bonus again on every connection. Only a zero streak suppresses it.
func applyDailyLoginStreak(db *sql.DB, pid string) (streak, creditsBonus, freeXPBonus, gpBonus int32) {
	if db == nil {
		return 0, 0, 0, 0
	}
	today := time.Now().UTC().Format("2006-01-02")
	var lastLogin string
	var lastStreak int32
	if err := db.QueryRow(`SELECT last_login_date, login_streak FROM player_state WHERE user_id=?`, pid).Scan(&lastLogin, &lastStreak); err != nil {
		return 0, 0, 0, 0
	}
	if lastLogin == today {
		// Already claimed today: report nothing, or the client re-shows the
		// bonus screen. The stored streak is untouched.
		return 0, 0, 0, 0
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	newStreak := int32(1)
	if lastLogin == yesterday {
		newStreak = lastStreak + 1
	}
	bonusStreak := newStreak
	if bonusStreak > dailyLoginStreakCap {
		bonusStreak = dailyLoginStreakCap
	}
	creditsBonus = 100 * bonusStreak
	freeXPBonus = 50 * bonusStreak
	gpBonus = 0
	_, _ = db.Exec(`UPDATE player_state SET login_streak=?, last_login_date=?, soft_currency=soft_currency+?, free_xp=free_xp+?, updated_at=datetime('now') WHERE user_id=?`,
		newStreak, today, creditsBonus, freeXPBonus, pid)
	return newStreak, creditsBonus, freeXPBonus, gpBonus
}

func buildMmogLoginSuccessPayload(playerPID ...string) []byte {
	var b []byte
	var stack []int
	pid := defaultMmogPlayerPID
	if len(playerPID) > 0 {
		pid = playerPID[0]
	}

	streak, creditsBonus, freeXPBonus, gpBonus := applyDailyLoginStreak(currentMmogPlayerStateDB(), normalizedPlayerStatePID(pid))

	b = protocol.AppendStringField(b, "RT", "YA_UserLogin")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	// issue #50: the client's YA_UserLogin "ok" handler (FUN_142a3af90) reads
	// result.LoginStreak.loginstreak, and only when loginstreak > 0 also
	// LoginStreak.credits/freexp/gp (that day's streak-bonus reward) — it
	// does not read flat result.credits/premiumCurrency/freexp/xp (those are
	// dead fields for this RT; the player's real currency balance is
	// delivered correctly via YA_PlayerGet's gl/ob/FreeXp fields instead).
	b, stack = protocol.AppendObjectStart(b, stack, "LoginStreak")
	b = protocol.AppendInt32Field(b, "loginstreak", streak)
	b = protocol.AppendInt32Field(b, "credits", creditsBonus)
	b = protocol.AppendInt32Field(b, "freexp", freeXPBonus)
	b = protocol.AppendInt32Field(b, "gp", gpBonus)
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
	// The client's quick-play button sends GameType="ANY" (and MapName="ANY"),
	// captured verbatim from a real request:
	//
	//	RT=YA_EnterMatchmaking Name="*matchmaking" MapName="ANY" GameType="ANY"
	//	FleetID=<guid> Cluster="" FMPeerID=<16 bytes> MaintenanceCost=<int32>
	//
	// "ANY" was not treated as a wildcard, so ValidGameMode rejected it and the
	// server answered "unsupported game mode" -- the player could never join the
	// queue at all. It means "any mode", so it resolves to the default like the
	// other wildcards do.
	if matchmaker.IsWildcardGameMode(gameMode) {
		gameMode = matchmaker.DefaultGameMode
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

func appendMmogFleetBackendFields(b []byte, stack []int, playerPID string, fleet mmogFleetSeed) ([]byte, []int) {
	// These fields are reflected (FUN_14071d4f0) onto the native struct
	// FYLocalServerPlayerDataInformation, which the YA_PlayerGet handler parses
	// and the loadout manager reads via InitializeFromPlayerData. The SDK
	// (FYLocalServerPlayerDataInformation) shows the real shapes:
	//   m_displayInformation : FString
	//   m_loadoutList        : TArray<FYShipImportLoadoutInfo>   <-- STRUCT array
	//   m_fleetId            : FName
	//   m_fleetType          : int32
	//   m_flagshipIndex      : int8
	// m_loadoutList was previously sent as a bare int32[] of loadout ids, which
	// cannot populate an array-of-struct property — so the loadout list came up
	// empty, InitializeFromPlayerData never completed, and the fleet manager's
	// OnLoadoutDataInitialized (readiness bit 1) never fired (stuck at 12/15).
	// Emit each loadout as a full FYShipImportLoadoutInfo object instead.
	// Reflection reads these int32 props correctly (unlike the int32-blind JSON
	// union parser), so numeric fields stay int32; FName/FString fields go as
	// strings.
	b = protocol.AppendStringField(b, "m_displayInformation", fleet.displayName)
	b = protocol.AppendInt32Field(b, "m_fleetId", fleet.fleetID)
	b = protocol.AppendInt32Field(b, "m_flagshipIndex", fleet.flagshipIndex())
	b = protocol.AppendInt32Field(b, "m_fleetType", fleet.fleetType)
	b, stack = protocol.AppendArrayStart(b, stack, "m_loadoutList")
	for _, lo := range fleet.shipLoadouts {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "m_loadoutID", strconv.Itoa(int(lo.loadoutID())))
		b = protocol.AppendStringField(b, "m_pid", playerPID)
		b = protocol.AppendInt32Field(b, "m_precastLoadoutID", lo.precastLoadoutID)
		b = protocol.AppendStringField(b, "m_name", lo.loadoutName)
		b = protocol.AppendInt32Field(b, "m_shipClass", lo.ship.shipClass)
		b = protocol.AppendStringField(b, "m_displayInfo", lo.displayInfo())
		b, stack = protocol.AppendInt32ArrayField(b, stack, "m_weaponIDs", lo.weaponIDs())
		b, stack = protocol.AppendInt32ArrayField(b, stack, "m_abilityIDs", lo.abilityItemIDs())
		b, stack = protocol.AppendInt32ArrayField(b, stack, "m_perkIds", lo.perkItemIDs())
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// Each Fleets entry's FID is a GATE with TWO requirements, both confirmed by
// disassembly and by live testing:
//   1. It must parse as a nonzero 32-hex GUID. FUN_142a1d450 parses the value
//      strictly as a GUID and yields an all-zero reference for anything else,
//      which the parser rejects. A plain token like "RecruitFleet" always
//      failed here (an old comment claimed the OPPOSITE — that FID must not be
//      GUID-shaped — which misled several sessions).
//   2. It must ALREADY be interned in the client's FName pool. The resolve is
//      FIND-ONLY: FUN_140ca0ab0 returns the chunked pool (0x400 bytes of chunk
//      pointers, count at +0x400) and the parser rejects an index that is
//      negative, >= the count, or resolves to a null entry (checks at
//      0x142a77ba4-0x142a77be7). A freshly generated GUID has never been
//      interned, so it fails too — verified live with an md5-derived GUID.
// The player's PID satisfies both: it is GUID-shaped and the client interns it
// from its own auth data. We only ever send one (unlocked) fleet, so reusing it
// as the fleet identity does not collide.

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
	// MINIMAL fleet entry. The client's Fleets-array parser (FUN_142a77910)
	// reads exactly these fields: FID (gate — see the FID note below), PID,
	// FleetType, AutoRepair, Maintenance, LastWinTime, ChargingBeginTime,
	// ChargingCharges, Rating, shipIds, ShipTechTreeComplete, FlagShipID,
	// FlagShipLoadoutIndex. Everything the parser ignores was dropped
	// (Type/FleetID/Name/DisplayName/Unlocked/shipCount/flagshipShipId/
	// bIsActive) — the hangar UI reads unlock/display state from the tech tree,
	// not from here. The m_* fields (appendMmogFleetBackendFields) are kept:
	// they feed a separate native reflection class (YLocalServerPlayerDataInformation),
	// not the parser, and are shared with the YA_PlayerGet fleet summary.
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "FID", normalizedPlayerStatePID(playerPID))
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = appendMmogFleetRuntimeFields(b, fleet)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b, stack = appendMmogFleetBackendFields(b, stack, playerPID, fleet)
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

// unlockedFleetsOnly filters a fleet list to the fleets the player actually
// owns/has unlocked — i.e. fleets that are active or contain at least one ship.
// A new player owns only the Recruit fleet; the locked Veteran/Legendary
// fleets (0 ships, not active) must NOT be sent. Sending those empty locked
// fleets made the client's fleet validator reject the whole set ("Invalid
// fleet data, fleet array is empty"). Falls back to the raw list if that would
// leave nothing to send.
func unlockedFleetsOnly(fleets []mmogFleetSeed) []mmogFleetSeed {
	out := make([]mmogFleetSeed, 0, len(fleets))
	for _, fleet := range fleets {
		if fleet.active || len(fleet.shipLoadouts) > 0 {
			out = append(out, fleet)
		}
	}
	if len(out) == 0 {
		return fleets
	}
	return out
}

func buildMmogPlayerFleetsPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)
	fleets := state.fleets
	if len(fleets) == 0 {
		fleets = []mmogFleetSeed{starterFleetState()}
	}
	fleets = unlockedFleetsOnly(fleets)

	// "result" IS the fleet array itself — not an object wrapping one.
	//
	// The client's YA_PlayerFleets handler (dispatched on request slot
	// interface+0x3730) does, at 0x142a2646a:
	//     GetField(payload, "result")  ->  FUN_142a77910(dest, thatValue)
	// i.e. it hands the "result" VALUE to the Fleets-array parser and never
	// looks at the payload root. Sending FID/PID/Fleets/Items at the top level
	// made that lookup return nothing, so the parser saw an element count of 0
	// (the check at 0x142a77a52), logged "Invalid fleet data, fleet array is
	// empty", returned false, and the handler called HandleMmogbrainError(8)
	// ("Failed to receive fleet updated data") instead of broadcasting the
	// fleet-updated delegate at interface+0xa50. That delegate is what sets
	// UYFleetManager readiness bit 2, so readiness stalled at 13 and the client
	// never left the loading screen (CheckCompletedInitialization needs 15).
	//
	// YA_RequestStaticFleetData already wraps its content in "result" — this
	// response was simply inconsistent with it.
	b = protocol.AppendStringField(b, "RT", "YA_PlayerFleets")
	b = protocol.AppendStringField(b, "FID", "PlayerFleets")
	b = protocol.AppendStringField(b, "PID", normalizedPlayerStatePID(playerPID))
	b = protocol.AppendStringField(b, "Name", "PlayerFleets")
	b = protocol.AppendInt32Field(b, "PlayedMatches", 0)
	// "result" IS the fleet array — not an object containing one.
	//
	// The handler does GetField(payload, "result") and hands that VALUE
	// straight to the Fleets-array parser (FUN_142a77910), which iterates the
	// value's elements as fleet entries. Two earlier shapes both failed:
	//   - fleets at the payload root, no "result": lookup returned nothing, so
	//     element count 0 -> "Invalid fleet data, fleet array is empty".
	//   - "result" as an OBJECT wrapping a "Fleets" array: the parser counted
	//     that object's members as entries. The client logged "Fleets received
	//     (5)" — our six members minus PlayedMatches, whose int32 tag its value
	//     parser drops — then rejected the first "entry" (the FID string) with
	//     "Invalid fleet data received".
	// Emitting the array directly gives the parser exactly what it iterates.
	b, stack = protocol.AppendArrayStart(b, stack, "result")
	for _, fleet := range fleets {
		b, stack = appendMmogPlayerFleetEntry(b, stack, playerPID, fleet)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	// NO root-level "Items" array. Its presence corrupted the client's parsed
	// value tree for the sibling "result" array: the Fleets parser read a
	// nonsense element count while its data pointer stayed correct, so entry 0
	// always parsed perfectly and a phantom entry 1 then failed with "Invalid
	// fleet data received". Measured counts were incoherent — 1 fleet reported
	// 2, 2 fleets reported 12, 3 fleets reported 12 — which is why no encoding
	// formula explained it. Dropping Items makes the count exact (1 fleet -> 1),
	// the entry parse succeeds, HandleMmogbrainFleetUpdated fires, and
	// UYFleetManager readiness finally reaches 15. Items was never read by this
	// parser anyway; fleet unlock state comes from the tech tree.
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
	fleets = unlockedFleetsOnly(fleets)

	// The client parses YA_FleetUpdate with the SAME parser as YA_PlayerFleets,
	// which gates on the top-level FID/PID wrapper before it will read the
	// Fleets array (the Fleets-array parser FUN_142a77910 keys off FID). A bare
	// { RT, Fleets:[...] } push (no FID/PID/Name/PlayedMatches/Items) makes the
	// client log "Invalid fleet data, fleet array is empty" and fire
	// HandleMmogbrainError (code 8, "Failed to receive fleet updated data"),
	// so fleet-manager bit 2 never completes. Mirror the full YA_PlayerFleets
	// shape here, only the RT differs.
	b = protocol.AppendStringField(b, "RT", "YA_FleetUpdate")
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
	// Static fleet-type definitions carry no per-player loadout ownership, so
	// there is no player PID to stamp on the FYShipImportLoadoutInfo entries.
	b, stack = appendMmogFleetBackendFields(b, stack, "", fleet)
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

// The client imports these two blobs as JSON DataTables. An empty array is
// NOT accepted — it makes UYSeasonsDataManager log "Error in seasons/events
// data table coming from mmogbrain: Failed to parse the JSON data", so send
// one well-formed row each. Both are declared INACTIVE (m_active:false, and
// CurrentSeason empty below) so no season/event is running.
//
// These were previously emptied to `[]` on the theory that any season/event
// let the client's UYPlayerMPQuestCycle build an empty-but-non-null quest
// provider and infinite-recurse. That theory is disproven: a full crash dump
// shows the flag actually gating that recursion (mmog interface +0x44c8) is
// still 1 with the season response withheld ENTIRELY, and the quest
// collection it recurses over is built from the client's own
// MPQuestCollection.uasset, not from anything we send.
const mmogSeasonDataSeasonsJSON = `[{"Name":"PVE_Season1","m_active":false,"m_name":"Miner Inconvenience","m_descShort":"","m_descLong":"","m_imageLarge":"None","m_imageSmall":"None","m_rewardLevels":[]}]`

const mmogSeasonDataEventsJSON = `[{"Name":"PVE_S1E1","m_name":"Incident Management","m_descShort":"","m_descLong":"","m_map":"None","m_mapParameters":"","m_gameMode":"YGMT_HORDE","m_color":{"r":160,"g":144,"b":131,"a":255},"m_imageSmall":"None","m_imageLarge":"None","m_rewardLevels":[],"m_startDate":"2018.05.16-16.00.00","m_endDate":"2018.05.16-16.19.59","m_season":"PVE_Season1"}]`

func buildMmogSeasonDataPayload() []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetSeasonData")
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "Events", mmogSeasonDataEventsJSON)
	b = protocol.AppendStringField(b, "Seasons", mmogSeasonDataSeasonsJSON)
	// CurrentSeason intentionally EMPTY to declare NO active season.
	// SetActiveEventAndSeason takes an early "clear active season" branch when
	// this is empty. An active season activates the client's
	// UYPlayerMPQuestCycle, which async-loads MP season quests
	// (UYMPQuestsCollection::OnQuestsAsyncLoaded, YMPQuestsCollection.cpp) and
	// enters INFINITE delegate recursion -> stack-overflow crash in the
	// private-server context (confirmed via crash minidump: the
	// FUN_1403fe800/FUN_140404440/FUN_140402db0 quest-load cycle). No active
	// season means the quest cycle never starts. It also (harmlessly) hides
	// season UI. Re-enable only with real, loadable MP season-quest assets.
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

// knownFactionNames maps real faction IDs (assigned here — the extracted
// client assets have no numeric faction registry, only named texture/vanity
// assets) to the two real named factions confirmed in extracted client
// content (issue #42): DevGroup/Meta/Factions/Texture/VAN_DCL_Takemikazuchi
// and VAN_DCL_Maestrom, both also referenced by VAN_CLR_Faction_*/VAN_PN_
// Faction_* vanity-item color/paint assets.
var knownFactionNames = map[int32]string{
	1: "Takemikazuchi",
	2: "Maelstrom",
}

type playerFactionReputation struct {
	factionID  int32
	reputation int32
}

func loadPlayerFactionReputation(playerPID string) []playerFactionReputation {
	db := currentMmogPlayerStateDB()
	if db == nil {
		return nil
	}
	pid := normalizedPlayerStatePID(playerPID)
	rows, err := db.Query(`SELECT faction_id, reputation FROM player_faction_reputation WHERE user_id=? ORDER BY faction_id`, pid)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []playerFactionReputation
	for rows.Next() {
		var entry playerFactionReputation
		if err := rows.Scan(&entry.factionID, &entry.reputation); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out
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

// appendMmogEventScoreEntry emits one EventScores entry per fleet type.
//
// issue #47: the client's per-entry parser (FUN_142a6bdc0) only reads
// EventID (string, must be non-empty), FleetType (int, must be in [1,3]),
// and Score (int, must be positive) — it never looks up SeasonID/Level at
// all, so every entry sent with those field names was silently rejected.
// We don't yet track per-event, per-fleet-type score server-side (only a
// per-season aggregate), so this reuses the season ID as the EventID and
// reports the same aggregate score once per fleet type (1=Recruit,
// 2=Veteran, 3=Legendary) — an honest approximation, not real granular
// event tracking, but it satisfies the client's validation gate instead of
// having every entry rejected outright.
func appendMmogEventScoreEntry(b []byte, stack []int, progress playerSeasonProgress) ([]byte, []int) {
	if progress.seasonID == "" || progress.xp <= 0 {
		return b, stack
	}
	for _, fleetType := range []int32{1, 2, 3} {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "EventID", progress.seasonID)
		b = protocol.AppendInt32Field(b, "FleetType", fleetType)
		b = protocol.AppendInt32Field(b, "Score", progress.xp)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
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
	// "disp" is the captain appearance string the client uploads with
	// YA_SavePlayerDisplayInformation and reads back here. Sending it empty
	// threw away the player's customisation on every login.
	b = protocol.AppendStringField(b, "disp", state.displayInfo)
	b = protocol.AppendStringField(b, "motto", "")
	// Client-owned save blobs, echoed back exactly as uploaded. These must be
	// byte-array fields (tag 0x0a), not strings: the client reads them through
	// a value-node accessor that only looks at the node's binary pointer/length
	// slot, so a string field would always read back as zero-length. Sending an
	// empty array for a player who has never saved is correct — that is a new
	// account, and the client will run onboarding and then upload its first
	// blob via YA_SaveGame.
	b = protocol.AppendBytesField(b, "SGD", loadPlayerSaveBlob(playerPID, playerSaveSlotOnboarding))
	b = protocol.AppendBytesField(b, "SCtA", loadPlayerSaveBlob(playerPID, playerSaveSlotCtA))
	b = protocol.AppendStringField(b, "LGVersion", "0")
	// Only emit the Membership block for players with real membership history
	// (active or previously expired). For players who never bought elite,
	// membershipExpiresAt returns 0, and the client's YA_PlayerGet parser
	// (FUN_142a85120, called from FUN_142a70da0) has a dedicated branch for a
	// wholly-absent Membership object (`if (*param_2 == 0)`) that skips its
	// int64 FILETIME conversion entirely. Sending ExpireTime="0" instead drives
	// it through the value-present branch, which computes a 1970-01-01 epoch
	// FILETIME and logs "Membership expires on 1970.01.01-00.00.00" /
	// "Membership expire in 0.000000 hours" — the exact last lines in the log
	// before an EXCEPTION_STACK_OVERFLOW crash (RVA 0xc9bf1e, UnrealNames.cpp
	// FName intern) 8s into a hangar-entry session. This was a working
	// always-1-year-active value until f6c1fcb switched it to literal 0; use
	// the object's presence itself as the "has membership ever" signal instead
	// of a sentinel value, so real purchasers (including expired ones) still
	// get a real ExpireTime while never-purchased players get the client's own
	// designed "no membership" path instead of a fabricated epoch timestamp.
	if expiresAt := membershipExpiresAt(playerPID); expiresAt != 0 {
		b, stack = protocol.AppendObjectStart(b, stack, "Membership")
		b = protocol.AppendStringField(b, "ExpireTime", strconv.Itoa(int(expiresAt)))
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b = protocol.AppendStringField(b, "DailyContractStateID", strconv.Itoa(dailyContractState(playerPID)))
	b = protocol.AppendStringField(b, "LastContractsAssignment", strconv.Itoa(int(now)))
	b = protocol.AppendStringField(b, "DailyContractLastReplaceTime", strconv.Itoa(int(now)))
	// issue #43: the client's top-level parser (FUN_142a70da0) reads Quests
	// from the same object as the three DailyContract* fields above (via
	// FUN_142a69310), but this payload never sent it — every entry silently
	// missing. Reuses the same active-contract data as YA_GetDailyContractsData
	// (different RT, different per-entry field names) rather than a separate
	// quest system, since no other quest data model exists server-side.
	b, stack = appendMmogQuestsArray(b, stack, playerPID)
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
	b, stack = appendMmogFleetBackendFields(b, stack, playerPID, starterFleet)
	// Adding a full "Fleets" array here was tested against the live client
	// (2026-07-27) and changed nothing — the fleet array the client complained
	// about comes from YA_PlayerFleets, not player data. Left out to keep
	// YA_PlayerGet small.
	b, stack = protocol.AppendArrayStart(b, stack, "FactionReputation")
	for _, entry := range loadPlayerFactionReputation(playerPID) {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "FactionID", entry.factionID)
		b = protocol.AppendInt32Field(b, "Reputation", entry.reputation)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
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
	// "Items" is the player's OWNED-ITEM inventory, and without it the hangar
	// has nothing to show. UYInventoryManager::UpdateItemsFromInventory reads
	// the owned-item array from the player-data snapshot at +0x150/+0x158, and
	// that array is filled only by FUN_142a6ced0 parsing this exact field out of
	// YA_PlayerGet. We never sent it, so the client logged
	// "UpdateItemsFromInventory | Updated 0 items."
	//
	// It is emitted LAST on purpose. In YA_PlayerFleets a trailing sibling
	// array corrupted the parsed value tree of the array BEFORE it, so an array
	// that must parse correctly should have no array siblings after it.
	//
	// Per-entry the client reads ItemID, Amount, NewPromotionID and Credits
	// (FUN_142a77660) through the restrictive tagged union that only accepts
	// double/int64/string — our int32 tag reads as 0 — so every value goes as a
	// numeric string. ItemID must be non-zero or the entry is skipped outright.
	b, stack = protocol.AppendArrayStart(b, stack, "Items")
	for _, item := range starterOwnedInventorySeeds() {
		if item.itemID == 0 {
			continue
		}
		amount := item.quantity
		if amount <= 0 {
			amount = 1
		}
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "ItemID", strconv.Itoa(int(item.itemID)))
		b = protocol.AppendStringField(b, "Amount", strconv.Itoa(int(amount)))
		b = protocol.AppendStringField(b, "NewPromotionID", "0")
		b = protocol.AppendStringField(b, "Credits", "0")
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
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

func buildMmogPlayerStatsCounterDataPayload(playerPID ...string) []byte {
	var b []byte
	var stack []int

	pid := defaultMmogPlayerPID
	if len(playerPID) > 0 {
		pid = playerPID[0]
	}
	counters := playerStatsCounters(pid)

	b = protocol.AppendStringField(b, "RT", "YA_GetPlayerStatsCounterData")
	b, stack = protocol.AppendArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntries(b, stack, counters)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntries(b, stack, counters)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
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

	// MINIMAL "dynamic" tech tree: the client already holds every static
	// ship/loadout/weapon/module definition in its own Content assets, so
	// YA_GetTechTree only conveys per-node identity + the player's
	// unlock/ownership state. Sending the full static dataset (ship stats,
	// names, per-ship loadout info, per-module weapon stats/prices/textures)
	// bloated this frame to ~39-56KB and overflowed the client's 32KB mmog
	// receive ring buffer. Rows are now ~identity+flags only, and moduleUiData
	// carries ownership state only. See t1t2TechTree Ships / appendMmogModuleOwnershipEntry.
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
		b, stack = appendMmogModuleOwnershipEntry(b, stack, module)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, _ = protocol.AppendObjectEnd(b, stack)

	// The client does not read any of the above. Its YA_GetTechTree handler
	// (response slot 0x36b0) builds the FName "TechTrees", fetches that single
	// field, and reads it through the BYTE-ARRAY accessor -- the same one the
	// SGD save blob uses. Everything else in the response is ignored, silently
	// and without an error, which is why a fully populated techTreeRow array
	// produced no parse logging and left the tech tree manager empty.
	//
	// The blob is a plain zlib stream: FYMmogbrain inflates it with
	// inflateInit_ ("1.2.5", stream size 0x58) straight from the field bytes,
	// with no length prefix, growing the output in 32KB chunks and logging
	// "Error during output decompression: %d" on failure. The inflated bytes
	// are then handed to the ordinary mmog document parser -- it dispatches on
	// the same wire tags we already emit (0x09 string, 0x56 int32) -- so the
	// payload inside is just another mmog document.
	b = protocol.AppendBytesField(b, "TechTrees", compressMmogDocument(buildMmogTechTreeDocument()))
	return b
}

// buildMmogTechTreeDocument builds the document that goes inside the TechTrees
// blob. It carries the same rows as the (ignored) plain fields above so the two
// cannot drift while the inner field names are still being established.
// buildMmogTechTreeDocument builds the document carried, zlib-compressed, in
// YA_GetTechTree's "TechTrees" byte-array field.
//
// SHAPE: the root is an ARRAY of ARRAYS of item objects -- not a named object.
// UYTechTreeManager's loader walks it as AsArray(root) -> AsArray(element) ->
// item, and stores it as an outer array (manager+0x38, stride 0x28) of inner
// arrays (stride 0x48). Each inner array is one manufacturer's tree. The old
// document invented a "techTreeRow"/"moduleUiData" object at the root, so the
// very first AsArray produced nothing and the manager stayed empty.
//
// FIELDS: the loader resolves these by wide-string name --
//
//	Id                        the item id; this is the key
//	                          TechTreeManager::FindItemForShipId matches on
//	ClassId, Manufacturer, Tier, Position, Visible
//	XPCost, FPCost, NumTechTreeItemsRequired
//	Prereq                    ARRAY of prerequisite ids
//	ProxyType                 scalar, validated to -1..9; anything else logs
//	                          "Invalid tech tree item type: %d"
//	Wires                     ARRAY of {type, x_start, y_start, x_end, y_end}
//
// Every numeric value is a numeric string: this loader reads through the same
// restrictive union as the rest of the protocol (types 1/2 double, 3 int64, 4
// string via _wtoi) and yields 0 for an int32 wire tag.
//
// An empty manager is why the hangar's fleet and loadout screens do nothing:
// they compose FUIShipData through the tech-tree interpreter, so with no items
// every ship entry comes back with an empty m_loadouts and m_shipId 0.
// techTreeItem is one node of the tech tree document.
type techTreeItem struct {
	id           int32
	classID      int32
	manufacturer int32
	tier         int32
	position     int32
	xpCost       int32
	prereq       []int32
	// hero items are laid out in their own grid on the manufacturer page
	// (HeroShipTechTreeRow0..4 alongside TechTreeRow0..4), so their Position
	// counts from zero independently of the ships'.
	hero bool
}

// techTreeXPCostByTier is what a hull costs to research.
//
// This is server-authored: no client asset states it, and the community
// reference does not carry costs either. Tier 1 must be free (the four starter
// hulls are owned from the start); the rest is a progression we choose. Flagged
// rather than dressed up as recovered data -- the previous code sent a flat
// 5000 for everything researchable, which at least was consistent, and this is
// the same kind of guess with a shape.
var techTreeXPCostByTier = map[int32]int32{1: 0, 2: 5000, 3: 15000, 4: 40000, 5: 100000}

// techTreeBaseItems turns the base hull roster into tech tree nodes.
//
// Prerequisites follow the hull line: T(n) requires T(n-1) of the same
// <Class><Size>. That is the one progression the data actually supports, and it
// reproduces the links the hand-written seeds already had (Trafalgar after
// Agosta, Nav after Simargl, and so on).
//
// The 11 lines that start above tier 1 -- the Light and Heavy variants opening
// at T2/T3/T4 -- get NO prerequisite. In the real game they must branch off
// some earlier hull, but neither the client's data nor the reference says which,
// so they are left unlinked rather than wired to a guess. That is also why the
// old seed for Furia pointed at Rurik: a plausible-looking cross-line link
// somebody invented. Wires are empty for the same reason.
func techTreeBaseItems() []techTreeItem {
	byLine := map[string]map[int32]int32{}
	for _, hull := range baseShipLoadouts {
		if byLine[hull.hullLine] == nil {
			byLine[hull.hullLine] = map[int32]int32{}
		}
		byLine[hull.hullLine][hull.tier] = hull.loadoutID
	}

	items := make([]techTreeItem, 0, len(baseShipLoadouts))
	for _, hull := range baseShipLoadouts {
		manufacturerID := shipManufacturerID(baseShipManufacturerByClassSize[hull.hullLine])
		if manufacturerID < 0 {
			logrus.WithField("hull_line", hull.hullLine).Warn("mmog: tech tree hull line has no manufacturer")
			continue
		}
		var prereq []int32
		if previous, ok := byLine[hull.hullLine][hull.tier-1]; ok {
			prereq = []int32{previous}
		}
		items = append(items, techTreeItem{
			id: hull.loadoutID,
			// ClassId is an ITEM ID, not the 1..15 EYShipClass enum. The
			// manager's store gate is
			//
			//	MOVSXD R15,[RBP-0x78]   ; ClassId
			//	TEST R15D,R15D / JLE skip
			//	CALL FUN_1405483e0      ; (ClassId >> 24) & 0xff in {1, 3}?
			//	JZ skip
			//
			// where FUN_1405483e0 resolves the registered category ids for
			// YShipLoadoutPrecast (1) and YShipLoadoutHero (3) and compares
			// them against FUN_1402cf640(ClassId) = the top byte. Sending the
			// EYShipClass ordinal put a 0 in that byte, so the gate rejected
			// EVERY item, nothing was ever added to a manufacturer group, and
			// the tech tree screen reported "Could not find a manufacturer with
			// id 0/1/2" with an empty TreeWidgetList.
			//
			// The value is the item's OWN id. That is not a grouping key: the
			// array the loader builds at manager+0x48 is keyed on ClassId
			// (140401426 compares entry[0] against it), and the client looks
			// that array up by SHIP ID --
			//
			//	FUN_1403f5050(manager, shipId): scan manager+0x48 stride 0x28
			//	                                for entry[0] == shipId
			//
			// which is what UTechTreeInterpreter::ComposeModuleUiDataForShip
			// calls. So a ship's modules resolve only when its ClassId equals
			// its own id. An earlier version of this used the hull line's root
			// loadout id, on the theory that a shared ClassId is what makes a
			// line one column; that was wrong twice over -- the column grouping
			// is the separate manufacturer-keyed array at manager+0x38, and a
			// shared ClassId left every tier above the line root unable to find
			// its modules ("Modules not found for ship id %d").
			classID:      hull.loadoutID,
			manufacturer: manufacturerID,
			tier:         hull.tier,
			xpCost:       techTreeXPCostByTier[hull.tier],
			prereq:       prereq,
		})
	}
	return items
}

// techTreeHeroItems turns the hero roster into tech tree nodes.
//
// Heroes belong in this document: the client never fetches them separately.
// UTechTreeInterpreter::GetHeroShipsFromManufacturerData asks the manager for a
// manufacturer's ordinary item array and keeps the entries whose type byte the
// manager stamped as "hero" -- which it decides purely from the id's category
// (3 = YShipLoadoutHero, against 1 = YShipLoadoutPrecast for the base hulls).
// So sending hero loadout ids with a Manufacturer is the whole requirement; no
// extra flag exists on the wire.
//
// Sending them used to blow the response to ~56KB and overflow the client's
// 32KB mmog receive ring buffer, which is why they were pulled. That happened
// because they were added as full response ROWS, with all their static data.
// Here they are items in the zlib'd document instead, which costs a fraction of
// that -- and the response rows are deliberately left alone.
func techTreeHeroItems() []techTreeItem {
	items := make([]techTreeItem, 0, len(heroShipLoadouts))
	for _, hero := range heroShipLoadouts {
		manufacturerID := shipManufacturerID(hero.manufacturer)
		if manufacturerID < 0 {
			logrus.WithFields(logrus.Fields{"hero": hero.name, "manufacturer": hero.manufacturer}).
				Warn("mmog: hero ship has no manufacturer id; it cannot be placed on any maker page")
			continue
		}
		items = append(items, techTreeItem{
			id: hero.loadoutID,
			// A hero is its own column -- nothing researches into or out of it
			// -- so it is its own class root. Its id is category 3, which the
			// store gate accepts alongside category 1; see techTreeBaseItems.
			classID:      hero.loadoutID,
			manufacturer: manufacturerID,
			tier:         hero.tier,
			// Heroes are bought in the store, not researched, and nothing in
			// the client states a research cost for them -- so 0 rather than a
			// made-up figure. Their real price rides on the market catalog.
			xpCost: 0,
			hero:   true,
		})
	}
	return items
}

// buildMmogTechTreeDocument emits the tech tree the client actually reads.
//
// It is built from the base hull roster rather than from the response's ship
// rows. Those rows exist for the hangar fleet loader, which looks a fleet's
// ships up by loadout id; the tree is a separate, static thing -- the whole
// buyable roster -- and tying it to the four ships a player happens to own is
// what limited it to ten nodes.
//
// Nodes are grouped by manufacturer because the client indexes the groups that
// way (GetManufacturerData(0/1/2)), and ordered by hull line then tier inside a
// group so Position increases along each line.
// techTreeItemLimit caps how many items the document carries, per manufacturer
// group, when DN_TECHTREE_LIMIT is set. 0 (the default) means no cap.
//
// This exists to bisect a client-side failure that no field value explains. The
// client's field lookup (FUN_1402c3bf0) has a fallback: when a node has children
// but NO stored names, it treats the requested field name as an array INDEX --
// _wtoi("ProxyType") is 0, so it returns child[0], which is Id. That is exactly
// what the client logged, "Invalid tech tree item type: 33489262" and eleven
// more, each value being that item's own Id. The other 88 items resolved their
// names correctly. Since the 12 are scattered rather than contiguous and share
// no field value, the suspicion is scale -- so being able to serve a smaller
// document and see whether the fallback stops firing is the cheapest way to
// find out. Unset the variable to go back to the full roster.
func techTreeItemLimit() int {
	limit, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DN_TECHTREE_LIMIT")))
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func buildMmogTechTreeDocument() []byte {
	limit := techTreeItemLimit()
	byManufacturer := map[int32][]techTreeItem{}
	nextPosition := map[int32]map[bool]int32{}
	for _, item := range append(techTreeBaseItems(), techTreeHeroItems()...) {
		if nextPosition[item.manufacturer] == nil {
			nextPosition[item.manufacturer] = map[bool]int32{}
		}
		item.position = nextPosition[item.manufacturer][item.hero]
		nextPosition[item.manufacturer][item.hero]++
		byManufacturer[item.manufacturer] = append(byManufacturer[item.manufacturer], item)
	}
	order := make([]int32, 0, len(byManufacturer))
	for manufacturerID := range byManufacturer {
		order = append(order, manufacturerID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	var b []byte
	var stack []int
	for _, manufacturerID := range order {
		b, stack = protocol.AppendUnnamedArrayStart(b, stack)
		for n, item := range byManufacturer[manufacturerID] {
			if limit > 0 && n >= limit {
				break
			}
			b, stack = appendMmogTechTreeItem(b, stack, item)
		}
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	return protocol.AppendRootEnd(b)
}

// techTreeProxyTypeShip is the ProxyType for a ship node.
//
// -1 is the loader's own default for an absent ProxyType (it seeds the slot with
// 0xff before parsing), so it is the right value to send. But CORRECTION: an
// earlier version of this comment claimed -1 was needed because ProxyType picks
// which sub-array TechTreeManager::FindItemForShipId reads. That is wrong. The
// two-sub-array split (+0x08 when ProxyType is -1, +0x18 otherwise) is in the
// array at manager+0x48. The array FindItemForShipId searches is the one at
// manager+0x38, and its store path has no ProxyType branch at all -- it always
// uses the sub-array at +0x08. Changing this value did not affect
// FindItemForShipId, and did not fix the "Could not find item for ship id"
// family of failures it was expected to.
const techTreeProxyTypeShip = -1

// techTreeRow pairs a ship with the id its tech tree row is keyed on.
type techTreeRow struct {
	id   int32
	ship mmogShipSeed
}

// techTreeRowPrereqs maps a seed's prerequisite ship ids into the row id space
// and drops any that no row in the document carries.
func techTreeRowPrereqs(ship mmogShipSeed, rowIDs map[int32]bool) []string {
	prereqs := []string{}
	for _, id := range []int32{ship.prereqID1, ship.prereqID2} {
		if id == 0 {
			continue
		}
		resolved := id
		if loadoutID, ok := dreadconfig.PrecastLoadoutIDForShip(id); ok {
			resolved = loadoutID
		}
		if !rowIDs[resolved] {
			continue
		}
		prereqs = append(prereqs, strconv.Itoa(int(resolved)))
	}
	return prereqs
}

func appendMmogTechTreeItem(b []byte, stack []int, item techTreeItem) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "Id", strconv.Itoa(int(item.id)))
	b = protocol.AppendStringField(b, "ClassId", strconv.Itoa(int(item.classID)))
	b = protocol.AppendStringField(b, "Manufacturer", strconv.Itoa(int(item.manufacturer)))
	b = protocol.AppendStringField(b, "Tier", strconv.Itoa(int(item.tier)))
	b = protocol.AppendStringField(b, "Position", strconv.Itoa(int(item.position)))
	// Visible gates the whole item: a falsy value makes the loader jump past the
	// rest of the entry, so the item is never stored, no manufacturer group is
	// created for it, and GetManufacturerData(0/1/2) finds nothing -- which is
	// what left the tech tree screen with "Could not find a manufacturer with
	// id 0" and an empty TreeWidgetList.
	//
	// The truthiness test is:
	//
	//	type < 1            -> skip
	//	type < 4 (bool/num) -> truthy = numeric slot != 0
	//	type == 4 (string)  -> truthy = (length - 1) > 0
	//
	// This deliberately uses the STRING branch with a two-character value.
	// ";;;;"-style one-character strings such as "1" evaluate FALSE there
	// (length 1 gives 0 > 0), and the earlier attempt at a bool relied on an
	// unverified assumption about where a bool node keeps its payload -- the
	// manufacturers stayed missing, so that assumption was wrong. The string
	// branch is read directly off the disassembly and needs no assumption: any
	// value of length 2 or more is true. "01" is also still numeric, so nothing
	// that parses it as a number gets a surprise.
	b = protocol.AppendStringField(b, "Visible", "01")
	b = protocol.AppendStringField(b, "XPCost", strconv.Itoa(int(item.xpCost)))
	b = protocol.AppendStringField(b, "FPCost", "0")
	b = protocol.AppendStringField(b, "NumTechTreeItemsRequired", strconv.Itoa(len(item.prereq)))
	b = protocol.AppendStringField(b, "ProxyType", strconv.Itoa(techTreeProxyTypeShip))
	// Prereq is an array the loader copies into the item's TArray<int32>, and
	// the entries are matched against other items' Id -- so they are loadout
	// ids, like Id itself. They used to be ship-PAWN ids, which named nothing
	// in the document and could not be admitted anyway: the gate compares the
	// top byte against YShipLoadoutPrecast (1) and YShipLoadoutHero (3), and a
	// pawn is 10.
	prereqs := make([]string, 0, len(item.prereq))
	for _, id := range item.prereq {
		prereqs = append(prereqs, strconv.Itoa(int(id)))
	}
	// Prereq goes out as a NAMED list, not a bare array. A bare array (0x0d)
	// parses to a container that keeps its children but discards their names,
	// and the client's field lookup treats a name-less container as indexable:
	// it resolves any field name to _wtoi(name), which is 0 for every
	// non-numeric name, and hands back child[0]. The loader read ProxyType off
	// this container once per prereq and got the prereq id, which then failed
	// its [-1, 9] range check -- "Invalid tech tree item type: 33489262" and
	// eleven more, which are exactly the twelve prereq ids of the twelve items
	// that carry one. Items with an empty Prereq never logged it, because the
	// fallback is guarded on childCount > 0.
	//
	// See AppendIndexedStringListField for the full mechanism. Positions are
	// unchanged, so the loader's stride-0x50 walk over the children still reads
	// the same ids in the same order.
	b, stack = protocol.AppendIndexedStringListField(b, stack, "Prereq", prereqs)
	// Wires are the connector lines drawn between nodes. Empty is valid -- the
	// nodes still render, just without the joining lines -- and the real
	// coordinates are a layout concern to solve once nodes appear at all. It
	// carries no children, so the name-less-container fallback above cannot
	// fire on it either way, but it is written the same way for consistency.
	b, stack = protocol.AppendIndexedStringListField(b, stack, "Wires", nil)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// compressMmogDocument zlib-compresses a document for a blob field. The client
// inflates with inflateInit_, i.e. a standard zlib stream with its 2-byte
// header -- not a raw deflate stream and not the length-prefixed form the
// save-game blobs use.
func compressMmogDocument(document []byte) []byte {
	var out bytes.Buffer
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(document); err != nil {
		logrus.WithError(err).Warn("mmog: compress tech tree document")
		return nil
	}
	if err := writer.Close(); err != nil {
		logrus.WithError(err).Warn("mmog: finish tech tree document")
		return nil
	}
	return out.Bytes()
}

// techTreeRowTier is the tier a tech tree row reports.
//
// This used to be inferred from unlockCost -- anything researchable was
// announced as tier 2 -- which misreported every row whose ship is not
// actually tier 2. Ceres is a tier-3 SupportMedium and was being sent as
// tier 2. The ship's registered asset path states its tier outright
// (/Ships/Support/Medium/T3/), so derive it and keep the cost heuristic only
// for rows whose id resolves to no tiered ship asset (the hero loadouts).
func techTreeRowTier(ship mmogShipSeed) int {
	if tier, ok := derivedShipTier(ship.id); ok {
		return tier
	}
	if ship.unlockCost > 0 {
		return 2
	}
	return 1
}

func appendMmogTechTreeRow(b []byte, stack []int, ship mmogShipSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	// Identity + structure the client uses to match this node against its
	// local static tech-tree definition, plus the dynamic unlock/ownership
	// state. All numeric scalars are numeric strings (the client's row parser
	// uses the restrictive double/int64/string-only tagged union — plain int32
	// silently reads as 0). Static presentation data (Name, weapon stats,
	// per-ship loadout info) is intentionally omitted — the client fills it
	// from its own Content assets keyed by ShipID/NodeID.
	// The client groups the tech tree by manufacturer and looks the groups up
	// by numeric id (see shipManufacturerID); a row without one cannot be
	// placed under any maker, which left every manufacturer page empty.
	if manufacturerID := shipManufacturerID(shipManufacturer(ship)); manufacturerID >= 0 {
		b = protocol.AppendStringField(b, "m_manufacturerID", strconv.Itoa(int(manufacturerID)))
		b = protocol.AppendStringField(b, "manufacturerId", strconv.Itoa(int(manufacturerID)))
	}
	// NodeID, ParentID, UnlockCost, PrereqID1, PrereqID2, bIsNew, bIsUnlocked
	// and bIsPurchased used to be emitted here and have been removed: none of
	// those names occurs anywhere in the shipping client binary, as ASCII or as
	// UTF-16LE (the same scan that identified the ten request names the client
	// never sends, run with a known-present control). Field lookup is by name,
	// so a name the binary does not contain cannot be read -- the tree's
	// unlock costs and prerequisites come from the TechTrees blob, whose loader
	// reads XPCost/FPCost/Prereq/Wires.
	//
	// This is a size fix, not a tidy-up. Frames carry a 16-bit length, so a
	// payload has to stay under 65535 bytes, and this response is already
	// ~13.7KB for the ten rows served today. The roster that belongs here is 51
	// tiered precast loadouts, which does not fit with dead fields attached.
	b = protocol.AppendStringField(b, "ShipID", strconv.Itoa(int(ship.id)))
	b = protocol.AppendStringField(b, "m_shipId", strconv.Itoa(int(ship.id)))
	b = protocol.AppendStringField(b, "NodeType", strconv.Itoa(int(ship.nodeType)))
	b = protocol.AppendStringField(b, "Tier", strconv.Itoa(techTreeRowTier(ship)))
	b = protocol.AppendStringField(b, "ShipClass", strconv.Itoa(int(ship.shipClass)))
	b = protocol.AppendStringField(b, "Weight", strconv.Itoa(int(ship.weight)))
	// REGRESSION FIX: for ships that have a starter loadout, emit
	// m_precastLoadoutID + m_shipLoadoutInfo. The client's hangar fleet loader
	// (YUIHangarFleetData::Load) builds each fleet ship from the loadout info
	// attached to its tech-tree node — stripping this (in the minimal-row pass)
	// made the fleet's ship array come back empty ("Invalid fleet data, fleet
	// array is empty" -> HandleMmogbrainError(8) -> fleet-manager bit 2 never
	// completes). Only nodes that actually have a loadout carry this, so the
	// frame stays small.
	if loadout, ok := starterLoadoutByShipID(ship.id); ok {
		b = protocol.AppendStringField(b, "m_precastLoadoutID", strconv.Itoa(int(loadout.precastLoadoutID)))
		b, stack = protocol.AppendObjectStart(b, stack, "m_shipLoadoutInfo")
		b, stack = appendMmogShipLoadoutInfoFields(b, stack, loadout)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// appendMmogModuleOwnershipEntry emits only the dynamic ownership state for a
// module. The client holds each module's static definition (weapon stats,
// prices, textures, tier) in its own Content, so only identity + owned/equipped
// need to come from the server.
func appendMmogModuleOwnershipEntry(b []byte, stack []int, module mmogModuleUIDataSeed) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "m_itemId", strconv.Itoa(int(module.itemID)))
	b = protocol.AppendStringField(b, "m_index", strconv.Itoa(int(module.index)))
	// Module UI data is stored per ship on the client; without the ship id an
	// entry belongs to no ship and ComposeModuleUiDataForShip finds nothing.
	b = protocol.AppendStringField(b, "m_shipId", strconv.Itoa(int(module.shipID)))
	b = protocol.AppendStringField(b, "m_techTreeItemState", strconv.Itoa(4))
	b = protocol.AppendBoolField(b, "m_isOwned", module.owned)
	b = protocol.AppendBoolField(b, "m_isEquipped", module.equipped)
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

func buildMmogCareerProgressionPayload(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetCareerProgression")
	b, _ = appendCareerGoalProgress(b, stack, playerPID)
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
	b, _ = appendCareerGoalsConfig(b, stack)
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

// appendMmogQuestsArray builds the "Quests" array read by YA_PlayerGet's
// top-level parser (FUN_142a70da0 -> FUN_142a69310, per-entry parser
// FUN_142a706f0). Per-entry fields are eid/id/act/cpl/prg/dif/ran — a
// different schema than YA_GetDailyContractsData's ContractID/Progress/
// State/etc, but backed by the same underlying active-contract data (no
// separate quest system exists server-side).
func appendMmogQuestsArray(b []byte, stack []int, playerPID string) ([]byte, []int) {
	// Quests/daily contracts intentionally sent EMPTY. The client's
	// UYPlayerMPQuestCycle::OnBackendDataAvailable (FUN_1403fe800/FUN_140404440)
	// enters an INFINITE mutual-recursion delegate broadcast when it is given
	// contract/quest backend data it can't drive to completion — confirmed via
	// crash minidump: a 6-function cycle (FUN_140404440->FUN_1403fe800->
	// FUN_1403feb30->FUN_140d18710->FUN_140d5b180->FUN_1402322a0) repeated
	// ~833x until stack overflow. Our seeded daily contracts (progress 0,
	// int32-typed Progress/Target fields the client reads as 0) triggered it.
	// Contracts aren't needed for hangar entry; an empty Quests array lets the
	// cycle terminate. Re-enable only with real, client-valid contract data +
	// progress tracking. See seedDailyContractsForPlayer (now a no-op).
	b, stack = protocol.AppendArrayStart(b, stack, "Quests")
	database := currentMmogPlayerStateDB()
	if database == nil {
		b, stack = protocol.AppendObjectEnd(b, stack)
		return b, stack
	}
	pid := normalizedPlayerStatePID(playerPID)
	// LIMIT 4 = 3 base slots + 1 elite slot. The client fills base slots first,
	// then the elite slot, in the order contracts arrive; sending only 3 leaves
	// the elite slot empty and UYPlayerMPQuestCycle loops resolving it.
	rows, err := database.Query(`SELECT contract_id, progress, state FROM player_contracts WHERE user_id=? AND state='active' ORDER BY created_at LIMIT 4`, pid)
	if err != nil {
		b, stack = protocol.AppendObjectEnd(b, stack)
		return b, stack
	}
	defer func() { _ = rows.Close() }()
	for idx := 0; rows.Next(); idx++ {
		var contractID, state string
		var progress int32
		if err := rows.Scan(&contractID, &progress, &state); err != nil {
			continue
		}
		// eid = the client's real YMPQ_ contract id (resolves against its loaded
		// MPQuestCollection). All numeric fields as strings (restrictive parser).
		// idx 3 = the elite slot -> harder difficulty.
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "eid", contractID)
		b = protocol.AppendStringField(b, "id", strconv.Itoa(idx))
		b = protocol.AppendStringField(b, "act", boolToOneZero(state == "active"))
		b = protocol.AppendStringField(b, "cpl", boolToOneZero(progress >= 100))
		b = protocol.AppendStringField(b, "prg", strconv.Itoa(int(progress)))
		b = protocol.AppendStringField(b, "dif", contractDifficulty(idx))
		b = protocol.AppendStringField(b, "ran", "0")
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func boolToOneZero(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// contractDifficulty maps a contract's slot index to its wire "dif" value. The
// first 3 (idx 0-2) are base slots (dif 1); idx 3 is the elite slot (dif 2).
func contractDifficulty(idx int) string {
	if idx >= 3 {
		return "2"
	}
	return "1"
}

func buildMmogDailyContractsDataPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int

	b = protocol.AppendStringField(b, "RT", "YA_GetDailyContractsData")
	b = protocol.AppendInt32Field(b, "DailyContractStateID", int32(dailyContractState(playerPID)))
	b = protocol.AppendInt32Field(b, "LastContractsAssignment", int32(time.Now().Unix()))
	b = protocol.AppendInt32Field(b, "DailyContractLastReplaceTime", int32(time.Now().Unix()))

	// Quests = the player's active daily contracts, using real YMPQ_ eids so
	// the client's daily-contract slots resolve (see dailyContractSeeds).
	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	b, stack = protocol.AppendArrayStart(b, stack, "Quests")
	if database != nil {
		// LIMIT 4 = 3 base + 1 elite slot (see buildMmogQuestsArray comment).
		rows, err := database.Query(`SELECT contract_id, payload, progress, state FROM player_contracts WHERE user_id=? AND state='active' ORDER BY created_at LIMIT 4`, pid)
		if err == nil {
			defer func() { _ = rows.Close() }()
			for idx := 0; rows.Next(); idx++ {
				var contractID, payloadJSON, state string
				var progress int32
				if err := rows.Scan(&contractID, &payloadJSON, &progress, &state); err != nil {
					continue
				}
				b, stack = protocol.AppendUnnamedObjectStart(b, stack)
				b = protocol.AppendStringField(b, "eid", contractID)
				b = protocol.AppendStringField(b, "id", strconv.Itoa(idx))
				b = protocol.AppendStringField(b, "act", boolToOneZero(state == "active"))
				b = protocol.AppendStringField(b, "cpl", boolToOneZero(progress >= 100))
				b = protocol.AppendStringField(b, "prg", strconv.Itoa(int(progress)))
				b = protocol.AppendStringField(b, "dif", contractDifficulty(idx))
				b = protocol.AppendStringField(b, "ran", "0")
				b, stack = protocol.AppendObjectEnd(b, stack)
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
	effectType   int32
	target       int32
	creditsPools []int32
	repPools     []int32
	multiplier   string
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
	waveNumber  int32
	enemyCount  int32
	eliteCount  int32
	bossWave    bool
	bossID      string
	timeLimit   int32
	rewardXP    int32
	rewardGP    int32
	description string
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
	mode        string
	highestWave int32
	totalWaves  int32
	bossKills   int32
	totalKills  int32
	bestScore   int32
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
	difficulty    string
	aiBehavior    string
	spawnRate     float32
	bossFrequency float32
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
	// Same body as YA_RequestStaticFleetData's FleetTypes/Maintenance, because
	// it is parsed by the same function.
	//
	// This response is dispatched on request slot interface+0x3740 -- the two
	// send sites, 0x142a1f2d5 and 0x142a40ec5, both bind that slot to
	// "YA_FleetEligibility" -- and the handler at 0x142a26e5b passes result to
	// FUN_142a78790, which is exactly the parser appendMmogStaticFleetTypeEntry
	// and appendMmogStaticFleetMaintenanceConfig were already written against.
	// The old body sent a "fleet_eligibility" array of FleetType/Reason pairs
	// and shared not one field name with what that parser reads, so it filled
	// nothing.
	//
	// FUN_142a78790 writes the array at interface+0x3c10, guarded by the flag
	// at +0x3c3c. Two consumers were left reading an empty array: the AI-ship
	// spawner in YGameMode_Multiplayer, which needs Tiers and otherwise logs
	// "No mmog tier data available for spawning AI ships, using default
	// hardcoded data", and the fourth UYFleetManager readiness bit, whose data
	// holder is that same +0x3c10 / +0x3c3c pair.
	//
	// The AllowedTiers this sends -- {1,2}, {2,3}, {4,5} -- are corroborated
	// independently: they are exactly the per-fleet-type min/max pairs the
	// client falls back to at 0x14036e2xx when the data is missing.
	b, stack = protocol.AppendArrayStart(b, stack, "FleetTypes")
	for _, eligibility := range configBackedFleetEligibilities() {
		b, stack = appendMmogStaticFleetTypeEntry(b, stack, eligibility)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	// Maintenance is resolved on the result object, not per entry
	// (FUN_140237c30(param_2, ...) at 0x142a78790+0x414, against
	// FUN_140237c30(lVar5, ...) for every field above).
	b, stack = appendMmogStaticFleetMaintenanceConfig(b, stack)
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
	// issue #52: "ReturnValue" only matches generic UFUNCTION reflection
	// boilerplate (present for every reflected function with a return type
	// in the binary) — no occurrence tied to YA_CheckReturn or MMOG-response
	// parsing specifically. Removed as a likely-fabricated field.
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
	extractedShipIDValcour:   5000,
	extractedShipIDTrafalgar: 5000,
	extractedShipIDNav:       5000,
	extractedShipIDCeres:     5000,
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
// dailyContractSeeds MUST use the client's real YMPQ_ contract IDs (from
// MPQuestCollection.m_dailyContractsConfig.m_initialContracts). The client's
// daily-contract system (UYPlayerMPQuestCycle) resolves each active contract's
// "eid" against its locally-loaded MPQuestCollection quest assets. Fabricated
// ids ("contract_kills_5" etc.) never match any loaded YMPQ_ quest, so the
// contract slots never fill, the cycle keeps re-generating/reloading, and it
// spams EYA_MenuNewQuest until the stack overflows.
//
// The config (MPQuestCollection.m_dailyContractsConfig) declares 4 slots:
// m_numBaseContractSlots=3 + m_numEliteContractSlots=1. The elite slot is NOT
// gated on membership at fill time — the client fills base slots first, then the
// elite slot, from the contracts it receives in order. The daily-contract wire
// struct (FUN_142a706f0: eid/id/act/cpl/prg/dif/ran) carries NO per-contract
// elite flag; elite is decided purely by slot position. So if we send only 3
// contracts, the elite slot stays empty and UYPlayerMPQuestCycle loops trying to
// resolve/fill it. Seed all 4 slots: 3 base + 1 elite (the 4th entry fills the
// elite slot). dif=2 on the elite one for a harder target.
var dailyContractSeeds = []struct {
	id, name, description    string
	targetKills, targetScore int32
	rewardXP, rewardGP       int32
}{
	{"YMPQ_Kills", "Kills", "Eliminate enemy ships", 10, 0, 500, 1000},
	{"YMPQ_CompleteMatches", "Complete Matches", "Complete matches", 3, 0, 300, 600},
	{"YMPQ_WinMatches", "Win Matches", "Win matches", 1, 0, 400, 800},
	{"YMPQ_ModuleKills", "Module Kills", "Destroy enemy modules", 15, 0, 800, 1600},
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
	// Seed the 4 daily contracts (3 base + 1 elite) using the client's real YMPQ_ contract
	// ids so the client's daily-contract slots resolve against its loaded
	// MPQuestCollection quests (see dailyContractSeeds). Sending valid ids (vs
	// the old fabricated "contract_*" ids) is what stops the quest-cycle
	// recursion / EYA_MenuNewQuest notification flood.
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM player_contracts WHERE user_id=? AND state='active'`, pid).Scan(&count)
	if count >= len(dailyContractSeeds) {
		return
	}
	for i := 0; i < len(dailyContractSeeds); i++ {
		seed := dailyContractSeeds[i]
		payload, _ := json.Marshal(map[string]interface{}{
			"id": seed.id, "name": seed.name, "description": seed.description,
			"targetKills": seed.targetKills, "targetScore": seed.targetScore,
			"rewardXP": seed.rewardXP, "rewardGP": seed.rewardGP,
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

// buildMmogSavePlayerDisplayInformationPayload answers the captain
// registration/appearance save.
//
// The client's handler for this response does two things, and a generic success
// payload satisfies neither:
//
//   - It reads "PID", parses it strictly as a GUID, and compares it against the
//     player it already knows. On a mismatch -- which is what an absent field
//     produces, since the missing value parses to an all-zero GUID -- it
//     broadcasts mmogbrain error 0x10. That is the error UYCaptain picks up and
//     logs as "HandleMmogbrainError | General MMogbrain captain display
//     information error", once per save.
//   - Only on a match does it read "disp", store it as the live captain
//     appearance, and broadcast the display-information-updated delegate that
//     the captain UI listens on.
//
// So the response has to echo both fields. persistMmogPlayerMutation has
// already run by this point, so the state read here is the value just saved.
func buildMmogSavePlayerDisplayInformationPayload(requestName string, playerPID string) []byte {
	state := mmogPlayerStateForPID(playerPID)

	var b []byte
	b = protocol.AppendStringField(b, "RT", requestName)
	b = protocol.AppendStringField(b, "PID", playerPID)
	b = protocol.AppendStringField(b, "disp", state.displayInfo)
	return b
}

// buildMmogRewardCurrenciesPayload reports the player's credit and GP balances.
//
// This is the only channel the client has for them. Its HUD reads
// FPlayerCurrencyAmountsData{m_freeXP, m_softCurrency, m_hardCurrency}; m_freeXP
// comes from YA_PlayerGet's "FreeXp", but a complete enumeration of that
// parser's 47 field lookups contains no currency field at all, so soft and hard
// currency have to arrive here.
//
// The YA_RewardCurrencies handler reads root-level "Credits" and "Points" and
// ASSIGNS them (mov [iface+0x3be4], Credits / mov [iface+0x3be0], Points) rather
// than adding, so sending the current balance is idempotent and safe to repeat
// on every login despite the "Reward" in the name.
//
// Both values go through FUN_1402380b0 -- the same accessor family that only
// understands double/int64/string and silently reads an int32 wire field as 0 --
// so they must be numeric strings.
func buildMmogRewardCurrenciesPayload(playerPID string) []byte {
	state := mmogPlayerStateForPID(playerPID)

	// "result" here is a plain STRING compared against "ok", not the usual
	// result{status:"ok"} object. The handler does
	//
	//	call 0x140237c30            ; GetField(response, "result")
	//	call 0x140237ef0            ; AsString(node, &out)
	//	call 0x14022d590            ; strcmp(out, "ok")
	//	jne  0x142a2c5ea            ; skip BOTH assignments
	//
	// and an object node yields an empty string from AsString, so sending the
	// standard success envelope silently skipped the currency writes entirely.
	var b []byte
	b = protocol.AppendStringField(b, "RT", "YA_RewardCurrencies")
	b = protocol.AppendStringField(b, "result", "ok")
	b = protocol.AppendStringField(b, "Credits", strconv.Itoa(int(state.softCurrency)))
	b = protocol.AppendStringField(b, "Points", strconv.Itoa(int(state.premiumCurrency)))
	return b
}
