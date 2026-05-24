//nolint:goconst,gosec // MMOG regression tests intentionally repeat protocol identifiers and bounded protocol-size casts.
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	mmogdb "github.com/dreadnought-ps/mmogbrain/db"
	"github.com/dreadnought-ps/mmogbrain/protocol"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
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
		case 0x09:
			if i+4 > len(payload) {
				t.Fatalf("string length truncated at byte %d", i)
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if i+valueLen > len(payload) {
				t.Fatalf("string overruns payload at byte %d", i)
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
			size := int(binary.LittleEndian.Uint32(payload[start : start+4]))
			if size < 6 {
				t.Fatalf("container size = %d at offset %d, want at least 6", size, start)
			}
			if start+size != i-4 {
				t.Fatalf("container at offset %d closes at %d, want %d", start, start+size, i-4)
			}
			stack = stack[:len(stack)-1]
		default:
			t.Fatalf("unsupported MMOG field type 0x%02x at byte %d", fieldType, i-1)
		}
	}
}

func TestExtractMmogPlayerPIDFromLoginTicket(t *testing.T) {
	const userID = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": userID})
	ticket, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	payload := protocol.AppendStringField(nil, "RT", "YA_UserLogin")
	payload = protocol.AppendStringField(payload, "Ticket", ticket)
	payload = append(payload, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)

	got := protocol.ExtractPlayerPID(payload, defaultMmogPlayerPID)
	want := "b7c42c0f3ac648a182ccfd35eb24f128"
	if got != want {
		t.Fatalf("player PID = %q, want %q", got, want)
	}
}

