package main

import (
	"strings"
	"testing"
)

// The client's fire-and-forget telemetry is acknowledged, not answered with
// "Unknown command" (it never reads the reply; see response_dispatcher.go).
func TestTelemetryIsAcknowledged(t *testing.T) {
	for _, name := range []string{"YA_GameModeEvent", "YA_AnalyticsInvestEvent",
		"YA_AnalyticsReceiveXPEvent", "YA_AnalyticsReceiveCreditsEvent"} {
		reply := string(buildMmogRequestResponsePayload(name, "00000000000000000000000000000001", nil))
		if strings.Contains(reply, "Unknown command") {
			t.Errorf("%s answered as unknown: %q", name, reply)
		}
		if reply != string(buildMmogRequestSuccessPayload(name)) {
			t.Errorf("%s: reply is not the plain acknowledgement", name)
		}
	}
}
