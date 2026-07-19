package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dreadnought-ps/mmogbrain/protocol"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// gatewaySession stores logged-in session state.
type gatewaySession struct {
	UserID    string
	Username  string
	createdAt time.Time
}

const gatewaySessionTTL = 24 * time.Hour

type playerDataReadyState struct {
	ready   bool
	waiters []chan struct{}
}

// sessions is an in-memory session store (session_id → session).
var (
	sessionsMu sync.Mutex
	sessions   = make(map[string]gatewaySession)

	gatewayPlayerDataReadyMu sync.Mutex
	gatewayPlayerDataReady   = make(map[string]*playerDataReadyState)

	gatewayBootstrapPlayerDataReadyTimeout = 1500 * time.Millisecond
)

func startGatewaySessionCleanup(ctx context.Context, log *logrus.Logger) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sessionsMu.Lock()
			now := time.Now()
			for id, sess := range sessions {
				if now.Sub(sess.createdAt) > gatewaySessionTTL {
					delete(sessions, id)
				}
			}
			count := len(sessions)
			sessionsMu.Unlock()
			log.WithField("sessions", count).Debug("gateway session cleanup")
		case <-ctx.Done():
			return
		}
	}
}

func startGatewayServer(ctx context.Context, log *logrus.Logger, addr, certFile, keyFile string, secret []byte) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/authentication/login", makeGatewayHandler(log, secret, handleGWLogin))
	mux.HandleFunc("/api/v1/authentication/logout", makeGatewayHandler(log, secret, handleGWLogout))
	mux.HandleFunc("/api/v1/session/create", makeGatewayHandler(log, secret, handleGWSessionCreate))
	mux.HandleFunc("/api/v1/session/touch", makeGatewayHandler(log, secret, handleGWTouch))
	mux.HandleFunc("/api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		log.WithFields(logrus.Fields{fieldMethod: r.Method, fieldPath: r.URL.Path}).Info("gateway request")
		gwJSON(w, map[string]any{})
	})
	mux.HandleFunc("/api/v1/play/lkg", makeGatewayHandler(log, secret, handleGWPlayLkg))
	mux.HandleFunc("/api/v1/play", makeGatewayHandler(log, secret, handleGWPlay))
	mux.HandleFunc("/api/v1/bundles", makeGatewayHandler(log, secret, handleGWBundles))
	mux.HandleFunc("/api/v1/catalog/digital_items_vc", makeGatewayHandler(log, secret, handleGWCatalog))
	mux.HandleFunc("/api/v1/catalog/currency_pack_vc", makeGatewayHandler(log, secret, handleGWCatalog))
	mux.HandleFunc("/api/v1/catalog/digital_items_rmt", makeGatewayHandler(log, secret, handleGWCatalog))
	mux.HandleFunc("/api/v1/catalog/currency_pack_rmt", makeGatewayHandler(log, secret, handleGWCatalog))
	mux.HandleFunc("/api/v1/account/legal", makeGatewayHandler(log, secret, handleGWLegalItems))
	mux.HandleFunc("/api/v1/account/legal/en/text", makeGatewayHandler(log, secret, handleGWLegalItems))
	mux.HandleFunc("/api/v1/account/legal/attest", makeGatewayHandler(log, secret, handleGWLegal))
	mux.HandleFunc("/api/v1/account/legal/document/accept", makeGatewayHandler(log, secret, handleGWLegal))
	mux.HandleFunc("/api/v1/account/legal/document/", makeGatewayHandler(log, secret, handleGWLegalDocument))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.WithFields(logrus.Fields{fieldMethod: r.Method, fieldPath: r.URL.Path}).Info("gateway request")
		gwJSON(w, map[string]any{})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.WithFields(logrus.Fields{fieldMethod: r.Method, fieldPath: r.URL.Path}).Warn("gateway: unknown endpoint")
		gwJSON(w, map[string]any{})
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if certFile != "" && keyFile != "" {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		srv.TLSConfig = tlsCfg
		log.WithField("addr", addr).Info("gateway HTTPS server starting")
		if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("gateway HTTPS server error")
		}
	} else {
		log.WithField("addr", addr).Info("gateway HTTP server starting (no TLS)")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("gateway HTTP server error")
		}
	}
}

