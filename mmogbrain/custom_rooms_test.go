package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func TestPayloadText(t *testing.T) {
	got := payloadText([]byte("\x01\x02RT\x00\x05YA_CustomRoomCreate\x00\x0aMapName\x00ab\x00Highlands"), 10)
	if got != "YA_CustomRoomCreate|MapName|Highlands" {
		t.Fatalf("payloadText = %q", got)
	}
}

// A created room names its host (the client's own PID) and has it in team 0,
// with the mode and map lists the lobby's dropdowns are filled from.
func TestCustomRoomCreateReply(t *testing.T) {
	pid := "c0570000000000000000000000000001"
	req := protocol.AppendStringField(protocol.AppendStringField(nil, "GameMode", "Onslaught"), "MapName", "ANY")
	reply, ok := handleCustomRoomRequest("YA_CustomRoomCreate", pid, req)
	if !ok {
		t.Fatal("create not handled")
	}
	for _, want := range []string{"YA_CustomRoomCreateResponse", "roomInfo", "hostPid", pid, "teams", "userPid",
		"supportedModes", "supportedMaps", "Escalation", "chatRoomId", "validFleets"} {
		if !bytes.Contains(reply, []byte(want)) {
			t.Errorf("reply lacks %q", want)
		}
	}
	if _, ok := handleCustomRoomRequest("YA_CustomRoomUserLeave", pid, nil); ok {
		t.Error("leave should keep the generic reply")
	}
	if customRooms.byPlayer[pid] != nil {
		t.Error("room kept after leave")
	}
}

// The settings reply carries gameMode / mapName / settings at the ROOT.
func TestCustomRoomSettingsReplyAtRoot(t *testing.T) {
	pid := "c0570000000000000000000000000002"
	handleCustomRoomRequest("YA_CustomRoomCreate", pid, protocol.AppendStringField(nil, "GameMode", "TDM"))
	defer handleCustomRoomRequest("YA_CustomRoomUserLeave", pid, nil)
	reply, ok := handleCustomRoomRequest("YA_CustomRoomChangeSettings", pid,
		protocol.AppendStringField(protocol.AppendStringField(nil, "GameMode", "Escalation"), "MapName", "Gorge"))
	if !ok {
		t.Fatal("settings change not handled")
	}
	if bytes.Contains(reply, []byte("roomInfo")) {
		t.Error("settings reply nests the room; the client reads root fields")
	}
	if got := protocol.ExtractStringField(reply, "gameMode"); got != "Escalation" {
		t.Errorf("gameMode = %q", got)
	}
	if got := protocol.ExtractStringField(reply, "mapName"); got != "Gorge" {
		t.Errorf("mapName = %q", got)
	}
}
