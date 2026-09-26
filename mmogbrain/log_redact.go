package main

import (
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

// Credentials must not reach the log files (audit 2026-09-26: 1152 complete
// player JWTs in mmogbrain.log, 132 in run/mmogbrain.log). They arrived through
// ~10 call sites that dump whole frames -- as text AND as hex -- plus the
// Firmament auth payload and a "token" field, so they are removed in ONE place:
// a hook on every logger, run before the entry is formatted. Anyone who can
// read the logs could otherwise sign in as any recently active player.
//
// Scrubbed: JWTs in any string field, the same JWTs hex-encoded (they begin
// "eyJ" = 65794a), and whole fields whose name says they are a credential.
type redactHook struct{}

func (redactHook) Levels() []logrus.Level { return logrus.AllLevels }

var jwtText = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`)

// credentialField names whose whole value is a secret.
func credentialField(key string) bool {
	k := strings.ToLower(key)
	return k == "ticket" || k == "jwt" || k == "password" || strings.HasSuffix(k, "token")
}

func (redactHook) Fire(entry *logrus.Entry) error {
	for key, value := range entry.Data {
		s, ok := value.(string)
		if !ok {
			continue
		}
		if credentialField(key) && s != "" {
			entry.Data[key] = "[redacted]"
			continue
		}
		entry.Data[key] = redactCredentials(s)
	}
	entry.Message = redactCredentials(entry.Message)
	return nil
}

func redactCredentials(s string) string {
	if strings.Contains(s, "eyJ") {
		s = jwtText.ReplaceAllString(s, "eyJ[redacted-jwt]")
	}
	if strings.Contains(s, "65794a") {
		s = redactHexJWT(s)
	}
	return s
}

// redactHexJWT replaces hex-encoded JWTs: "65794a" at a byte boundary,
// followed by hex pairs that decode to the JWT alphabet (base64url and '.'),
// at least 40 bytes long.
func redactHexJWT(s string) string {
	var b strings.Builder
	from := 0
	for {
		rel := strings.Index(s[from:], "65794a")
		if rel < 0 {
			b.WriteString(s[from:])
			return b.String()
		}
		i := from + rel
		if i%2 != 0 { // not a byte boundary of this hex string
			b.WriteString(s[from : i+1])
			from = i + 1
			continue
		}
		end := i
		for end+2 <= len(s) {
			v, err := hex.DecodeString(s[end : end+2])
			if err != nil || !jwtByte(v[0]) {
				break
			}
			end += 2
		}
		b.WriteString(s[from:i])
		if (end-i)/2 >= 40 {
			b.WriteString("[redacted-jwt-hex]")
		} else {
			b.WriteString(s[i:end])
		}
		from = end
	}
}

func jwtByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

// installLogRedaction adds the hook to the given logger and to logrus's
// package-level logger (used by the handlers and builders).
func installLogRedaction(log *logrus.Logger) {
	log.AddHook(redactHook{})
	logrus.AddHook(redactHook{})
}