// makeGatewayHandler wraps a handler with auth validation and logging.
// The game sends Bearer {jwt} on the initial login, then Session {uuid} on all
// subsequent requests (confirmed from game logs).
func makeGatewayHandler(log *logrus.Logger, secret []byte, fn func(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.WithFields(logrus.Fields{fieldMethod: r.Method, fieldPath: r.URL.Path}).Info("gateway request")

		authHdr := r.Header.Get("Authorization")

		// Session token: "Session {uuid}" — look up in our in-memory session store.
		if strings.HasPrefix(authHdr, "Session ") {
			sessionID := parseGatewaySessionID(authHdr)
			sessionsMu.Lock()
			sess, ok := sessions[sessionID]
			if ok && time.Since(sess.createdAt) > gatewaySessionTTL {
				delete(sessions, sessionID)
				ok = false
			}
			sessionsMu.Unlock()
			if !ok {
				log.WithField("session_id", sessionID).Warn("gateway: unknown session id")
				http.Error(w, `{"error":"invalid session"}`, http.StatusUnauthorized)
				return
			}
			claims := jwt.MapClaims{
				"user_id":  sess.UserID,
				"username": sess.Username,
				"sub":      sess.UserID,
			}
			fn(w, r, claims)
			return
		}

		// Bearer JWT: used only for the initial login request.
		tokenStr := strings.TrimPrefix(authHdr, "Bearer ")
		if tokenStr == "" || tokenStr == authHdr {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}
		claims, err := protocol.VerifiedJWTClaims(tokenStr, secret, "launcher", "dreadnought")
		if err != nil {
			log.WithError(err).Warn("gateway: invalid JWT")
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		fn(w, r, claims)
	}
}

func parseGatewaySessionID(authHdr string) string {
	sessionID := strings.TrimSpace(strings.TrimPrefix(authHdr, "Session "))
	if idx := strings.Index(sessionID, ","); idx >= 0 {
		sessionID = sessionID[:idx]
	}
	return strings.TrimSpace(sessionID)
}

// handleGWLogin handles POST /api/v1/authentication/login.
// The game sends its JWT (from HKCU AuthToken registry) as a Bearer token.
// We create a session and return session_id.
func handleGWLogin(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	userID, _ := claims["user_id"].(string)
	username, _ := claims["username"].(string)
	if userID == "" {
		userID, _ = claims["sub"].(string)
	}
	if userID == "" {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	sessionID := uuid.New().String()
	sessionsMu.Lock()
	// Replace any existing session(s) for this user instead of always
	// inserting a new one — previously a single authenticated caller could
	// grow the session map without bound (up to the 24h TTL cleanup) by
	// simply calling this endpoint in a loop.
	for id, sess := range sessions {
		if sess.UserID == userID {
			delete(sessions, id)
		}
	}
	sessions[sessionID] = gatewaySession{UserID: userID, Username: username, createdAt: time.Now()}
	sessionsMu.Unlock()

	w.Header().Set("Authorization", "Session "+sessionID+", "+username)

	gwJSON(w, map[string]any{
		"SessionId":  sessionID,
		"sessionId":  sessionID,
		"session_id": sessionID,
		"id":         sessionID,
		"userId":     userID,
		"user_id":    userID,
		"UserName":   username,
		"Username":   username,
		"username":   username,
	})
}

// handleGWLogout handles POST /api/v1/authentication/logout.
func handleGWLogout(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{})
}

// handleGWLegalDocument handles GET /api/v1/account/legal/document/{type}/en/text.
// Ghidra FUN_142ab23a0 uses FUN_142ab4e90 which returns 5 when "Documents" OR "Attestations"
// field is present. Without these, it returns the "Code" value (unknown → fails).
// Returning {"Code":0,"Documents":[]} satisfies the handler: "Documents" present → type=5.
func handleGWLegalDocument(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{
		"Code":      0,
		"Documents": []any{},
	})
}

