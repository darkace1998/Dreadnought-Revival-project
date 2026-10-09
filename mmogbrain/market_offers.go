package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Market offers built from the original game's documented store: hero ships
// and bundles (2026-10-01). Sources: the Steam DLC pages of the bundles (exact
// contents and euro prices), the Steam forum (coating price, hero ship cost),
// PlayStation Store GP packs. Collected in memory note
// dreadnought-original-market-data.
//
// The client sorts the Market by EYMarketItemPromotionFlags (SDK): Featured 0,
// Spotlight 1, OnSale 2, Recommended 3, Popular 4, ShipVanity 5,
// CaptainVanity 6, HavocReward 7.
const promotionFlagFeatured = 1 << 0

// promotionFlagNames are the names the client's PromotionFlagSet parser
// (0x142a62a50) accepts, by bit. Ship and captain vanity (bits 5-6) have no
// name there: the client derives them from the items an offer references.
var promotionFlagNames = []struct {
	bit  int
	name string
}{{1 << 0, "featured"}, {1 << 1, "spotlight"}, {1 << 2, "onsale"}, {1 << 3, "recommended"}, {1 << 4, "popular"}}

func promotionFlagSetNames(flags int) []any {
	names := []any{}
	for _, f := range promotionFlagNames {
		if flags&f.bit != 0 {
			names = append(names, f.name)
		}
	}
	return names
}

// heroPriceGP is a hero ship's price in GP by tier. GUESS: the one surviving
// figure is "$30" to "forty dollars" for a tier-IV hero, which is ~2,100 to
// ~4,200 GP at the GP-pack rates (500 GP $6.99 ... 14,000 GP $133.49); T2/T3
// scale down from that.
func heroPriceGP(tier int32) int32 {
	switch {
	case tier <= 2:
		return 1500
	case tier == 3:
		return 2500
	default:
		return 3500
	}
}

func heroByID(id int32) (heroShipLoadout, bool) {
	for _, h := range heroShipLoadouts {
		if h.loadoutID == id {
			return h, true
		}
	}
	return heroShipLoadout{}, false
}

// heroCatalogSeeds offers every hero ship in the Market, for GP. There were
// no hero ship offers at all: the retail "Heroships" bucket is synthetic ids
// with no items behind them. Each offer references its hero (ItemIDs), as the
// client derives an offer's section from what it references, and is a hero
// ship (IsHeroShip). Buying one goes through buildMmogPurchasePayload's hero
// path, which charges this price and grants the ship.
func heroCatalogSeeds(playerID string) []gatewayCatalogEntitySeed {
	owned := map[int32]bool{}
	if playerID != "" {
		for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(playerID), playerID) {
			owned[l.precastLoadoutID] = true
		}
	}
	seeds := make([]gatewayCatalogEntitySeed, 0, len(heroShipLoadouts))
	for _, h := range heroShipLoadouts {
		seed := gatewayCatalogEntitySeed{
			itemID:          h.loadoutID,
			externalID:      "999" + strconv.Itoa(int(h.loadoutID)),
			displayName:     h.name,
			entityType:      "item",
			itemType:        "loadout",
			manufacturer:    h.manufacturer,
			loadoutID:       h.loadoutID,
			priceCurrencyID: vanityCurrency,
			priceAmount:     heroPriceGP(h.tier),
			quantity:        1,
			owned:           owned[h.loadoutID],
			heroShip:        true,
			offerItemIDs:    []int32{h.loadoutID},
		}
		text, _ := dreadconfig.HeroMarketData(h.loadoutID)
		seed.localizedName = text.Name
		if len(seed.localizedName) == 0 {
			// Nine heroes have headlines the client never localized; the
			// game showed the blueprint's own text, which is the name.
			seed.localizedName = dreadconfig.Localized{"en": h.name}
		}
		seed.localizedDescription = text.Description
		if seed.localizedDescription["en"] == "" {
			// No English description of its own (a few heroes have one in
			// every other language only): its class line ("Corvette"),
			// which the client falls back to for any locale it lacks.
			if len(seed.localizedDescription) == 0 {
				seed.localizedDescription = text.Subline
			} else if en := text.Subline["en"]; en != "" {
				seed.localizedDescription = withEnglish(seed.localizedDescription, en)
			}
		}
		seed.description = seed.localizedDescription["en"]
		seed.image = text.Image
		seeds = append(seeds, seed)
	}
	return seeds
}

