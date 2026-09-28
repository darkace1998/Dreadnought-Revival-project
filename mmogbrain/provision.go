package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
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

	summary, err := provisionAccount(database, pid, provisionOptions{
		Rank: *rank, Credits: *credits, Premium: *premium, FreeXP: *freeXP,
		MaxTier: *maxTier, WithHeroes: *withHeroes, ShipXP: *shipXP,
		WithItems: *withItems, SaveFrom: *saveFrom,
	})
	if err != nil {
		return err
	}
	fmt.Printf("provisioned %s: rank %d (%d XP), credits %d, premium %d, free XP %d\n",
		pid, summary.Rank, summary.TotalXP, summary.Credits, summary.Premium, summary.FreeXP)
	fmt.Printf("  %d ships unlocked, %d modules/weapons/officers owned\n", summary.Ships, summary.Items)
	fmt.Printf("  player now has %d ship loadouts and %d purchase rows\n", summary.Loadouts, summary.Purchases)
	return nil
}

// provisionOptions mirrors the provision-test-account flags. Defaults come
// from defaultProvisionOptions (the CLI flag defaults).
type provisionOptions struct {
	Rank       int
	Credits    int64
	Premium    int64
	FreeXP     int64
	MaxTier    int
	WithHeroes bool
	ShipXP     int
	WithItems  bool
	SaveFrom   string
}

func defaultProvisionOptions() provisionOptions {
	return provisionOptions{
		Rank: 20, Credits: 200000, Premium: 200000, FreeXP: 200000,
		WithHeroes: true, WithItems: true,
	}
}

type provisionSummary struct {
	Rank                int   `json:"rank"`
	TotalXP             int64 `json:"total_xp"`
	Credits             int64 `json:"credits"`
	Premium             int64 `json:"premium"`
	FreeXP              int64 `json:"free_xp"`
	Ships               int   `json:"ships"`
	Items               int   `json:"items"`
	Loadouts            int   `json:"loadouts"`
	Purchases           int   `json:"purchases"`
}

