package dreadgameconfig

import (
	"regexp"
	"strings"
	"sync"
)

// This file is the single source of truth for what an item is CALLED.
//
// The server's name tables were originally populated from asset FILENAMES,
// which are not display names, and later filled in by hand. Auditing them
// against the client's own data found invented entries: "Leipzig" and "Trieste"
// for the tier-2 medium hulls (the client calls them Trafalgar and Nav),
// "Skagerrak" for hero 67043329 (Huscarl), and the four starter hulls carrying
// their class descriptor ("Assault Medium T1") instead of a name at all.
//
// ItemIDConversionTable is the authority: the client ships it to translate an
// older build's item id to the current one, and every row pairs the current id
// with the name the game displays. Resolving through it means a hand-written
// table can no longer silently disagree with the game.
var (
	// Asset paths encode class, size and tier, so the ship -> loadout mapping is
	// derived rather than hand-written -- a hand-written one is what routed
	// ships to Development blueprints:
	//
	//	ship:    /Game/Generic/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP
	//	loadout: /Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP
	//
	// The tier appears in up to three places and one hull puts it in an odd one:
	// AssaultLight T5 is VH_AssaultLight_PrecastLoadout_T5_BP, with the suffix
	// after "PrecastLoadout" instead of before it. It is the only player-facing
	// loadout that does, and matching only the usual shape silently dropped it
	// -- 66 hulls resolved instead of 67, so Brutus had no name and no
	// pawn -> loadout mapping. Every other odd path under /Precast/ is Havoc,
	// PVE, Special, TM or Tutorial content, which stays excluded on purpose.
	shipAssetPathPattern    = regexp.MustCompile(`^/Game/Generic/Ships/([A-Za-z]+)/([A-Za-z]+)/T(\d)/`)
	precastAssetPathPattern = regexp.MustCompile(`^/Game/Generic/Loadouts/Precast/(?:T(\d)/)?VH_([A-Za-z]+)_(?:T(\d)_)?PrecastLoadout(?:_T(\d))?_BP$`)

	// These caches are rebuilt while they are still empty rather than being
	// filled exactly once. The tables they read (ItemIDTable, the item catalog,
	// ItemIDConversionTable) are mutable package globals loaded at init and
	// reloadable afterwards, so a first call that lands before or between loads
	// sees nothing -- and a sync.Once would then cache that emptiness forever,
	// silently turning every name lookup into a miss. An empty index is never a
	// legitimate answer here, so treat it as "not built yet".
	nameCacheMu sync.Mutex

	precastLoadoutByKey      map[string]int32
	authoritativeNameByID    map[int32]string
	authoritativeNameByAsset map[string]string
	hullNameByID             map[int32]string
	hullDescriptionByID      map[int32]string
)

// ensurePrecastLoadoutIndex builds the ship -> loadout index if it is missing.
func ensurePrecastLoadoutIndex() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(precastLoadoutByKey) == 0 {
		buildPrecastLoadoutIndex()
	}
}

// ensureAuthoritativeNames builds the name indexes if they are missing.
func ensureAuthoritativeNames() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(authoritativeNameByID) == 0 {
		buildAuthoritativeNames()
	}
}

// buildPrecastLoadoutIndex indexes the player-facing precast loadouts by
// "<Class><Size>|<tier>". It deliberately matches only loadouts sitting directly
// under /Loadouts/Precast/, which excludes the Havoc, AI-boss and Development
// variants that share the category.
func buildPrecastLoadoutIndex() {
	precastLoadoutByKey = map[string]int32{}
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := ItemByID(itemID)
			if !ok {
				continue
			}
			match := precastAssetPathPattern.FindStringSubmatch(item.AssetPath)
			if match == nil {
				continue
			}
			tier := match[1]
			if tier == "" {
				tier = match[3]
			}
			if tier == "" {
				tier = match[4]
			}
			precastLoadoutByKey[match[2]+"|"+tier] = itemID
		}
	}
}

// PrecastLoadoutIDForShip returns the precast-loadout id representing a ship
// pawn, derived from the two assets' paths.
//
// This matters beyond naming: the client's tech-tree gate compares the top byte
// of an id -- its ItemIDTable category -- against YShipLoadoutPrecast (1) and
// YShipLoadoutHero (3), so a pawn id (category 10) is always rejected. Rows and
// lookups have to use the loadout id.
func PrecastLoadoutIDForShip(shipID int32) (int32, bool) {
	ensurePrecastLoadoutIndex()

	item, ok := ItemByID(shipID)
	if !ok {
		return 0, false
	}
	match := shipAssetPathPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return 0, false
	}
	id, ok := precastLoadoutByKey[match[1]+match[2]+"|"+match[3]]
	return id, ok
}

// normalizeAuthoritativeName trims the padding the shipped table carries,
// including the non-breaking spaces in entries like "Lorica ".
func normalizeAuthoritativeName(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(name, " ", " "))
}