// marketBundle is one of the original bundles. heroes and vanity are names in
// the client's own data (hero loadout names, cosmetic asset names); an item
// the client data does not contain is left out rather than substituted.
type marketBundle struct {
	id          int32
	name        string
	description string
	// featured puts the bundle in the Market's Featured section too. That
	// section shows at most four offers (operator, 2026-10-01); the rest are
	// in Bundles only.
	featured bool
	// descriptionText names a localized description the client still
	// carries (dreadconfig.MarketString); description is then its English.
	descriptionText string
	priceEUR        float64 // the original real-money price
	credits         int32
	eliteDays       int32
	heroes          []string
	vanity          []string // exact cosmetic names
	vanityLike      []string // every cosmetic whose name contains this
}

// marketBundleIDBase keeps bundle ids in top byte 0, which claims no item
// category (see realCatalogBucketIDBase).
const marketBundleIDBase = 9200000

// marketBundles are the documented bundles with a known price. Their GP
// component is NOT granted: a bundle is bought with GP here (there is no real
// money), and Battle Ready: Recruit granted more GP (2,500) than it would
// cost, which would mint GP. Missing from the client's data, so absent:
// Keep 'em Flying decals (Rogue Cache), Hazmat Mask (Renegade Stash), Blaze
// coatings, Protector decals, Lion's Mane figurehead, Gearhead Worksuit,
// Good Fortune Cap, Vitra retrofits (Outlaw Hoard), Shugyosha outfit
// (Recruit), Smuggler Cartel outfit and Harvest tint (Veteran), Mercenary
// emblem and coating.
var marketBundles = []marketBundle{
	{id: marketBundleIDBase + 1, name: "Rogue Cache", priceEUR: 14.99, credits: 15000, eliteDays: 7,
		description: "Hanuman Hero Ship (Tier-III Oberon dreadnought), Colors of Mars coating, 15,000 Credits, 7 days of Elite Status.",
		heroes:      []string{"Hanuman"}, vanity: []string{"VAN_PN_ColorsOfMars_DA"}},
	{id: marketBundleIDBase + 2, name: "Renegade Stash", priceEUR: 39.99, credits: 50000, eliteDays: 30,
		description: "Zaratan Hero Ship (Tier-IV Oberon destroyer), Venomous decal, coating and holographic emblem, 50,000 Credits, 30 days of Elite Status.",
		heroes:      []string{"Zaratan"}, vanity: []string{"VAN_DCL_Venomous_DA", "VAN_PN_Venomous_DA", "VAN_EMB_Ho_Venomousl_DA"}},
	{id: marketBundleIDBase + 3, name: "Outlaw Hoard", featured: true, priceEUR: 69.99, credits: 200000, eliteDays: 180,
		description: "Herja Hero Ship (Tier-III Jupiter Arms tactical cruiser), Trident Hero Ship (Tier-IV Jupiter Arms dreadnought), 200,000 Credits, 180 days of Elite Status.",
		heroes:      []string{"Herja", "Trident"}},
	{id: marketBundleIDBase + 4, name: "Battle Ready: Recruit", featured: true, priceEUR: 20.99, credits: 110000, eliteDays: 90,
		description: "Marauder Hero Ship, 110,000 Credits, 90 days of Elite Status.",
		heroes:      []string{"Marauder"}},
	{id: marketBundleIDBase + 5, name: "Battle Ready: Veteran", featured: true, priceEUR: 41.99, credits: 220000, eliteDays: 180,
		description: "Hermes Hero Ship with its ship vanity and figureheads, Laser Shades, 220,000 Credits, 180 days of Elite Status.",
		heroes:      []string{"Hermes"}, vanity: []string{"CH_Attachment_LaserShades", "CH_Attachment_LaserShades_F"}, vanityLike: []string{"Hermes"}},
	{id: marketBundleIDBase + 6, name: "Mercenary Pack", priceEUR: 39.99, eliteDays: 30,
		description: "Seven Hero Ships -- Morningstar, PCF Morningstar, Silesia, PCF Silesia, Outis, Huscarl, Kali -- Mercenary decals, 30 days of Elite Status.",
		heroes:      []string{"Morningstar", "PCF Morningstar", "Silesia", "PCF Silesia", "Outis", "Huscarl", "Kali"},
		vanity:      []string{"VAN_DCL_Mercenary_DA", "VAN_DCL_Mercenary_Dark_DA"}},
	// The one bundle whose text survives in the client's own localization
	// (key C0E97B7A405588EF5A18D0BAFD3D5349, all seven languages).
	// GUESS: the price -- no source gives it; the cheapest documented bundle
	// tier is used.
	{id: marketBundleIDBase + 7, name: "Vanguard Bundle", featured: true, priceEUR: 4.99, credits: 10000, eliteDays: 7,
		descriptionText: "VanguardBundleDescription",
		description:     "Kick-start your career as a mercenary captain. Progress faster with 7 days of Elite Status, command the powerful PCF Silesia Hero Ship, and get 10,000 Credits to spend within your ships' tech trees! The Vanguard Bundle contains everything a new recruit needs to become a legend.",
		heroes:          []string{"PCF Silesia"}},
}

