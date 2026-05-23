//nolint:goconst,gosec,ineffassign,unused,unconvert // Reverse-engineered protocol compatibility code intentionally keeps explicit literals, casts, helper scaffolding, and legacy crypto/TLS details.
package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/tls"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dreadnought-ps/mmogbrain/db"
	"github.com/dreadnought-ps/mmogbrain/handlers"
	"github.com/dreadnought-ps/mmogbrain/matchmaker"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const (
	fieldMethod = "method"
	fieldPath   = "path"
	fieldStatus = "status"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	dbPath := getenv("DB_PATH", "mmog.db")
	addr := getenv("ADDR", ":8083")
	secret := []byte(getenv("JWT_SECRET", "changeme-dreadnought-jwt-secret"))
	gameMgrURL := getenv("GAME_MGR_URL", "http://127.0.0.1:8085")
	playersPerMatch := 2 // default; override with PLAYERS_PER_MATCH env
	if v, err := strconv.Atoi(os.Getenv("PLAYERS_PER_MATCH")); err == nil && v > 0 {
		playersPerMatch = v
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.WithError(err).Fatal("open database")
	}
	setMmogPlayerStateDB(database)
	defer func() {
		if err := database.Close(); err != nil {
			log.WithError(err).Warn("close database")
		}
	}()

	h := &handlers.Handler{DB: database, Log: log}

	mm := matchmaker.New(database, log, gameMgrURL, playersPerMatch)
	mm.Start()
	defer mm.Stop()

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Public
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())
	r.HandleFunc("/mmog/chat", h.ChatHistory).Methods(http.MethodGet)

	// Admin endpoints
	adminSub := r.PathPrefix("/admin").Subrouter()
	adminSub.Use(adminKeyMiddleware(getenv("ADMIN_KEY", "changeme-admin-key")))
	adminSub.HandleFunc("/queue", h.AdminQueue).Methods(http.MethodGet)

	// Authenticated
	auth := r.PathPrefix("/mmog").Subrouter()
	auth.Use(jwtMiddleware(secret, log))
	auth.HandleFunc("/queue", h.QueueJoin).Methods(http.MethodPost)
	auth.HandleFunc("/queue/status", h.QueueStatus).Methods(http.MethodGet)
	auth.HandleFunc("/queue", h.QueueLeave).Methods(http.MethodDelete)
	auth.HandleFunc("/match/{id}", h.GetMatch).Methods(http.MethodGet)
	auth.HandleFunc("/chat", h.ChatSend).Methods(http.MethodPost)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.WithField("addr", addr).Info("mmogbrain starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// YFirmament WebSocket server — the game connects here for the handshake/auth/chat protocol.
	// Protocol (derived from Ghidra decompile of DreadGame-Win64-Shipping.exe):
	//   1. Server → Client: JSON with status=="connection_successful" → game state=1
	//   2. Client → Server: JWT refresh token auth payload
	//   3. Server → Client: JSON with status=="success" → game state=3 (handshake complete)
	//   4. Keep-alive: respond to JSON-RPC {"method":"ping"} calls.
	// Serves TLS (WSS) when FIRMAMENT_CERT/FIRMAMENT_KEY are set; the game does a TLS
	// certificate check (confirmed via Ghidra: FUN_142aa3e00 "Firmament TLS Certificate check").
	firmamentCert := getenv("FIRMAMENT_CERT", "")
	firmamentKey := getenv("FIRMAMENT_KEY", "")
	go startFirmamentServer(shutdownCtx, log, getenv("FIRMAMENT_ADDR", ":48843"), firmamentCert, firmamentKey)

	// Gateway HTTPS server — the game sends REST API calls here for login, session, catalog, etc.
	// Protocol confirmed from game logs: POST /api/v1/authentication/login with Bearer JWT.
	gatewayCert := getenv("GATEWAY_CERT", getenv("FIRMAMENT_CERT", ""))
	gatewayKey := getenv("GATEWAY_KEY", getenv("FIRMAMENT_KEY", ""))
	go startGatewayServer(shutdownCtx, log, getenv("GATEWAY_ADDR", ":65443"), gatewayCert, gatewayKey, secret)

	go startGatewaySessionCleanup(shutdownCtx, log)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down mmogbrain")
	shutdownCancel()

	httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		log.WithError(err).Warn("shutdown mmogbrain")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func jwtMiddleware(secret []byte, log *logrus.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := auth[7:]
			type claims struct {
				UserID   string `json:"sub"`
				Username string `json:"username"`
				jwt.RegisteredClaims
			}
			c := &claims{}
			token, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (interface{}, error) {
				return secret, nil
			})
			if err != nil || !token.Valid {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			hasAud := false
			for _, a := range c.Audience {
				if a == "dreadnought" {
					hasAud = true
					break
				}
			}
			if !hasAud {
				http.Error(w, `{"error":"invalid audience"}`, http.StatusUnauthorized)
				return
			}
			r.Header.Set("X-User-ID", c.UserID)
			r.Header.Set("X-Username", c.Username)
			next.ServeHTTP(w, r)
		})
	}
}

func loggingMiddleware(log *logrus.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			log.WithFields(logrus.Fields{
				fieldMethod: r.Method,
				fieldPath:   r.URL.Path,
				fieldStatus: rw.status,
				"latency":   time.Since(start).Milliseconds(),
			}).Info("request")
		})
	}
}

func adminKeyMiddleware(key string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Admin-Key") != key {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// startFirmamentServer runs a raw TLS server on addr for the YFirmament game protocol.
// Protocol confirmed from Ghidra: FUN_142aa39a0 splits incoming bytes on '\n' (newline 0x0a),
// so each message is a JSON object terminated with a newline byte.
// The game does NOT speak HTTP/WebSocket — it uses SSL_read/SSL_write directly.
func startFirmamentServer(ctx context.Context, log *logrus.Logger, addr, certFile, keyFile string) {
	var tlsCfg *tls.Config

	if certFile != "" && keyFile != "" {
		cert, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
		if loadErr != nil {
			log.WithError(loadErr).Fatal("firmament: load TLS cert/key")
		}
		tlsCfg = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS10,
			MaxVersion:   tls.VersionTLS12,
			SessionTicketsDisabled: true,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			},
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.WithError(err).Fatal("firmament: TCP listen")
	}

	const maxConnsPerIP = 16
	connCounts := map[string]int{}
	var connCountsMu sync.Mutex

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	if tlsCfg != nil {
		log.WithField("addr", addr).Info("firmament TLS/MMOG TCP mux listening")
	} else {
		log.WithField("addr", addr).Info("firmament raw TCP server listening (no TLS)")
	}

	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.WithError(acceptErr).Error("firmament: accept error")
				continue
			}
		}

		ip := conn.RemoteAddr().(*net.TCPAddr).IP.String()
		connCountsMu.Lock()
		count := connCounts[ip]
		if count >= maxConnsPerIP {
			connCountsMu.Unlock()
			log.WithField("remote", conn.RemoteAddr().String()).Warn("firmament: connection rate limited")
			_ = conn.Close()
			continue
		}
		connCounts[ip] = count + 1
		connCountsMu.Unlock()

		go func() {
			defer func() {
				connCountsMu.Lock()
				connCounts[ip]--
				if connCounts[ip] <= 0 {
					delete(connCounts, ip)
				}
				connCountsMu.Unlock()
			}()
			routeFirmamentOrMmogConn(log, conn, tlsCfg)
		}()
	}
}

// [INFERRED] Connection routing heuristic: peeks at the first byte to determine protocol.
// TLS ClientHello starts with 0x16 (handshake record); MMOG binary protocol starts with
// any other byte (typically 0x67 'g' for the MMOG magic marker). This heuristic is derived
// from observing that both protocols share port :48843 in the production server — the game
// uses TLS for Firmament (JSON-RPC over SSL_read/SSL_write) and plain TCP with stream
// cipher for MMOG binary frames. Confirmed working with unmodified Dreadnought client.
func routeFirmamentOrMmogConn(log *logrus.Logger, conn net.Conn, tlsCfg *tls.Config) {
	remote := conn.RemoteAddr().String()
	log.WithField("remote", remote).Info("firmament/mmog: TCP connection accepted")

	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	first, err := reader.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		log.WithError(err).WithField("remote", remote).Warn("firmament/mmog: initial read failed")
		_ = conn.Close()
		return
	}

	buffered := &bufferedConn{Conn: conn, reader: reader}
	if tlsCfg != nil && len(first) > 0 && first[0] == 0x16 {
		handleFirmamentConn(log, tls.Server(buffered, tlsCfg))
		return
	}

	if tlsCfg == nil {
		handleFirmamentConn(log, buffered)
		return
	}

	handleMmogConn(log, buffered)
}

func handleMmogConn(log *logrus.Logger, conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	remote := conn.RemoteAddr().String()
	log.WithField("remote", remote).Info("mmog: plaintext connection accepted")

	buf := make([]byte, 8192)
	handshakeStage := 0
	var clientNonce []byte
	var appDecoder *mmogStreamCipher
	var appEncoder *mmogStreamCipher
	var appPlainBuf []byte
	state := &mmogConnState{playerPID: defaultMmogPlayerPID}
	for {
		readTimeout := 30 * time.Second
		if state.pendingPlayerFleets != nil && !state.playerGetResponded {
			// Give the client's explicit YA_PlayerGet a chance to arrive before we
			// fall back to reconnect-mode fleet flushing.
			readTimeout = 1500 * time.Millisecond
		}
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		n, err := conn.Read(buf)
		_ = conn.SetReadDeadline(time.Time{})
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			log.WithFields(logrus.Fields{
				"remote": remote,
				"bytes":  n,
				"hex":    hex.EncodeToString(data),
				"text":   string(data),
			}).Info("mmog: raw bytes received")

			switch handshakeStage {
			case 0:
				if isMmogHandshakePacket(data) {
					if len(data) >= 22 {
						clientNonce = append([]byte(nil), data[6:22]...)
					}
					if writeErr := sendMmogSeedResponse(conn, data); writeErr != nil {
						log.WithError(writeErr).WithField("remote", remote).Warn("mmog: seed response failed")
						return
					}
					log.WithField("remote", remote).Info("mmog: sent seed response")
					handshakeStage = 1
				}
			case 1:
				if isMmogDigestPacket(data) {
					if writeErr := sendMmogConnectedPing(conn, data); writeErr != nil {
						log.WithError(writeErr).WithField("remote", remote).Warn("mmog: connected ping failed")
						return
					}
					log.WithField("remote", remote).Info("mmog: sent connected ping")
					if len(clientNonce) == 16 {
						key := deriveMmogSessionKey(clientNonce)
						appDecoder = newMmogStreamCipher(key, 5)
						appEncoder = newMmogStreamCipher(key, 0)
						log.WithFields(logrus.Fields{
							"remote": remote,
							"key":    hex.EncodeToString(key[:]),
						}).Info("mmog: initialized application decryptor")
					}
					handshakeStage = 2
				}
			case 2:
				if frames, remaining := parseMmogAppFrames(data); len(frames) > 0 && len(remaining) == 0 {
					log.WithFields(logrus.Fields{
						"remote": remote,
						"bytes":  len(data),
						"hex":    hex.EncodeToString(data),
						"text":   string(data),
					}).Info("mmog: plaintext application bytes")
					if err := processMmogAppFrames(log, conn, remote, frames, nil, false, state); err != nil {
						return
					}
					continue
				}
				if appDecoder != nil {
					plain := appDecoder.decrypt(data)
					appPlainBuf = append(appPlainBuf, plain...)
					log.WithFields(logrus.Fields{
						"remote": remote,
						"bytes":  len(plain),
						"hex":    hex.EncodeToString(plain),
						"text":   string(plain),
					}).Info("mmog: decrypted application bytes")
					frames, remaining := parseMmogAppFrames(appPlainBuf)
					appPlainBuf = remaining
					if err := processMmogAppFrames(log, conn, remote, frames, appEncoder, true, state); err != nil {
						return
					}
				}
			}
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if err := flushPendingPlayerFleets(log, conn, remote, appEncoder, appEncoder != nil, state, "read-timeout"); err != nil {
					return
				}
				continue
			}
			if err == io.EOF {
				log.WithField("remote", remote).Info("mmog: client disconnected")
			} else {
				log.WithError(err).WithField("remote", remote).Warn("mmog: read error")
			}
			return
		}
	}
}

type mmogConnState struct {
	loginResponseSent        bool
	playerGetResponded       bool
	pendingPlayerPurchases   *mmogAppFrame // delayed until YA_PlayerGet marks bootstrap ready
	pendingPlayerFleets      *mmogAppFrame // briefly delayed to preserve YA_PlayerGet ordering
	staticFleetDataReceived  bool
	fleetEligibilityReceived bool
	playerFleetsReceived     bool
	playerPID                string
}

func flushPendingPlayerFleets(log *logrus.Logger, conn net.Conn, remote string, appEncoder *mmogStreamCipher, encryptResponses bool, state *mmogConnState, reason string) error {
	if state.pendingPlayerFleets == nil || state.playerGetResponded {
		return nil
	}
	pf := state.pendingPlayerFleets
	state.pendingPlayerFleets = nil
	log.WithFields(logrus.Fields{
		"remote": remote,
		"reason": reason,
	}).Info("mmog: flushing delayed YA_PlayerFleets without synthetic YA_PlayerGet")
	pfResp := buildMmogRequestResponseFrame(pf.requestID, pf.msgType, "YA_PlayerFleets", state.playerPID, pf.payload)
	return writeMmogAppResponse(log, conn, remote, pf.requestID, "YA_PlayerFleets", pfResp, appEncoder, encryptResponses, "reconnect fleet flush failed", "flushed delayed YA_PlayerFleets response")
}

func syntheticRequestID(tag byte) [16]byte {
	var synthID [16]byte
	synthID[0] = tag
	synthID[1] = 0xee
	synthID[2] = 0x77
	return synthID
}

func handlePlayerGetSatisfied(log *logrus.Logger, conn net.Conn, remote string, appEncoder *mmogStreamCipher, encryptResponses bool, state *mmogConnState, source string) error {
	state.playerGetResponded = true
	setGatewayPlayerDataReadyState(state.playerPID, true)
	bootstrapID := syntheticRequestID(0xf1)
	if !state.staticFleetDataReceived {
		staticResp := buildMmogResponseFrame(bootstrapID, 0x0320, buildMmogStaticFleetDataPayloadForPlayer(state.playerPID))
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_RequestStaticFleetData", staticResp, appEncoder, encryptResponses, "proactive static fleet data push failed", "sent proactive YA_RequestStaticFleetData push"); err != nil {
			return err
		}
		state.staticFleetDataReceived = true
	}
	if state.pendingPlayerPurchases != nil {
		pp := state.pendingPlayerPurchases
		state.pendingPlayerPurchases = nil
		ppResp := buildMmogRequestResponseFrame(pp.requestID, pp.msgType, "YA_GetPlayerPurchases", state.playerPID, pp.payload)
		if err := writeMmogAppResponse(log, conn, remote, pp.requestID, "YA_GetPlayerPurchases", ppResp, appEncoder, encryptResponses, "pending purchases response failed", "sent pending YA_GetPlayerPurchases response"); err != nil {
			return err
		}
	}
	if state.pendingPlayerFleets != nil {
		pf := state.pendingPlayerFleets
		state.pendingPlayerFleets = nil
		pfResp := buildMmogRequestResponseFrame(pf.requestID, pf.msgType, "YA_PlayerFleets", state.playerPID, pf.payload)
		if err := writeMmogAppResponse(log, conn, remote, pf.requestID, "YA_PlayerFleets", pfResp, appEncoder, encryptResponses, "pending fleet response failed", "sent pending YA_PlayerFleets response"); err != nil {
			return err
		}
	}
	// If client never requested YA_FleetEligibility or YA_PlayerFleets
	// (reconnect session sending only YA_PlayerGet), push them proactively.
	if !state.fleetEligibilityReceived {
		eligResp := buildMmogResponseFrame(bootstrapID, 0x0320, buildMmogFleetEligibilityPayload())
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_FleetEligibility", eligResp, appEncoder, encryptResponses, "proactive fleet eligibility push failed", "sent proactive YA_FleetEligibility push"); err != nil {
			return err
		}
	}
	if !state.playerFleetsReceived {
		fleetsResp := buildMmogResponseFrame(bootstrapID, 0x0320, buildMmogPlayerFleetsPayload(state.playerPID))
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_PlayerFleets", fleetsResp, appEncoder, encryptResponses, "proactive fleet push failed", "sent proactive YA_PlayerFleets push"); err != nil {
			return err
		}
	}
	log.WithFields(logrus.Fields{
		"remote": remote,
		"source": source,
	}).Info("mmog: satisfied YA_PlayerGet bootstrap")
	return nil
}

