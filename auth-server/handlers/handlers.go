package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dreadnought-ps/auth-server/jwt"
	"github.com/dreadnought-ps/auth-server/models"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

const (
	fieldUsername = "username"
	fieldUserID   = "user_id"
	fieldStatus   = "status"
)

type Handler struct {
	DB     *sql.DB
	Log    *logrus.Logger
	Secret []byte
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeGreyboxError writes an error in Greybox-compatible format.
// The native profileService only treats a response as a "real" error (non-null)
// when the JSON body contains a "code" field with value <= -32000.
// Without this, the launcher shows "Greybox service unavailable" instead of a real error.
func writeGreyboxError(w http.ResponseWriter, status int, code int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error":   msg,
		"code":    code,
		"message": msg,
	})
}

// Login handles POST /auth/ — the Dreadnought launcher auth endpoint.
//
// The launcher calls this in two ways:
//  1. Auto-login via Steam ticket:  request contains steam_ticket / grant_type=steam_ticket
//  2. Password login (our custom):  request contains username + password
//
// For Steam tickets we auto-register the user on first seen, then return a JWT.
// This lets the unmodified launcher skip the account-link page and go straight to the game.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64KB limit
	rawBody, err := io.ReadAll(r.Body)
	ct := r.Header.Get("Content-Type")
	h.Log.WithFields(logrus.Fields{
		"content_type": ct,
		"body_len":     len(rawBody),
	}).Info("auth request")
	if err != nil {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "cannot read request body")
		return
	}

	var (
		grantType   string
		steamTicket string
		username    string
		password    string
		rpcID       interface{} // non-nil → wrap response in JSON-RPC envelope
	)

	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		vals, parseErr := url.ParseQuery(string(rawBody))
		if parseErr != nil {
			writeGreyboxError(w, http.StatusBadRequest, -32602, "invalid form data")
			return
		}
		grantType = vals.Get("grant_type")
		steamTicket = vals.Get("steam_ticket")
		if steamTicket == "" {
			steamTicket = vals.Get("ticket")
		}
		username = vals.Get("username")
		password = vals.Get("password")
	} else {
		var req map[string]interface{}
		if json.Unmarshal(rawBody, &req) == nil {
			// JSON-RPC 2.0 — the real launcher format
			if jsonrpc, _ := req["jsonrpc"].(string); jsonrpc == "2.0" {
				rpcID = req["id"]
				method, _ := req["method"].(string)
				if method == "jwt.get.by_steam_ticket" {
					if params, ok := req["params"].(map[string]interface{}); ok {
						steamTicket, _ = params["ticket"].(string)
						grantType = "steam_ticket"
					}
				}
			} else {
				// Plain JSON fallback
				grantType, _ = req["grant_type"].(string)
				for _, k := range []string{"steam_ticket", "ticket", "auth_ticket", "steamTicket"} {
					if v, _ := req[k].(string); v != "" {
						steamTicket = v
						break
					}
				}
				username, _ = req["username"].(string)
				password, _ = req["password"].(string)
			}
		}
	}

	h.Log.WithFields(logrus.Fields{
		"grant_type":  grantType,
		"has_ticket":  steamTicket != "",
		"ticket_len":  len(steamTicket),
		fieldUsername: username,
		"is_rpc":      rpcID != nil,
	}).Info("auth request parsed")

	isSteam := steamTicket != "" || strings.HasPrefix(grantType, "steam")

	if isSteam {
		h.loginSteam(w, steamTicket, rpcID)
		return
	}
	if username != "" {
		h.loginPassword(w, username, password)
		return
	}

	h.Log.Warn("auth request: unknown format, no steam_ticket or username found")
	writeGreyboxError(w, http.StatusBadRequest, -32602, "Invalid request: no credentials provided")
}

