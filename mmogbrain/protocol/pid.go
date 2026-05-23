package protocol

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

func ExtractPlayerPID(payload []byte, defaultPID string) string {
	ticket := ExtractStringField(payload, "Ticket")
	if ticket == "" {
		return defaultPID
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

	return GatewayPlayerDataReadyKey(GatewayClaimsUserID(claims))
}

func GatewayPlayerDataReadyKey(playerPID string) string {
	return NormalizePlayerPID(playerPID)
}

func GatewayClaimsUserID(claims jwt.MapClaims) string {
	if uid, ok := claims["user_id"].(string); ok && strings.TrimSpace(uid) != "" {
		return uid
	}
	if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
		return sub
	}
	return ""
}

func NormalizePlayerPID(value string) string {
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

func NumericPlayerID(normalizedPID string) int32 {
	if len(normalizedPID) < 8 {
		return 1
	}
	value, err := strconv.ParseUint(normalizedPID[:8], 16, 32)
	if err != nil {
		return 1
	}
	return int32(value & 0x7fffffff)
}
