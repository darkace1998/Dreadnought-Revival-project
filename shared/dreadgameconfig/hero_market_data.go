package dreadgameconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Hero ship Market text and pictures, from the client's own data.
//
// The Market is fed in the original store's ("Aviary") format: an offer's
// name and description are objects keyed by locale, and its pictures are URLs.
// scripts/gen-hero-market-data.py resolves each hero blueprint's headline,
// subline and description keys in every language the client ships, decodes
// its high-res icon to data/market-images/<icon>.png, and writes
// data/assets/HeroMarketData.json.

// Localized is one text in every language the client has it in ("en", "de", ...).
type Localized map[string]string

// HeroMarketEntry is one hero's Market text and picture. Any part may be
// missing: nine heroes have headlines the client never localized (the game
// showed the blueprint's own text), and ten have no picture of their own.
type HeroMarketEntry struct {
	Name        Localized `json:"name"`
	Subline     Localized `json:"subline"`
	Description Localized `json:"description"`
	Image       string    `json:"image"`
}

var (
	heroMarketOnce    sync.Once
	heroMarketEntries map[string]HeroMarketEntry
	marketStrings     map[string]Localized
)

func loadHeroMarketData() {
	var file struct {
		Heroes  map[string]HeroMarketEntry `json:"heroes"`
		Strings map[string]Localized       `json:"strings"`
	}
	if raw, err := os.ReadFile(AssetPath("HeroMarketData.json")); err == nil {
		_ = json.Unmarshal(raw, &file)
	}
	heroMarketEntries, marketStrings = file.Heroes, file.Strings
}

// HeroMarketData is the Market text and picture of a hero loadout id.
func HeroMarketData(itemID int32) (HeroMarketEntry, bool) {
	heroMarketOnce.Do(loadHeroMarketData)
	entry, ok := heroMarketEntries[strconv.Itoa(int(itemID))]
	return entry, ok
}

// MarketString is a Market text the client localizes outside the hero
// blueprints (see STRINGS in the generator).
func MarketString(name string) (Localized, bool) {
	heroMarketOnce.Do(loadHeroMarketData)
	text, ok := marketStrings[name]
	return text, ok && len(text) > 0
}

// MarketImagesDir holds the generated Market pictures.
func MarketImagesDir() string {
	return filepath.Join(DataDir(), "market-images")
}
