package main

import (
	"strconv"
	"strings"
	"sync"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Custom rooms (2026-11-04, exploration). With DN_CUSTOM_GAME_TILE=1 the
// client's Custom Game tile is listed (matchmaker.customGameListed); it opens
// the custom match lobby (UI_Screen_CustomMatchLobby). The first try got the
// generic room success and the client logged "YA_CustomRoomCreateResponse -
// Creating custom room!" then "Could not find the custom room user data for
// the local user": an empty room, empty mode and map lists.
//
// What the client reads, verified in the exe:
//
//	dispatcher  YA_CustomRoomCreateResponse (0x142A2AF87..): the ROOT node
//	            "roomInfo" -> parser 0x142A69BB0 into the mmog room record
//	            (+0x3A68, 0xC8 bytes), in-room flag +0x3B08 set; then the
//	            local player is the host (room hostPid == own PID, mmog
//	            +0x3898) or must be found among the room's users (vtable
//	            +0x90), else the error above.
//	roomInfo    roomId, hostPid (strings, 0x142A1D450), teams (at most 2
//	            arrays of users; more fails the parse), gameMode, mapName,
//	            settings (array of {id 0-3, value string}; id 3 is parsed as
//	            a JSON object, 0x142A6AEE0), supportedModes (array of
//	            {gameMode, supportedMaps: [map names]} -> the mode/map
//	            choices), chatRoomId.
//	user        0x142A6B0D0: userPid, DisplayName, team (0 or 1), slot,
//	            info {UnlockedFleetType 0-3, Rank, DisplayInfo}, state (0 or
//	            1), validFleets (each 1-3); out of range fails the user.
//
// Numbers go as numeric strings (the client's scalar reader converts them).
// Every room request is still logged with its readable text. GUESSES: the
// meaning of setting ids 0-2 (the create request's InitialSettings carried
// three values, 8/0/0, in that order) and of state; one room per host, kept
// in memory (lost on restart); joining, team switches and starting a match
// are not built.

type customRoomUser struct {
	pid  string
	team int
	slot int
}

type customRoom struct {
	id, host      string
	mode, mapName string
	settings      []string // by setting id
	users         []customRoomUser
}

var customRooms = struct {
	sync.Mutex
	byPlayer map[string]*customRoom
}{byPlayer: map[string]*customRoom{}}

// handleCustomRoomRequest answers the room requests it implements; ok false
// leaves the rest to the generic reply.
func handleCustomRoomRequest(requestName, playerPID string, payload []byte) ([]byte, bool) {
	pid := normalizedPlayerStatePID(playerPID)
	customRooms.Lock()
	defer customRooms.Unlock()
	switch requestName {
	case "YA_CustomRoomCreate":
		room := &customRoom{
			id:       strings.ReplaceAll(uuid.New().String(), "-", ""),
			host:     pid,
			mode:     customRoomMode(protocol.ExtractStringField(payload, "GameMode")),
			settings: protocol.ExtractStringFields(payload, "Value"),
			users:    []customRoomUser{{pid: pid}},
		}
		room.mapName = customRoomMap(room.mode, protocol.ExtractStringField(payload, "MapName"))
		customRooms.byPlayer[pid] = room
		logrus.WithFields(logrus.Fields{"room": room.id, "pid": pid, "mode": room.mode, "map": room.mapName,
			"settings": room.settings}).Info("custom room: created")
		return buildCustomRoomPayload(mmogRoomResponseName(requestName), room), true
	case "YA_CustomRoomChangeSettings", "YA_CustomRoomUpdate":
		room := customRooms.byPlayer[pid]
		if room == nil {
			return nil, false
		}
		if mode := protocol.ExtractStringField(payload, "GameMode"); mode != "" {
			room.mode = customRoomMode(mode)
		}
		if m := protocol.ExtractStringField(payload, "MapName"); m != "" || room.mapName == "" {
			room.mapName = customRoomMap(room.mode, m)
		}
		if vals := protocol.ExtractStringFields(payload, "Value"); len(vals) > 0 {
			room.settings = vals
		}
		if requestName == "YA_CustomRoomChangeSettings" {
			return buildCustomRoomSettingsPayload(mmogRoomResponseName(requestName), room), true
		}
		return buildCustomRoomPayload(mmogRoomResponseName(requestName), room), true
	case "YA_CustomRoomUserLeave":
		if room := customRooms.byPlayer[pid]; room != nil {
			delete(customRooms.byPlayer, pid)
			logrus.WithFields(logrus.Fields{"room": room.id, "pid": pid}).Info("custom room: left")
		}
	}
	return nil, false
}

// customRoomMode is a mode the room offers: the one asked for, else TDM.
func customRoomMode(mode string) string {
	for _, m := range matchmaker.CustomRoomModes() {
		if strings.EqualFold(m.Mode, mode) {
			return m.Mode
		}
	}
	return "TDM"
}

// customRoomMap is a map the mode offers: the one asked for, else its first.
func customRoomMap(mode, mapName string) string {
	for _, m := range matchmaker.CustomRoomModes() {
		if m.Mode != mode || len(m.Maps) == 0 {
			continue
		}
		for _, name := range m.Maps {
			if strings.EqualFold(name, mapName) {
				return name
			}
		}
		return m.Maps[0]
	}
	return mapName
}

// buildCustomRoomSettingsPayload is the settings change reply. Its handler
// (YA_CustomRoomSettingsUpdateResponse, dispatcher 0x142A294B7..) reads ROOT
// "gameMode" and "mapName" into the room record (+0x10, +0x20) and the ROOT
// "settings" array (0x142A6AEE0 each) into +0x40, then broadcasts the update
// (0x142AB0B10). Sent inside roomInfo (first try, 2026-11-04 18:48) they read
// empty, the room's mode and map were cleared, and the lobby logged
// "GetSelectableMaps: Game mode not yet selected!" (0x140323720: maps are the
// selected mode's supportedMaps) and "GetSelectableGameModes: Map not yet
// selected!".
func buildCustomRoomSettingsPayload(responseName string, room *customRoom) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", responseName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "gameMode", room.mode)
	b = protocol.AppendStringField(b, "mapName", room.mapName)
	b, _ = appendCustomRoomSettings(b, stack, room)
	return b
}

