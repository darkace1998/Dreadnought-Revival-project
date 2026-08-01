package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// maxHandshakeBufferBytes bounds how much unconsumed data a connection may
// accumulate before completing the initial handshake. The real handshake
// (seed + digest packets) fits comfortably within a few hundred bytes; this
// is deliberately generous headroom, not a tight fit.
const maxHandshakeBufferBytes = 16 * 1024

// seasonDataResponseDisabled once withheld YA_GetSeasonData entirely, as a
// last-ditch attempt to stop the hangar-entry EXCEPTION_STACK_OVERFLOW in
// UYPlayerMPQuestCycle::OnBackendDataAvailable. It is off because that
// experiment was run and FAILED: the client crashed identically with the
// response withheld.
//
// A full recursive stack (5MB stack-only minidump, 2026-07-27) then showed
// why no server payload can prevent that crash. The cycle is entirely
// client-side and unconditional:
//
//	OnBackendDataAvailable (FUN_1403fe800) subscribes itself to the quest
//	collection's delegate at +0x48, then calls UYMPQuestsCollection's loader
//	(FUN_140404440). When every YMPQ_* quest asset is already resolved there
//	is nothing to async-load, so the loader invokes the "loaded" callback
//	(FUN_140402db0) SYNCHRONOUSLY — and that callback ends by broadcasting
//	the very +0x48 delegate just subscribed to, re-entering
//	OnBackendDataAvailable with identical state. Nothing advances, so it
//	recurses until the stack is exhausted (~2824 frames observed).
//
// Confirmed against a full crash dump: the collection holds 24 loaded quest
// entries, 0 pending loads, and both load counters at 0 — so the "is a load
// still outstanding?" guard both functions share is false on every pass.
//
// Nothing in that path reads server data: the quest list comes from the
// client's own MPQuestCollection.uasset, and the season/event block we do
// feed (interface +0x44a0..+0x44c8) is passed to the loader as an argument
// it never reads. This is why every earlier server-side attempt — empty
// Seasons/Events, real season metadata, real vs fabricated daily-contract
// ids, delaying the response, and withholding it — changed nothing. Fixing
// it requires a client-side change, not a wire change.
const seasonDataResponseDisabled = false

// dailyContractsResponseDisabled withholds YA_GetDailyContractsData. See the
// use site for the full trace: answering it sets interface+0x44c8 = 1, which
// arms the UYPlayerMPQuestCycle recursion that crashes the client on hangar
// entry. Set DN_ANSWER_DAILY_CONTRACTS=1 to restore the old behaviour.
var dailyContractsResponseDisabled = os.Getenv("DN_ANSWER_DAILY_CONTRACTS") != "1"

// deferPlayerFleetsDisabled restores the old behaviour of answering
// YA_PlayerFleets immediately (DN_NO_DEFER_PLAYER_FLEETS=1). Escape hatch
// only; see the deferral site below for why deferring is the default.
var deferPlayerFleetsDisabled = os.Getenv("DN_NO_DEFER_PLAYER_FLEETS") == "1"

