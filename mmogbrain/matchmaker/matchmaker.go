package matchmaker

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// maps available in the game (from /src Documents/game_modes/maps.md references)
var availableMaps = []string{
	"Charon", "Medusa", "Procyon", "DS-75", "Onyx", "Vesta", "Kylo", "Spree",
}

// validGameModes lists game modes the server supports.
var validGameModes = map[string]bool{
	"TeamDeathmatch":   true,
	"TeamElimination":  true,
	"TerritoryControl": true,
}

// ValidGameMode returns true if the given mode is supported.
func ValidGameMode(mode string) bool {
	return validGameModes[mode]
}

// Matchmaker polls the queue and fires match creation when enough players are present.
type Matchmaker struct {
	DB              *sql.DB
	Log             *logrus.Logger
	GameMgrURL      string // e.g. http://127.0.0.1:8085
	PlayersPerMatch int
	ticker          *time.Ticker
	stop            chan struct{}
}

// New creates a Matchmaker.
func New(db *sql.DB, log *logrus.Logger, gameMgrURL string, playersPerMatch int) *Matchmaker {
	if playersPerMatch < 1 {
		playersPerMatch = 2 // minimum for testing; production is 10
	}
	return &Matchmaker{
		DB:              db,
		Log:             log,
		GameMgrURL:      gameMgrURL,
		PlayersPerMatch: playersPerMatch,
		stop:            make(chan struct{}),
	}
}

// Start begins the matchmaking loop.
func (m *Matchmaker) Start() {
	m.ticker = time.NewTicker(3 * time.Second)
	go func() {
		for {
			select {
			case <-m.ticker.C:
				if err := m.tick(); err != nil {
					m.Log.WithError(err).Warn("matchmaker tick error")
				}
			case <-m.stop:
				m.ticker.Stop()
				return
			}
		}
	}()
	m.Log.WithField("players_per_match", m.PlayersPerMatch).Info("matchmaker started")
}

// Stop halts the matchmaking loop.
func (m *Matchmaker) Stop() {
	close(m.stop)
}

func (m *Matchmaker) tick() error {
	// Group by game_mode and tier_min to match players within compatible tier ranges.
	// Players with the same tier_min are treated as compatible for matchmaking.
	rows, err := m.DB.Query(`
		SELECT game_mode, tier_min, COUNT(*) as cnt
		FROM queue_entries WHERE status='waiting'
		GROUP BY game_mode, tier_min
		HAVING cnt >= ?
	`, m.PlayersPerMatch)
	if err != nil {
		return fmt.Errorf("queue query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	type bucket struct {
		GameMode string
		TierMin  int
		Count    int
	}
	var ready []bucket
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.GameMode, &b.TierMin, &b.Count); err != nil {
			return fmt.Errorf("scan ready queue counts: %w", err)
		}
		ready = append(ready, b)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ready queue counts: %w", err)
	}

	for _, b := range ready {
		if err := m.formMatch(b.GameMode, b.TierMin); err != nil {
			m.Log.WithError(err).WithFields(logrus.Fields{
				"game_mode": b.GameMode,
				"tier_min":  b.TierMin,
			}).Warn("form match failed")
		}
	}
	return nil
}

func (m *Matchmaker) formMatch(gameMode string, tierMin int) error {
	// Pull the oldest waiting players for this mode and tier
	rows, err := m.DB.Query(`
		SELECT id, user_id FROM queue_entries
		WHERE status='waiting' AND game_mode=? AND tier_min=?
		ORDER BY queued_at ASC
		LIMIT ?
	`, gameMode, tierMin, m.PlayersPerMatch)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	type entry struct {
		ID     string
		UserID string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.UserID); err != nil {
			return fmt.Errorf("scan queue entries: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate queue entries: %w", err)
	}

	if len(entries) < m.PlayersPerMatch {
		return nil
	}

	// Mark them as matched
	for _, e := range entries {
		if _, err := m.DB.Exec(`UPDATE queue_entries SET status='matched' WHERE id=?`, e.ID); err != nil {
			return fmt.Errorf("mark queue entry matched %s: %w", e.ID, err)
		}
	}

	// Pick a map
	mapName := availableMaps[time.Now().UnixNano()%int64(len(availableMaps))]

	// Request a game instance from the game manager
	playerIDs := make([]string, len(entries))
	for i, e := range entries {
		playerIDs[i] = e.UserID
	}
	serverIP, serverPort, instanceID, err := m.requestGameInstance(gameMode, mapName, playerIDs)
	if err != nil {
		// Rollback queue entries on failure
		for _, e := range entries {
			if _, rollbackErr := m.DB.Exec(`UPDATE queue_entries SET status='waiting' WHERE id=?`, e.ID); rollbackErr != nil {
				m.Log.WithError(rollbackErr).WithField("queue_entry_id", e.ID).Warn("rollback queue entry")
			}
		}
		return fmt.Errorf("request game instance: %w", err)
	}

	// Record match in DB
	matchID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.DB.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at) VALUES(?,?,?,?,?,?,?,?)`,
		matchID, gameMode, mapName, serverIP, serverPort, "active", now, now,
	); err != nil {
		return fmt.Errorf("insert match %s: %w", matchID, err)
	}
	for i, e := range entries {
		team := i % 2
		if _, err := m.DB.Exec(
			`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,?)`,
			matchID, e.UserID, team,
		); err != nil {
			return fmt.Errorf("insert match slot for user %s: %w", e.UserID, err)
		}
	}

	// Remove matched queue entries
	for _, e := range entries {
		if _, err := m.DB.Exec(`DELETE FROM queue_entries WHERE id=?`, e.ID); err != nil {
			return fmt.Errorf("delete matched queue entry %s: %w", e.ID, err)
		}
	}

	m.Log.WithFields(logrus.Fields{
		"match_id":    matchID,
		"instance_id": instanceID,
		"game_mode":   gameMode,
		"tier_min":    tierMin,
		"map":         mapName,
		"players":     len(entries),
		"server":      fmt.Sprintf("%s:%d", serverIP, serverPort),
	}).Info("match formed")

	return nil
}

func (m *Matchmaker) requestGameInstance(gameMode, mapName string, players []string) (string, int, string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"game_mode": gameMode,
		"map":       mapName,
		"players":   players,
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal game manager request: %w", err)
	}
	resp, err := http.Post(
		fmt.Sprintf("%s/instances", m.GameMgrURL),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", 0, "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusCreated {
		return "", 0, "", fmt.Errorf("game manager returned %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, "", fmt.Errorf("decode game manager response: %w", err)
	}

	ip, _ := result["ip"].(string)
	portF, _ := result["port"].(float64)
	instID, _ := result["instance_id"].(string)
	return ip, int(portF), instID, nil
}
