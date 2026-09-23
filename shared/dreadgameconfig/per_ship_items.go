package dreadgameconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// What each ship researches, read out of the client's module preview table.
//
// datatables/UI/Module_data_table_v01.json (row struct YS_ModuleVideoTable,
// read by UI_Screen_ModuleDetails.SetupVideoAndStill) has one row per
// secondary weapon and module AS FITTED TO A SHIP CLASS: every key is a
// per-ship ("inflated") id -- the shared id with its middle byte set to the
// EYShipClass (see inflatedItemID in mmogbrain) -- and every row is named after
// the hulls that use that variant and its tier:
//
//	68026398  Agosta Tempest Missiles N               AssaultMedium, T0
//	68026399  Agosta Trafalgar Tempest Missiles I     AssaultMedium, T1
//	68026413  Trafalgar Goliath Torpedo II            AssaultMedium, T2
//
// Measured over all 51 base hulls on 2026-09-23, with no exception: a tier-t
// hull FITS exactly five rows of its class at tier t-1 (its secondary weapon and
// four modules -- the cooked blueprint's defaults), and every row of its class
// at tier t names it: those are what it RESEARCHES, and they become the next
// hull's defaults ("Agosta Trafalgar ... I" is researched on Agosta and fitted
// on Trafalgar). The roman numeral agrees with the asset path's /T<n>/ in 822 of
// 822 rows where both exist; 408 rows sit on untiered legacy paths, which is why
// the numeral is the tier source here.
//
// This replaces a research list the server used to COMPOSE from sibling asset
// lines, which offered ids the game never did and nothing else in the client
// knew: Trafalgar's Plasma Ram II and Energy Generator II, reported broken live
// on 2026-09-23 while its other modules worked. Plasma Ram exists in this table
// from III up; those T2 ids appear only in LoadoutDevelopmentTable (a dev
// blueprint and Havoc-mode "Modified Trafalgar"). It also gave Trafalgar 4 of
// its 9 and the tier-1 starters none of their 5.
//
// Primary weapons have no rows here and none in the store's Weapons bucket
// either: the game did not research them per ship.

// PerShipResearchItem is one row of a ship's research list.
type PerShipResearchItem struct {
	ID   int32 // per-ship id, as the client keys it
	Tier int32
	Name string // the table's itemName, for logs and tests
}

// Guarded by a mutex and rebuilt while incomplete rather than built once: the
// asset-path fallback in rowTier needs the item catalog, and a first call that
// lands before the catalog is loaded would otherwise cache a table missing
// those rows forever (the trap described on nameCacheMu).
var (
	perShipResearchMu         sync.Mutex
	perShipResearch           map[[2]int32][]PerShipResearchItem // {EYShipClass, tier}
	perShipResearchUnresolved = -1                               // -1: not built yet
	perShipResearchHadCatalog bool

	romanTier = map[string]int32{"N": 0, "I": 1, "II": 2, "III": 3, "IV": 4, "V": 5}
	pathTier  = regexp.MustCompile(`/T(\d)/`)
)

func loadPerShipResearch() {
	perShipResearch = map[[2]int32][]PerShipResearchItem{}
	perShipResearchUnresolved = 0
	perShipResearchHadCatalog = len(GetAllCategories()) > 0
	raw, err := os.ReadFile(DataTablePath(filepath.Join("UI", "Module_data_table_v01.json")))
	if err != nil {
		return
	}
	var table struct {
		Rows map[string]struct {
			ItemName string `json:"itemName"`
		} `json:"rows"`
	}
	if json.Unmarshal(raw, &table) != nil {
		return
	}
	for key, row := range table.Rows {
		id64, err := strconv.ParseInt(key, 10, 32)
		if err != nil {
			continue
		}
		id := int32(id64)
		shipClass := (id >> 16) & 0xff
		if shipClass < 1 || shipClass > 15 {
			continue
		}
		tier, ok := rowTier(id, row.ItemName)
		if !ok {
			perShipResearchUnresolved++
			continue
		}
		k := [2]int32{shipClass, tier}
		perShipResearch[k] = append(perShipResearch[k], PerShipResearchItem{ID: id, Tier: tier, Name: row.ItemName})
	}
	for _, items := range perShipResearch {
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	}
}

// rowTier reads the tier from the name's roman numeral. Seven rows carry a
// suffix after it ("... Plasma Turrets III PAT") and one none at all
// ("Svarog Anti-Nuke Lasers"); those fall back to the asset path.
func rowTier(id int32, name string) (int32, bool) {
	words := strings.Fields(name)
	for i := len(words) - 1; i >= 0 && i >= len(words)-2; i-- {
		if tier, ok := romanTier[words[i]]; ok {
			return tier, true
		}
	}
	if item, ok := ItemByID(id&^0x00ff0000 | 0x00ff0000); ok {
		if m := pathTier.FindStringSubmatch(item.AssetPath); m != nil {
			tier, _ := strconv.Atoi(m[1])
			return int32(tier), true
		}
	}
	return 0, false
}

// ShipResearchItems is what a hull of this EYShipClass and tier researches:
// every preview-table row of its class at its own tier. Nil when the table is
// missing.
func ShipResearchItems(shipClass, tier int32) []PerShipResearchItem {
	perShipResearchMu.Lock()
	defer perShipResearchMu.Unlock()
	// Rebuild only while the result could still change: never built, or built
	// before the catalog existed with rows left unresolved. A row that stays
	// unresolvable WITH the catalog is not retried on every call.
	if perShipResearchUnresolved < 0 || (perShipResearchUnresolved > 0 && !perShipResearchHadCatalog) {
		loadPerShipResearch()
	}
	return perShipResearch[[2]int32{shipClass, tier}]
}
