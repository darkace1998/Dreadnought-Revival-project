package jwt

import (
	"fmt"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// Claims are the JWT payload fields expected by the Dreadnought client.
// Used only for the Parse return value; signing uses MapClaims (see Issue).
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Realm    string `json:"realm"`
	gojwt.RegisteredClaims
}

// Issue creates and signs a new JWT for the given user using HMAC-SHA256.
// audience should be "launcher" when called by the Dreadnought launcher
// (jwt.get.by_steam_ticket), or "dreadnought" for in-game authentication.
func Issue(secret []byte, userID, username, audience string, ttl time.Duration) (string, error) {
	now := time.Now()
	// Use MapClaims so "aud" is serialized as a plain string, not []string.
	// The C++ profileService does a strict string comparison on the aud field.
	claims := gojwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"realm":    "dreadnought.pc-us",
		"iss":      "Dreadnought-Revival-project",
		"sub":      userID,
		"aud":      audience,
		"exp":      now.Add(ttl).Unix(),
		"iat":      now.Unix(),
		// verifytoken.js parses this JWT and checks user_groups for access control.
		// Without "DREADNOUGHT PLAYER" the launcher shows Greybox_Error_TokenAccess.
		"user_groups": []string{"DREADNOUGHT PLAYER"},
	}
	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// Parse validates the token and returns the claims.
// Uses MapClaims so it accepts "aud" as either a string or an array.
func Parse(secret []byte, tokenStr string) (*Claims, error) {
	mc := gojwt.MapClaims{}
	token, err := gojwt.ParseWithClaims(tokenStr, mc, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, gojwt.WithLeeway(1*time.Minute))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	claims := &Claims{}
	claims.UserID, _ = mc["user_id"].(string)
	claims.Username, _ = mc["username"].(string)
	claims.Realm, _ = mc["realm"].(string)
	claims.Subject, _ = mc["sub"].(string)
	return claims, nil
}
