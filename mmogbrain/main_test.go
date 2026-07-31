//nolint:goconst,gosec // MMOG regression tests intentionally repeat protocol identifiers and bounded protocol-size casts.
package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	mmogdb "github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

type captureConn struct {
	bytes.Buffer
}

func appendFieldMarker(name string, fieldType byte) []byte {
	b := make([]byte, 0, len(name)+2)
	b = append(b, byte(len(name)))
	b = append(b, name...)
	b = append(b, fieldType)
	return b
}

func (c *captureConn) Read(_ []byte) (int, error)         { return 0, nil }
func (c *captureConn) Write(p []byte) (int, error)        { return c.Buffer.Write(p) }
func (c *captureConn) Close() error                       { return nil }
func (c *captureConn) LocalAddr() net.Addr                { return nil }
func (c *captureConn) RemoteAddr() net.Addr               { return nil }
func (c *captureConn) SetDeadline(_ time.Time) error      { return nil }
func (c *captureConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *captureConn) SetWriteDeadline(_ time.Time) error { return nil }

func mustSignTestJWT(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

func writeFirmamentTestMessage(t *testing.T, conn net.Conn, payload map[string]any) {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal firmament Payload: %v", err)
	}
	encoded = append(encoded, '\r', '\n')
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("write firmament Payload: %v", err)
	}
}

func readFirmamentTestMessage(t *testing.T, conn net.Conn, reader *bufio.Reader, timeout time.Duration) map[string]any {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read firmament Payload: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &payload); err != nil {
		t.Fatalf("decode firmament payload %q: %v", string(line), err)
	}
	return payload
}

func useTempMmogPlayerStateDB(t *testing.T) *sql.DB {
	t.Helper()

	database, err := mmogdb.Open(t.TempDir() + "/mmog.db")
	if err != nil {
		t.Fatalf("open temp mmog db: %v", err)
	}
	setMmogPlayerStateDB(database)
	t.Cleanup(func() {
		setMmogPlayerStateDB(nil)
		if err := database.Close(); err != nil {
			t.Fatalf("close temp mmog db: %v", err)
		}
	})
	return database
}

func extractNamedMmogContainer(t *testing.T, payload []byte, name string, fieldType byte) []byte {
	t.Helper()

	marker := appendFieldMarker(name, fieldType)
	idx := bytes.Index(payload, marker)
	if idx < 0 {
		t.Fatalf("missing %s container", name)
	}

	sizeOffset := idx + len(marker)
	if len(payload) < sizeOffset+4 {
		t.Fatalf("%s container header truncated", name)
	}

	size := int(binary.LittleEndian.Uint32(payload[sizeOffset : sizeOffset+4]))
	if size < 6 {
		t.Fatalf("%s container size = %d, want at least 6", name, size)
	}

	end := sizeOffset + size
	if end > len(payload) {
		t.Fatalf("%s container end = %d, payload len = %d", name, end, len(payload))
	}

	return payload[sizeOffset+4 : end]
}

func extractNamedMmogObject(t *testing.T, payload []byte, name string) []byte {
	t.Helper()
	return extractNamedMmogContainer(t, payload, name, 0x0c)
}

func extractNamedMmogArray(t *testing.T, payload []byte, name string) []byte {
	t.Helper()
	return extractNamedMmogContainer(t, payload, name, 0x0d)
}

func validateMmogPayloadNesting(t *testing.T, payload []byte) {
	t.Helper()

	rootEnd := []byte{0x00, 0x0e, 0x00, 0x00, 0x00, 0x00}
	if len(payload) < len(rootEnd) || !bytes.Equal(payload[len(payload)-len(rootEnd):], rootEnd) {
		t.Fatal("payload missing root terminator")
	}

	var stack []int
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if i+nameLen > len(payload) {
			t.Fatalf("field name overruns payload at byte %d", i-1)
		}
		i += nameLen
		if i >= len(payload) {
			t.Fatalf("field type truncated at byte %d", i)
		}

		fieldType := payload[i]
		i++

		switch fieldType {
		case 0x05:
			if i >= len(payload) {
				t.Fatalf("bool value truncated at byte %d", i)
			}
			i++
		case 0x09, 0x0a:
			if i+4 > len(payload) {
				t.Fatalf("string/bytes length truncated at byte %d", i)
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if i+valueLen > len(payload) {
				t.Fatalf("string/bytes overruns payload at byte %d", i)
			}
			i += valueLen
		case 0x56:
			if i+4 > len(payload) {
				t.Fatalf("int32 value truncated at byte %d", i)
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				t.Fatalf("container length truncated at byte %d", i)
			}
			stack = append(stack, i)
			i += 4
		case 0x0e:
			if nameLen != 0 {
				t.Fatalf("container terminator should be unnamed at byte %d", i-2)
			}
			if i+4 > len(payload) {
				t.Fatalf("container terminator offset truncated at byte %d", i)
			}
			start := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if start == 0 {
				if i != len(payload) {
					t.Fatalf("root terminator found before payload end at byte %d", i)
				}
				if len(stack) != 0 {
					t.Fatalf("root terminator reached with %d unclosed containers", len(stack))
				}
				continue
			}
			if len(stack) == 0 {
				t.Fatalf("unexpected container terminator for offset %d", start)
			}
			wantStart := stack[len(stack)-1]
			if start != wantStart {
				t.Fatalf("container terminator offset = %d, want %d", start, wantStart)
			}
			// A container's declared size covers its contents plus the 6-byte
			// terminator, measured from just after the length field. It does
			// NOT include the length field itself -- confirmed against frames
			// the client sent us. See protocol.AppendObjectEnd.
			size := int(binary.LittleEndian.Uint32(payload[start : start+4]))
			if size < 6 {
				t.Fatalf("container size = %d at offset %d, want at least 6", size, start)
			}
			if start+4+size != i {
				t.Fatalf("container at offset %d closes at %d, want %d", start, start+4+size, i)
			}
			stack = stack[:len(stack)-1]
		default:
			t.Fatalf("unsupported MMOG field type 0x%02x at byte %d", fieldType, i-1)
		}
	}
}

func TestExtractMmogPlayerPIDFromLoginTicket(t *testing.T) {
	const userID = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": userID, "iss": protocol.GatewayJWTIssuer, "aud": "dreadnought", "realm": "dreadnought.pc-us", "exp": time.Now().Add(time.Hour).Unix()})
	ticket, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	payload := protocol.AppendStringField(nil, "RT", "YA_UserLogin")
	payload = protocol.AppendStringField(payload, "Ticket", ticket)
	payload = append(payload, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)

	got := protocol.ExtractPlayerPID(payload, defaultMmogPlayerPID, []byte("test-secret"))
	want := "b7c42c0f3ac648a182ccfd35eb24f128"
	if got != want {
		t.Fatalf("player PID = %q, want %q", got, want)
	}
}

func TestExtractMmogPlayerPIDRejectsUnsignedJWT(t *testing.T) {
	const userID = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": userID, "iss": protocol.GatewayJWTIssuer, "aud": "dreadnought", "realm": "dreadnought.pc-us", "exp": time.Now().Add(time.Hour).Unix()})
	ticket, err := token.SignedString([]byte("wrong-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	payload := protocol.AppendStringField(nil, "RT", "YA_UserLogin")
	payload = protocol.AppendStringField(payload, "Ticket", ticket)
	payload = append(payload, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)

	got := protocol.ExtractPlayerPID(payload, defaultMmogPlayerPID, []byte("test-secret"))
	if got != defaultMmogPlayerPID {
		t.Fatalf("invalid JWT player PID = %q, want default", got)
	}
}

func TestPlayerDataResponsesUseHexPlayerPID(t *testing.T) {
	const pid = "b7c42c0f3ac648a182ccfd35eb24f128"

	for name, payload := range map[string][]byte{
		"YA_PlayerGet":    buildMmogRequestResponsePayload("YA_PlayerGet", pid, buildMmogPlayerDataPayload("YA_PlayerGet", pid)),
		"YA_PlayerFleets": buildMmogRequestResponsePayload("YA_PlayerFleets", pid, buildMmogPlayerFleetsPayload(pid)),
	} {
		// The client resolves "PID" via a find-only FName lookup against the
		// same hyphen-stripped hex form it already interned locally (see
		// AGENTS.md's mmogbrain gotcha on PID format) — sending a hyphenated
		// GUID here makes that lookup fail silently and the client never
		// completes its post-login bootstrap. Confirmed via a live memory
		// dump of a hung client session (2026-07-18).
		validPIDField := protocol.AppendStringField(nil, "PID", pid)
		invalidPIDField := protocol.AppendStringField(nil, "PID", "local")
		if !bytes.Contains(payload, validPIDField) {
			t.Fatalf("%s response does not include player PID %q", name, pid)
		}
		if bytes.Contains(payload, invalidPIDField) {
			t.Fatalf("%s response still contains invalid local PID", name)
		}
	}
}

func TestPlayerStatsCounterDataUsesArray(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := persistMmogPlayerMutation(playerPID, "YA_IncrementPlayerStatsCounter",
		realIncrementRequest("Customize", "Captain", 1)); err != nil {
		t.Fatal(err)
	}
	payload := buildMmogPlayerStatsCounterDataPayload(playerPID)

	if !bytes.Contains(payload, appendFieldMarker("counterData", 0x0d)) {
		t.Fatalf("stats counter response does not expose counterData as an array")
	}
	if bytes.Contains(payload, appendFieldMarker("counterData", 0x0c)) {
		t.Fatalf("stats counter response still exposes counterData as an object")
	}
	// Both the root and the result copy carry the rows. This used to assert an
	// int32 counterId, which was the bug: the client sends counterId and
	// counterSubId as STRINGS ("Customize"/"Captain"), and an int32 reads back
	// as 0 through its value accessors anyway.
	if got := bytes.Count(payload, protocol.AppendStringField(nil, "counterId", "Customize")); got < 2 {
		t.Fatalf("counterId appears %d times, want it in both the root and result arrays", got)
	}
	if bytes.Contains(payload, protocol.AppendInt32Field(nil, "counterId", 0)) {
		t.Fatal("the hardcoded int32 counterId row is back")
	}
}

func TestPlayerGetPayloadUsesTopLevelPlayerData(t *testing.T) {
	payload := buildMmogPlayerGetPayload(defaultMmogPlayerPID)

	if bytes.Contains(payload, appendFieldMarker("result", 0x0c)) {
		t.Fatal("YA_PlayerGet must not wrap player data in result")
	}
	for _, field := range []struct {
		name      string
		fieldType byte
	}{
		{name: "PID", fieldType: 0x09},
		{name: "ShipLoadouts", fieldType: 0x0d},
		// ServerTime/ClientTime: the client's YA_PlayerGet parser reads these
		// through the same int32-blind tagged union confirmed for
		// tll/tpl/tc/rep/FreeXp/etc — only double/int64/string are recognized,
		// so these must be numeric strings (0x09), not int32 (0x56).
		{name: "ServerTime", fieldType: 0x09},
		{name: "ClientTime", fieldType: 0x09},
	} {
		if !bytes.Contains(payload, appendFieldMarker(field.name, field.fieldType)) {
			t.Fatalf("YA_PlayerGet missing top-level field %s", field.name)
		}
	}
}

func TestUserLoginPayloadKeepsEconomyFieldsOnResult(t *testing.T) {
	// issue #50: the client's YA_UserLogin "ok" handler (FUN_142a3af90) never
	// reads flat result.credits/premiumCurrency/freexp/xp — only nested
	// result.LoginStreak.{loginstreak,credits,freexp,gp}. Those flat fields
	// are dead for this RT (the real balance goes out via YA_PlayerGet), so
	// this payload must NOT send them, only the nested LoginStreak object.
	payload := buildMmogLoginSuccessPayload()
	result := extractNamedMmogObject(t, payload, "result")
	loginStreak := extractNamedMmogObject(t, result, "LoginStreak")

	if !bytes.Contains(result, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatal("YA_UserLogin result missing status=ok")
	}
	// credits/freexp legitimately appear nested inside LoginStreak — only
	// check they (and premiumCurrency/xp, gone entirely) aren't ALSO sent
	// flat, outside that nested object.
	outerResult := bytes.Replace(result, loginStreak, nil, 1)
	for _, name := range []string{"credits", "premiumCurrency", "freexp", "xp"} {
		if bytes.Contains(outerResult, appendFieldMarker(name, 0x56)) {
			t.Fatalf("YA_UserLogin result should not send dead flat field %s", name)
		}
	}
	for _, name := range []string{"loginstreak", "credits", "freexp", "gp"} {
		if !bytes.Contains(loginStreak, appendFieldMarker(name, 0x56)) {
			t.Fatalf("YA_UserLogin LoginStreak missing %s", name)
		}
	}
	if !bytes.Contains(loginStreak, protocol.AppendInt32Field(nil, "loginstreak", 0)) {
		t.Fatal("YA_UserLogin LoginStreak missing loginstreak")
	}
}

func TestTechTreeRowsExposeMinimalIdentityAndUnlock(t *testing.T) {
	// The minimal tech tree conveys per-node identity + unlock/ownership only;
	// static presentation (Name, weight labels, loadout info, weapon stats)
	// comes from the client's own Content. Verify each validated T1+T2 node
	// carries its identity fields and the correct unlock flag.
	for _, ship := range t1t2TechTreeShips {
		row, _ := appendMmogTechTreeRow(nil, nil, ship)
		if !bytes.Contains(row, protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(ship.id)))) {
			t.Fatalf("tech tree row for %q missing ShipID=%d", ship.name, ship.id)
		}
		// ShipClass goes out ONE-BASED (0 means "no class"), not as the raw
		// internal ordinal. Established from what the client rendered for the
		// starter fleet: Rurik (ArtilleryCruiser, sent 2) showed as "Corvette",
		// Cerberus (TacticalCruiser, sent 3) showed as "Artillery Cruiser", and
		// Simargl (Dreadnought, sent 0) showed no class at all -- i.e.
		// displayed = table[sent-1]. See mmogShipClassWire.
		wireClass := mmogShipClassWire(ship.shipClass)
		if !bytes.Contains(row, protocol.AppendStringField(nil, "ShipClass", strconv.Itoa(int(wireClass)))) {
			t.Fatalf("tech tree row for %q missing ShipClass=%d (internal %d)", ship.name, wireClass, ship.shipClass)
		}
		if !bytes.Contains(row, protocol.AppendStringField(nil, "Weight", strconv.Itoa(int(ship.weight)))) {
			t.Fatalf("tech tree row for %q missing Weight=%d", ship.name, ship.weight)
		}
		// These names occur nowhere in the client binary, so the client cannot
		// read them; they were costing ~180 bytes a row on a payload that has
		// to fit a 16-bit frame length. Assert they stay gone.
		for _, dead := range []string{"NodeID", "ParentID", "UnlockCost", "PrereqID1", "PrereqID2", "bIsNew", "bIsUnlocked", "bIsPurchased"} {
			if bytes.Contains(row, appendFieldMarker(dead, 0x09)) || bytes.Contains(row, appendFieldMarker(dead, 0x05)) {
				t.Fatalf("tech tree row for %q re-added dead field %s", ship.name, dead)
			}
		}
		// m_shipLoadoutInfo IS required for ships that have a starter loadout —
		// the client's hangar fleet loader builds each fleet ship from it (see
		// appendMmogTechTreeRow). Ships without a loadout must not carry it.
		if _, hasLoadout := starterLoadoutByShipID(ship.id); hasLoadout {
			if !bytes.Contains(row, appendFieldMarker("m_shipLoadoutInfo", 0x0c)) {
				t.Fatalf("tech tree row for %q (has loadout) must include m_shipLoadoutInfo", ship.name)
			}
		}
	}
}

func TestTechTreeIncludesInstallerStarterShips(t *testing.T) {
	// The player's owned starter ships (the 4 T1 ships) must be present in the
	// minimal tech tree so the hangar can select them. Fleet/development ship
	// ids that are NOT T1/T2 nodes are intentionally no longer sent — the
	// client resolves those from its own local static tech tree.
	payload := buildMmogTechTreePayload()
	for _, shipID := range dreadconfig.StarterInventoryShipIDs() {
		if !bytes.Contains(payload, protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(shipID)))) {
			t.Fatalf("YA_GetTechTree missing installer starter ship id %d", shipID)
		}
	}
}

func TestPlayersInformationPayloadUsesDisplayInfoShape(t *testing.T) {
	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	payload := buildMmogPlayersInformationPayload(playerPID, nil)
	infos := extractNamedMmogArray(t, payload, "infos")

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "result", "ok")) {
		t.Fatal("YA_GetPlayersInformation must report result=ok before infos")
	}
	if bytes.Contains(payload, appendFieldMarker("result", 0x0d)) {
		t.Fatal("YA_GetPlayersInformation result must not be the legacy player array")
	}
	if !bytes.Contains(payload, appendFieldMarker("infos", 0x0d)) {
		t.Fatal("YA_GetPlayersInformation missing retail infos array")
	}
	if !bytes.Contains(infos, protocol.AppendStringField(nil, "ID", playerPID)) {
		t.Fatal("YA_GetPlayersInformation missing player ID")
	}
	if !bytes.Contains(infos, protocol.AppendStringField(nil, "DisplayInfo", defaultCaptainDisplayInfo)) {
		t.Fatal("YA_GetPlayersInformation missing DisplayInfo")
	}
	if !bytes.Contains(infos, protocol.AppendStringField(nil, "Rank", "1")) {
		t.Fatal("YA_GetPlayersInformation missing Rank")
	}
	if !bytes.Contains(infos, protocol.AppendStringField(nil, "UnlockedFleetType", "1")) {
		t.Fatal("YA_GetPlayersInformation missing UnlockedFleetType")
	}
	if !bytes.Contains(infos, protocol.AppendBoolField(nil, "Elite", false)) {
		t.Fatal("YA_GetPlayersInformation missing Elite=false")
	}
	if bytes.Contains(infos, appendFieldMarker("shipId", 0x56)) ||
		bytes.Contains(infos, appendFieldMarker("ShipID", 0x56)) {
		t.Fatal("YA_GetPlayersInformation infos payload should not inject shipId fields")
	}
}

func TestPlayersInformationPayloadUsesRequestedPlayerIDs(t *testing.T) {
	const requesterPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const requestedPID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	request := protocol.AppendStringField(nil, "RT", "YA_GetPlayersInformation")
	request, stack := protocol.AppendArrayStart(request, nil, "PIDs")
	request = protocol.AppendUnnamedStringField(request, requestedPID)
	request, _ = protocol.AppendObjectEnd(request, stack)
	request = protocol.AppendRootEnd(request)

	payload := buildMmogPlayersInformationPayload(requesterPID, request)
	infos := extractNamedMmogArray(t, payload, "infos")

	if !bytes.Contains(infos, protocol.AppendStringField(nil, "ID", requestedPID)) {
		t.Fatal("YA_GetPlayersInformation missing requested player ID")
	}
	if bytes.Contains(infos, protocol.AppendStringField(nil, "ID", requesterPID)) {
		t.Fatal("YA_GetPlayersInformation should not replace requested player IDs with requester PID")
	}
}

func TestTechTreeModuleUIDataIncludesStarterItems(t *testing.T) {
	// Minimal moduleUiData: identity + ownership only. Static module data
	// (prices, textures, weapon stats) comes from the client's own Content.
	payload := buildMmogTechTreePayload()
	moduleMarker := appendFieldMarker("moduleUiData", 0x0d)
	idx := bytes.Index(payload, moduleMarker)
	if idx == -1 {
		t.Fatal("YA_GetTechTree missing moduleUiData array")
	}
	modulePayload := payload[idx:]
	seeds := starterModuleUIDataSeeds()
	if len(seeds) == 0 {
		t.Fatal("starterModuleUIDataSeeds returned no starter module rows")
	}
	for _, seed := range seeds {
		if !bytes.Contains(modulePayload, protocol.AppendStringField(nil, "m_itemId", strconv.Itoa(int(seed.itemID)))) {
			t.Fatalf("moduleUiData missing m_itemId=%d", seed.itemID)
		}
	}
	if !bytes.Contains(modulePayload, protocol.AppendBoolField(nil, "m_isOwned", true)) {
		t.Fatal("moduleUiData missing ownership state")
	}
	// Static presentation must no longer be shipped.
	if bytes.Contains(modulePayload, appendFieldMarker("m_techTreePurchasePrice", 0x0c)) {
		t.Fatal("moduleUiData should not include static m_techTreePurchasePrice object")
	}
	if bytes.Contains(modulePayload, protocol.AppendStringField(nil, "m_iconTexturePath", "")) {
		t.Fatal("moduleUiData should not include static m_iconTexturePath")
	}
}

