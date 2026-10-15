package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// provision-test-account: give an existing account everything, for testing.
//
//	mmogbrain provision-test-account -user <32-hex player id> \
//	    [-rank 20] [-credits 200000] [-premium 200000] [-free-xp 200000] \
//	    [-save-blobs-from <player who finished the tutorial>] [-ship-xp 50000]
//
// The account must already exist in auth-server (register it through
// /auth/register, so the password is hashed properly); this only fills in the
// player side.
//
// Everything is granted through the SAME paths a real purchase uses, so the
// account exercises the code under test rather than a hand-written shortcut:
//
//   - every base hull and every hero: a player_purchases row (price 0) plus
//     grantUnlockedShipLoadout, which is exactly what an unlock does;
//   - every weapon, ability and officer perk any hull fits by default or can
//     be offered in the tech tree: a player_purchases row, which is what makes
//     it OWNED in YA_PlayerGet's inventory (purchasedInventoryItemIDs).
//
// The roster is baseShipLoadouts + heroShipLoadouts, which are validated
// against the client's cooked blueprints (ship_roster_cooked_test.go), so a
// ship the game no longer has (Brutus) cannot be granted.
//
// Rank is stored directly (CurrentRank) with RankXP as progress WITHIN the rank,
// so rank N is current_rank=N, rank_xp=0, and current_xp is the sum of
// RankXPThreshold(2..N) -- the XP it takes to get there, not a made-up number.
//
// Safe to run twice: purchases and loadouts are INSERT OR IGNORE, and the
// currency/rank values are SET, not added.
func runProvisionTestAccount(args []string) error {
	fs := flag.NewFlagSet("provision-test-account", flag.ContinueOnError)
	user := fs.String("user", "", "player id (32 hex chars, auth-server user id without hyphens)")
	rank := fs.Int("rank", 20, "rank to set")
	credits := fs.Int64("credits", 200000, "credits (soft currency) to SET")
	premium := fs.Int64("premium", 200000, "premium currency to SET")
	freeXP := fs.Int64("free-xp", 200000, "free XP to SET")
	maxTier := fs.Int("max-tier", 0, "only grant ships up to this tier (0 = every tier)")
	withHeroes := fs.Bool("heroes", true, "also grant hero ships")
	shipXP := fs.Int("ship-xp", 0, "set every owned ship's XP to this (0 = leave ship XP alone)")
	withItems := fs.Bool("items", true, "also grant every weapon/ability/officer perk; false leaves "+
		"modules to be unlocked in game, which is what testing the unlock flow needs")
	saveFrom := fs.String("save-blobs-from", "", "copy the client's SGD/SCtA save blobs from this player "+
		"(e.g. one that has finished the tutorial), so the account skips onboarding")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pid := normalizedPlayerStatePID(*user)
	if len(pid) != 32 {
		return fmt.Errorf("-user must be a 32-hex player id, got %q", *user)
	}
	if *rank < 1 || *rank > 50 {
		return fmt.Errorf("-rank must be 1..50, got %d", *rank)
	}

	database, err := db.Open(getenv("DB_PATH", "mmog.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = database.Close() }()
	setMmogPlayerStateDB(database)

	if err := seedMmogPlayerState(database, pid); err != nil {
		return fmt.Errorf("seed player: %w", err)
	}

	var totalXP int64
	for r := int32(2); r <= int32(*rank); r++ {
		totalXP += int64(handlers.RankXPThreshold(r))
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=?, premium_currency=?, free_xp=?,
		current_rank=?, rank_xp=0, current_xp=?, updated_at=datetime('now') WHERE user_id=?`,
		*credits, *premium, *freeXP, *rank, totalXP, pid); err != nil {
		return fmt.Errorf("set currencies and rank: %w", err)
	}

	// A brand-new account is sent straight into the onboarding tutorial
	// (S01E00_00_Tutorial) and never reaches the real hangar or tech tree --
	// measured 2026-09-22, a whole 4.5-minute test session spent there. The
	// client decides that from its OWN save blob: SGD carries
	// m_bTutorialFinished and the onboarding rule states (Ob_TutorialFinished,
	// Ob_CharacterFinished, ...). It holds no player id, so copying one from a
	// player who has finished onboarding is the client's own data, not ours.
	if *saveFrom != "" {
		src := normalizedPlayerStatePID(*saveFrom)
		res, err := database.Exec(`INSERT OR REPLACE INTO player_save_blobs(user_id,slot,data,updated_at)
			SELECT ?,slot,data,datetime('now') FROM player_save_blobs WHERE user_id=?`, pid, src)
		if err != nil {
			return fmt.Errorf("copy save blobs: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("player %s has no save blobs to copy", src)
		}
	}

	ships, items := provisionUnlockSet()
	// Narrow the ship set on request. A test account that owns everything has
	// nothing left to unlock, so the unlock and research flows cannot be
	// validated on it; "tier 1-2, defaults only" leaves the rest to earn.
	if *maxTier > 0 || !*withHeroes {
		tierOf := map[int32]int32{}
		hero := map[int32]bool{}
		for _, h := range baseShipLoadouts {
			tierOf[h.loadoutID] = h.tier
		}
		for _, h := range heroShipLoadouts {
			tierOf[h.loadoutID], hero[h.loadoutID] = h.tier, true
		}
		kept := ships[:0]
		for _, id := range ships {
			if (*maxTier > 0 && tierOf[id] > int32(*maxTier)) || (!*withHeroes && hero[id]) {
				continue
			}
			kept = append(kept, id)
		}
		ships = kept
	}
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record := func(id int32, kind string) error {
		_, err := tx.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
			SELECT ?,?,?,0,'admin' WHERE NOT EXISTS
			(SELECT 1 FROM player_purchases WHERE user_id=? AND item_id=?)`, pid, id, kind, pid, id)
		return err
	}
	for _, id := range ships {
		if err := record(id, "loadout"); err != nil {
			return fmt.Errorf("record ship %d: %w", id, err)
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			return err
		}
	}
	if !*withItems {
		items = nil
	}
	for _, id := range items {
		kind := map[int32]string{4: "ability", 5: "weapon", 6: "perk"}[(id>>24)&0xff]
		if err := record(id, kind); err != nil {
			return fmt.Errorf("record item %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Ship XP, so research can be paid with it (YA_PlayerGet ShipXps). Keyed
	// by pawn id, as match rewards record it (awardFleetShipXP).
	if *shipXP > 0 {
		if _, err := database.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp)
			SELECT DISTINCT user_id, ship_id, ? FROM player_ship_loadouts WHERE user_id=? AND ship_id>0
			ON CONFLICT(user_id,ship_id) DO UPDATE SET xp=excluded.xp, updated_at=datetime('now')`, *shipXP, pid); err != nil {
			return fmt.Errorf("set ship xp: %w", err)
		}
	}

	var loadouts, purchases int
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_ship_loadouts WHERE user_id=?`, pid).Scan(&loadouts)
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=?`, pid).Scan(&purchases)
	fmt.Printf("provisioned %s: rank %d (%d XP), credits %d, premium %d, free XP %d\n",
		pid, *rank, totalXP, *credits, *premium, *freeXP)
	fmt.Printf("  %d ships unlocked, %d modules/weapons/officers owned\n", len(ships), len(items))
	fmt.Printf("  player now has %d ship loadouts and %d purchase rows\n", loadouts, purchases)
	return nil
}

// provisionUnlockSet is every ship, and every weapon/ability/officer perk the
// server can put in front of a player, sorted for a deterministic run.
func provisionUnlockSet() (ships []int32, items []int32) {
	itemSet := map[int32]bool{}
	add := func(ids ...int32) {
		for _, id := range ids {
			if id > 0 {
				itemSet[id] = true
			}
		}
	}
	for _, hull := range baseShipLoadouts {
		ships = append(ships, hull.loadoutID)
		add(hull.primary, hull.secondary)
		add(hull.abilities[:]...)
		add(hull.perks[:]...)
		manufacturer := shipManufacturerID(baseShipManufacturerByClassSize[hull.hullLine])
		for _, module := range techTreeModuleItems(hull, manufacturer) {
			add(module.id)
		}
	}
	for _, hero := range heroShipLoadouts {
		ships = append(ships, hero.loadoutID)
		add(hero.primary, hero.secondary)
		add(hero.abilities[:]...)
		add(hero.perks[:]...)
	}
	for id := range itemSet {
		items = append(items, id)
	}
	sort.Slice(ships, func(i, j int) bool { return ships[i] < ships[j] })
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	return ships, items
}

// provisionCosmeticSet is every player-facing cosmetic (ship vanity 20-24,
// captain customisation 50-55): the cooked catalog filtered by
// VanityItemIsPlayerFacing, i.e. exactly what the store lists (1,427 of
// 1,494). The excluded 67 are the NPC officers' heads/bodies,
// gender items and test assets -- owned, the captain editor showed them as
// options with no head or nothing to wear.
// Kept out of provisionUnlockSet so its ship/module tests stay unchanged.
func provisionCosmeticSet() []int32 {
	var ids []int32
	for _, v := range dreadconfig.VanityItems() {
		if v.ItemID > 0 && dreadconfig.VanityItemIsPlayerFacing(v) {
			ids = append(ids, v.ItemID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// maybeRunSubcommand runs a one-shot subcommand and exits, or returns false.
func maybeRunSubcommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "provision-test-account" {
		return false
	}
	if err := runProvisionTestAccount(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "provision-test-account:", err)
		os.Exit(1)
	}
	return true
}

// DN_UNLOCK_ALL: everything unlocked BY DEFAULT, for
// every player, with no rank or currency changes. The grants are the same
// player_purchases rows (+ grantUnlockedShipLoadout) provision-test-account
// uses, which is the OWNED state in YA_PlayerGet's inventory
// (purchasedInventoryItemIDs) and reads as "owned" in the tech tree -- the
// currency='admin' rows are deliberately NOT researchOnlyPurchase, so a hull's
// NumTechTreeItemsRequired module gates see bought modules too.
//
// Runs once per player, at the YA_PlayerGet dispatch (response_dispatcher.go),
// so the granted rows exist before the player data is built. Idempotent both
// within the process (per-pid cache) and across restarts (NOT EXISTS guard,
// price 0), and the in-game unlock flow can still be tested with
// DN_UNLOCK_ALL=off.
//
// Cosmetics are included (provisionCosmeticSet, item_type 'vanity'); before
// that a new player owned no cosmetics beyond the vanity probe's four.
var unlockAllEnabled = os.Getenv("DN_UNLOCK_ALL") != "off"

var unlockAllDone sync.Map // pid -> true, once this process granted it

func autoProvisionEverything(pid string) {
	if !unlockAllEnabled {
		return
	}
	pid = normalizedPlayerStatePID(pid)
	if len(pid) != 32 {
		return
	}
	if _, done := unlockAllDone.LoadOrStore(pid, true); done {
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		// Nothing to write against; retry on the next PlayerGet, not just this
		// one (delete the cache entry so the LoadOrStore above is re-run).
		unlockAllDone.Delete(pid)
		return
	}
	ships, items := provisionUnlockSet()
	cosmetics := provisionCosmeticSet()
	started := time.Now()
	if err := func() error {
		tx, err := database.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		record := func(id int32, kind string) error {
			_, err := tx.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
				SELECT ?,?,?,0,'admin' WHERE NOT EXISTS
				(SELECT 1 FROM player_purchases WHERE user_id=? AND item_id=?)`, pid, id, kind, pid, id)
			return err
		}
		for _, id := range ships {
			if err := record(id, "loadout"); err != nil {
				return fmt.Errorf("record ship %d: %w", id, err)
			}
			if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
				return fmt.Errorf("grant loadout %d: %w", id, err)
			}
		}
		for _, id := range items {
			kind := map[int32]string{4: "ability", 5: "weapon", 6: "perk"}[(id>>24)&0xff]
			if err := record(id, kind); err != nil {
				return fmt.Errorf("record item %d: %w", id, err)
			}
		}
		for _, id := range cosmetics {
			if err := record(id, "vanity"); err != nil {
				return fmt.Errorf("record cosmetic %d: %w", id, err)
			}
		}
		return tx.Commit()
	}(); err != nil {
		unlockAllDone.Delete(pid)
		logrus.WithError(err).WithFields(logrus.Fields{"player": pid}).
			Warn("mmog: DN_UNLOCK_ALL grant failed -- will retry on next YA_PlayerGet")
		return
	}
	logrus.WithFields(logrus.Fields{
		"player": pid, "ships": len(ships), "items": len(items), "cosmetics": len(cosmetics),
		"took": time.Since(started).String(),
	}).Info("mmog: DN_UNLOCK_ALL -- granted every ship, weapon/ability/perk and cosmetic at YA_PlayerGet")
}
