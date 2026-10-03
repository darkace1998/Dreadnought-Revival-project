package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Live captures (2026-10-03).
const (
	telemetryCreditsHex  = "025254091f00000059415f416e616c797469637352656365697665437265646974734576656e7408426174746c654944092400000033623062316535302d306335662d346235362d383565332d64626663666637343935326307437265646974730d51010000074372456e7472790c2500000006416d6f756e7456b603000006536f75726365090700000052616e6b205570000e6f000000074372456e7472790c2500000006416d6f756e7456ee02000006536f75726365090700000053636f72696e67000ea1000000074372456e7472790c2900000006416d6f756e7456c800000006536f75726365090b00000053636f72696e6742617365000ed3000000074372456e7472790c2800000006416d6f756e7456c902000006536f75726365090a000000426f6f7374657257696e000e09010000074372456e7472790c3500000006416d6f756e7456ee00000006536f757263650917000000426f6f73746572466972737457696e4f66546865446179000e3e010000074372456e7472790c2d00000006416d6f756e7456b503000006536f75726365090f000000476f6c64204d656d62657273686970000e80010000000e62000000000e00000000"
	telemetryInvestHex   = "025254091700000059415f416e616c7974696373496e766573744576656e74066974656d49445624030e050673686970587056e80300000666726565587056000000000f72656d61696e696e6753686970587056aa0500000f72656d61696e696e6746726565587056fb040000000e00000000"
	telemetryGameModeHex = "025254091000000059415f47616d654d6f64654576656e740847616d654d6f646509020000005445054576656e74090d000000537461727465645f4d6174636805626567696e0501000e00000000"
)

func telemetryPayload(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Telemetry is acknowledged as before AND stored, every field kept, with a
// readable summary.
func TestTelemetryIsStored(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ rt, hex, summary, battle string }{
		{"YA_AnalyticsReceiveCreditsEvent", telemetryCreditsHex, "Rank Up 950; Scoring 750; ScoringBase 200; BoosterWin 713; BoosterFirstWinOfTheDay 238; Gold Membership 949", "3b0b1e50-0c5f-4b56-85e3-dbfcff74952c"},
		{"YA_GameModeEvent", telemetryGameModeHex, "TE Started_Match", ""},
	}
	for _, c := range cases {
		reply := buildMmogRequestResponsePayload(c.rt, pid, telemetryPayload(t, c.hex))
		if string(reply) != string(buildMmogRequestSuccessPayload(c.rt)) {
			t.Errorf("%s: reply changed", c.rt)
		}
		var summary, battle, fields string
		if err := database.QueryRow(`SELECT summary, battle_id, fields FROM client_telemetry WHERE rt=?`, c.rt).Scan(&summary, &battle, &fields); err != nil {
			t.Fatalf("%s not stored: %v", c.rt, err)
		}
		if summary != c.summary || battle != c.battle {
			t.Errorf("%s: summary %q battle %q, want %q / %q", c.rt, summary, battle, c.summary, c.battle)
		}
		if strings.Contains(fields, "\"RT\"") || len(fields) < 10 {
			t.Errorf("%s: fields %s", c.rt, fields)
		}
	}
}

// The client's free XP after a research is checked against the server's.
func TestTelemetryInvestCheck(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	// The capture says remainingFreeXp=1275.
	for _, c := range []struct {
		serverFree int
		want       string
	}{{1275, "matches the server's free XP"}, {9999, "MISMATCH: server free XP 9999"}} {
		if _, err := database.Exec(`UPDATE player_state SET free_xp=? WHERE user_id=?`, c.serverFree, pid); err != nil {
			t.Fatal(err)
		}
		recordClientTelemetry(pid, "YA_AnalyticsInvestEvent", telemetryPayload(t, telemetryInvestHex))
		var summary string
		if err := database.QueryRow(`SELECT summary FROM client_telemetry ORDER BY id DESC LIMIT 1`).Scan(&summary); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(summary, "item 84804388: ship XP 1000, free XP 0; left ship XP 1450, free XP 1275") || !strings.HasSuffix(summary, c.want) {
			t.Errorf("server free XP %d: summary %q, want it to end %q", c.serverFree, summary, c.want)
		}
	}
}
