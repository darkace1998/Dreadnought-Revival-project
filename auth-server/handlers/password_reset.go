package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// resetPasswordAlphabet leaves out look-alike characters (0/O, 1/l/I), since
// the password is read out to the player by hand.
const resetPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func randomResetPassword(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(resetPasswordAlphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = resetPasswordAlphabet[v.Int64()]
	}
	return string(out), nil
}

// AdminResetPassword handles POST /admin/password: gives an account a new
// random password and signs it out everywhere. The password is in the reply
// and nowhere else -- only its hash is stored, and it is never logged.
func (h *Handler) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Username == "" && req.UserID == "") {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "username or user_id required")
		return
	}
	userID, err := h.adminTargetUser(&req.Username, req.UserID)
	if err == sql.ErrNoRows {
		writeGreyboxError(w, http.StatusNotFound, -32001, "user not found")
		return
	}
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "db error")
		return
	}
	password, err := randomResetPassword(14)
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "password generation failed")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "password hashing failed")
		return
	}
	tx, err := h.DB.Begin()
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "db error")
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, string(hash), userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "reset failed")
		return
	}
	// Signed out everywhere: whoever held the old password keeps no session.
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "reset failed")
		return
	}
	if err := tx.Commit(); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "reset failed")
		return
	}
	h.Log.WithField(fieldUsername, req.Username).Warn("password reset by admin")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "reset", fieldUsername: req.Username, "password": password})
}