// priceGP is the bundle's GP price. GUESS: the original euro price at 100 GP
// per euro, the rate of the larger GP packs (14,000 GP for $133.49).
func (b marketBundle) priceGP() int32 { return int32(b.priceEUR*100 + 0.5) }

// contents resolves a bundle's heroes and cosmetics to item ids.
func (b marketBundle) contents() (heroes, vanity []int32) {
	for _, name := range b.heroes {
		for _, h := range heroShipLoadouts {
			if h.name == name {
				heroes = append(heroes, h.loadoutID)
				break
			}
		}
	}
	seen := map[int32]bool{}
	for _, v := range dreadconfig.VanityItems() {
		match := false
		for _, n := range b.vanity {
			match = match || v.Name == n
		}
		for _, n := range b.vanityLike {
			match = match || strings.Contains(v.Name, n)
		}
		if match && !seen[v.ItemID] {
			seen[v.ItemID] = true
			vanity = append(vanity, v.ItemID)
		}
	}
	return heroes, vanity
}

func marketBundleByID(id int32) (marketBundle, bool) {
	for _, b := range marketBundles {
		if b.id == id {
			return b, true
		}
	}
	return marketBundle{}, false
}

// marketBundleCatalogSeeds lists the bundles, Featured, referencing their
// contents (ItemIDs) so the client can show what is inside.
func marketBundleCatalogSeeds() []gatewayCatalogEntitySeed {
	seeds := make([]gatewayCatalogEntitySeed, 0, len(marketBundles))
	for _, b := range marketBundles {
		heroes, vanity := b.contents()
		seed := gatewayCatalogEntitySeed{
			itemID: b.id,
			// The bundle's own id, NOT "999"+id: the client takes the offer
			// id from entity_id, which for a bundle must equal external_id
			// (gatewayBundleListing), and parses it as an int32. "9999200001"
			// does not fit: all seven bundles became the one offer
			// "999-2147483648", whose items could not be found (operator log,
			// 2026-10-01).
			externalID:      strconv.Itoa(int(b.id)),
			providedCredits: b.credits,
			displayName:     b.name,
			description:     b.description,
			entityType:      "bundle",
			itemType:        "bundle",
			priceCurrencyID: vanityCurrency,
			priceAmount:     b.priceGP(),
			quantity:        1,
			offerItemIDs:    append(heroes, vanity...),
			// The original names were English everywhere; descriptions are
			// localized where the client still has the text.
			localizedName:        dreadconfig.Localized{"en": b.name},
			localizedDescription: dreadconfig.Localized{"en": b.description},
		}
		if b.featured {
			seed.promotionFlags = promotionFlagFeatured
		}
		if text, ok := dreadconfig.MarketString(b.descriptionText); ok {
			seed.localizedDescription = text
		}
		// No bundle art survives in the client; a bundle shows its first
		// hero ship's picture.
		for _, id := range heroes {
			if text, ok := dreadconfig.HeroMarketData(id); ok && text.Image != "" {
				seed.image = text.Image
				break
			}
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

// grantMarketBundle charges a bundle's GP price and grants what is in it:
// its hero ships (with their loadouts), its cosmetics, its credits and its
// Elite days. Items the player already has are skipped; the rest of the
// bundle is still granted. Returns the heroes newly granted, for the caller
// to push to the client.
func grantMarketBundle(database *sql.DB, pid string, b marketBundle) (charged int32, newHeroes []int32, reason string) {
	heroes, vanity := b.contents()
	owned := map[int32]bool{}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		owned[l.precastLoadoutID] = true
	}
	for _, id := range ownedPurchaseItemIDs(pid) {
		owned[id] = true
	}
	price := b.priceGP()
	tx, err := database.Begin()
	if err != nil {
		return 0, nil, "database unavailable"
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE player_state SET premium_currency=premium_currency-?, soft_currency=soft_currency+?,
		updated_at=datetime('now') WHERE user_id=? AND premium_currency>=?`, price, b.credits, pid, price)
	if err != nil {
		return 0, nil, "currency deduction failed"
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil, "insufficient premium currency"
	}
	for _, id := range heroes {
		if owned[id] {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'loadout',0,'bundle')`, pid, id); err != nil {
			return 0, nil, "purchase record failed"
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			return 0, nil, "ship grant failed"
		}
		newHeroes = append(newHeroes, id)
	}
	for _, id := range vanity {
		if owned[id] {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'vanity',0,'bundle')`, pid, id); err != nil {
			return 0, nil, "purchase record failed"
		}
	}
	if b.eliteDays > 0 {
		if _, err := extendMembershipTx(tx, pid, b.eliteDays, int32(time.Now().Unix())); err != nil {
			return 0, nil, "membership persistence failed"
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, "purchase commit failed"
	}
	logrus.WithFields(logrus.Fields{"player": pid, "bundle": b.name, "gp": price, "heroes": len(newHeroes),
		"credits": b.credits, "elite_days": b.eliteDays}).Info("mmog: bundle bought")
	return price, newHeroes, ""
}

// pushBundleGrants hands the client what a bundle granted, without a relog:
// each new ship (YA_ClaimItem addedLoadouts), the fleets they may unlock, and
// the balances (credits were added).
func pushBundleGrants(pid string, heroes []int32) {
	pushShipGrants(pid, heroes)
	squadHubInstance.push(pid, buildMmogRewardCurrenciesPayload(pid))
}

// pushShipGrants hands the client ships it was just granted by a purchase:
// each as a YA_ClaimItem addedLoadouts push (buildMmogShipClaimPush), then the
// fleets, which a new ship may unlock. Without it a bought hero reached
// neither the owned-ships screen nor the fleet roster until a relog: the
// purchase reply does not carry loadouts, and the post-purchase fleet push in
// the connection looks for an ItemID that a store purchase does not send (it
// names the offer, "999<id>") -- live 2026-10-03, two heroes bought.
func pushShipGrants(pid string, ships []int32) {
	for _, id := range ships {
		if payload, ok := buildMmogShipClaimPush(pid, id); ok {
			squadHubInstance.push(pid, payload)
		}
	}
	if len(ships) > 0 {
		squadHubInstance.push(pid, buildMmogFleetUpdatePush(pid))
		// A ship brings owned-list items with it: its own look's parts and,
		// from T3, officer briefings (client_refresh.go).
		pushInventoryRefresh(pid)
	}
}

func (b marketBundle) String() string { return fmt.Sprintf("%s (%d)", b.name, b.id) }

// withEnglish is a copy of text with English added; the loaded table is shared.
func withEnglish(text dreadconfig.Localized, en string) dreadconfig.Localized {
	out := dreadconfig.Localized{"en": en}
	for lang, t := range text {
		if lang != "en" {
			out[lang] = t
		}
	}
	return out
}