// TestF6WirePerksIntoTechTree tests that perks are wired into tech tree (F6)
func TestF6WirePerksIntoTechTree(t *testing.T) {
	// F6: Wire perks into tech tree and store catalog
	payload := buildMmogTechTreePayload()

	moduleUiData := extractNamedMmogArray(t, payload, "moduleUiData")
	if len(moduleUiData) == 0 {
		t.Fatal("YA_GetTechTree missing moduleUiData array")
	}

	// Check that perk items are included in the module UI data
	// We know from F5 that we have perks loaded
	perkCount := dreadconfig.PerkCount()
	if perkCount == 0 {
		t.Fatal("No perks loaded, cannot test F6")
	}

	// Check that the seeds include perks
	seeds := starterModuleUIDataSeeds()
	perkFoundInSeeds := false
	for _, seed := range seeds {
		// Check if this seed's itemID corresponds to a perk
		if _, exists := dreadconfig.PerkByID(seed.itemID); exists {
			perkFoundInSeeds = true
			break
		}
	}

	if !perkFoundInSeeds {
		t.Fatal("F6: Expected to find perks in starterModuleUIDataSeeds")
	}

	// Count how many perks are in the seeds
	perkCountInSeeds := 0
	for _, seed := range seeds {
		if _, exists := dreadconfig.PerkByID(seed.itemID); exists {
			perkCountInSeeds++
		}
	}

	t.Logf("✅ F6: Found %d perks in tech tree moduleUiData seeds out of %d total perks", perkCountInSeeds, perkCount)
	t.Logf("✅ F6: Perks successfully wired into tech tree")
}

func TestFleetStateIsConsistentAcrossResponses(t *testing.T) {
	const pid = defaultMmogPlayerPID

	starterFleet := starterFleetState()
	if got := len(starterFleet.shipLoadouts); got < 2 {
		t.Fatalf("full starter fleet unexpectedly collapsed to %d loadouts", got)
	}
	playerFleets := buildMmogPlayerFleetsPayload(pid)
	staticFleetData := buildMmogStaticFleetDataPayload()
	playerGet := buildMmogPlayerGetPayload(pid)
	refreshProfile := buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", pid)

	// FlagShipID/FlagShipLoadoutID/FlagShipLoadoutIndex inside a Fleets array
	// entry (YA_PlayerFleets, YA_RequestStaticFleetData) go through the
	// client's int32-blind fleet-array parser (FUN_142a77910 in the
	// decompile — see int32SliceToStrings' doc comment in
	// response_builders.go) and must be numeric strings, not int32.
	if !bytes.Contains(playerFleets, protocol.AppendStringField(nil, "FlagShipID", strconv.Itoa(int(starterFleet.flagshipShipID)))) {
		t.Fatalf("YA_PlayerFleets does not expose starter flagship ship %d", starterFleet.flagshipShipID)
	}
	if !bytes.Contains(playerFleets, protocol.AppendStringField(nil, "FlagShipLoadoutID", strconv.Itoa(int(starterFleet.flagshipLoadoutID)))) {
		t.Fatalf("YA_PlayerFleets does not expose starter flagship loadout %d", starterFleet.flagshipLoadoutID)
	}
	if !bytes.Contains(staticFleetData, appendFieldMarker("Fleets", 0x0d)) {
		t.Fatal("YA_RequestStaticFleetData does not expose Fleets array")
	}
	if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "FlagShipID", strconv.Itoa(int(starterFleet.flagshipShipID)))) {
		t.Fatalf("YA_RequestStaticFleetData does not expose starter flagship ship %d", starterFleet.flagshipShipID)
	}
	if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "FlagShipLoadoutID", strconv.Itoa(int(starterFleet.flagshipLoadoutID)))) {
		t.Fatalf("YA_RequestStaticFleetData does not expose starter flagship loadout %d", starterFleet.flagshipLoadoutID)
	}
	if !bytes.Contains(playerGet, protocol.AppendInt32Field(nil, "shipId", starterFleet.flagshipShipID)) {
		t.Fatalf("YA_PlayerGet does not expose selected starter ship %d", starterFleet.flagshipShipID)
	}
	if !bytes.Contains(playerGet, protocol.AppendInt32Field(nil, "selectedLoadoutID", starterFleet.flagshipLoadoutID)) {
		t.Fatalf("YA_PlayerGet does not expose selected starter loadout %d", starterFleet.flagshipLoadoutID)
	}
	for payloadName, payload := range map[string][]byte{
		"YA_PlayerFleets":           playerFleets,
		"YA_RequestStaticFleetData": staticFleetData,
		"YA_PlayerGet":              playerGet,
		"YA_RefreshPlayerProfile":   refreshProfile,
	} {
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "fleet id", starterFleet.fleetID)) {
			t.Fatalf("%s missing raw fleet id %d", payloadName, starterFleet.fleetID)
		}
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "m_fleetId", starterFleet.fleetID)) {
			t.Fatalf("%s missing m_fleetId=%d", payloadName, starterFleet.fleetID)
		}
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "m_flagshipIndex", starterFleet.flagshipIndex())) {
			t.Fatalf("%s missing m_flagshipIndex=%d", payloadName, starterFleet.flagshipIndex())
		}
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "m_fleetType", starterFleet.fleetType)) {
			t.Fatalf("%s missing m_fleetType=%d", payloadName, starterFleet.fleetType)
		}
		if !bytes.Contains(payload, appendFieldMarker("m_loadoutList", 0x0d)) {
			t.Fatalf("%s missing starter m_loadoutList", payloadName)
		}
	}
	for payloadName, payload := range map[string][]byte{
		"YA_PlayerFleets":         playerFleets,
		"YA_PlayerGet":            playerGet,
		"YA_RefreshPlayerProfile": refreshProfile,
	} {
		if !bytes.Contains(payload, appendFieldMarker("shipIds", 0x0d)) {
			t.Fatalf("%s missing starter shipIds snapshot", payloadName)
		}
		completion := extractNamedMmogArray(t, payload, "ShipTechTreeComplete")
		if got := bytes.Count(completion, protocol.AppendUnnamedBoolField(nil, true)); got != len(starterFleet.shipLoadouts) {
			t.Fatalf("%s ShipTechTreeComplete true count = %d, want %d", payloadName, got, len(starterFleet.shipLoadouts))
		}
		// Only the Fleets-array entry (YA_PlayerFleets) goes through the
		// client's int32-blind parser and needs the string form; the
		// top-level YA_PlayerGet/YA_RefreshPlayerProfile field is a
		// separate, unaffected assignment still sent as int32.
		wantFlagShipLoadoutIndex := protocol.AppendInt32Field(nil, "FlagShipLoadoutIndex", starterFleet.flagshipLoadoutIndex)
		if payloadName == "YA_PlayerFleets" {
			wantFlagShipLoadoutIndex = protocol.AppendStringField(nil, "FlagShipLoadoutIndex", strconv.Itoa(int(starterFleet.flagshipLoadoutIndex)))
		}
		if !bytes.Contains(payload, wantFlagShipLoadoutIndex) {
			t.Fatalf("%s missing flagship loadout index %d", payloadName, starterFleet.flagshipLoadoutIndex)
		}
	}
	staticCompletion := extractNamedMmogArray(t, staticFleetData, "ShipTechTreeComplete")
	if got := bytes.Count(staticCompletion, protocol.AppendUnnamedBoolField(nil, true)); got != len(starterFleet.shipLoadouts) {
		t.Fatalf("YA_RequestStaticFleetData ShipTechTreeComplete true count = %d, want %d", got, len(starterFleet.shipLoadouts))
	}

	for _, loadout := range starterFleet.shipLoadouts {
		// The loadout is now referenced as a string m_loadoutID inside the
		// FYShipImportLoadoutInfo entries of m_loadoutList (was a bare int32).
		loadoutIDStr := protocol.AppendStringField(nil, "m_loadoutID", strconv.Itoa(int(loadout.loadoutID())))
		if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "LoadoutID", loadout.loadoutID())) &&
			!bytes.Contains(playerFleets, protocol.AppendUnnamedInt32Field(nil, loadout.loadoutID())) &&
			!bytes.Contains(playerFleets, loadoutIDStr) {
			t.Fatalf("YA_PlayerFleets missing starter loadout reference %d", loadout.loadoutID())
		}
		if !bytes.Contains(playerGet, protocol.AppendUnnamedInt32Field(nil, loadout.loadoutID())) &&
			!bytes.Contains(playerGet, protocol.AppendInt32Field(nil, "LoadoutID", loadout.loadoutID())) &&
			!bytes.Contains(playerGet, loadoutIDStr) {
			t.Fatalf("YA_PlayerGet missing starter loadout reference %d", loadout.loadoutID())
		}
		if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(loadout.effectiveFleetShipID())))) {
			t.Fatalf("YA_RequestStaticFleetData missing starter fleet ship %d", loadout.effectiveFleetShipID())
		}
		if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "LoadoutID", strconv.Itoa(int(loadout.loadoutID())))) {
			t.Fatalf("YA_RequestStaticFleetData missing starter loadout id %d", loadout.loadoutID())
		}
		if !bytes.Contains(playerGet, protocol.AppendUnnamedInt32Field(nil, loadout.effectiveFleetShipID())) {
			t.Fatalf("YA_PlayerGet missing starter fleet ship id %d", loadout.effectiveFleetShipID())
		}
	}
	for _, loadout := range starterShipLoadouts() {
		for payloadName, payload := range map[string][]byte{
			"YA_PlayerFleets":           playerFleets,
			"YA_RequestStaticFleetData": staticFleetData,
			"YA_PlayerGet":              playerGet,
		} {
			if bytes.Contains(payload, protocol.AppendInt32Field(nil, "FlagShipID", loadout.ship.id)) ||
				bytes.Contains(payload, protocol.AppendUnnamedInt32Field(nil, loadout.ship.id)) {
				t.Fatalf("%s should use fleet/loadout-development ship id %d, not pawn item id %d", payloadName, loadout.effectiveFleetShipID(), loadout.ship.id)
			}
		}
	}
	for _, sharedLoadout := range dreadconfig.StarterInventoryLoadouts() {
		runtimeShip, ok := runtimeStarterShipForInstallerShipID(sharedLoadout.ShipID)
		if !ok {
			t.Fatalf("missing runtime starter ship mapping for installer ship %d", sharedLoadout.ShipID)
		}
		if runtimeShip.id == sharedLoadout.ShipID {
			continue
		}
		for payloadName, payload := range map[string][]byte{
			"YA_PlayerFleets":           playerFleets,
			"YA_RequestStaticFleetData": staticFleetData,
			"YA_PlayerGet":              playerGet,
		} {
			if bytes.Contains(payload, protocol.AppendInt32Field(nil, "FlagShipID", runtimeShip.id)) ||
				bytes.Contains(payload, protocol.AppendUnnamedInt32Field(nil, runtimeShip.id)) {
				t.Fatalf("%s should use installer starter ship id %d, not runtime/base ship id %d", payloadName, sharedLoadout.ShipID, runtimeShip.id)
			}
		}
	}
}

func TestHeroShipsAreInTheDocumentButNotTheResponseRows(t *testing.T) {
	// Heroes were once pulled out of the tech tree entirely: adding all of them
	// as response ROWS, each carrying its static data, took YA_GetTechTree to
	// ~56KB and overflowed the client's 32KB mmog receive ring buffer (0x8000,
	// confirmed in FUN_142a655a0). That reasoning applied to the rows, not to
	// the tree itself -- and the client cannot show a manufacturer's hero row
	// without them, because GetHeroShipsFromManufacturerData reads the
	// manufacturer's item array and filters on the hero category. So they now
	// ride in the zlib'd document, and the rows stay as they were.
	if len(heroShipLoadouts) == 0 {
		t.Fatal("the hero roster is empty")
	}

	techTree := buildMmogTechTreePayload()
	for _, hero := range heroShipLoadouts {
		row := protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(hero.loadoutID)))
		if bytes.Contains(techTree, row) {
			t.Fatalf("hero %s (%d) leaked into the response rows; that is what caused the 56KB overflow", hero.name, hero.loadoutID)
		}
	}

	document := inflateTechTreeDocument(t, techTree)
	for _, hero := range heroShipLoadouts {
		if !bytes.Contains(document, protocol.AppendStringField(nil, "Id", strconv.Itoa(int(hero.loadoutID)))) {
			t.Errorf("hero %s (%d) is missing from the tech tree document", hero.name, hero.loadoutID)
		}
	}

	// The whole point: hero ids must be category 3, because that is the only
	// thing that makes the manager tag them as heroes.
	for _, hero := range heroShipLoadouts {
		if category := (hero.loadoutID >> 24) & 0xff; category != 3 {
			t.Errorf("hero %s (%d) is category %d, not 3 (YShipLoadoutHero); it would render as an ordinary ship", hero.name, hero.loadoutID, category)
		}
	}

	if len(techTree) >= 0x8000 {
		t.Errorf("YA_GetTechTree is %d bytes, at or over the client's 32768-byte mmog ring buffer", len(techTree))
	}
	t.Logf("YA_GetTechTree %d bytes with %d heroes in the document", len(techTree), len(heroShipLoadouts))
}

func TestPlayerGetPayloadPopulatesFactionReputation(t *testing.T) {
	// issue #42: FactionReputation was always empty — now seeded with the two
	// real named factions confirmed in extracted client assets and persisted
	// per player.
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "dddddddddddddddddddddddddddddddd"
	pid := normalizedPlayerStatePID(playerPID)
	_ = buildMmogPlayerGetPayload(playerPID) // seeds player_state + factions

	entries := loadPlayerFactionReputation(pid)
	if len(entries) != len(knownFactionNames) {
		t.Fatalf("faction reputation rows = %d, want %d", len(entries), len(knownFactionNames))
	}

	if _, err := database.Exec(`UPDATE player_faction_reputation SET reputation=? WHERE user_id=? AND faction_id=?`, 500, pid, int32(1)); err != nil {
		t.Fatalf("update faction reputation: %v", err)
	}

	payload := buildMmogPlayerGetPayload(playerPID)
	factionRep := extractNamedMmogArray(t, payload, "FactionReputation")
	if !bytes.Contains(factionRep, protocol.AppendInt32Field(nil, "FactionID", 1)) {
		t.Fatal("FactionReputation missing FactionID=1")
	}
	if !bytes.Contains(factionRep, protocol.AppendInt32Field(nil, "Reputation", 500)) {
		t.Fatal("FactionReputation missing persisted Reputation=500")
	}
}

func TestMmogPlayerStatePersistsCurrencyPerPlayer(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const playerB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	_ = buildMmogPlayerGetPayload(playerA)
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=?, premium_currency=?, free_xp=?, current_xp=?, current_rank=?, rank_xp=? WHERE user_id=?`,
		12345, 678, 90, 111, 7, 222, playerA); err != nil {
		t.Fatalf("update player currency: %v", err)
	}

	playerAGet := buildMmogPlayerGetPayload(playerA)
	if !bytes.Contains(playerAGet, protocol.AppendInt32Field(nil, "gl", 12345)) {
		t.Fatal("YA_PlayerGet missing persisted soft currency")
	}
	if !bytes.Contains(playerAGet, protocol.AppendInt32Field(nil, "ob", 678)) {
		t.Fatal("YA_PlayerGet missing persisted premium currency")
	}
	// FreeXp goes through the same int32-blind parser as tll/tpl/tc/etc — sent
	// as a numeric string.
	if !bytes.Contains(playerAGet, protocol.AppendStringField(nil, "FreeXp", "90")) {
		t.Fatal("YA_PlayerGet missing persisted free XP")
	}

	// CurrentXP/CurrentRank/RankXP go through the same int32-blind parser
	// family — numeric strings, not int32.
	playerAProgression := buildMmogPlayerProgressionPayload(playerA)
	if !bytes.Contains(playerAProgression, protocol.AppendStringField(nil, "CurrentXP", "111")) {
		t.Fatal("YA_GetPlayerProgression missing persisted current XP")
	}
	if !bytes.Contains(playerAProgression, protocol.AppendStringField(nil, "CurrentRank", "7")) {
		t.Fatal("YA_GetPlayerProgression missing persisted rank")
	}
	if !bytes.Contains(playerAProgression, protocol.AppendStringField(nil, "RankXP", "222")) {
		t.Fatal("YA_GetPlayerProgression missing persisted rank XP")
	}

	playerBGet := buildMmogPlayerGetPayload(playerB)
	if !bytes.Contains(playerBGet, protocol.AppendInt32Field(nil, "gl", 10000)) {
		t.Fatal("second player should keep default soft currency")
	}
	if !bytes.Contains(playerBGet, protocol.AppendInt32Field(nil, "ob", 0)) {
		t.Fatal("second player should keep default premium currency")
	}
}

func TestUserLoginPayloadGrantsStreakBonusOncePerDay(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "cccccccccccccccccccccccccccccccc"
	_ = buildMmogPlayerGetPayload(playerPID) // seeds player_state row
	pid := normalizedPlayerStatePID(playerPID)

	var startingCredits, startingFreeXP int32
	if err := database.QueryRow(`SELECT soft_currency, free_xp FROM player_state WHERE user_id=?`, pid).Scan(&startingCredits, &startingFreeXP); err != nil {
		t.Fatalf("read starting economy: %v", err)
	}

	first := extractNamedMmogObject(t, buildMmogLoginSuccessPayload(playerPID), "result")
	firstStreak := extractNamedMmogObject(t, first, "LoginStreak")
	if !bytes.Contains(firstStreak, protocol.AppendInt32Field(nil, "loginstreak", 1)) {
		t.Fatal("first login of the day should set loginstreak=1")
	}
	if !bytes.Contains(firstStreak, protocol.AppendInt32Field(nil, "credits", 100)) {
		t.Fatal("first login of the day should grant a nonzero credits bonus")
	}

	var afterFirstCredits, afterFirstFreeXP int32
	if err := database.QueryRow(`SELECT soft_currency, free_xp FROM player_state WHERE user_id=?`, pid).Scan(&afterFirstCredits, &afterFirstFreeXP); err != nil {
		t.Fatalf("read economy after first login: %v", err)
	}
	if afterFirstCredits != startingCredits+100 || afterFirstFreeXP != startingFreeXP+50 {
		t.Fatalf("streak bonus not persisted: credits %d->%d, freeXP %d->%d", startingCredits, afterFirstCredits, startingFreeXP, afterFirstFreeXP)
	}

	second := extractNamedMmogObject(t, buildMmogLoginSuccessPayload(playerPID), "result")
	secondStreak := extractNamedMmogObject(t, second, "LoginStreak")
	// Zero, not the stored streak. The client's handler (FUN_142a3af90) sets
	// its "show the login bonus" flag on `0 < loginstreak` alone, without
	// looking at the reward values, so reporting the streak again here is what
	// made the daily bonus appear on every launch.
	if !bytes.Contains(secondStreak, protocol.AppendInt32Field(nil, "loginstreak", 0)) {
		t.Fatal("second login same day must report loginstreak=0 or the bonus screen re-appears")
	}
	if !bytes.Contains(secondStreak, protocol.AppendInt32Field(nil, "credits", 0)) {
		t.Fatal("second login same day should not grant another credits bonus")
	}

	var afterSecondCredits int32
	if err := database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&afterSecondCredits); err != nil {
		t.Fatalf("read economy after second login: %v", err)
	}
	if afterSecondCredits != afterFirstCredits {
		t.Fatalf("second login same day should not grant additional currency: %d -> %d", afterFirstCredits, afterSecondCredits)
	}
}

func TestMmogLoadoutMutationsPersistPerPlayer(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const playerB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	starter := starterShipLoadouts()[0]

	_ = buildMmogPlayerGetPayload(playerA)
	mutation := protocol.AppendInt32Field(nil, "LoadoutID", starter.loadoutID())
	mutation = protocol.AppendInt32Field(mutation, "weaponPrimary", 123456789)
	mutation = protocol.AppendStringField(mutation, "Name", "Persisted Test Loadout")
	if err := persistMmogPlayerMutation(playerA, "YA_UpdateShipLoadout", mutation); err != nil {
		t.Fatalf("persist update loadout: %v", err)
	}
	if err := persistMmogPlayerMutation(playerA, "YA_RenameShipLoadout", mutation); err != nil {
		t.Fatalf("persist rename loadout: %v", err)
	}

	playerAGet := buildMmogPlayerGetPayload(playerA)
	// weaponPrimary goes out to the client as a numeric string (see
	// int32SliceToStrings' doc comment in response_builders.go); this only
	// affects our outgoing wire format, not the incoming mutation parser
	// above, which still accepts int32 from the client.
	if !bytes.Contains(playerAGet, protocol.AppendStringField(nil, "weaponPrimary", "123456789")) {
		t.Fatal("player A loadout mutation was not persisted")
	}
	if !bytes.Contains(playerAGet, protocol.AppendStringField(nil, "name", "Persisted Test Loadout")) {
		t.Fatal("player A loadout rename was not persisted")
	}

	playerBGet := buildMmogPlayerGetPayload(playerB)
	if bytes.Contains(playerBGet, protocol.AppendInt32Field(nil, "weaponPrimary", 123456789)) {
		t.Fatal("player B should not see player A loadout mutation")
	}
	if bytes.Contains(playerBGet, protocol.AppendStringField(nil, "name", "Persisted Test Loadout")) {
		t.Fatal("player B should not see player A loadout rename")
	}
}

func TestMmogSavePlayerDisplayInformationPersistsForPlayersInformation(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "12121212121212121212121212121212"
	const displayInfo = "CaptainFallbackToken"
	const displayName = "Local"

	_ = buildMmogPlayerGetPayload(playerPID)

	mutation := protocol.AppendStringField(nil, "DisplayInfo", displayInfo)
	if err := persistMmogPlayerMutation(playerPID, "YA_SavePlayerDisplayInformation", mutation); err != nil {
		t.Fatalf("persist save player display information: %v", err)
	}

	var storedDisplayName string
	var storedDisplayInfo string
	if err := database.QueryRow(`SELECT display_name,display_info FROM player_state WHERE user_id=?`, playerPID).Scan(&storedDisplayName, &storedDisplayInfo); err != nil {
		t.Fatalf("query saved player display information: %v", err)
	}
	if storedDisplayName != displayName {
		t.Fatalf("stored display name = %q, want %q", storedDisplayName, displayName)
	}
	if storedDisplayInfo != displayInfo {
		t.Fatalf("stored display info = %q, want %q", storedDisplayInfo, displayInfo)
	}

	payload := buildMmogPlayersInformationPayload(playerPID, nil)
	infos := extractNamedMmogArray(t, payload, "infos")

	if !bytes.Contains(infos, protocol.AppendStringField(nil, "DisplayInfo", displayInfo)) {
		t.Fatalf("YA_GetPlayersInformation did not use persisted display info %q", displayInfo)
	}
}

func TestMmogSavePlayerDisplayInformationFallsBackToDefaultDisplayInfo(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	const displayName = "OnlyName"

	_ = buildMmogPlayerGetPayload(playerPID)

	mutation := protocol.AppendStringField(nil, "DisplayName", displayName)
	if err := persistMmogPlayerMutation(playerPID, "YA_SavePlayerDisplayInformation", mutation); err != nil {
		t.Fatalf("persist save player display information: %v", err)
	}

	var storedDisplayInfo string
	if err := database.QueryRow(`SELECT display_info FROM player_state WHERE user_id=?`, playerPID).Scan(&storedDisplayInfo); err != nil {
		t.Fatalf("query saved player display information: %v", err)
	}
	if storedDisplayInfo != defaultCaptainDisplayInfo {
		t.Fatalf("stored display info = %q, want %q", storedDisplayInfo, defaultCaptainDisplayInfo)
	}
}

func TestMmogPlayerStatePreservesDistinctLoadoutAndPrecastIDs(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "dddddddddddddddddddddddddddddddd"
	const customLoadoutID int32 = 777777
	starter := starterShipLoadouts()[0]

	_ = buildMmogPlayerGetPayload(playerPID)
	if _, err := database.Exec(`INSERT INTO player_ship_loadouts(
			user_id,loadout_id,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active,
			weapon_primary_id,weapon_secondary_id,ability_primary_id,ability_secondary_id,ability_perimeter_id,ability_internal_id,
			perk_com_id,perk_weapon_id,perk_navigation_id,perk_engineer_id
		)
		SELECT user_id,?,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active,
			weapon_primary_id,weapon_secondary_id,ability_primary_id,ability_secondary_id,ability_perimeter_id,ability_internal_id,
			perk_com_id,perk_weapon_id,perk_navigation_id,perk_engineer_id
		FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`,
		customLoadoutID, playerPID, starter.loadoutID()); err != nil {
		t.Fatalf("insert persisted loadout id: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_fleet_loadouts SET loadout_id=? WHERE user_id=? AND loadout_id=?`,
		customLoadoutID, playerPID, starter.loadoutID()); err != nil {
		t.Fatalf("update persisted fleet loadout id: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_fleets SET flagship_loadout_id=? WHERE user_id=? AND flagship_loadout_id=?`,
		customLoadoutID, playerPID, starter.loadoutID()); err != nil {
		t.Fatalf("update persisted flagship loadout id: %v", err)
	}

	payload := buildMmogPlayerGetPayload(playerPID)
	if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "LoadoutID", customLoadoutID)) {
		t.Fatal("YA_PlayerGet missing persisted player loadout id")
	}
	if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "precastLoadoutID", starter.precastLoadoutID)) {
		t.Fatal("YA_PlayerGet missing original precast loadout id")
	}
	if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "m_precastLoadoutID", starter.precastLoadoutID)) {
		t.Fatal("YA_PlayerGet missing original m_precastLoadoutID")
	}
}

