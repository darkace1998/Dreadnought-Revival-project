package dreadgameconfig

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