func TestPlayerDataResponsesUseHexPlayerPID(t *testing.T) {
	const pid = "b7c42c0f3ac648a182ccfd35eb24f128"

	for name, payload := range map[string][]byte{
		"YA_PlayerGet":    buildMmogRequestResponsePayload("YA_PlayerGet", pid, buildMmogPlayerDataPayload("YA_PlayerGet", pid)),
		"YA_PlayerFleets": buildMmogRequestResponsePayload("YA_PlayerFleets", pid, buildMmogPlayerFleetsPayload(pid)),
	} {
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
	payload := buildMmogPlayerStatsCounterDataPayload()
	counterDataArrayField := appendFieldMarker("counterData", 0x0d)
	counterDataObjectField := appendFieldMarker("counterData", 0x0c)
	counterIDField := protocol.AppendInt32Field(nil, "counterId", 0)

	if !bytes.Contains(payload, counterDataArrayField) {
		t.Fatalf("stats counter response does not expose counterData as an array")
	}
	if bytes.Contains(payload, counterDataObjectField) {
		t.Fatalf("stats counter response still exposes counterData as an object")
	}
	if bytes.Count(payload, counterIDField) < 2 {
		t.Fatalf("stats counter response should include non-empty root and result counterData rows")
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
		{name: "ServerTime", fieldType: 0x56},
		{name: "ClientTime", fieldType: 0x56},
	} {
		if !bytes.Contains(payload, appendFieldMarker(field.name, field.fieldType)) {
			t.Fatalf("YA_PlayerGet missing top-level field %s", field.name)
		}
	}
}

func TestUserLoginPayloadKeepsEconomyFieldsOnResult(t *testing.T) {
	payload := buildMmogLoginSuccessPayload()
	result := extractNamedMmogObject(t, payload, "result")
	loginStreak := extractNamedMmogObject(t, result, "LoginStreak")

	if !bytes.Contains(result, protocol.AppendStringField(nil, fieldStatus, "ok")) {
		t.Fatal("YA_UserLogin result missing status=ok")
	}
	for _, field := range []struct {
		name  string
		value int32
	}{
		{name: "credits", value: 10000},
		{name: "premiumCurrency", value: 0},
		{name: "freexp", value: 0},
		{name: "xp", value: 0},
	} {
		fieldBytes := protocol.AppendInt32Field(nil, field.name, field.value)
		if !bytes.Contains(result, fieldBytes) {
			t.Fatalf("YA_UserLogin result missing %s", field.name)
		}
		if bytes.Contains(loginStreak, fieldBytes) {
			t.Fatalf("YA_UserLogin LoginStreak should not contain %s", field.name)
		}
	}
	if !bytes.Contains(loginStreak, protocol.AppendInt32Field(nil, "loginstreak", 0)) {
		t.Fatal("YA_UserLogin LoginStreak missing loginstreak")
	}
}

func TestTechTreeRowsExposeWeight(t *testing.T) {
	expectedWeights := map[string]int32{
		"Assault Medium T1":     1,
		"Dreadnought Medium T1": 1,
		"Sniper Medium T1":      1,
		"Support Medium T1":     1,
		"Athos":                 1,
		"Zmey":                  1,
		"Aion":                  1,
		"Valcour":               0,
		"Svarog":                1,
		"Leipzig":               1,
		"Trieste":               1,
		"Ceres":                 1,
	}

	for _, ship := range allT1Ships() {
		expectedWeight, ok := expectedWeights[ship.name]
		if !ok {
			t.Fatalf("missing expected weight for tech tree ship %q", ship.name)
		}

		row, _ := appendMmogTechTreeRow(nil, nil, ship)
		if !bytes.Contains(row, protocol.AppendStringField(nil, "m_name", ship.name)) {
			t.Fatalf("tech tree row for %q does not include m_name", ship.name)
		}
		if !bytes.Contains(row, protocol.AppendInt32Field(nil, "Weight", expectedWeight)) {
			t.Fatalf("tech tree row for %q does not include Weight=%d", ship.name, expectedWeight)
		}
		if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_weight", expectedWeight)) {
			t.Fatalf("tech tree row for %q does not include m_weight=%d", ship.name, expectedWeight)
		}
		if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_currentBaseClass", ship.shipClass)) {
			t.Fatalf("tech tree row for %q does not include m_currentBaseClass=%d", ship.name, ship.shipClass)
		}
		if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_currentShipClass", ship.shipClass)) {
			t.Fatalf("tech tree row for %q does not include m_currentShipClass=%d", ship.name, ship.shipClass)
		}

		if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_shipTier", 1)) {
			t.Fatalf("tech tree row for %q does not include m_shipTier=1", ship.name)
		}

		if loadout, ok := starterLoadoutByShipID(ship.id); ok {
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_precastLoadoutID", loadout.precastLoadoutID)) {
				t.Fatalf("tech tree row for %q does not include m_precastLoadoutID=%d", ship.name, loadout.precastLoadoutID)
			}
			if !bytes.Contains(row, appendFieldMarker("m_shipLoadoutInfo", 0x0c)) {
				t.Fatalf("tech tree row for %q does not include m_shipLoadoutInfo object", ship.name)
			}
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_loadoutTier", 1)) {
				t.Fatalf("tech tree row for %q does not include m_loadoutTier=1", ship.name)
			}
			if !bytes.Contains(row, protocol.AppendBoolField(nil, "m_loadoutComplete", loadout.complete())) {
				t.Fatalf("tech tree row for %q does not include m_loadoutComplete", ship.name)
			}
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_loadoutID", loadout.loadoutID())) {
				t.Fatalf("tech tree row for %q does not include m_loadoutID=%d", ship.name, loadout.loadoutID())
			}
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_shipClass", ship.shipClass)) {
				t.Fatalf("tech tree row for %q does not include m_shipClass=%d", ship.name, ship.shipClass)
			}
			if !bytes.Contains(row, protocol.AppendStringField(nil, "m_loadoutName", loadout.loadoutName)) {
				t.Fatalf("tech tree row for %q does not include m_loadoutName=%q", ship.name, loadout.loadoutName)
			}
			if !bytes.Contains(row, protocol.AppendStringField(nil, "m_displayInfo", loadout.displayInfo())) {
				t.Fatalf("tech tree row for %q does not include m_displayInfo", ship.name)
			}
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_primaryWeaponItemId", loadout.weaponPrimaryItemID())) {
				t.Fatalf("tech tree row for %q does not include m_primaryWeaponItemId=%d", ship.name, loadout.weaponPrimaryItemID())
			}
			if !bytes.Contains(row, protocol.AppendInt32Field(nil, "m_secondaryWeaponItemId", loadout.weaponSecondaryItemID())) {
				t.Fatalf("tech tree row for %q does not include m_secondaryWeaponItemId=%d", ship.name, loadout.weaponSecondaryItemID())
			}
			if !bytes.Contains(row, appendFieldMarker("m_abilityItemIds", 0x0d)) {
				t.Fatalf("tech tree row for %q does not include m_abilityItemIds array", ship.name)
			}
			if !bytes.Contains(row, appendFieldMarker("m_perkIds", 0x0d)) {
				t.Fatalf("tech tree row for %q does not include m_perkIds array", ship.name)
			}
			if !bytes.Contains(row, appendFieldMarker("m_perkNames", 0x0d)) {
				t.Fatalf("tech tree row for %q does not include m_perkNames array", ship.name)
			}
		}
	}
}