func TestMmogEnterAndLeaveMatchmakingUseQueueDB(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	request := protocol.AppendStringField(nil, "GameMode", "TeamElimination")
	request = protocol.AppendInt32Field(request, "TierMin", 2)
	request = protocol.AppendInt32Field(request, "TierMax", 4)
	response := buildMmogRequestResponsePayload("YA_EnterMatchmaking", playerPID, request)
	result := extractNamedMmogObject(t, response, "result")

	if !bytes.Contains(result, protocol.AppendStringField(nil, "matchmakingStatus", "waiting")) {
		t.Fatal("YA_EnterMatchmaking did not report waiting state")
	}
	if !bytes.Contains(result, protocol.AppendStringField(nil, "GameMode", "TE")) {
		t.Fatal("YA_EnterMatchmaking did not normalize requested game mode")
	}

	var gameMode string
	var tierMin, tierMax int
	if err := database.QueryRow(`SELECT game_mode,tier_min,tier_max FROM queue_entries WHERE user_id=? AND status='waiting'`, playerPID).
		Scan(&gameMode, &tierMin, &tierMax); err != nil {
		t.Fatalf("load queued entry: %v", err)
	}
	if gameMode != "TE" || tierMin != 2 || tierMax != 4 {
		t.Fatalf("queued entry = %s/%d/%d, want TE/2/4", gameMode, tierMin, tierMax)
	}

	leave := buildMmogRequestResponsePayload("YA_LeaveMatchmaking", playerPID, nil)
	if !bytes.Contains(extractNamedMmogObject(t, leave, "result"), protocol.AppendStringField(nil, "matchmakingStatus", "left")) {
		t.Fatal("YA_LeaveMatchmaking did not report left state")
	}
	var queued int
	if err := database.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id=? AND status='waiting'`, playerPID).Scan(&queued); err != nil {
		t.Fatalf("count queued entries: %v", err)
	}
	if queued != 0 {
		t.Fatalf("queued entries after leave = %d, want 0", queued)
	}
}

func TestGameConfigDataUsesClientGameModeRows(t *testing.T) {
	result := extractNamedMmogObject(t, buildMmogGameConfigDataPayload(), "result")
	gameModes := extractNamedMmogArray(t, result, "GameModes")

	for _, mode := range matchmaker.GameModeConfigs() {
		if !bytes.Contains(gameModes, protocol.AppendStringField(nil, "Name", mode.Name)) {
			t.Fatalf("YA_GetGameConfigData missing game mode name %q", mode.Name)
		}
	}
	if bytes.Contains(gameModes, protocol.AppendUnnamedStringField(nil, "TeamDeathmatch")) {
		t.Fatal("YA_GetGameConfigData should send structured GameModes rows, not legacy bare strings")
	}
	for _, alias := range []string{"TDM", "TE", "BC", "TER"} {
		if !bytes.Contains(gameModes, protocol.AppendStringField(nil, "Name", alias)) {
			t.Fatalf("YA_GetGameConfigData missing client game mode alias %q", alias)
		}
	}
	if bytes.Contains(gameModes, protocol.AppendStringField(nil, "Name", "TeamDeathMatch")) {
		t.Fatal("YA_GetGameConfigData should expose client aliases, not long server mode names")
	}
}

func TestUpdateGameModesIsTopLevelAndRTNamed(t *testing.T) {
	payload := buildMmogUpdateGameModesPayload()

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", "YA_UpdateGameModes")) {
		t.Fatal("YA_UpdateGameModes payload missing RT=YA_UpdateGameModes")
	}
	// The client reads GameModes at the message root (sibling of RT), not under
	// a "result" object; a nested placement leaves m_gameModes empty.
	if bytes.Contains(payload, appendFieldMarker("result", 0x0c)) {
		t.Fatal("YA_UpdateGameModes must not nest GameModes under a result object")
	}
	gameModes := extractNamedMmogArray(t, payload, "GameModes")
	for _, mode := range matchmaker.GameModeConfigs() {
		if !bytes.Contains(gameModes, protocol.AppendStringField(nil, "Name", mode.Name)) {
			t.Fatalf("YA_UpdateGameModes missing game mode name %q", mode.Name)
		}
	}
}

func TestChatPayloadIncludesLowercaseMessagesAlias(t *testing.T) {
	payload := buildMmogChatPayload("YA_GlobalChat", defaultMmogPlayerPID, nil)
	if !bytes.Contains(payload, appendFieldMarker("Messages", 0x0d)) {
		t.Fatal("chat payload missing Messages array")
	}
	if !bytes.Contains(payload, appendFieldMarker("messages", 0x0d)) {
		t.Fatal("chat payload missing lowercase messages array")
	}
}

func TestAnalyticsBeginTransactionEchoesTransactionID(t *testing.T) {
	payload := buildMmogAnalyticsBeginTransactionPayload("tx-123")
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "transactionId", "tx-123")) {
		t.Fatal("YA_AnalyticsBeginTransaction did not echo transactionId")
	}
}

func TestGetShipBonusesReturnsDataShape(t *testing.T) {
	request := protocol.AppendInt32Field(nil, "shipID", extractedShipIDAthos)
	payload := buildMmogRequestResponsePayload("YA_GetShipBonuses", defaultMmogPlayerPID, request)
	result := extractNamedMmogObject(t, payload, "result")
	if !bytes.Contains(result, appendFieldMarker("ShipBonuses", 0x0d)) {
		t.Fatal("YA_GetShipBonuses missing ShipBonuses array")
	}
	if !bytes.Contains(result, appendFieldMarker("shipBonuses", 0x0d)) {
		t.Fatal("YA_GetShipBonuses missing lowercase shipBonuses array")
	}
	if !bytes.Contains(result, protocol.AppendInt32Field(nil, "shipID", extractedShipIDAthos)) {
		t.Fatal("YA_GetShipBonuses missing requested shipID")
	}
}

func TestAliasResponsesEchoRequestRT(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	if _, err := database.Exec(`UPDATE player_state SET soft_currency=20000,premium_currency=5000 WHERE user_id=?`, playerPID); err != nil {
		t.Fatalf("seed currencies: %v", err)
	}

	purchaseRequest := protocol.AppendInt32Field(nil, "ItemID", extractedShipIDTrafalgar)
	purchase := buildMmogRequestResponsePayload("YA_BuyItem", playerPID, purchaseRequest)
	if !bytes.Contains(purchase, protocol.AppendStringField(nil, "RT", "YA_BuyItem")) {
		t.Fatal("YA_BuyItem response did not echo request RT")
	}
	if bytes.Contains(purchase, protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")) {
		t.Fatal("YA_BuyItem response used YA_PurchaseItem RT")
	}

	elite := buildMmogRequestResponsePayload("YA_ActivateElite", playerPID, nil)
	if !bytes.Contains(elite, protocol.AppendStringField(nil, "RT", "YA_ActivateElite")) {
		t.Fatal("YA_ActivateElite response did not echo request RT")
	}
}

func TestPurchaseItemAcceptsClientOfferShape(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=20000 WHERE user_id=?`, playerPID); err != nil {
		t.Fatalf("seed currency: %v", err)
	}
	offer := extractedMarketItemExternalID(extractedShipIDTrafalgar, "")
	request := protocol.AppendStringField(nil, "offer", offer)
	request = protocol.AppendInt32Field(request, "quantity", 1)
	request = protocol.AppendStringField(request, "currency", "CR")
	request = protocol.AppendStringField(request, "campaign", "")
	request = protocol.AppendStringField(request, "priceId", "price_free")

	purchase := buildMmogPurchasePayload("YA_PurchaseItem", playerPID, request)
	if !bytes.Contains(purchase, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatalf("offer-shaped purchase did not succeed: %x", purchase)
	}
	if !bytes.Contains(purchase, protocol.AppendInt32Field(nil, "itemID", extractedShipIDTrafalgar)) {
		t.Fatal("offer-shaped purchase did not resolve offer to itemID")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_id=? AND currency='CR'`, playerPID, extractedShipIDTrafalgar).Scan(&count); err != nil {
		t.Fatalf("count purchases: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted purchases = %d, want 1", count)
	}
}

func TestPurchasedShipUpdatesTechTreeAndProgressionOwnership(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "edededededededededededededededed"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=20000 WHERE user_id=?`, playerPID); err != nil {
		t.Fatalf("seed currency: %v", err)
	}

	purchaseRequest := protocol.AppendInt32Field(nil, "ItemID", extractedShipIDTrafalgar)
	purchase := buildMmogPurchasePayload("YA_BuyItem", playerPID, purchaseRequest)
	if !bytes.Contains(purchase, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatalf("purchase did not succeed: %x", purchase)
	}

	techTree := buildMmogTechTreePayload(playerPID)
	marker := bytes.Index(techTree, protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(extractedShipIDTrafalgar))))
	if marker < 0 {
		t.Fatal("YA_GetTechTree missing purchased Trafalgar row")
	}
	// Ownership is NOT asserted on the tech tree row: bIsPurchased/bIsUnlocked
	// are names the client binary does not contain, so they never conveyed it.
	// The progression payload below carries the flag the client does read.

	// shipID in shipProgressionUiData entries goes through the same
	// int32-blind parser family — numeric string, not int32.
	progression := buildMmogPlayerProgressionPayload(playerPID)
	progressionMarker := bytes.Index(progression, protocol.AppendStringField(nil, "shipID", strconv.Itoa(int(extractedShipIDTrafalgar))))
	if progressionMarker < 0 {
		t.Fatal("YA_GetPlayerProgression missing purchased Trafalgar row")
	}
	progressionEnd := progressionMarker + 120
	if progressionEnd > len(progression) {
		progressionEnd = len(progression)
	}
	if !bytes.Contains(progression[progressionMarker:progressionEnd], protocol.AppendBoolField(nil, "owned", true)) {
		t.Fatal("YA_GetPlayerProgression does not mark purchased Trafalgar as owned")
	}
}

func TestErrorPayloadIncludesRequestRT(t *testing.T) {
	payload := buildMmogRequestResponsePayload("YA_GetUnknownThing", defaultMmogPlayerPID, nil)
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", "YA_GetUnknownThing")) {
		t.Fatal("unknown read error response missing request RT")
	}
	if !bytes.Contains(extractNamedMmogObject(t, payload, "result"), protocol.AppendStringField(nil, fieldStatus, "error")) {
		t.Fatal("unknown read error response missing error status")
	}
}

func TestMmogCheckReturnUsesCanReturnToMatchFields(t *testing.T) {
	payload := buildMmogRequestResponsePayload("YA_CheckReturn", defaultMmogPlayerPID, nil)
	result := extractNamedMmogObject(t, payload, "result")

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", "YA_CheckReturn")) {
		t.Fatal("YA_CheckReturn dispatcher response missing RT")
	}
	if !bytes.Contains(result, protocol.AppendBoolField(nil, "CanReturnToMatch", false)) {
		t.Fatal("YA_CheckReturn missing CanReturnToMatch=false")
	}
	// canReturnToMatch (lowercase) collides case-insensitively with
	// CanReturnToMatch as a UE4 FName (see commit 8f72937) and must NOT be
	// emitted alongside it.
	if bytes.Contains(result, protocol.AppendBoolField(nil, "canReturnToMatch", false)) {
		t.Fatal("YA_CheckReturn must not emit canReturnToMatch, a case-insensitive FName duplicate of CanReturnToMatch")
	}
	// issue #52: "ReturnValue" has no genuine footprint in the client binary
	// (only generic UFUNCTION reflection boilerplate) — removed.
	if bytes.Contains(result, appendFieldMarker("ReturnValue", 0x05)) {
		t.Fatal("YA_CheckReturn should not send fabricated ReturnValue field")
	}
	if bytes.Contains(result, appendFieldMarker("CanReturn", 0x01)) ||
		bytes.Contains(result, appendFieldMarker("canReturn", 0x01)) {
		t.Fatal("YA_CheckReturn still contains legacy CanReturn fields")
	}
}

func TestMmogCustomRoomActionsUseResponseRTs(t *testing.T) {
	for requestName, expectedRT := range map[string]string{
		"YA_CustomRoomCreate":           "YA_CustomRoomCreateResponse",
		"YA_CustomRoomStartMatch":       "YA_CustomRoomStartMatchResponse",
		"YA_CustomRoomUserJoin":         "YA_CustomRoomUserJoinResponse",
		"YA_CustomRoomUserLeave":        "YA_CustomRoomUserLeaveResponse",
		"YA_CustomRoomUserReturn":       "YA_CustomRoomUserReturnResponse",
		"YA_CustomRoomUserSwitchTeam":   "YA_CustomRoomUserSwitchTeamResponse",
		"YA_CustomRoomChangeHost":       "YA_CustomRoomChangeHostResponse",
		"YA_CustomRoomChangeSettings":   "YA_CustomRoomChangeSettingsResponse",
		"YA_CustomRoomUpdate":           "YA_CustomRoomUpdateResponse",
		"YA_CustomRoomEnterFleetSelect": "YA_CustomRoomEnterFleetSelectResponse",
		"YA_CustomRoomExitFleetSelect":  "YA_CustomRoomExitFleetSelectResponse",
	} {
		payload := buildMmogRequestResponsePayload(requestName, defaultMmogPlayerPID, nil)
		rt := protocol.ExtractStringField(payload, "RT")
		if rt != expectedRT {
			t.Fatalf("%s RT = %q, want %s", requestName, rt, expectedRT)
		}

		result := extractNamedMmogObject(t, payload, "result")
		if !bytes.Contains(result, protocol.AppendInt32Field(nil, "Code", 0)) {
			t.Fatalf("%s missing Code=0", expectedRT)
		}
		if !bytes.Contains(result, appendFieldMarker("Room", 0x0c)) {
			t.Fatalf("%s missing Room object", expectedRT)
		}
	}

	payload := buildMmogRequestResponsePayload("YA_CustomRoomInvite", defaultMmogPlayerPID, nil)
	if rt := protocol.ExtractStringField(payload, "RT"); rt != "YA_CustomRoomInvite" {
		t.Fatalf("YA_CustomRoomInvite RT = %q, want request name passthrough", rt)
	}
	result := extractNamedMmogObject(t, payload, "result")
	if !bytes.Contains(result, protocol.AppendInt32Field(nil, "Code", 0)) {
		t.Fatal("YA_CustomRoomInvite missing Code=0")
	}
	if !bytes.Contains(result, appendFieldMarker("Room", 0x0c)) {
		t.Fatal("YA_CustomRoomInvite missing Room object")
	}
}

func TestMmogRoomResponseNameMapsKnownCustomRoomResponses(t *testing.T) {
	for requestName, expectedRT := range map[string]string{
		"YA_CustomRoomCreate":           "YA_CustomRoomCreateResponse",
		"YA_CustomRoomStartMatch":       "YA_CustomRoomStartMatchResponse",
		"YA_CustomRoomUserJoin":         "YA_CustomRoomUserJoinResponse",
		"YA_CustomRoomUserLeave":        "YA_CustomRoomUserLeaveResponse",
		"YA_CustomRoomUserReturn":       "YA_CustomRoomUserReturnResponse",
		"YA_CustomRoomUserSwitchTeam":   "YA_CustomRoomUserSwitchTeamResponse",
		"YA_CustomRoomChangeHost":       "YA_CustomRoomChangeHostResponse",
		"YA_CustomRoomChangeSettings":   "YA_CustomRoomChangeSettingsResponse",
		"YA_CustomRoomUpdate":           "YA_CustomRoomUpdateResponse",
		"YA_CustomRoomEnterFleetSelect": "YA_CustomRoomEnterFleetSelectResponse",
		"YA_CustomRoomExitFleetSelect":  "YA_CustomRoomExitFleetSelectResponse",
	} {
		if got := mmogRoomResponseName(requestName); got != expectedRT {
			t.Fatalf("%s response RT = %q, want %q", requestName, got, expectedRT)
		}
	}
	if got := mmogRoomResponseName("YA_CustomRoomInvite"); got != "YA_CustomRoomInvite" {
		t.Fatalf("YA_CustomRoomInvite response RT = %q, want passthrough", got)
	}
}

func TestMmogEnterMatchmakingReportsExistingMatch(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "ffffffffffffffffffffffffffffffff"
	const matchID = "test-match-id"

	if _, err := database.Exec(`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,started_at) VALUES(?,?,?,?,?,'active',datetime('now'))`,
		matchID, "TeamDeathmatch", "Charon", "127.0.0.1", 7777); err != nil {
		t.Fatalf("insert active match: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,?)`, matchID, playerPID, 0); err != nil {
		t.Fatalf("insert match slot: %v", err)
	}

	result := extractNamedMmogObject(t, buildMmogRequestResponsePayload("YA_EnterMatchmaking", playerPID, nil), "result")
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "matchmakingStatus", value: "matched"},
		{name: "MatchID", value: matchID},
		{name: "serverHost", value: "127.0.0.1"},
		{name: "Map", value: "Charon"},
	} {
		if !bytes.Contains(result, protocol.AppendStringField(nil, field.name, field.value)) {
			t.Fatalf("matched response missing %s=%q", field.name, field.value)
		}
	}
	if !bytes.Contains(result, protocol.AppendInt32Field(nil, "serverPort", 7777)) {
		t.Fatal("matched response missing serverPort=7777")
	}
}

