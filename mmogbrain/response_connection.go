package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"time"

	"github.com/dreadnought-ps/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// maxHandshakeBufferBytes bounds how much unconsumed data a connection may
// accumulate before completing the initial handshake. The real handshake
// (seed + digest packets) fits comfortably within a few hundred bytes; this
// is deliberately generous headroom, not a tight fit.
const maxHandshakeBufferBytes = 16 * 1024

// seasonDataResponseDisabled: full-memory-dump analysis (two independent
// crash sessions, ~2824-deep recursion both times, identical function
// addresses) traced a hangar-entry EXCEPTION_STACK_OVERFLOW to
// UYPlayerMPQuestCycle::OnBackendDataAvailable, which fires as soon as our
// YA_GetSeasonData response completes OnSeasonDataAvailable ->
// SetActiveEventAndSeason (called unconditionally, even for an empty
// season). Every variant tried — empty Seasons/Events, real season/event
// metadata with matching CurrentSeason, real daily-contract ids sent
// first, and a 3s delay before answering (to give the client's own
// MPQuestCollection asset load a head start) — hit the exact same crash
// at the exact same instruction. That total invariance to content and
// timing means the recursion isn't gated by any condition our data can
// influence: once this response lets OnSeasonDataAvailable's delegate
// broadcast fire at all, the crash follows unconditionally. The only
// remaining lever is not answering this request at all, so
// OnSeasonDataAvailable never runs. Risk: if the client blocks waiting
// for this specific response before proceeding, this could stall hangar
// entry the way a withheld YA_PlayerFleets/YA_GetDailyContractsData
// response once did (see the "mutual wait" comment below) — that's the
// open question this experiment is meant to answer.
const seasonDataResponseDisabled = true

// maxMmogConnIdleDuration bounds how long a connection may go without
// sending any data before it's closed. Generous relative to the client's
// normal ping cadence (observed ~5s) so legitimate idle players aren't
// disconnected, while still bounding the number of long-lived,
// mostly-idle goroutines/sockets one client can accumulate.
const maxMmogConnIdleDuration = 15 * time.Minute