// handleGWPlayLkg handles GET /api/v1/play/lkg (mmog connection info).
// Ghidra analysis (FUN_142ab3560, FUN_142abcce0, FUN_14020e860/9a0/9e0) confirmed:
//   - "Code" (DAT_143d9bcf0): REQUIRED numeric type selector — all three handlers
//     exit immediately if "Code" is absent. Code=0 selects handler 1.
//   - "serverHost" (DAT_143d9bd40): server address string
//   - "serverPort" (DAT_143d9bd50): port as a STRING — read via FUN_140ccc750 then _wtoi()
//
// MMOG_HOST defaults to 10.0.0.73; FIRMAMENT_PORT defaults to 48843.
func handleGWPlayLkg(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	host := getenv("MMOG_HOST", "10.0.0.73")
	port := getenv("FIRMAMENT_PORT", "48843")
	gwJSON(w, map[string]any{
		"Code":       0,
		"serverHost": host,
		"serverPort": port,
	})
}

// handleGWSessionCreate handles POST /api/v1/session/create.
// Called by the client after auth-login to create (or refresh) a game session.
// The client sends either Bearer {jwt} or Session {uuid}; either way we ensure
// a session exists and return the session ID in both the Authorization header
// and the JSON body, matching the same format as handleGWLogin.
func handleGWSessionCreate(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	userID, _ := claims["user_id"].(string)
	username, _ := claims["username"].(string)
	if userID == "" {
		userID, _ = claims["sub"].(string)
	}
	if userID == "" {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	authHdr := r.Header.Get("Authorization")
	sessionID := ""
	if strings.HasPrefix(authHdr, "Session ") {
		sessionID = parseGatewaySessionID(authHdr)
		sessionsMu.Lock()
		if _, ok := sessions[sessionID]; !ok {
			sessionID = ""
		}
		sessionsMu.Unlock()
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
		sessionsMu.Lock()
		// Same fix as handleGWLogin: replace any existing session(s) for
		// this user rather than accumulating a new one on every call that
		// doesn't happen to supply a still-valid Session header.
		for id, sess := range sessions {
			if sess.UserID == userID {
				delete(sessions, id)
			}
		}
		sessions[sessionID] = gatewaySession{UserID: userID, Username: username, createdAt: time.Now()}
		sessionsMu.Unlock()
	}

	w.Header().Set("Authorization", "Session "+sessionID+", "+username)
	gwJSON(w, map[string]any{
		"SessionId":  sessionID,
		"sessionId":  sessionID,
		"session_id": sessionID,
		"id":         sessionID,
		"userId":     userID,
		"user_id":    userID,
		"UserName":   username,
		"Username":   username,
		"username":   username,
	})
}

// handleGWTouch handles POST /api/v1/session/touch.
func handleGWTouch(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{})
}

// handleGWPlay handles GET /api/v1/play.
func handleGWPlay(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	host := getenv("MMOG_HOST", "10.0.0.73")
	port := getenv("FIRMAMENT_PORT", "48843")
	gwJSON(w, map[string]any{
		"Code":       0,
		fieldStatus:  "ok",
		"serverHost": host,
		"serverPort": port,
	})
}

// handleGWBundles handles GET /api/v1/bundles.
func handleGWBundles(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	playerID := protocol.GatewayClaimsUserID(claims)
	gwJSON(w, gatewayBootstrapPayload(playerID, "bundles", waitForGatewayBootstrapPlayerDataReady(playerID)))
}

// handleGWCatalog handles catalog endpoints.
func handleGWCatalog(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	playerID := protocol.GatewayClaimsUserID(claims)
	gwJSON(w, gatewayBootstrapPayload(playerID, gatewayCatalogResponseKey(r.URL.Path), waitForGatewayBootstrapPlayerDataReady(playerID)))
}