func TestMmogSocialRoomAndChatPayloadsAreExplicit(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "99999999999999999999999999999999"

	queryRooms := buildMmogRequestResponsePayload("YA_QueryRooms", playerPID, nil)
	if !bytes.Contains(queryRooms, appendFieldMarker("Rooms", 0x0d)) {
		t.Fatal("YA_QueryRooms missing Rooms array")
	}

	squad := buildMmogRequestResponsePayload("YA_SquadInvite", playerPID, nil)
	if !bytes.Contains(squad, appendFieldMarker("Squad", 0x0d)) {
		t.Fatal("YA_SquadInvite missing Squad array")
	}
	if !bytes.Contains(squad, appendFieldMarker("Members", 0x0d)) {
		t.Fatal("YA_SquadInvite missing Members array")
	}

	chatRequest := protocol.AppendStringField(nil, "channelName", "global")
	chatRequest = protocol.AppendStringField(chatRequest, "Message", "hello hangar")
	chat := buildMmogRequestResponsePayload("YA_GlobalChat", playerPID, chatRequest)
	if !bytes.Contains(chat, protocol.AppendStringField(nil, "channelName", "global")) {
		t.Fatal("YA_GlobalChat did not echo channel name")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE sender_id=? AND content=?`, playerPID, "hello hangar").Scan(&count); err != nil {
		t.Fatalf("count chat messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted chat messages = %d, want 1", count)
	}
}

func TestFleetMetadataUsesConfigBackedEligibility(t *testing.T) {
	starterFleet := starterFleetState()
	staticFleetData := buildMmogStaticFleetDataPayload()
	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	fleetEligibility := buildMmogFleetEligibilityPayload()
	fleetTypes := extractNamedMmogArray(t, extractNamedMmogObject(t, staticFleetData, "result"), "FleetTypes")

	recruit := mustConfigBackedFleetEligibility("Recruit")
	if starterFleet.token != configBackedFleetToken(recruit) {
		t.Fatalf("starter fleet token = %q, want %q", starterFleet.token, configBackedFleetToken(recruit))
	}
	if starterFleet.displayName != recruit.DisplayName {
		t.Fatalf("starter fleet display name = %q, want %q", starterFleet.displayName, recruit.DisplayName)
	}
	if starterFleet.fleetType != recruit.FleetType {
		t.Fatalf("starter fleet type = %d, want %d", starterFleet.fleetType, recruit.FleetType)
	}
	if len(starterFleet.tiers) != len(recruit.AllowedTiers) {
		t.Fatalf("starter fleet tier count = %d, want %d", len(starterFleet.tiers), len(recruit.AllowedTiers))
	}
	for idx, tier := range recruit.AllowedTiers {
		if starterFleet.tiers[idx] != tier {
			t.Fatalf("starter fleet tier[%d] = %d, want %d", idx, starterFleet.tiers[idx], tier)
		}
	}
	if !bytes.Contains(playerGet, protocol.AppendStringField(nil, "FleetID", starterFleet.token)) {
		t.Fatalf("YA_PlayerGet missing config-backed starter FleetID %q", starterFleet.token)
	}

	wantEligibilities := configBackedFleetEligibilities()
	if got := len(mmogFleetSeeds()); got != len(wantEligibilities) {
		t.Fatalf("fleet seed count = %d, want %d", got, len(wantEligibilities))
	}
	// YA_FleetEligibility is parsed by FUN_142a78790, the same function as
	// YA_RequestStaticFleetData, so it carries FleetTypes entries -- not the
	// "fleet_eligibility" array of FleetType/Reason pairs it used to send,
	// which shared no field name with that parser and filled nothing.
	if got := bytes.Count(fleetEligibility, appendFieldMarker("Tiers", 0x0d)); got != len(wantEligibilities) {
		t.Fatalf("YA_FleetEligibility FleetTypes tier-array count = %d, want %d", got, len(wantEligibilities))
	}
	for _, eligibility := range wantEligibilities {
		if !bytes.Contains(fleetEligibility, protocol.AppendStringField(nil, "ID", strconv.Itoa(int(eligibility.FleetType)))) {
			t.Fatalf("YA_FleetEligibility missing config-backed FleetType id %d", eligibility.FleetType)
		}
	}
	// The AI-ship spawner reads Tiers; an int32 tag there is silently read as 0.
	if bytes.Contains(fleetEligibility, appendFieldMarker("FleetType", 0x56)) {
		t.Fatal("YA_FleetEligibility still carries the old int32 FleetType field")
	}
	// Maintenance is resolved on the result object, not per FleetTypes entry.
	if !bytes.Contains(fleetEligibility, appendFieldMarker("Maintenance", 0x0c)) {
		t.Fatal("YA_FleetEligibility is missing the result-level Maintenance object")
	}
	if got := bytes.Count(fleetTypes, appendFieldMarker("Tiers", 0x0d)); got != len(wantEligibilities) {
		t.Fatalf("YA_RequestStaticFleetData FleetTypes tier-array count = %d, want %d", got, len(wantEligibilities))
	}
	tierCounts := map[int32]int{}
	for _, eligibility := range wantEligibilities {
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "ID", strconv.Itoa(int(eligibility.FleetType)))) {
			t.Fatalf("YA_RequestStaticFleetData missing config-backed FleetType id %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "ShipsToUnlock", strconv.Itoa(int(eligibility.NumShipsToUnlockFleet)))) {
			t.Fatalf("YA_RequestStaticFleetData missing ShipsToUnlock=%d for fleet type %d", eligibility.NumShipsToUnlockFleet, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "BaseMaintenanceCost", strconv.Itoa(int(eligibility.BaseMaintenanceCost)))) {
			t.Fatalf("YA_RequestStaticFleetData missing BaseMaintenanceCost=%d for fleet type %d", eligibility.BaseMaintenanceCost, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "FleetRatingMin", strconv.FormatFloat(eligibility.FleetRatingMin, 'f', 1, 64))) {
			t.Fatalf("YA_RequestStaticFleetData missing FleetRatingMin for fleet type %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "FleetRatingCost", strconv.Itoa(int(eligibility.FleetRatingCost)))) {
			t.Fatalf("YA_RequestStaticFleetData missing FleetRatingCost=%d for fleet type %d", eligibility.FleetRatingCost, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "ChargeTime", strconv.Itoa(int(eligibility.MaintenanceTime)))) {
			t.Fatalf("YA_RequestStaticFleetData missing ChargeTime=%d for fleet type %d", eligibility.MaintenanceTime, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "ChargeCost", strconv.Itoa(0))) {
			t.Fatalf("YA_RequestStaticFleetData missing neutral ChargeCost for fleet type %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "AvailableCharges", strconv.Itoa(1))) {
			t.Fatalf("YA_RequestStaticFleetData missing AvailableCharges=1 for fleet type %d", eligibility.FleetType)
		}
		for _, tier := range eligibility.AllowedTiers {
			tierCounts[tier]++
		}
	}
	for tier, wantCount := range tierCounts {
		if got := bytes.Count(fleetTypes, protocol.AppendUnnamedStringField(nil, strconv.Itoa(int(tier)))); got != wantCount {
			t.Fatalf("YA_RequestStaticFleetData tier %d count = %d, want %d", tier, got, wantCount)
		}
	}
	maintenance := extractNamedMmogObject(t, extractNamedMmogObject(t, staticFleetData, "result"), "Maintenance")
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "EliteCostMultiplier", value: "1.0"},
		{name: "NonEliteCostMultiplier", value: "1.0"},
		{name: "TopPlayerCostMultiplier", value: "1.0"},
		{name: "NonTopPlayerCostMultiplier", value: "1.0"},
		{name: "WinningCostMultiplier", value: "1.0"},
		{name: "LoosingCostMultiplier", value: "1.0"},
	} {
		if !bytes.Contains(maintenance, protocol.AppendStringField(nil, field.name, field.value)) {
			t.Fatalf("YA_RequestStaticFleetData missing Maintenance.%s=%q", field.name, field.value)
		}
	}
	if !bytes.Contains(maintenance, protocol.AppendInt32Field(nil, "TopPlayerCount", 0)) {
		t.Fatal("YA_RequestStaticFleetData missing Maintenance.TopPlayerCount=0")
	}
	if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "Name", starterFleet.displayName)) {
		t.Fatalf("YA_RequestStaticFleetData missing active fleet display name %q", starterFleet.displayName)
	}
	if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "FID", starterFleet.token)) {
		t.Fatalf("YA_RequestStaticFleetData missing active fleet token %q", starterFleet.token)
	}
	if bytes.Contains(staticFleetData, []byte("Veteran Fleet")) || bytes.Contains(staticFleetData, []byte("Legendary Fleet")) {
		t.Fatal("YA_RequestStaticFleetData should only include the active fleet row in the Fleets array")
	}
	if got := bytes.Count(staticFleetData, appendFieldMarker("FID", 0x09)); got != 1 {
		t.Fatalf("YA_RequestStaticFleetData fleet row count = %d, want 1", got)
	}
}

func sharedStarterSlotItemID(t *testing.T, loadout dreadconfig.StarterLoadout, slotName string) int32 {
	t.Helper()

	for _, slot := range loadout.Slots {
		if slot.SlotName == slotName {
			return slot.ItemID
		}
	}
	t.Fatalf("missing starter slot %s for %s", slotName, loadout.ShipName)
	return 0
}

func sharedStarterLoadoutByID(t *testing.T, loadoutID int32) dreadconfig.StarterLoadout {
	t.Helper()

	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		if loadout.LoadoutID == loadoutID {
			return loadout
		}
	}
	t.Fatalf("missing shared starter loadout %d", loadoutID)
	return dreadconfig.StarterLoadout{}
}

func starterFleetShipIDsFromShared(t *testing.T) []int32 {
	t.Helper()

	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	ids := make([]int32, 0, len(sharedLoadouts))
	for _, loadout := range sharedLoadouts {
		fleetShipID := fleetStarterShipIDForPrecast(loadout.LoadoutID)
		ids = append(ids, fleetShipID)
	}
	return ids
}

func TestStarterRosterMatchesSharedConfigExactly(t *testing.T) {
	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	loadouts := starterShipLoadouts()
	if len(loadouts) != len(sharedLoadouts) {
		t.Fatalf("starter loadout count = %d, want %d", len(loadouts), len(sharedLoadouts))
	}

	wantShipIDs := starterFleetShipIDsFromShared(t)
	gotShipIDs := starterShipIDs()
	if len(gotShipIDs) != len(wantShipIDs) {
		t.Fatalf("starter ship id count = %d, want %d", len(gotShipIDs), len(wantShipIDs))
	}
	for idx, wantID := range wantShipIDs {
		if gotShipIDs[idx] != wantID {
			t.Fatalf("starter ship id[%d] = %d, want %d", idx, gotShipIDs[idx], wantID)
		}
	}

	wantLoadoutIDs := dreadconfig.StarterInventoryLoadoutIDs()
	gotLoadoutIDs := starterLoadoutIDs()
	if len(gotLoadoutIDs) != len(wantLoadoutIDs) {
		t.Fatalf("starter loadout id count = %d, want %d", len(gotLoadoutIDs), len(wantLoadoutIDs))
	}
	for idx, wantID := range wantLoadoutIDs {
		if gotLoadoutIDs[idx] != wantID {
			t.Fatalf("starter loadout id[%d] = %d, want %d", idx, gotLoadoutIDs[idx], wantID)
		}
	}

	for idx, sharedLoadout := range sharedLoadouts {
		loadout := loadouts[idx]
		wantShip, ok := starterBootstrapShipByID(sharedLoadout.ShipID)
		if !ok {
			t.Fatalf("missing starter bootstrap ship for installer ship %d", sharedLoadout.ShipID)
		}
		wantLoadoutMeta, ok := dreadconfig.ItemByID(sharedLoadout.LoadoutID)
		if !ok {
			t.Fatalf("missing shared starter loadout metadata for %d", sharedLoadout.LoadoutID)
		}
		if loadout.ship.id != wantShip.id {
			t.Fatalf("starter ship id[%d] = %d, want %d", idx, loadout.ship.id, wantShip.id)
		}
		if loadout.ship.name != wantShip.name {
			t.Fatalf("starter ship name[%d] = %q, want %q", idx, loadout.ship.name, wantShip.name)
		}
		wantFleetShipID := fleetStarterShipIDForPrecast(sharedLoadout.LoadoutID)
		if loadout.effectiveFleetShipID() != wantFleetShipID {
			t.Fatalf("starter fleet ship id[%d] = %d, want %d", idx, loadout.effectiveFleetShipID(), wantFleetShipID)
		}
		if loadout.loadoutID() != sharedLoadout.LoadoutID {
			t.Fatalf("starter loadout id[%d] = %d, want %d", idx, loadout.loadoutID(), sharedLoadout.LoadoutID)
		}
		if loadout.loadoutName != wantLoadoutMeta.DisplayName {
			t.Fatalf("starter loadout name[%d] = %q, want %q", idx, loadout.loadoutName, wantLoadoutMeta.DisplayName)
		}
		wantEntryID, ok := nativeStarterLoadoutID(sharedLoadout.LoadoutID)
		if !ok {
			t.Fatalf("missing native starter loadout ID for %d", sharedLoadout.LoadoutID)
		}
		if loadout.entryID() != wantEntryID {
			t.Fatalf("starter loadout entry id[%d] = %q, want %q", idx, loadout.entryID(), wantEntryID)
		}
		if loadout.weaponPrimaryItemID() != sharedStarterSlotItemID(t, sharedLoadout, dreadconfig.SlotWeaponPrimary) {
			t.Fatalf("%s weaponPrimary = %d, want %d", loadout.ship.name, loadout.weaponPrimaryItemID(), sharedStarterSlotItemID(t, sharedLoadout, dreadconfig.SlotWeaponPrimary))
		}
		if loadout.weaponSecondaryItemID() != sharedStarterSlotItemID(t, sharedLoadout, dreadconfig.SlotWeaponSecondary) {
			t.Fatalf("%s weaponSecondary = %d, want %d", loadout.ship.name, loadout.weaponSecondaryItemID(), sharedStarterSlotItemID(t, sharedLoadout, dreadconfig.SlotWeaponSecondary))
		}
		for slotIndex, slotName := range []string{
			dreadconfig.SlotAbilityPrimary,
			dreadconfig.SlotAbilitySecondary,
			dreadconfig.SlotAbilityPerimeter,
			dreadconfig.SlotAbilityInternal,
		} {
			wantID := sharedStarterSlotItemID(t, sharedLoadout, slotName)
			if got := loadout.abilityItemID(slotIndex); got != wantID {
				t.Fatalf("%s %s = %d, want %d", loadout.ship.name, slotName, got, wantID)
			}
		}
	}
}

func TestStarterLoadoutsUseRealPrecastIDsAndActiveFlags(t *testing.T) {
	expectedPrecastIDs := map[int32]int32{}
	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		expectedPrecastIDs[loadout.LoadoutID] = loadout.LoadoutID
	}
	// The SHIPPING precast blueprints. These used to name the Development
	// variants under /Game/Generic/Loadouts/Precast/Development/, which made the
	// client instantiate a development blueprint and report that blueprint's own
	// m_precastLoadoutID (33489198 and friends) instead of the id we asked for.
	expectedNativeIDs := map[int32]string{
		33489262: "Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C",
		33489423: "Default__VH_DreadnoughtMedium_T1_PrecastLoadout_BP_C",
		33489263: "Default__VH_SniperMedium_T1_PrecastLoadout_BP_C",
		33489264: "Default__VH_SupportMedium_T1_PrecastLoadout_BP_C",
	}

	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	staticFleetData := buildMmogStaticFleetDataPayload()
	loadouts := starterShipLoadouts()
	hangarFleet := starterFleetState()
	hangarLoadoutIDs := make(map[int32]struct{}, len(hangarFleet.shipLoadouts))
	for _, loadout := range hangarFleet.shipLoadouts {
		hangarLoadoutIDs[loadout.loadoutID()] = struct{}{}
	}

	for _, loadout := range loadouts {
		expectedID, ok := expectedPrecastIDs[loadout.loadoutID()]
		if !ok {
			t.Fatalf("missing expected precast loadout id for %d", loadout.loadoutID())
		}
		if loadout.precastLoadoutID != expectedID {
			t.Fatalf("%s precast loadout id = %d, want %d", loadout.ship.name, loadout.precastLoadoutID, expectedID)
		}
		if loadout.loadoutID() != expectedID {
			t.Fatalf("%s loadout id = %d, want %d", loadout.ship.name, loadout.loadoutID(), expectedID)
		}
		if loadout.entryID() != expectedNativeIDs[expectedID] {
			t.Fatalf("%s native loadout id = %q, want %q", loadout.ship.name, loadout.entryID(), expectedNativeIDs[expectedID])
		}
		if !loadout.active {
			t.Fatalf("%s starter loadout should be active", loadout.ship.name)
		}
		if !bytes.Contains(playerGet, protocol.AppendStringField(nil, "ID", loadout.entryID())) {
			t.Fatalf("YA_PlayerGet missing starter loadout native ID %q", loadout.entryID())
		}
		if !bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "ID", loadout.entryID())) {
			t.Fatalf("YA_RequestStaticFleetData missing starter loadout native ID %q", loadout.entryID())
		}

		if _, ok := hangarLoadoutIDs[expectedID]; ok &&
			!bytes.Contains(staticFleetData, protocol.AppendUnnamedInt32Field(nil, expectedID)) &&
			!bytes.Contains(staticFleetData, protocol.AppendInt32Field(nil, "LoadoutID", expectedID)) {
			t.Fatalf("YA_RequestStaticFleetData missing starter fleet loadout reference %d", expectedID)
		}
	}

	for _, staleID := range []string{"Agosta", "Simargl", "Rurik", "Cerberus"} {
		if bytes.Contains(playerGet, protocol.AppendStringField(nil, "ID", staleID)) {
			t.Fatalf("YA_PlayerGet native loadout IDs should use development-table object IDs, not stale display id %q", staleID)
		}
		if bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "ID", staleID)) {
			t.Fatalf("YA_RequestStaticFleetData native loadout IDs should use development-table object IDs, not stale display id %q", staleID)
		}
	}
	// INVERTED on evidence. These used to require the development blueprints and
	// forbid the shipping precast ones. The client instantiates whatever class is
	// named here and then reports THAT blueprint's own m_precastLoadoutID, so
	// naming a development BP made it report 33489198 and friends -- ids no
	// ship-class check can accept. See nativeStarterLoadoutClassName.
	if !bytes.Contains(playerGet, []byte("PrecastLoadout_BP")) {
		t.Fatal("YA_PlayerGet must name the shipping precast loadout blueprints")
	}
	if !bytes.Contains(staticFleetData, []byte("PrecastLoadout_BP")) {
		t.Fatal("YA_RequestStaticFleetData must name the shipping precast loadout blueprints")
	}

	if !bytes.Contains(playerGet, appendFieldMarker("precastLoadout", 0x56)) {
		t.Fatal("YA_PlayerGet should emit starter loadouts as MMOG ShipLoadouts")
	}
	if !bytes.Contains(staticFleetData, appendFieldMarker("precastLoadout", 0x56)) {
		t.Fatal("YA_RequestStaticFleetData should emit starter loadouts as MMOG ShipLoadouts")
	}
}

func TestMmogPlayerStateNormalizesStaleStarterNativeLoadoutIDs(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "abababababababababababababababab"

	_ = buildMmogPlayerGetPayload(playerPID)
	if _, err := database.Exec(`UPDATE player_ship_loadouts SET native_loadout_id='Agosta' WHERE user_id=? AND precast_loadout_id=33489262`, playerPID); err != nil {
		t.Fatalf("seed stale native id: %v", err)
	}

	payload := buildMmogPlayerGetPayload(playerPID)
	if bytes.Contains(payload, protocol.AppendStringField(nil, "ID", "Agosta")) {
		t.Fatal("YA_PlayerGet still exposes stale Agosta native loadout ID")
	}
	var nativeLoadoutID string
	if err := database.QueryRow(`SELECT native_loadout_id FROM player_ship_loadouts WHERE user_id=? AND precast_loadout_id=33489262`, playerPID).Scan(&nativeLoadoutID); err != nil {
		t.Fatalf("query normalized native loadout id: %v", err)
	}
	if nativeLoadoutID != "Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C" {
		t.Fatalf("normalized assault native loadout ID = %q", nativeLoadoutID)
	}
}

func TestNativeLoadoutShapesStayConsistentAcrossPlayerPayloads(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "cccccccccccccccccccccccccccccccc"
	starter := starterShipLoadouts()[0]
	_ = buildMmogPlayerGetPayload(playerPID)
	mutation := protocol.AppendInt32Field(nil, "LoadoutID", starter.loadoutID())
	mutation = protocol.AppendInt32Field(mutation, "weaponPrimary", 123456789)
	if err := persistMmogPlayerMutation(playerPID, "YA_UpdateShipLoadout", mutation); err != nil {
		t.Fatalf("persist custom loadout: %v", err)
	}

	playerGet := buildMmogPlayerGetPayload(playerPID)
	playerFleets := buildMmogPlayerFleetsPayload(playerPID)
	starterFleet := starterFleetState()

	for payloadName, payload := range map[string][]byte{
		"YA_PlayerGet": playerGet,
	} {
		if !bytes.Contains(payload, appendFieldMarker("ShipLoadouts", 0x0d)) {
			t.Fatalf("%s missing ShipLoadouts array", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("ID", 0x09)) {
			t.Fatalf("%s missing native loadout ID field", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("precastLoadout", 0x56)) {
			t.Fatalf("%s missing native precastLoadout field", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("name", 0x09)) {
			t.Fatalf("%s missing native loadout name field", payloadName)
		}
		// class is sent as a numeric string now (see int32SliceToStrings' doc
		// comment in response_builders.go), not int32.
		if !bytes.Contains(payload, appendFieldMarker("class", 0x09)) {
			t.Fatalf("%s missing native loadout class field", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("displayInfo", 0x09)) {
			t.Fatalf("%s missing native displayInfo field", payloadName)
		}
		for _, field := range []string{
			"weaponPrimary",
			"weaponSecondary",
			"abilityPrimary",
			"abilitySecondary",
			"abilityPerimeter",
			"abilityInternal",
			"perkCom",
			"perkWeapon",
			"perkNavigation",
			"perkEngineer",
		} {
			// Sent as numeric strings now (see int32SliceToStrings' doc
			// comment in response_builders.go), not int32.
			if !bytes.Contains(payload, appendFieldMarker(field, 0x09)) {
				t.Fatalf("%s missing native loadout slot field %s", payloadName, field)
			}
		}
		if !bytes.Contains(payload, appendFieldMarker("m_loadoutID", 0x56)) {
			t.Fatalf("%s missing m_loadoutID field", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("m_displayInfo", 0x09)) {
			t.Fatalf("%s missing m_displayInfo field", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("m_weaponIDs", 0x0d)) {
			t.Fatalf("%s missing m_weaponIDs array", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("m_abilityIDs", 0x0d)) {
			t.Fatalf("%s missing m_abilityIDs array", payloadName)
		}
		if !bytes.Contains(payload, appendFieldMarker("m_perkIDs", 0x0d)) {
			t.Fatalf("%s missing m_perkIDs array", payloadName)
		}
	}

	if !bytes.Contains(playerFleets, appendFieldMarker("m_loadoutList", 0x0d)) {
		t.Fatal("YA_PlayerFleets missing m_loadoutList array")
	}
	if !bytes.Contains(playerGet, appendFieldMarker("m_loadoutList", 0x0d)) {
		t.Fatal("YA_PlayerGet missing m_loadoutList array")
	}
	// Scalar m_-prefixed fields go through the same int32-blind parser family
	// as the rest of this payload — numeric strings (0x09), not int32 (0x56).
	if !bytes.Contains(playerGet, appendFieldMarker("m_fleetId", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_fleetId field")
	}
	if !bytes.Contains(playerGet, appendFieldMarker("m_flagshipIndex", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_flagshipIndex field")
	}
	if !bytes.Contains(playerGet, appendFieldMarker("m_fleetType", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_fleetType field")
	}
	// UE4 FName comparison is case-insensitive, so any field whose lowercase form
	// equals "flagshipid" collides with FlagShipID. Sending such a field with the
	// loadout ID (instead of the ship ID) overwrote FlagShipID and made the client
	// drop every fleet entry with "Invalid fleet data, fleet array is empty". The
	// canonical FlagShipID must always carry the ship ID, and no case-insensitive
	// duplicate may carry a different value.
	// FlagShipID in a Fleets array entry is sent as a numeric string, not
	// int32 (see int32SliceToStrings' doc comment in response_builders.go).
	if !bytes.Contains(playerFleets, protocol.AppendStringField(nil, "FlagShipID", strconv.Itoa(int(starterFleet.flagshipShipID)))) {
		t.Fatalf("YA_PlayerFleets missing FlagShipID=%d", starterFleet.flagshipShipID)
	}
	if bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "flagshipID", starterFleet.flagshipLoadoutID)) {
		t.Fatal("YA_PlayerFleets must not emit flagshipID (case-insensitive duplicate of FlagShipID) with a non-ship value")
	}
	// shipCount/DisplayName/Unlocked/Type/Name/FleetID/flagshipShipId/bIsActive
	// were intentionally dropped from the Fleets entry — the client's parser
	// (FUN_142a77910) never reads them and the hangar UI sources unlock/display
	// state from the tech tree. Assert the entry no longer carries shipCount.
	if bytes.Contains(playerFleets, protocol.AppendStringField(nil, "shipCount", strconv.Itoa(len(starterFleet.shipLoadouts)))) {
		t.Fatal("YA_PlayerFleets should no longer emit the unread shipCount field")
	}
	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "m_flagshipIndex", starterFleet.flagshipIndex())) {
		t.Fatalf("YA_PlayerFleets missing m_flagshipIndex=%d", starterFleet.flagshipIndex())
	}
	for _, field := range []struct {
		name  string
		value []byte
	}{
		// AutoRepair is a genuine bool client-side and stays as a bool field.
		// The rest are read through the client's int32-blind fleet-array
		// parser (see int32SliceToStrings' doc comment in
		// response_builders.go) and must be numeric strings.
		{name: "AutoRepair", value: protocol.AppendBoolField(nil, "AutoRepair", false)},
		{name: "Maintenance", value: protocol.AppendStringField(nil, "Maintenance", "0")},
		{name: "LastWinTime", value: protocol.AppendStringField(nil, "LastWinTime", "0")},
		{name: "ChargingBeginTime", value: protocol.AppendStringField(nil, "ChargingBeginTime", "0")},
		{name: "ChargingCharges", value: protocol.AppendStringField(nil, "ChargingCharges", "1")},
		{name: "Rating", value: protocol.AppendStringField(nil, "Rating", "0")},
	} {
		if !bytes.Contains(playerFleets, field.value) {
			t.Fatalf("YA_PlayerFleets missing %s default state", field.name)
		}
	}
}

// TestPlayerPayloadsHaveNoFNameCollisions guards the bootstrap payloads against
// case-insensitive duplicate field names within the same parent object. UE4
// FName comparison is case insensitive, so any two sibling fields whose
// lowercase form is identical collide in the client's parsed object name
// table. When the colliding fields carry different values, the second field
// overwrites the first; this is exactly how "FlagShipID" (ship id) used to be
// clobbered by "flagshipID" (loadout id) and make the client report
// "Invalid fleet data, fleet array is empty" while loading the hangar.
func TestPlayerPayloadsHaveNoFNameCollisions(t *testing.T) {
	const pid = defaultMmogPlayerPID
	payloads := map[string][]byte{
		"YA_PlayerFleets":           buildMmogPlayerFleetsPayload(pid),
		"YA_PlayerGet":              buildMmogPlayerGetPayload(pid),
		"YA_RefreshPlayerProfile":   buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", pid),
		"YA_RequestStaticFleetData": buildMmogStaticFleetDataPayload(),
	}
	for name, payload := range payloads {
		assertMmogPayloadHasNoSiblingFNameCollisions(t, name, payload)
	}
}

// assertMmogPayloadHasNoSiblingFNameCollisions walks the binary MMOG payload
// and fails the test when any object/array contains two children with the same
// case-insensitive FName but different original cases (which means the second
// would silently overwrite the first in the client's name table).
func assertMmogPayloadHasNoSiblingFNameCollisions(t *testing.T, payloadName string, payload []byte) {
	t.Helper()
	// Stack of per-scope "seen" maps. seen[depth][lowercase-name] = first-seen
	// original case. Index 0 is the implicit root scope.
	seenStack := []map[string]string{{}}
	idx := 0
	for idx < len(payload) {
		// Each field starts with: <name_len><name?><type><type-data>...
		if idx+1 > len(payload) {
			return
		}
		nameLen := int(payload[idx])
		idx++
		fieldName := ""
		if nameLen > 0 {
			if idx+nameLen > len(payload) {
				return
			}
			fieldName = string(payload[idx : idx+nameLen])
			idx += nameLen
		}
		if idx >= len(payload) {
			return
		}
		typeByte := payload[idx]
		idx++

		// End-marker fields close the current scope and are unnamed; they have
		// a 4-byte start offset payload, no children-name-table entry.
		if typeByte == 0x0e {
			idx += 4
			if len(seenStack) > 1 {
				seenStack = seenStack[:len(seenStack)-1]
			}
			continue
		}

		// Record the field name in the current scope's seen map, if it has a
		// name. Unnamed array entries (nameLen == 0) don't participate in name
		// lookups.
		if fieldName != "" {
			scope := seenStack[len(seenStack)-1]
			lower := strings.ToLower(fieldName)
			if existing, ok := scope[lower]; ok && existing != fieldName {
				t.Fatalf("%s contains FName-colliding sibling fields %q and %q (both lowercase to %q)",
					payloadName, existing, fieldName, lower)
			}
			scope[lower] = fieldName
		}

		switch typeByte {
		case 0x09, 0x0a: // string / byte array (same u32 length prefix)
			if idx+4 > len(payload) {
				return
			}
			strLen := int(binary.LittleEndian.Uint32(payload[idx : idx+4]))
			idx += 4 + strLen
		case 0x56: // int32
			idx += 4
		case 0x05: // bool
			idx++
		case 0x0c, 0x0d: // object / array - opens a new scope
			idx += 4 // skip 4-byte placeholder
			seenStack = append(seenStack, map[string]string{})
		default:
			// Unknown type: bail out rather than misalign the walk.
			return
		}
	}
}

func TestStarterLoadoutIdentifiersStayUniquePerShipClass(t *testing.T) {
	loadouts := starterShipLoadouts()
	seenIDs := map[int32]struct{}{}
	perClassCount := map[int32]int{}

	for _, loadout := range loadouts {
		if _, exists := seenIDs[loadout.loadoutID()]; exists {
			t.Fatalf("duplicate starter loadout id %d", loadout.loadoutID())
		}
		seenIDs[loadout.loadoutID()] = struct{}{}

		perClassCount[loadout.ship.shipClass]++
		if perClassCount[loadout.ship.shipClass] > 2 {
			t.Fatalf("ship class %d has %d starter loadouts, want at most 2", loadout.ship.shipClass, perClassCount[loadout.ship.shipClass])
		}
	}
}

func TestStarterLoadoutDetailSlotCountsStayHangarSafe(t *testing.T) {
	for _, loadout := range starterShipLoadouts() {
		configLoadout := sharedStarterLoadoutByID(t, loadout.loadoutID())
		perkCount := 0
		for _, slot := range configLoadout.Slots {
			if len(slot.SlotName) >= 4 && slot.SlotName[:4] == "perk" {
				perkCount++
			}
		}
		if got := len(loadout.weaponSlots()); got != 2 {
			t.Fatalf("%s weapon slot count = %d, want 2", loadout.ship.name, got)
		}
		if got := len(loadout.abilitySlots()); got != 4 {
			t.Fatalf("%s ability slot count = %d, want 4", loadout.ship.name, got)
		}
		if got := len(loadout.perkSlots()); got != perkCount {
			t.Fatalf("%s perk slot count = %d, want %d", loadout.ship.name, got, perkCount)
		}
		if got := len(loadout.loadoutSlots()); got != len(configLoadout.Slots) {
			t.Fatalf("%s combined slot count = %d, want %d", loadout.ship.name, got, len(configLoadout.Slots))
		}
	}
}

func TestBootstrapPayloadsExposeFullFleetWithoutHeavyBattleReadyData(t *testing.T) {
	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	playerFleets := buildMmogPlayerFleetsPayload(defaultMmogPlayerPID)
	staticFleetData := buildMmogStaticFleetDataPayload()
	starterFleet := starterFleetState()

	for payloadName, payload := range map[string][]byte{
		"YA_PlayerGet":              playerGet,
		"YA_RequestStaticFleetData": staticFleetData,
	} {
		for _, field := range []string{
			"EquippedLoadoutItems",
			"AvailableLoadoutItems",
			"PreviewLoadoutItems",
		} {
			if bytes.Contains(payload, appendFieldMarker(field, 0x0d)) {
				t.Fatalf("%s should not include %s after payload trim", payloadName, field)
			}
		}
	}

	for _, payloadName := range []string{"YA_PlayerGet", "YA_RequestStaticFleetData"} {
		payload := map[string][]byte{
			"YA_PlayerGet":              playerGet,
			"YA_RequestStaticFleetData": staticFleetData,
		}[payloadName]
		for _, field := range []string{"m_setLoadoutData", "BattleReadyFleetsInfo"} {
			if bytes.Contains(payload, appendFieldMarker(field, 0x0d)) {
				t.Fatalf("%s should not include %s after payload trim", payloadName, field)
			}
		}
	}

	for _, field := range []string{"OwnedShipLoadouts", "PreviewLoadoutItems"} {
		if bytes.Contains(playerGet, appendFieldMarker(field, 0x0d)) {
			t.Fatalf("YA_PlayerGet should not include %s after payload trim", field)
		}
	}
	// "Items" is NOT legacy bloat: it is the owned-item inventory. It feeds the
	// player-data snapshot at +0x150/+0x158, which is the only source
	// UYInventoryManager::UpdateItemsFromInventory reads. Trimming it made the
	// client log "UpdateItemsFromInventory | Updated 0 items." and left the
	// hangar with nothing to show.
	if !bytes.Contains(playerGet, appendFieldMarker("Items", 0x0d)) {
		t.Fatal("YA_PlayerGet must include the owned-item Items array")
	}
	for _, field := range []string{"BaseMaintenanceCost", "ChargeTime", "ChargeCost", "AvailableCharges", "ShipsToUnlock"} {
		if !bytes.Contains(staticFleetData, appendFieldMarker(field, 0x09)) {
			t.Fatalf("YA_RequestStaticFleetData missing FleetTypes field %s", field)
		}
	}
	if !bytes.Contains(staticFleetData, appendFieldMarker("FleetRatingMin", 0x09)) {
		t.Fatal("YA_RequestStaticFleetData missing FleetRatingMin string field")
	}
	if !bytes.Contains(staticFleetData, appendFieldMarker("Maintenance", 0x0c)) {
		t.Fatal("YA_RequestStaticFleetData missing Maintenance object")
	}
	if bytes.Contains(playerFleets, appendFieldMarker("BattleReadyFleetsInfo", 0x0d)) {
		t.Fatal("YA_PlayerFleets should not include BattleReadyFleetsInfo after payload trim")
	}
	if bytes.Contains(playerFleets, appendFieldMarker("Bonuses", 0x0d)) {
		t.Fatal("YA_PlayerFleets should not include battle-ready bonus placeholders after payload trim")
	}
	// A new player owns ONLY the Recruit fleet, and it is identified on the wire
	// by its FID — the player's PID — not by the "RecruitFleet" token, which the
	// client rejects because FID must be a GUID it has already interned.
	if !bytes.Contains(playerFleets, appendFieldMarker("FID", 0x09)) {
		t.Fatal("YA_PlayerFleets missing the fleet FID")
	}
	if !bytes.Contains(playerFleets, []byte(defaultMmogPlayerPID)) {
		t.Fatal("YA_PlayerFleets fleet FID should be the player's PID (GUID-shaped and already interned client-side)")
	}
	for _, fleetName := range []string{"VeteranFleet", "LegendaryFleet"} {
		if bytes.Contains(playerFleets, []byte(fleetName)) {
			t.Fatalf("YA_PlayerFleets should not include locked/unowned fleet %q for a new player", fleetName)
		}
	}
	// The sibling root-level "Items" array corrupted the client's parsed value
	// tree for "result", producing a nonsense element count and a phantom entry
	// that failed validation. It must stay out.
	if bytes.Contains(playerFleets, appendFieldMarker("Items", 0x0d)) {
		t.Fatal("YA_PlayerFleets must not carry a root-level Items array — it breaks the client's fleet-array parse")
	}
	if got := bytes.Count(playerFleets, appendFieldMarker("m_fleetId", 0x56)); got != 1 {
		t.Fatalf("YA_PlayerFleets fleet row count = %d, want 1 (Recruit only)", got)
	}
	staticSlots := extractNamedMmogArray(t, extractNamedMmogObject(t, staticFleetData, "result"), "Fleets")
	if got := bytes.Count(staticSlots, appendFieldMarker("ShipSlots", 0x0d)); got != 1 {
		t.Fatalf("YA_RequestStaticFleetData active fleet count = %d, want 1", got)
	}
	if got := bytes.Count(staticFleetData, appendFieldMarker("LoadoutID", 0x56)); got < len(starterFleet.shipLoadouts) {
		t.Fatalf("YA_RequestStaticFleetData loadout id count = %d, want at least %d", got, len(starterFleet.shipLoadouts))
	}
}

func TestPlayerGetPayloadUsesSquadObjectShape(t *testing.T) {
	const playerPID = "b7c42c0f3ac648a182ccfd35eb24f128"

	payload := buildMmogPlayerGetPayload(playerPID)
	squad := extractNamedMmogObject(t, payload, "Squad")
	users := extractNamedMmogArray(t, squad, "Users")

	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "PID", value: ""},
		{name: "PIDLeader", value: ""},
		{name: "GameMode", value: ""},
	} {
		if !bytes.Contains(squad, protocol.AppendStringField(nil, field.name, field.value)) {
			t.Fatalf("Squad missing %s=%q", field.name, field.value)
		}
	}
	for _, field := range []struct {
		name  string
		value int32
	}{
		{name: "State", value: 0},
		{name: "FleetType", value: 0},
	} {
		if !bytes.Contains(squad, protocol.AppendInt32Field(nil, field.name, field.value)) {
			t.Fatalf("Squad missing %s=%d", field.name, field.value)
		}
	}
	if bytes.Contains(users, appendFieldMarker("PID", 0x09)) {
		t.Fatal("Squad.Users should not contain fabricated squad members")
	}
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "PPF", "")) {
		t.Fatal("YA_PlayerGet should encode empty PPF as a string")
	}
	if bytes.Contains(payload, appendFieldMarker("PPF", 0x0d)) {
		t.Fatal("PPF should not be encoded as an array")
	}
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "LGVersion", "0")) {
		t.Fatal("YA_PlayerGet should encode LGVersion as a string")
	}
	if bytes.Contains(payload, protocol.AppendInt32Field(nil, "LGVersion", 0)) {
		t.Fatal("LGVersion should not be encoded as an int32")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		// Both go through the int32-blind parser confirmed for tll/tpl/tc/etc
		// — sent as numeric strings, not int32.
		{name: "DailyContractStateID", value: "0"},
		{name: "tslm", value: "0"},
	} {
		if !bytes.Contains(payload, protocol.AppendStringField(nil, field.name, field.value)) {
			t.Fatalf("YA_PlayerGet missing %s=%s", field.name, field.value)
		}
	}
	// issue #43: the client's top-level parser (FUN_142a70da0 ->
	// FUN_142a69310) reads Quests from the same object as DailyContractStateID
	// etc — it must be present (this replaces the old, incorrect assertion
	// that it should be absent).
	if !bytes.Contains(payload, appendFieldMarker("Quests", 0x0d)) {
		t.Fatal("YA_PlayerGet missing Quests array")
	}
	if bytes.Contains(payload, appendFieldMarker("QuestID", 0x56)) {
		t.Fatal("YA_PlayerGet should not fabricate daily contract entries")
	}
	shipXps := extractNamedMmogArray(t, payload, "ShipXps")
	if bytes.Contains(shipXps, appendFieldMarker("ShipID", 0x56)) {
		t.Fatal("ShipXps should not fabricate ship XP entries")
	}
	customRoom := extractNamedMmogObject(t, payload, "CustomRoom")
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "roomId", value: ""},
		{name: "hostPid", value: ""},
		{name: "gameMode", value: ""},
		{name: "mapName", value: ""},
		{name: "chatRoomId", value: ""},
	} {
		if !bytes.Contains(customRoom, protocol.AppendStringField(nil, field.name, field.value)) {
			t.Fatalf("CustomRoom missing %s=%q", field.name, field.value)
		}
	}
	for _, field := range []string{"teams", "settings", "supportedModes", "supportedMaps"} {
		container := extractNamedMmogArray(t, customRoom, field)
		if bytes.Contains(container, appendFieldMarker("PID", 0x09)) {
			t.Fatalf("CustomRoom.%s should not fabricate entries", field)
		}
	}
}

func TestDailyContractsPayloadIsInertButParserShaped(t *testing.T) {
	payload := buildMmogDailyContractsDataPayload()

	for _, field := range []struct {
		name  string
		value int32
	}{
		{name: "DailyContractStateID", value: 0},
	} {
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, field.name, field.value)) {
			t.Fatalf("YA_GetDailyContractsData missing %s=%d", field.name, field.value)
		}
	}

	quests := extractNamedMmogArray(t, payload, "Quests")
	topLevelContracts := extractNamedMmogArray(t, payload, "Contracts")
	resultContracts := extractNamedMmogArray(t, extractNamedMmogObject(t, payload, "result"), "Contracts")
	for name, container := range map[string][]byte{
		"Quests":           quests,
		"Contracts":        topLevelContracts,
		"result.Contracts": resultContracts,
	} {
		if bytes.Contains(container, appendFieldMarker("QuestID", 0x56)) ||
			bytes.Contains(container, appendFieldMarker("ContractID", 0x56)) ||
			bytes.Contains(container, appendFieldMarker("ID", 0x09)) {
			t.Fatalf("%s should not fabricate quest/contract entries", name)
		}
	}
	if bytes.Contains(payload, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatal("YA_GetDailyContractsData should not substitute status-only result for result.Contracts")
	}
}

func TestCareerPayloadsUseGoalsModel(t *testing.T) {
	// The client parses career progression as a GOALS system, not the
	// progression-item taxonomy this used to send. FYCareerProgressionConfig::Load
	// reads "CareerGoalsConfig" from the static response and resolves m_category /
	// m_platformVisibility / m_rewardType through UEnum::GetValueByName, so those
	// must be enum NAME strings. FYCareerProgressionData::Update reads
	// {goalId, progress} from the dynamic response and rejects ids that are not in
	// the static config, so both sides must agree on m_id.
	staticCareerData := buildMmogStaticCareerDataPayload()
	careerProgression := buildMmogCareerProgressionPayload(defaultMmogPlayerPID)
	goals := careerGoalsConfig()
	if len(goals) == 0 {
		t.Fatal("careerGoalsConfig must not be empty — an empty config is what made the client log \"Career progression Data empty\"")
	}

	// The dispatcher hands the parsers the response's "result" child, so both
	// payloads are wrapped -- but in different shapes. Static: result is an
	// OBJECT that Load() then looks up "CareerGoalsConfig" on.
	staticResult := extractNamedMmogObject(t, staticCareerData, "result")
	config := extractNamedMmogArray(t, staticResult, "CareerGoalsConfig")
	for _, field := range []string{"m_id", "m_title", "m_description", "m_counterID", "m_category", "m_platformVisibility", "m_stageData"} {
		if !bytes.Contains(config, appendFieldMarker(field, 0x09)) && !bytes.Contains(config, appendFieldMarker(field, 0x0d)) {
			t.Fatalf("CareerGoalsConfig entries missing %q", field)
		}
	}
	for _, enumName := range []string{"EYGoalCategory::YGC_RECRUIT", "EYGoalRewardType::YGR_CREDITS", "EYGoalPlatformVisibility::YGPV_PC"} {
		if !bytes.Contains(config, []byte(enumName)) {
			t.Fatalf("CareerGoalsConfig should carry the FULLY-QUALIFIED enum name %q (UEnum::GetValueByName rejects bare entry names)", enumName)
		}
	}
	if bytes.Contains(staticCareerData, appendFieldMarker("m_categories", 0x0d)) {
		t.Fatal("static career data should no longer send m_categories — that belongs to UYPlayerMatchStatisticsManager, not career progression")
	}

	// Dynamic: Update() reads the result node's element count and walks it
	// directly, so result IS the array -- the YA_PlayerFleets shape.
	progress := extractNamedMmogArray(t, careerProgression, "result")
	for _, field := range []string{"goalId", "progress", "claimed_stage"} {
		if !bytes.Contains(progress, appendFieldMarker(field, 0x09)) {
			t.Fatalf("career progression entries missing %q as a string field", field)
		}
	}

	// Both parsers read numbers through the client's restrictive tagged union
	// (bool/double/int64/string), where an int32 field silently reads back 0.
	for _, field := range []string{"progress", "claimed_stage"} {
		if bytes.Contains(progress, appendFieldMarker(field, 0x56)) {
			t.Fatalf("career progression %q must be a numeric string, not int32", field)
		}
	}
	for _, field := range []string{"m_amountToComplete", "m_reward"} {
		if bytes.Contains(config, appendFieldMarker(field, 0x56)) {
			t.Fatalf("CareerGoalsConfig %q must be a numeric string, not int32", field)
		}
	}
	for _, goal := range goals {
		if !bytes.Contains(progress, []byte(goal.id)) {
			t.Fatalf("career progression missing goal id %q declared in the static config", goal.id)
		}
	}
}
func TestSeasonProgressPayloadUsesEmptyParserShape(t *testing.T) {
	payload := buildMmogSeasonProgressPayload()
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", "YA_GetSeasonProgress")) {
		t.Fatal("YA_GetSeasonProgress missing RT acknowledgement")
	}
	result := extractNamedMmogObject(t, payload, "result")
	for _, field := range []string{"EventScores", "EventRewards", "SeasonRewards"} {
		container := extractNamedMmogArray(t, result, field)
		if bytes.Contains(container, appendFieldMarker("EventID", 0x56)) ||
			bytes.Contains(container, appendFieldMarker("SeasonID", 0x56)) ||
			bytes.Contains(container, appendFieldMarker("ID", 0x09)) {
			t.Fatalf("YA_GetSeasonProgress %s should not fabricate rows", field)
		}
	}
}

func TestSeasonDataPayloadUsesStructuredSeasonAndEventTables(t *testing.T) {
	result := extractNamedMmogObject(t, buildMmogSeasonDataPayload(), "result")

	// Seasons/Events must be well-formed, NON-empty JSON arrays: the client
	// imports each as a JSON DataTable and rejects "[]" with "Failed to parse
	// the JSON data" (observed live). They are declared inactive instead of
	// omitted. See buildMmogSeasonDataPayload.
	seasonsRaw := protocol.ExtractStringField(result, "Seasons")
	if !strings.HasPrefix(seasonsRaw, "[{") {
		t.Fatalf("YA_GetSeasonData Seasons must be a non-empty JSON array the client can import, got %q", seasonsRaw)
	}
	if !strings.Contains(seasonsRaw, `"m_active":false`) {
		t.Fatalf("YA_GetSeasonData Seasons must declare no active season, got %q", seasonsRaw)
	}
	eventsRaw := protocol.ExtractStringField(result, "Events")
	if !strings.HasPrefix(eventsRaw, "[{") {
		t.Fatalf("YA_GetSeasonData Events must be a non-empty JSON array the client can import, got %q", eventsRaw)
	}
	// CurrentSeason intentionally EMPTY: an active season activates the
	// client's UYPlayerMPQuestCycle, which infinite-recurses loading MP season
	// quests and crashes (see buildMmogSeasonDataPayload). Empty makes the
	// client clear active-season state and never start the quest cycle.
	if currentSeason := protocol.ExtractStringField(result, "CurrentSeason"); currentSeason != "" {
		t.Fatalf("YA_GetSeasonData CurrentSeason = %q, want empty (no active season)", currentSeason)
	}
	if activeEvent := protocol.ExtractStringField(result, "ActiveEvent"); activeEvent != "" {
		t.Fatalf("YA_GetSeasonData ActiveEvent = %q, want empty", activeEvent)
	}
}

func TestTunePayloadUsesClientParserShape(t *testing.T) {
	payload := buildMmogTunePayload()
	if rt := protocol.ExtractStringField(payload, "RT"); rt != "YA_Tune" {
		t.Fatalf("YA_Tune RT = %q, want YA_Tune", rt)
	}

	returning := extractNamedMmogObject(t, payload, "Returning")
	if version := protocol.ExtractStringField(returning, "Version"); version != "1.0.0" {
		t.Fatalf("YA_Tune Version = %q, want 1.0.0", version)
	}

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
		sectionJSON := protocol.ExtractStringField(returning, section)
		if sectionJSON == "" {
			t.Fatalf("YA_Tune %s missing or empty", section)
		}
	}

	result := extractNamedMmogObject(t, payload, "result")
	if status := protocol.ExtractStringField(result, fieldStatus); status != "ok" {
		t.Fatalf("YA_Tune result.status = %q, want ok", status)
	}
}

func TestPlayerBootstrapAvoidsSyntheticOfficersAndPurchases(t *testing.T) {
	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	officers := extractNamedMmogArray(t, playerGet, "Officers")
	if bytes.Contains(officers, appendFieldMarker("OfficerID", 0x09)) ||
		bytes.Contains(officers, appendFieldMarker("Name", 0x09)) ||
		bytes.Contains(officers, appendFieldMarker("Effect", 0x09)) {
		t.Fatal("YA_PlayerGet should not include synthetic officer rows with non-client parser fields")
	}

	purchases := extractNamedMmogArray(t, extractNamedMmogObject(t, buildMmogPlayerPurchasesPayload(), "result"), "PurchasesData")
	if bytes.Contains(purchases, []byte{0x00, 0x56}) {
		t.Fatal("YA_GetPlayerPurchases should not synthesize starter inventory purchases")
	}
}

func TestCriticalPayloadsMaintainValidMmogNesting(t *testing.T) {
	pid := defaultMmogPlayerPID

	builders := map[string]func() []byte{
		"YA_UserLogin":                 func() []byte { return buildMmogLoginSuccessPayload() },
		"YA_RequestSuccess":            func() []byte { return buildMmogRequestSuccessPayload("YA_Tune") },
		"YA_PlayerGet":                 func() []byte { return buildMmogPlayerGetPayload(pid) },
		"YA_PlayerFleets":              func() []byte { return buildMmogPlayerFleetsPayload(pid) },
		"YA_RequestStaticFleetData":    buildMmogStaticFleetDataPayload,
		"YA_GetSeasonData":             buildMmogSeasonDataPayload,
		"YA_GetSeasonProgress":         buildMmogSeasonProgressPayload,
		"YA_GetPlayerStatsCounterData": func() []byte { return buildMmogPlayerStatsCounterDataPayload() },
		"YA_GetPlayerProgression":      func() []byte { return buildMmogPlayerProgressionPayload(pid) },
		"YA_GetTechTree":               func() []byte { return buildMmogTechTreePayload(pid) },
		"YA_GetPlayerPurchases":        buildMmogPlayerPurchasesPayload,
		"YA_FleetEligibility":          buildMmogFleetEligibilityPayload,
		"YA_Tune":                      buildMmogTunePayload,
		"YA_RefreshPlayerProfile":      func() []byte { return buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", pid) },
		"YA_EnterMatchmaking":          func() []byte { return buildMmogEnterMatchmakingPayload("YA_EnterMatchmaking", pid, nil) },
		"YA_LeaveMatchmaking":          func() []byte { return buildMmogLeaveMatchmakingPayload("YA_LeaveMatchmaking", pid) },
		"YA_QueryRooms":                buildMmogQueryRoomsPayload,
		"YA_SquadInvite":               func() []byte { return buildMmogSquadPayload("YA_SquadInvite", pid) },
		"YA_GlobalChat":                func() []byte { return buildMmogChatPayload("YA_GlobalChat", pid, nil) },
	}

	for name, builder := range builders {
		t.Run(name, func(t *testing.T) {
			payload := protocol.AppendRootEnd(builder())
			validateMmogPayloadNesting(t, payload)
		})
	}
}

func TestPlayerFleetsIsDeferredUntilPlayerGet(t *testing.T) {
	conn := &captureConn{}
	fleetRequestID := syntheticRequestID(0xa1)
	requestPayload := protocol.AppendStringField(nil, "RT", "YA_PlayerFleets")
	requestPayload = protocol.AppendRootEnd(requestPayload)
	state := &mmogConnState{
		playerPID:                defaultMmogPlayerPID,
		loginResponseSent:        true,
		staticFleetDataReceived:  true,
		fleetEligibilityReceived: true,
	}

	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: fleetRequestID,
		Payload:   requestPayload,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames: %v", err)
	}

	// The client rejects a byte-perfect Fleets array with "Invalid fleet data,
	// fleet array is empty" (then HandleMmogbrainError(8)) if it arrives before
	// player data exists, so nothing may go out yet — verified against a live
	// client, 2026-07-27.
	if conn.Len() != 0 {
		t.Fatalf("YA_PlayerFleets answered before YA_PlayerGet, want deferral")
	}
	if len(state.pendingPlayerFleets) != 1 {
		t.Fatalf("pendingPlayerFleets = %d, want 1", len(state.pendingPlayerFleets))
	}
	if state.playerGetResponded {
		t.Fatal("YA_PlayerFleets should not mark playerGetResponded")
	}
	if !state.playerFleetsReceived {
		t.Fatal("YA_PlayerFleets should mark playerFleetsReceived")
	}

	// Answering YA_PlayerGet must flush the queued fleet response.
	if err := handlePlayerGetSatisfied(logrus.New(), conn, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
	}
	frames, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after parsing fleet response")
	}
	if len(frames) != 1 {
		t.Fatalf("flush wrote %d frames, want 1 (the deferred YA_PlayerFleets response)", len(frames))
	}
	if got := protocol.ExtractRequestName(frames[0].Payload); got != "YA_PlayerFleets" {
		t.Fatalf("flushed frame = %q, want YA_PlayerFleets", got)
	}
	if frames[0].RequestID != fleetRequestID {
		t.Fatalf("deferred response id = %x, want original %x", frames[0].RequestID, fleetRequestID)
	}
	if len(state.pendingPlayerFleets) != 0 {
		t.Fatalf("pendingPlayerFleets = %d after flush, want 0", len(state.pendingPlayerFleets))
	}
}

func TestFleetUpdatePushIsTopLevelWithFleetsArray(t *testing.T) {
	payload := buildMmogFleetUpdatePush(defaultMmogPlayerPID)
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", "YA_FleetUpdate")) {
		t.Fatal("YA_FleetUpdate push missing RT=YA_FleetUpdate")
	}
	fleets := extractNamedMmogArray(t, payload, "Fleets")
	if len(fleets) == 0 {
		t.Fatal("YA_FleetUpdate push has an empty Fleets array")
	}
}

// These bootstrap reads carry no dependency on YA_PlayerGet — every response is
// built from state.playerPID, already selected at YA_UserLogin — so they are
// answered immediately.
//
// YA_PlayerFleets is the exception and IS deferred (see
// TestPlayerFleetsIsDeferredUntilPlayerGet): the client rejects its fleet array
// outright if it lands before player data. The old blanket rule here assumed
// the client blocks on all of these before sending YA_PlayerGet, so deferring
// any of them would deadlock it into a ~44s stall; live client testing on
// 2026-07-27 disproved that for the fleet response specifically — with it
// entirely unanswered, YA_PlayerGet still arrived ~6s after login.
func TestPlayerPurchasesAnsweredImmediately(t *testing.T) {
	conn := &captureConn{}
	purchasesRequestID := syntheticRequestID(0xb1)
	purchasesRequest := protocol.AppendStringField(nil, "RT", "YA_GetPlayerPurchases")
	purchasesRequest = protocol.AppendRootEnd(purchasesRequest)
	state := &mmogConnState{
		playerPID:         defaultMmogPlayerPID,
		loginResponseSent: true,
	}
	setGatewayPlayerDataReadyState(defaultMmogPlayerPID, false)
	t.Cleanup(func() {
		setGatewayPlayerDataReadyState(defaultMmogPlayerPID, false)
	})

	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: purchasesRequestID,
		Payload:   purchasesRequest,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames purchases: %v", err)
	}
	if len(state.pendingPlayerPurchases) != 0 {
		t.Fatal("YA_GetPlayerPurchases must not be deferred; the client blocks on it before YA_PlayerGet")
	}
	frames, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("purchases response left %d trailing bytes", len(remaining))
	}
	if len(frames) != 1 {
		t.Fatalf("YA_GetPlayerPurchases wrote %d frames, want 1 (answered immediately)", len(frames))
	}
	if got := protocol.ExtractRequestName(frames[0].Payload); got != "YA_GetPlayerPurchases" {
		t.Fatalf("frame = %q, want YA_GetPlayerPurchases", got)
	}
	if frames[0].RequestID != purchasesRequestID {
		t.Fatalf("YA_GetPlayerPurchases response id = %x, want %x", frames[0].RequestID, purchasesRequestID)
	}
}

func TestObserverOnlyBootstrapResponsePolicy(t *testing.T) {
	for _, tc := range []struct {
		requestName string
		wantFrames  int
		wantDelayed bool
	}{
		// YA_GetDailyContractsData is deliberately NOT answered — see the
		// withholding site in response_connection.go: any response to it sets
		// interface+0x44c8 = 1, which arms the client's hangar-entry
		// stack-overflow recursion.
		{requestName: "YA_GetDailyContractsData", wantFrames: 0},
		{requestName: "YA_GetSeasonProgress", wantFrames: 1},
	} {
		requestName := tc.requestName
		t.Run(requestName, func(t *testing.T) {
			conn := &captureConn{}
			request := protocol.AppendStringField(nil, "RT", requestName)
			request = protocol.AppendRootEnd(request)
			state := &mmogConnState{
				playerPID:         defaultMmogPlayerPID,
				loginResponseSent: true,
			}

			if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
				MsgType:   0x0320,
				RequestID: syntheticRequestID(0xc1),
				Payload:   request,
			}}, nil, false, state); err != nil {
				t.Fatalf("processMmogAppFrames %s: %v", requestName, err)
			}
			if tc.wantFrames == 0 && conn.Len() != 0 {
				t.Fatalf("%s wrote %d bytes before YA_PlayerGet, want delayed response", requestName, conn.Len())
			}
			if tc.wantDelayed && len(state.pendingDailyContracts) == 0 {
				t.Fatalf("%s was not delayed until YA_PlayerGet", requestName)
			}
			if tc.wantFrames == 0 {
				return
			}
			frames, remaining := protocol.ParseAppFrames(conn.Bytes())
			if len(remaining) != 0 {
				t.Fatalf("%s response left %d trailing bytes", requestName, len(remaining))
			}
			if len(frames) != tc.wantFrames {
				t.Fatalf("%s wrote %d frames, want %d", requestName, len(frames), tc.wantFrames)
			}
			if !bytes.Contains(frames[0].Payload, protocol.AppendStringField(nil, "RT", requestName)) {
				t.Fatalf("%s response did not echo RT", requestName)
			}
		})
	}
}

func TestDailyContractsResponseIsWithheld(t *testing.T) {
	// Answering YA_GetDailyContractsData is what arms the hangar-entry crash:
	// the client's handler parses the payload with FUN_142a6b7f0, which sets
	// interface+0x44c8 = 1 UNCONDITIONALLY (whatever the contents) and then
	// broadcasts interface+0x1070. That gate is what makes
	// UYPlayerMPQuestCycle::OnBackendDataAvailable walk the quest collection
	// and recurse until the stack is exhausted. While the gate stays 0 the
	// cycle just binds to +0x1070 and waits, which is harmless.
	conn := &captureConn{}
	request := protocol.AppendStringField(nil, "RT", "YA_GetDailyContractsData")
	request = protocol.AppendRootEnd(request)
	state := &mmogConnState{
		playerPID:         defaultMmogPlayerPID,
		loginResponseSent: true,
	}

	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: syntheticRequestID(0xd1),
		Payload:   request,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames daily contracts: %v", err)
	}
	if conn.Len() != 0 {
		frames, _ := protocol.ParseAppFrames(conn.Bytes())
		t.Fatalf("YA_GetDailyContractsData wrote %d frames, want 0 (it must go unanswered)", len(frames))
	}
	if len(state.pendingDailyContracts) != 0 {
		t.Fatal("YA_GetDailyContractsData must be dropped outright, not queued for later delivery")
	}
}
func TestMultiplePendingBootstrapRequestsFlushInOrder(t *testing.T) {
	conn := &captureConn{}
	state := &mmogConnState{
		playerPID:         defaultMmogPlayerPID,
		loginResponseSent: true,
	}
	request := protocol.AppendStringField(nil, "RT", "YA_GetPlayerPurchases")
	request = protocol.AppendRootEnd(request)
	frames := []protocol.AppFrame{
		{MsgType: 0x0320, RequestID: syntheticRequestID(0xe1), Payload: request},
		{MsgType: 0x0320, RequestID: syntheticRequestID(0xe2), Payload: request},
	}
	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", frames, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames purchases: %v", err)
	}
	if got := len(state.pendingPlayerPurchases); got != 0 {
		t.Fatalf("pending purchases = %d, want 0 (answered immediately, not deferred)", got)
	}
	if immediate, _ := protocol.ParseAppFrames(conn.Bytes()); len(immediate) != 2 {
		t.Fatalf("expected 2 immediate purchase responses, got %d", len(immediate))
	}

	playerGet := protocol.AppendStringField(nil, "RT", "YA_PlayerGet")
	playerGet = protocol.AppendRootEnd(playerGet)
	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: syntheticRequestID(0xe3),
		Payload:   playerGet,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames PlayerGet: %v", err)
	}
	parsed, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after responses")
	}
	if len(parsed) != 4 {
		t.Fatalf("frame count = %d, want 4 (2 purchases answered immediately + YA_PlayerGet + its currency push)", len(parsed))
	}
	// Both purchase responses are answered immediately in request order, ahead of
	// the later YA_PlayerGet frame.
	if parsed[0].RequestID != frames[0].RequestID || parsed[1].RequestID != frames[1].RequestID {
		t.Fatal("immediate purchase responses did not preserve original request IDs in order")
	}
	if got := protocol.ExtractRequestName(parsed[2].Payload); got != "YA_PlayerGet" {
		t.Fatalf("frame 3 = %q, want YA_PlayerGet", got)
	}
	// The currency push must trail the player data it reflects, never precede it.
	if got := protocol.ExtractRequestName(parsed[3].Payload); got != "YA_RewardCurrencies" {
		t.Fatalf("last frame = %q, want the YA_RewardCurrencies push", got)
	}
}

func TestPlayerGetBootstrapDoesNotPushUnrequestedFleetData(t *testing.T) {
	conn := &captureConn{}
	state := &mmogConnState{
		playerPID:         defaultMmogPlayerPID,
		loginResponseSent: true,
	}

	if err := handlePlayerGetSatisfied(logrus.New(), conn, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
	}

	if conn.Len() != 0 {
		frames, _ := protocol.ParseAppFrames(conn.Bytes())
		t.Fatalf("PlayerGet bootstrap wrote %d unsolicited frames, want 0", len(frames))
	}
	if !state.playerGetResponded {
		t.Fatal("PlayerGet bootstrap should mark playerGetResponded")
	}
}

func TestParseAppFramesPreservesSplitMagicByte(t *testing.T) {
	reqID := syntheticRequestID(0xf1)
	payload := protocol.AppendStringField(nil, "RT", "YA_PlayerStateInHangar")
	frame := protocol.BuildResponseFrame(reqID, 0x0320, payload)
	frames, remaining := protocol.ParseAppFrames(frame[:1])
	if len(frames) != 0 {
		t.Fatalf("split first byte parsed %d frames, want 0", len(frames))
	}
	if !bytes.Equal(remaining, frame[:1]) {
		t.Fatalf("remaining split magic = %x, want %x", remaining, frame[:1])
	}
	frames, remaining = protocol.ParseAppFrames(append(remaining, frame[1:]...))
	if len(remaining) != 0 {
		t.Fatalf("full frame left %d remaining bytes", len(remaining))
	}
	if len(frames) != 1 {
		t.Fatalf("full frame parsed %d frames, want 1", len(frames))
	}
	if frames[0].RequestID != reqID {
		t.Fatal("parsed frame lost request ID")
	}
}

func TestSafeNoopClientCallsReturnSuccess(t *testing.T) {
	for _, name := range []string{
		"YA_PlayerStateInHangar",
		"YA_UserLogout",
		"YA_UnlockItem",
		"YA_ClaimItem",
		"YA_AddItems",
		"YA_RemoveItems",
		"YA_ContractReplace",
		"YA_ContractRemove",
		"YA_AnalyticsEndTransaction",
		"YA_AnalyticsUpdateTransaction",
		"YA_ReconnectJoinChannels",
	} {
		payload := buildMmogRequestResponsePayload(name, defaultMmogPlayerPID, nil)
		if !bytes.Contains(payload, protocol.AppendStringField(nil, "RT", name)) {
			t.Fatalf("%s response missing RT", name)
		}
		if !bytes.Contains(payload, protocol.AppendStringField(nil, fieldStatus, "ok")) {
			t.Fatalf("%s response missing status ok", name)
		}
	}
}

func TestFirmamentAuthSuccessDoesNotWaitForPlayerDataReady(t *testing.T) {
	const userID = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"

	setGatewayPlayerDataReadyState(userID, false)
	t.Cleanup(func() {
		setGatewayPlayerDataReadyState(userID, false)
	})

	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleFirmamentConn(logrus.New(), serverConn, []byte("test-secret"))
		close(done)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handleFirmamentConn did not exit after test cleanup")
		}
	})

	reader := bufio.NewReader(clientConn)
	greeting := readFirmamentTestMessage(t, clientConn, reader, time.Second)
	if got := greeting["type"]; got != "server.notice" {
		t.Fatalf("greeting type = %v, want server.notice", got)
	}
	greetingData, ok := greeting["data"].(map[string]any)
	if !ok {
		t.Fatalf("greeting data has unexpected type %T", greeting["data"])
	}
	greetingNotice, ok := greetingData["notice"].(map[string]any)
	if !ok {
		t.Fatalf("greeting notice has unexpected type %T", greetingData["notice"])
	}
	if got := greetingNotice[fieldStatus]; got != "connection_successful" {
		t.Fatalf("greeting status = %v, want connection_successful", got)
	}

	token := mustSignTestJWT(t, jwt.MapClaims{"user_id": userID, "iss": protocol.GatewayJWTIssuer, "aud": "dreadnought", "realm": "dreadnought.pc-us", "exp": time.Now().Add(time.Hour).Unix()})
	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "auth-1",
		"method": "auth.refresh.redeem",
		"params": map[string]any{
			"token":          token,
			"client_name":    "Dreadnought Game Client",
			"client_version": "test",
		},
	})

	if gatewayPlayerDataReadyForUser(userID) {
		t.Fatal("player data should still be gated before YA_PlayerGet")
	}

	authSuccess := readFirmamentTestMessage(t, clientConn, reader, 200*time.Millisecond)
	if got := authSuccess["type"]; got != "server.notice" {
		t.Fatalf("auth success type = %v, want server.notice", got)
	}
	authData, ok := authSuccess["data"].(map[string]any)
	if !ok {
		t.Fatalf("auth success data has unexpected type %T", authSuccess["data"])
	}
	authNotice, ok := authData["notice"].(map[string]any)
	if !ok {
		t.Fatalf("auth success notice has unexpected type %T", authData["notice"])
	}
	if got := authNotice[fieldStatus]; got != "success" {
		t.Fatalf("auth success status = %v, want success", got)
	}
	if got := authNotice["action"]; got != "auth.refresh.redeem" {
		t.Fatalf("auth success action = %v, want auth.refresh.redeem", got)
	}
	if gatewayPlayerDataReadyForUser(userID) {
		t.Fatal("firmament auth success should not mark player data ready")
	}

	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "ping-1",
		"method": "ping",
		"params": map[string]any{"timeecho": 123},
	})
	pong := readFirmamentTestMessage(t, clientConn, reader, time.Second)
	if got := pong["type"]; got != "pong" {
		t.Fatalf("pong type = %v, want pong", got)
	}
	pongData, ok := pong["data"].(map[string]any)
	if !ok {
		t.Fatalf("pong data has unexpected type %T", pong["data"])
	}
	if got := pongData[fieldStatus]; got != "success" {
		t.Fatalf("pong status = %v, want success", got)
	}
	if got := pongData["timeecho"]; got != float64(123) {
		t.Fatalf("pong timeecho = %v, want 123", got)
	}

	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "friends-1",
		"method": "presence.friends.listing",
	})
	friends := readFirmamentTestMessage(t, clientConn, reader, time.Second)
	friendsResult, ok := friends["result"].(map[string]any)
	if !ok {
		t.Fatalf("friends result has unexpected type %T", friends["result"])
	}
	if got := friendsResult[fieldStatus]; got != "success" {
		t.Fatalf("friends status = %v, want success", got)
	}
	if _, ok := friendsResult["friends"].([]any); !ok {
		t.Fatalf("friends listing missing friends array: %T", friendsResult["friends"])
	}

	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "presence-data-1",
		"method": "presence.data.list",
	})
	presenceData := readFirmamentTestMessage(t, clientConn, reader, time.Second)
	presenceDataResult, ok := presenceData["result"].(map[string]any)
	if !ok {
		t.Fatalf("presence.data.list result has unexpected type %T", presenceData["result"])
	}
	if got := presenceDataResult[fieldStatus]; got != "success" {
		t.Fatalf("presence.data.list status = %v, want success", got)
	}
	if got := presenceDataResult["method"]; got != "presence.data.list" {
		t.Fatalf("presence.data.list method = %v, want presence.data.list", got)
	}
	if _, ok := presenceDataResult["result"].([]any); !ok {
		t.Fatalf("presence.data.list missing nested result array: %T", presenceDataResult["result"])
	}

	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "generic-1",
		"method": "party.lookup",
	})
	generic := readFirmamentTestMessage(t, clientConn, reader, time.Second)
	if got := generic["jsonrpc"]; got != "2.0" {
		t.Fatalf("generic jsonrpc = %v, want 2.0", got)
	}
	result, ok := generic["result"].(map[string]any)
	if !ok {
		t.Fatalf("generic result has unexpected type %T", generic["result"])
	}
	if got := result[fieldStatus]; got != "success" {
		t.Fatalf("generic status = %v, want success", got)
	}
}

func TestFirmamentRejectsInvalidAudience(t *testing.T) {
	const userID = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleFirmamentConn(logrus.New(), serverConn, []byte("test-secret"))
		close(done)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handleFirmamentConn did not exit after invalid audience test cleanup")
		}
	})

	reader := bufio.NewReader(clientConn)
	_ = readFirmamentTestMessage(t, clientConn, reader, time.Second)
	token := mustSignTestJWT(t, jwt.MapClaims{"user_id": userID, "iss": protocol.GatewayJWTIssuer, "aud": "wrong", "realm": "dreadnought.pc-us", "exp": time.Now().Add(time.Hour).Unix()})
	writeFirmamentTestMessage(t, clientConn, map[string]any{
		"id":     "auth-1",
		"method": "auth.refresh.redeem",
		"params": map[string]any{"token": token},
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("firmament did not close after invalid audience")
	}
}

func TestGatewayParsesSessionHeaderWithUsernameSuffix(t *testing.T) {
	sessionsMu.Lock()
	sessions = map[string]gatewaySession{"session-1": {UserID: "user-1", Username: "captain", createdAt: time.Now()}}
	sessionsMu.Unlock()
	t.Cleanup(func() {
		sessionsMu.Lock()
		sessions = make(map[string]gatewaySession)
		sessionsMu.Unlock()
	})

	called := false
	handler := makeGatewayHandler(logrus.New(), []byte("test-secret"), func(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
		called = true
		if got := claims["user_id"]; got != "user-1" {
			t.Fatalf("user_id = %v, want user-1", got)
		}
		gwJSON(w, map[string]any{"ok": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/session/touch", nil)
	req.Header.Set("Authorization", "Session session-1, captain")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("gateway handler was not called")
	}
}

// TestCompleteContractRejectsImmediateCompletion is a regression test for
// the exploit where a contract could be completed (and its reward paid out)
// instantly after being assigned, with no gameplay and no progress
// validation, since seedDailyContractsForPlayer re-seeds a fresh contract
// on every completion — allowing unlimited free XP/credit farming via a
// tight complete-and-reseed loop. completeContract now requires a contract
// to have existed for at least minContractCompletionAge before it can be
// completed.
func TestCompleteContractRejectsImmediateCompletion(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}

	// Auto-seeding is disabled (it crashed the client's quest cycle), so
	// insert a contract row directly to exercise the still-supported
	// completeContract machinery.
	if len(dailyContractSeeds) == 0 {
		t.Fatal("dailyContractSeeds is empty, cannot exercise this test")
	}
	contractID := dailyContractSeeds[0].id
	payload, _ := json.Marshal(map[string]interface{}{"rewardXP": dailyContractSeeds[0].rewardXP, "rewardGP": dailyContractSeeds[0].rewardGP})
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_contracts(user_id,contract_id,state,progress,payload) VALUES(?,?,'active',0,?)`, playerPID, contractID, string(payload)); err != nil {
		t.Fatalf("insert contract: %v", err)
	}

	if _, _, success := completeContract(database, playerPID, contractID); success {
		t.Fatal("completeContract succeeded immediately after seeding — age gate did not apply")
	}

	// Backdate the contract past the minimum completion age and retry.
	if _, err := database.Exec(
		`UPDATE player_contracts SET created_at=datetime('now', ?) WHERE user_id=? AND contract_id=?`,
		fmt.Sprintf("-%d seconds", minContractCompletionAge+1), playerPID, contractID,
	); err != nil {
		t.Fatalf("backdate contract: %v", err)
	}

	rewardXP, rewardGP, success := completeContract(database, playerPID, contractID)
	if !success {
		t.Fatal("completeContract failed after contract aged past the minimum — age gate too strict or broken")
	}
	if rewardXP <= 0 && rewardGP <= 0 {
		t.Fatalf("completeContract reported no reward: xp=%d gp=%d", rewardXP, rewardGP)
	}

	// A second completion attempt for the same (now-completed) contract
	// must not succeed again (state is no longer 'active').
	if _, _, success := completeContract(database, playerPID, contractID); success {
		t.Fatal("completeContract succeeded a second time for an already-completed contract")
	}
}

