package dreadgameconfig

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
)

// The localization key that NAMES a weapon or ability -- its blueprint's
// m_itemUIData.m_headline -- by shared (0xFF) item id.
//
// Generated into assets/ItemHeadlineKeys.json by
// scripts/gen-item-headline-keys.py, which documents the sources and checks
// every key resolves in the client's English string tables. Needed because a
// store offer's "name" is a KEY the client resolves itself; the per-ship
// research offers went out with none and rendered
// "<DNT> Empty Name in Json en" (live, 2026-09-24).
var (
	headlineKeysMu sync.Mutex
	headlineKeys   map[int32]string
)

// ItemHeadlineKey returns the name key for a shared weapon/ability id.
//
// Rebuilt while empty rather than cached once, like the other lookups here: a
// missing file must not be remembered as "no keys" if data arrives later.
func ItemHeadlineKey(itemID int32) (string, bool) {
	headlineKeysMu.Lock()
	defer headlineKeysMu.Unlock()
	if len(headlineKeys) == 0 {
		headlineKeys = map[int32]string{}
		var rows map[string]struct {
			Key string `json:"key"`
		}
		if raw, err := os.ReadFile(AssetPath("ItemHeadlineKeys.json")); err == nil && json.Unmarshal(raw, &rows) == nil {
			for id, row := range rows {
				if n, err := strconv.ParseInt(id, 10, 32); err == nil && row.Key != "" {
					headlineKeys[int32(n)] = row.Key
				}
			}
		}
	}
	key, ok := headlineKeys[itemID]
	return key, ok
}
