package dreadgameconfig

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Hero ship appearance, from the client's own hero-loadout blueprints.
//
// A hero ship IS its look: every *_HeroLoadout_BP carries an m_appereance
// (FYShipAppeareaceContainer, the game's spelling) naming its four hull mesh
// parts, emblem, paint, pattern and decal -- for the Hermes,
// VAN_H_ScoutL_{Forecastle_FH,Bridge,Hull,Stern}_Hermes_DA, VAN_PN_Hermes_DA
// and VAN_DCL_Hermes_DA. The server sent every hero its HULL LINE's default
// appearance instead, so an owned hero rendered as the plain base ship
// (operator, 2026-10-01).
//
// All 48 blueprints name the parts, paint and decal; two name no emblem or
// pattern, and those slots keep the hull line's default.

// HeroAppearance is a hero ship's own appearance, as item ids.
type HeroAppearance struct {
	MeshParts []int32
	Emblem    int32
	Paint     int32
	Pattern   int32
	Decal     int32
}

// Items lists every cosmetic id of the appearance.
func (a HeroAppearance) Items() []int32 {
	out := append([]int32(nil), a.MeshParts...)
	for _, id := range []int32{a.Emblem, a.Paint, a.Pattern, a.Decal} {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}

var (
	heroAppearanceOnce sync.Once
	heroAppearances    map[int32]HeroAppearance
)

func loadHeroAppearances() {
	heroAppearances = loadBlueprintAppearances("HeroLoadouts_cooked.jsonl")
}

// loadBlueprintAppearances reads the m_appereance of every loadout blueprint
// in one of the cooked loadout dumps, keyed by the blueprint's item id.
func loadBlueprintAppearances(file string) map[int32]HeroAppearance {
	out := map[int32]HeroAppearance{}
	f, err := os.Open(filepath.Join(LoadoutsDir(), file))
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	resolve := func(path string) int32 {
		if path == "" {
			return 0
		}
		// "/Game/X/Y.Y" -> the registered asset path.
		if item, ok := ItemByAssetPath(path); ok {
			return item.ItemID
		}
		if item, ok := ItemByAssetPath(strings.SplitN(path, ".", 2)[0]); ok {
			return item.ItemID
		}
		return 0
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			SystemData struct {
				ItemID int32 `json:"m_itemID"`
			} `json:"m_itemSystemData"`
			Appearance struct {
				Parts   []string `json:"m_heroShipParts"`
				Emblem  string   `json:"m_emblem"`
				Paint   string   `json:"m_paint"`
				Pattern string   `json:"m_pattern"`
				Decal   string   `json:"m_decal"`
			} `json:"m_appereance"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.SystemData.ItemID == 0 {
			continue
		}
		a := HeroAppearance{
			Emblem:  resolve(row.Appearance.Emblem),
			Paint:   resolve(row.Appearance.Paint),
			Pattern: resolve(row.Appearance.Pattern),
			Decal:   resolve(row.Appearance.Decal),
		}
		for _, p := range row.Appearance.Parts {
			if id := resolve(p); id != 0 {
				a.MeshParts = append(a.MeshParts, id)
			}
		}
		if len(a.MeshParts) > 0 || a.Paint != 0 {
			out[row.SystemData.ItemID] = a
		}
	}
	return out
}

// Base ships have a look of their own too. A precast (base) loadout
// blueprint's m_appereance names its hull parts and paint, and from tier III
// up they are rarely the hull line's generic default: Onager (33489292) is
// built from the VAN_H_SniperH_Kore_* parts, Chernobog (33489274) from
// VAN_H_DreadM_*_Ravens_*, Aion (33489297) from VAN_H_SupportM_*_Bix_*,
// Valcour (33489290) from its own VAN_H_ScoutL_*_Valcour_* -- asset folders
// named after hero ships, but these are the base ships' own parts. 68 of the
// 102 precast blueprints name parts. Owning the ship owns its look: the
// loadout ownership check flagged honest players wearing exactly these
// (2026-10-03, every "unowned cosmetic" warning of the day).
var (
	precastAppearanceOnce sync.Once
	precastAppearances    map[int32]HeroAppearance
)

// ShipBlueprintAppearance is a base (precast) loadout's own appearance, as
// its blueprint names it.
func ShipBlueprintAppearance(loadoutID int32) (HeroAppearance, bool) {
	precastAppearanceOnce.Do(func() { precastAppearances = loadBlueprintAppearances("PrecastLoadouts_cooked.jsonl") })
	a, ok := precastAppearances[loadoutID]
	return a, ok
}

// HeroShipAppearance is a hero loadout's own appearance.
func HeroShipAppearance(loadoutID int32) (HeroAppearance, bool) {
	heroAppearanceOnce.Do(loadHeroAppearances)
	a, ok := heroAppearances[loadoutID]
	return a, ok
}

// HeroShipDisplayInfo is a hero's m_displayInfo ("m#m#m#m;emblem;paint;
// pattern;decal"), its own appearance with any slot it does not name taken
// from the hull line's default.
func HeroShipDisplayInfo(loadoutID int32, hullLine, manufacturer string) (string, bool) {
	a, ok := HeroShipAppearance(loadoutID)
	if !ok {
		return "", false
	}
	return appearanceDisplayInfo(a, hullLine, manufacturer), true
}

// ShipBlueprintDisplayInfo is a base ship's own look as a display info, the
// same way: its blueprint's parts and finish, any slot it does not name from
// the hull line's default. Until 2026-10-09 a base ship nobody had customised
// was sent its hull line's default instead -- a Trafalgar without its Wedge
// parts, a Nav without its own forecastle and bridge -- and the client saved
// that back on the first edit (122 of 152 customised base ships with a look
// of their own had lost it).
func ShipBlueprintDisplayInfo(loadoutID int32, hullLine, manufacturer string) (string, bool) {
	a, ok := ShipBlueprintAppearance(loadoutID)
	if !ok {
		return "", false
	}
	return appearanceDisplayInfo(a, hullLine, manufacturer), true
}

// appearanceDisplayInfo writes an appearance as "m#m#m#m;emblem;paint;
// pattern;decal", any slot it does not name from the hull line's default.
func appearanceDisplayInfo(a HeroAppearance, hullLine, manufacturer string) string {
	emblem, pattern, decal := DefaultShipVanityItemIDs(hullLine)
	pick := func(own, fallback int32) string {
		if own != 0 {
			return itoa(own)
		}
		if fallback != 0 {
			return itoa(fallback)
		}
		return VanityUnsetSlot
	}
	mesh := make([]string, 0, meshSlotCount)
	for _, id := range a.MeshParts {
		if len(mesh) < meshSlotCount {
			mesh = append(mesh, itoa(id))
		}
	}
	defaults := DefaultShipMeshPartIDs(hullLine)
	for i := len(mesh); i < meshSlotCount; i++ {
		if i < len(defaults) {
			mesh = append(mesh, itoa(defaults[i]))
		} else {
			mesh = append(mesh, VanityUnsetSlot)
		}
	}
	return strings.Join([]string{
		strings.Join(mesh, "#"),
		pick(a.Emblem, emblem),
		pick(a.Paint, DefaultShipPaintID(manufacturer)),
		pick(a.Pattern, pattern),
		pick(a.Decal, decal),
	}, ";")
}
