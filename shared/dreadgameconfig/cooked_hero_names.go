package dreadgameconfig

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Hero ship names, read out of the client's own cooked hero-loadout blueprints.
//
// The hero half of the same problem hull_names.go solves for base hulls: the
// Name column of ItemIDConversionTable carries an OLDER build's names. Measured
// against the 48 cooked *_HeroLoadout_BP blueprints (m_name), 28 hero names were
// wrong -- the current Huscarl (67043379) went out as "Skagerrak Mk.2", the
// current Zaratan (67043377) as "Minotaurus Mk.2", and every "- Vintage" variant
// lost its suffix, so a Vintage hull and its current successor were
// indistinguishable by name.
//
// The name is not cosmetic. The client compares the loadout name we send with
// the blueprint's own default and, when they differ, displays OURS:
//
//	GetLocalizedOrIndividualLoadoutName | Loadout name from Mmogbrain [Agosta]
//	  is the same as the default name by Design. Using localized...
//
// Sending exactly m_name is what lets the client fall back to its own
// localisation, which is the behaviour of the real service.
//
// Source: data/loadouts/HeroLoadouts_cooked.jsonl, produced by
//
//	dotnet bpdump/bin/Debug/net8.0/bpdump.dll --loadouts \
//	    DreadGame/Content/Generic/Loadouts/Hero "*.uasset"
//
// ("*.uasset", not "*_HeroLoadout_BP.uasset": Phoenix is spelled
// _Heroloadout_BP and Linux globs are case-sensitive.) Validated by
// scripts/validate-precast-loadouts.py.

type cookedHero struct {
	name string
	tier int
}

var (
	cookedHeroesOnce sync.Once
	cookedHeroes     map[int32]cookedHero
)

func loadCookedHeroes() {
	cookedHeroes = map[int32]cookedHero{}
	f, err := os.Open(filepath.Join(LoadoutsDir(), "HeroLoadouts_cooked.jsonl"))
	if err != nil {
		return // optional: without it, heroes fall back to the conversion table
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			Name       string `json:"m_name"`
			SystemData struct {
				ItemID int32 `json:"m_itemID"`
				Tier   int   `json:"m_itemTier"`
			} `json:"m_itemSystemData"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		if row.SystemData.ItemID != 0 && row.Name != "" {
			cookedHeroes[row.SystemData.ItemID] = cookedHero{row.Name, row.SystemData.Tier}
		}
	}
}

// CookedHeroName is the name the client's own hero blueprint gives an id.
func CookedHeroName(itemID int32) (string, bool) {
	cookedHeroesOnce.Do(loadCookedHeroes)
	hero, ok := cookedHeroes[itemID]
	return hero.name, ok
}

// CookedHeroCount reports how many hero blueprints were loaded, so a test can
// tell "no heroes on disk" apart from "no names matched".
func CookedHeroCount() int {
	cookedHeroesOnce.Do(loadCookedHeroes)
	return len(cookedHeroes)
}

// Pawn (ship) ids by loadout id, from the m_pawnClass of every cooked precast
// and hero loadout blueprint.
//
// ShipIDForPrecastLoadout derived the pawn by matching asset-path PATTERNS
// (/Ships/<Class>/<Size>/T<n>/ against /Loadouts/Precast/T<n>/), and that could
// not answer for 63 of the 99 player ships: all 15 tier-4 hulls (their pawns do
// not sit on the tiered path the pattern expects) and all 48 heroes (their
// loadouts are not under /Precast/ at all). The failure was silent:
// grantUnlockedShipLoadout returns nil when it gets no pawn, so unlocking any of
// those ships charged the player, recorded the purchase, and never gave them
// the ship. Measured by provisioning an account with every ship: 99 unlocked,
// 36 loadouts created.
//
// The blueprint names its own pawn, and all 63 resolve through the register.
//
// Rebuilt while EMPTY rather than guarded by a sync.Once: it resolves through
// ItemByAssetPath, i.e. the item catalog, and a first call that lands before the
// catalog is loaded would otherwise cache an empty map forever -- the trap the
// comment on nameCacheMu describes. An empty index is never a real answer.
var (
	cookedPawnMu        sync.Mutex
	cookedPawnByLoadout map[int32]int32
)

func loadCookedPawns() {
	cookedPawnByLoadout = map[int32]int32{}
	for _, file := range []string{"PrecastLoadouts_cooked.jsonl", "HeroLoadouts_cooked.jsonl"} {
		f, err := os.Open(filepath.Join(LoadoutsDir(), file))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var row struct {
				Pawn       string `json:"m_pawnClass"`
				SystemData struct {
					ItemID int32 `json:"m_itemID"`
				} `json:"m_itemSystemData"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Pawn == "" || row.SystemData.ItemID == 0 {
				continue
			}
			pkg := row.Pawn
			if i := strings.IndexByte(pkg, '.'); i >= 0 {
				pkg = pkg[:i]
			}
			if pawn, ok := ItemByAssetPath(pkg); ok {
				cookedPawnByLoadout[row.SystemData.ItemID] = pawn.ItemID
			}
		}
		_ = f.Close()
	}
}

// CookedPawnForLoadout is the ship pawn a loadout's own blueprint names.
func CookedPawnForLoadout(loadoutID int32) (int32, bool) {
	cookedPawnMu.Lock()
	defer cookedPawnMu.Unlock()
	if len(cookedPawnByLoadout) == 0 {
		loadCookedPawns()
	}
	pawn, ok := cookedPawnByLoadout[loadoutID]
	return pawn, ok
}
