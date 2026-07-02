package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/dreadnought-ps/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

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
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
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

			handshakeBuf = append(handshakeBuf, data...)

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

func mmogJWTSecret() []byte {
	return []byte(getenv("JWT_SECRET", "changeme-dreadnought-jwt-secret"))
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
			if requestName == "YA_GetPlayerPurchases" && !state.playerGetResponded {
				state.pendingPlayerPurchases = append(state.pendingPlayerPurchases, frame)
				log.WithField("remote", remote).Info("mmog: delaying YA_GetPlayerPurchases response until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_PlayerFleets" && !state.playerGetResponded && (!state.staticFleetDataReceived || !state.fleetEligibilityReceived) {
				state.pendingPlayerFleets = append(state.pendingPlayerFleets, frame)
				log.WithField("remote", remote).Info("mmog: delaying YA_PlayerFleets response until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_RequestStaticFleetData" && !state.playerGetResponded {
				state.pendingStaticFleetData = append(state.pendingStaticFleetData, frame)
				log.WithField("remote", remote).Info("mmog: delaying YA_RequestStaticFleetData response until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_PlayerFleets" {
				state.playerFleetsReceived = true
			}
			if requestName == "YA_GetDailyContractsData" && !state.playerGetResponded {
				// The quest cycle needs this response eventually, but answering it
				// before YA_PlayerGet can recurse through OnBackendDataAvailable.
				state.pendingDailyContracts = append(state.pendingDailyContracts, frame)
				log.WithField("remote", remote).Info("mmog: delaying YA_GetDailyContractsData response until YA_PlayerGet is answered")
				continue
			}
			if isMmogPlayerMutationRequest(requestName) {
				if err := persistMmogPlayerMutation(state.playerPID, requestName, frame.Payload); err != nil {
					log.WithError(err).WithFields(logrus.Fields{
						"remote": remote,
						"name":   requestName,
						"pid":    state.playerPID,
					}).Warn("mmog: failed to persist player mutation")
				}
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
			if requestName == "YA_PlayerGet" {
				if err := handlePlayerGetSatisfied(log, conn, remote, appEncoder, encryptResponses, state, "client-request"); err != nil {
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
