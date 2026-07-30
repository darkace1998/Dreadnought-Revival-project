package main

import (
	"database/sql"
	"strconv"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// Goal counters are the client's own bookkeeping, reported to us.
//
// It sends YA_IncrementPlayerStatsCounter as
//
//	counterId    : string   e.g. "Customize"
//	counterSubId : string   e.g. "Captain", "Ship"
//	increment    : int32
//
// captured verbatim from the frame log. The names come from the client, so
// recording what it sends is the only way to hold the real ones -- the counter
// ids in careerGoalsConfig were flagged as guesses precisely because no client
// asset defines them.
//
// The request used to be acknowledged and thrown away, and
// YA_GetPlayerStatsCounterData answered with a single hardcoded row whose
// counterId was the INT32 0 -- the wrong type for a field the client sends as a
// string, and an int32 at that, which this client's value accessors read back
// as 0 regardless. Both are fixed here.
const (
	counterFieldID    = "counterId"
	counterFieldSubID = "counterSubId"
)

// statsCounter is one stored counter.
type statsCounter struct {
	id    string
	subID string
	value int32
}

// persistIncrementPlayerStatsCounter applies one increment from the client.
func persistIncrementPlayerStatsCounter(database *sql.DB, playerPID string, payload []byte) error {
	counterID := protocol.FirstNonEmptyString(payload, counterFieldID, "CounterId", "counter_id")
	if counterID == "" {
		// Nothing to key on; treat as a no-op rather than inventing a name.
		return nil
	}
	subID := protocol.FirstNonEmptyString(payload, counterFieldSubID, "CounterSubId", "counter_sub_id", "subId")
	increment := protocol.FirstInt32Field(payload, 1, "increment", "Increment", "amount", "value")
	if increment == 0 {
		increment = 1
	}

	_, err := database.Exec(`
		INSERT INTO player_stats_counters(user_id,counter_id,counter_sub_id,value)
		VALUES(?,?,?,?)
		ON CONFLICT(user_id,counter_id,counter_sub_id)
		DO UPDATE SET value = value + excluded.value, updated_at = datetime('now')
	`, playerPID, counterID, subID, increment)
	return err
}

// playerStatsCounters returns every counter recorded for a player.
func playerStatsCounters(playerPID string) []statsCounter {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	rows, err := database.Query(
		`SELECT counter_id,counter_sub_id,value FROM player_stats_counters WHERE user_id=? ORDER BY counter_id,counter_sub_id`,
		normalizedPlayerStatePID(playerPID))
	if err != nil {
		logrus.WithError(err).Warn("mmog: read stats counters")
		return nil
	}
	defer func() { _ = rows.Close() }()

	var counters []statsCounter
	for rows.Next() {
		var c statsCounter
		if err := rows.Scan(&c.id, &c.subID, &c.value); err != nil {
			logrus.WithError(err).Warn("mmog: scan stats counter")
			return counters
		}
		counters = append(counters, c)
	}
	return counters
}

// playerStatsCounterValue sums a counter across its sub-ids, or reads one
// sub-id when it is named. A goal whose counter has never been reported reads 0.
func playerStatsCounterValue(playerPID, counterID, subID string) int32 {
	if counterID == "" {
		return 0
	}
	var total int32
	for _, counter := range playerStatsCounters(playerPID) {
		if counter.id != counterID {
			continue
		}
		if subID != "" && counter.subID != subID {
			continue
		}
		total += counter.value
	}
	return total
}

// appendMmogStatsCounterEntries writes the counter rows.
//
// counterId and counterSubId go out as the strings the client sent, and the
// value as a numeric string: these fields go through the same restrictive
// tagged union as the rest of this protocol, where an int32 reads back as 0.
func appendMmogStatsCounterEntries(b []byte, stack []int, counters []statsCounter) ([]byte, []int) {
	for _, counter := range counters {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, counterFieldID, counter.id)
		b = protocol.AppendStringField(b, counterFieldSubID, counter.subID)
		b = protocol.AppendStringField(b, "value", strconv.Itoa(int(counter.value)))
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	return b, stack
}