func processMmogAppFrames(log *logrus.Logger, conn net.Conn, remote string, frames []mmogAppFrame, appEncoder *mmogStreamCipher, encryptResponses bool, state *mmogConnState) error {
	for _, frame := range frames {
		requestName := extractMmogRequestName(frame.payload)
		log.WithFields(logrus.Fields{
			"remote":  remote,
			"type":    frame.msgType,
			"request": hex.EncodeToString(frame.requestID[:]),
			"name":    requestName,
			"bytes":   len(frame.payload),
			"hex":     hex.EncodeToString(frame.payload),
			"text":    string(frame.payload),
			"cipher":  encryptResponses,
		}).Info("mmog: application frame")

		if !state.loginResponseSent && requestName == "YA_UserLogin" {
			state.playerPID = extractMmogPlayerPID(frame.payload)
			setGatewayPlayerDataReadyState(state.playerPID, false)
			log.WithFields(logrus.Fields{
				"remote": remote,
				"pid":    state.playerPID,
			}).Info("mmog: selected player PID")
			response := buildMmogLoginSuccessFrame(frame.requestID, frame.msgType, state.playerPID)
			if err := writeMmogAppResponse(log, conn, remote, frame.requestID, requestName, response, appEncoder, encryptResponses, "login response failed", "sent YA_UserLogin success response"); err != nil {
				return err
			}
			state.loginResponseSent = true
			continue
		}
		if isMmogPingFrame(frame) {
			response := buildMmogPingFrame(frame.requestID, frame.payload[0])
			if err := writeMmogAppResponse(log, conn, remote, frame.requestID, requestName, response, appEncoder, encryptResponses, "ping response failed", "sent application ping response"); err != nil {
				return err
			}
			continue
		}
		if state.loginResponseSent && requestName != "" && requestName != "YA_UserLogin" {
			if requestName == "YA_FleetEligibility" {
				state.fleetEligibilityReceived = true
			}
			if requestName == "YA_RequestStaticFleetData" {
				state.staticFleetDataReceived = true
			}
			if requestName == "YA_GetPlayerPurchases" && !state.playerGetResponded {
				saved := frame
				state.pendingPlayerPurchases = &saved
				log.WithField("remote", remote).Info("mmog: delaying YA_GetPlayerPurchases response until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_PlayerFleets" {
				state.playerFleetsReceived = true
				if !state.playerGetResponded {
					// Delay fleet response until after YA_PlayerGet — the game requires
					// this ordering to correctly enter the 3D hangar.
					saved := frame
					state.pendingPlayerFleets = &saved
					log.WithField("remote", remote).Info("mmog: delaying YA_PlayerFleets response until YA_PlayerGet is answered")
					continue
				}
			}
			if isMmogPlayerMutationRequest(requestName) {
				if err := persistMmogPlayerMutation(state.playerPID, requestName, frame.payload); err != nil {
					log.WithError(err).WithFields(logrus.Fields{
						"remote": remote,
						"name":   requestName,
						"pid":    state.playerPID,
					}).Warn("mmog: failed to persist player mutation")
				}
			}

			if suppressUnsafeMmogObserverResponse(requestName) {
				log.WithFields(logrus.Fields{
					"remote":  remote,
					"request": hex.EncodeToString(frame.requestID[:]),
					"name":    requestName,
				}).Info("mmog: suppressed unsafe observer-only bootstrap response")
				continue
			}

			response := buildMmogRequestResponseFrame(frame.requestID, frame.msgType, requestName, state.playerPID, frame.payload)
			if err := writeMmogAppResponse(log, conn, remote, frame.requestID, requestName, response, appEncoder, encryptResponses, "request response failed", "sent request response"); err != nil {
				return err
			}
			if requestName == "YA_PlayerGet" {
				if err := handlePlayerGetSatisfied(log, conn, remote, appEncoder, encryptResponses, state, "client-request"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func suppressUnsafeMmogObserverResponse(requestName string) bool {
	return requestName == "YA_GetDailyContractsData" || requestName == "YA_GetSeasonProgress"
}

func writeMmogAppResponse(log *logrus.Logger, conn net.Conn, remote string, requestID [16]byte, requestName string, response []byte, appEncoder *mmogStreamCipher, encryptResponses bool, warnMsg string, infoMsg string) error {
	wire := response
	if encryptResponses {
		if appEncoder == nil {
			return nil
		}
		wire = appEncoder.encrypt(response)
	}
	if _, err := conn.Write(wire); err != nil {
		log.WithError(err).WithField("remote", remote).Warn("mmog: " + warnMsg)
		return err
	}
	log.WithFields(logrus.Fields{
		"remote":       remote,
		"request":      hex.EncodeToString(requestID[:]),
		"name":         requestName,
		"plain_bytes":  len(response),
		"cipher_bytes": len(wire),
		"cipher":       encryptResponses,
	}).Info("mmog: " + infoMsg)
	return nil
}

type mmogAppFrame struct {
	msgType   uint16
	requestID [16]byte
	payload   []byte
}

// [INFERRED] Binary protocol magic bytes and field type codes derived from Ghidra
// decompile of the YMmogClient plugin in DreadGame-Win64-Shipping.exe.
//
// Magic: 0x67 0x50 ("gP") — frame start marker found in all application frames.
// Field types:
//   0x09 = variable-length string (4-byte LE length + data)
//   0x56 = int32 (4-byte LE)
//   0x05 = bool (1 byte)
//   0x0c = named object (4-byte LE size, ends with 0x00 0x0e + 4-byte LE start offset)
//   0x0d = named array  (same structure as object)
//   0x0e = container terminator (6 bytes: 0x00 0x0e 0x00 0x00 0x00 0x00 for root)
//
// The field encoding is a SAX-like tagged format: each field is [name_len:1][name:N][type:1][value:N].
// Objects and arrays recursively contain more fields terminated by a 0x0e marker.
func parseMmogAppFrames(data []byte) ([]mmogAppFrame, []byte) {
	var frames []mmogAppFrame
	for {
		if len(data) < 22 {
			return frames, data
		}
		if data[0] != 0x67 || data[1] != 0x50 {
			next := bytesIndexMmogMagic(data[1:])
			if next < 0 {
				return frames, nil
			}
			data = data[next+1:]
			continue
		}
		size := int(binary.LittleEndian.Uint16(data[2:4]))
		if size < 22 {
			data = data[2:]
			continue
		}
		if len(data) < size {
			return frames, data
		}
		var requestID [16]byte
		copy(requestID[:], data[6:22])
		payload := append([]byte(nil), data[22:size]...)
		frames = append(frames, mmogAppFrame{
			msgType:   binary.LittleEndian.Uint16(data[4:6]),
			requestID: requestID,
			payload:   payload,
		})
		data = data[size:]
	}
}

func bytesIndexMmogMagic(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == 0x67 && data[i+1] == 0x50 {
			return i
		}
	}
	return -1
}

// [INFERRED] Cryptographic constants derived from Ghidra decompile of DreadGame-Win64-Shipping.exe.
//
// mmogServerSeed / mmogServerNonce:
//   Extracted from the YMmogbrain client plugin's handshake init function (FUN_142aa*).
//   These are the server's contribution to the DH-style seed exchange in the 3-step handshake
//   (client seed → server seed+nonce → client digest → server connected ping).
//   The seed is sent in msgType 0x11 response, nonce follows immediately in the same packet.
//
// mmogSecretA / mmogSecretB:
//   Static 16-byte secrets embedded in the game binary's OnlineSubsystemMmogbrain plugin.
//   Used as HKDF-like input together with client nonce and server seed to derive the per-session
//   RC4-style stream cipher key (deriveMmogSessionKey). Discovered via Ghidra string search and
//   cross-referenced with the YMmogClient key schedule init (FUN_142aa*). Both are required
//   to produce the identical key the game client computes.
var (
	mmogServerSeed  = [16]byte{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01}
	mmogServerNonce = [16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}
	mmogSecretA     = mustDecode16("A32A61B2E749FCF9DAB79D9A4D1E0452")
	mmogSecretB     = mustDecode16("F2AE945B4FE2A6840A81F891435796F3")
)

func isMmogHandshakePacket(data []byte) bool {
	if len(data) < 6 || data[0] != 0x67 || data[1] != 0x50 {
		return false
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	return msgType == 0x10
}

func isMmogDigestPacket(data []byte) bool {
	if len(data) < 6 || data[0] != 0x67 || data[1] != 0x50 {
		return false
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	return msgType == 0x12
}

func sendMmogSeedResponse(conn net.Conn, _ []byte) error {
	packet := make([]byte, 0, 38)
	packet = appendMmogHeader(packet, 0x26, 0x11)
	packet = append(packet, mmogServerSeed[:]...)
	packet = append(packet, mmogServerNonce[:]...)
	_, err := conn.Write(packet)
	return err
}

func sendMmogConnectedPing(conn net.Conn, _ []byte) error {
	payload := []byte{
		0xa5, 0x5a, 0xa5, 0x5a, 0x3c, 0xc3, 0x3c, 0xc3,
		0x69, 0x96, 0x69, 0x96, 0x0f, 0xf0, 0x0f, 0xf0,
	}
	packet := make([]byte, 0, 22)
	packet = appendMmogHeader(packet, 0x16, 0x16)
	packet = append(packet, payload...)
	_, err := conn.Write(packet)
	return err
}

func appendMmogHeader(packet []byte, size uint16, msgType uint16) []byte {
	var header [6]byte
	binary.LittleEndian.PutUint16(header[0:2], 0x5067)
	binary.LittleEndian.PutUint16(header[2:4], size)
	binary.LittleEndian.PutUint16(header[4:6], msgType)
	return append(packet, header[:]...)
}

func buildMmogLoginSuccessFrame(requestID [16]byte, requestType uint16, playerPID ...string) []byte {
	payload := buildMmogLoginSuccessPayload(playerPID...)
	return buildMmogResponseFrame(requestID, requestType, payload)
}

func buildMmogRequestSuccessFrame(requestID [16]byte, requestType uint16, requestName string) []byte {
	payload := buildMmogRequestSuccessPayload(requestName)
	return buildMmogResponseFrame(requestID, requestType, payload)
}

func buildMmogRequestResponseFrame(requestID [16]byte, requestType uint16, requestName string, playerPID string, reqPayload []byte) []byte {
	payload := buildMmogRequestResponsePayload(requestName, playerPID, reqPayload)
	return buildMmogResponseFrame(requestID, requestType, payload)
}

func buildMmogResponseFrame(requestID [16]byte, requestType uint16, payload []byte) []byte {
	// Append the implicit root-frame terminator (0x00 0x0e 0x00 0x00 0x00 0x00) that the client
	// expects at the end of every application-layer payload to signal end-of-frame.
	payload = appendMmogRootEnd(payload)
	frameType := requestType&0x00ff | 0x0300
	frame := make([]byte, 0, 22+len(payload))
	frame = appendMmogHeader(frame, uint16(22+len(payload)), frameType)
	frame = append(frame, requestID[:]...)
	frame = append(frame, payload...)
	return frame
}

func isMmogPingFrame(frame mmogAppFrame) bool {
	return frame.msgType == 0x0300 && len(frame.payload) == 1
}

func buildMmogPingFrame(requestID [16]byte, payload byte) []byte {
	frame := make([]byte, 0, 23)
	frame = appendMmogHeader(frame, 23, 0x0300)
	frame = append(frame, requestID[:]...)
	frame = append(frame, payload)
	return frame
}

func buildMmogLoginSuccessPayload(playerPID ...string) []byte {
	var b []byte
	var stack []int
	pid := defaultMmogPlayerPID
	if len(playerPID) > 0 {
		pid = playerPID[0]
	}
	state := mmogPlayerStateForPID(pid)

	b = appendMmogStringField(b, "RT", "YA_UserLogin")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogInt32Field(b, "credits", state.softCurrency)
	b = appendMmogInt32Field(b, "premiumCurrency", state.premiumCurrency)
	b = appendMmogInt32Field(b, "freexp", state.freeXP)
	b = appendMmogInt32Field(b, "xp", state.currentXP)
	b, stack = appendMmogObjectStart(b, stack, "LoginStreak")
	b = appendMmogInt32Field(b, "loginstreak", 0)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogRequestSuccessPayload(requestName string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

type mmogShipSeed struct {
	id           int32
	name         string
	classID      int32
	shipClass    int32
	weight       int32
	manufacturer string
	owned        bool
	nodeID       int32
	parentID     int32
	nodeType     int32
	unlockCost   int32
	prereqID1    int32
	prereqID2    int32
	bIsNew       bool
}

type mmogShipLoadoutSeed struct {
	ship              mmogShipSeed
	fleetShipID       int32
	playerLoadoutID   int32
	precastLoadoutID  int32
	nativeLoadoutID   string
	loadoutIndex      int32
	loadoutName       string
	position          int32
	active            bool
	weaponPrimaryID   int32
	weaponSecondaryID int32
	abilityIDs        [4]int32
	perkIDs           [4]int32
}

func (loadout mmogShipLoadoutSeed) loadoutID() int32 {
	if loadout.playerLoadoutID != 0 {
		return loadout.playerLoadoutID
	}
	return loadout.precastLoadoutID
}

func (loadout mmogShipLoadoutSeed) effectiveFleetShipID() int32 {
	if loadout.fleetShipID != 0 {
		return loadout.fleetShipID
	}
	return loadout.ship.id
}

func (loadout mmogShipLoadoutSeed) entryID() string {
	if loadout.nativeLoadoutID != "" {
		return loadout.nativeLoadoutID
	}
	return "Default__" + strings.ReplaceAll(loadout.loadoutName, " ", "") + "_" + strconv.FormatInt(int64(loadout.loadoutID()), 10) + "_C"
}

func (loadout mmogShipLoadoutSeed) displayInfo() string {
	return ""
}

func (loadout mmogShipLoadoutSeed) slotItemID(offset int32) int32 {
	return loadout.precastLoadoutID*10 + offset
}

func nativeLoadoutObjectID(assetPath string) string {
	assetName := assetPath
	if idx := strings.LastIndex(assetName, "/"); idx >= 0 {
		assetName = assetName[idx+1:]
	}
	if assetName == "" {
		return ""
	}
	return assetName + ".Default__" + assetName + "_C"
}

var nativeStarterLoadoutIDsByPrecastID = map[int32]string{
	33489262: "Default__VH_AssaultMedium_T1_Loadout_BP_C",
	33489423: "Default__VH_DreadnoughtMedium_Loadout_BP_C",
	33489263: "Default__VH_SniperMedium_T1_Loadout_BP_C",
	33489264: "Default__VH_SupportMedium_T1_Loadout_BP_C",
}

var fleetStarterShipIDsByPrecastID = map[int32]int32{
	33489262: 33489198,
	33489423: 33489239,
	33489263: 33489199,
	33489264: 33489200,
}

func nativeStarterLoadoutID(precastLoadoutID int32) (string, bool) {
	id, ok := nativeStarterLoadoutIDsByPrecastID[precastLoadoutID]
	return id, ok
}

func (loadout mmogShipLoadoutSeed) weaponIDs() []int32 {
	return collectNonZeroItemIDs(loadout.weaponPrimaryItemID(), loadout.weaponSecondaryItemID())
}

func (loadout mmogShipLoadoutSeed) abilityItemIDs() []int32 {
	return collectNonZeroItemIDs(
		loadout.abilityItemID(0),
		loadout.abilityItemID(1),
		loadout.abilityItemID(2),
		loadout.abilityItemID(3),
	)
}

func (loadout mmogShipLoadoutSeed) perkItemIDs() []int32 {
	return collectNonZeroItemIDs(loadout.perkIDs[:]...)
}

func (loadout mmogShipLoadoutSeed) perkNames() []string {
	slotNames := []string{"Command Briefing", "Weapon Briefing", "Navigation Briefing", "Engineering Briefing"}
	names := make([]string, 0, len(slotNames))
	for idx, fallback := range slotNames {
		itemID := loadout.perkItemID(idx)
		if itemID == 0 {
			continue
		}
		names = append(names, extractedMarketItemDisplayName(itemID, fallback))
	}
	return names
}

func (loadout mmogShipLoadoutSeed) complete() bool {
	if len(loadout.loadoutSlots()) == 0 {
		return false
	}
	for _, slot := range loadout.loadoutSlots() {
		if slot.itemID == 0 {
			return false
		}
	}
	return true
}

type mmogLoadoutItemSeed struct {
	slotName    string
	headline    string
	description string
	itemType    string
	position    int32
	itemID      int32
	itemTier    int32
}

type mmogModuleUIDataSeed struct {
	itemID   int32
	index    int32
	owned    bool
	equipped bool
}

func nonZeroLoadoutItemID(value int32, fallback int32) int32 {
	if value != 0 {
		return value
	}
	return fallback
}

func collectNonZeroItemIDs(ids ...int32) []int32 {
	items := make([]int32, 0, len(ids))
	for _, itemID := range ids {
		if itemID != 0 {
			items = append(items, itemID)
		}
	}
	return items
}

func starterModuleUIDataSeeds() []mmogModuleUIDataSeed {
	loadouts := starterShipLoadouts()
	seen := make(map[int32]int)
	seeds := make([]mmogModuleUIDataSeed, 0, len(loadouts)*6)
	for _, loadout := range loadouts {
		for _, slot := range loadout.loadoutSlots() {
			if slot.itemID == 0 {
				continue
			}
			if _, exists := seen[slot.itemID]; exists {
				continue
			}
			if _, ok := extractedMarketItemMetadataForID(slot.itemID); !ok {
				continue
			}
			seen[slot.itemID] = len(seeds)
			seeds = append(seeds, mmogModuleUIDataSeed{
				itemID:   slot.itemID,
				index:    int32(len(seeds)),
				owned:    true,
				equipped: true,
			})
		}
	}
	return seeds
}

func (loadout mmogShipLoadoutSeed) weaponPrimaryItemID() int32 {
	return loadout.weaponPrimaryID
}

func (loadout mmogShipLoadoutSeed) weaponSecondaryItemID() int32 {
	return loadout.weaponSecondaryID
}

func (loadout mmogShipLoadoutSeed) abilityItemID(index int) int32 {
	return loadout.abilityIDs[index]
}

func (loadout mmogShipLoadoutSeed) perkItemID(index int) int32 {
	return loadout.perkIDs[index]
}

func (loadout mmogShipLoadoutSeed) weaponSlots() []mmogLoadoutItemSeed {
	slots := []mmogLoadoutItemSeed{}
	if itemID := loadout.weaponPrimaryItemID(); itemID != 0 {
		slots = append(slots, mmogLoadoutItemSeed{slotName: "weaponPrimary", headline: extractedMarketItemDisplayName(itemID, "Primary Weapon"), description: loadout.loadoutName + " primary weapon slot", itemType: "weapon", position: 0, itemID: itemID, itemTier: 1})
	}
	if itemID := loadout.weaponSecondaryItemID(); itemID != 0 {
		slots = append(slots, mmogLoadoutItemSeed{slotName: "weaponSecondary", headline: extractedMarketItemDisplayName(itemID, "Secondary Weapon"), description: loadout.loadoutName + " secondary weapon slot", itemType: "weapon", position: 1, itemID: itemID, itemTier: 1})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) abilitySlots() []mmogLoadoutItemSeed {
	slotNames := []struct {
		name     string
		headline string
	}{
		{name: "abilityPrimary", headline: "Primary Ability"},
		{name: "abilitySecondary", headline: "Secondary Ability"},
		{name: "abilityPerimeter", headline: "Perimeter Ability"},
		{name: "abilityInternal", headline: "Internal Ability"},
	}
	slots := make([]mmogLoadoutItemSeed, 0, len(slotNames))
	for idx, slot := range slotNames {
		itemID := loadout.abilityItemID(idx)
		if itemID == 0 {
			continue
		}
		slots = append(slots, mmogLoadoutItemSeed{
			slotName:    slot.name,
			headline:    extractedMarketItemDisplayName(itemID, slot.headline),
			description: loadout.loadoutName + " " + strings.ToLower(slot.headline) + " slot",
			itemType:    "ability",
			position:    int32(idx),
			itemID:      itemID,
			itemTier:    1,
		})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) perkSlots() []mmogLoadoutItemSeed {
	slotNames := []struct {
		name     string
		headline string
	}{
		{name: "perkCom", headline: "Command Briefing"},
		{name: "perkWeapon", headline: "Weapon Briefing"},
		{name: "perkNavigation", headline: "Navigation Briefing"},
		{name: "perkEngineer", headline: "Engineering Briefing"},
	}
	slots := make([]mmogLoadoutItemSeed, 0, len(slotNames))
	for idx, slot := range slotNames {
		itemID := loadout.perkItemID(idx)
		if itemID == 0 {
			continue
		}
		slots = append(slots, mmogLoadoutItemSeed{
			slotName:    slot.name,
			headline:    extractedMarketItemDisplayName(itemID, slot.headline),
			description: loadout.loadoutName + " " + strings.ToLower(slot.headline) + " slot",
			itemType:    "perk",
			position:    int32(idx),
			itemID:      itemID,
			itemTier:    1,
		})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) loadoutSlots() []mmogLoadoutItemSeed {
	slots := make([]mmogLoadoutItemSeed, 0, len(loadout.weaponSlots())+len(loadout.abilitySlots())+len(loadout.perkSlots()))
	slots = append(slots, loadout.weaponSlots()...)
	slots = append(slots, loadout.abilitySlots()...)
	slots = append(slots, loadout.perkSlots()...)
	return slots
}

type mmogFleetSeed struct {
	fleetID              int32
	token                string
	displayName          string
	fleetType            int32
	tiers                []int32
	active               bool
	shipLoadouts         []mmogShipLoadoutSeed
	flagshipShipID       int32
	flagshipLoadoutID    int32
	flagshipLoadoutIndex int32
}

func (fleet mmogFleetSeed) flagshipIndex() int32 {
	if fleet.flagshipLoadoutIndex >= 0 {
		return fleet.flagshipLoadoutIndex
	}
	for idx, loadout := range fleet.shipLoadouts {
		if loadout.effectiveFleetShipID() == fleet.flagshipShipID && loadout.loadoutID() == fleet.flagshipLoadoutID {
			return int32(idx)
		}
	}
	return 0
}

func (fleet mmogFleetSeed) flagshipOnly() mmogFleetSeed {
	var flagship []mmogShipLoadoutSeed
	for _, loadout := range fleet.shipLoadouts {
		if loadout.effectiveFleetShipID() == fleet.flagshipShipID && loadout.loadoutID() == fleet.flagshipLoadoutID {
			flagship = []mmogShipLoadoutSeed{loadout}
			break
		}
	}
	if len(flagship) == 0 && len(fleet.shipLoadouts) > 0 {
		flagship = []mmogShipLoadoutSeed{fleet.shipLoadouts[0]}
	}
	if len(flagship) > 0 {
		flagship[0].position = 0
		flagship[0].loadoutIndex = 0
	}
	fleet.shipLoadouts = flagship
	fleet.flagshipLoadoutIndex = 0
	if len(fleet.shipLoadouts) > 0 {
		fleet.flagshipShipID = fleet.shipLoadouts[0].effectiveFleetShipID()
		fleet.flagshipLoadoutID = fleet.shipLoadouts[0].loadoutID()
	}
	return fleet
}

func (fleet mmogFleetSeed) shipIDs() []int32 {
	ids := make([]int32, 0, len(fleet.shipLoadouts))
	for _, loadout := range fleet.shipLoadouts {
		ids = append(ids, loadout.effectiveFleetShipID())
	}
	return ids
}

func (fleet mmogFleetSeed) loadoutIDs() []int32 {
	ids := make([]int32, 0, len(fleet.shipLoadouts))
	for _, loadout := range fleet.shipLoadouts {
		ids = append(ids, loadout.loadoutID())
	}
	return ids
}

func (fleet mmogFleetSeed) shipTechTreeComplete() []bool {
	complete := make([]bool, len(fleet.shipLoadouts))
	for idx := range complete {
		complete[idx] = true
	}
	return complete
}

const defaultMmogPlayerPID = "00000000000000000000000000000001"

type starterShipArchetype struct {
	classKey     string
	classID      int32
	shipClass    int32
	manufacturer string
}

var starterShipArchetypes = map[string]starterShipArchetype{
	"assault":     {classKey: "assault", classID: 14, shipClass: 0, manufacturer: "JupiterArms"},
	"dreadnought": {classKey: "dreadnought", classID: 6, shipClass: 4, manufacturer: "AkulaVektor"},
	"sniper":      {classKey: "sniper", classID: 10, shipClass: 2, manufacturer: "AkulaVektor"},
	"support":     {classKey: "support", classID: 12, shipClass: 3, manufacturer: "Oberon"},
}

var starterShips = []mmogShipSeed{
	{id: extractedShipIDAthos, name: "Athos", classID: 14, shipClass: 0, weight: 1, manufacturer: "JupiterArms", owned: true, nodeID: extractedShipIDAthos, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},    // Jupiter Arms Destroyer
	{id: extractedShipIDZmey, name: "Zmey", classID: 6, shipClass: 4, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDZmey, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},        // Akula Vektor Dreadnought
	{id: extractedShipIDSvarog, name: "Svarog", classID: 10, shipClass: 2, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDSvarog, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false}, // Akula Vektor Artillery
	{id: extractedShipIDAion, name: "Aion", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: true, nodeID: extractedShipIDAion, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},            // Oberon Tactical
}

var lockedT1Ships = []mmogShipSeed{
	{id: extractedShipIDValcour, name: "Valcour", classID: 2, shipClass: 1, weight: 0, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDValcour, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false},                        // Jupiter Arms Corvette
	{id: extractedShipIDLeipzig, name: "Leipzig", classID: 14, shipClass: 0, weight: 1, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDLeipzig, parentID: extractedShipIDAthos, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false}, // Jupiter Arms Destroyer T2
	{id: extractedShipIDTrieste, name: "Trieste", classID: 6, shipClass: 4, weight: 1, manufacturer: "AkulaVektor", owned: false, nodeID: extractedShipIDTrieste, parentID: extractedShipIDZmey, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDZmey, prereqID2: 0, bIsNew: false},    // Akula Vektor Dreadnought T2
	{id: extractedShipIDCeres, name: "Ceres", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: false, nodeID: extractedShipIDCeres, parentID: extractedShipIDAion, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAion, prereqID2: 0, bIsNew: false},              // Oberon Tactical follow-up
}

func allT1Ships() []mmogShipSeed {
	installerStarterShips := starterBootstrapShips()
	ships := make([]mmogShipSeed, 0, len(installerStarterShips)+len(starterShips)+len(lockedT1Ships))
	seen := make(map[int32]struct{}, cap(ships))
	for _, group := range [][]mmogShipSeed{installerStarterShips, starterShips, lockedT1Ships} {
		for _, ship := range group {
			if _, ok := seen[ship.id]; ok {
				continue
			}
			seen[ship.id] = struct{}{}
			ships = append(ships, ship)
		}
	}
	return ships
}

func runtimeStarterShipForInstallerClass(classKey string) (mmogShipSeed, bool) {
	switch strings.ToLower(strings.TrimSpace(classKey)) {
	case "assault":
		return starterShips[0], true
	case "dreadnought":
		return starterShips[1], true
	case "sniper":
		return starterShips[2], true
	case "support":
		return starterShips[3], true
	default:
		return mmogShipSeed{}, false
	}
}

func runtimeStarterShipForInstallerShipID(shipID int32) (mmogShipSeed, bool) {
	for _, pkg := range dreadconfig.InstallerStarterPackages() {
		if pkg.ShipID != shipID {
			continue
		}
		return runtimeStarterShipForInstallerClass(pkg.ClassKey)
	}
	return mmogShipSeed{}, false
}

func starterBootstrapShipByID(shipID int32) (mmogShipSeed, bool) {
	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		if loadout.ShipID != shipID {
			continue
		}
		item, ok := dreadconfig.ItemByID(loadout.ShipID)
		if !ok {
			return mmogShipSeed{}, false
		}
		for _, pkg := range dreadconfig.InstallerStarterPackages() {
			if pkg.ShipID != loadout.ShipID {
				continue
			}
			archetype, ok := starterShipArchetypes[pkg.ClassKey]
			if !ok {
				return mmogShipSeed{}, false
			}
			return mmogShipSeed{
				id:           loadout.ShipID,
				name:         item.DisplayName,
				classID:      archetype.classID,
				shipClass:    archetype.shipClass,
				weight:       1,
				manufacturer: archetype.manufacturer,
				owned:        true,
				nodeID:       loadout.ShipID,
				parentID:     0,
				nodeType:     0,
				unlockCost:   0,
				prereqID1:    0,
				prereqID2:    0,
				bIsNew:       false,
			}, true
		}
	}
	return mmogShipSeed{}, false
}

func starterBootstrapShips() []mmogShipSeed {
	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	ships := make([]mmogShipSeed, 0, len(sharedLoadouts))
	for _, loadout := range sharedLoadouts {
		ship, ok := starterBootstrapShipByID(loadout.ShipID)
		if !ok {
			panic("missing starter bootstrap ship metadata")
		}
		ships = append(ships, ship)
	}
	return ships
}

func starterShipLoadouts() []mmogShipLoadoutSeed {
	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	loadouts := make([]mmogShipLoadoutSeed, 0, len(sharedLoadouts))
	for idx, sharedLoadout := range sharedLoadouts {
		identity, ok := extractedStarterLoadoutIdentityForShip(sharedLoadout.ShipName)
		if !ok {
			panic("missing shared starter loadout identity")
		}
		ship, ok := starterBootstrapShipByID(sharedLoadout.ShipID)
		if !ok {
			panic("missing starter bootstrap ship")
		}
		loadoutMeta, ok := dreadconfig.ItemByID(sharedLoadout.LoadoutID)
		if !ok {
			panic("missing starter loadout metadata")
		}
		nativeID, ok := nativeStarterLoadoutID(sharedLoadout.LoadoutID)
		if !ok {
			panic("missing starter native loadout ID")
		}
		loadouts = append(loadouts, mmogShipLoadoutSeed{
			ship:              ship,
			fleetShipID:       fleetStarterShipIDsByPrecastID[sharedLoadout.LoadoutID],
			precastLoadoutID:  sharedLoadout.LoadoutID,
			nativeLoadoutID:   nativeID,
			loadoutIndex:      0,
			loadoutName:       loadoutMeta.DisplayName,
			position:          int32(idx),
			active:            true,
			weaponPrimaryID:   identity.weapons[0],
			weaponSecondaryID: identity.weapons[1],
			abilityIDs:        identity.abilities,
			perkIDs:           identity.perks,
		})
	}
	return loadouts
}

func starterFleetState() mmogFleetSeed {
	loadouts := starterShipLoadouts()
	return buildConfigBackedStarterFleet(loadouts)
}

func mmogFleetSeeds() []mmogFleetSeed {
	return buildConfigBackedFleetSeeds(starterShipLoadouts())
}

func activeMmogFleetSeeds() []mmogFleetSeed {
	return []mmogFleetSeed{starterFleetState()}
}

func starterShipIDs() []int32 {
	return starterFleetState().shipIDs()
}

func starterLoadoutIDs() []int32 {
	loadouts := starterShipLoadouts()
	ids := make([]int32, 0, len(loadouts))
	for _, loadout := range loadouts {
		ids = append(ids, loadout.loadoutID())
	}
	return ids
}

type mmogInventoryItemSeed struct {
	id           string
	name         string
	itemType     string
	externalID   string
	description  string
	itemID       int32
	shipID       int32
	loadoutID    int32
	manufacturer string
	slotName     string
	quantity     int32
}

func inventoryDisplayType(itemType string) string {
	switch itemType {
	case "ship":
		return "Ship"
	case "loadout":
		return "Loadout"
	case "weapon":
		return "Weapon"
	case "ability":
		return "Ability"
	case "perk":
		return "Perk"
	default:
		return itemType
	}
}

func starterSeedSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func starterOwnedInventorySeeds() []mmogInventoryItemSeed {
	sharedItems := dreadconfig.StarterInventoryItems()
	items := make([]mmogInventoryItemSeed, 0, len(sharedItems))
	for _, item := range sharedItems {
		ship, _ := starterBootstrapShipByID(item.ShipID)
		shipSlug := starterSeedSlug(item.ShipName)
		fallbackExternalID := item.Item.ItemType + "_" + shipSlug
		seed := mmogInventoryItemSeed{
			name:         item.Item.DisplayName,
			itemType:     item.Item.ItemType,
			externalID:   extractedMarketItemExternalID(item.Item.ItemID, fallbackExternalID),
			description:  item.Item.DisplayName + " starter " + item.Item.ItemType + " entitlement",
			itemID:       item.Item.ItemID,
			shipID:       item.ShipID,
			loadoutID:    item.LoadoutID,
			manufacturer: ship.manufacturer,
			slotName:     item.SlotName,
			quantity:     1,
		}
		switch item.Item.ItemType {
		case "ship":
			seed.id = "ship_" + starterSeedSlug(item.Item.DisplayName)
		case "loadout":
			seed.id = "loadout_" + starterSeedSlug(item.Item.DisplayName)
			seed.externalID = extractedMarketItemExternalID(item.Item.ItemID, seed.id)
			seed.description = item.Item.DisplayName + " starter loadout entitlement"
		default:
			seed.id = "item_" + shipSlug + "_" + item.SlotName
			seed.externalID = extractedMarketItemExternalID(item.Item.ItemID, seed.id)
		}
		items = append(items, seed)
	}
	return items
}

func buildMmogRequestResponsePayload(requestName string, playerPID string, payload []byte) []byte {
    switch requestName {
    // --- Progression & Career ---
    case "YA_PlayerFleets":
        return buildMmogPlayerFleetsPayload(playerPID)
    case "YA_RequestStaticFleetData":
        return buildMmogStaticFleetDataPayloadForPlayer(playerPID)
    case "YA_GetSeasonData":
        return buildMmogSeasonDataPayload()
    case "YA_GetSeasonProgress":
        return buildMmogSeasonProgressPayload()
    case "YA_PlayerGet":
        return buildMmogPlayerGetPayload(playerPID)
    case "YA_GetPlayerStatsCounterData":
        return buildMmogPlayerStatsCounterDataPayload()
    case "YA_GetPlayerProgression":
        return buildMmogPlayerProgressionPayload(playerPID)
    case "YA_GetTechTree":
        return buildMmogTechTreePayload()
    case "YA_GetCareerProgression":
        return buildMmogCareerProgressionPayload()
    case "YA_GetStaticCareerData":
        return buildMmogStaticCareerDataPayload() // No playerPID needed
    case "YA_GetFeatureToggle":
        return buildMmogFeatureTogglePayload()
    case "YA_GetGameConfigData":
        return buildMmogGameConfigDataPayload() // No playerPID needed
    case "YA_GetProgressionData":
        return buildMmogProgressionDataPayload()
    case "YA_GetPlayerPurchases":
        return buildMmogPlayerPurchasesPayloadForPlayer(playerPID) // Fixed: Added playerPID
    case "YA_GetScoringData":
        return buildMmogScoringDataPayload()
    case "YA_GetDailyContractsData":
        return buildMmogDailyContractsDataPayload()
    case "YA_GetBoosterData":
        return buildMmogBoosterDataPayload()
    case "YA_GetPlayerScores":
        return buildMmogPlayerScoresPayload()
    case "YA_GetPlayerStatistics":
        return buildMmogPlayerStatisticsPayload()
    case "YA_FleetEligibility":
        return buildMmogFleetEligibilityPayload()
    case "YA_Tune":
        return buildMmogTunePayload()

    // --- Matchmaking & Rooms ---
    case "YA_EnterMatchmaking", "YA_SquadEnterMatchmaking":
        return buildMmogEnterMatchmakingPayload(requestName, playerPID, payload)
    case "YA_LeaveMatchmaking":
        return buildMmogLeaveMatchmakingPayload(requestName, playerPID)
    case "YA_QueryRooms":
        return buildMmogQueryRoomsPayload()
    case "YA_RoomStart", "YA_CustomRoomCreate", "YA_CustomRoomStartMatch",
         "YA_CustomRoomStartMatchCountdown", "YA_CustomRoomCancelMatchCountdown",
         "YA_CustomRoomUserJoin", "YA_CustomRoomUserLeave", "YA_CustomRoomUserReturn",
         "YA_CustomRoomUserRemove", "YA_CustomRoomUserSwitchTeam",
         "YA_CustomRoomChangeHost", "YA_CustomRoomChangeSettings", "YA_CustomRoomUpdate",
         "YA_CustomRoomInvite", "YA_CustomRoomAnalyticsInvite",
         "YA_CustomRoomEnterFleetSelect", "YA_CustomRoomExitFleetSelect",
         "YA_RequeuingRoomStart":
        return buildMmogRoomSuccessPayload(mmogRoomResponseName(requestName))

    // --- Squads ---
    case "YA_SquadInvite", "YA_SquadAccept", "YA_SquadLeave", "YA_SquadEliteStatusUpdate":
        return buildMmogSquadPayload(requestName, playerPID)

    // --- Chat ---
    case "YA_Chat", "YA_GlobalChat", "YA_LanguageChat", "YA_ChatStatus",
         "YA_ChatMergeRequest", "YA_ChatJoinRequest", "YA_ChatAwayRemovalRequest",
         "YA_ChatAwayChange":
        return buildMmogChatPayload(requestName, playerPID, payload)

    // --- Fleet/Loadout Modifications ---
    case "YA_AddToFleet", "YA_RemoveFromFleet", "YA_SetFleetFlagship",
         "YA_ChargeFleet", "YA_RepairFleet", "YA_FleetUpdate",
         "YA_FleetAutoRepair", "YA_UpdateFleetMaintenance":
        return buildMmogRequestSuccessPayload(requestName)
    case "YA_UpdateShipLoadout", "YA_RenameShipLoadout", "YA_AddShipDefaultLoadouts":
        return buildMmogRequestSuccessPayload(requestName)

    // --- Navigation ---
    case "YA_RoomReturn", "YA_PlayAgain":
        return buildMmogRequestSuccessPayload(requestName)

    // --- Analytics ---
    case "YA_AnalyticsEvent", "YA_SaveCtAData", "YA_IncrementPlayerStatsCounter":
        return buildMmogRequestSuccessPayload(requestName)
    case "YA_AnalyticsBeginTransaction":
        transactionId := extractMmogStringField(payload, "transactionId")
        if transactionId == "" {
            return buildMmogErrorPayload("Missing transactionId for YA_AnalyticsBeginTransaction")
        }
        return buildMmogAnalyticsBeginTransactionPayload(transactionId)

    // --- Profile ---
    case "YA_RefreshPlayerProfile":
        return buildMmogPlayerDataPayload("YA_RefreshPlayerProfile", playerPID)
    case "YA_GetPlayersInformation":
        if len(payload) == 0 {
            return buildMmogErrorPayload("Empty payload for YA_GetPlayersInformation")
        }
        return buildMmogPlayersInformationPayload(playerPID, payload)

    // --- Connection ---
    case "YA_Connect":
        return buildMmogConnectPayload(playerPID)

    // --- Default ---
	default:
		logrus.WithField("request", requestName).Warn("unknown MMOG request")
        if strings.HasPrefix(requestName, "YA_Get") {
            return buildMmogErrorPayload("Unknown read command: " + requestName)
        }
        return buildMmogRequestSuccessPayload(requestName)
    }
}

type mmogMatchmakingStatus struct {
	entryID    string
	state      string
	gameMode   string
	mapName    string
	matchID    string
	serverIP   string
	serverPort int32
}

func buildMmogEnterMatchmakingPayload(requestName string, playerPID string, payload []byte) []byte {
	pid := normalizedPlayerStatePID(playerPID)
	status := currentMmogMatchmakingStatus(pid)
	if status.state == "matched" {
		return buildMmogMatchmakingPayload(requestName, status)
	}

	gameMode := firstNonEmptyMmogString(payload, "GameMode", "gameMode", "Mode", "mode", "matchmaking")
	if gameMode == "" || gameMode == "*matchmaking" {
		gameMode = "TeamDeathmatch"
	}
	tierMin := firstMmogInt32(payload, 1, "TierMin", "tierMin", "minTier", "MinTier")
	tierMax := firstMmogInt32(payload, 5, "TierMax", "tierMax", "maxTier", "MaxTier")

	entryID := uuid.New().String()
	database := currentMmogPlayerStateDB()
	if database != nil {
		_, _ = database.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, pid)
		if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,status) VALUES(?,?,?,?,?,'waiting')`,
			entryID, pid, gameMode, tierMin, tierMax); err != nil {
			return buildMmogMatchmakingErrorPayload(requestName, 2, "queue insert failed")
		}
	}

	return buildMmogMatchmakingPayload(requestName, mmogMatchmakingStatus{
		entryID:  entryID,
		state:    "waiting",
		gameMode: gameMode,
	})
}

func buildMmogLeaveMatchmakingPayload(requestName string, playerPID string) []byte {
	pid := normalizedPlayerStatePID(playerPID)
	if database := currentMmogPlayerStateDB(); database != nil {
		if _, err := database.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, pid); err != nil {
			return buildMmogMatchmakingErrorPayload(requestName, 2, "queue leave failed")
		}
	}
	return buildMmogMatchmakingPayload(requestName, mmogMatchmakingStatus{state: "left"})
}

func currentMmogMatchmakingStatus(playerPID string) mmogMatchmakingStatus {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return mmogMatchmakingStatus{state: "idle"}
	}

	var matched mmogMatchmakingStatus
	err := database.QueryRow(`
		SELECT m.id,m.server_ip,m.server_port,m.game_mode,m.map
		FROM match_slots ms
		JOIN matches m ON ms.match_id=m.id
		WHERE ms.user_id=? AND m.status='active'
		ORDER BY ms.joined_at DESC
		LIMIT 1
	`, playerPID).Scan(&matched.matchID, &matched.serverIP, &matched.serverPort, &matched.gameMode, &matched.mapName)
	if err == nil {
		matched.state = "matched"
		return matched
	}
	if err != sql.ErrNoRows {
		return mmogMatchmakingStatus{state: "idle"}
	}

	var queued mmogMatchmakingStatus
	err = database.QueryRow(`
		SELECT id,game_mode FROM queue_entries
		WHERE user_id=? AND status='waiting'
		ORDER BY queued_at DESC
		LIMIT 1
	`, playerPID).Scan(&queued.entryID, &queued.gameMode)
	if err == nil {
		queued.state = "waiting"
		return queued
	}
	return mmogMatchmakingStatus{state: "idle"}
}

func buildMmogMatchmakingPayload(requestName string, status mmogMatchmakingStatus) []byte {
	var b []byte
	var stack []int
	if status.state == "" {
		status.state = "ok"
	}
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogStringField(b, "matchmakingStatus", status.state)
	b = appendMmogStringField(b, "state", status.state)
	b = appendMmogInt32Field(b, "Code", 0)
	if status.entryID != "" {
		b = appendMmogStringField(b, "queueId", status.entryID)
		b = appendMmogStringField(b, "entry_id", status.entryID)
	}
	if status.gameMode != "" {
		b = appendMmogStringField(b, "gameMode", status.gameMode)
		b = appendMmogStringField(b, "GameMode", status.gameMode)
	}
	if status.matchID != "" {
		b = appendMmogStringField(b, "matchId", status.matchID)
		b = appendMmogStringField(b, "MatchID", status.matchID)
	}
	if status.serverIP != "" {
		b = appendMmogStringField(b, "serverIP", status.serverIP)
		b = appendMmogStringField(b, "serverHost", status.serverIP)
	}
	if status.serverPort != 0 {
		b = appendMmogInt32Field(b, "serverPort", status.serverPort)
	}
	if status.mapName != "" {
		b = appendMmogStringField(b, "map", status.mapName)
		b = appendMmogStringField(b, "Map", status.mapName)
	}
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogErrorPayload(message string) []byte {
	var b []byte
	var stack []int
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "error")
	b = appendMmogStringField(b, "message", message)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogMatchmakingErrorPayload(requestName string, code int32, message string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "error")
	b = appendMmogInt32Field(b, "Code", code)
	b = appendMmogStringField(b, "message", message)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogQueryRoomsPayload() []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_QueryRooms")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogInt32Field(b, "Code", 0)
	b, stack = appendMmogArrayStart(b, stack, "Rooms")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "rooms")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogRoomSuccessPayload(requestName string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogInt32Field(b, "Code", 0)
	b, stack = appendMmogObjectStart(b, stack, "Room")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func mmogRoomResponseName(requestName string) string {
	switch requestName {
	case "YA_CustomRoomCreate":
		return "YA_CustomRoomCreateResponse"
	case "YA_CustomRoomStartMatch":
		return "YA_CustomRoomStartMatchResponse"
	case "YA_CustomRoomUserJoin":
		return "YA_CustomRoomUserJoinResponse"
	case "YA_CustomRoomUserLeave":
		return "YA_CustomRoomUserLeaveResponse"
	case "YA_CustomRoomUserReturn":
		return "YA_CustomRoomUserReturnResponse"
	case "YA_CustomRoomUserSwitchTeam":
		return "YA_CustomRoomUserSwitchTeamResponse"
	case "YA_CustomRoomChangeHost":
		return "YA_CustomRoomChangeHostResponse"
	case "YA_CustomRoomChangeSettings":
		return "YA_CustomRoomChangeSettingsResponse"
	case "YA_CustomRoomUpdate":
		return "YA_CustomRoomUpdateResponse"
	case "YA_CustomRoomEnterFleetSelect":
		return "YA_CustomRoomEnterFleetSelectResponse"
	case "YA_CustomRoomExitFleetSelect":
		return "YA_CustomRoomExitFleetSelectResponse"
	default:
		return requestName
	}
}

func buildMmogSquadPayload(requestName string, playerPID string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogInt32Field(b, "Code", 0)
	b = appendMmogStringField(b, "PID", normalizedPlayerStatePID(playerPID))
	b, stack = appendMmogArrayStart(b, stack, "Squad")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Members")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogChatPayload(requestName string, playerPID string, payload []byte) []byte {
	channel := firstNonEmptyMmogString(payload, "channelName", "Channel", "channel")
	if channel == "" {
		channel = "global"
	}
	message := firstNonEmptyMmogString(payload, "message", "Message", "content", "Content", "text", "Text")
	if message != "" {
		persistMmogChatMessage(normalizedPlayerStatePID(playerPID), channel, message)
	}

	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", requestName)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogInt32Field(b, "Code", 0)
	b = appendMmogStringField(b, "channelName", channel)
	b, stack = appendMmogArrayStart(b, stack, "Messages")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func persistMmogChatMessage(playerPID string, channel string, message string) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	_, _ = database.Exec(`INSERT INTO chat_messages(id,channel,sender_id,content) VALUES(?,?,?,?)`,
		uuid.New().String(), channel, playerPID, message)
}

func firstNonEmptyMmogString(payload []byte, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(extractMmogStringField(payload, name)); value != "" {
			return value
		}
	}
	return ""
}

func firstMmogInt32(payload []byte, fallback int32, names ...string) int32 {
	for _, name := range names {
		if value, ok := extractMmogInt32Field(payload, name); ok {
			return value
		}
	}
	return fallback
}

func isMmogPlayerMutationRequest(requestName string) bool {
	switch requestName {
	case "YA_SavePlayerDisplayInformation",
		"YA_AddToFleet", "YA_RemoveFromFleet", "YA_SetFleetFlagship",
		"YA_UpdateShipLoadout", "YA_RenameShipLoadout", "YA_AddShipDefaultLoadouts":
		return true
	default:
		return false
	}
}

func appendMmogFleetRawFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b = appendMmogInt32Field(b, "fleet id", fleet.fleetID)
	b = appendMmogInt32Field(b, "FleetType", fleet.fleetType)
	b, stack = appendMmogInt32ArrayField(b, stack, "shipIds", fleet.shipIDs())
	b, stack = appendMmogBoolArrayField(b, stack, "ShipTechTreeComplete", fleet.shipTechTreeComplete())
	b = appendMmogInt32Field(b, "FlagShipID", fleet.flagshipShipID)
	b = appendMmogInt32Field(b, "FlagShipLoadoutID", fleet.flagshipLoadoutID)
	b = appendMmogInt32Field(b, "FlagShipLoadoutIndex", fleet.flagshipLoadoutIndex)
	return b, stack
}

func appendMmogFleetRuntimeFields(b []byte, fleet mmogFleetSeed) []byte {
	b = appendMmogBoolField(b, "AutoRepair", false)
	b = appendMmogBoolField(b, "Maintenance", false)
	b = appendMmogInt32Field(b, "LastWinTime", 0)
	b = appendMmogInt32Field(b, "ChargingBeginTime", 0)
	b = appendMmogInt32Field(b, "ChargingCharges", 1)
	b = appendMmogInt32Field(b, "Rating", 0)
	return b
}

func appendMmogFleetBackendFields(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b = appendMmogInt32Field(b, "m_fleetId", fleet.fleetID)
	b = appendMmogInt32Field(b, "m_flagshipIndex", fleet.flagshipIndex())
	b = appendMmogInt32Field(b, "m_fleetType", fleet.fleetType)
	b, stack = appendMmogInt32ArrayField(b, stack, "m_loadoutList", fleet.loadoutIDs())
	return b, stack
}

func appendMmogPlayerFleetEntry(b []byte, stack []int, playerPID string, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "FID", fleet.token)
	b = appendMmogStringField(b, "PID", playerPID)
	b = appendMmogStringField(b, "FleetID", fleet.token)
	b = appendMmogStringField(b, "Name", fleet.displayName)
	b = appendMmogInt32Field(b, "FleetType", fleet.fleetType)
	b = appendMmogInt32Field(b, "shipCount", int32(len(fleet.shipLoadouts)))
	b = appendMmogFleetRuntimeFields(b, fleet)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b = appendMmogInt32Field(b, "flagshipShipId", fleet.flagshipShipID)
	b = appendMmogInt32Field(b, "flagshipLoadoutID", fleet.flagshipLoadoutID)
	b = appendMmogInt32Field(b, "flagshipLoadoutIndex", fleet.flagshipLoadoutIndex)
	b = appendMmogInt32Field(b, "flagshipID", fleet.flagshipLoadoutID)
	b, stack = appendMmogFleetBackendFields(b, stack, fleet)
	b = appendMmogBoolField(b, "bIsActive", fleet.active)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayerFleetsPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)

	b = appendMmogStringField(b, "RT", "YA_PlayerFleets")
	b, stack = appendMmogArrayStart(b, stack, "result")
	for _, fleet := range state.activeFleets() {
		b, stack = appendMmogPlayerFleetEntry(b, stack, playerPID, fleet.flagshipOnly())
	}
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogStaticFleetTypeEntry(b []byte, stack []int, eligibility dreadconfig.FleetEligibility) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "ID", eligibility.FleetType)
	b = appendMmogInt32Field(b, "ShipsToUnlock", eligibility.NumShipsToUnlockFleet)
	b = appendMmogInt32Field(b, "BaseMaintenanceCost", eligibility.BaseMaintenanceCost)
	b = appendMmogStringField(b, "FleetRatingMin", strconv.FormatFloat(eligibility.FleetRatingMin, 'f', 1, 64))
	b = appendMmogInt32Field(b, "FleetRatingCost", eligibility.FleetRatingCost)
	b = appendMmogInt32Field(b, "ChargeTime", eligibility.MaintenanceTime)
	b = appendMmogInt32Field(b, "ChargeCost", 0)
	b = appendMmogInt32Field(b, "AvailableCharges", 1)
	b, stack = appendMmogArrayStart(b, stack, "Tiers")
	for _, tier := range eligibility.AllowedTiers {
		b = appendMmogUnnamedInt32Field(b, tier)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetMaintenanceConfig(b []byte, stack []int) ([]byte, []int) {
	b, stack = appendMmogObjectStart(b, stack, "Maintenance")
	b = appendMmogStringField(b, "EliteCostMultiplier", "1.0")
	b = appendMmogStringField(b, "NonEliteCostMultiplier", "1.0")
	b = appendMmogInt32Field(b, "TopPlayerCount", 0)
	b = appendMmogStringField(b, "TopPlayerCostMultiplier", "1.0")
	b = appendMmogStringField(b, "NonTopPlayerCostMultiplier", "1.0")
	b = appendMmogStringField(b, "WinningCostMultiplier", "1.0")
	b = appendMmogStringField(b, "LoosingCostMultiplier", "1.0")
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetSlotEntry(b []byte, stack []int, loadout mmogShipLoadoutSeed, flagshipShipID int32) ([]byte, []int) {
	loadoutID := loadout.loadoutID()
	fleetShipID := loadout.effectiveFleetShipID()
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "ShipID", fleetShipID)
	b = appendMmogInt32Field(b, "shipID", fleetShipID)
	b = appendMmogInt32Field(b, "LoadoutID", loadoutID)
	b = appendMmogInt32Field(b, "loadoutID", loadoutID)
	b = appendMmogInt32Field(b, "Position", loadout.position)
	b = appendMmogBoolField(b, "bIsFlagship", fleetShipID == flagshipShipID)
	b = appendMmogInt32Field(b, "Status", 0)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogStaticFleetEntry(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "FID", fleet.token)
	b = appendMmogStringField(b, "FleetID", fleet.token)
	b = appendMmogStringField(b, "Name", fleet.displayName)
	b = appendMmogBoolField(b, "bIsActive", fleet.active)
	b, stack = appendMmogFleetRawFields(b, stack, fleet)
	b, stack = appendMmogFleetBackendFields(b, stack, fleet)
	b, stack = appendMmogArrayStart(b, stack, "ShipSlots")
	for _, loadout := range fleet.shipLoadouts {
		b, stack = appendMmogStaticFleetSlotEntry(b, stack, loadout, fleet.flagshipShipID)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogStaticFleetDataPayload() []byte {
	return buildMmogStaticFleetDataPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogStaticFleetDataPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)

	b = appendMmogStringField(b, "RT", "YA_RequestStaticFleetData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "FleetTypes")
	for _, eligibility := range configBackedFleetEligibilities() {
		b, stack = appendMmogStaticFleetTypeEntry(b, stack, eligibility)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogStaticFleetMaintenanceConfig(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Fleets")
	for _, fleet := range state.activeFleets() {
		b, stack = appendMmogStaticFleetEntry(b, stack, fleet.flagshipOnly())
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "ShipLoadouts")
	for _, loadout := range customMmogShipLoadoutsForPayload(state.shipLoadouts()) {
		b, stack = appendMmogStaticShipLoadout(b, stack, loadout)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogSeasonDataPayload() []byte {
	var b []byte
	var stack []int
	seasonsJSON := mustMarshalSeasonTableJSON([]mmogSeasonDataTableRow{
		{
			RowName:      "PVE_Season1",
			Active:       false,
			Name:         "Miner Inconvenience",
			DescShort:    "Season 1 short Description",
			DescLong:     "Season 1 long Description",
			ImageLarge:   "",
			ImageSmall:   "",
			RewardLevels: []map[string]any{},
		},
		{
			RowName:      "PVE_Season3",
			Active:       false,
			Name:         "Battleship Down",
			DescShort:    "Season 3 short Description",
			DescLong:     "Season 3 long Description",
			ImageLarge:   "",
			ImageSmall:   "",
			RewardLevels: []map[string]any{},
		},
	})
	eventsJSON := mustMarshalSeasonTableJSON([]mmogEventDataTableRow{
		{
			RowName:       "PVE_S1E1",
			Name:          "Incident Management",
			DescShort:     "Miner Inconvenience - Incident Management",
			DescLong:      "Jupiter Arms installations on the surface of Io have been under attack for weeks by raiding parties using hit and run tactics.",
			Map:           "",
			MapParameters: "",
			GameMode:      "YGMT_HORDE",
			Color:         mmogDataTableColor{R: 160, G: 144, B: 131, A: 255},
			ImageSmall:    "",
			ImageLarge:    "",
			RewardLevels:  []map[string]any{},
			StartDate:     "2018.05.16-16.00.00",
			EndDate:       "2018.05.16-16.19.59",
			Season:        "PVE_Season1",
		},
	})

	b = appendMmogStringField(b, "RT", "YA_GetSeasonData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, "Events", eventsJSON)
	b = appendMmogStringField(b, "Seasons", seasonsJSON)
	b = appendMmogStringField(b, "CurrentSeason", "")
	b = appendMmogStringField(b, "ActiveEvent", "")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

type mmogSeasonDataTableRow struct {
	RowName      string           `json:"Name"`
	Active       bool             `json:"m_active"`
	Name         string           `json:"m_name"`
	DescShort    string           `json:"m_descShort"`
	DescLong     string           `json:"m_descLong"`
	ImageLarge   string           `json:"m_imageLarge"`
	ImageSmall   string           `json:"m_imageSmall"`
	RewardLevels []map[string]any `json:"m_rewardLevels"`
}

type mmogEventDataTableRow struct {
	RowName       string             `json:"Name"`
	Name          string             `json:"m_name"`
	DescShort     string             `json:"m_descShort"`
	DescLong      string             `json:"m_descLong"`
	Map           string             `json:"m_map"`
	MapParameters string             `json:"m_mapParameters"`
	GameMode      string             `json:"m_gameMode"`
	Color         mmogDataTableColor `json:"m_color"`
	ImageSmall    string             `json:"m_imageSmall"`
	ImageLarge    string             `json:"m_imageLarge"`
	RewardLevels  []map[string]any   `json:"m_rewardLevels"`
	StartDate     string             `json:"m_startDate"`
	EndDate       string             `json:"m_endDate"`
	Season        string             `json:"m_season"`
}

type mmogDataTableColor struct {
	R int32 `json:"r"`
	G int32 `json:"g"`
	B int32 `json:"b"`
	A int32 `json:"a"`
}

func mustMarshalSeasonTableJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic("marshal season data payload: " + err.Error())
	}
	return string(data)
}
func buildMmogSeasonProgressPayload() []byte {
	var b []byte
	var stack []int // Track nesting for objects/arrays

	// Add routing tag (command name)
	b = appendMmogStringField(b, "RT", "YA_GetSeasonProgress")

	// Start the "result" object
	b, stack = appendMmogObjectStart(b, stack, "result")

	// Add empty arrays for season data
	b, stack = appendMmogArrayStart(b, stack, "EventScores")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "EventRewards")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "SeasonRewards")
	b, stack = appendMmogObjectEnd(b, stack)

	// Close the "result" object
	b, stack = appendMmogObjectEnd(b, stack)

	// Validate stack is empty (all objects/arrays closed)
	if len(stack) != 0 {
		return nil // or panic("Unbalanced MMoG payload")
	}

	return b
}

func buildMmogPlayerGetPayload(playerPID string) []byte {
	return buildMmogPlayerDataPayload("YA_PlayerGet", playerPID)
}

// buildMmogPlayerDataPayload builds the full player data payload with a configurable RT field.
// Used by both YA_PlayerGet and YA_RefreshPlayerProfile (which must echo back the correct RT).
func buildMmogPlayerDataPayload(rt string, playerPID string) []byte {
	var b []byte
	var stack []int
	now := int32(time.Now().Unix())
	membershipExpiresAt := now + 31536000
	state := mmogPlayerStateForPID(playerPID)
	starterFleet := state.activeFleet().flagshipOnly()

	b = appendMmogStringField(b, "RT", rt)
	b = appendMmogStringField(b, "PID", playerPID)
	b = appendMmogStringField(b, "SID", "local_session")
	b = appendMmogInt32Field(b, "tll", 1)
	b = appendMmogInt32Field(b, "tpl", 1)
	b = appendMmogInt32Field(b, "gl", state.softCurrency)
	b = appendMmogInt32Field(b, "ob", state.premiumCurrency)
	b = appendMmogInt32Field(b, "rep", 0)
	b = appendMmogInt32Field(b, "repDN_L", 0)
	b = appendMmogInt32Field(b, "repDN_M", 0)
	b = appendMmogInt32Field(b, "repDN_H", 0)
	b = appendMmogInt32Field(b, "repAS_L", 0)
	b = appendMmogInt32Field(b, "repAS_M", 0)
	b = appendMmogInt32Field(b, "repAS_H", 0)
	b = appendMmogInt32Field(b, "repSC_L", 0)
	b = appendMmogInt32Field(b, "repSC_M", 0)
	b = appendMmogInt32Field(b, "repSC_H", 0)
	b = appendMmogInt32Field(b, "repSN_L", 0)
	b = appendMmogInt32Field(b, "repSN_M", 0)
	b = appendMmogInt32Field(b, "repSN_H", 0)
	b = appendMmogInt32Field(b, "repSU_L", 0)
	b = appendMmogInt32Field(b, "repSU_M", 0)
	b = appendMmogInt32Field(b, "repSU_H", 0)
	b = appendMmogInt32Field(b, "ReputationGoalID", 0)
	b = appendMmogStringField(b, "disp", "")
	b = appendMmogStringField(b, "motto", "")
	b = appendMmogStringField(b, "SGD", "")
	b = appendMmogStringField(b, "SCtA", "")
	b = appendMmogStringField(b, "LGVersion", "0")
	b, stack = appendMmogObjectStart(b, stack, "Membership")
	b = appendMmogInt32Field(b, "ExpireTime", membershipExpiresAt)
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogInt32Field(b, "DailyContractStateID", 0)
	b = appendMmogInt32Field(b, "LastContractsAssignment", 0)
	b = appendMmogInt32Field(b, "DailyContractLastReplaceTime", 0)
	b = appendMmogInt32Field(b, "FreeXp", state.freeXP)
	b, stack = appendMmogArrayStart(b, stack, "ShipXps")
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogInt32Field(b, "ServerTime", now)
	b = appendMmogInt32Field(b, "ClientTime", now)
	b = appendMmogStringField(b, "PublicIP", "")
	b = appendMmogStringField(b, "Country", "")
	b = appendMmogStringField(b, "Platform", "steam")
	b, stack = appendMmogObjectStart(b, stack, "CustomRoom")
	b = appendMmogStringField(b, "roomId", "")
	b = appendMmogStringField(b, "hostPid", "")
	b, stack = appendMmogArrayStart(b, stack, "teams")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "settings")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "supportedModes")
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogStringField(b, "gameMode", "")
	b = appendMmogStringField(b, "mapName", "")
	b, stack = appendMmogArrayStart(b, stack, "supportedMaps")
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogStringField(b, "chatRoomId", "")
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogStringField(b, "FleetID", starterFleet.token)
	b = appendMmogStringField(b, "fleetId", starterFleet.token)
	b = appendMmogInt32Field(b, "fleet id", starterFleet.fleetID)
	b = appendMmogInt32Field(b, "FleetType", starterFleet.fleetType)
	b = appendMmogInt32Field(b, "shipId", starterFleet.flagshipShipID)
	b, stack = appendMmogInt32ArrayField(b, stack, "shipIds", starterFleet.shipIDs())
	b, stack = appendMmogBoolArrayField(b, stack, "ShipTechTreeComplete", starterFleet.shipTechTreeComplete())
	b = appendMmogInt32Field(b, "FlagShipID", starterFleet.flagshipShipID)
	b = appendMmogInt32Field(b, "FlagShipLoadoutID", starterFleet.flagshipLoadoutID)
	b = appendMmogInt32Field(b, "FlagShipLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b = appendMmogInt32Field(b, "selectedLoadoutID", starterFleet.flagshipLoadoutID)
	b = appendMmogInt32Field(b, "selectedLoadoutIndex", starterFleet.flagshipLoadoutIndex)
	b = appendMmogInt32Field(b, "flagshipID", starterFleet.flagshipLoadoutID)
	b, stack = appendMmogFleetBackendFields(b, stack, starterFleet)
	b, stack = appendMmogArrayStart(b, stack, "FactionReputation")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Officers")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "ShipLoadouts")
	for _, loadout := range customMmogShipLoadoutsForPayload(state.shipLoadouts()) {
		b, stack = appendMmogShipLoadout(b, stack, playerPID, loadout)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Ribbons")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Medals")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Friends")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "Squad")
	b = appendMmogStringField(b, "PID", "")
	b = appendMmogStringField(b, "PIDLeader", "")
	b, stack = appendMmogArrayStart(b, stack, "Users")
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogStringField(b, "GameMode", "")
	b = appendMmogInt32Field(b, "State", 0)
	b = appendMmogInt32Field(b, "FleetType", 0)
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogStringField(b, "PPF", "")
	b = appendMmogInt32Field(b, "tslm", 0)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogShipLoadoutEntry(b []byte, stack []int, playerPID string, loadout mmogShipLoadoutSeed, includePID bool) ([]byte, []int) {
	loadoutID := loadout.loadoutID()
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "ID", loadout.entryID())
	if includePID {
		b = appendMmogStringField(b, "PID", playerPID)
	}
	b = appendMmogInt32Field(b, "LoadoutID", loadoutID)
	b = appendMmogInt32Field(b, "m_loadoutID", loadoutID)
	b = appendMmogInt32Field(b, "precastLoadout", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogBoolField(b, "m_isActiveLoadout", loadout.active)
	b = appendMmogStringField(b, "name", loadout.loadoutName)
	b = appendMmogStringField(b, "m_loadoutName", loadout.loadoutName)
	b = appendMmogInt32Field(b, "shipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "m_shipId", loadout.ship.id)
	b = appendMmogInt32Field(b, "class", loadout.ship.shipClass)
	b = appendMmogStringField(b, "m_name", loadout.loadoutName)
	b = appendMmogInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = appendMmogStringField(b, "displayInfo", loadout.displayInfo())
	b = appendMmogStringField(b, "m_displayInfo", loadout.displayInfo())
	b = appendMmogInt32Field(b, "m_loadoutTier", 1)
	b = appendMmogBoolField(b, "m_loadoutComplete", loadout.complete())
	b = appendMmogInt32Field(b, "weaponPrimary", loadout.weaponPrimaryItemID())
	b = appendMmogInt32Field(b, "weaponSecondary", loadout.weaponSecondaryItemID())
	b = appendMmogInt32Field(b, "abilityPrimary", loadout.abilityItemID(0))
	b = appendMmogInt32Field(b, "abilitySecondary", loadout.abilityItemID(1))
	b = appendMmogInt32Field(b, "abilityPerimeter", loadout.abilityItemID(2))
	b = appendMmogInt32Field(b, "abilityInternal", loadout.abilityItemID(3))
	b = appendMmogInt32Field(b, "perkCom", loadout.perkItemID(0))
	b = appendMmogInt32Field(b, "perkWeapon", loadout.perkItemID(1))
	b = appendMmogInt32Field(b, "perkNavigation", loadout.perkItemID(2))
	b = appendMmogInt32Field(b, "perkEngineer", loadout.perkItemID(3))
	b = appendMmogInt32Field(b, "m_primaryWeaponItemId", loadout.weaponPrimaryItemID())
	b = appendMmogInt32Field(b, "m_secondaryWeaponItemId", loadout.weaponSecondaryItemID())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_weaponIDs", loadout.weaponIDs())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_abilityIDs", loadout.abilityItemIDs())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_perkIDs", loadout.perkItemIDs())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_abilityItemIds", loadout.abilityItemIDs())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_perkIds", loadout.perkItemIDs())
	b, stack = appendMmogStringArrayField(b, stack, "m_perkNames", loadout.perkNames())
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogShipLoadout(b []byte, stack []int, playerPID string, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogShipLoadoutEntry(b, stack, playerPID, loadout, true)
}

func appendMmogStaticShipLoadout(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogShipLoadoutEntry(b, stack, "", loadout, false)
}

func customMmogShipLoadoutsForPayload(loadouts []mmogShipLoadoutSeed) []mmogShipLoadoutSeed {
	custom := make([]mmogShipLoadoutSeed, 0, len(loadouts))
	for _, loadout := range loadouts {
		if isDefaultStarterShipLoadout(loadout) {
			continue
		}
		custom = append(custom, loadout)
	}
	return custom
}

func isDefaultStarterShipLoadout(loadout mmogShipLoadoutSeed) bool {
	starter, ok := starterLoadoutByPrecastID(loadout.precastLoadoutID)
	if !ok {
		return false
	}
	if loadout.loadoutID() != starter.loadoutID() ||
		loadout.entryID() != starter.entryID() ||
		loadout.ship.id != starter.ship.id ||
		loadout.loadoutName != starter.loadoutName ||
		loadout.weaponPrimaryItemID() != starter.weaponPrimaryItemID() ||
		loadout.weaponSecondaryItemID() != starter.weaponSecondaryItemID() {
		return false
	}
	for idx := range loadout.abilityIDs {
		if loadout.abilityItemID(idx) != starter.abilityItemID(idx) {
			return false
		}
	}
	for idx := range loadout.perkIDs {
		if loadout.perkItemID(idx) != starter.perkItemID(idx) {
			return false
		}
	}
	return true
}

func starterLoadoutByPrecastID(precastLoadoutID int32) (mmogShipLoadoutSeed, bool) {
	for _, loadout := range starterShipLoadouts() {
		if loadout.precastLoadoutID == precastLoadoutID {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}

func starterLoadoutByShipID(shipID int32) (mmogShipLoadoutSeed, bool) {
	for _, loadout := range starterShipLoadouts() {
		if loadout.ship.id == shipID {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}

func appendMmogOwnedShipLoadoutsArray(b []byte, stack []int, name string, playerPID string, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, loadout := range fleet.shipLoadouts {
		b, stack = appendMmogOwnedShipLoadoutEntry(b, stack, playerPID, loadout)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogOwnedShipLoadoutEntry(b []byte, stack []int, playerPID string, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "ID", loadout.entryID())
	b = appendMmogStringField(b, "PID", playerPID)
	b = appendMmogStringField(b, "LoadoutName", loadout.loadoutName)
	b = appendMmogInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "loadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "shipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "ShipID", loadout.ship.id)
	b = appendMmogBoolField(b, "isActiveLoadout", false)
	b = appendMmogBoolField(b, "m_isActiveLoadout", false)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogShipLoadoutInfoEntry(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b, stack = appendMmogShipLoadoutInfoFields(b, stack, loadout)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogShipLoadoutInfoFields(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b = appendMmogStringField(b, "ID", loadout.entryID())
	b = appendMmogStringField(b, "m_loadoutName", loadout.loadoutName)
	b = appendMmogInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "loadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "m_loadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
	b = appendMmogInt32Field(b, "shipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "ShipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "m_shipId", loadout.ship.id)
	b = appendMmogInt32Field(b, "loadoutIndex", loadout.loadoutIndex)
	b = appendMmogInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = appendMmogStringField(b, "m_displayInfo", loadout.displayInfo())
	b = appendMmogInt32Field(b, "m_loadoutTier", 1)
	b = appendMmogBoolField(b, "m_loadoutComplete", loadout.complete())
	b = appendMmogInt32Field(b, "m_primaryWeaponItemId", loadout.weaponPrimaryItemID())
	b = appendMmogInt32Field(b, "m_secondaryWeaponItemId", loadout.weaponSecondaryItemID())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_abilityItemIds", loadout.abilityItemIDs())
	b, stack = appendMmogInt32ArrayField(b, stack, "m_perkIds", loadout.perkItemIDs())
	b, stack = appendMmogStringArrayField(b, stack, "m_perkNames", loadout.perkNames())
	return b, stack
}

func appendMmogPreviewLoadoutItemsArray(b []byte, stack []int, name string, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogLoadoutItemStateArray(b, stack, name, loadout, "preview")
}

func appendMmogLoadoutItemStateArray(b []byte, stack []int, name string, loadout mmogShipLoadoutSeed, state string) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, slot := range loadout.loadoutSlots() {
		b, stack = appendMmogLoadoutItemStateEntry(b, stack, loadout, slot, state)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogLoadoutItemStateEntry(b []byte, stack []int, loadout mmogShipLoadoutSeed, slot mmogLoadoutItemSeed, state string) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "ShipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "ItemID", slot.itemID)
	b = appendMmogInt32Field(b, "itemID", slot.itemID)
	b = appendMmogStringField(b, "SlotName", slot.slotName)
	b = appendMmogStringField(b, "Headline", slot.headline)
	b = appendMmogStringField(b, "Description", slot.description)
	b = appendMmogStringField(b, "ItemType", slot.itemType)
	b = appendMmogInt32Field(b, "Position", slot.position)
	b = appendMmogInt32Field(b, "ItemTier", slot.itemTier)
	b = appendMmogBoolField(b, "bIsOwned", true)
	b = appendMmogBoolField(b, "bIsEquipped", state == "equipped")
	b = appendMmogBoolField(b, "bCanEquip", true)
	b = appendMmogBoolField(b, "bIsPreview", state == "preview")
	b = appendMmogBoolField(b, "bIsPlaceholder", slot.itemID == 0)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogNamedLoadoutDetail(b []byte, stack []int, name string, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b, stack = appendMmogObjectStart(b, stack, name)
	b, stack = appendMmogLoadoutDetailContent(b, stack, loadout)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogUnnamedLoadoutDetail(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b, stack = appendMmogLoadoutDetailContent(b, stack, loadout)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogLoadoutDetailContent(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b = appendMmogStringField(b, "name", loadout.loadoutName)
	b = appendMmogInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "ShipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "weaponCount", int32(len(loadout.weaponSlots())))
	b = appendMmogInt32Field(b, "moduleCount", int32(len(loadout.abilitySlots())))
	b = appendMmogInt32Field(b, "officerBriefingCount", int32(len(loadout.perkSlots())))
	b, stack = appendMmogArrayStart(b, stack, "weapons")
	for _, slot := range loadout.weaponSlots() {
		b, stack = appendMmogLoadoutDetailSlotEntry(b, stack, loadout, slot)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "modules")
	for _, slot := range loadout.abilitySlots() {
		b, stack = appendMmogLoadoutDetailSlotEntry(b, stack, loadout, slot)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "officerBriefings")
	for _, slot := range loadout.perkSlots() {
		b, stack = appendMmogLoadoutDetailSlotEntry(b, stack, loadout, slot)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogLoadoutDetailSlotEntry(b []byte, stack []int, loadout mmogShipLoadoutSeed, slot mmogLoadoutItemSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "SlotName", slot.slotName)
	b = appendMmogStringField(b, "Headline", slot.headline)
	b = appendMmogStringField(b, "Description", slot.description)
	b = appendMmogStringField(b, "IconImagePath", "")
	b = appendMmogStringField(b, "ItemType", slot.itemType)
	b = appendMmogInt32Field(b, "LoadoutID", loadout.loadoutID())
	b = appendMmogInt32Field(b, "ShipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "ItemID", slot.itemID)
	b = appendMmogInt32Field(b, "Position", slot.position)
	b = appendMmogInt32Field(b, "ItemTier", slot.itemTier)
	b = appendMmogBoolField(b, "bIsPlaceholder", slot.itemID == 0)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogOwnedLoadoutItem(b []byte, stack []int, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	return appendMmogOwnedInventoryItem(b, stack, mmogInventoryItemSeed{
		id:           loadout.entryID(),
		name:         loadout.loadoutName,
		itemType:     "loadout",
		externalID:   loadout.entryID(),
		description:  loadout.loadoutName + " starter loadout entitlement",
		itemID:       loadout.precastLoadoutID,
		shipID:       loadout.ship.id,
		loadoutID:    loadout.loadoutID(),
		manufacturer: loadout.ship.manufacturer,
		quantity:     1,
	})
}

func appendMmogOwnedInventoryItem(b []byte, stack []int, item mmogInventoryItemSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "ID", item.id)
	b = appendMmogStringField(b, "Name", item.name)
	b = appendMmogStringField(b, "Type", inventoryDisplayType(item.itemType))
	b = appendMmogInt32Field(b, "ItemID", item.itemID)
	b = appendMmogInt32Field(b, "itemID", item.itemID)
	b = appendMmogStringField(b, "ExternalID", item.externalID)
	b = appendMmogStringField(b, "Description", item.description)
	if item.shipID != 0 {
		b = appendMmogInt32Field(b, "ShipID", item.shipID)
	}
	if item.loadoutID != 0 {
		b = appendMmogInt32Field(b, "LoadoutID", item.loadoutID)
	}
	if item.slotName != "" {
		b = appendMmogStringField(b, "SlotName", item.slotName)
	}
	if item.manufacturer != "" {
		b = appendMmogStringField(b, "Manufacturer", item.manufacturer)
	}
	b = appendMmogBoolField(b, "bIsOwned", true)
	b = appendMmogBoolField(b, "bIsEquipped", item.itemType != "ship")
	b = appendMmogBoolField(b, "bIsPreviewable", true)
	b = appendMmogInt32Field(b, "Quantity", item.quantity)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogSetLoadoutDataArray(b []byte, stack []int, name string, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, loadout := range fleet.shipLoadouts {
		b, stack = appendMmogSetLoadoutShipEntry(b, stack, fleet.fleetType, loadout)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogSetLoadoutShipEntry(b []byte, stack []int, fleetType int32, loadout mmogShipLoadoutSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "shipName", loadout.ship.name)
	b = appendMmogStringField(b, "m_shipName", loadout.ship.name)
	b = appendMmogInt32Field(b, "shipID", loadout.ship.id)
	b = appendMmogInt32Field(b, "shipId", loadout.ship.id)
	b = appendMmogInt32Field(b, "m_shipId", loadout.ship.id)
	b = appendMmogInt32Field(b, "shipClass", loadout.ship.shipClass)
	b = appendMmogInt32Field(b, "m_shipClass", loadout.ship.shipClass)
	b = appendMmogInt32Field(b, "FleetType", fleetType)
	b = appendMmogInt32Field(b, "m_fleetType", fleetType)
	b = appendMmogInt32Field(b, "shipXP", 0)
	b = appendMmogInt32Field(b, "m_shipXp", 0)
	b = appendMmogInt32Field(b, "m_shipTier", 1)
	b = appendMmogInt32Field(b, "m_manufacturerId", 0)
	b = appendMmogStringField(b, "m_shipClassImagePath", "")
	b = appendMmogStringField(b, "m_shipClassIconPath", "")
	b = appendMmogStringField(b, "m_tierIconImagePath", "")
	b = appendMmogInt32Field(b, "loadoutCount", 1)
	b, stack = appendMmogArrayStart(b, stack, "loadouts")
	b, stack = appendMmogUnnamedLoadoutDetail(b, stack, loadout)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "m_loadouts")
	b, stack = appendMmogShipLoadoutInfoEntry(b, stack, loadout)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogBattleReadyFleetsInfoArray(b []byte, stack []int, name string, fleets []mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, fleet := range fleets {
		b, stack = appendMmogBattleReadyFleetEntry(b, stack, fleet)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogBattleReadyFleetEntry(b []byte, stack []int, fleet mmogFleetSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "FleetID", fleet.token)
	b = appendMmogStringField(b, "FleetName", fleet.displayName)
	b = appendMmogInt32Field(b, "FleetType", fleet.fleetType)
	b = appendMmogInt32Field(b, "FlagShipID", fleet.flagshipShipID)
	b = appendMmogInt32Field(b, "FlagShipLoadoutID", fleet.flagshipLoadoutID)
	b = appendMmogBoolField(b, "bIsBattleReady", fleet.active && len(fleet.shipLoadouts) > 0)
	b = appendMmogInt32Field(b, "BonusCount", 1)
	b, stack = appendMmogArrayStart(b, stack, "Bonuses")
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "SlotName", "BattleReadyBonus")
	b = appendMmogInt32Field(b, "BonusID", 0)
	b = appendMmogBoolField(b, "bIsOwned", true)
	b = appendMmogBoolField(b, "bIsPlaceholder", true)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayerStatsCounterDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetPlayerStatsCounterData")
	b, stack = appendMmogArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntry(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "counterData")
	b, stack = appendMmogStatsCounterEntry(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogStatsCounterEntry(b []byte, stack []int) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "counterId", 0)
	b = appendMmogInt32Field(b, "subId", 0)
	b = appendMmogInt32Field(b, "counterSubId", 0)
	b = appendMmogInt32Field(b, "m_counterSubId", 0)
	b = appendMmogInt32Field(b, "counterValue", 0)
	b = appendMmogInt32Field(b, "value", 0)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogPlayerProgressionPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)

	b = appendMmogStringField(b, "RT", "YA_GetPlayerProgression")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, "PID", playerPID)
	b = appendMmogInt32Field(b, "CurrentXP", state.currentXP)
	b = appendMmogInt32Field(b, "CurrentRank", state.currentRank)
	b = appendMmogInt32Field(b, "RankXP", state.rankXP)
	b = appendMmogInt32Field(b, "XPToNextRank", 1000)
	b = appendMmogInt32Field(b, "NumUnlockedShips", int32(len(allT1Ships())))
	b, stack = appendMmogArrayStart(b, stack, "shipProgressionUiData")
	for _, ship := range allT1Ships() {
		b, stack = appendMmogShipProgression(b, stack, ship)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogShipProgression(b []byte, stack []int, ship mmogShipSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "shipID", ship.id)
	b = appendMmogInt32Field(b, "xp", 0)
	b = appendMmogInt32Field(b, "tier", 1)
	b = appendMmogBoolField(b, "owned", ship.owned)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogProgressionDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetProgressionData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "ProgressionData")
	for _, shipID := range starterShipIDs() {
		b = appendMmogUnnamedInt32Field(b, shipID)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogTechTreePayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetTechTree")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogInt32Field(b, "techTreeRowCount", int32(len(allT1Ships())))
	b, stack = appendMmogArrayStart(b, stack, "techTreeRow")
	for _, ship := range allT1Ships() {
		b, stack = appendMmogTechTreeRow(b, stack, ship)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "moduleUiData")
	for _, module := range starterModuleUIDataSeeds() {
		b, stack = appendMmogModuleUIDataEntry(b, stack, module)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogTechTreeRow(b []byte, stack []int, ship mmogShipSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogInt32Field(b, "NodeID", ship.nodeID)
	b = appendMmogInt32Field(b, "ShipID", ship.id)
	b = appendMmogInt32Field(b, "shipID", ship.id)
	b = appendMmogInt32Field(b, "m_shipID", ship.id)
	b = appendMmogInt32Field(b, "m_shipId", ship.id)
	b = appendMmogInt32Field(b, "ParentID", ship.parentID)
	b = appendMmogStringField(b, "Name", ship.name)
	b = appendMmogStringField(b, "m_name", ship.name)
	b = appendMmogInt32Field(b, "NodeType", ship.nodeType)
	b = appendMmogInt32Field(b, "Tier", 1)
	b = appendMmogInt32Field(b, "UnlockCost", ship.unlockCost)
	b = appendMmogInt32Field(b, "PrereqID1", ship.prereqID1)
	b = appendMmogInt32Field(b, "PrereqID2", ship.prereqID2)
	b = appendMmogBoolField(b, "bIsUnlocked", ship.owned)
	b = appendMmogBoolField(b, "bIsPurchased", ship.owned)
	b = appendMmogBoolField(b, "bIsNew", ship.bIsNew)
	b = appendMmogInt32Field(b, "ShipClass", ship.shipClass)
	b = appendMmogInt32Field(b, "Weight", ship.weight)
	b = appendMmogInt32Field(b, "m_currentBaseClass", ship.shipClass)
	b = appendMmogInt32Field(b, "m_currentShipClass", ship.shipClass)
	b = appendMmogInt32Field(b, "m_shipTier", 1)
	b = appendMmogInt32Field(b, "m_weight", ship.weight)
	if loadout, ok := starterLoadoutByShipID(ship.id); ok {
		b = appendMmogInt32Field(b, "m_precastLoadoutID", loadout.precastLoadoutID)
		b, stack = appendMmogObjectStart(b, stack, "m_shipLoadoutInfo")
		b, stack = appendMmogShipLoadoutInfoFields(b, stack, loadout)
		b, stack = appendMmogObjectEnd(b, stack)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogItemPriceDataFields(b []byte) []byte {
	b = appendMmogBoolField(b, "m_hasPriceChanged", false)
	b = appendMmogStringField(b, "m_currencyCode", "")
	b = appendMmogInt32Field(b, "m_realCurrency", 0)
	b = appendMmogInt32Field(b, "m_hardCurrency", 0)
	b = appendMmogInt32Field(b, "m_softCurrency", 0)
	b = appendMmogInt32Field(b, "m_freeXP", 0)
	b = appendMmogInt32Field(b, "m_shipXP", 0)
	return b
}

func appendMmogModuleUIDataEntry(b []byte, stack []int, module mmogModuleUIDataSeed) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "m_techTreePurchasePrice")
	b = appendMmogItemPriceDataFields(b)
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "m_techTreeResearchPrice")
	b = appendMmogItemPriceDataFields(b)
	b, stack = appendMmogObjectEnd(b, stack)
	b = appendMmogInt32Field(b, "m_techTreeItemState", 4)
	b = appendMmogInt32Field(b, "m_index", module.index)
	b = appendMmogInt32Field(b, "m_priceCurrency", 0)
	b = appendMmogInt32Field(b, "m_priceAmount", 0)
	b = appendMmogInt32Field(b, "m_originalPriceCurrency", 0)
	b = appendMmogInt32Field(b, "m_originalPriceAmount", 0)
	b = appendMmogStringField(b, "m_moduleTexturePath", "")
	b = appendMmogStringField(b, "m_iconTexturePath", "")
	b = appendMmogInt32Field(b, "m_tier", 1)
	b = appendMmogBoolField(b, "m_shouldShowTierIcon", true)
	b = appendMmogBoolField(b, "m_isOwned", module.owned)
	b = appendMmogBoolField(b, "m_isOnSale", false)
	b = appendMmogBoolField(b, "m_isNew", false)
	b = appendMmogBoolField(b, "m_isEquipped", module.equipped)
	b = appendMmogInt32Field(b, "m_itemId", module.itemID)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func buildMmogCareerProgressionPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetCareerProgression")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "m_categories")
	b, stack = appendMmogProgressionCategories(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogGameConfigDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetGameConfigData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogInt32Field(b, "MaxSquadSize", 5)
	b = appendMmogBoolField(b, "banned", false)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogFeatureTogglePayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetFeatureToggle")
	b = appendMmogBoolField(b, "isEnabled", true)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogBoolField(b, "isEnabled", true)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogPlayerPurchasesPayload() []byte {
	return buildMmogPlayerPurchasesPayloadForPlayer(defaultMmogPlayerPID)
}

func buildMmogPlayerPurchasesPayloadForPlayer(playerPID string) []byte {
	var b []byte
	var stack []int
	state := mmogPlayerStateForPID(playerPID)

	b = appendMmogStringField(b, "RT", "YA_GetPlayerPurchases")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "PurchasesData")
	for _, itemID := range state.purchaseItemIDs() {
		b = appendMmogUnnamedInt32Field(b, itemID)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogStaticCareerDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetStaticCareerData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, "m_categoryDTPath", configBackedProgressionCategoryDataTablePath())
	b, stack = appendMmogArrayStart(b, stack, "m_categories")
	b, stack = appendMmogProgressionCategories(b, stack)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogScoringDataPayload() []byte {
	var b []byte
	var stack []int
	const emptyTableJSON = `[]`

	b = appendMmogStringField(b, "RT", "YA_GetScoringData")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, "YScoringDataTableRow", emptyTableJSON)
	b = appendMmogStringField(b, "m_defendScoringDataTable", emptyTableJSON)
	b = appendMmogStringField(b, "m_remainingPlayerScoringDataTable", emptyTableJSON)
	b = appendMmogStringField(b, "m_killScoringDataTable", emptyTableJSON)
	b = appendMmogStringField(b, "m_waveScoringDataTable", emptyTableJSON)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogDailyContractsDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetDailyContractsData")
	b = appendMmogInt32Field(b, "DailyContractStateID", 0)
	b = appendMmogInt32Field(b, "LastContractsAssignment", 0)
	b = appendMmogInt32Field(b, "DailyContractLastReplaceTime", 0)
	b, stack = appendMmogArrayStart(b, stack, "Quests")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "Contracts")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "Contracts")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogBoosterDataPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetBoosterData")
	b, stack = appendMmogArrayStart(b, stack, "BoosterTable")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogArrayStart(b, stack, "GoldMembershipTable")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogPlayerScoresPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_GetPlayerScores")
	b = appendMmogStringField(b, "modename", "TeamElimination")
	b = appendMmogInt32Field(b, "fleettier", 1)
	b = appendMmogStringField(b, "timespan", "alltime")
	b = appendMmogBoolField(b, "prevweek", false)
	b, stack = appendMmogObjectStart(b, stack, "result")
	b, stack = appendMmogArrayStart(b, stack, "leaderboard")
	b, stack = appendMmogObjectEnd(b, stack)
	b, stack = appendMmogObjectStart(b, stack, "playerrank")
	b = appendMmogStringField(b, "UserName", "Local")
	b = appendMmogInt32Field(b, "Rank", 0)
	b = appendMmogInt32Field(b, "Score", 0)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogFleetEligibilityPayload() []byte {
	var b []byte
	var stack []int

	b = appendMmogStringField(b, "RT", "YA_FleetEligibility")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, stack = appendMmogArrayStart(b, stack, "fleet_eligibility")
	for _, eligibility := range configBackedFleetEligibilities() {
		b, stack = appendMmogUnnamedObjectStart(b, stack)
		b = appendMmogInt32Field(b, "FleetType", eligibility.FleetType)
		b = appendMmogBoolField(b, "Eligible", true)
		b = appendMmogBoolField(b, "isEligible", true)
		b = appendMmogStringField(b, "Reason", "")
		b, stack = appendMmogObjectEnd(b, stack)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogTunePayload() []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_Tune")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogPlayerStatisticsPayload() []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_GetPlayerStatistics")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, stack = appendMmogArrayStart(b, stack, "stats")
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogUserOnlinePayload() []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_UserOnline")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogConnectPayload(playerPID string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_Connect")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b = appendMmogStringField(b, "PID", playerPID)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogAnalyticsBeginTransactionPayload(transactionID string) []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_AnalyticsBeginTransaction")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, fieldStatus, "ok")
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogCheckReturnPayload() []byte {
	var b []byte
	var stack []int
	b = appendMmogStringField(b, "RT", "YA_CheckReturn")
	b, stack = appendMmogObjectStart(b, stack, "result")
	b = appendMmogStringField(b, "status", "ok")
	b = appendMmogBoolField(b, "CanReturnToMatch", false)
	b = appendMmogBoolField(b, "canReturnToMatch", false)
	b = appendMmogBoolField(b, "ReturnValue", false)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func buildMmogPlayersInformationPayload(playerPID string, _ []byte) []byte {
	var b []byte
	var stack []int
	pid := normalizedPlayerStatePID(playerPID)
	state := mmogPlayerStateForPID(pid)

	b = appendMmogStringField(b, "RT", "YA_GetPlayersInformation")
	b = appendMmogStringField(b, "result", "ok")
	b, stack = appendMmogArrayStart(b, stack, "infos")
	b, stack = appendMmogPlayerDisplayInfoEntry(b, stack, pid, state)
	b, stack = appendMmogObjectEnd(b, stack)
	b, _ = appendMmogObjectEnd(b, stack)
	return b
}

func appendMmogPlayerDisplayInfoEntry(b []byte, stack []int, playerPID string, state mmogPlayerState) ([]byte, []int) {
	b, stack = appendMmogUnnamedObjectStart(b, stack)
	b = appendMmogStringField(b, "ID", normalizedPlayerStatePID(playerPID))
	b = appendMmogStringField(b, "DisplayInfo", state.displayInfo)
	b = appendMmogInt32Field(b, "Rank", state.currentRank)
	b = appendMmogInt32Field(b, "UnlockedFleetType", 1)
	b = appendMmogBoolField(b, "Elite", false)
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func numericMmogPlayerID(playerPID string) int32 {
	normalized := normalizedPlayerStatePID(playerPID)
	if len(normalized) < 8 {
		return 1
	}
	value, err := strconv.ParseUint(normalized[:8], 16, 32)
	if err != nil {
		return 1
	}
	return int32(value & 0x7fffffff)
}

func extractMmogPlayerPID(payload []byte) string {
	ticket := extractMmogStringField(payload, "Ticket")
	if ticket == "" {
		return defaultMmogPlayerPID
	}

	if pid := extractPlayerIDFromJWT(ticket); pid != "" {
		return pid
	}

	sum := md5.Sum([]byte(ticket))
	return hex.EncodeToString(sum[:])
}

func extractPlayerIDFromJWT(token string) string {
	if strings.TrimSpace(token) == "" {
		return ""
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(token, claims); err != nil {
		return ""
	}

	return gatewayPlayerDataReadyKey(gatewayClaimsUserID(claims))
}

func normalizeMmogPlayerPID(value string) string {
	cleaned := strings.ToLower(strings.ReplaceAll(value, "-", ""))
	if len(cleaned) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(cleaned); err != nil {
		return ""
	}
	if cleaned == "00000000000000000000000000000000" {
		return ""
	}
	return cleaned
}

func extractMmogStringField(payload []byte, target string) string {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			return ""
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return ""
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return ""
			}
			value := string(payload[i : i+valueLen])
			i += valueLen
			if name == target {
				return value
			}
		case 0x05:
			if i >= len(payload) {
				return ""
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return ""
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return ""
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return ""
			}
			if name == target {
				return ""
			}
			nestedStart := i + 4
			nestedEnd := i + objectLen
			if value := extractMmogStringField(payload[nestedStart:nestedEnd], target); value != "" {
				return value
			}
			i += objectLen
		default:
			return ""
		}
	}
	return ""
}

func extractMmogInt32Field(payload []byte, target string) (int32, bool) {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			return 0, false
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return 0, false
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return 0, false
			}
			i += valueLen
		case 0x05:
			if i >= len(payload) {
				return 0, false
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return 0, false
			}
			value := int32(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if name == target {
				return value, true
			}
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return 0, false
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return 0, false
			}
			if name == target {
				return 0, false
			}
			nestedStart := i + 4
			nestedEnd := i + objectLen
			if value, ok := extractMmogInt32Field(payload[nestedStart:nestedEnd], target); ok {
				return value, true
			}
			i += objectLen
		default:
			return 0, false
		}
	}
	return 0, false
}

func extractMmogRequestName(payload []byte) string {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			break
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return ""
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return ""
			}
			value := string(payload[i : i+valueLen])
			i += valueLen
			if name == "RT" {
				return value
			}
		case 0x05:
			if i >= len(payload) {
				return ""
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return ""
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return ""
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return ""
			}
			i += objectLen
		default:
			return extractMmogRequestNameFromText(payload)
		}
	}
	return extractMmogRequestNameFromText(payload)
}

func extractMmogRequestNameFromText(payload []byte) string {
	text := string(payload)
	idx := strings.Index(text, "YA_")
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(text) {
		c := text[end]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			end++
			continue
		}
		break
	}
	return text[idx:end]
}

func appendMmogObjectStart(b []byte, stack []int, name string) ([]byte, []int) {
	b = appendMmogFieldNameAndType(b, name, 0x0c)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func appendMmogArrayStart(b []byte, stack []int, name string) ([]byte, []int) {
	b = appendMmogFieldNameAndType(b, name, 0x0d)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func appendMmogUnnamedObjectStart(b []byte, stack []int) ([]byte, []int) {
	b = append(b, 0x00, 0x0c)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func appendMmogObjectEnd(b []byte, stack []int) ([]byte, []int) {
	if len(stack) == 0 {
		return b, stack
	}
	start := stack[len(stack)-1]
	stack = stack[:len(stack)-1]
	b = append(b, 0x00, 0x0e)
	binary.LittleEndian.PutUint32(b[start:start+4], uint32(len(b)-start))
	var offset [4]byte
	binary.LittleEndian.PutUint32(offset[:], uint32(start))
	b = append(b, offset[:]...)
	return b, stack
}

// appendMmogRootEnd appends the implicit root-frame terminator (6 bytes: 0x00 0x0e 0x00 0x00 0x00 0x00)
// that the client's SAX-style parser requires at the end of every application-layer payload to signal
// end-of-frame. This is distinct from appendMmogObjectEnd (which uses a stack and is a no-op on empty
// stack). Must be called as the very last step in every buildMmog*Payload function.
func appendMmogRootEnd(b []byte) []byte {
	return append(b, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)
}

func appendMmogStringField(b []byte, name string, value string) []byte {
	b = appendMmogFieldNameAndType(b, name, 0x09)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	b = append(b, length[:]...)
	b = append(b, value...)
	return b
}

func appendMmogInt32Field(b []byte, name string, value int32) []byte {
	b = appendMmogFieldNameAndType(b, name, 0x56)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(value))
	b = append(b, raw[:]...)
	return b
}

func appendMmogBoolField(b []byte, name string, value bool) []byte {
	b = appendMmogFieldNameAndType(b, name, 0x05)
	if value {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

func appendMmogUnnamedStringField(b []byte, value string) []byte {
	b = append(b, 0x00, 0x09)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	b = append(b, length[:]...)
	b = append(b, value...)
	return b
}

func appendMmogUnnamedInt32Field(b []byte, value int32) []byte {
	b = append(b, 0x00, 0x56)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(value))
	b = append(b, raw[:]...)
	return b
}

func appendMmogUnnamedBoolField(b []byte, value bool) []byte {
	b = append(b, 0x00, 0x05)
	if value {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

func appendMmogInt32ArrayField(b []byte, stack []int, name string, values []int32) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, value := range values {
		b = appendMmogUnnamedInt32Field(b, value)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogBoolArrayField(b []byte, stack []int, name string, values []bool) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, value := range values {
		b = appendMmogUnnamedBoolField(b, value)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogStringArrayField(b []byte, stack []int, name string, values []string) ([]byte, []int) {
	b, stack = appendMmogArrayStart(b, stack, name)
	for _, value := range values {
		b = appendMmogUnnamedStringField(b, value)
	}
	b, stack = appendMmogObjectEnd(b, stack)
	return b, stack
}

func appendMmogFieldNameAndType(b []byte, name string, fieldType byte) []byte {
	if len(name) > 255 {
		panic("MMOG field name exceeds 255 bytes: " + name)
	}
	b = append(b, byte(len(name)))
	b = append(b, name...)
	b = append(b, fieldType)
	return b
}

func deriveMmogSessionKey(clientNonce []byte) [16]byte {
	h := md5.New()
	h.Write(clientNonce)
	h.Write(mmogServerSeed[:])
	h.Write(mmogSecretA[:])
	h.Write(mmogSecretB[:])
	sum := h.Sum(nil)
	var key [16]byte
	copy(key[:], sum)
	return key
}

func mustDecode16(s string) [16]byte {
	decoded, err := hex.DecodeString(s)
	if err != nil || len(decoded) != 16 {
		panic("invalid MMOG secret")
	}
	var out [16]byte
	copy(out[:], decoded)
	return out
}

// [INFERRED] Stream cipher derived from Ghidra decompile of the YMmogClient key schedule
// (FUN_142aa*). This is an RC4 variant with a feedback byte per position.
//
// The game uses this cipher after the 3-step MMOG handshake completes:
//   1. Client sends seed (msgType 0x10)
//   2. Server sends seed + nonce (msgType 0x11)
//   3. Client sends digest (msgType 0x12)
//   4. Server sends connected ping, then all subsequent frames are encrypted
//
// Key derivation: MD5(client_nonce || server_seed || secret_a || secret_b) → 16-byte key.
// The decryptor uses key_offset=5, encryptor uses key_offset=0, producing complementary
// keystream positions.
type mmogStreamCipher struct {
	s        [256]byte
	i        byte
	j        byte
	feedback byte
}

func newMmogStreamCipher(key [16]byte, keyOffset int) *mmogStreamCipher {
	c := &mmogStreamCipher{}
	for i := 0; i < 256; i++ {
		c.s[i] = byte(i)
	}
	var j byte
	for i := 0; i < 256; i++ {
		j += c.s[i] + key[(i+keyOffset)&0x0f]
		c.s[i], c.s[j] = c.s[j], c.s[i]
	}
	return c
}

func (c *mmogStreamCipher) decrypt(data []byte) []byte {
	out := make([]byte, len(data))
	for idx, b := range data {
		keyByte := c.nextKeyByte()
		plain := keyByte ^ c.feedback ^ b
		c.feedback = plain
		out[idx] = plain
	}
	return out
}

func (c *mmogStreamCipher) encrypt(data []byte) []byte {
	out := make([]byte, len(data))
	for idx, b := range data {
		keyByte := c.nextKeyByte()
		out[idx] = keyByte ^ b ^ c.feedback
		c.feedback = b
	}
	return out
}

func (c *mmogStreamCipher) nextKeyByte() byte {
	c.i++
	c.j += c.s[c.i]
	c.s[c.i], c.s[c.j] = c.s[c.j], c.s[c.i]
	return c.s[byte(c.s[c.i]+c.s[c.j])]
}

// rawByteLogger logs raw bytes received from the game (for debugging the Firmament protocol).
type rawByteLogger struct {
	log    *logrus.Logger
	remote string
}

func (r *rawByteLogger) Write(p []byte) (int, error) {
	r.log.WithFields(logrus.Fields{
		"remote": r.remote,
		"hex":    hex.EncodeToString(p),
		"text":   string(p),
	}).Info("firmament: raw bytes received")
	return len(p), nil
}

// handleFirmamentConn performs the YFirmament handshake over raw TLS.
//
// Protocol (from Ghidra decompile):
//
//	FUN_142aa39a0: splits raw TLS stream on '\n' → one JSON object per line
//	FUN_142aa9800: on first message from server, checks status=="connection_successful" → state=1
//	FUN_142aa5b90: at state=1 game sends auth payload (JWT + "Dreadnought Game Client")
//	FUN_142aa79f0: on server response, checks status=="success" → state=3 (hangar active)
func handleFirmamentConn(log *logrus.Logger, conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()
	remote := conn.RemoteAddr().String()
	log.WithField("remote", remote).Info("firmament: TCP connection accepted")

	// Force TLS handshake so we can log success/failure immediately.
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			log.WithError(err).WithField("remote", remote).Warn("firmament: TLS handshake failed")
			return
		}
		st := tlsConn.ConnectionState()
		log.WithFields(logrus.Fields{
			"remote":       remote,
			"tls_version":  st.Version,
			"cipher_suite": tls.CipherSuiteName(st.CipherSuite),
		}).Info("firmament: TLS handshake succeeded")
		// Give the game's old OpenSSL time to process any remaining handshake records
		// before we send the first application-data frame.
		time.Sleep(200 * time.Millisecond)
	}

	log.WithField("remote", remote).Info("firmament: client connected")

	// Step 1: send connection_successful greeting.
	// Protocol: {JSON}\r\n — the framer (strchr '\n') includes the \n in the message body,
	// so the dispatcher (FUN_142a8dfa0) finds \r\n at position len({JSON}), extracts {JSON},
	// parses "type" field → routes to connection_successful handler (FUN_142aa9800).
	// FUN_142aa9800: checks parsed struct field "status" == "connection_successful" (21 bytes).
	// Also extracts "peer_id" from the message (struct offset +0x98).
	// Wire format: {"id":"<uuid>","type":"server.notice","data":{"notice":{...}}}\r\n
	// FUN_142a52390 parses message; type="server.notice" routes via FUN_142a8fb80 →
	// FUN_142aa9800 reads data.notice.status (offset +0x58) — compares to
	// "connection_successful" (21 chars). On match, FirmamentClient state → 1.
	// FUN_142aa9800 also reads data.notice.client_id (offset +0x98) as PeerID.
	peerID := uuid.New().String()
	msgID := uuid.New().String()
	hello, _ := json.Marshal(map[string]interface{}{
		"id":   msgID,
		"type": "server.notice",
		"data": map[string]interface{}{
			"notice": map[string]interface{}{
				fieldStatus:         "connection_successful",
				"client_id":         peerID,
				"created_timestamp": time.Now().Unix(),
			},
		},
	})
	hello = append(hello, '\r', '\n')
	if _, err := conn.Write(hello); err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: write hello failed")
		return
	}
	log.WithFields(logrus.Fields{"remote": remote, "peer_id": peerID}).Info("firmament: sent connection_successful")

	// Step 2: read the client's auth payload.
	// Game → Server messages are {JSON}\r\n. json.Decoder handles this correctly:
	// it reads until a complete JSON object, leaving the trailing \r\n buffered.
	// bufio.Scanner ScanLines also strips both \r\n and \n correctly.
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	// Tee-reader logs every raw byte received from the game for debugging.
	rawLog := &rawByteLogger{log: log, remote: remote}
	decoder := json.NewDecoder(io.TeeReader(conn, rawLog))

	var authRaw json.RawMessage
	if err := decoder.Decode(&authRaw); err != nil {
		log.WithError(err).WithField("remote", remote).Warn("firmament: read auth message failed")
		return
	}
	var authPayload map[string]interface{}
	if json.Unmarshal(authRaw, &authPayload) == nil {
		log.WithFields(logrus.Fields{"remote": remote, "payload": authPayload}).Info("firmament: received auth payload")
	} else {
		log.WithFields(logrus.Fields{"remote": remote, "hex": hex.EncodeToString(authRaw)}).Info("firmament: received raw auth bytes")
	}

	// Step 3: send success — game transitions to state=3 (hangar active).
	// The game's auth.refresh.redeem is a JSON-RPC 2.0 style request:
	//   {"method":"auth.refresh.redeem","params":{"token":"...","client_name":"...","client_version":"..."},"id":"<reqid>"}
	// The response handler (_OnRedeemResult / login result) is dispatched via server.notice.
	// Do not send JSON-RPC/auth.refreshtoken probe frames here: they can invoke the same login
	// handler with an empty authentication field and force an immediate "Login failed!".
	// Newline-framed (\r\n) per FUN_142aa39a0 framer + dispatcher FUN_142a8dfa0.
	_ = conn.SetDeadline(time.Time{})

	reqID, _ := authPayload["id"].(string)
	if reqID == "" {
		reqID = uuid.New().String()
	}
	now := time.Now().Unix()
	tokenStr, _ := authPayload["params"].(map[string]interface{})
	var jwtToken string
	if tokenStr != nil {
		jwtToken, _ = tokenStr["token"].(string)
	}
	if jwtToken == "" {
		jwtToken = "ok"
	}
	playerID := extractPlayerIDFromJWT(jwtToken)
	if playerID == "" {
		log.WithField("remote", remote).Warn("firmament: auth payload missing player identity; sending success without MMOG readiness gate")
	} else if !gatewayPlayerDataReadyForUser(playerID) {
		log.WithFields(logrus.Fields{"remote": remote, "pid": playerID}).Info("firmament: sending auth success before MMOG player data is ready; gateway inventory bootstrap remains gated on YA_PlayerGet")
	}

	authOK, _ := json.Marshal(map[string]interface{}{
		"id":   reqID,
		"type": "server.notice",
		"data": map[string]interface{}{
			"notice": map[string]interface{}{
				fieldStatus:         "success",
				"action":            "auth.refresh.redeem",
				"authentication":    "success",
				"token":             jwtToken,
				"user_token":        jwtToken,
				"refresh_token":     jwtToken,
				"client_id":         peerID,
				"recipient_peer":    peerID,
				"created_timestamp": now,
			},
		},
	})
	authOK = append(authOK, '\r', '\n')
	if _, err := conn.Write(authOK); err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: write auth_ok failed")
		return
	}
	log.WithFields(logrus.Fields{"remote": remote, "bytes": len(authOK)}).Info("firmament: sent success, handshake complete")

	// Step 4: keep-alive loop — handle ongoing messages (ping/pong, username resolve, etc.).
	// All game→server messages are raw JSON without '\n' — json.Decoder handles this correctly.
	for {
		var msgRaw json.RawMessage
		if err := decoder.Decode(&msgRaw); err != nil {
			if err == io.EOF {
				log.WithField("remote", remote).Info("firmament: client disconnected")
			} else {
				log.WithError(err).WithField("remote", remote).Warn("firmament: read error")
			}
			return
		}
		var msg map[string]interface{}
		if json.Unmarshal(msgRaw, &msg) != nil {
			log.WithFields(logrus.Fields{"remote": remote, "hex": hex.EncodeToString(msgRaw)}).Info("firmament: non-JSON message")
			continue
		}
		log.WithFields(logrus.Fields{"remote": remote, "msg": msg}).Debug("firmament: received message")

		method, _ := msg[fieldMethod].(string)
		switch method {
		case "ping":
			data := map[string]interface{}{fieldStatus: "success"}
			if params, _ := msg["params"].(map[string]interface{}); params != nil {
				if timeecho, ok := params["timeecho"]; ok {
					data["timeecho"] = timeecho
				}
			}
			if err := writeFirmamentTypedMessage(conn, msg["id"], "pong", data); err != nil {
				log.WithError(err).WithField("remote", remote).Warn("firmament: write ping result failed")
				return
			}
			log.WithField("remote", remote).Info("firmament: sent pong reply")
		case "presence.status.set", "presence.status.setmessage":
			if err := writeFirmamentResult(conn, msg["id"], firmamentPresenceResult(method)); err != nil {
				log.WithError(err).WithField("remote", remote).Warn("firmament: write presence result failed")
				return
			}
			log.WithField("remote", remote).Debug("firmament: sent presence status result")
		case "presence.data.list":
			if err := writeFirmamentResult(conn, msg["id"], firmamentPresenceDataListResult(method)); err != nil {
				log.WithError(err).WithField("remote", remote).Warn("firmament: write presence data result failed")
				return
			}
			log.WithFields(logrus.Fields{"remote": remote, fieldMethod: method}).Info("firmament: sent presence data empty-state result")
		default:
			if isFirmamentSocialMethod(method) && msg["id"] != nil {
				if err := writeFirmamentResult(conn, msg["id"], firmamentPresenceResult(method)); err != nil {
					log.WithError(err).WithField("remote", remote).Warn("firmament: write social result failed")
					return
				}
				log.WithFields(logrus.Fields{"remote": remote, fieldMethod: method}).Info("firmament: sent social empty-state result")
				continue
			}
			if method != "" && msg["id"] != nil {
				if err := writeFirmamentResult(conn, msg["id"], map[string]interface{}{fieldStatus: "success"}); err != nil {
					log.WithError(err).WithField("remote", remote).Warn("firmament: write generic result failed")
					return
				}
				log.WithFields(logrus.Fields{"remote": remote, fieldMethod: method}).Info("firmament: sent generic method result")
				continue
			}
			log.WithFields(logrus.Fields{"remote": remote, "type": msg["type"], fieldMethod: method}).Info("firmament: unhandled message")
		}
	}
}

func isFirmamentSocialMethod(method string) bool {
	return strings.HasPrefix(method, "presence.friends.") ||
		strings.HasPrefix(method, "presence.pending_friends.") ||
		strings.HasPrefix(method, "chat.channel.")
}

func firmamentPresenceResult(method string) map[string]interface{} {
	result := map[string]interface{}{
		fieldStatus:       "success",
		"method":          method,
		"friends":         []interface{}{},
		"pending_friends": []interface{}{},
		"channels":        []interface{}{},
		"presence": map[string]interface{}{
			fieldStatus: "online",
			"message":   "",
		},
	}
	if strings.Contains(method, "listing") {
		result["listing"] = []interface{}{}
	}
	return result
}

func firmamentPresenceDataListResult(method string) map[string]interface{} {
	result := firmamentPresenceResult(method)
	result["result"] = []interface{}{}
	return result
}

func writeFirmamentTypedMessage(conn net.Conn, id interface{}, msgType string, data map[string]interface{}) error {
	response, _ := json.Marshal(map[string]interface{}{
		"id":   id,
		"type": msgType,
		"data": data,
	})
	response = append(response, '\r', '\n')
	_, err := conn.Write(response)
	return err
}

func writeFirmamentResult(conn net.Conn, id interface{}, result map[string]interface{}) error {
	response, _ := json.Marshal(map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
		"result":  result,
	})
	response = append(response, '\r', '\n')
	_, err := conn.Write(response)
	return err
}

// ─── Gateway HTTPS server ─────────────────────────────────────────────────────
//
// The game client connects to https://<GatewayAddress>:<GatewayPort>/api/v1/...
// Confirmed endpoints (from Ghidra decompile of DreadGame-Win64-Shipping.exe):
//   POST /api/v1/authentication/login      — Bearer JWT → session_id
//   POST /api/v1/authentication/logout     — end session
//   POST /api/v1/session/touch             — keepalive
//   GET  /api/v1/ping                      — health check
//   GET  /api/v1/play                      — matchmaking / play button
//   GET  /api/v1/bundles                   — ship/item bundles
//   GET  /api/v1/catalog/digital_items_vc  — VC store items
//   GET  /api/v1/catalog/currency_pack_vc  — currency packs
//   GET  /api/v1/catalog/digital_items_rmt — RMT store items
//   POST /api/v1/account/legal/attest      — legal attestation (ToS accept)
//   POST /api/v1/account/legal/document/accept — accept specific legal doc

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

const firmamentPlayerDataReadyTimeout = 30 * time.Second

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
	// Legal document text endpoint: /api/v1/account/legal/document/{type}/en/text
	// Ghidra FUN_142ab23a0: expects JSON Array (type 5) — returns [] to indicate empty/accepted doc.
	mux.HandleFunc("/api/v1/account/legal/document/", makeGatewayHandler(log, secret, handleGWLegalDocument))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.WithFields(logrus.Fields{fieldMethod: r.Method, fieldPath: r.URL.Path}).Info("gateway request")
		gwJSON(w, map[string]any{})
	})
	// Catch-all: log unknown paths and return empty 200
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
			sessionID := strings.TrimPrefix(authHdr, "Session ")
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
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			return secret, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !token.Valid {
			log.WithError(err).Warn("gateway: invalid JWT")
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		claims, _ := token.Claims.(jwt.MapClaims)
		fn(w, r, claims)
	}
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

	sessionID := uuid.New().String()
	sessionsMu.Lock()
	sessions[sessionID] = gatewaySession{UserID: userID, Username: username, createdAt: time.Now()}
	sessionsMu.Unlock()

	w.Header().Set("Authorization", "Session "+sessionID+", "+username)

	gwJSON(w, map[string]any{
		"SessionId":  sessionID, // kept as JSON fallback (Ghidra: u_SessionId_*)
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

// handleGWLogout handles POST /api/v1/authentication/logout.
func handleGWLogout(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	gwJSON(w, map[string]any{})
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

	// Reuse the existing session if the client sent a Session token; otherwise create new.
	authHdr := r.Header.Get("Authorization")
	sessionID := ""
	if strings.HasPrefix(authHdr, "Session ") {
		sessionID = strings.TrimPrefix(authHdr, "Session ")
		// Verify it still exists; if not, issue a fresh one.
		sessionsMu.Lock()
		if _, ok := sessions[sessionID]; !ok {
			sessionID = ""
		}
		sessionsMu.Unlock()
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
		sessionsMu.Lock()
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
	playerID := gatewayClaimsUserID(claims)
	gwJSON(w, gatewayBootstrapPayload(playerID, "bundles", waitForGatewayBootstrapPlayerDataReady(playerID)))
}

// handleGWCatalog handles catalog endpoints.
func handleGWCatalog(w http.ResponseWriter, r *http.Request, claims jwt.MapClaims) {
	playerID := gatewayClaimsUserID(claims)
	gwJSON(w, gatewayBootstrapPayload(playerID, gatewayCatalogResponseKey(r.URL.Path), waitForGatewayBootstrapPlayerDataReady(playerID)))
}

type gatewayCatalogEntitySeed struct {
	itemID          int32
	externalID      string
	displayName     string
	description     string
	entityType      string
	itemType        string
	manufacturer    string
	shipID          int32
	loadoutID       int32
	priceCurrencyID string
	priceAmount     int32
	grantedCurrency string
	grantedAmount   int32
	owned           bool
	hidden          bool
	quantity        int32
	isNew           bool
	gateIdentity    bool
	bundleItems     []gatewayCatalogEntitySeed
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

func gatewayBootstrapPayload(playerID string, requestedCatalog string, playerDataReady bool) map[string]any {
	ownedItems := []any{}
	starterShipIDs := []int32{}
	starterLoadoutIDs := []int32{}
	if playerDataReady {
		ownedItems = gatewayOwnedInventorySnapshot()
		starterShipIDs = starterShipIDsForBootstrap()
		starterLoadoutIDs = starterLoadoutIDsForBootstrap()
	}
	payload := map[string]any{
		"Code":                0,
		"catalog_version":     "starter-hangar-bootstrap-v6",
		"requested_catalog":   requestedCatalog,
		"player_id":           playerID,
		"wallet":              gatewayWalletSnapshot(),
		"owned_items":         ownedItems,
		"starter_ship_ids":    starterShipIDs,
		"starter_loadout_ids": starterLoadoutIDs,
	}
	if catalog := gatewayRequestedCatalogCollection(requestedCatalog, playerDataReady); catalog != nil {
		payload["entities"] = catalog["entities"]
		payload["Items"] = catalog["Items"]
		payload["ItemOffers"] = catalog["ItemOffers"]
		payload["ForexOffers"] = catalog["ForexOffers"]
	}
	if requestedCatalog == "bundles" {
		bundles := gatewayMarketEntities(gatewayBundleCatalogSeeds(), playerDataReady)
		payload["bundles"] = bundles
		payload["Bundles"] = bundles
	}
	return payload
}

func gatewayRequestedCatalogCollection(requestedCatalog string, playerDataReady bool) map[string]any {
	switch requestedCatalog {
	case "item_catalog_real":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeeds("RMT"), playerDataReady))
	case "item_catalog_virtual":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeeds("CR"), playerDataReady))
	case "currency_catalog_real":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("RMT", "RMT"), playerDataReady))
	case "currency_catalog_virtual":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("CR", "CR"), playerDataReady))
	default:
		return nil
	}
}

func starterShipIDsForBootstrap() []int32 {
	return dreadconfig.StarterInventoryShipIDs()
}

func starterLoadoutIDsForBootstrap() []int32 {
	return starterLoadoutIDs()
}

func gatewayClaimsUserID(claims jwt.MapClaims) string {
	userID, _ := claims["user_id"].(string)
	if userID != "" {
		return userID
	}
	userID, _ = claims["sub"].(string)
	return userID
}

func gatewayPlayerDataReadyForUser(playerID string) bool {
	key := gatewayPlayerDataReadyKey(playerID)
	if key == "" {
		return false
	}
	gatewayPlayerDataReadyMu.Lock()
	defer gatewayPlayerDataReadyMu.Unlock()
	state := gatewayPlayerDataReady[key]
	return state != nil && state.ready
}

func setGatewayPlayerDataReadyState(playerID string, ready bool) {
	key := gatewayPlayerDataReadyKey(playerID)
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
	key := gatewayPlayerDataReadyKey(playerID)
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

func gatewayPlayerDataReadyKey(playerID string) string {
	if key := normalizeMmogPlayerPID(playerID); key != "" {
		return key
	}
	return strings.ToLower(strings.TrimSpace(playerID))
}

func gatewayItemCatalogCollection(entities []any) map[string]any {
	return map[string]any{
		"entities":    entities,
		"Items":       entities,
		"ItemOffers":  []any{},
		"ForexOffers": []any{},
	}
}

func gatewayCurrencyCatalogCollection(entities []any) map[string]any {
	return map[string]any{
		"entities":    entities,
		"Items":       []any{},
		"ItemOffers":  []any{},
		"ForexOffers": entities,
	}
}

func gatewayShipClassDisplayName(shipClass int32) string {
	switch shipClass {
	case 0:
		return "Destroyer"
	case 1:
		return "Corvette"
	case 2:
		return "Artillery"
	case 3:
		return "Tactical"
	case 4:
		return "Dreadnought"
	default:
		return ""
	}
}

func gatewayManufacturerDisplayName(manufacturer string) string {
	switch manufacturer {
	case "JupiterArms":
		return "Jupiter Arms"
	case "AkulaVektor":
		return "Akula Vektor"
	default:
		return manufacturer
	}
}

func gatewayMarketCategoryName(itemType string) string {
	switch itemType {
	case "ship":
		return "Ship"
	case "loadout":
		return "Loadout"
	case "weapon":
		return "Weapon"
	case "ability":
		return "Ability"
	case "perk":
		return "Perk"
	case "bundle":
		return "Bundle"
	case "currency_pack":
		return "Currency Pack"
	default:
		return itemType
	}
}

func gatewayShipByID(shipID int32) (mmogShipSeed, bool) {
	for _, ship := range allT1Ships() {
		if ship.id == shipID {
			return ship, true
		}
	}
	for _, ship := range starterBootstrapShips() {
		if ship.id == shipID {
			return ship, true
		}
	}
	return mmogShipSeed{}, false
}

func gatewayMarketCategoryMetadata(seed gatewayCatalogEntitySeed) (string, string, string, string) {
	categoryName := gatewayMarketCategoryName(seed.itemType)
	parentCategoryName := ""
	extractedMeta, hasExtractedMeta := extractedMarketItemMetadataForID(seed.itemID)
	if ship, ok := gatewayShipByID(seed.shipID); ok {
		if seed.itemType == "ship" {
			if shipClassName := gatewayShipClassDisplayName(ship.shipClass); shipClassName != "" {
				categoryName = shipClassName
			}
			parentCategoryName = gatewayManufacturerDisplayName(ship.manufacturer)
		} else {
			if hasExtractedMeta && extractedMeta.catalogBucket != "" {
				categoryName = extractedMeta.catalogBucket
			}
			parentCategoryName = ship.name
		}
	} else if seed.manufacturer != "" {
		parentCategoryName = gatewayManufacturerDisplayName(seed.manufacturer)
	} else if hasExtractedMeta && extractedMeta.catalogBucket != "" {
		categoryName = extractedMeta.catalogBucket
	}
	if categoryName == "" {
		categoryName = seed.displayName
	}
	categoryDescription := seed.description
	if categoryDescription == "" {
		categoryDescription = strings.TrimSpace(parentCategoryName + " " + categoryName)
		if categoryDescription == "" {
			categoryDescription = categoryName
		}
	}
	return "", categoryName, parentCategoryName, categoryDescription
}

func gatewayWalletSnapshot() map[string]any {
	return map[string]any{
		"CR":     10000,
		"RMT":    0,
		"FreeXp": 0,
	}
}

func gatewayOwnedInventorySnapshot() []any {
	items := starterOwnedInventorySeeds()
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"external_id": item.externalID,
			"item_id":     item.itemID,
			"item_type":   item.itemType,
			"ship_id":     item.shipID,
			"loadout_id":  item.loadoutID,
			"owned":       true,
		})
	}
	return result
}

func gatewayItemCatalogSeeds(priceCurrencyID string) []gatewayCatalogEntitySeed {
	seeds := make([]gatewayCatalogEntitySeed, 0, len(starterOwnedInventorySeeds()))
	for _, item := range starterOwnedInventorySeeds() {
		if item.itemType == "ship" {
			continue
		}
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID:          item.itemID,
			externalID:      item.externalID,
			displayName:     item.name,
			description:     item.description,
			entityType:      "item",
			itemType:        item.itemType,
			manufacturer:    item.manufacturer,
			shipID:          item.shipID,
			loadoutID:       item.loadoutID,
			priceCurrencyID: priceCurrencyID,
			priceAmount:     0,
			owned:           true,
			hidden:          item.itemType != "ship" && item.itemType != "loadout",
			quantity:        item.quantity,
			gateIdentity:    true,
		})
	}
	return seeds
}

func gatewayCurrencyCatalogSeeds(priceCurrencyID string, grantedCurrency string) []gatewayCatalogEntitySeed {
	grantedAmount := int32(1000)
	if grantedCurrency == "CR" {
		grantedAmount = 10000
	}
	return []gatewayCatalogEntitySeed{{
		itemID:          9000001,
		externalID:      "currency_pack_" + strings.ToLower(grantedCurrency),
		displayName:     strings.ToUpper(grantedCurrency) + " Starter Pack",
		description:     "Bootstrap currency pack for hangar readiness",
		entityType:      "forex_offer",
		itemType:        "currency_pack",
		priceCurrencyID: priceCurrencyID,
		priceAmount:     0,
		grantedCurrency: grantedCurrency,
		grantedAmount:   grantedAmount,
		quantity:        1,
	}}
}

func gatewayBundleCatalogSeeds() []gatewayCatalogEntitySeed {
	return []gatewayCatalogEntitySeed{{
		itemID:          9100001,
		externalID:      "starter_bundle",
		displayName:     "Starter Bundle",
		description:     "Starter ships, loadouts, and equipped items",
		entityType:      "bundle",
		itemType:        "bundle",
		priceCurrencyID: "CR",
		priceAmount:     0,
		owned:           true,
		quantity:        1,
		gateIdentity:    true,
	}}
}

func gatewayBundleItemSeeds() []gatewayCatalogEntitySeed {
	items := starterOwnedInventorySeeds()
	seeds := make([]gatewayCatalogEntitySeed, 0, len(items))
	for _, item := range items {
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID:          item.itemID,
			externalID:      item.externalID,
			displayName:     item.name,
			description:     item.description,
			entityType:      "item",
			itemType:        item.itemType,
			manufacturer:    item.manufacturer,
			shipID:          item.shipID,
			loadoutID:       item.loadoutID,
			priceCurrencyID: "CR",
			priceAmount:     0,
			owned:           true,
			hidden:          item.itemType != "ship" && item.itemType != "loadout",
			quantity:        item.quantity,
			gateIdentity:    true,
		})
	}
	return seeds
}

func gatewayMarketIdentity(seed gatewayCatalogEntitySeed, _ bool) (int32, int32, int32, string) {
	itemID := seed.itemID
	shipID := seed.shipID
	loadoutID := seed.loadoutID
	entityID := strconv.Itoa(int(seed.itemID))
	return itemID, shipID, loadoutID, entityID
}

func gatewayMarketEntities(seeds []gatewayCatalogEntitySeed, playerDataReady bool) []any {
	entities := make([]any, 0, len(seeds))
	for _, seed := range seeds {
		entities = append(entities, gatewayMarketEntity(seed, playerDataReady))
	}
	return entities
}

func gatewayMarketEntity(seed gatewayCatalogEntitySeed, playerDataReady bool) map[string]any {
	categoryIcon, categoryName, parentCategoryName, categoryDescription := gatewayMarketCategoryMetadata(seed)
	priceValue := strconv.Itoa(int(seed.priceAmount))
	owned := playerDataReady && seed.owned
	itemID, shipID, loadoutID, entityID := gatewayMarketIdentity(seed, playerDataReady)
	price := map[string]any{
		"id":            "price_free",
		"PriceID":       "price_free",
		"PriceId":       "price_free",
		"price_id":      "price_free",
		"region_id":     "US",
		"amount":        priceValue,
		"currency_id":   seed.priceCurrencyID,
		"currency":      seed.priceCurrencyID,
		"currency_code": seed.priceCurrencyID,
	}
	bundleItems := make([]any, 0, len(seed.bundleItems))
	for _, item := range seed.bundleItems {
		bundleItems = append(bundleItems, gatewayMarketEntity(item, playerDataReady))
	}
	entity := map[string]any{
		"ID":                  itemID,
		"Name":                seed.displayName,
		"Sku":                 seed.externalID,
		"ImgUrlS":             "",
		"ImgUrlM":             "",
		"ImgUrlL":             "",
		"Flags":               0,
		"id":                  entityID,
		"name":                seed.displayName,
		"display_name":        seed.displayName,
		"entity_id":           entityID,
		"external_id":         seed.externalID,
		"item_id":             itemID,
		"entity_ID":           itemID,
		"entity_type":         seed.entityType,
		"item_type":           seed.itemType,
		"Description":         seed.description,
		"description":         seed.description,
		"full_image_url":      "",
		"ImageURL":            "",
		"currency_id":         seed.priceCurrencyID,
		"quantity":            seed.quantity,
		"CategoryIcon":        categoryIcon,
		"CategoryName":        categoryName,
		"ParentCategoryName":  parentCategoryName,
		"CategoryDescription": categoryDescription,
		"GrantedCurrency": map[string]any{
			"Currency": seed.grantedCurrency,
			"Amount":   seed.grantedAmount,
		},
		"ItemID":                  itemID,
		"CurrencyCode":            seed.priceCurrencyID,
		"CurrencySymbol":          seed.priceCurrencyID,
		"CurrencyAmount":          priceValue,
		"Price":                   priceValue,
		"IsNew":                   seed.isNew,
		"DoNotDisplayInStore":     seed.hidden,
		"IsOwned":                 owned,
		"Owned":                   owned,
		"bIsOwned":                owned,
		"ActionAvailabilityIndex": 0,
		"HasVideoPreview":         false,
		"OnSale":                  false,
		"ItemStatsArray":          []any{},
		"AdditionalTextArray":     []any{},
		"IsHeroShip":              false,
		"HasVeteranStatus":        false,
		"HeroShipStatsArray":      []any{},
		"PreviousItemStatsArray":  []any{},
		"Manufacturer":            seed.manufacturer,
		"ship_id":                 shipID,
		"ShipID":                  shipID,
		"loadout_id":              loadoutID,
		"LoadoutID":               loadoutID,
		"PriceId":                 "price_free",
		"campaign_id":             "",
		"PromotionFlagSet":        []any{},
		"prices":                  []any{price},
		"items":                   bundleItems,
		"entities":                []any{},
		"entitlements":            []any{},
	}
	if seed.grantedCurrency != "" {
		entity["granted_currency_id"] = seed.grantedCurrency
		entity["granted_currency_amount"] = seed.grantedAmount
	}
	return entity
}

func maxInt32(a int32, b int32) int32 {
	if a > b {
		return a
	}
	return b
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
