package dreadgameconfig

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// VanityItem is one ship or captain cosmetic, read from the client's own data
// assets (data/vanity/VanityItems_cooked.jsonl, produced by
// `bpdump --items DreadGame/Content/Generic/VanityItems`).
type VanityItem struct {
	ItemID int32
	// Name is the asset's export name, e.g. "VAN_EMB_Bear_DA", "Head_Male_B03".
	Name string
	// File is "DreadGame/Content/Generic/VanityItems/...".
	File string
	// HeadlineKey is m_itemUIData.m_headline: the localization key the
	// client resolves for the item's name.
	HeadlineKey string
	// PublicReady is m_itemMetaData.m_publicReady -- the developers' own "ready
	// for players" flag. Captain materials and genders carry no such flag.
	PublicReady bool
}

// Category is the item id's top byte (the category law): 20-24 ship vanity,
// 50-55 captain customisation.
func (v VanityItem) Category() int32 { return (v.ItemID >> 24) & 0xff }

// IsCaptain reports a captain-customisation item (categories 50-55).
func (v VanityItem) IsCaptain() bool { c := v.Category(); return c >= 50 && c <= 55 }

var (
	vanityItemsOnce sync.Once
	vanityItems     []VanityItem
	vanityByID      map[int32]VanityItem
)

func loadVanityItems() {
	vanityByID = map[int32]VanityItem{}
	f, err := os.Open(filepath.Join(DataDir(), "vanity", "VanityItems_cooked.jsonl"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			File   string `json:"file"`
			Export string `json:"export"`
			UI     struct {
				Headline string `json:"m_headline"`
			} `json:"m_itemUIData"`
			Meta struct {
				PublicReady bool `json:"m_publicReady"`
			} `json:"m_itemMetaData"`
			System struct {
				ItemID int32 `json:"m_itemID"`
			} `json:"m_itemSystemData"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.System.ItemID == 0 {
			continue
		}
		item := VanityItem{
			ItemID: row.System.ItemID, Name: row.Export, File: row.File,
			HeadlineKey: row.UI.Headline, PublicReady: row.Meta.PublicReady,
		}
		if _, dup := vanityByID[item.ItemID]; dup {
			continue
		}
		vanityByID[item.ItemID] = item
		vanityItems = append(vanityItems, item)
	}
	sort.Slice(vanityItems, func(i, j int) bool { return vanityItems[i].ItemID < vanityItems[j].ItemID })
}

// VanityItems returns every cosmetic, sorted by item id.
func VanityItems() []VanityItem {
	vanityItemsOnce.Do(loadVanityItems)
	return vanityItems
}

// VanityItemByID looks one cosmetic up.
func VanityItemByID(itemID int32) (VanityItem, bool) {
	vanityItemsOnce.Do(loadVanityItems)
	v, ok := vanityByID[itemID]
	return v, ok
}

// VanityItemIsFree is the server's rule for which cosmetics cost nothing.
//
// The client assets carry NO price and NO "starter/default" flag (checked on
// the vanity data assets and on Captain_Template_*, which lists every item a
// gender may use, not a starter set), so this is a server design choice:
//
//   - captain body features: genders (52), materials such as eyes, skin and
//     scars (50), heads and hair (51 under Heads/, Hair/), the base outfits
//     (Bodies/*_Base_*) and the default head attachment;
//   - every ship cosmetic that is part of some hull's default appearance
//     (AllDefaultShipVanityItemIDs).
func VanityItemIsFree(v VanityItem) bool {
	switch v.Category() {
	case 50, 52:
		return true
	case 51:
		f := v.File
		return strings.Contains(f, "/Characters/Heads/") || strings.Contains(f, "/Characters/Hair/") ||
			(strings.Contains(f, "/Characters/Bodies/") && strings.Contains(v.Name, "_Base_")) ||
			strings.Contains(v.Name, "Attachment_Default")
	}
	return AllDefaultShipVanityItemIDs()[v.ItemID]
}

// VanityItemIsSold reports whether the store lists the item: every item with a
// player-facing name (m_headline) that is not a test asset.
//
// CHANGED 2026-09-28 (the operator asked for all vanity items in the market):
// this sold only the items the developers marked m_publicReady (850) plus the
// free ones, i.e. 1,013 of 1,494. The 606 named items with no flags at all
// (Explorer, Jovian, NanoDoc sets, ...) and the 9 marked internal-only are now
// sold too: 1,465. Still excluded: items with no player-facing name -- the NPC
// officers' and Bix/Trader bodies and heads (Head_ChiefOfficer, ...) and the two
// gender items (the captain's gender travels as text, "GENDER_MALE", in the
// display info, not as an owned item) -- and test assets (their Test folder,
// Test_* and *_TEST names). Unreleased items may not have been finished by the
// developers; they are the client's own cooked assets either way.
//
// CHANGED 2026-10-06: a captain MESH (category 51: heads, hair, bodies,
// outfits, head attachments) must also be one a captain can wear -- listed by
// a gender's template (InCaptainTemplate). Materials (50: eyes, tints,
// tattoos, scars) are not held to it: 32 named ones, the PCF rank tints
// among them, are in no template and were bought and worn; nothing reported
// them broken. The male C00a/C00b/C01
// heads, the seasonal "_Outfit" variants, the Explorer sets, Outfit_Base_
// Military and 44 head attachments have names but are in no template, and
// owned they appeared in the captain editor as options with no head or
// nothing to wear.
func VanityItemIsSold(v VanityItem) bool {
	if v.HeadlineKey == "" || strings.Contains(v.File, "/Test/") ||
		strings.HasPrefix(v.Name, "Test") || strings.HasSuffix(strings.ToUpper(v.Name), "_TEST") {
		return false
	}
	if v.Category() == 51 && captainTemplatesLoaded() && !InCaptainTemplate(v.ItemID) {
		return false
	}
	return true
}

// VanityItemIsPlayerFacing reports whether a cosmetic is one players get to
// see and use at all: listed in the store (VanityItemIsSold). The free rule
// only applies to these -- free NPC heads and test hair are not player items.
func VanityItemIsPlayerFacing(v VanityItem) bool { return VanityItemIsSold(v) }