func buildAuthoritativeNames() {
	authoritativeNameByID = map[int32]string{}
	authoritativeNameByAsset = map[string]string{}
	for _, entry := range GetAllItemIDConversionEntries() {
		name := normalizeAuthoritativeName(entry.Name)
		if name == "" {
			continue
		}
		id := int32(entry.NewItemID)
		if _, seen := authoritativeNameByID[id]; !seen {
			authoritativeNameByID[id] = name
		}
		// The table's Asset holds the default-object reference
		// ("<path>.Default__<name>_C"); the registry uses the bare path.
		asset := entry.Asset
		if dot := strings.Index(asset, "."); dot >= 0 {
			asset = asset[:dot]
		}
		if asset == "" {
			continue
		}
		if _, seen := authoritativeNameByAsset[asset]; !seen {
			authoritativeNameByAsset[asset] = name
		}
	}
	applyHullNameOverrides()
}

// applyHullNameOverrides lets the client's own precast loadout blueprints win
// over ItemIDConversionTable for player hulls.
//
// Applied AFTER the table on purpose: the table stays the authority for
// everything it is right about (which is most of it), and this corrects only the
// hulls it names from the previous build. See hull_names.go for the mechanism
// and the four hulls it affects.
//
// The join is the asset path, which both sides carry, so no id mapping has to be
// invented. Ids come from the item catalog rather than from the conversion
// table, which also picks up VH_DreadnoughtMedium_T1 (Simargl) -- it has no
// conversion row at all, so the table could never have named it.
func applyHullNameOverrides() {
	if len(GetAllHullNames()) == 0 {
		return
	}
	for _, hull := range GetAllHullNames() {
		authoritativeNameByAsset[hull.Asset] = hull.Name
	}
}

// ensureHullNameIndex builds the id -> hull name index if it is missing.
//
// Separate from buildAuthoritativeNames, and rebuilt while empty rather than
// once, for the reason the comment on nameCacheMu already gives: this one needs
// the ITEM CATALOG, and the catalog is not necessarily loaded when the name
// tables are first built. Doing it inside buildAuthoritativeNames looked right
// and silently produced nothing -- ItemByID answered "not found" for all 259
// precast items at that point, so the four corrected hulls kept their legacy
// names and the cache, being non-empty, was never rebuilt.
func ensureHullNameIndex() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(hullNameByID) > 0 {
		return
	}
	if len(GetAllHullNames()) == 0 {
		return
	}
	index := map[int32]string{}
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := ItemByID(itemID)
			if !ok {
				continue
			}
			if name, ok := hullNameForAsset(item.AssetPath); ok {
				index[itemID] = name
			}
		}
	}
	hullNameByID = index
}

// AuthoritativeItemName returns the name the client displays for an item id.
//
// Hull names read from the client's own precast loadout blueprints win over
// ItemIDConversionTable, whose Name column carries the previous build's name for
// any hull that was renamed. See hull_names.go.
func AuthoritativeItemName(itemID int32) (string, bool) {
	ensureHullNameIndex()
	nameCacheMu.Lock()
	if name, ok := hullNameByID[itemID]; ok {
		nameCacheMu.Unlock()
		return name, true
	}
	nameCacheMu.Unlock()

	ensureAuthoritativeNames()
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	name, ok := authoritativeNameByID[itemID]
	return name, ok
}

// AuthoritativeShipName is the display name for a ship.
//
// Ship PAWN ids carry no name of their own -- the name belongs to the precast
// loadout representing the ship, which is why the pawn seeds ended up with
// placeholders. So this resolves the loadout first and falls back to the id's
// own entry, which is what covers hero loadouts.
func AuthoritativeShipName(shipID int32) (string, bool) {
	if loadoutID, ok := PrecastLoadoutIDForShip(shipID); ok {
		if name, ok := AuthoritativeItemName(loadoutID); ok {
			return name, true
		}
	}
	return AuthoritativeItemName(shipID)
}

// AuthoritativeNameForAssetPath resolves a display name straight from an asset
// path, for callers that have a path but no id yet. Ship pawn paths resolve
// through the precast loadout that names the ship.
func AuthoritativeNameForAssetPath(assetPath string) (string, bool) {
	if assetPath == "" {
		return "", false
	}
	ensureAuthoritativeNames()
	if name, ok := authoritativeNameByAsset[assetPath]; ok {
		return name, true
	}
	if match := shipAssetPathPattern.FindStringSubmatch(assetPath); match != nil {
		ensurePrecastLoadoutIndex()
		if id, ok := precastLoadoutByKey[match[1]+match[2]+"|"+match[3]]; ok {
			return AuthoritativeItemName(id)
		}
	}
	return "", false
}

// ShipIDForPrecastLoadout is the inverse of PrecastLoadoutIDForShip: given a
// precast-loadout id it returns the ship pawn that loadout represents.
//
// Needed because ownership arrives as loadout ids -- a tech tree unlock and a
// fleet entry both name the loadout (category 1/3), never the pawn (category
// 10) -- while a ship row still has to carry the pawn's identity.
func ShipIDForPrecastLoadout(precastLoadoutID int32) (int32, bool) {
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YPawn" {
			continue
		}
		for _, shipID := range category.ItemIDs {
			if id, ok := PrecastLoadoutIDForShip(shipID); ok && id == precastLoadoutID {
				return shipID, true
			}
		}
	}
	return 0, false
}
