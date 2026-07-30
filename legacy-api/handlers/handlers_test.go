package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	legacydb "github.com/dreadnought-ps/legacy-api/db"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func TestNewMmogProgressionRequestUsesInternalEndpointAndKey(t *testing.T) {
	oldKey, hadKey := os.LookupEnv("INTERNAL_API_KEY")
	t.Setenv("INTERNAL_API_KEY", "test-internal-key")
	if !hadKey {
		t.Cleanup(func() { _ = os.Unsetenv("INTERNAL_API_KEY") })
	} else {
		t.Cleanup(func() { _ = os.Setenv("INTERNAL_API_KEY", oldKey) })
	}

	req, err := newMmogProgressionRequest([]byte(`{"user_id":"user-1"}`))
	if err != nil {
		t.Fatalf("newMmogProgressionRequest: %v", err)
	}
	if got := req.URL.Path; got != "/internal/progression" {
		t.Fatalf("path = %q, want /internal/progression", got)
	}
	if got := req.Header.Get("X-Internal-Key"); got != "test-internal-key" {
		t.Fatalf("internal key = %q, want test-internal-key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
}

func TestHealthReturnsOKWhenDatabaseReachable(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if payload.Status != "ok" || payload.Database != "ok" {
		t.Fatalf("payload = %+v, want status/database = ok/ok", payload)
	}
}

func TestHealthReturnsServiceUnavailableWhenDatabaseUnreachable(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Close the DB up front so Ping() fails, simulating an unreachable database.
	if err := database.Close(); err != nil {
		t.Fatalf("close test db: %v", err)
	}

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.Health(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var payload struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if payload.Status != "error" || payload.Database != "error" {
		t.Fatalf("payload = %+v, want status/database = error/error", payload)
	}
}

func TestGetInventoryPinsStarterIdentityListsToSharedConfig(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-identity")

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-identity/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-identity"})
	req.Header.Set("X-User-ID", "user-identity")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Items             []InventoryItem `json:"items"`
		StarterShipIDs    []int32         `json:"starter_ship_ids"`
		StarterLoadoutIDs []int32         `json:"starter_loadout_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload: %v", err)
	}

	wantShipIDs := dreadconfig.StarterInventoryShipIDs()
	if len(payload.StarterShipIDs) != len(wantShipIDs) {
		t.Fatalf("starter_ship_ids count = %d, want %d", len(payload.StarterShipIDs), len(wantShipIDs))
	}
	for idx, wantID := range wantShipIDs {
		if payload.StarterShipIDs[idx] != wantID {
			t.Fatalf("starter_ship_ids[%d] = %d, want %d", idx, payload.StarterShipIDs[idx], wantID)
		}
	}

	wantLoadoutIDs := dreadconfig.StarterInventoryLoadoutIDs()
	if len(payload.StarterLoadoutIDs) != len(wantLoadoutIDs) {
		t.Fatalf("starter_loadout_ids count = %d, want %d", len(payload.StarterLoadoutIDs), len(wantLoadoutIDs))
	}
	for idx, wantID := range wantLoadoutIDs {
		if payload.StarterLoadoutIDs[idx] != wantID {
			t.Fatalf("starter_loadout_ids[%d] = %d, want %d", idx, payload.StarterLoadoutIDs[idx], wantID)
		}
	}

	if len(payload.Items) != len(starterInventoryBootstrapSeeds()) {
		t.Fatalf("starter inventory item count = %d, want %d", len(payload.Items), len(starterInventoryBootstrapSeeds()))
	}
}

func TestGetInventoryCreatesProfileBeforeStarterSeeding(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}

	if _, err := database.Exec(
		`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES('starter-hidden','user-gated','ship','5000235')`,
	); err != nil {
		t.Fatalf("insert starter row before readiness: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES('custom-visible','user-gated','ship','custom-ship')`,
	); err != nil {
		t.Fatalf("insert non-starter row before readiness: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-gated/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-gated"})
	req.Header.Set("X-User-ID", "user-gated")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Items             []InventoryItem `json:"items"`
		StarterShipIDs    []int32         `json:"starter_ship_ids"`
		StarterLoadoutIDs []int32         `json:"starter_loadout_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload: %v", err)
	}

	if len(payload.Items) != 1 {
		t.Fatalf("item count before profile bootstrap = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].ID != "custom-visible" {
		t.Fatalf("visible row before profile bootstrap = %s, want custom-visible", payload.Items[0].ID)
	}
	if len(payload.StarterShipIDs) != 0 {
		t.Fatalf("starter_ship_ids count before profile bootstrap = %d, want 0", len(payload.StarterShipIDs))
	}
	if len(payload.StarterLoadoutIDs) != 0 {
		t.Fatalf("starter_loadout_ids count before profile bootstrap = %d, want 0", len(payload.StarterLoadoutIDs))
	}

	var profileCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_profiles WHERE user_id='user-gated'`).Scan(&profileCount); err != nil {
		t.Fatalf("count profiles after inventory bootstrap: %v", err)
	}
	if profileCount != 1 {
		t.Fatalf("profile count after inventory bootstrap = %d, want 1", profileCount)
	}

	var statsCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_stats WHERE user_id='user-gated'`).Scan(&statsCount); err != nil {
		t.Fatalf("count stats after inventory bootstrap: %v", err)
	}
	if statsCount != 1 {
		t.Fatalf("stats count after inventory bootstrap = %d, want 1", statsCount)
	}

	var rowCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_inventory WHERE user_id='user-gated'`).Scan(&rowCount); err != nil {
		t.Fatalf("count inventory rows before profile bootstrap: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("inventory row count before profile bootstrap = %d, want 2", rowCount)
	}

	rec = httptest.NewRecorder()
	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status after profile bootstrap = %d, want %d", rec.Code, http.StatusOK)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload after profile bootstrap: %v", err)
	}
	if len(payload.StarterShipIDs) != len(dreadconfig.StarterInventoryShipIDs()) {
		t.Fatalf("starter_ship_ids count after profile bootstrap = %d, want %d", len(payload.StarterShipIDs), len(dreadconfig.StarterInventoryShipIDs()))
	}
	if len(payload.StarterLoadoutIDs) != len(dreadconfig.StarterInventoryLoadoutIDs()) {
		t.Fatalf("starter_loadout_ids count after profile bootstrap = %d, want %d", len(payload.StarterLoadoutIDs), len(dreadconfig.StarterInventoryLoadoutIDs()))
	}
}

