package main

import (
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

			switch handshakeStage {
			case 0:
				if protocol.IsHandshakePacket(data) {
					if len(data) >= 22 {
						clientNonce = append([]byte(nil), data[6:22]...)
					}
					if writeErr := protocol.SendSeedResponse(conn, data); writeErr != nil {
						log.WithError(writeErr).WithField("remote", remote).Warn("mmog: seed response failed")
						return
					}
					log.WithField("remote", remote).Info("mmog: sent seed response")
					handshakeStage = 1
				}
			case 1:
				if protocol.IsDigestPacket(data) {
					if writeErr := protocol.SendConnectedPing(conn, data); writeErr != nil {
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
					handshakeStage = 2
				}
			case 2:
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

type mmogConnState struct {
	loginResponseSent        bool
	playerGetResponded       bool
	pendingPlayerPurchases   *protocol.AppFrame // delayed until YA_PlayerGet marks bootstrap ready
	pendingDailyContracts    *protocol.AppFrame // delayed until YA_PlayerGet avoids early quest-cycle recursion
	staticFleetDataReceived  bool
	fleetEligibilityReceived bool
	playerFleetsReceived     bool
	playerPID                string
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
	bootstrapID := syntheticRequestID(0xf1)
	if !state.staticFleetDataReceived {
		staticResp := protocol.BuildResponseFrame(bootstrapID, 0x0320, buildMmogStaticFleetDataPayloadForPlayer(state.playerPID))
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_RequestStaticFleetData", staticResp, appEncoder, encryptResponses, "proactive static fleet data push failed", "sent proactive YA_RequestStaticFleetData push"); err != nil {
			return err
		}
		state.staticFleetDataReceived = true
	}
	if state.pendingPlayerPurchases != nil {
		pp := state.pendingPlayerPurchases
		state.pendingPlayerPurchases = nil
		ppResp := buildMmogRequestResponseFrame(pp.RequestID, pp.MsgType, "YA_GetPlayerPurchases", state.playerPID, pp.Payload)
		if err := writeMmogAppResponse(log, conn, remote, pp.RequestID, "YA_GetPlayerPurchases", ppResp, appEncoder, encryptResponses, "pending purchases response failed", "sent pending YA_GetPlayerPurchases response"); err != nil {
			return err
		}
	}
	// If client never requested YA_FleetEligibility or YA_PlayerFleets
	// (reconnect session sending only YA_PlayerGet), push them proactively.
	if !state.fleetEligibilityReceived {
		eligResp := protocol.BuildResponseFrame(bootstrapID, 0x0320, buildMmogFleetEligibilityPayload())
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_FleetEligibility", eligResp, appEncoder, encryptResponses, "proactive fleet eligibility push failed", "sent proactive YA_FleetEligibility push"); err != nil {
			return err
		}
	}
	if !state.playerFleetsReceived {
		fleetsResp := protocol.BuildResponseFrame(bootstrapID, 0x0320, buildMmogPlayerFleetsPayload(state.playerPID))
		if err := writeMmogAppResponse(log, conn, remote, bootstrapID, "YA_PlayerFleets", fleetsResp, appEncoder, encryptResponses, "proactive fleet push failed", "sent proactive YA_PlayerFleets push"); err != nil {
			return err
		}
	}
	if state.pendingDailyContracts != nil {
		dc := state.pendingDailyContracts
		state.pendingDailyContracts = nil
		dcResp := buildMmogRequestResponseFrame(dc.RequestID, dc.MsgType, "YA_GetDailyContractsData", state.playerPID, dc.Payload)
		if err := writeMmogAppResponse(log, conn, remote, dc.RequestID, "YA_GetDailyContractsData", dcResp, appEncoder, encryptResponses, "pending daily contracts response failed", "sent pending YA_GetDailyContractsData response"); err != nil {
			return err
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
			state.playerPID = protocol.ExtractPlayerPID(frame.Payload, defaultMmogPlayerPID)
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
				saved := frame
				state.pendingPlayerPurchases = &saved
				log.WithField("remote", remote).Info("mmog: delaying YA_GetPlayerPurchases response until YA_PlayerGet is answered")
				continue
			}
			if requestName == "YA_PlayerFleets" {
				state.playerFleetsReceived = true
			}
			if requestName == "YA_GetDailyContractsData" && !state.playerGetResponded {
				// The quest cycle needs this response eventually, but answering it
				// before YA_PlayerGet can recurse through OnBackendDataAvailable.
				saved := frame
				state.pendingDailyContracts = &saved
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
