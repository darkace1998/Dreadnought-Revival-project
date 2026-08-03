package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// playerPIDPattern is the binary protocol's PID form: a UUID with the hyphens
// stripped. player_state is keyed on it.
var playerPIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// AdminPlayers lists accounts with their balances, so an operator can find the
// pid to grant against. The pid is not visible anywhere in the client UI, and
// asking someone to read it out of a log or a sqlite shell is how a support task
// becomes an hour.
func (h *Handler) AdminPlayers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(`SELECT user_id,display_name,soft_currency,premium_currency,free_xp,current_rank
		FROM player_state ORDER BY updated_at DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()

	type player struct {
		UserID  string `json:"user_id"`
		Name    string `json:"display_name"`
		Credits int64  `json:"credits"`
		Premium int64  `json:"premium"`
		FreeXP  int64  `json:"free_xp"`
		Rank    int    `json:"rank"`
	}
	out := []player{}
	for rows.Next() {
		var p player
		if err := rows.Scan(&p.UserID, &p.Name, &p.Credits, &p.Premium, &p.FreeXP, &p.Rank); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"players": out, "count": len(out)})
}

// AdminGrant adds currency or free XP to an account.
//
// It ADDS rather than sets: "grant" is the operation an operator wants, and a
// set would silently destroy a balance when someone means to top it up. Negative
// amounts are refused for the same reason -- taking currency away is a different
// operation and should look like one.
//
// This exists because there was no way at all to put credits on an account. A
// tester could not buy a single item to check whether the tech tree's
// "TECH ACQUIRED n / m" counter responds to ownership, which is the one open
// question about that screen (AGENT-CHAT C13.4, S8).
func (h *Handler) AdminGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  string `json:"user_id"`
		Credits int64  `json:"credits"`
		Premium int64  `json:"premium"`
		FreeXP  int64  `json:"free_xp"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !playerPIDPattern.MatchString(req.UserID) {
		writeError(w, http.StatusBadRequest, "user_id must be a 32-character hex player id (see /admin/players)")
		return
	}
	if req.Credits < 0 || req.Premium < 0 || req.FreeXP < 0 {
		writeError(w, http.StatusBadRequest, "amounts cannot be negative; this endpoint only adds")
		return
	}
	if req.Credits == 0 && req.Premium == 0 && req.FreeXP == 0 {
		writeError(w, http.StatusBadRequest, "nothing to grant")
		return
	}

	result, err := h.DB.Exec(`UPDATE player_state
		SET soft_currency=soft_currency+?, premium_currency=premium_currency+?, free_xp=free_xp+?,
		    updated_at=datetime('now')
		WHERE user_id=?`, req.Credits, req.Premium, req.FreeXP, req.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		// Deliberately not creating the row: a typo'd pid would otherwise make a
		// ghost account that looks funded and belongs to nobody.
		writeError(w, http.StatusNotFound, "no such player; check /admin/players")
		return
	}

	var credits, premium, freeXP int64
	if err := h.DB.QueryRow(`SELECT soft_currency,premium_currency,free_xp FROM player_state WHERE user_id=?`,
		req.UserID).Scan(&credits, &premium, &freeXP); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	h.Log.WithField("user_id", req.UserID).WithField("credits", req.Credits).
		WithField("premium", req.Premium).WithField("free_xp", req.FreeXP).Warn("admin grant")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok", "user_id": req.UserID,
		"credits": credits, "premium": premium, "free_xp": freeXP,
	})
}