// loginSteam accepts any Steam auth ticket and auto-creates a user on first login.
// We hash the ticket bytes to get a stable "steam_id" without needing Steamworks validation.
func (h *Handler) loginSteam(w http.ResponseWriter, ticket string, rpcID interface{}) {
	// SHA256 of ticket bytes → stable 16-char hex ID for this Steam session
	sum := sha256.Sum256([]byte(ticket))
	steamID := fmt.Sprintf("%x", sum[:8]) // 16 hex chars

	// Look up existing user by steam_id
	var user models.User
	err := h.DB.QueryRow(
		`SELECT id, username, email FROM users WHERE steam_id=? AND banned_at IS NULL`,
		steamID,
	).Scan(&user.ID, &user.Username, &user.Email)

	if err == sql.ErrNoRows {
		// Auto-register new Steam user
		user.ID = uuid.New().String()
		user.Username = "player_" + steamID[:8]
		user.Email = user.Username + "@steam.private"
		// Use a random bcrypt hash as placeholder (Steam users never use password login)
		dummyHash, _ := bcrypt.GenerateFromPassword([]byte(uuid.New().String()), bcrypt.MinCost)
		_, insErr := h.DB.Exec(
			`INSERT INTO users(id, username, email, password_hash, steam_id) VALUES(?,?,?,?,?)`,
			user.ID, user.Username, user.Email, string(dummyHash), steamID,
		)
		if insErr != nil {
			h.Log.WithError(insErr).Error("steam auto-register failed")
			writeGreyboxError(w, http.StatusInternalServerError, -32603, "Registration failed")
			return
		}
		h.Log.WithFields(logrus.Fields{
			fieldUserID:   user.ID,
			fieldUsername: user.Username,
			"steam_id":    steamID,
		}).Info("steam user auto-registered")
	} else if err != nil {
		h.Log.WithError(err).Error("steam login: db query")
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "Login failed")
		return
	} else {
		h.Log.WithFields(logrus.Fields{
			fieldUserID:   user.ID,
			fieldUsername: user.Username,
			"steam_id":    steamID,
		}).Info("steam user logged in")
	}

	// Steam ticket auth from the launcher uses JSON-RPC 2.0 (rpcID != nil).
	// The launcher expects a JWT with aud="launcher".
	// Direct game-client auth (plain form/JSON) gets aud="dreadnought".
	audience := "dreadnought"
	if rpcID != nil {
		audience = "launcher"
	}
	h.issueAndReturnJWT(w, user, rpcID, audience)
}

// loginPassword handles classic username/password login (our custom /auth/register accounts).
func (h *Handler) loginPassword(w http.ResponseWriter, username, password string) {
	var user models.User
	err := h.DB.QueryRow(
		`SELECT id, username, email, password_hash FROM users WHERE username=? AND banned_at IS NULL`,
		username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash)
	if err == sql.ErrNoRows {
		writeGreyboxError(w, http.StatusUnauthorized, -32006, "Invalid username or password")
		return
	}
	if err != nil {
		h.Log.WithError(err).Error("login: db query")
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "Login failed")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		writeGreyboxError(w, http.StatusUnauthorized, -32006, "Invalid username or password")
		return
	}
	h.Log.WithField(fieldUserID, user.ID).Info("password user logged in")
	h.issueAndReturnJWT(w, user, nil, "dreadnought")
}

// issueAndReturnJWT mints a JWT and writes the Greybox-compatible auth response.
// If rpcID is non-nil the response is wrapped in a JSON-RPC 2.0 envelope.
// audience should be "launcher" for launcher auth, "dreadnought" for game auth.
func (h *Handler) issueAndReturnJWT(w http.ResponseWriter, user models.User, rpcID interface{}, audience string) {
	tokenStr, err := jwt.Issue(h.Secret, user.ID, user.Username, audience, 24*time.Hour)
	if err != nil {
		h.Log.WithError(err).Error("jwt issue")
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "Could not issue token")
		return
	}

	// Store session
	sum := sha256.Sum256([]byte(tokenStr))
	tokenHash := fmt.Sprintf("%x", sum[:])
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour).UTC()
	if _, err := h.DB.Exec(
		`INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)`,
		sessionID, user.ID, tokenHash, expiresAt.Format(time.RFC3339),
	); err != nil {
		h.Log.WithError(err).Warn("store session")
	}

	// The JS profileService (services/profile.js) uses postJsonRPC() which calls
	// getResponseResult() → expects response.data.result to exist and be non-null.
	// getJwtBySteamTicket then does:
	//   successful = result[0]      → must be boolean true
	//   self.user  = result[1]      → user object
	// getUserJwt() returns this.user.jwt   → needs "jwt" field
	// getUuid()    returns this.user.guid  → needs "guid" field
	// verifytoken.js parses this.user.jwt and checks user_groups claim.
	userObj := map[string]interface{}{
		"guid":         user.ID,
		"jwt":          tokenStr,
		"token":        tokenStr,
		"access_token": tokenStr,
		"temp_token":   tokenStr,
		"token_type":   "Bearer",
		"expires_in":   86400,
		"expires_at":   expiresAt.Format(time.RFC3339),
		"user_id":      user.ID,
		"uuid":         user.ID,
		"username":     user.Username,
		"realm":        "dreadnought.pc-us",
	}

	if rpcID != nil {
		// JSON-RPC 2.0 envelope: result is [true, userObject]
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":      rpcID,
			"jsonrpc": "2.0",
			"result":  []interface{}{true, userObj},
		})
	} else {
		writeJSON(w, http.StatusOK, userObj)
	}
}