func TestGetProfileEnsuresMissingProfileAndStats(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-profile/profile", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-profile"})
	req.Header.Set("X-User-ID", "user-profile")
	rec := httptest.NewRecorder()

	handler.GetProfile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		UserID      string `json:"user_id"`
		DisplayName string `json:"display_name"`
		Stats       struct {
			Credits int `json:"credits"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode profile payload: %v", err)
	}
	if payload.UserID != "user-profile" {
		t.Fatalf("user_id = %s, want user-profile", payload.UserID)
	}
	if payload.DisplayName != "Player_user-pro" {
		t.Fatalf("display_name = %s, want Player_user-pro", payload.DisplayName)
	}
	if payload.Stats.Credits != 10000 {
		t.Fatalf("credits = %d, want 10000", payload.Stats.Credits)
	}

	var profileCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_profiles WHERE user_id='user-profile'`).Scan(&profileCount); err != nil {
		t.Fatalf("count created profile: %v", err)
	}
	if profileCount != 1 {
		t.Fatalf("profile count = %d, want 1", profileCount)
	}

	var statsCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_stats WHERE user_id='user-profile'`).Scan(&statsCount); err != nil {
		t.Fatalf("count created stats: %v", err)
	}
	if statsCount != 1 {
		t.Fatalf("stats count = %d, want 1", statsCount)
	}
}

func TestGetInventorySeedsCoherentStarterInventory(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-1")

	if _, err := database.Exec(
		`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES('legacy-agosta','user-1','ship','5000235')`,
	); err != nil {
		t.Fatalf("insert legacy placeholder: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-1/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-1"})
	req.Header.Set("X-User-ID", "user-1")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Items []InventoryItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload: %v", err)
	}

	counts := map[string]int{}
	seen := map[string]bool{}
	for _, item := range payload.Items {
		counts[item.ItemType]++
		seen[inventorySeedKey(item.ItemType, item.ItemID)] = true
	}

	if counts[dreadconfig.ItemTypeShip] != 4 {
		t.Fatalf("ship item count = %d, want 4", counts[dreadconfig.ItemTypeShip])
	}
	if counts[dreadconfig.ItemTypeLoadout] != 4 {
		t.Fatalf("loadout item count = %d, want 4", counts[dreadconfig.ItemTypeLoadout])
	}
	if counts[dreadconfig.ItemTypeWeapon] != 8 {
		t.Fatalf("weapon item count = %d, want 8", counts[dreadconfig.ItemTypeWeapon])
	}
	if counts[dreadconfig.ItemTypeAbility] != 16 {
		t.Fatalf("ability item count = %d, want 16", counts[dreadconfig.ItemTypeAbility])
	}
	if counts[dreadconfig.ItemTypePerk] != 0 {
		t.Fatalf("perk item count = %d, want 0", counts[dreadconfig.ItemTypePerk])
	}
	if counts[itemTypeLegacyUI] != 0 {
		t.Fatalf("legacy loadout_item count = %d, want 0", counts[itemTypeLegacyUI])
	}
	if seen[inventorySeedKey("ship", "5000235")] {
		t.Fatal("legacy placeholder ship should not remain in inventory response")
	}
	legacyStarterPrimaryID := strconv.Itoa(int(dreadconfig.StarterInventoryLoadouts()[0].LoadoutID*10 + 1))
	if seen[inventorySeedKey(dreadconfig.ItemTypeWeapon, legacyStarterPrimaryID)] {
		t.Fatalf("legacy fabricated starter slot id %s should not remain in inventory response", legacyStarterPrimaryID)
	}
	for _, seed := range starterInventoryBootstrapSeeds() {
		if !seen[inventorySeedKey(seed.ItemType, seed.ItemID)] {
			t.Fatalf("missing starter inventory seed %s", inventorySeedKey(seed.ItemType, seed.ItemID))
		}
	}
}

func TestGetInventoryNormalizesLegacyLoadoutItemRows(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-2")

	legacyStarterPrimaryID := strconv.Itoa(int(dreadconfig.StarterInventoryLoadouts()[0].LoadoutID*10 + 1))
	legacySeed, ok := legacyStarterInventorySeedAliases()[inventorySeedKey(itemTypeLegacyUI, legacyStarterPrimaryID)]
	if !ok {
		t.Fatalf("missing legacy starter inventory alias for starter slot %s", legacyStarterPrimaryID)
	}
	if _, err := database.Exec(
		`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES('legacy-starter-primary','user-2',?,?)`,
		itemTypeLegacyUI, legacyStarterPrimaryID,
	); err != nil {
		t.Fatalf("insert legacy loadout item: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-2/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-2"})
	req.Header.Set("X-User-ID", "user-2")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Items []InventoryItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload: %v", err)
	}

	normalizedCount := 0
	for _, item := range payload.Items {
		if item.ID != "legacy-starter-primary" {
			continue
		}
		if item.ItemID != legacySeed.ItemID {
			t.Fatalf("normalized item id = %s, want %s", item.ItemID, legacySeed.ItemID)
		}
		if item.ItemType != legacySeed.ItemType {
			t.Fatalf("normalized item type = %s, want %s", item.ItemType, legacySeed.ItemType)
		}
		if item.SlotName != legacySeed.SlotName {
			t.Fatalf("normalized slot name = %s, want %s", item.SlotName, legacySeed.SlotName)
		}
		normalizedCount++
	}
	if normalizedCount != 1 {
		t.Fatalf("normalized starter item count = %d, want 1", normalizedCount)
	}

	var storedType string
	var storedItemID string
	if err := database.QueryRow(
		`SELECT item_type,item_id FROM player_inventory WHERE id='legacy-starter-primary'`,
	).Scan(&storedType, &storedItemID); err != nil {
		t.Fatalf("query normalized starter row: %v", err)
	}
	if storedType != legacySeed.ItemType {
		t.Fatalf("stored starter item type = %s, want %s", storedType, legacySeed.ItemType)
	}
	if storedItemID != legacySeed.ItemID {
		t.Fatalf("stored starter item id = %s, want %s", storedItemID, legacySeed.ItemID)
	}
}

func TestGetInventoryNormalizesFabricatedStarterItemIDs(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-3")

	legacyStarterPrimaryID := strconv.Itoa(int(dreadconfig.StarterInventoryLoadouts()[0].LoadoutID*10 + 1))
	legacySeed, ok := legacyStarterInventorySeedAliases()[inventorySeedKey(dreadconfig.ItemTypeWeapon, legacyStarterPrimaryID)]
	if !ok {
		t.Fatalf("missing starter inventory alias for normalized starter slot %s", legacyStarterPrimaryID)
	}
	if _, err := database.Exec(
		`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES('legacy-starter-primary-normalized','user-3',?,?)`,
		dreadconfig.ItemTypeWeapon, legacyStarterPrimaryID,
	); err != nil {
		t.Fatalf("insert normalized fabricated row: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-3/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-3"})
	req.Header.Set("X-User-ID", "user-3")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var storedType string
	var storedItemID string
	if err := database.QueryRow(
		`SELECT item_type,item_id FROM player_inventory WHERE id='legacy-starter-primary-normalized'`,
	).Scan(&storedType, &storedItemID); err != nil {
		t.Fatalf("query normalized starter row: %v", err)
	}
	if storedType != legacySeed.ItemType {
		t.Fatalf("stored starter item type = %s, want %s", storedType, legacySeed.ItemType)
	}
	if storedItemID != legacySeed.ItemID {
		t.Fatalf("stored starter item id = %s, want %s", storedItemID, legacySeed.ItemID)
	}
}

func TestGetInventoryNormalizesAllFabricatedStarterItemAliases(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-fabricated")

	wantByRowID := map[string]inventoryBootstrapSeed{}
	fabricatedIDs := map[string]struct{}{}
	rowIndex := 0
	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		for slotIndex := range loadout.Slots {
			legacyItemID := strconv.Itoa(int(loadout.LoadoutID*10 + int32(slotIndex) + 1))
			insertType := itemTypeLegacyUI
			if slotIndex%2 == 1 {
				meta, ok := dreadconfig.ItemByID(loadout.Slots[slotIndex].ItemID)
				if !ok {
					t.Fatalf("missing shared starter item metadata for %d", loadout.Slots[slotIndex].ItemID)
				}
				insertType = meta.ItemType
			}
			key := inventorySeedKey(insertType, legacyItemID)
			seed, ok := legacyStarterInventorySeedAliases()[key]
			if !ok {
				t.Fatalf("missing fabricated starter alias for %s", key)
			}
			rowID := "legacy-fabricated-" + strconv.Itoa(rowIndex)
			rowIndex++
			if _, err := database.Exec(
				`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES(?,?,?,?)`,
				rowID, "user-fabricated", insertType, legacyItemID,
			); err != nil {
				t.Fatalf("insert fabricated starter row %s: %v", rowID, err)
			}
			wantByRowID[rowID] = seed
			fabricatedIDs[legacyItemID] = struct{}{}
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-fabricated/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-fabricated"})
	req.Header.Set("X-User-ID", "user-fabricated")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Items []InventoryItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode inventory payload: %v", err)
	}

	for rowID, seed := range wantByRowID {
		assertStoredInventoryRow(t, database, rowID, seed.ItemType, seed.ItemID)
	}
	for _, item := range payload.Items {
		if _, ok := fabricatedIDs[item.ItemID]; ok {
			t.Fatalf("fabricated starter item id %s should not remain in inventory payload", item.ItemID)
		}
	}
}

func TestGetInventoryNormalizesLegacyStarterShipAliases(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-4")

	// These ids are the OldItemID the client's own ItemIDConversionTable pairs
	// with each starter hull -- the table exists precisely to translate an
	// older build's id to the current one, so they are the only legacy ids
	// that were ever real. The previous version of this test used
	// "16777223"/"Akula_T1"/"Lorica_T1", none of which appear anywhere in the
	// client: the numbers were an ordinal stuffed into the loadout category,
	// "Akula" is a paint and "Lorica" a tier-4 hull.
	assaultSeed := starterShipSeedByName(t, "Agosta")
	sniperSeed := starterShipSeedByName(t, "Rurik")

	for _, row := range []struct {
		id     string
		itemID string
	}{
		{id: "legacy-agosta-id", itemID: "5000235"},
		{id: "legacy-rurik-id", itemID: "5000257"},
	} {
		if _, err := database.Exec(
			`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES(?,?,?,?)`,
			row.id, "user-4", dreadconfig.ItemTypeShip, row.itemID,
		); err != nil {
			t.Fatalf("insert legacy starter ship row %s: %v", row.id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-4/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-4"})
	req.Header.Set("X-User-ID", "user-4")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	assertStoredInventoryRow(t, database, "legacy-agosta-id", assaultSeed.ItemType, assaultSeed.ItemID)
	assertStoredInventoryRow(t, database, "legacy-rurik-id", sniperSeed.ItemType, sniperSeed.ItemID)

	// The other two starter hulls are absent from the conversion table, so they
	// have no legacy id to normalise. Asserting that keeps anyone from
	// "fixing" the gap by inventing one again.
	for _, name := range []string{"Simargl", "Cerberus"} {
		seed := starterShipSeedByName(t, name)
		if aliases := legacyStarterShipItemAliases(seed.ItemID); len(aliases) != 0 {
			t.Errorf("%s (%s) now has legacy aliases %v; assert them directly instead of exempting it", name, seed.ItemID, aliases)
		}
	}
}

func TestGetInventoryNormalizesAssetLinkedStarterRows(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "user-5")

	// Picked out of the starter seeds rather than looked up by display name.
	// Only starter items get an asset-path alias, and display names are not
	// unique in the client's data -- "Beam Amplifier" and "Repeater Turrets"
	// each name several per-hull variants, so a name lookup could just as well
	// return one that is not in the starter inventory at all.
	starterLoadout := starterSeedOfType(t, dreadconfig.ItemTypeLoadout)
	starterAbility := starterSeedOfType(t, dreadconfig.ItemTypeAbility)
	starterWeapon := starterSeedOfType(t, dreadconfig.ItemTypeWeapon)

	for _, row := range []struct {
		id       string
		itemType string
		itemID   string
	}{
		{
			id:       "legacy-agosta-loadout-asset",
			itemType: dreadconfig.ItemTypeLoadout,
			itemID:   starterLoadout.AssetPath,
		},
		{
			id:       "legacy-cerberus-ability-asset",
			itemType: dreadconfig.ItemTypeAbility,
			itemID:   starterAbility.AssetPath,
		},
		{
			id:       "legacy-agosta-weapon-ui-asset",
			itemType: itemTypeLegacyUI,
			itemID:   starterWeapon.AssetPath,
		},
	} {
		if _, err := database.Exec(
			`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES(?,?,?,?)`,
			row.id, "user-5", row.itemType, row.itemID,
		); err != nil {
			t.Fatalf("insert asset-linked starter row %s: %v", row.id, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/dreadnought/player/user-5/inventory", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "user-5"})
	req.Header.Set("X-User-ID", "user-5")
	rec := httptest.NewRecorder()

	handler.GetInventory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	assertStoredInventoryRow(t, database, "legacy-agosta-loadout-asset", dreadconfig.ItemTypeLoadout, starterLoadout.ItemID)
	assertStoredInventoryRow(t, database, "legacy-cerberus-ability-asset", dreadconfig.ItemTypeAbility, starterAbility.ItemID)
	assertStoredInventoryRow(t, database, "legacy-agosta-weapon-ui-asset", dreadconfig.ItemTypeWeapon, starterWeapon.ItemID)
}

// starterSeedOfType returns the first starter inventory seed of a given item
// type, with its asset path populated.
func starterSeedOfType(t *testing.T, itemType string) inventoryBootstrapSeed {
	t.Helper()
	for _, seed := range starterInventoryBootstrapSeeds() {
		if seed.ItemType == itemType && seed.AssetPath != "" {
			return seed
		}
	}
	t.Fatalf("no starter seed of type %s carries an asset path", itemType)
	return inventoryBootstrapSeed{}
}

func starterShipSeedByName(t *testing.T, shipName string) inventoryBootstrapSeed {
	t.Helper()
	item := dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, shipName)
	wantID := strconv.Itoa(int(item.ItemID))
	for _, seed := range starterInventoryBootstrapSeeds() {
		if seed.ItemType == dreadconfig.ItemTypeShip && seed.ItemID == wantID {
			return seed
		}
	}
	t.Fatalf("missing starter ship seed for %s", shipName)
	return inventoryBootstrapSeed{}
}

func assertStoredInventoryRow(t *testing.T, database *sql.DB, rowID string, wantType string, wantItemID string) {
	t.Helper()
	var storedType string
	var storedItemID string
	if err := database.QueryRow(
		`SELECT item_type,item_id FROM player_inventory WHERE id=?`,
		rowID,
	).Scan(&storedType, &storedItemID); err != nil {
		t.Fatalf("query normalized starter row %s: %v", rowID, err)
	}
	if storedType != wantType {
		t.Fatalf("%s stored starter item type = %s, want %s", rowID, storedType, wantType)
	}
	if storedItemID != wantItemID {
		t.Fatalf("%s stored starter item id = %s, want %s", rowID, storedItemID, wantItemID)
	}
}

func seedLegacyProfile(t *testing.T, database *sql.DB, userID string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO player_profiles(id,user_id,display_name) VALUES(?,?,?)`,
		"profile-"+userID, userID, "Player_"+userID,
	); err != nil {
		t.Fatalf("insert legacy profile for %s: %v", userID, err)
	}
	if err := ensurePlayerStatsExec(database, userID); err != nil {
		t.Fatalf("insert legacy stats for %s: %v", userID, err)
	}
}

// TestGetProfileAndInventoryRejectMismatchedCaller is a regression test for
// the IDOR where GetProfile/GetInventory trusted the URL path {id} instead
// of the authenticated caller (X-User-ID, set by jwtMiddleware from the
// verified JWT) — any logged-in player could previously read any other
// player's profile/inventory by substituting a different id in the path.
func TestGetProfileAndInventoryRejectMismatchedCaller(t *testing.T) {
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	}()

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	handler := &Handler{DB: database, Log: logger}
	seedLegacyProfile(t, database, "victim")

	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		path string
	}{
		{"profile", handler.GetProfile, "/v2/dreadnought/player/victim/profile"},
		{"inventory", handler.GetInventory, "/v2/dreadnought/player/victim/inventory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req = mux.SetURLVars(req, map[string]string{"id": "victim"})
			req.Header.Set("X-User-ID", "attacker")
			rec := httptest.NewRecorder()

			tc.call(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (attacker requesting victim's %s should be forbidden)", rec.Code, http.StatusForbidden, tc.name)
			}
		})
	}
}
