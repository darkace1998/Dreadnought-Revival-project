package main

import (
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"sync"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

// Cosmetic ownership: what a player owns, and the form the client checks it in.
//
// Audit 2026-10-06, all from the client's code (verified in the exe):
//
//   - The customizer's entry builder (0x140A9B350) decides "owned" per item
//     with FUN_1405484E0 -- "is this a ship cosmetic?" (decal, pattern,
//     emblem, paint, colour set, mesh part, dirt set). A ship cosmetic is
//     owned iff its id is in the owned-item Items list (+0x39E8, exact match,
//     FUN_140548860) or a loadout holds it; any other item -- the captain's --
//     iff its id is in the list at +0x3F90 (FUN_140548990), filled from
//     YA_GetPlayerPurchases' root "PurchasesData".
//   - Ship cosmetics are PER-SHIP there: the customizer lists them with the
//     ship's EYShipClass in the middle byte, like weapons (inflatedItemID), and
//     writes them so into a loadout's display info (336461851 = 0x140E001B,
//     AssaultMedium); the client's tables only hold the shared 0xFF form.
//
// The server answered with shared ids in Items and NO cosmetics at all in
// PurchasesData (withoutVanity), so in the customizer no captain cosmetic was
// ever owned and no ship cosmetic either -- bought, from a bundle, or a ship's
// own look. And the "free" rule (dreadconfig.VanityItemIsFree: captain heads,
// hair, eyes, skin, base outfits; each hull's default parts) was never applied:
// 67 purchases at 100 GP were for such items, *_Default ones among them.
//
// Now: ownedVanityItemIDs is the shared-form set (bought + free + every owned
// ship's blueprint look); clientVanityItemIDs adds each ship cosmetic's
// per-ship forms for the classes it fits.

var figureheadNamePattern = regexp.MustCompile(`^VAN_FH_(Assault|Dread|Scout|Sniper|Support)([LMH])_`)

// shipVanityClasses is the EYShipClasses a ship cosmetic fits, from its asset
// folder: Heroships|ThemedShips/<Type>/<Size>/... and Patterns/<Type>/<Size>/...
// name one hull line; a figurehead (Heroships/<Type>/Figureheads/...) names its
// size in the asset name. nil: it fits every ship (emblems, paints, decals,
// shared patterns).
func shipVanityClasses(v dreadconfig.VanityItem) []int32 {
	_, rest, ok := strings.Cut(v.File, "/VanityItems/")
	if !ok {
		return nil
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 {
		return nil
	}
	switch parts[0] {
	case "Heroships", "ThemedShips", "Patterns":
	default:
		return nil
	}
	typ := parts[1]
	if typ == "Dread" {
		typ = "Dreadnought"
	}
	if parts[2] == "Figureheads" {
		// One asset per size, the size in its name: VAN_FH_AssaultH_Hermes,
		// VAN_FH_DreadL_Gargoyle ("Dread" for Dreadnought).
		if m := figureheadNamePattern.FindStringSubmatch(v.Name); m != nil {
			t := m[1]
			if t == "Dread" {
				t = "Dreadnought"
			}
			if c, ok := eyShipClassByKey[t+map[string]string{"L": "Light", "M": "Medium", "H": "Heavy"}[m[2]]]; ok {
				return []int32{c}
			}
		}
		return nil
	}
	if c, ok := eyShipClassByKey[typ+parts[2]]; ok {
		return []int32{c}
	}
	return nil
}

// isShipVanityItemID: categories 20-24 (mesh part, emblem, paint, pattern,
// decal).
func isShipVanityItemID(id int32) bool {
	c := (id >> 24) & 0xff
	return c >= 20 && c <= 24
}

// shipVanityClientID is a ship cosmetic's per-ship form for one EYShipClass.
func shipVanityClientID(id, class int32) int32 {
	if !isShipVanityItemID(id) || class < 1 || class > 15 {
		return id
	}
	return id&^0x00ff0000 | class<<16
}

// ownedShipClasses is the EYShipClass of every ship the player owns.
func ownedShipClasses(playerPID string) []int32 {
	lines := map[int32]string{}
	for _, h := range baseShipLoadouts {
		lines[h.loadoutID] = h.hullLine
	}
	for _, h := range heroShipLoadouts {
		lines[h.loadoutID] = h.hullLine
	}
	seen := map[int32]bool{}
	var out []int32
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(playerPID), playerPID) {
		if c, ok := eyShipClassByKey[lines[l.precastLoadoutID]]; ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// freeVanityItemIDs is every cosmetic everyone owns: free (VanityItemIsFree)
// AND player-facing. CHANGED 2026-10-06: the free rule also matched the NPC
// officers' and traders' heads and test hair, which have no name and are in
// no captain template; owned, they were captain-editor options with no head.
func freeVanityItemIDs() []int32 {
	var out []int32
	for _, v := range dreadconfig.VanityItems() {
		if dreadconfig.VanityItemIsFree(v) && dreadconfig.VanityItemIsPlayerFacing(v) {
			out = append(out, v.ItemID)
		}
	}
	return out
}

// isFreeVanityItem reports whether a cosmetic (either form) is free.
func isFreeVanityItem(id int32) bool {
	v, ok := dreadconfig.VanityItemByID(sharedGearID(id))
	return ok && dreadconfig.VanityItemIsFree(v) && dreadconfig.VanityItemIsPlayerFacing(v)
}

// isUnwearableCaptainItem reports a captain item no captain can wear -- a
// captain mesh in no gender's template, or a test/unnamed one. Such an item is never shown
// to the client, even when bought, and what was paid for it is refunded.
func isUnwearableCaptainItem(id int32) bool {
	v, ok := dreadconfig.VanityItemByID(id)
	return ok && v.IsCaptain() && !dreadconfig.VanityItemIsPlayerFacing(v)
}

// shipOwnHullParts is every hull part (category 20) that some ship's
// blueprint wears as its own hull, base or hero: within one hull line each
// ship has its own set (DreadnoughtHeavy: Jutland the hull line's
// "_Default" parts, Monarch the "Tyr" ones; AssaultHeavy: Blud "Marzanna",
// Gora "Lion").
var shipOwnHullParts = sync.OnceValue(func() map[int32]bool {
	out := map[int32]bool{}
	add := func(a dreadconfig.HeroAppearance) {
		for _, id := range a.Items() {
			if (id>>24)&0xff == 20 {
				out[sharedGearID(id)] = true
			}
		}
	}
	for _, b := range baseShipLoadouts {
		if a, ok := dreadconfig.ShipBlueprintAppearance(b.loadoutID); ok {
			add(a)
		}
	}
	for _, h := range heroShipLoadouts {
		if a, ok := dreadconfig.HeroShipAppearance(h.loadoutID); ok {
			add(a)
		}
	}
	return out
})

// heroShipPartIDs maps a hero ship to the hull parts (category 20, shared
// form) it lends to the player's other ships of its line.
//
// The original rule, in Grey Box's own words: hero ships "have custom ship
// parts that are usable on any other ship of the same manufacturer and ship
// class (any tier)" ("Introducing Retrofits", 2018-06-29); the Trident DLC:
// "You can also use the Trident's parts on any of your Jupiter Arms
// dreadnoughts"; the Herja: "the figurehead, forecastle, bridge, hull, and
// stern ... on any of your Jupiter Arms tactical cruisers" (Steam, Outlaw
// Hoard). Until now no hero lent anything: since 2026-10-07 no ship's own
// hull is an owned item, heroes included, so a Trident owner could not put
// a single Trident part on a Jutland.
//
// The parts are every player-facing part in the asset folders of the parts
// the hero wears, so a hero lends the variants it does not wear too (the
// plain forecastle beside its figurehead). The client checks ownership per
// hull LINE (the EYShipClass byte, see ownedVanityItemIDs), and a base line
// is built by one manufacturer (baseShipManufacturerByClassSize), so a hero
// of the line's manufacturer lends to the whole line. A hero of another
// manufacturer (Kore, Oberon, on the Jupiter Arms SniperHeavy line; Outis,
// Jupiter Arms, on the Oberon ScoutMedium line) has no ship to lend to: hero
// ships themselves cannot be customised.
var heroShipPartIDs = sync.OnceValue(func() map[int32][]int32 {
	dir := func(v dreadconfig.VanityItem) string {
		if i := strings.LastIndex(v.File, "/"); i >= 0 {
			return v.File[:i]
		}
		return v.File
	}
	byDir := map[string][]int32{}
	for _, v := range dreadconfig.VanityItems() {
		if v.Category() == 20 && dreadconfig.VanityItemIsPlayerFacing(v) && !dreadconfig.VanityItemIsFree(v) {
			byDir[dir(v)] = append(byDir[dir(v)], v.ItemID)
		}
	}
	out := map[int32][]int32{}
	for _, h := range heroShipLoadouts {
		if h.manufacturer != baseShipManufacturerByClassSize[h.hullLine] {
			continue
		}
		a, ok := dreadconfig.HeroShipAppearance(h.loadoutID)
		if !ok {
			continue
		}
		dirs := map[string]bool{}
		for _, id := range a.Items() {
			if (id>>24)&0xff != 20 {
				continue
			}
			if v, ok := dreadconfig.VanityItemByID(sharedGearID(id)); ok && !dreadconfig.VanityItemIsFree(v) {
				dirs[dir(v)] = true
			}
		}
		seen := map[int32]bool{}
		for d := range dirs {
			for _, id := range byDir[d] {
				if !seen[id] {
					seen[id] = true
					out[h.loadoutID] = append(out[h.loadoutID], id)
				}
			}
		}
		sort.Slice(out[h.loadoutID], func(i, j int) bool { return out[h.loadoutID][i] < out[h.loadoutID][j] })
	}
	return out
})

// isOtherShipsHull reports a hull part that is some ship's own hull and not
// free -- one a player may not take onto another ship of the hull line.
func isOtherShipsHull(id int32) bool {
	id = sharedGearID(id)
	return shipOwnHullParts()[id] && !isFreeVanityItem(id)
}

// ownedVanityItemIDs is every cosmetic the player owns, in the shared form:
// bought (store or bundle), free, and every owned ship's own finish (emblem,
// paint, pattern, decal) as its blueprint names it (base and hero ships).
//
// And an owned HERO ship's hull parts, which it lends to its whole line
// (heroShipPartIDs; changed 2026-10-09).
//
// And an owned REGULAR ship's own hull parts, on its whole line (restored
// 2026-10-09, operator decision). The client can only own a ship cosmetic per
// hull line: the customizer lists a hull line's parts (0x140AA0260: the asset
// table, every blueprint of the line -- heroes included -- and the store, all
// by the EYShipClass byte, never by maker) and marks one usable iff its
// per-line id is in Items (0x140548860; a loadout does not count). From
// 2026-10-07 to 10-09 these were withheld (Monarch's "Tyr" hull on a Jutland
// was reported as another maker's), so a ship's own hull, once swapped away,
// could never be chosen again. The original allowed it: retrofit parts are
// selected "just as you would the bridge, hull, stern or forecastle of
// vessels in your fleet (Hero Ships and regular progression ships)" (Grey
// Box, "Introducing Retrofits"), parts go on ships "of the same manufacturer
// and ship class", and a base hull line is built by one manufacturer
// (baseShipManufacturerByClassSize) -- Jutland and Monarch are both Jupiter
// Arms dreadnoughts. Exactly the parts the blueprint wears; a hero's are
// heroShipPartIDs (a hero of another maker than its line lends none).
func ownedVanityItemIDs(playerPID string) []int32 {
	seen := map[int32]bool{}
	var out []int32
	add := func(id int32) {
		id = sharedGearID(id)
		if id > 0 && isVanityItemID(id) && !seen[id] && !isUnwearableCaptainItem(id) {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range ownedPurchaseItemIDs(playerPID) {
		add(id)
	}
	for _, id := range freeVanityItemIDs() {
		add(id)
	}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(playerPID), playerPID) {
		// An owned hero's parts, for the player's other ships of its line.
		for _, id := range heroShipPartIDs()[l.precastLoadoutID] {
			add(id)
		}
		if a, ok := dreadconfig.ShipBlueprintAppearance(l.precastLoadoutID); ok {
			for _, id := range a.Items() {
				add(id) // hull parts included: see above
			}
		}
		if a, ok := dreadconfig.HeroShipAppearance(l.precastLoadoutID); ok {
			for _, id := range a.Items() {
				if (id>>24)&0xff != 20 { // a hero's parts: heroShipPartIDs
					add(id)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// clientVanityItemIDs is the owned cosmetics in the forms the client checks:
// a captain cosmetic as is; a ship cosmetic in its shared form and its
// per-ship form for every class it fits -- its own hull line, or, for one that
// fits every ship, every class the player owns a ship of.
//
// With frame chunking off (DN_FRAME_CHUNKING=0, a single-frame debug mode)
// the reply must fit the 32 KB receive ring, so only the cosmetics the player
// bought go out, as they did before -- the full set is ~180 ids for any
// player and several times that per ship class.
func clientVanityItemIDs(playerPID string) []int32 {
	if !frameChunkingEnabled() {
		var bought []int32
		for _, id := range ownedPurchaseItemIDs(playerPID) {
			if isVanityItemID(id) {
				bought = append(bought, id)
			}
		}
		return bought
	}
	owned := ownedVanityItemIDs(playerPID)
	classes := ownedShipClasses(playerPID)
	seen := map[int32]bool{}
	var out []int32
	add := func(id int32) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range owned {
		add(id)
		if !isShipVanityItemID(id) {
			continue
		}
		fits := classes
		if v, ok := dreadconfig.VanityItemByID(id); ok {
			if own := shipVanityClasses(v); own != nil {
				fits = own
			}
		}
		for _, c := range fits {
			add(shipVanityClientID(id, c))
		}
	}
	return out
}

// refundFreeCosmeticPurchases gives back what players paid for cosmetics that
// were always meant to be free (VanityItemIsFree): the rule existed but was
// never applied, so the store sold them -- 67 purchases at 100 GP by
// 2026-10-06, default eyes, scars and tints among them. Also for captain items
// no captain can wear (isUnwearableCaptainItem), which the store sold too. Each refunded row is
// kept (the item stays owned; it is free anyway) with its price for the
// record, its currency set to "refunded", which also makes this idempotent.
// Runs at startup. Returns the rows refunded.
func refundFreeCosmeticPurchases(database *sql.DB) int {
	if database == nil {
		return 0
	}
	type row struct {
		pid      string
		item     int32
		currency string
		price    int32
	}
	rows, err := database.Query(`SELECT user_id, item_id, currency, price_paid FROM player_purchases
		WHERE item_type='vanity' AND price_paid > 0 AND currency IN ('SP_regular','SP','gp')`)
	if err != nil {
		logrus.WithError(err).Warn("cosmetics: refund scan failed")
		return 0
	}
	var due []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.pid, &r.item, &r.currency, &r.price) == nil && (isFreeVanityItem(r.item) || isUnwearableCaptainItem(r.item)) {
			due = append(due, r)
		}
	}
	_ = rows.Close()
	// After Close: one database connection.
	refunded := 0
	for _, r := range due {
		tx, err := database.Begin()
		if err != nil {
			continue
		}
		res, err := tx.Exec(`UPDATE player_purchases SET currency='refunded'
			WHERE user_id=? AND item_id=? AND currency=? AND price_paid=?`, r.pid, r.item, r.currency, r.price)
		if n, _ := res.RowsAffected(); err != nil || n != 1 {
			_ = tx.Rollback()
			continue
		}
		if _, err := tx.Exec(`UPDATE player_state SET premium_currency=premium_currency+?, updated_at=datetime('now') WHERE user_id=?`,
			r.price, r.pid); err != nil {
			_ = tx.Rollback()
			continue
		}
		if tx.Commit() == nil {
			refunded++
			logrus.WithFields(logrus.Fields{"player": r.pid, "item": r.item, "gp": r.price}).
				Info("cosmetics: refunded a purchase of a free cosmetic")
		}
	}
	return refunded
}