func TestFirstMmogInt32FieldAcceptsNumericStringFallback(t *testing.T) {
	int32Payload := protocol.AppendInt32Field(nil, "LoadoutID", 42)
	if got := firstMmogInt32Field(int32Payload, "LoadoutID"); got != 42 {
		t.Fatalf("firstMmogInt32Field with int32-tagged field = %d, want 42", got)
	}

	stringPayload := protocol.AppendStringField(nil, "LoadoutID", "42")
	if got := firstMmogInt32Field(stringPayload, "LoadoutID"); got != 42 {
		t.Fatalf("firstMmogInt32Field with string-tagged numeric field = %d, want 42", got)
	}

	// int32-tagged match must win over a later numeric-string candidate name.
	mixedPayload := protocol.AppendInt32Field(nil, "loadoutID", 7)
	mixedPayload = protocol.AppendStringField(mixedPayload, "LoadoutID", "99")
	if got := firstMmogInt32Field(mixedPayload, "loadoutID", "LoadoutID"); got != 7 {
		t.Fatalf("firstMmogInt32Field with mixed tags = %d, want 7 (int32 match preferred)", got)
	}

	if got := firstMmogInt32Field(nil, "LoadoutID"); got != 0 {
		t.Fatalf("firstMmogInt32Field with no match = %d, want 0", got)
	}
}

