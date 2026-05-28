package protocol

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const GatewayJWTIssuer = "Dreadnought-Revival-project"

func ExtractPlayerPID(payload []byte, defaultPID string, secret []byte) string {
	ticket := ExtractStringField(payload, "Ticket")
	if ticket == "" {
		return defaultPID
	}

	if pid, err := ExtractVerifiedPlayerPIDFromJWT(ticket, secret, "launcher", "dreadnought"); err == nil && pid != "" {
		return pid
	}
	if looksLikeJWT(ticket) {
		return defaultPID
	}

	sum := md5.Sum([]byte(ticket))
	return hex.EncodeToString(sum[:])
}

func ExtractVerifiedPlayerPIDFromJWT(token string, secret []byte, audiences ...string) (string, error) {
	claims, err := VerifiedJWTClaims(token, secret, audiences...)
	if err != nil {
		return "", err
	}
	pid := GatewayPlayerDataReadyKey(GatewayClaimsUserID(claims))
	if pid == "" {
		return "", fmt.Errorf("JWT missing normalized player identity")
	}
	return pid, nil
}

func VerifiedJWTClaims(token string, secret []byte, audiences ...string) (jwt.MapClaims, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("empty JWT")
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("empty JWT secret")
	}

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithLeeway(time.Minute))
	if err != nil || !parsed.Valid {
		if err == nil {
			err = fmt.Errorf("invalid JWT")
		}
		return nil, err
	}
	if !claimsHasIssuer(claims, GatewayJWTIssuer) {
		return nil, fmt.Errorf("invalid issuer")
	}
	if !claimsHasString(claims, "realm", "dreadnought.pc-us") {
		return nil, fmt.Errorf("invalid realm")
	}
	if len(audiences) > 0 && !claimsHasAudience(claims, audiences...) {
		return nil, fmt.Errorf("invalid audience")
	}
	return claims, nil
}

func claimsHasIssuer(claims jwt.MapClaims, issuer string) bool {
	if value, _ := claims["iss"].(string); value == issuer {
		return true
	}
	if value, _ := claims["Issuer"].(string); value == issuer {
		return true
	}
	return false
}

func claimsHasString(claims jwt.MapClaims, key string, want string) bool {
	if value, _ := claims[key].(string); value == want {
		return true
	}
	if key == "realm" {
		value, _ := claims["Realm"].(string)
		return value == want
	}
	if key == "iss" {
		value, _ := claims["Issuer"].(string)
		return value == want
	}
	return false
}

func looksLikeJWT(value string) bool {
	return strings.Count(value, ".") == 2
}

func claimsHasAudience(claims jwt.MapClaims, audiences ...string) bool {
	allowed := map[string]struct{}{}
	for _, audience := range audiences {
		if strings.TrimSpace(audience) != "" {
			allowed[audience] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return true
	}

	switch aud := claims["aud"].(type) {
	case string:
		_, ok := allowed[aud]
		return ok
	case []string:
		for _, value := range aud {
			if _, ok := allowed[value]; ok {
				return true
			}
		}
	case []any:
		for _, raw := range aud {
			value, _ := raw.(string)
			if _, ok := allowed[value]; ok {
				return true
			}
		}
	}
	return false
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
