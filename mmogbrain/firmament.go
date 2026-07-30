package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// startFirmamentServer runs a raw TLS server on addr for the YFirmament game protocol.
// Protocol confirmed from Ghidra: FUN_142aa39a0 splits incoming bytes on '\n' (newline 0x0a),
// so each message is a JSON object terminated with a newline byte.
// The game does NOT speak HTTP/WebSocket — it uses SSL_read/SSL_write directly.
func startFirmamentServer(ctx context.Context, log *logrus.Logger, addr, certFile, keyFile string, secret []byte) {
	var tlsCfg *tls.Config

	if certFile != "" && keyFile != "" {
		cert, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
		if loadErr != nil {
			log.WithError(loadErr).Fatal("firmament: load TLS cert/key")
		}
		tlsCfg = &tls.Config{
			Certificates:           []tls.Certificate{cert},
			MinVersion:             tls.VersionTLS10,
			MaxVersion:             tls.VersionTLS12,
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

		ip := conn.RemoteAddr().String()
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
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
			routeFirmamentOrMmogConn(log, conn, tlsCfg, secret)
		}()
	}
}

// [INFERRED] Connection routing heuristic: peeks at the first byte to determine protocol.
// TLS ClientHello starts with 0x16 (handshake record); MMOG binary protocol starts with
// any other byte (typically 0x67 'g' for the MMOG magic marker). This heuristic is derived
// from observing that both protocols share port :48843 in the production server — the game
// uses TLS for Firmament (JSON-RPC over SSL_read/SSL_write) and plain TCP with stream
// cipher for MMOG binary frames. Confirmed working with unmodified Dreadnought client.
func routeFirmamentOrMmogConn(log *logrus.Logger, conn net.Conn, tlsCfg *tls.Config, secret []byte) {
	remote := conn.RemoteAddr().String()
	log.WithField("remote", remote).Info("firmament/mmog: TCP connection accepted")

	buffered := protocol.NewBufferedConn(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	first, err := buffered.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		log.WithError(err).WithField("remote", remote).Warn("firmament/mmog: initial read failed")
		_ = conn.Close()
		return
	}

	if tlsCfg != nil && len(first) > 0 && first[0] == 0x16 {
		handleFirmamentConn(log, tls.Server(buffered, tlsCfg), secret)
		return
	}

	if tlsCfg == nil {
		handleFirmamentConn(log, buffered, secret)
		return
	}

	handleMmogConn(log, buffered)
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
func handleFirmamentConn(log *logrus.Logger, conn net.Conn, secret []byte) {
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
	hello, err := json.Marshal(map[string]interface{}{
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
	if err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: marshal hello failed")
		return
	}
	hello = append(hello, '\r', '\n')
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(hello); err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: write hello failed")
		return
	}
	if err := writer.Flush(); err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: flush hello failed")
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
	params, _ := authPayload["params"].(map[string]interface{})
	var jwtToken string
	if params != nil {
		jwtToken, _ = params["token"].(string)
	}
	if jwtToken == "" {
		log.WithField("remote", remote).Warn("firmament: no JWT token provided, rejecting connection")
		return
	}
	claims, err := protocol.VerifiedJWTClaims(jwtToken, secret, "dreadnought", "launcher")
	if err != nil {
		log.WithError(err).WithField("remote", remote).Warn("firmament: invalid JWT, rejecting connection")
		return
	}
	playerID := protocol.GatewayPlayerDataReadyKey(protocol.GatewayClaimsUserID(claims))
	if playerID == "" {
		log.WithField("remote", remote).Warn("firmament: auth payload missing player identity; sending success without MMOG readiness gate")
	} else if !gatewayPlayerDataReadyForUser(playerID) {
		log.WithFields(logrus.Fields{"remote": remote, "pid": playerID}).Info("firmament: sending auth success before MMOG player data is ready; gateway inventory bootstrap remains gated on YA_PlayerGet")
	}

	authOK, err := json.Marshal(map[string]interface{}{
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
	if err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: marshal authOK failed")
		return
	}
	authOK = append(authOK, '\r', '\n')
	if _, err := conn.Write(authOK); err != nil {
		log.WithError(err).WithField("remote", remote).Error("firmament: write auth_ok failed")
		return
	}
	log.WithFields(logrus.Fields{"remote": remote, "bytes": len(authOK)}).Info("firmament: sent success, handshake complete")

	// Step 4: keep-alive loop — handle ongoing messages (ping/pong, username resolve, etc.).
	// All game→server messages are raw JSON without '\n' — json.Decoder handles this correctly.
	//
	// The deadline was cleared permanently above (step 3) and, until this
	// fix, was never reinstated — Decode() blocked forever, so a connection
	// that authenticated and then sent an unterminated/partial message (or
	// just stopped sending) held its goroutine and socket open indefinitely.
	// Re-arm a deadline before every Decode call; a timeout just means the
	// client's been quiet, not that the connection is dead, so only close
	// once total idle time exceeds maxMmogConnIdleDuration. json.Decoder's
	// internal buffer is preserved across a timed-out Read, so retrying
	// Decode() on the same decoder correctly resumes any partially-received
	// message rather than corrupting/dropping it.
	for {
		var msgRaw json.RawMessage
		// One deadline covering the whole permitted idle window, and a timeout
		// is FATAL — never retried.
		//
		// This connection is a *tls.Conn, and crypto/tls permanently latches
		// the first read error: after a deadline fires, every later Read
		// returns that same timeout immediately without touching the socket.
		// The previous code used a short 30s deadline and treated a timeout as
		// "client is just quiet" (continue), so a single timeout turned into a
		// hot spin — Decode returning instantly, looping until the 15-minute
		// idle cap. Observed burning ~45% CPU on an idle server with no client
		// connected. Using the full idle window as the deadline keeps the same
		// tolerance for quiet clients without ever retrying a latched error.
		_ = conn.SetReadDeadline(time.Now().Add(maxMmogConnIdleDuration))
		err := decoder.Decode(&msgRaw)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.WithField("remote", remote).Info("firmament: closing connection idle past maximum duration")
				return
			}
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