// provisionAccount is the core of provision-test-account, extracted so the
// HTTP variant serves it live: it works on the passed DB handle — the
// server's own when called over HTTP — where the CLI opens a second handle
// and therefore needs a stopped stack.
func provisionAccount(database *sql.DB, pid string, opts provisionOptions) (provisionSummary, error) {
	var out provisionSummary
	if protocol.NormalizePlayerPID(pid) == "" {
		return out, fmt.Errorf("user must be a 32-hex player id")
	}
	pid = protocol.NormalizePlayerPID(pid)
	if opts.Rank < 1 || opts.Rank > 50 {
		return out, fmt.Errorf("rank must be 1..50, got %d", opts.Rank)
	}
	setMmogPlayerStateDB(database)

	if err := seedMmogPlayerState(database, pid); err != nil {
		return out, fmt.Errorf("seed player: %w", err)
	}

	var totalXP int64
	for r := int32(2); r <= int32(opts.Rank); r++ {
		totalXP += int64(handlers.RankXPThreshold(r))
	}
	if _, err := database.Exec(`UPDATE player_state SET soft_currency=?, premium_currency=?, free_xp=?,
		current_rank=?, rank_xp=0, current_xp=?, updated_at=datetime('now') WHERE user_id=?`,
		opts.Credits, opts.Premium, opts.FreeXP, opts.Rank, totalXP, pid); err != nil {
		return out, fmt.Errorf("set currencies and rank: %w", err)
	}

	// A brand-new account is sent straight into the onboarding tutorial
	// (S01E00_00_Tutorial) and never reaches the real hangar or tech tree --
	// measured 2026-09-22, a whole 4.5-minute test session spent there. The
	// client decides that from its OWN save blob: SGD carries
	// m_bTutorialFinished and the onboarding rule states (Ob_TutorialFinished,
	// Ob_CharacterFinished, ...). It holds no player id, so copying one from a
	// player who has finished onboarding is the client's own data, not ours.
	if opts.SaveFrom != "" {
		src := protocol.NormalizePlayerPID(opts.SaveFrom)
		if src == "" {
			return out, fmt.Errorf("save-blobs-from must be a 32-hex player id")
		}
		res, err := database.Exec(`INSERT OR REPLACE INTO player_save_blobs(user_id,slot,data,updated_at)
			SELECT ?,slot,data,datetime('now') FROM player_save_blobs WHERE user_id=?`, pid, src)
		if err != nil {
			return out, fmt.Errorf("copy save blobs: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return out, fmt.Errorf("player %s has no save blobs to copy", src)
		}
	}

	ships, items := provisionUnlockSet()
	// Narrow the ship set on request. A test account that owns everything has
	// nothing left to unlock, so the unlock and research flows cannot be
	// validated on it; "tier 1-2, defaults only" leaves the rest to earn.
	if opts.MaxTier > 0 || !opts.WithHeroes {
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
			if (opts.MaxTier > 0 && tierOf[id] > int32(opts.MaxTier)) || (!opts.WithHeroes && hero[id]) {
				continue
			}
			kept = append(kept, id)
		}
		ships = kept
	}
	tx, err := database.Begin()
	if err != nil {
		return out, err
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
			return out, fmt.Errorf("record ship %d: %w", id, err)
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			return out, err
		}
	}
	if !opts.WithItems {
		items = nil
	}
	for _, id := range items {
		kind := map[int32]string{4: "ability", 5: "weapon", 6: "perk"}[(id>>24)&0xff]
		if err := record(id, kind); err != nil {
			return out, fmt.Errorf("record item %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}

	// Ship XP, so research can be paid with it (YA_PlayerGet ShipXps). Keyed
	// by pawn id, as match rewards record it (awardFleetShipXP).
	if opts.ShipXP > 0 {
		if _, err := database.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp)
			SELECT DISTINCT user_id, ship_id, ? FROM player_ship_loadouts WHERE user_id=? AND ship_id>0
			ON CONFLICT(user_id,ship_id) DO UPDATE SET xp=excluded.xp, updated_at=datetime('now')`, opts.ShipXP, pid); err != nil {
			return out, fmt.Errorf("set ship xp: %w", err)
		}
	}

	out = provisionSummary{
		Rank: opts.Rank, TotalXP: totalXP,
		Credits: opts.Credits, Premium: opts.Premium, FreeXP: opts.FreeXP,
		Ships: len(ships), Items: len(items),
	}
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_ship_loadouts WHERE user_id=?`, pid).Scan(&out.Loadouts)
	_ = database.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=?`, pid).Scan(&out.Purchases)
	return out, nil
}

// adminProvision handles POST /admin/provision — provision-test-account over
// HTTP, against the LIVE server (no stopped stack: it uses the server's own
// DB handle, where the CLI opens a second one and therefore needs exclusivity).
// Values are SET, not added, exactly like the CLI.
func adminProvision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   string `json:"user_id"`
		Rank     *int   `json:"rank"`
		Credits  *int64 `json:"credits"`
		Premium  *int64 `json:"premium"`
		FreeXP   *int64 `json:"free_xp"`
		MaxTier  *int   `json:"max_tier"`
		Heroes   *bool  `json:"heroes"`
		ShipXP   *int   `json:"ship_xp"`
		Items    *bool  `json:"items"`
		SaveFrom string `json:"save_blobs_from"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeAdminLiveError(w, http.StatusBadRequest, "invalid body")
		return
	}
	pid := protocol.NormalizePlayerPID(req.UserID)
	if pid == "" {
		writeAdminLiveError(w, http.StatusBadRequest, "user_id must be a 32-hex player id")
		return
	}
	opts := defaultProvisionOptions()
	if req.Rank != nil {
		opts.Rank = *req.Rank
	}
	if req.Credits != nil {
		opts.Credits = *req.Credits
	}
	if req.Premium != nil {
		opts.Premium = *req.Premium
	}
	if req.FreeXP != nil {
		opts.FreeXP = *req.FreeXP
	}
	if req.MaxTier != nil {
		opts.MaxTier = *req.MaxTier
	}
	if req.Heroes != nil {
		opts.WithHeroes = *req.Heroes
	}
	if req.ShipXP != nil {
		opts.ShipXP = *req.ShipXP
	}
	if req.Items != nil {
		opts.WithItems = *req.Items
	}
	opts.SaveFrom = req.SaveFrom
	if opts.Rank < 1 || opts.Rank > 50 {
		writeAdminLiveError(w, http.StatusBadRequest, "rank must be 1..50")
		return
	}
	for name, v := range map[string]int64{"credits": opts.Credits, "premium": opts.Premium, "free_xp": opts.FreeXP} {
		if v < 0 || v > 100_000_000 {
			writeAdminLiveError(w, http.StatusBadRequest, name+" out of range")
			return
		}
	}
	if opts.MaxTier < 0 || opts.MaxTier > 5 || opts.ShipXP < 0 || opts.ShipXP > 10_000_000 {
		writeAdminLiveError(w, http.StatusBadRequest, "max_tier or ship_xp out of range")
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	summary, err := provisionAccount(database, pid, opts)
	if err != nil {
		writeAdminLiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	logrus.WithFields(logrus.Fields{"user_id": pid, "rank": summary.Rank}).Warn("dashboard provisioned test account")
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{
		"status": "provisioned", "user_id": pid, "summary": summary,
	})
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