func TestElitePurchasePersistsMembershipExpiry(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "efefefefefefefefefefefefefefefe"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=10000 WHERE user_id=?`, normalizedPlayerStatePID(playerPID)); err != nil {
		t.Fatalf("seed premium currency: %v", err)
	}

	// YA_PlayerGet's Membership.ExpireTime goes through the client's
	// int32-blind parser (same as tll/tpl/tc/etc) — sent as a numeric string.
	//
	// Before any purchase, the Membership object must be entirely absent, not
	// present with ExpireTime="0": the client's parser (FUN_142a85120) has a
	// dedicated branch for an absent Membership object that skips its int64
	// FILETIME conversion, but a present object with ExpireTime="0" drives it
	// through the value-present branch instead, computing a 1970-01-01 epoch
	// timestamp. That is the exact value observed immediately preceding an
	// EXCEPTION_STACK_OVERFLOW crash during hangar entry (see UE4Minidump.dmp /
	// DreadGame.log, 2026-07-22 crash session) — the never-purchased case must
	// use the client's own "no membership" path, not a fabricated timestamp.
	before := buildMmogPlayerGetPayload(playerPID)
	if bytes.Contains(before, []byte("Membership")) {
		t.Fatalf("YA_PlayerGet before any purchase must omit Membership entirely, got payload=%x", before)
	}

	purchaseRequest := protocol.AppendInt32Field(nil, "Duration", 30)
	purchase := buildMmogElitePurchasePayload("YA_BuyEliteStatus", playerPID, purchaseRequest)
	purchaseResult := extractNamedMmogObject(t, purchase, "result")
	if status := protocol.ExtractStringField(purchaseResult, fieldStatus); status != "ok" {
		t.Fatalf("elite purchase status = %q, want ok (payload=%x)", status, purchase)
	}
	purchaseExpiry, ok := protocol.ExtractInt32Field(purchaseResult, "ExpireTime")
	if !ok || purchaseExpiry <= 0 {
		t.Fatalf("elite purchase response ExpireTime = %d (ok=%v), want positive", purchaseExpiry, ok)
	}

	after := buildMmogPlayerGetPayload(playerPID)
	afterMembership := extractNamedMmogObject(t, after, "Membership")
	wantExpire := strconv.Itoa(int(purchaseExpiry))
	if expire := protocol.ExtractStringField(afterMembership, "ExpireTime"); expire != wantExpire {
		t.Fatalf("YA_PlayerGet ExpireTime after purchase = %q, want %q (persisted mismatch)", expire, wantExpire)
	}
}

func TestPurchasedItemTypeMatchesRealItemCategory(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "fefefefefefefefefefefefefefefef"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=20000 WHERE user_id=?`, normalizedPlayerStatePID(playerPID)); err != nil {
		t.Fatalf("seed currency: %v", err)
	}

	const weaponItemID int32 = 100597772 // Repeater Turrets, confirmed YWeapon in ItemIDTable.json
	request := protocol.AppendInt32Field(nil, "ItemID", weaponItemID)
	purchase := buildMmogPurchasePayload("YA_PurchaseItem", playerPID, request)
	if !bytes.Contains(purchase, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatalf("weapon purchase did not succeed: %x", purchase)
	}

	var itemType string
	if err := database.QueryRow(`SELECT item_type FROM player_purchases WHERE user_id=? AND item_id=?`, normalizedPlayerStatePID(playerPID), weaponItemID).Scan(&itemType); err != nil {
		t.Fatalf("query persisted item_type: %v", err)
	}
	if itemType != "weapon" {
		t.Fatalf("persisted item_type = %q, want %q (purchasedItemType must not default every purchase to ship)", itemType, "weapon")
	}
}