func gatewayCatalogResponseKey(path string) string {
	switch {
	case strings.Contains(path, "digital_items_vc"):
		return "item_catalog_virtual"
	case strings.Contains(path, "currency_pack_vc"):
		return "currency_catalog_virtual"
	case strings.Contains(path, "currency_pack_rmt"):
		return "currency_catalog_real"
	default:
		return "item_catalog_real"
	}
}

func waitForGatewayBootstrapPlayerDataReady(playerID string) bool {
	if gatewayPlayerDataReadyForUser(playerID) {
		return true
	}
	if gatewayBootstrapPlayerDataReadyTimeout <= 0 {
		return false
	}
	return waitForGatewayPlayerDataReady(playerID, gatewayBootstrapPlayerDataReadyTimeout)
}

// handleGWLegal handles legal attestation endpoints — always accepted.
func handleGWLegal(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{"accepted": true})
}

// handleGWLegalItems handles GET /api/v1/account/legal and /api/v1/account/legal/en/text.
// Returns empty list so game skips T&C dialog.
// Game expects a numeric "Code" field; 0 means "no items to accept".
func handleGWLegalItems(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{
		"Code":        0,
		"items":       []any{},
		"legal_items": []any{},
		"documents":   []any{},
	})
}

func gwJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func gatewayPlayerDataReadyForUser(playerID string) bool {
	key := protocol.GatewayPlayerDataReadyKey(playerID)
	if key == "" {
		return false
	}
	gatewayPlayerDataReadyMu.Lock()
	defer gatewayPlayerDataReadyMu.Unlock()
	state := gatewayPlayerDataReady[key]
	return state != nil && state.ready
}

func setGatewayPlayerDataReadyState(playerID string, ready bool) {
	key := protocol.GatewayPlayerDataReadyKey(playerID)
	if key == "" {
		return
	}

	var waiters []chan struct{}
	gatewayPlayerDataReadyMu.Lock()
	state := gatewayPlayerDataReady[key]
	if ready {
		if state == nil {
			state = &playerDataReadyState{}
			gatewayPlayerDataReady[key] = state
		}
		if !state.ready {
			state.ready = true
			waiters = append(waiters, state.waiters...)
			state.waiters = nil
		}
		gatewayPlayerDataReadyMu.Unlock()
		for _, waiter := range waiters {
			close(waiter)
		}
		return
	}
	if state != nil {
		state.ready = false
		if len(state.waiters) == 0 {
			delete(gatewayPlayerDataReady, key)
		}
	}
	gatewayPlayerDataReadyMu.Unlock()
}

func waitForGatewayPlayerDataReady(playerID string, timeout time.Duration) bool {
	key := protocol.GatewayPlayerDataReadyKey(playerID)
	if key == "" {
		return false
	}

	readyCh := make(chan struct{})
	gatewayPlayerDataReadyMu.Lock()
	state := gatewayPlayerDataReady[key]
	if state != nil && state.ready {
		gatewayPlayerDataReadyMu.Unlock()
		return true
	}
	if state == nil {
		state = &playerDataReadyState{}
		gatewayPlayerDataReady[key] = state
	}
	state.waiters = append(state.waiters, readyCh)
	gatewayPlayerDataReadyMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-readyCh:
		return true
	case <-timer.C:
		gatewayPlayerDataReadyMu.Lock()
		defer gatewayPlayerDataReadyMu.Unlock()

		state := gatewayPlayerDataReady[key]
		if state == nil {
			return false
		}
		if state.ready {
			return true
		}
		for i, waiter := range state.waiters {
			if waiter == readyCh {
				state.waiters = append(state.waiters[:i], state.waiters[i+1:]...)
				break
			}
		}
		if len(state.waiters) == 0 {
			delete(gatewayPlayerDataReady, key)
		}
		return false
	}
}
