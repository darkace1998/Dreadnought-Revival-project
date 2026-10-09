package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

// PvE seasons: the season and its episode events, from the client's own
// tables (DN_Seasons_DT / DN_Events_DT, extracted to
// data/pve/PVESeasonsEvents.json with texts and asset paths).
//
// Until now YA_GetSeasonData declared no season at all ("CurrentSeason"
// empty), so the client hid the whole PvE side. A season is a set of episode
// events -- Season 1 "Miner Inconvenience": Incident Management, Operational
// Risks, Asset Write Off (Horde) and Goodwill Expenses (Escort) -- each its
// own map (/Game/Maps/PVE/Season1/EpisodeN), with bronze/silver/gold reward
// levels per fleet type by score.
//
// The running event reaches the client as YA_GetSeasonData ActiveEvent; its
// AYPVEEventManager (ActivatePVEEvent, 0x4D9C60) titles the "PVE" entry of
// the client's mode list with it. Queueing for it sends GameType "PVE"
// (matchmaker.PvEGameMode); the battle server runs the event's map in the
// event's game mode (dn-dedicated: Horde / Escort by class path).
//
// The schedule is ours: the event dates in the tables are 2018 test windows
// (Season 1's are 20 minutes long). GUESS: one event at a time, each for
// DN_PVE_EVENT_DAYS days (default 7), in table order, from 2026-10-05 00:00
// UTC; DN_PVE_SEASON names the season (default PVE_Season1; "off" = none).

type pveText struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

type pveRewardLevel struct {
	Fleet                string  `json:"m_fleet,omitempty"`
	RequiredScore        int32   `json:"m_requiredScore"`
	RequiredEventRewards int32   `json:"m_requiredEventRewards"`
	RewardLevel          string  `json:"m_rewardLevel"`
	Image                string  `json:"m_image"`
	Items                string  `json:"m_items"`
	Name                 pveText `json:"m_name"`
	HardCurrency         int32   `json:"m_hardCurrency"`
	SoftCurrency         int32   `json:"m_softCurrency"`
	FreeXP               int32   `json:"m_freeXP"`
}

type pveEventRow struct {
	Name          pveText          `json:"m_name"`
	DescShort     pveText          `json:"m_descShort"`
	DescLong      pveText          `json:"m_descLong"`
	Map           string           `json:"m_map"`
	MapParameters string           `json:"m_mapParameters"`
	GameMode      string           `json:"m_gameMode"`
	ImageSmall    string           `json:"m_imageSmall"`
	ImageLarge    string           `json:"m_imageLarge"`
	RewardLevels  []pveRewardLevel `json:"m_rewardLevels"`
	Season        string           `json:"m_season"`
}

type pveSeasonRow struct {
	Name         pveText          `json:"m_name"`
	DescShort    pveText          `json:"m_descShort"`
	DescLong     pveText          `json:"m_descLong"`
	ImageLarge   string           `json:"m_imageLarge"`
	ImageSmall   string           `json:"m_imageSmall"`
	RewardLevels []pveRewardLevel `json:"m_rewardLevels"`
}

type pveColor struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

type pveTables struct {
	seasons map[string]pveSeasonRow
	events  map[string]pveEventRow
	colors  map[string]pveColor
}

var loadPVETables = sync.OnceValue(func() pveTables {
	t := pveTables{seasons: map[string]pveSeasonRow{}, events: map[string]pveEventRow{}, colors: map[string]pveColor{}}
	var raw struct {
		Seasons map[string]pveSeasonRow `json:"DN_Seasons_DT"`
		Events  map[string]pveEventRow  `json:"DN_Events_DT"`
	}
	data, err := os.ReadFile(filepath.Join(dreadconfig.DataDir(), "pve", "PVESeasonsEvents.json"))
	if err == nil {
		err = json.Unmarshal(data, &raw)
	}
	if err != nil {
		logrus.WithError(err).Warn("pve seasons: no season data; no season will run")
		return t
	}
	t.seasons, t.events = raw.Seasons, raw.Events
	// The event colours, which the full extraction misreads, from the older
	// table export.
	var colors struct {
		Rows map[string]struct {
			Color pveColor `json:"m_color"`
		} `json:"rows"`
	}
	if data, err := os.ReadFile(dreadconfig.DataTablePath(filepath.Join("PVE", "DN_Events_DT.json"))); err == nil &&
		json.Unmarshal(data, &colors) == nil {
		for id, r := range colors.Rows {
			t.colors[id] = r.Color
		}
	}
	return t
})

// pveScheduleAnchor is the start of the first event period (a Monday).
var pveScheduleAnchor = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

func pveSeasonID() string {
	switch v := strings.TrimSpace(os.Getenv("DN_PVE_SEASON")); strings.ToLower(v) {
	case "":
		return "PVE_Season1"
	case "off", "0", "none":
		return ""
	default:
		return v
	}
}

func pveEventPeriod() time.Duration {
	if d, err := strconv.Atoi(os.Getenv("DN_PVE_EVENT_DAYS")); err == nil && d > 0 {
		return time.Duration(d) * 24 * time.Hour
	}
	return 7 * 24 * time.Hour
}

