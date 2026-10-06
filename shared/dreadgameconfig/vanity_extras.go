package dreadgameconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Captain templates and cosmetic texts, from the client's own data
// (scripts/gen-hero-market-data.py writes both).
//
// CaptainTemplates.json: the captain items each gender's template
// (Captain_Template_<Male|Female>_01, a YCharacterTemplate: m_meshes,
// m_materials) lets a captain wear. Items in neither template -- the NPC
// officers' and traders' heads, the male C00a/C00b/C01 heads, the seasonal
// "_Outfit" variants, Outfit_Base_Military, test hair -- showed up in the
// captain editor once owned, as options with no head, no picture, or a suit
// tab with nothing in it (operator, 2026-10-06).
//
// VanityTexts.json: each cosmetic's name, subline and description in every
// language the client ships, from its m_itemUIData keys -- what the Market
// needs as locale objects (see HeroMarketData).

type vanityText struct {
	Name        Localized `json:"name"`
	Subline     Localized `json:"subline"`
	Description Localized `json:"description"`
}

var (
	vanityExtrasOnce sync.Once
	captainTemplate  map[int32]bool
	vanityTexts      map[string]vanityText
)

func loadVanityExtras() {
	captainTemplate = map[int32]bool{}
	var templates map[string][]int32
	if raw, err := os.ReadFile(filepath.Join(DataDir(), "vanity", "CaptainTemplates.json")); err == nil {
		_ = json.Unmarshal(raw, &templates)
	}
	for _, ids := range templates {
		for _, id := range ids {
			captainTemplate[id] = true
		}
	}
	if raw, err := os.ReadFile(filepath.Join(DataDir(), "vanity", "VanityTexts.json")); err == nil {
		_ = json.Unmarshal(raw, &vanityTexts)
	}
}

// InCaptainTemplate reports whether some gender's captain template lists the
// item. False for every item when the template data is missing.
func InCaptainTemplate(itemID int32) bool {
	vanityExtrasOnce.Do(loadVanityExtras)
	return captainTemplate[itemID]
}

// captainTemplatesLoaded reports whether the template data was found.
func captainTemplatesLoaded() bool {
	vanityExtrasOnce.Do(loadVanityExtras)
	return len(captainTemplate) > 0
}

// VanityItemText is a cosmetic's localized name and description. The
// description falls back to the item's subline, then its name: the Market's
// offer converter (0x142A7D7E0, called from 0x142A5B960) reports "<DNT>
// Invalid Description Field in Json" for any description shorter than two
// characters, and 240 named cosmetics have no English description.
func VanityItemText(itemID int32) (name, description Localized, ok bool) {
	vanityExtrasOnce.Do(loadVanityExtras)
	t, ok := vanityTexts[strconv.Itoa(int(itemID))]
	description = t.Description
	for _, fallback := range []Localized{t.Subline, t.Name} {
		if description["en"] != "" {
			break
		}
		description = fallback
	}
	return t.Name, description, ok
}