// deferTechTreeDisabled restores answering YA_GetTechTree immediately
// (DN_NO_DEFER_TECHTREE=1). Escape hatch only; see the deferral site for why
// deferring is the default.
var deferTechTreeDisabled = os.Getenv("DN_NO_DEFER_TECHTREE") == "1"

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
				// A quiet client is not a client with nothing waiting for it.
				// The match-ready and travel pushes are driven from here too,
				// so a player who stops sending frames while queued still gets
				// moved into their battle server within one read deadline
				// rather than whenever the client next happens to speak.
				if state.lastMsgType != 0 {
					if pushErr := pushMatchProgress(log, conn, remote, state.lastMsgType, appEncoder, state.lastEncrypted, state); pushErr != nil {
						log.WithError(pushErr).WithField("remote", remote).Warn("mmog: match progress push failed on idle tick")
						return
					}
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
	pendingTechTree          []protocol.AppFrame // delayed so its document is the LAST one stored
	tuneResponded            bool
	staticFleetDataReceived  bool
	fleetEligibilityReceived bool
	playerFleetsReceived     bool
	// gatewayReadySignalled records that we have told the gateway the client
	// has player data. Deliberately NOT set the moment we write the
	// YA_PlayerGet response: the client processes HTTP callbacks well before
	// it drains the mmog frame (observed live — market catalog handled on UE4
	// frame 96, "Player Data Received" only on frame 105), so signalling on
	// send lets the market fetch complete first and the client's
	// OnUpdateInventory then fires with no player data. We wait for the
	// client's NEXT read instead, which is evidence it has moved past the
	// player-data frame.
	gatewayReadySignalled bool
	playerPID             string
	// queuedForMatch is set while this player has an outstanding matchmaking
	// entry, so the frame loop polls for a formed match only during that
	// window rather than querying the DB on every one of the client's many
	// frames. serverStartingPushed makes the match-ready push fire once.
	queuedForMatch       bool
	serverStartingPushed bool
	// connectPushed makes the YA_Connect travel push fire once. It is separate
	// from serverStartingPushed because the two are different phases:
	// YA_ServerStarting says "a server is coming up", YA_Connect says "go here
	// now" and makes the client travel immediately.
	connectPushed bool
	// lastMsgType is the MsgType of the most recent inbound frame. Pushes sent
	// from the read-timeout path have no frame of their own to derive it from,
	// and reusing the client's last one is exactly the value the frame-driven
	// path would have used.
	lastMsgType uint16
	// lastEncrypted mirrors lastMsgType for the encryption flag: the client can
	// be answered in plaintext or under the stream cipher, and a push from the
	// timeout path has to match whatever mode its frames were last handled in.
	lastEncrypted bool
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

// flushPendingTechTree writes any tech tree response held back by the deferral
// in processMmogAppFrames. It is a no-op when nothing is pending.
func flushPendingTechTree(log *logrus.Logger, conn net.Conn, remote string, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState) error {
	if len(state.pendingTechTree) == 0 {
		return nil
	}
	pending := state.pendingTechTree
	state.pendingTechTree = nil
	for _, tt := range pending {
		ttResp := buildMmogRequestResponseFrame(tt.RequestID, tt.MsgType, "YA_GetTechTree", state.playerPID, tt.Payload)
		if err := writeMmogAppResponse(log, conn, remote, tt.RequestID, "YA_GetTechTree", ttResp, appEncoder, encryptResponses, "pending tech tree response failed", "sent pending YA_GetTechTree response"); err != nil {
			return err
		}
	}
	return nil
}

func handlePlayerGetSatisfied(log *logrus.Logger, conn net.Conn, remote string, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState, source string) error {
	state.playerGetResponded = true
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
	// Backstop: if YA_Tune never arrives, do not sit on the tech tree forever.
	if err := flushPendingTechTree(log, conn, remote, appEncoder, encryptResponses, state); err != nil {
		return err
	}
	log.WithFields(logrus.Fields{
		"remote": remote,
		"source": source,
	}).Info("mmog: satisfied YA_PlayerGet bootstrap")
	return nil
}

func processMmogAppFrames(log *logrus.Logger, conn net.Conn, remote string, frames []protocol.AppFrame, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState) error {
	// This runs once per socket read. Reaching here with the YA_PlayerGet
	// response already written means the client has come back for more, so it
	// has drained that frame — only now is it safe to let the gateway answer
	// the market catalog. See mmogConnState.gatewayReadySignalled.
	if state.playerGetResponded && !state.gatewayReadySignalled {
		state.gatewayReadySignalled = true
		setGatewayPlayerDataReadyState(state.playerPID, true)
		log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: client resumed after player data, releasing gateway catalog")
	}
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
			// The ping is the ONLY thing a queued client reliably sends, and it
			// is what makes the match pushes timely. Measured: the client pings
			// every 5s (1 byte, 0x10, type 0x0300) while sitting in the queue
			// and sends no named request at all, so a match formed at 01:02:17
			// was not announced until 01:03:12 -- 55s -- because this branch
			// continued past the push and the ping simultaneously kept resetting
			// the read deadline, so the idle-tick path never fired either. Both
			// escape routes were closed by the same one-byte frame.
			state.lastMsgType = frame.MsgType
			state.lastEncrypted = encryptResponses
			if err := pushMatchProgress(log, conn, remote, frame.MsgType, appEncoder, encryptResponses, state); err != nil {
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
			// Matchmaking queue tracking. Entering the queue arms the
			// match-ready poll below; leaving it (or being told there is no
			// match) disarms it. The match itself is formed asynchronously by
			// the background matchmaker, so the connection cannot know it is
			// ready at request time -- it discovers it on a later frame.
			if requestName == "YA_EnterMatchmaking" || requestName == "YA_SquadEnterMatchmaking" {
				state.queuedForMatch = true
				state.serverStartingPushed = false
				// connectPushed has to be rearmed here too, or a player who
				// queues a second time in one session never gets YA_Connect and
				// so never travels again.
				state.connectPushed = false
			}
			if requestName == "YA_LeaveMatchmaking" {
				state.queuedForMatch = false
				state.serverStartingPushed = false
				state.connectPushed = false
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

			// NOTE: YA_PlayerGet is deliberately answered immediately.
			// It used to block here (up to 1.5s) waiting for the client to
			// fetch a market catalog endpoint, on the theory that the client
			// only accepts player data once it holds the catalog. A verbose
			// client log disproved that and showed the wait actively causes a
			// bug: the client asks for player data FIRST (t+0ms), our delay
			// lets all five market responses land at t+13..64ms, and the
			// client's "market data complete" handler then runs
			// OnUpdateInventory at t+134ms — 66ms BEFORE our delayed player
			// data arrives at t+200ms — producing "Inventory of player data
			// not yet initialized!" and leaving the inventory unpopulated.
			// Player data must win the race, so the catalog endpoints now wait
			// on it instead (handleGWCatalog), matching what /bundles already
			// did.
			if requestName == "YA_GetSeasonData" && seasonDataResponseDisabled {
				log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: withholding YA_GetSeasonData response")
				continue
			}
			// Withhold YA_GetDailyContractsData: answering it is what arms the
			// client's hangar-entry stack overflow, and the payload cannot
			// avoid it.
			//
			// Traced in the shipping binary (the Ghidra export MISSES the
			// relevant write; it was found by byte-scanning .text for the
			// displacement, so re-derive that way rather than trusting the
			// decompile here):
			//
			//   FUN_142a33640 ("Sending daily contracts data request") issues
			//   this request on slot interface+0x3690. The response handler
			//   inside FUN_142a21cf0 matches that slot, then calls
			//   FUN_142a6b7f0(interface+0x44a0, payload) — which parses
			//   ContractConfigTable / ContractTable / ContractNextResetTime and
			//   then executes `*(byte *)(block + 0x28) = 1`, i.e.
			//   interface+0x44c8 = 1, UNCONDITIONALLY, whatever the contents —
			//   and finally broadcasts interface+0x1070.
			//
			//   interface+0x44c8 is the gate in
			//   UYPlayerMPQuestCycle::OnBackendDataAvailable (FUN_1403fe800):
			//   while it is 0 the cycle binds to interface+0x1070 and waits
			//   harmlessly; once it is 1 the cycle walks
			//   UYMPQuestsCollection instead, and because every YMPQ_* asset is
			//   already resolved the loader invokes the "loaded" callback
			//   synchronously, which re-broadcasts the delegate the cycle just
			//   subscribed to — unbounded recursion (~2824 frames observed) and
			//   EXCEPTION_STACK_OVERFLOW right after "Entering Hangar".
			//
			// This is why every earlier content-level attempt failed (empty
			// Quests arrays, real vs fabricated YMPQ_ ids, empty/real seasons):
			// the parser sets the gate before any of that is examined. Not
			// answering at all is the only lever, and it leaves the cycle in
			// its documented waiting state. Daily contracts are not needed to
			// reach an interactive hangar.
			if requestName == "YA_GetDailyContractsData" && dailyContractsResponseDisabled {
				log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: withholding YA_GetDailyContractsData response (arms the quest-cycle recursion)")
				continue
			}
			// Defer the fleet response until player data exists client-side.
			// Live Wine-client testing (2026-07-27) showed the client rejects a
			// byte-perfect, non-empty Fleets array with "Invalid fleet data,
			// fleet array is empty" + HandleMmogbrainError(8) whenever the
			// response lands before YA_PlayerGet; deferring it until after
			// YA_PlayerGet makes both messages disappear and the client stops
			// crashing during hangar entry.
			//
			// This deliberately re-enables the deferral the comment above
			// describes removing. That removal assumed a mutual wait — that the
			// client blocks on this response before sending YA_PlayerGet, so
			// only its ~44s timeout broke the deadlock. Live testing disproved
			// it: with YA_PlayerFleets left entirely unanswered the client still
			// sent YA_PlayerGet ~6s after login, with no stall.
			// Defer the tech tree so its document is the LAST one stored.
			//
			// Every blob the client parses this way lands in ONE shared slot at
			// mmogbrain+0x40a0 (FUN_142a14420), and each store broadcasts the
			// delegate at +0x1230. UYTechTreeManager subscribes to that delegate
			// (FUN_140403d60 -> FUN_140401900), and its loader FUN_1403ffde0
			// begins with FUN_140404e70 -- clear all tables -- then reads
			// whatever document is in the slot. So any later document silently
			// WIPES the tech tree, with nothing logged.
			//
			// Four fields write that slot: CatalogData (response slot 0x3660),
			// TechTrees (0x36b0), "packed" (YA_TuneReturn) and CatalogData
			// (YA_GetOffers). The client asks for the tech tree EARLY --
			// observed order is YA_GetTechTree ... YA_PlayerFleets, YA_Tune,
			// YA_GetSeasonData, YA_PlayerGet -- so YA_Tune is answered after it.
			// Holding the tech tree until YA_PlayerGet puts it after every one
			// of those, making ours the document the manager finally loads.
			if requestName == "YA_GetTechTree" && !deferTechTreeDisabled && !state.tuneResponded {
				state.pendingTechTree = append(state.pendingTechTree, frame)
				log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: deferring YA_GetTechTree until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_PlayerFleets" && !deferPlayerFleetsDisabled && !state.playerGetResponded {
				state.pendingPlayerFleets = append(state.pendingPlayerFleets, frame)
				log.WithFields(logrus.Fields{"remote": remote, "pid": state.playerPID}).Info("mmog: deferring YA_PlayerFleets until YA_PlayerGet is answered")
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
				// The client has no currency field in YA_PlayerGet at all, so
				// its credit and GP balances can only arrive through
				// YA_RewardCurrencies. Its handler assigns rather than adds, so
				// pushing the current balance here is idempotent. Correlate to
				// this request so it arrives after the player data it reflects.
				currencies := buildMmogRewardCurrenciesPayload(state.playerPID)
				currencyFrame := protocol.BuildResponseFrame(frame.RequestID, frame.MsgType, currencies)
				if err := writeMmogAppResponse(log, conn, remote, frame.RequestID, "YA_RewardCurrencies", currencyFrame, appEncoder, encryptResponses, "currency push failed", "sent YA_RewardCurrencies push"); err != nil {
					return err
				}
				if err := handlePlayerGetSatisfied(log, conn, remote, appEncoder, encryptResponses, state, "client-request"); err != nil {
					return err
				}
			}
			if requestName == "YA_LeaveMatchmaking" {
				// The response is only an ack. The client sets matchmaking state
				// 7 ("awaiting a cancellation response") when it sends the
				// request, and only the YA_LeftQueue push unwinds it -- see
				// buildMmogLeftQueuePayload. Without this the cancel button is
				// dead for the rest of the session.
				leftID, err := uuid.NewRandom()
				if err != nil {
					log.WithError(err).Warn("mmog: failed to generate left-queue push id")
				} else {
					leftFrame := protocol.BuildResponseFrame(leftID, frame.MsgType, buildMmogLeftQueuePayload(state.playerPID))
					if err := writeMmogAppResponse(log, conn, remote, leftID, "YA_LeftQueue", leftFrame, appEncoder, encryptResponses, "left queue push failed", "sent YA_LeftQueue push"); err != nil {
						return err
					}
				}
			}
			if requestName == "YA_Tune" {
				// YA_Tune's response carries the "packed" blob, which the client
				// parses into the SAME shared document slot the tech tree uses
				// (mmogbrain+0x40a0) and which re-fires the delegate that makes
				// UYTechTreeManager reload -- clearing its tables first. So the
				// tech tree has to land after this. Release it now rather than
				// waiting for YA_PlayerGet: the client is known to block on some
				// bootstrap responses before sending YA_PlayerGet, and a shorter
				// hold is less likely to trip that mutual wait.
				state.tuneResponded = true
				if err := flushPendingTechTree(log, conn, remote, appEncoder, encryptResponses, state); err != nil {
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
				// Pushed with a FRESH id, not the config request's.
				//
				// Correlating it to YA_GetGameConfigData meant reusing a request
				// id the client's pending-callback map had already resolved with
				// the config response itself, so this second frame was dropped
				// before any RT dispatch saw it -- the same one-shot behaviour
				// that made the first YA_FleetUpdate attempt a no-op. Evidence it
				// never ran: the client logged "Game modes list contains <0>
				// items" exactly ONCE, from the config handler's own call, and
				// never from this message's handler.
				gameModesID, err := uuid.NewRandom()
				if err != nil {
					log.WithError(err).Warn("mmog: failed to generate game modes push id")
				} else {
					gameModes := buildMmogUpdateGameModesPayload()
					gameModesFrame := protocol.BuildResponseFrame(gameModesID, frame.MsgType, gameModes)
					if err := writeMmogAppResponse(log, conn, remote, gameModesID, "YA_UpdateGameModes", gameModesFrame, appEncoder, encryptResponses, "game modes update failed", "sent YA_UpdateGameModes push"); err != nil {
						return err
					}
				}
			}
			state.lastMsgType = frame.MsgType
			state.lastEncrypted = encryptResponses
			if err := pushMatchProgress(log, conn, remote, frame.MsgType, appEncoder, encryptResponses, state); err != nil {
				return err
			}
		}
	}
	return nil
}

// pushMatchProgress delivers the two unsolicited messages that move a queued
// player into a battle server: YA_ServerStarting ("a server is coming up") and
// then YA_Connect ("go here now").
//
// It is called both from the frame loop and from the read-deadline timeout,
// which is the whole point. These pushes used to ride only on inbound frames,
// on the assumption the client is chatty enough for that to land "within a
// second or two". Measured, it is not: on 2026-08-02 a match formed at 00:36:37
// and neither push went out until 00:39:00 -- 143 seconds of the player staring
// at "Searching" -- because the client sent nothing at all in that window and
// the timeout path just continued. Driving it from the timeout as well bounds
// the wait by the read deadline instead of by the client's whim.
func pushMatchProgress(log *logrus.Logger, conn net.Conn, remote string, msgType uint16, appEncoder *protocol.StreamCipher, encryptResponses bool, state *mmogConnState) error {
	// Match-ready push. The queuedForMatch gate keeps the DB query out of the
	// hot path for everyone who is not in a queue.
	//
	// The client never learned a match was ready before this existed: the
	// matchmaker recorded the match and requested a battle server, but nothing
	// pushed YA_ServerStarting, so the player sat in the queue forever. Pushed
	// with a fresh id, as an unsolicited server message, like YA_FleetUpdate.
	if state.queuedForMatch && !state.serverStartingPushed {
		status := currentMmogMatchmakingStatus(state.playerPID)
		if status.state == "matched" && status.serverIP != "" {
			pushID, err := uuid.NewRandom()
			if err != nil {
				log.WithError(err).Warn("mmog: failed to generate server-starting push id")
			} else {
				payload := buildMmogServerStartingPayload(status)
				pushFrame := protocol.BuildResponseFrame(pushID, msgType, payload)
				if err := writeMmogAppResponse(log, conn, remote, pushID, "YA_ServerStarting", pushFrame, appEncoder, encryptResponses, "server starting push failed", "sent YA_ServerStarting push"); err != nil {
					return err
				}
				state.serverStartingPushed = true
				log.WithFields(logrus.Fields{
					"remote": remote, "pid": state.playerPID,
					"server": fmt.Sprintf("%s:%d", status.serverIP, status.serverPort),
					"match":  status.matchID, "mode": status.gameMode, "map": status.mapName,
				}).Info("mmog: match ready, pushed battle server address to client")
			}
		}
	}

	// Travel push. YA_ServerStarting alone leaves the client sitting on "Battle
	// server starting" forever: the client's dispatcher (the YA_Connect arm at
	// 0x142a271f5) waits for a SECOND push before it moves, reading
	// Connect/Team/DediID/Room/PVEEvent in that order and then running
	//
	//	TRAVEL <Connect>?TEAM=<Team>
	//
	// Deliberately a sibling of the YA_ServerStarting push, not nested inside
	// it: that push sets serverStartingPushed and so closes its own gate, and
	// this one has to stay reachable on later passes while the delay runs down.
	if state.serverStartingPushed && !state.connectPushed {
		status := currentMmogMatchmakingStatus(state.playerPID)
		ready := status.state == "matched" && status.serverIP != "" &&
			!status.createdAt.IsZero() &&
			time.Since(status.createdAt) >= mmogConnectPushDelay
		if ready {
			pushID, err := uuid.NewRandom()
			if err != nil {
				log.WithError(err).Warn("mmog: failed to generate connect push id")
			} else {
				payload := buildMmogConnectPushPayload(status)
				pushFrame := protocol.BuildResponseFrame(pushID, msgType, payload)
				if err := writeMmogAppResponse(log, conn, remote, pushID, "YA_Connect", pushFrame, appEncoder, encryptResponses, "connect push failed", "sent YA_Connect push"); err != nil {
					return err
				}
				state.connectPushed = true
				state.queuedForMatch = false
				log.WithFields(logrus.Fields{
					"remote": remote, "pid": state.playerPID,
					"connect": net.JoinHostPort(status.serverIP, strconv.Itoa(int(status.serverPort))),
					"team":    status.team, "match": status.matchID,
				}).Info("mmog: pushed YA_Connect, client should now travel to the battle server")
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
		"YA_ChargeFleet", "YA_RepairFleet",
		"YA_SaveGame", "YA_SaveCtAData":
		return true
	default:
		return false
	}
}