func handleMmogConn(log *logrus.Logger, conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	remote := conn.RemoteAddr().String()
	log.WithField("remote", remote).Info("mmog: plaintext connection accepted")

	buf := make([]byte, 8192)
	handshakeStage := 0
	var clientNonce []byte
	var appDecoder *protocol.StreamCipher
	var appEncoder *protocol.StreamCipher
	var appPlainBuf []byte
	var handshakeBuf []byte
	state := &mmogConnState{playerPID: defaultMmogPlayerPID}
	lastActivity := time.Now()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		_ = conn.SetReadDeadline(time.Time{})
		if n > 0 {
			lastActivity = time.Now()
			data := append([]byte(nil), buf[:n]...)
			log.WithFields(logrus.Fields{
				"remote": remote,
				"bytes":  n,
				"hex":    hex.EncodeToString(data),
				"text":   string(data),
			}).Info("mmog: raw bytes received")

			handshakeBuf = append(handshakeBuf, data...)

			// A real handshake completes within a couple of small packets.
			// nextBufferedMmogPacket only ever consumes bytes once it finds
			// valid magic (0x67 0x50) at the front of the buffer — a
			// connection that never sends a matching prefix would otherwise
			// grow handshakeBuf without bound for as long as it stays open
			// (memory-exhaustion DoS). Cap it and drop the connection if a
			// real handshake hasn't completed within a generous size.
			if handshakeStage < 2 && len(handshakeBuf) > maxHandshakeBufferBytes {
				log.WithFields(logrus.Fields{"remote": remote, "bytes": len(handshakeBuf)}).
					Warn("mmog: handshake buffer exceeded size limit without completing handshake, dropping connection")
				return
			}

			if handshakeStage < 2 {
				for handshakeStage < 2 {
					packet, remaining, ok := nextBufferedMmogPacket(handshakeBuf)
					if !ok {
						break
					}
					switch handshakeStage {
					case 0:
						if !protocol.IsHandshakePacket(packet) {
							log.WithField("remote", remote).Warn("mmog: unexpected packet before handshake")
							return
						}
						if len(packet) >= 22 {
							clientNonce = append([]byte(nil), packet[6:22]...)
						}
						if writeErr := protocol.SendSeedResponse(conn, packet); writeErr != nil {
							log.WithError(writeErr).WithField("remote", remote).Warn("mmog: seed response failed")
							return
						}
						log.WithField("remote", remote).Info("mmog: sent seed response")
						handshakeBuf = remaining
						handshakeStage = 1
					case 1:
						if !protocol.IsDigestPacket(packet) {
							log.WithField("remote", remote).Warn("mmog: unexpected packet before digest")
							return
						}
						if writeErr := protocol.SendConnectedPing(conn, packet); writeErr != nil {
							log.WithError(writeErr).WithField("remote", remote).Warn("mmog: connected ping failed")
							return
						}
						log.WithField("remote", remote).Info("mmog: sent connected ping")
						if len(clientNonce) == 16 {
							key := protocol.DeriveSessionKey(clientNonce)
							appDecoder = protocol.NewStreamCipher(key, 5)
							appEncoder = protocol.NewStreamCipher(key, 0)
							log.WithFields(logrus.Fields{
								"remote": remote,
								"key":    hex.EncodeToString(key[:]),
							}).Info("mmog: initialized application decryptor")
						} else {
							log.WithField("remote", remote).Warn("mmog: client nonce too short, cipher not initialized")
						}
						handshakeBuf = remaining
						handshakeStage = 2
					}
					if len(handshakeBuf) == 0 {
						break
					}
				}
				if handshakeStage < 2 || len(handshakeBuf) == 0 {
					continue
				}
			}

			data = handshakeBuf
			handshakeBuf = nil
			if frames, remaining := protocol.ParseAppFrames(data); len(frames) > 0 && len(remaining) == 0 {
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
				plain := appDecoder.Decrypt(data)
				appPlainBuf = append(appPlainBuf, plain...)
				log.WithFields(logrus.Fields{
					"remote": remote,
					"bytes":  len(plain),
					"hex":    hex.EncodeToString(plain),
					"text":   string(plain),
				}).Info("mmog: decrypted application bytes")
				frames, remaining := protocol.ParseAppFrames(appPlainBuf)
				appPlainBuf = remaining
				if err := processMmogAppFrames(log, conn, remote, frames, appEncoder, true, state); err != nil {
					return
				}
			} else {
				log.WithFields(logrus.Fields{
					"remote": remote,
					"bytes":  len(data),
				}).Warn("mmog: encrypted frames received but cipher is nil, dropping")
			}
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// The 30s read deadline re-arms on every timeout with no
				// upper bound, so a connection that never sends data (or
				// trickles a byte just often enough) could stay open —
				// and its goroutine/socket held — indefinitely. Close it
				// once it's been genuinely idle past a generous ceiling.
				if time.Since(lastActivity) > maxMmogConnIdleDuration {
					log.WithField("remote", remote).Info("mmog: closing connection idle past maximum duration")
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

func nextBufferedMmogPacket(data []byte) ([]byte, []byte, bool) {
	if len(data) < 6 || data[0] != 0x67 || data[1] != 0x50 {
		return nil, data, false
	}
	size := int(binary.LittleEndian.Uint16(data[2:4]))
	if size < 6 || len(data) < size {
		return nil, data, false
	}
	return data[:size], data[size:], true
}

type mmogConnState struct {
	loginResponseSent        bool
	playerGetResponded       bool
	pendingPlayerPurchases   []protocol.AppFrame // delayed until YA_PlayerGet marks bootstrap ready
	pendingDailyContracts    []protocol.AppFrame // delayed until YA_PlayerGet avoids early quest-cycle recursion
	pendingPlayerFleets      []protocol.AppFrame // delayed until YA_PlayerGet fleet config is loaded
	pendingStaticFleetData   []protocol.AppFrame // delayed until YA_PlayerGet fleet config is loaded
	staticFleetDataReceived  bool
	fleetEligibilityReceived bool
	playerFleetsReceived     bool
	playerPID                string
}

// mmogJWTSecretValue is set once at startup by setMmogJWTSecret, after
// main() has validated it's a real secret (see requireJWTSecret) — avoids
// re-reading and re-validating the environment on every login attempt.
var mmogJWTSecretValue []byte

func setMmogJWTSecret(secret []byte) {
	mmogJWTSecretValue = secret
}

func mmogJWTSecret() []byte {
	return mmogJWTSecretValue
}

func syntheticRequestID(tag byte) [16]byte {
	var synthID [16]byte
	synthID[0] = tag
	synthID[1] = 0xee
	synthID[2] = 0x77
	return synthID
}

func handlePlayerGetSatisfied(log *logrus.Logger, conn net.Conn, remote string, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState, source string) error {
	state.playerGetResponded = true
	setGatewayPlayerDataReadyState(state.playerPID, true)
	if len(state.pendingStaticFleetData) > 0 {
		pending := state.pendingStaticFleetData
		state.pendingStaticFleetData = nil
		for _, ps := range pending {
			psResp := buildMmogRequestResponseFrame(ps.RequestID, ps.MsgType, "YA_RequestStaticFleetData", state.playerPID, ps.Payload)
			log.WithFields(logrus.Fields{"remote": remote, "name": "YA_RequestStaticFleetData", "hex": hex.EncodeToString(psResp), "plain_len": len(psResp)}).Info("mmog: fleet response hex dump")
			if err := writeMmogAppResponse(log, conn, remote, ps.RequestID, "YA_RequestStaticFleetData", psResp, appEncoder, encryptResponses, "pending static fleet data response failed", "sent pending YA_RequestStaticFleetData response"); err != nil {
				return err
			}
		}
	}
	if len(state.pendingPlayerFleets) > 0 {
		pending := state.pendingPlayerFleets
		state.pendingPlayerFleets = nil
		for _, pf := range pending {
			pfResp := buildMmogRequestResponseFrame(pf.RequestID, pf.MsgType, "YA_PlayerFleets", state.playerPID, pf.Payload)
			log.WithFields(logrus.Fields{"remote": remote, "name": "YA_PlayerFleets", "hex": hex.EncodeToString(pfResp), "plain_len": len(pfResp)}).Info("mmog: fleet response hex dump")
			if err := writeMmogAppResponse(log, conn, remote, pf.RequestID, "YA_PlayerFleets", pfResp, appEncoder, encryptResponses, "pending player fleets response failed", "sent pending YA_PlayerFleets response"); err != nil {
				return err
			}
		}
	}
	if len(state.pendingPlayerPurchases) > 0 {
		pending := state.pendingPlayerPurchases
		state.pendingPlayerPurchases = nil
		for _, pp := range pending {
			ppResp := buildMmogRequestResponseFrame(pp.RequestID, pp.MsgType, "YA_GetPlayerPurchases", state.playerPID, pp.Payload)
			if err := writeMmogAppResponse(log, conn, remote, pp.RequestID, "YA_GetPlayerPurchases", ppResp, appEncoder, encryptResponses, "pending purchases response failed", "sent pending YA_GetPlayerPurchases response"); err != nil {
				return err
			}
		}
	}
	if len(state.pendingDailyContracts) > 0 {
		pending := state.pendingDailyContracts
		state.pendingDailyContracts = nil
		for _, dc := range pending {
			dcResp := buildMmogRequestResponseFrame(dc.RequestID, dc.MsgType, "YA_GetDailyContractsData", state.playerPID, dc.Payload)
			if err := writeMmogAppResponse(log, conn, remote, dc.RequestID, "YA_GetDailyContractsData", dcResp, appEncoder, encryptResponses, "pending daily contracts response failed", "sent pending YA_GetDailyContractsData response"); err != nil {
				return err
			}
		}
	}
	log.WithFields(logrus.Fields{
		"remote": remote,
		"source": source,
	}).Info("mmog: satisfied YA_PlayerGet bootstrap")
	return nil
}

func processMmogAppFrames(log *logrus.Logger, conn net.Conn, remote string, frames []protocol.AppFrame, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState) error {
	for _, frame := range frames {
		requestName := protocol.ExtractRequestName(frame.Payload)
		log.WithFields(logrus.Fields{
			"remote":  remote,
			"type":    frame.MsgType,
			"request": hex.EncodeToString(frame.RequestID[:]),
			"name":    requestName,
			"bytes":   len(frame.Payload),
			"hex":     hex.EncodeToString(frame.Payload),
			"text":    string(frame.Payload),
			"cipher":  encryptResponses,
		}).Info("mmog: application frame")

		if !state.loginResponseSent && requestName == "YA_UserLogin" {
			state.playerPID = protocol.ExtractPlayerPID(frame.Payload, defaultMmogPlayerPID, mmogJWTSecret())
			setGatewayPlayerDataReadyState(state.playerPID, false)
			log.WithFields(logrus.Fields{
				"remote": remote,
				"pid":    state.playerPID,
			}).Info("mmog: selected player PID")
			response := buildMmogLoginSuccessFrame(frame.RequestID, frame.MsgType, state.playerPID)
			if err := writeMmogAppResponse(log, conn, remote, frame.RequestID, requestName, response, appEncoder, encryptResponses, "login response failed", "sent YA_UserLogin success response"); err != nil {
				return err
			}
			state.loginResponseSent = true
			continue
		}
		if protocol.IsPingFrame(frame) {
			response := protocol.BuildPingResponseFrame(frame.RequestID, frame.Payload[0])
			if err := writeMmogAppResponse(log, conn, remote, frame.RequestID, requestName, response, appEncoder, encryptResponses, "ping response failed", "sent application ping response"); err != nil {
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
			if requestName == "YA_PlayerFleets" {
				state.playerFleetsReceived = true
			}
			// These bootstrap reads (YA_RequestStaticFleetData, YA_PlayerFleets,
			// YA_GetPlayerPurchases, YA_GetDailyContractsData) were previously
			// deferred until YA_PlayerGet was answered. But DreadGame.log shows the
			// client sends this batch and then blocks waiting for these exact
			// responses *before* it will send YA_PlayerGet — a mutual wait that only
			// broke via the client's ~44s request timeout, stalling hangar entry.
			// Every response here is built purely from state.playerPID, which is
			// already selected at YA_UserLogin, so there is no data dependency on
			// YA_PlayerGet: answer them immediately. handlePlayerGetSatisfied still
			// runs on YA_PlayerGet to flip the gateway inventory-ready gate.
			if isMmogPlayerMutationRequest(requestName) {
				if err := persistMmogPlayerMutation(state.playerPID, requestName, frame.Payload); err != nil {
					log.WithError(err).WithFields(logrus.Fields{
						"remote": remote,
						"name":   requestName,
						"pid":    state.playerPID,
					}).Warn("mmog: failed to persist player mutation")
				}
			}

			if requestName == "YA_PlayerGet" {
				// Testing the theory that the client only accepts player data
				// once it already has the market catalog in hand. Gateway
				// catalog endpoints no longer wait on player-data-ready (see
				// handleGWCatalog); instead this blocks (briefly, with a
				// timeout fallback so a client that never hits the gateway
				// still gets answered) until the client has fetched at least
				// one catalog endpoint.
				if !waitForGatewayCatalogFetched(state.playerPID, gatewayCatalogFetchedTimeout) {
					log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: answering YA_PlayerGet without observed catalog fetch (timeout)")
				}
			}
			if requestName == "YA_GetSeasonData" && seasonDataResponseDisabled {
				// See seasonDataResponseDisabled's doc comment: never answer
				// this request at all, so OnSeasonDataAvailable's delegate
				// broadcast (and the OnBackendDataAvailable recursion it
				// unconditionally triggers) never fires.
				log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: withholding YA_GetSeasonData response (quest-cycle recursion experiment)")
				continue
			}
			response := buildMmogRequestResponseFrame(frame.RequestID, frame.MsgType, requestName, state.playerPID, frame.Payload)
			if requestName == "YA_PlayerFleets" || requestName == "YA_RequestStaticFleetData" {
				log.WithFields(logrus.Fields{
					"remote":    remote,
					"name":      requestName,
					"hex":       hex.EncodeToString(response),
					"plain_len": len(response),
				}).Info("mmog: fleet response hex dump")
			}
			if err := writeMmogAppResponse(log, conn, remote, frame.RequestID, requestName, response, appEncoder, encryptResponses, "request response failed", "sent request response"); err != nil {
				return err
			}
			if requestName == "YA_PlayerFleets" {
				// UYFleetManager's readiness bitmask (this+0x110) is written exactly
				// once at connection setup and never again — confirmed live via a
				// hardware write breakpoint through an entire session. The delegate
				// meant to complete it, HandleMmogbrainFleetUpdated, recorded zero
				// hits across that same session despite a full YA_PlayerFleets round
				// trip, and its own "not ready" fallback explicitly re-requests
				// YA_PlayerFleets — evidence it expects a distinct server-pushed
				// update, not data embedded in this response.
				//
				// First attempt correlated this push to the same request ID as
				// YA_PlayerFleets (mirroring YA_UpdateGameModes, which worked). It had
				// no effect: fleet-init stayed at 4 and LogYShipLoadout stayed silent.
				// The per-request callback map is almost certainly one-shot — the
				// first frame for a request ID resolves and discards that pending
				// callback, so a second frame reusing the same ID is likely dropped
				// before any RT-name dispatch even sees it. Use a fresh, uncorrelated
				// ID instead so this arrives as a genuinely unsolicited push, the way
				// a real server-initiated fleet-update notification would.
				fleetUpdateID, err := uuid.NewRandom()
				if err != nil {
					log.WithError(err).Warn("mmog: failed to generate fleet update push id")
				} else {
					fleetUpdate := buildMmogFleetUpdatePush(state.playerPID)
					fleetUpdateFrame := protocol.BuildResponseFrame(fleetUpdateID, frame.MsgType, fleetUpdate)
					if err := writeMmogAppResponse(log, conn, remote, fleetUpdateID, "YA_FleetUpdate", fleetUpdateFrame, appEncoder, encryptResponses, "fleet update push failed", "sent YA_FleetUpdate push"); err != nil {
						return err
					}
				}
			}
			if requestName == "YA_PlayerGet" {
				if err := handlePlayerGetSatisfied(log, conn, remote, appEncoder, encryptResponses, state, "client-request"); err != nil {
					return err
				}
			}
			if requestName == "YA_GetGameConfigData" {
				// The client's MatchmakingInterpreter populates its playable-mode
				// list from a separate YA_UpdateGameModes message (top-level
				// GameModes array), not from the GameModes nested in the
				// YA_GetGameConfigData result. Push it right after the config
				// response so m_gameModes is non-empty and the hangar Play UI can
				// build. Correlate to the same request so ordering is preserved.
				gameModes := buildMmogUpdateGameModesPayload()
				gameModesFrame := protocol.BuildResponseFrame(frame.RequestID, frame.MsgType, gameModes)
				if err := writeMmogAppResponse(log, conn, remote, frame.RequestID, "YA_UpdateGameModes", gameModesFrame, appEncoder, encryptResponses, "game modes update failed", "sent YA_UpdateGameModes push"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func writeMmogAppResponse(log *logrus.Logger, conn net.Conn, remote string, requestID [16]byte, requestName string, response []byte, appEncoder *protocol.StreamCipher, encryptResponses bool, warnMsg string, infoMsg string) error {
	wire := response
	if encryptResponses {
		if appEncoder == nil {
			log.WithField("request", fmt.Sprintf("%x", requestID)).Warn("mmog: encrypt requested but encoder is nil")
			return fmt.Errorf("encrypt requested but encoder is nil")
		}
		wire = appEncoder.Encrypt(response)
	}
	// Frame-header correlation diagnostics: decode the outgoing frame header so we
	// can confirm the client can actually match this response to its request.
	// response is the full built frame: magic(2) size(2) type(2) reqID(16) payload.
	// Key things this surfaces: (1) the 16-bit `size` header field overflows for
	// payloads >~65513 bytes (e.g. YA_Tune), which would desync the client's frame
	// reader for every following frame; (2) whether the embedded request ID matches
	// the request we're answering.
	if len(response) > 0xffff {
		// The 16-bit frame size field cannot represent this length; sending it
		// would desync the client's frame stream and corrupt every following
		// response (this is exactly what an oversized YA_Tune did). Refuse rather
		// than silently corrupt the connection.
		log.WithFields(logrus.Fields{
			"remote":     remote,
			"name":       requestName,
			"frame_size": len(response),
			"max":        0xffff,
		}).Error("mmog: response frame exceeds 16-bit size limit; not sending (would desync stream)")
		return fmt.Errorf("mmog response %q too large for frame: %d bytes", requestName, len(response))
	}
	if len(response) >= 22 {
		embeddedID := response[6:22]
		if !bytes.Equal(embeddedID, requestID[:]) {
			log.WithFields(logrus.Fields{
				"remote":          remote,
				"name":            requestName,
				"req_id_param":    hex.EncodeToString(requestID[:]),
				"req_id_in_frame": hex.EncodeToString(embeddedID),
			}).Warn("mmog: response frame request-id mismatch (client cannot correlate)")
		}
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

func isMmogPlayerMutationRequest(requestName string) bool {
	switch requestName {
	case "YA_SavePlayerDisplayInformation",
		"YA_AddToFleet", "YA_RemoveFromFleet", "YA_SetFleetFlagship",
		"YA_UpdateShipLoadout", "YA_RenameShipLoadout", "YA_AddShipDefaultLoadouts",
		"YA_ChargeFleet", "YA_RepairFleet":
		return true
	default:
		return false
	}
}