// pveSeasonEvents is the season's events in table order (PVE_S1E1, ...).
func pveSeasonEvents(season string) []string {
	var ids []string
	for id, e := range loadPVETables().events {
		if e.Season == season {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// pveHostMode is the battle server mode for an event's EYGameModeType.
func pveHostMode(gameMode string) (string, bool) {
	switch gameMode {
	case "YGMT_HORDE":
		return "Horde", true
	case "YGMT_ESCORT":
		return "Escort", true
	}
	return "", false
}

type pveScheduledEvent struct {
	id         string
	start, end time.Time
}

// pveSchedule is every event of the running season with its window in the
// current cycle, and the index of the one running now.
func pveSchedule(now time.Time) (season string, events []pveScheduledEvent, active int) {
	season = pveSeasonID()
	ids := pveSeasonEvents(season)
	if season == "" || len(ids) == 0 {
		return "", nil, -1
	}
	period := pveEventPeriod()
	since := now.Sub(pveScheduleAnchor)
	if since < 0 {
		since = 0
	}
	slot := int(since / period)
	cycleStart := pveScheduleAnchor.Add(time.Duration(slot-slot%len(ids)) * period)
	for i, id := range ids {
		start := cycleStart.Add(time.Duration(i) * period)
		events = append(events, pveScheduledEvent{id: id, start: start, end: start.Add(period - time.Second)})
	}
	return season, events, slot % len(ids)
}

// activePvEEvent is the event running now, for the matchmaker.
func activePvEEvent() (matchmaker.PvEEvent, bool) {
	_, events, active := pveSchedule(time.Now().UTC())
	if active < 0 {
		return matchmaker.PvEEvent{}, false
	}
	id := events[active].id
	row := loadPVETables().events[id]
	mode, ok := pveHostMode(row.GameMode)
	if !ok || row.Map == "" {
		return matchmaker.PvEEvent{}, false
	}
	path, _, _ := strings.Cut(row.Map, ".") // package path, without the object name
	return matchmaker.PvEEvent{ID: id, MapPath: path, HostMode: mode}, true
}

func init() { matchmaker.PvEEventSource = activePvEEvent }

const pveDateFormat = "2006.01.02-15.04.05"

func pveRewardLevelsJSON(levels []pveRewardLevel, season bool) []map[string]any {
	out := []map[string]any{}
	for _, l := range levels {
		m := map[string]any{
			"m_rewardLevel": l.RewardLevel, "m_image": l.Image, "m_items": l.Items, "m_name": l.Name.Text,
			"m_hardCurrency": l.HardCurrency, "m_softCurrency": l.SoftCurrency, "m_freeXP": l.FreeXP,
		}
		if season {
			m["m_requiredEventRewards"] = l.RequiredEventRewards
		} else {
			m["m_fleet"] = l.Fleet
			m["m_requiredScore"] = l.RequiredScore
		}
		out = append(out, m)
	}
	return out
}

// pveSeasonDataJSON is YA_GetSeasonData's Events and Seasons blobs (JSON
// DataTables the client imports) and its CurrentSeason / ActiveEvent.
func pveSeasonDataJSON(now time.Time) (eventsJSON, seasonsJSON, current, active string, ok bool) {
	season, events, idx := pveSchedule(now)
	t := loadPVETables()
	srow, found := t.seasons[season]
	if idx < 0 || !found {
		return "", "", "", "", false
	}
	var evs []map[string]any
	for _, e := range events {
		row := t.events[e.id]
		evs = append(evs, map[string]any{
			"Name": e.id, "m_name": row.Name.Text, "m_descShort": row.DescShort.Text, "m_descLong": row.DescLong.Text,
			"m_map": row.Map, "m_mapParameters": row.MapParameters, "m_gameMode": row.GameMode,
			"m_color": t.colors[e.id], "m_imageSmall": row.ImageSmall, "m_imageLarge": row.ImageLarge,
			"m_rewardLevels": pveRewardLevelsJSON(row.RewardLevels, false),
			"m_startDate":    e.start.Format(pveDateFormat), "m_endDate": e.end.Format(pveDateFormat),
			"m_season": season,
		})
	}
	seasons := []map[string]any{{
		"Name": season, "m_active": true, "m_name": srow.Name.Text, "m_descShort": srow.DescShort.Text,
		"m_descLong": srow.DescLong.Text, "m_imageLarge": noneIfEmpty(srow.ImageLarge),
		"m_imageSmall": noneIfEmpty(srow.ImageSmall), "m_rewardLevels": pveRewardLevelsJSON(srow.RewardLevels, true),
	}}
	eb, err1 := json.Marshal(evs)
	sb, err2 := json.Marshal(seasons)
	if err1 != nil || err2 != nil {
		return "", "", "", "", false
	}
	return string(eb), string(sb), season, events[idx].id, true
}

func noneIfEmpty(s string) string {
	if s == "" {
		return "None"
	}
	return s
}
