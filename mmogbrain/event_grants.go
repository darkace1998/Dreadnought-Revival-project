package main

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// DN_EVENT_GRANTS: a comma-separated list of cosmetic item ids every player
// is given for free, once, at their next YA_PlayerGet -- how the original
// timed events handed out their rewards (from player recollection: the
// Invader event gave a free skin, nothing else). The grant is a normal
// purchase row (price 0, currency 'event'), so it is kept after the event's
// switch is turned off, as an event reward was.
//
// Invader event: DN_EVENT_GRANTS=385810495,419365147 -- the "Invader" coating
// and the Saucer decal, the two items carrying the event's name/art in the
// vanity tables (the VAN_H_AssaultM_*_Invader hull parts are the Vigo's stock
// look and are not separate items). Which items the original event granted
// is not recorded anywhere we have. GUESS: both.
//
// Only cosmetics (isVanityItemID) are granted; anything else in the list is
// skipped with a warning, so a typo cannot hand out a ship or a module.
var eventGrantsDone sync.Map

func eventGrantIDs() []int32 {
	var ids []int32
	for _, f := range strings.Split(os.Getenv("DN_EVENT_GRANTS"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 32); err == nil && n > 0 {
			ids = append(ids, int32(n))
		}
	}
	return ids
}

func grantEventItems(pid string) {
	ids := eventGrantIDs()
	pid = normalizedPlayerStatePID(pid)
	if len(ids) == 0 || len(pid) != 32 {
		return
	}
	if _, done := eventGrantsDone.LoadOrStore(pid, true); done {
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		eventGrantsDone.Delete(pid)
		return
	}
	granted := 0
	for _, id := range ids {
		if !isVanityItemID(id) {
			logrus.WithField("item", id).Warn("DN_EVENT_GRANTS: not a cosmetic item id, skipped")
			continue
		}
		res, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
			SELECT ?,?,'vanity',0,'event' WHERE NOT EXISTS
			(SELECT 1 FROM player_purchases WHERE user_id=? AND item_id=?)`, pid, id, pid, id)
		if err != nil {
			logrus.WithError(err).WithField("item", id).Warn("DN_EVENT_GRANTS: grant failed")
			eventGrantsDone.Delete(pid)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			granted++
		}
	}
	if granted > 0 {
		logrus.WithFields(logrus.Fields{"pid": pid, "granted": granted}).Info("DN_EVENT_GRANTS: event items granted")
	}
}