func TestChatPayloadDeliversPersistedMessages(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "abababababababababababababababab"
	if err := seedMmogPlayerState(database, playerPID); err != nil {
		t.Fatalf("seed player state: %v", err)
	}

	sendRequest := protocol.AppendStringField(nil, "channelName", "global")
	sendRequest = protocol.AppendStringField(sendRequest, "message", "hello world")
	send := buildMmogChatPayload("YA_Chat", playerPID, sendRequest)
	if !bytes.Contains(send, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatalf("chat send did not report ok: %x", send)
	}

	readRequest := protocol.AppendStringField(nil, "channelName", "global")
	read := buildMmogChatPayload("YA_Chat", "other-player-id", readRequest)
	result := extractNamedMmogObject(t, read, "result")
	messages := extractNamedMmogArray(t, result, "messages")
	if !bytes.Contains(messages, protocol.AppendStringField(nil, "sender", normalizedPlayerStatePID(playerPID))) {
		t.Fatalf("messages array missing sender for persisted chat message: %x", messages)
	}
	if !bytes.Contains(messages, protocol.AppendStringField(nil, "text", "hello world")) {
		t.Fatalf("messages array missing text for persisted chat message: %x", messages)
	}
}

// TestClientSaveBlobsRoundTripThroughPlayerGet covers the onboarding
// persistence path. The client uploads its own onboarding state with
// YA_SaveGame and restores it from the SGD field of YA_PlayerGet; if that round
// trip is lossy, UYOnboardingManager::LoadStates restores nothing, the
// Ob_TutorialFinished rule keeps a zero timestamp, and
// UUI_LoginGateScreen::EnterGame refuses to leave the loading screen forever.
func TestClientSaveBlobsRoundTripThroughPlayerGet(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "13131313131313131313131313131313"

	// Shaped like a real blob: int32 uncompressed size then zlib data. The
	// server must not care what is inside, so exercise bytes that are not
	// valid UTF-8 and contain NULs.
	onboarding := []byte{0x15, 0x00, 0x00, 0x00, 0x78, 0x9c, 0x00, 0xff, 0xfe, 0x01}
	cta := []byte{0x04, 0x00, 0x00, 0x00, 0x78, 0x9c, 0x80, 0x00}

	_ = buildMmogPlayerGetPayload(playerPID)

	for _, tc := range []struct {
		request string
		slot    string
		blob    []byte
	}{
		{"YA_SaveGame", "SGD", onboarding},
		{"YA_SaveCtAData", "SCtA", cta},
	} {
		mutation := protocol.AppendBytesField(nil, "data", tc.blob)
		if err := persistMmogPlayerMutation(playerPID, tc.request, mutation); err != nil {
			t.Fatalf("persist %s: %v", tc.request, err)
		}
		if got := loadPlayerSaveBlob(playerPID, tc.slot); !bytes.Equal(got, tc.blob) {
			t.Fatalf("%s blob round trip = % x, want % x", tc.slot, got, tc.blob)
		}
	}

	// A second save must replace the first, not append or be ignored: this is
	// how onboarding progress advances.
	updated := []byte{0x20, 0x00, 0x00, 0x00, 0x78, 0x9c, 0x11, 0x22}
	if err := persistMmogPlayerMutation(playerPID, "YA_SaveGame", protocol.AppendBytesField(nil, "data", updated)); err != nil {
		t.Fatalf("persist second YA_SaveGame: %v", err)
	}
	if got := loadPlayerSaveBlob(playerPID, "SGD"); !bytes.Equal(got, updated) {
		t.Fatalf("updated SGD blob = % x, want % x", got, updated)
	}

	// And it has to come back out on the wire as a byte array, since the
	// client's accessor only reads a value node's binary slot.
	payload := buildMmogPlayerGetPayload(playerPID)
	if got, ok := protocol.ExtractBytesField(payload, "SGD"); !ok || !bytes.Equal(got, updated) {
		t.Fatalf("YA_PlayerGet SGD = % x (found=%v), want % x", got, ok, updated)
	}
	if got, ok := protocol.ExtractBytesField(payload, "SCtA"); !ok || !bytes.Equal(got, cta) {
		t.Fatalf("YA_PlayerGet SCtA = % x (found=%v), want % x", got, ok, cta)
	}

	// A brand-new player has no blob and must still get a well-formed empty
	// byte array, so the client runs onboarding instead of reading garbage.
	fresh := buildMmogPlayerGetPayload("14141414141414141414141414141414")
	if got, ok := protocol.ExtractBytesField(fresh, "SGD"); !ok || len(got) != 0 {
		t.Fatalf("new player SGD = % x (found=%v), want empty byte array", got, ok)
	}
}

