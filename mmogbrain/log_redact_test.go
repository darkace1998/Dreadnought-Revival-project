package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// A JWT shaped like the launcher's (header.payload.signature).
const sampleJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJmZWE5OTAzZCIsInVzZXJuYW1lIjoiVW5sb2NrQWxsIn0.c2lnbmF0dXJlX2J5dGVzX2hlcmVfMTIzNDU2Nzg5MA"

func TestLogsNeverContainPlayerTokens(t *testing.T) {
	var out bytes.Buffer
	log := logrus.New()
	log.SetOutput(&out)
	log.SetFormatter(&logrus.JSONFormatter{})
	log.AddHook(redactHook{})

	frame := "\x02RT\tYA_UserLogin\x05Ticket\t" + sampleJWT + "\x00"
	log.WithFields(logrus.Fields{
		"name":    "YA_UserLogin",
		"text":    frame,
		"hex":     hex.EncodeToString([]byte(frame)),
		"token":   sampleJWT,
		"payload": `{"method":"auth.refresh.redeem","params":{"token":"` + sampleJWT + `"}}`,
	}).Info("mmog: application frame")

	got := out.String()
	for _, leak := range []string{sampleJWT, hex.EncodeToString([]byte(sampleJWT)), sampleJWT[40:80]} {
		if strings.Contains(got, leak) {
			t.Fatalf("token material reached the log:\n%s", got)
		}
	}
	for _, keep := range []string{"YA_UserLogin", hex.EncodeToString([]byte("YA_UserLogin")), "[redacted]", "eyJ[redacted-jwt]", "[redacted-jwt-hex]"} {
		if !strings.Contains(got, keep) {
			t.Errorf("expected %q in the redacted line:\n%s", keep, got)
		}
	}
}

// Ordinary hex that happens to contain 65794a ("eyJ") briefly is not mangled.
func TestRedactionLeavesOrdinaryHexAlone(t *testing.T) {
	for _, s := range []string{
		hex.EncodeToString([]byte("eyJ short")),
		"0165794a02",
		hex.EncodeToString([]byte("GENDER_MALE;#iiS=872349903")),
	} {
		if got := redactCredentials(s); got != s {
			t.Errorf("redactCredentials(%q) = %q, want unchanged", s, got)
		}
	}
}