// Register handles POST /auth/register — create a new account with username/email/password.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64KB limit
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Email) == "" || len(req.Password) < 6 {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "username, email, and password (min 6 chars) required")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "could not hash password")
		return
	}

	id := uuid.New().String()
	_, err = h.DB.Exec(
		`INSERT INTO users(id,username,email,password_hash) VALUES(?,?,?,?)`,
		id, req.Username, req.Email, string(hash),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeGreyboxError(w, http.StatusConflict, -32002, "username or email already taken")
			return
		}
		h.Log.WithError(err).Error("register: db insert")
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "registration failed")
		return
	}

	h.Log.WithField(fieldUserID, id).Info("user registered")
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, fieldUsername: req.Username})
}

// Login handles POST /auth/ — the Dreadnought launcher sends a Steam auth ticket here.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	username := r.Header.Get("X-Username")
	if userID == "" {
		writeGreyboxError(w, http.StatusUnauthorized, -32001, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		fieldUserID:   userID,
		fieldUsername: username,
		"realm":       "dreadnought.pc-us",
	})
}

// Logout handles POST /auth/logout — invalidates the session token.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	if tokenStr != "" {
		sum := sha256.Sum256([]byte(tokenStr))
		tokenHash := fmt.Sprintf("%x", sum[:])
		if _, err := h.DB.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash); err != nil {
			h.Log.WithError(err).Warn("delete session on logout")
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "logged out"})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	dbOK := "ok"
	if err := h.DB.Ping(); err != nil {
		dbOK = "error"
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "service": "auth-server", "database": dbOK})
}

// AdminBan handles POST /admin/ban — bans a user by username.
func (h *Handler) AdminBan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "username and reason required")
		return
	}
	var userID string
	err := h.DB.QueryRow(`SELECT id FROM users WHERE username=?`, req.Username).Scan(&userID)
	if err == sql.ErrNoRows {
		writeGreyboxError(w, http.StatusNotFound, -32001, "user not found")
		return
	}
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "db error")
		return
	}
	tx, err := h.DB.Begin()
	if err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "db error")
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`UPDATE users SET banned_at=? WHERE id=?`, time.Now().UTC(), userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "ban failed")
		return
	}
	if _, err = tx.Exec(`INSERT OR REPLACE INTO bans(id,user_id,reason,banned_by,expires_at) VALUES(?,?,?,?,NULL)`,
		uuid.New().String(), userID, req.Reason, "admin"); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "ban failed")
		return
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "ban failed")
		return
	}
	if err = tx.Commit(); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "ban failed")
		return
	}
	err = nil // so defer doesn't rollback
	h.Log.WithFields(logrus.Fields{fieldUsername: req.Username, "reason": req.Reason}).Warn("player banned")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "banned", fieldUsername: req.Username})
}

// AdminUnban handles POST /admin/unban — removes a ban by username.
func (h *Handler) AdminUnban(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		writeGreyboxError(w, http.StatusBadRequest, -32602, "username required")
		return
	}
	var userID string
	err := h.DB.QueryRow(`SELECT id FROM users WHERE username=?`, req.Username).Scan(&userID)
	if err == sql.ErrNoRows {
		writeGreyboxError(w, http.StatusNotFound, -32001, "user not found")
		return
	}
	if _, err := h.DB.Exec(`UPDATE users SET banned_at=NULL WHERE id=?`, userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "unban failed")
		return
	}
	if _, err := h.DB.Exec(`DELETE FROM bans WHERE user_id=?`, userID); err != nil {
		writeGreyboxError(w, http.StatusInternalServerError, -32603, "unban failed")
		return
	}
	h.Log.WithField(fieldUsername, req.Username).Info("player unbanned")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "unbanned", fieldUsername: req.Username})
}