// TestContainerLengthMatchesClientEncoding pins the container framing against
// bytes the client itself produced, decoded from a YA_AnalyticsEvent request
// captured on the wire.
//
// A container declares its length as contents + 6-byte terminator, measured
// from just AFTER the length field, and its terminator carries the absolute
// offset of that length field as a back-reference. We used to include the
// length field in the count, making every container 4 bytes too long. That is
// invisible for a lone trailing container but catastrophic for siblings: an
// over-long container swallows the start of whatever follows, so only the LAST
// element of any array survived and every earlier one was silently dropped.
func TestContainerLengthMatchesClientEncoding(t *testing.T) {
	// Real client frame payload (truncated after the "payload" object header).
	clientPrefix, err := hex.DecodeString(
		"025254091100000059415f416e616c79746963734576656e740863617465676f" +
			"7279091a000000636c69656e745f737461746973746963735f68617264776172" +
			"6507636f6e74657874091a000000636c69656e745f7374617469737469637358" +
			"5f68617264776172650000")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = clientPrefix

	// The captured object's length field sat at offset 113, its terminator
	// back-reference was 0x71 (=113), it declared 600, and the whole frame
	// payload was 723 bytes: 113 + 4 + 600 + 6 == 723.
	const lengthFieldOffset, declared, frameSize = 113, 600, 723
	if lengthFieldOffset+4+declared+6 != frameSize {
		t.Fatalf("client convention mismatch: %d+4+%d+6 != %d", lengthFieldOffset, declared, frameSize)
	}

	// Our encoder must follow the same rule.
	var stack []int
	b, stack := protocol.AppendObjectStart(nil, stack, "obj")
	b = protocol.AppendStringField(b, "k", "v")
	b, _ = protocol.AppendObjectEnd(b, stack)

	start := 1 + len("obj") + 1 // namelen + name + type byte
	size := int(binary.LittleEndian.Uint32(b[start : start+4]))
	backRef := int(binary.LittleEndian.Uint32(b[len(b)-4:]))
	if backRef != start {
		t.Fatalf("terminator back-reference = %d, want the length field offset %d", backRef, start)
	}
	if start+4+size != len(b) {
		t.Fatalf("container size %d does not close at end of buffer: %d+4+%d != %d", size, start, size, len(b))
	}

	// Two sibling objects in an array: the first must not overlap the second.
	stack = nil
	arr, stack := protocol.AppendArrayStart(nil, stack, "a")
	arr, stack = protocol.AppendUnnamedObjectStart(arr, stack)
	arr = protocol.AppendStringField(arr, "first", "1")
	arr, stack = protocol.AppendObjectEnd(arr, stack)
	firstEnd := len(arr)
	arr, stack = protocol.AppendUnnamedObjectStart(arr, stack)
	arr = protocol.AppendStringField(arr, "second", "2")
	arr, stack = protocol.AppendObjectEnd(arr, stack)
	arr, _ = protocol.AppendObjectEnd(arr, stack)

	firstStart := 1 + len("a") + 1 + 4 + 2 // array header, then unnamed object header
	firstSize := int(binary.LittleEndian.Uint32(arr[firstStart : firstStart+4]))
	if firstStart+4+firstSize != firstEnd {
		t.Fatalf("first array element claims bytes past its own terminator: %d+4+%d != %d",
			firstStart, firstSize, firstEnd)
	}
}

// TestOnboardingFlowRequestsAreAcknowledged guards the requests a brand-new
// player sends while going through the tutorial. They only appear once
// onboarding can actually be reached, so they were invisible for a long time
// and fell through to the dispatcher's "unknown command" error.
//
// That was not harmless. YA_SavePlayerDisplayInformation is the captain
// registration sent immediately after the tutorial: answering it with an error
// left the client sitting on a loading screen forever. YA_SaveGame carries the
// onboarding save blob, so an error reply makes the client treat its own
// progress as unsaved.
func TestOnboardingFlowRequestsAreAcknowledged(t *testing.T) {
	for _, requestName := range []string{
		"YA_SavePlayerDisplayInformation",
		"YA_SaveGame",
		"YA_SaveCtAData",
		"YA_AnalyticsTutorialEvent",
		"YA_AnalyticsTutorialSummaryEvent",
		"YA_AnalyticsOnboardingMovie",
		"YA_PlayerStateInHangar",
	} {
		t.Run(requestName, func(t *testing.T) {
			request := protocol.AppendRootEnd(protocol.AppendStringField(nil, "RT", requestName))
			response := buildMmogRequestResponsePayload(requestName, defaultMmogPlayerPID, request)
			if len(response) == 0 {
				t.Fatalf("%s produced no response at all", requestName)
			}
			if bytes.Contains(response, protocol.AppendStringField(nil, fieldStatus, "error")) {
				t.Fatalf("%s was answered with an error payload; the client waits on this reply", requestName)
			}
			if !bytes.Contains(response, protocol.AppendStringField(nil, "RT", requestName)) {
				t.Fatalf("%s response missing its RT acknowledgement", requestName)
			}
		})
	}
}

// TestLoadoutDisplayInfoIsWellFormedShipVanity guards the ship-customisation
// string. UYShipCustomisationComponent::ImportFromDisplayInfo rejects anything
// that does not split into exactly five ';' groups ("Invalid import string"),
// and UYShipLoadout::ImportLoadoutParameterAsync rejects a string shorter than
// two characters ("No item IDs retrieved from display info!"). Both have been
// hit live, from opposite directions.
func TestLoadoutDisplayInfoIsWellFormedShipVanity(t *testing.T) {
	loadouts := starterShipLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("no starter loadouts to check")
	}
	for _, loadout := range loadouts {
		info := loadout.displayInfo()
		if len(info) < 2 {
			t.Fatalf("%s display info %q is too short; the loadout importer needs >1 char", loadout.entryID(), info)
		}
		groups := strings.Split(info, ";")
		if len(groups) != 5 {
			t.Fatalf("%s display info has %d groups, want exactly 5: %q", loadout.entryID(), len(groups), info)
		}
		// A first group of 2+ chars must itself split into exactly four mesh
		// ids, or the importer logs "Invalid mesh import string".
		if len(groups[0]) > 1 {
			if meshes := strings.Split(groups[0], "#"); len(meshes) != 4 {
				t.Fatalf("%s mesh group has %d parts, want 4: %q", loadout.entryID(), len(meshes), groups[0])
			}
		}
	}

	// It has to reach the wire too, not just the accessor.
	payload := buildMmogPlayerFleetsPayload(defaultMmogPlayerPID)
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "m_displayInfo", loadouts[0].displayInfo())) {
		t.Fatal("YA_PlayerFleets does not carry the loadout display info string")
	}
}

// TestSavePlayerDisplayInformationEchoesPIDAndAppearance covers the captain
// save round trip.
//
// The client checks the response's PID as a GUID against the player it knows
// and, on a mismatch (including an absent field, which parses to an all-zero
// GUID), broadcasts mmogbrain error 0x10 -- logged as "UYCaptain::
// HandleMmogbrainError | General MMogbrain captain display information error".
// Only on a match does it read "disp" and broadcast the update to the UI.
func TestSavePlayerDisplayInformationEchoesPIDAndAppearance(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "15151515151515151515151515151515"
	const appearance = "GENDER_FEMALE;#iiS=872349703#iiH=872349769#bIam=0"

	_ = buildMmogPlayerGetPayload(playerPID)

	// The client sends the appearance in "disp", not "DisplayInfo".
	mutation := protocol.AppendStringField(nil, "disp", appearance)
	if err := persistMmogPlayerMutation(playerPID, "YA_SavePlayerDisplayInformation", mutation); err != nil {
		t.Fatalf("persist: %v", err)
	}

	response := buildMmogRequestResponsePayload("YA_SavePlayerDisplayInformation", playerPID, mutation)
	if got := protocol.ExtractStringField(response, "PID"); got != playerPID {
		t.Fatalf("response PID = %q, want %q — a mismatch raises mmogbrain error 0x10", got, playerPID)
	}
	if got := protocol.ExtractStringField(response, "disp"); got != appearance {
		t.Fatalf("response disp = %q, want the saved appearance %q", got, appearance)
	}

	// And it must actually have been stored, not just echoed back.
	if got := buildMmogPlayerGetPayload(playerPID); protocol.ExtractStringField(got, "disp") != appearance {
		t.Fatal("YA_PlayerGet does not carry the saved captain appearance")
	}
}

// TestTechTreeCarriesZlibBlob pins the envelope the client actually reads.
//
// The YA_GetTechTree handler (response slot 0x36b0) fetches exactly one field,
// "TechTrees", through the byte-array accessor, inflates it with inflateInit_
// -- a standard zlib stream, no length prefix -- and hands the result to the
// ordinary mmog document parser. Everything else in the response is ignored
// without an error, which is how a fully populated techTreeRow array produced
// no parse logging at all and left the tech tree manager empty.
func TestTechTreeCarriesZlibBlob(t *testing.T) {
	payload := buildMmogTechTreePayload(defaultMmogPlayerPID)

	blob, ok := protocol.ExtractBytesField(payload, "TechTrees")
	if !ok {
		t.Fatal("YA_GetTechTree has no TechTrees byte field; the client reads nothing else")
	}
	if len(blob) == 0 {
		t.Fatal("TechTrees blob is empty")
	}
	// A zlib stream, not raw deflate: the client uses inflateInit_.
	if blob[0]&0x0f != 8 {
		t.Fatalf("TechTrees blob is not a zlib stream (CMF=0x%02x)", blob[0])
	}

	reader, err := zlib.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("TechTrees blob does not inflate: %v", err)
	}
	defer func() { _ = reader.Close() }()
	document, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read inflated TechTrees: %v", err)
	}

	// The inflated bytes must be a well-formed mmog document.
	validateMmogPayloadNesting(t, document)

	// The root is an ARRAY of ARRAYS of item objects, matching how
	// UYTechTreeManager's loader walks it: AsArray(root) -> AsArray(element)
	// -> item. It is not a named object -- a "techTreeRow" array at the root
	// made the loader's very first AsArray produce nothing, leaving the
	// manager empty and every tech-tree-derived screen (including fleet
	// management) with no data.
	if bytes.Contains(document, appendFieldMarker("techTreeRow", 0x0d)) {
		t.Fatal("document still carries the invented techTreeRow array")
	}
	if document[0] != 0x00 || document[1] != 0x0d {
		t.Fatalf("document must open with an unnamed array, got % x", document[:2])
	}
	// Fields the loader resolves by name. Id is the key
	// TechTreeManager::FindItemForShipId matches on.
	// Visible gates the entire item. Its string branch tests (length - 1) > 0,
	// so it must be at least TWO characters -- "1" reads as false and the item
	// is discarded, taking its manufacturer group with it.
	if !bytes.Contains(document, appendFieldMarker("Visible", 0x09)) {
		t.Error("Visible must be a string field (the branch whose truthiness rule is known)")
	}
	if bytes.Contains(document, protocol.AppendStringField(nil, "Visible", "1")) {
		t.Error(`Visible="1" is a single character, which the loader reads as false`)
	}
	for _, field := range []string{
		"Id", "ClassId", "Manufacturer", "Tier", "Position",
		"XPCost", "FPCost", "NumTechTreeItemsRequired", "ProxyType",
	} {
		if !bytes.Contains(document, appendFieldMarker(field, 0x09)) {
			t.Errorf("document has no %q field, which the loader reads", field)
		}
		// int32 reads as 0 through this loader's union, same as elsewhere.
		if bytes.Contains(document, appendFieldMarker(field, 0x56)) {
			t.Errorf("%q is an int32 field; the loader reads that as 0", field)
		}
	}
	// ProxyType must be -1: it selects which sub-array of the manufacturer group
	// the item is filed under, and FindItemForShipId reads only the -1 one. Any
	// other value leaves every ship unfindable by ship id even though the
	// manufacturer groups themselves resolve.
	if !bytes.Contains(document, protocol.AppendStringField(nil, "ProxyType", "-1")) {
		t.Error("ProxyType must be -1 or ships land in the sub-array FindItemForShipId never reads")
	}

	// Prereq and Wires are containers, not scalars -- and specifically OBJECT
	// containers (0x0c), not arrays (0x0d). An array's children are stored
	// positionally with their names discarded, and the client's field lookup
	// treats a name-less container as indexable: it resolves any field name to
	// _wtoi(name) == 0 and returns child[0]. The loader read ProxyType off the
	// Prereq container that way and got the prereq id, failing its [-1, 9]
	// range check once per item that carries a prereq.
	for _, field := range []string{"Prereq", "Wires"} {
		if !bytes.Contains(document, appendFieldMarker(field, 0x0c)) {
			t.Errorf("%q must be an object field; an array loses its child names", field)
		}
		if bytes.Contains(document, appendFieldMarker(field, 0x0d)) {
			t.Errorf("%q is still an array; that is what made the loader index it by position", field)
		}
	}
}

// TestRewardCurrenciesCarriesBalanceAsStrings covers the only channel the client
// has for credit and GP balances.
//
// Its HUD reads FPlayerCurrencyAmountsData{m_freeXP, m_softCurrency,
// m_hardCurrency}. m_freeXP arrives via YA_PlayerGet's "FreeXp", but that
// parser has no currency field of any spelling among its 47 lookups, so soft
// and hard currency can only come from YA_RewardCurrencies' root-level
// "Credits" and "Points". Both are read through the accessor family that
// silently reads an int32 wire field as 0, so they must be numeric strings.
func TestRewardCurrenciesCarriesBalanceAsStrings(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "17171717171717171717171717171717"

	_ = buildMmogPlayerGetPayload(playerPID)
	database := currentMmogPlayerStateDB()
	if _, err := database.Exec(
		`UPDATE player_state SET soft_currency=?, premium_currency=? WHERE user_id=?`,
		4242, 77, normalizedPlayerStatePID(playerPID),
	); err != nil {
		t.Fatalf("seed balances: %v", err)
	}

	payload := buildMmogRewardCurrenciesPayload(playerPID)
	if got := protocol.ExtractStringField(payload, "RT"); got != "YA_RewardCurrencies" {
		t.Fatalf("RT = %q, want YA_RewardCurrencies", got)
	}
	// "result" is compared as a plain string against "ok"; the usual
	// result{status:"ok"} object reads back as an empty string there and makes
	// the client skip both currency assignments.
	if got := protocol.ExtractStringField(payload, "result"); got != "ok" {
		t.Fatalf("result = %q, want the bare string \"ok\" (not a status object)", got)
	}
	if bytes.Contains(payload, appendFieldMarker("result", 0x0c)) {
		t.Fatal("result must be a string field, not an object")
	}
	if got := protocol.ExtractStringField(payload, "Credits"); got != "4242" {
		t.Fatalf("Credits = %q, want the persisted 4242", got)
	}
	if got := protocol.ExtractStringField(payload, "Points"); got != "77" {
		t.Fatalf("Points = %q, want the persisted 77", got)
	}
	// int32 here reads back as 0 on the client.
	for _, field := range []string{"Credits", "Points"} {
		if bytes.Contains(payload, appendFieldMarker(field, 0x56)) {
			t.Fatalf("%s must be a numeric string, not an int32 field", field)
		}
	}
	validateMmogPayloadNesting(t, protocol.AppendRootEnd(payload))
}

// TestTechTreeRowsAreKeyedOnAdmissibleIDs pins the id space of the tech tree's
// ship rows.
//
// The gate deciding whether a row enters the array TechTreeManager::
// FindItemForShipId searches compares the top byte of the id -- a category tag,
// (Id >> 24) & 0xff -- against the resolved YShipLoadoutHero and
// YShipLoadoutPrecast classes, which ItemIDTable numbers 3 and 1. YPawn is
// category 10, so a pawn id is silently discarded by the loader and the row
// might as well not have been sent. Every row therefore has to be keyed on a
// precast or hero loadout id.
func TestTechTreeRowsAreKeyedOnAdmissibleIDs(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	const (
		categoryPrecastLoadout = 1
		categoryHeroLoadout    = 3
	)
	seen := map[int32]bool{}
	for _, ship := range techTreeShips() {
		rowID := techTreeRowID(ship)
		switch category := (rowID >> 24) & 0xff; category {
		case categoryPrecastLoadout, categoryHeroLoadout:
		default:
			t.Errorf("ship %d keys its row on %d, whose category byte is %d; only 1 (precast) and 3 (hero) are admitted",
				ship.id, rowID, category)
		}
		seen[rowID] = true
	}
	if len(seen) == 0 {
		t.Fatal("no tech tree rows")
	}

	// Each id must appear exactly once in the emitted document. Mapping every
	// T1 ship onto the loadout id its fleet row already used means the raw ship
	// list contains duplicates by design; buildMmogTechTreeDocument drops them,
	// and it has to, or the loader would file the same item under one
	// manufacturer group twice.
	document := inflateTechTreeDocument(t, buildMmogTechTreePayload(defaultMmogPlayerPID))
	for rowID := range seen {
		field := protocol.AppendStringField(nil, "Id", strconv.Itoa(int(rowID)))
		if got := bytes.Count(document, field); got != 1 {
			t.Errorf("row id %d appears %d times in the document, want exactly 1", rowID, got)
		}
	}

	// The derivation must keep reproducing the four starter loadout ids that
	// were known independently, from the shared config. That is what
	// distinguishes it from the hand-written table it replaced.
	for shipID, wantLoadoutID := range map[int32]int32{
		184483982: 33489262, // Agosta
		184484170: 33489423, // Simargl
		184483950: 33489263, // Rurik
		184484202: 33489264, // Cerberus
	} {
		got, ok := techTreePrecastLoadoutID(shipID)
		if !ok {
			t.Errorf("no precast loadout derived for starter ship %d", shipID)
			continue
		}
		if got != wantLoadoutID {
			t.Errorf("starter ship %d derived precast loadout %d, want %d", shipID, got, wantLoadoutID)
		}
	}
}

// inflateTechTreeDocument returns the zlib-compressed document carried in
// YA_GetTechTree's TechTrees byte-array field.
func inflateTechTreeDocument(t *testing.T, payload []byte) []byte {
	t.Helper()

	blob, ok := protocol.ExtractBytesField(payload, "TechTrees")
	if !ok {
		t.Fatal("YA_GetTechTree carries no TechTrees blob")
	}
	reader, err := zlib.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("TechTrees blob does not inflate: %v", err)
	}
	defer func() { _ = reader.Close() }()
	document, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read inflated TechTrees: %v", err)
	}
	return document
}

// TestTechTreeClassIDsMatchTheAssetPaths validates every tech tree row's ClassId
// against the class encoded in its ship's registered asset path.
//
// A full audit of the extracted client tables found exactly one disagreement --
// Sniper Light T2 (184483954) carried classID 10, SNIPER_MEDIUM, where its path
// (/Game/Generic/Ships/Sniper/Light/T2/) says 3, SNIPER_LIGHT. Deriving the
// value fixes it; this test keeps the seeds and the asset data from diverging
// again.
func TestTechTreeClassIDsMatchTheAssetPaths(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	checked := 0
	for _, ship := range techTreeShips() {
		want, ok := derivedShipClassID(ship.id)
		if !ok {
			continue // fleet rows are loadout ids, not pawn paths
		}
		checked++
		if got := techTreeRowClassID(ship); got != want {
			t.Errorf("ship %d reports ClassId %d, but its asset path says %d", ship.id, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no ship rows had a derivable class; the derivation is not being exercised")
	}

	// The specific drift the audit found, pinned by id.
	if got, ok := derivedShipClassID(184483954); !ok || got != 3 {
		t.Errorf("Sniper Light T2 derives ClassId %d (ok=%v), want 3 (YSC_SNIPER_LIGHT)", got, ok)
	}
}

// TestShipAndHeroNamesMatchTheAuthoritativeTable guards every hardcoded ship and
// hero name against ItemIDConversionTable, which pairs each item id with the name
// the game actually displays.
//
// The seed tables were populated from ASSET FILENAMES, which are not display
// names, and the audit found several wrong: hero 67043329 was "Skagerrak" (its
// filename) where the game calls it Huscarl; "FallofTroy" and "JunkyardPrince"
// were run together; and the tier-2 roster was named Leipzig and Trieste, neither
// of which is a string that appears anywhere in the game -- the real ships are
// Trafalgar and Nav.
func TestShipAndHeroNamesMatchTheAuthoritativeTable(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	checked := 0
	named := map[int32]string{}
	for _, ship := range techTreeShips() {
		named[ship.id] = ship.name
	}
	for _, hero := range heroShipLoadouts {
		named[hero.loadoutID] = hero.name
	}
	for _, hull := range baseShipLoadouts {
		named[hull.loadoutID] = hull.name
	}
	for id, name := range named {
		want, ok := authoritativeShipName(id)
		if !ok {
			continue // no authoritative name for this id
		}
		checked++
		if !strings.EqualFold(strings.ReplaceAll(name, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("id %d is named %q by the server; the game calls it %q", id, name, want)
		}
	}
	if checked == 0 {
		t.Fatal("no names were checked; the authoritative table is not loading")
	}

	// The specific corrections the audit produced, pinned by id.
	for id, want := range map[int32]string{
		67043329:  "Huscarl",         // filename says Skagerrak
		67043330:  "Fall of Troy",    // filename runs it together
		67043338:  "Junkyard Prince", // filename runs it together
		184483981: "Trafalgar",       // was "Leipzig", not a game string
		184483972: "Nav",             // was "Trieste", not a game string
	} {
		got, ok := authoritativeShipName(id)
		if !ok || got != want {
			t.Errorf("authoritative name for %d = %q (ok=%v), want %q", id, got, ok, want)
		}
	}
}