func appendCustomRoomSettings(b []byte, stack []int, room *customRoom) ([]byte, []int) {
	b, stack = protocol.AppendArrayStart(b, stack, "settings")
	for id, v := range room.settings {
		if id > 2 { // id 3 is a JSON object, not sent
			break
		}
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "id", strconv.Itoa(id))
		b = protocol.AppendStringField(b, "value", v)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	return protocol.AppendObjectEnd(b, stack)
}

func buildCustomRoomPayload(responseName string, room *customRoom) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", responseName)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, fieldStatus, "ok")
	b = protocol.AppendInt32Field(b, "Code", 0)
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, stack = protocol.AppendObjectStart(b, stack, "roomInfo")
	b = protocol.AppendStringField(b, "roomId", room.id)
	b = protocol.AppendStringField(b, "hostPid", room.host)
	b, stack = protocol.AppendArrayStart(b, stack, "teams")
	for team := 0; team < 2; team++ {
		b, stack = protocol.AppendUnnamedArrayStart(b, stack)
		for _, u := range room.users {
			if u.team == team {
				b, stack = appendCustomRoomUser(b, stack, u)
			}
		}
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "gameMode", room.mode)
	b = protocol.AppendStringField(b, "mapName", room.mapName)
	b, stack = appendCustomRoomSettings(b, stack, room)
	b, stack = protocol.AppendArrayStart(b, stack, "supportedModes")
	for _, m := range matchmaker.CustomRoomModes() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "gameMode", m.Mode)
		b, stack = protocol.AppendStringArrayField(b, stack, "supportedMaps", m.Maps)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "chatRoomId", "")
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

func appendCustomRoomUser(b []byte, stack []int, u customRoomUser) ([]byte, []int) {
	state := mmogPlayerStateForPID(u.pid)
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "userPid", u.pid)
	b = protocol.AppendStringField(b, "DisplayName", state.displayName)
	b = protocol.AppendStringField(b, "team", strconv.Itoa(u.team))
	b = protocol.AppendStringField(b, "slot", strconv.Itoa(u.slot))
	b, stack = protocol.AppendObjectStart(b, stack, "info")
	b = protocol.AppendStringField(b, "UnlockedFleetType", strconv.Itoa(customRoomFleetType(state, u.pid)))
	b = protocol.AppendStringField(b, "Rank", strconv.Itoa(int(state.currentRank)))
	b = protocol.AppendStringField(b, "DisplayInfo", state.displayInfo)
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "state", "0")
	var fleets []string
	for _, f := range unlockedFleets(state, u.pid, state.fleets) {
		if f.fleetType >= 1 && f.fleetType <= 3 {
			fleets = append(fleets, strconv.Itoa(int(f.fleetType)))
		}
	}
	b, stack = protocol.AppendStringArrayField(b, stack, "validFleets", fleets)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

// customRoomFleetType is the player's highest unlocked fleet (EYFleetType 1-3).
func customRoomFleetType(state mmogPlayerState, pid string) int {
	best := 1
	for _, f := range unlockedFleets(state, pid, state.fleets) {
		if int(f.fleetType) > best && f.fleetType <= 3 {
			best = int(f.fleetType)
		}
	}
	return best
}

// logCustomRoomRequest logs a room request and the readable strings in it.
func logCustomRoomRequest(requestName, playerPID string, payload []byte) {
	logrus.WithFields(logrus.Fields{
		"request": requestName,
		"pid":     playerPID,
		"bytes":   len(payload),
		"text":    payloadText(payload, 40),
	}).Info("custom room: request")
}

// payloadText is the runs of 3+ printable ASCII bytes in a binary payload
// (field names and string values), at most max of them, joined by "|".
func payloadText(payload []byte, max int) string {
	var runs []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 3 && len(runs) < max {
			runs = append(runs, string(payload[start:end]))
		}
		start = -1
	}
	for i, b := range payload {
		if b >= 0x20 && b < 0x7f {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(payload))
	return strings.Join(runs, "|")
}