func TestTechTreeIncludesInstallerStarterShips(t *testing.T) {
	payload := buildMmogTechTreePayload()
	for _, shipID := range dreadconfig.StarterInventoryShipIDs() {
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "ShipID", shipID)) {
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
	if !bytes.Contains(infos, protocol.AppendInt32Field(nil, "Rank", 1)) {
		t.Fatal("YA_GetPlayersInformation missing Rank")
	}
	if !bytes.Contains(infos, protocol.AppendInt32Field(nil, "UnlockedFleetType", 1)) {
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

func TestTechTreeModuleUIDataIncludesStarterItems(t *testing.T) {
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
		if !bytes.Contains(modulePayload, protocol.AppendInt32Field(nil, "m_itemId", seed.itemID)) {
			t.Fatalf("moduleUiData missing m_itemId=%d", seed.itemID)
		}
		if !bytes.Contains(modulePayload, protocol.AppendInt32Field(nil, "m_techTreeItemState", 4)) {
			t.Fatal("moduleUiData missing owned m_techTreeItemState")
		}
		if !bytes.Contains(modulePayload, protocol.AppendStringField(nil, "m_iconTexturePath", "")) {
			t.Fatalf("moduleUiData missing client-safe empty m_iconTexturePath for item %d", seed.itemID)
		}
		if !bytes.Contains(modulePayload, protocol.AppendStringField(nil, "m_moduleTexturePath", "")) {
			t.Fatalf("moduleUiData missing client-safe empty m_moduleTexturePath for item %d", seed.itemID)
		}
		if meta, ok := extractedMarketItemMetadataForID(seed.itemID); ok && meta.externalID != "" && bytes.Contains(modulePayload, []byte(meta.externalID)) {
			t.Fatalf("moduleUiData should not expose raw asset path %q for item %d", meta.externalID, seed.itemID)
		}
		if !bytes.Contains(modulePayload, appendFieldMarker("m_techTreePurchasePrice", 0x0c)) {
			t.Fatal("moduleUiData missing m_techTreePurchasePrice object")
		}
		if !bytes.Contains(modulePayload, appendFieldMarker("m_techTreeResearchPrice", 0x0c)) {
			t.Fatal("moduleUiData missing m_techTreeResearchPrice object")
		}
	}
}

func TestFleetStateIsConsistentAcrossResponses(t *testing.T) {
	const pid = defaultMmogPlayerPID

	fullStarterFleet := starterFleetState()
	starterFleet := fullStarterFleet.flagshipOnly()
	if got := len(starterFleet.shipLoadouts); got != 1 {
		t.Fatalf("flagship-only hangar fleet exposes %d loadouts, want 1", got)
	}
	if got := len(fullStarterFleet.shipLoadouts); got < 2 {
		t.Fatalf("full starter fleet unexpectedly collapsed to %d loadouts", got)
	}
	playerFleets := buildMmogPlayerFleetsPayload(pid)
	staticFleetData := buildMmogStaticFleetDataPayload()
	playerGet := buildMmogPlayerGetPayload(pid)
	refreshProfile := buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", pid)

	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "FlagShipID", starterFleet.flagshipShipID)) {
		t.Fatalf("YA_PlayerFleets does not expose starter flagship ship %d", starterFleet.flagshipShipID)
	}
	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "FlagShipLoadoutID", starterFleet.flagshipLoadoutID)) {
		t.Fatalf("YA_PlayerFleets does not expose starter flagship loadout %d", starterFleet.flagshipLoadoutID)
	}
	if !bytes.Contains(staticFleetData, appendFieldMarker("Fleets", 0x0d)) {
		t.Fatal("YA_RequestStaticFleetData does not expose Fleets array")
	}
	if !bytes.Contains(staticFleetData, protocol.AppendInt32Field(nil, "FlagShipID", starterFleet.flagshipShipID)) {
		t.Fatalf("YA_RequestStaticFleetData does not expose starter flagship ship %d", starterFleet.flagshipShipID)
	}
	if !bytes.Contains(staticFleetData, protocol.AppendInt32Field(nil, "FlagShipLoadoutID", starterFleet.flagshipLoadoutID)) {
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
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, "FlagShipLoadoutIndex", starterFleet.flagshipLoadoutIndex)) {
			t.Fatalf("%s missing flagship loadout index %d", payloadName, starterFleet.flagshipLoadoutIndex)
		}
	}
	staticCompletion := extractNamedMmogArray(t, staticFleetData, "ShipTechTreeComplete")
	if got := bytes.Count(staticCompletion, protocol.AppendUnnamedBoolField(nil, true)); got != len(starterFleet.shipLoadouts) {
		t.Fatalf("YA_RequestStaticFleetData ShipTechTreeComplete true count = %d, want %d", got, len(starterFleet.shipLoadouts))
	}

	for _, loadout := range starterFleet.shipLoadouts {
		if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "LoadoutID", loadout.loadoutID())) &&
			!bytes.Contains(playerFleets, protocol.AppendUnnamedInt32Field(nil, loadout.loadoutID())) {
			t.Fatalf("YA_PlayerFleets missing starter loadout reference %d", loadout.loadoutID())
		}
		if !bytes.Contains(playerGet, protocol.AppendUnnamedInt32Field(nil, loadout.loadoutID())) &&
			!bytes.Contains(playerGet, protocol.AppendInt32Field(nil, "LoadoutID", loadout.loadoutID())) {
			t.Fatalf("YA_PlayerGet missing starter loadout reference %d", loadout.loadoutID())
		}
		if !bytes.Contains(staticFleetData, protocol.AppendInt32Field(nil, "ShipID", loadout.effectiveFleetShipID())) {
			t.Fatalf("YA_RequestStaticFleetData missing starter fleet ship %d", loadout.effectiveFleetShipID())
		}
		if !bytes.Contains(staticFleetData, protocol.AppendInt32Field(nil, "LoadoutID", loadout.loadoutID())) {
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
	if !bytes.Contains(playerAGet, protocol.AppendInt32Field(nil, "FreeXp", 90)) {
		t.Fatal("YA_PlayerGet missing persisted free XP")
	}

	playerAProgression := buildMmogPlayerProgressionPayload(playerA)
	if !bytes.Contains(playerAProgression, protocol.AppendInt32Field(nil, "CurrentXP", 111)) {
		t.Fatal("YA_GetPlayerProgression missing persisted current XP")
	}
	if !bytes.Contains(playerAProgression, protocol.AppendInt32Field(nil, "CurrentRank", 7)) {
		t.Fatal("YA_GetPlayerProgression missing persisted rank")
	}
	if !bytes.Contains(playerAProgression, protocol.AppendInt32Field(nil, "RankXP", 222)) {
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

func TestUserLoginPayloadUsesPersistedEconomyState(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const playerPID = "cccccccccccccccccccccccccccccccc"

	_ = buildMmogPlayerGetPayload(playerPID)
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=?,premium_currency=?,free_xp=?,current_xp=? WHERE user_id=?`,
		54321, 987, 654, 321, playerPID); err != nil {
		t.Fatalf("update login economy state: %v", err)
	}

	result := extractNamedMmogObject(t, buildMmogLoginSuccessPayload(playerPID), "result")
	for _, field := range []struct {
		name  string
		value int32
	}{
		{name: "credits", value: 54321},
		{name: "premiumCurrency", value: 987},
		{name: "freexp", value: 654},
		{name: "xp", value: 321},
	} {
		if !bytes.Contains(result, protocol.AppendInt32Field(nil, field.name, field.value)) {
			t.Fatalf("YA_UserLogin result missing persisted %s=%d", field.name, field.value)
		}
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
	if !bytes.Contains(playerAGet, protocol.AppendInt32Field(nil, "weaponPrimary", 123456789)) {
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
	if !bytes.Contains(result, protocol.AppendStringField(nil, "GameMode", "TeamElimination")) {
		t.Fatal("YA_EnterMatchmaking did not echo requested game mode")
	}

	var gameMode string
	var tierMin, tierMax int
	if err := database.QueryRow(`SELECT game_mode,tier_min,tier_max FROM queue_entries WHERE user_id=? AND status='waiting'`, playerPID).
		Scan(&gameMode, &tierMin, &tierMax); err != nil {
		t.Fatalf("load queued entry: %v", err)
	}
	if gameMode != "TeamElimination" || tierMin != 2 || tierMax != 4 {
		t.Fatalf("queued entry = %s/%d/%d, want TeamElimination/2/4", gameMode, tierMin, tierMax)
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

func TestMmogCheckReturnUsesCanReturnToMatchFields(t *testing.T) {
	result := extractNamedMmogObject(t, buildMmogCheckReturnPayload(), "result")

	if !bytes.Contains(result, protocol.AppendBoolField(nil, "CanReturnToMatch", false)) {
		t.Fatal("YA_CheckReturn missing CanReturnToMatch=false")
	}
	if !bytes.Contains(result, protocol.AppendBoolField(nil, "canReturnToMatch", false)) {
		t.Fatal("YA_CheckReturn missing canReturnToMatch=false")
	}
	if !bytes.Contains(result, protocol.AppendBoolField(nil, "ReturnValue", false)) {
		t.Fatal("YA_CheckReturn missing ReturnValue=false")
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
	if got := bytes.Count(fleetEligibility, appendFieldMarker("FleetType", 0x56)); got != len(wantEligibilities) {
		t.Fatalf("YA_FleetEligibility FleetType count = %d, want %d", got, len(wantEligibilities))
	}
	if got := bytes.Count(fleetTypes, appendFieldMarker("Tiers", 0x0d)); got != len(wantEligibilities) {
		t.Fatalf("YA_RequestStaticFleetData FleetTypes tier-array count = %d, want %d", got, len(wantEligibilities))
	}
	tierCounts := map[int32]int{}
	for _, eligibility := range wantEligibilities {
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "ID", eligibility.FleetType)) {
			t.Fatalf("YA_RequestStaticFleetData missing config-backed FleetType id %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "ShipsToUnlock", eligibility.NumShipsToUnlockFleet)) {
			t.Fatalf("YA_RequestStaticFleetData missing ShipsToUnlock=%d for fleet type %d", eligibility.NumShipsToUnlockFleet, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "BaseMaintenanceCost", eligibility.BaseMaintenanceCost)) {
			t.Fatalf("YA_RequestStaticFleetData missing BaseMaintenanceCost=%d for fleet type %d", eligibility.BaseMaintenanceCost, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendStringField(nil, "FleetRatingMin", strconv.FormatFloat(eligibility.FleetRatingMin, 'f', 1, 64))) {
			t.Fatalf("YA_RequestStaticFleetData missing FleetRatingMin for fleet type %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "FleetRatingCost", eligibility.FleetRatingCost)) {
			t.Fatalf("YA_RequestStaticFleetData missing FleetRatingCost=%d for fleet type %d", eligibility.FleetRatingCost, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "ChargeTime", eligibility.MaintenanceTime)) {
			t.Fatalf("YA_RequestStaticFleetData missing ChargeTime=%d for fleet type %d", eligibility.MaintenanceTime, eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "ChargeCost", 0)) {
			t.Fatalf("YA_RequestStaticFleetData missing neutral ChargeCost for fleet type %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetTypes, protocol.AppendInt32Field(nil, "AvailableCharges", 1)) {
			t.Fatalf("YA_RequestStaticFleetData missing AvailableCharges=1 for fleet type %d", eligibility.FleetType)
		}
		if !bytes.Contains(fleetEligibility, protocol.AppendInt32Field(nil, "FleetType", eligibility.FleetType)) {
			t.Fatalf("YA_FleetEligibility missing config-backed FleetType %d", eligibility.FleetType)
		}
		for _, tier := range eligibility.AllowedTiers {
			tierCounts[tier]++
		}
	}
	for tier, wantCount := range tierCounts {
		if got := bytes.Count(fleetTypes, protocol.AppendUnnamedInt32Field(nil, tier)); got != wantCount {
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
		fleetShipID, ok := fleetStarterShipIDsByPrecastID[loadout.LoadoutID]
		if !ok {
			t.Fatalf("missing fleet ship id for starter loadout %d", loadout.LoadoutID)
		}
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
		wantFleetShipID, ok := fleetStarterShipIDsByPrecastID[sharedLoadout.LoadoutID]
		if !ok {
			t.Fatalf("missing fleet ship id for starter loadout %d", sharedLoadout.LoadoutID)
		}
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
	expectedNativeIDs := map[int32]string{
		33489262: "Default__VH_AssaultMedium_T1_Loadout_BP_C",
		33489423: "Default__VH_DreadnoughtMedium_Loadout_BP_C",
		33489263: "Default__VH_SniperMedium_T1_Loadout_BP_C",
		33489264: "Default__VH_SupportMedium_T1_Loadout_BP_C",
	}

	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	staticFleetData := buildMmogStaticFleetDataPayload()
	loadouts := starterShipLoadouts()
	hangarFleet := starterFleetState().flagshipOnly()
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
		if bytes.Contains(playerGet, protocol.AppendStringField(nil, "ID", loadout.entryID())) {
			t.Fatalf("YA_PlayerGet should not emit default starter loadout as custom native ID %q", loadout.entryID())
		}
		if bytes.Contains(staticFleetData, protocol.AppendStringField(nil, "ID", loadout.entryID())) {
			t.Fatalf("YA_RequestStaticFleetData should not emit default starter loadout as custom native ID %q", loadout.entryID())
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
	if bytes.Contains(playerGet, []byte("PrecastLoadout_BP")) {
		t.Fatal("YA_PlayerGet native loadout IDs should use development-table object IDs, not precast asset IDs")
	}
	if bytes.Contains(staticFleetData, []byte("PrecastLoadout_BP")) {
		t.Fatal("YA_RequestStaticFleetData native loadout IDs should use development-table object IDs, not precast asset IDs")
	}

	if bytes.Contains(playerGet, appendFieldMarker("precastLoadout", 0x56)) {
		t.Fatal("YA_PlayerGet should not emit default starter loadouts as custom MMOG ShipLoadouts")
	}
	if bytes.Contains(staticFleetData, appendFieldMarker("precastLoadout", 0x56)) {
		t.Fatal("YA_RequestStaticFleetData should not emit default starter loadouts as custom MMOG ShipLoadouts")
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
	if nativeLoadoutID != "Default__VH_AssaultMedium_T1_Loadout_BP_C" {
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
	starterFleet := starterFleetState().flagshipOnly()

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
		if !bytes.Contains(payload, appendFieldMarker("class", 0x56)) {
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
			if !bytes.Contains(payload, appendFieldMarker(field, 0x56)) {
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
	if !bytes.Contains(playerGet, appendFieldMarker("m_fleetId", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_fleetId field")
	}
	if !bytes.Contains(playerGet, appendFieldMarker("m_flagshipIndex", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_flagshipIndex field")
	}
	if !bytes.Contains(playerGet, appendFieldMarker("m_fleetType", 0x56)) {
		t.Fatal("YA_PlayerGet missing m_fleetType field")
	}
	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "flagshipID", starterFleet.flagshipLoadoutID)) {
		t.Fatalf("YA_PlayerFleets missing flagshipID keyed to starter loadout %d", starterFleet.flagshipLoadoutID)
	}
	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "shipCount", int32(len(starterFleet.shipLoadouts)))) {
		t.Fatalf("YA_PlayerFleets missing shipCount=%d", len(starterFleet.shipLoadouts))
	}
	if !bytes.Contains(playerFleets, protocol.AppendInt32Field(nil, "m_flagshipIndex", starterFleet.flagshipIndex())) {
		t.Fatalf("YA_PlayerFleets missing m_flagshipIndex=%d", starterFleet.flagshipIndex())
	}
	for _, field := range []struct {
		name  string
		value []byte
	}{
		{name: "AutoRepair", value: protocol.AppendBoolField(nil, "AutoRepair", false)},
		{name: "Maintenance", value: protocol.AppendBoolField(nil, "Maintenance", false)},
		{name: "LastWinTime", value: protocol.AppendInt32Field(nil, "LastWinTime", 0)},
		{name: "ChargingBeginTime", value: protocol.AppendInt32Field(nil, "ChargingBeginTime", 0)},
		{name: "ChargingCharges", value: protocol.AppendInt32Field(nil, "ChargingCharges", 1)},
		{name: "Rating", value: protocol.AppendInt32Field(nil, "Rating", 0)},
	} {
		if !bytes.Contains(playerFleets, field.value) {
			t.Fatalf("YA_PlayerFleets missing %s default state", field.name)
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

func TestBootstrapPayloadsTrimDeepLoadoutCollections(t *testing.T) {
	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	playerFleets := buildMmogPlayerFleetsPayload(defaultMmogPlayerPID)
	staticFleetData := buildMmogStaticFleetDataPayload()

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

	for _, field := range []string{"OwnedShipLoadouts", "PreviewLoadoutItems", "Items"} {
		if bytes.Contains(playerGet, appendFieldMarker(field, 0x0d)) {
			t.Fatalf("YA_PlayerGet should not include %s after payload trim", field)
		}
	}
	for _, field := range []string{"BaseMaintenanceCost", "ChargeTime", "ChargeCost", "AvailableCharges", "ShipsToUnlock"} {
		if !bytes.Contains(staticFleetData, appendFieldMarker(field, 0x56)) {
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
	if bytes.Contains(playerFleets, []byte("VeteranFleet")) || bytes.Contains(playerFleets, []byte("LegendaryFleet")) {
		t.Fatal("YA_PlayerFleets should only include the active starter fleet after payload trim")
	}
	if got := bytes.Count(playerFleets, appendFieldMarker("m_fleetId", 0x56)); got != 1 {
		t.Fatalf("YA_PlayerFleets active fleet row count = %d, want 1", got)
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
		value int32
	}{
		{name: "DailyContractStateID", value: 0},
		{name: "tslm", value: 0},
	} {
		if !bytes.Contains(payload, protocol.AppendInt32Field(nil, field.name, field.value)) {
			t.Fatalf("YA_PlayerGet missing %s=%d", field.name, field.value)
		}
	}
	if bytes.Contains(payload, appendFieldMarker("Quests", 0x0d)) {
		t.Fatal("YA_PlayerGet should not mark player quest state ready")
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

func TestCareerPayloadsUseConfigBackedProgressionMetadata(t *testing.T) {
	staticCareerData := buildMmogStaticCareerDataPayload()
	careerProgression := buildMmogCareerProgressionPayload()
	taxonomies := configBackedProgressionTaxonomies()
	staticCategories := extractNamedMmogArray(t, extractNamedMmogObject(t, staticCareerData, "result"), "m_categories")
	careerCategories := extractNamedMmogArray(t, extractNamedMmogObject(t, careerProgression, "result"), "m_categories")

	if got := bytes.Count(staticCategories, appendFieldMarker("TableCategory", 0x09)); got != len(taxonomies) {
		t.Fatalf("YA_GetStaticCareerData category count = %d, want %d", got, len(taxonomies))
	}
	if got := bytes.Count(careerCategories, appendFieldMarker("TableCategory", 0x09)); got != len(taxonomies) {
		t.Fatalf("YA_GetCareerProgression category count = %d, want %d", got, len(taxonomies))
	}

	wantPath := configBackedProgressionCategoryDataTablePath()
	if !bytes.Contains(staticCareerData, protocol.AppendStringField(nil, "m_categoryDTPath", wantPath)) {
		t.Fatalf("YA_GetStaticCareerData missing config-backed m_categoryDTPath %q", wantPath)
	}

	for _, taxonomy := range taxonomies {
		if !bytes.Contains(staticCategories, protocol.AppendStringField(nil, "TableCategory", taxonomy.TableCategory)) {
			t.Fatalf("YA_GetStaticCareerData missing progression category %q", taxonomy.TableCategory)
		}
		if !bytes.Contains(careerCategories, protocol.AppendStringField(nil, "TableCategory", taxonomy.TableCategory)) {
			t.Fatalf("YA_GetCareerProgression missing progression category %q", taxonomy.TableCategory)
		}
		if !bytes.Contains(staticCategories, protocol.AppendInt32Field(nil, "CategoryID", taxonomy.CategoryID)) {
			t.Fatalf("YA_GetStaticCareerData missing category id %d", taxonomy.CategoryID)
		}
		if !bytes.Contains(careerCategories, protocol.AppendInt32Field(nil, "CategoryID", taxonomy.CategoryID)) {
			t.Fatalf("YA_GetCareerProgression missing category id %d", taxonomy.CategoryID)
		}
		for _, assetRoot := range taxonomy.AssetRoots {
			if !bytes.Contains(staticCategories, protocol.AppendUnnamedStringField(nil, assetRoot)) {
				t.Fatalf("YA_GetStaticCareerData missing asset root %q", assetRoot)
			}
			if !bytes.Contains(careerCategories, protocol.AppendUnnamedStringField(nil, assetRoot)) {
				t.Fatalf("YA_GetCareerProgression missing asset root %q", assetRoot)
			}
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

	seasonsRaw := protocol.ExtractStringField(result, "Seasons")
	if seasonsRaw != "[]" {
		t.Fatalf("YA_GetSeasonData Seasons = %q, want empty array", seasonsRaw)
	}
	eventsRaw := protocol.ExtractStringField(result, "Events")
	if eventsRaw != "[]" {
		t.Fatalf("YA_GetSeasonData Events = %q, want empty array", eventsRaw)
	}
	if currentSeason := protocol.ExtractStringField(result, "CurrentSeason"); currentSeason != "" {
		t.Fatalf("YA_GetSeasonData CurrentSeason = %q, want empty", currentSeason)
	}
	if activeEvent := protocol.ExtractStringField(result, "ActiveEvent"); activeEvent != "" {
		t.Fatalf("YA_GetSeasonData ActiveEvent = %q, want empty", activeEvent)
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
		"YA_GetPlayerStatsCounterData": buildMmogPlayerStatsCounterDataPayload,
		"YA_GetPlayerProgression":      func() []byte { return buildMmogPlayerProgressionPayload(pid) },
		"YA_GetTechTree":               buildMmogTechTreePayload,
		"YA_GetPlayerPurchases":        buildMmogPlayerPurchasesPayload,
		"YA_FleetEligibility":          buildMmogFleetEligibilityPayload,
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

func TestReconnectFlushOnlySendsPendingPlayerFleets(t *testing.T) {
	conn := &captureConn{}
	fleetRequestID := syntheticRequestID(0xa1)
	clientPlayerGetID := syntheticRequestID(0xa2)
	reconnectPlayerGetID := syntheticRequestID(0xf0)
	requestPayload := protocol.AppendStringField(nil, "RT", "YA_PlayerFleets")
	requestPayload = protocol.AppendRootEnd(requestPayload)
	state := &mmogConnState{
		playerPID:                defaultMmogPlayerPID,
		loginResponseSent:        true,
		staticFleetDataReceived:  true,
		fleetEligibilityReceived: true,
		playerFleetsReceived:     true,
		pendingPlayerFleets: &protocol.AppFrame{
			MsgType:   0x0320,
			RequestID: fleetRequestID,
			Payload:   requestPayload,
		},
	}

	if err := flushPendingPlayerFleets(logrus.New(), conn, "test-remote", nil, false, state, "test"); err != nil {
		t.Fatalf("flushPendingPlayerFleets: %v", err)
	}

	frames, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after parsing reconnect flush")
	}
	if len(frames) != 1 {
		t.Fatalf("reconnect flush wrote %d frames, want 1", len(frames))
	}
	if got := protocol.ExtractRequestName(frames[0].Payload); got != "YA_PlayerFleets" {
		t.Fatalf("first reconnect frame = %q, want YA_PlayerFleets", got)
	}
	if frames[0].RequestID != fleetRequestID {
		t.Fatalf("reconnect fleet flush request id = %x, want original %x", frames[0].RequestID, fleetRequestID)
	}
	if state.playerGetResponded {
		t.Fatal("reconnect flush should not mark playerGetResponded before the client asks for YA_PlayerGet")
	}
	playerGetRequest := protocol.AppendStringField(nil, "RT", "YA_PlayerGet")
	playerGetRequest = protocol.AppendRootEnd(playerGetRequest)
	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: clientPlayerGetID,
		Payload:   playerGetRequest,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames: %v", err)
	}

	frames, remaining = protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after parsing reconnect PlayerGet")
	}
	if len(frames) != 2 {
		t.Fatalf("reconnect flow wrote %d frames, want 2", len(frames))
	}
	if got := protocol.ExtractRequestName(frames[1].Payload); got != "YA_PlayerGet" {
		t.Fatalf("second reconnect frame = %q, want YA_PlayerGet", got)
	}
	if frames[1].RequestID != clientPlayerGetID {
		t.Fatalf("client YA_PlayerGet response id = %x, want %x", frames[1].RequestID, clientPlayerGetID)
	}
	for i, frame := range frames {
		if frame.RequestID == reconnectPlayerGetID {
			t.Fatalf("reconnect flow synthesized YA_PlayerGet frame at index %d", i)
		}
	}
	if !state.playerGetResponded {
		t.Fatal("explicit YA_PlayerGet did not mark playerGetResponded")
	}
	if state.pendingPlayerFleets != nil {
		t.Fatal("reconnect flush did not clear pendingPlayerFleets")
	}
}

func TestPlayerPurchasesWaitForPlayerGet(t *testing.T) {
	conn := &captureConn{}
	purchasesRequestID := syntheticRequestID(0xb1)
	playerGetRequestID := syntheticRequestID(0xb2)
	purchasesRequest := protocol.AppendStringField(nil, "RT", "YA_GetPlayerPurchases")
	purchasesRequest = protocol.AppendRootEnd(purchasesRequest)
	playerGetRequest := protocol.AppendStringField(nil, "RT", "YA_PlayerGet")
	playerGetRequest = protocol.AppendRootEnd(playerGetRequest)
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
	if conn.Len() != 0 {
		t.Fatalf("delayed purchases wrote %d bytes before YA_PlayerGet, want 0", conn.Len())
	}
	if state.pendingPlayerPurchases == nil {
		t.Fatal("YA_GetPlayerPurchases was not delayed before YA_PlayerGet")
	}
	if state.playerGetResponded {
		t.Fatal("delayed purchases should not mark playerGetResponded")
	}

	if err := processMmogAppFrames(logrus.New(), conn, "test-remote", []protocol.AppFrame{{
		MsgType:   0x0320,
		RequestID: playerGetRequestID,
		Payload:   playerGetRequest,
	}}, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames PlayerGet: %v", err)
	}

	frames, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after delayed purchases flush")
	}
	if len(frames) != 5 {
		t.Fatalf("PlayerGet with delayed purchases wrote %d frames, want 5", len(frames))
	}
	wantNames := []string{"YA_PlayerGet", "YA_RequestStaticFleetData", "YA_GetPlayerPurchases", "YA_FleetEligibility", "YA_PlayerFleets"}
	for i, wantName := range wantNames {
		if got := protocol.ExtractRequestName(frames[i].Payload); got != wantName {
			t.Fatalf("frame %d = %q, want %q", i, got, wantName)
		}
	}
	if frames[0].RequestID != playerGetRequestID {
		t.Fatalf("YA_PlayerGet response id = %x, want %x", frames[0].RequestID, playerGetRequestID)
	}
	if frames[2].RequestID != purchasesRequestID {
		t.Fatalf("YA_GetPlayerPurchases response id = %x, want %x", frames[2].RequestID, purchasesRequestID)
	}
	if state.pendingPlayerPurchases != nil {
		t.Fatal("PlayerGet did not clear pendingPlayerPurchases")
	}
	if !state.playerGetResponded {
		t.Fatal("PlayerGet should mark playerGetResponded after delayed purchases flush")
	}
}

func TestObserverOnlyBootstrapResponsePolicy(t *testing.T) {
	for _, tc := range []struct {
		requestName string
		wantFrames  int
	}{
		{requestName: "YA_GetDailyContractsData", wantFrames: 1},
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
				t.Fatalf("%s wrote %d bytes, want suppressed response", requestName, conn.Len())
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

func TestPlayerGetBootstrapOnlyPushesFleetData(t *testing.T) {
	conn := &captureConn{}
	state := &mmogConnState{
		playerPID:         defaultMmogPlayerPID,
		loginResponseSent: true,
	}

	if err := handlePlayerGetSatisfied(logrus.New(), conn, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
	}

	frames, remaining := protocol.ParseAppFrames(conn.Bytes())
	if len(remaining) != 0 {
		t.Fatalf("unexpected remaining bytes after PlayerGet bootstrap")
	}
	if len(frames) != 3 {
		t.Fatalf("PlayerGet bootstrap wrote %d frames, want 3", len(frames))
	}

	wantNames := []string{"YA_RequestStaticFleetData", "YA_FleetEligibility", "YA_PlayerFleets"}
	bootstrapID := syntheticRequestID(0xf1)
	for i, wantName := range wantNames {
		if got := protocol.ExtractRequestName(frames[i].Payload); got != wantName {
			t.Fatalf("bootstrap frame %d = %q, want %q", i, got, wantName)
		}
		if frames[i].RequestID != bootstrapID {
			t.Fatalf("bootstrap frame %d request id = %x, want %x", i, frames[i].RequestID, bootstrapID)
		}
		if got := protocol.ExtractRequestName(frames[i].Payload); got == "YA_PlayerGet" {
			t.Fatalf("bootstrap frame %d unexpectedly synthesized YA_PlayerGet", i)
		}
	}
	if !state.playerGetResponded {
		t.Fatal("PlayerGet bootstrap should mark playerGetResponded")
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

	token := mustSignTestJWT(t, jwt.MapClaims{"user_id": userID})
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
