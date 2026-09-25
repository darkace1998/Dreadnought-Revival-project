package server

import (
	"io"
	"testing"
	"time"
)

// Lines copied from run/battle-logs/battle-20260924-093129-port7781.log.
func TestPlayerEventReadsTheEngineJoinAndCloseLines(t *testing.T) {
	cases := []struct {
		line string
		want int
	}{
		{"[0014.42][114]LogNet: Join succeeded: 257", 1},
		{"[0576.15][788]LogNet: UNetConnection::Close: [UNetConnection] RemoteAddr: 10.0.0.26:53013, Name: IpConnection_0, " +
			"Driver: GameNetDriver IpNetDriver_0, IsServer: YES, PC: VH_YPlayerCtrl_BP_C_1, Owner: VH_YPlayerCtrl_BP_C_1, " +
			"Channels: 32, Time: 2026.09.24-07.41.06", -1},
		// Closed before login: never counted as a join, so not a leave either.
		{"[0001.00][  1]LogNet: UNetConnection::Close: [UNetConnection] RemoteAddr: 10.0.0.26:1, IsServer: YES, PC: NULL, Owner: NULL", 0},
		// The client side of the same event, and the per-channel line.
		{"[0001.00][  1]LogNet: UNetConnection::Close: [UNetConnection] RemoteAddr: 10.0.0.73:7779, IsServer: NO, PC: VH_YPlayerCtrl_BP_C_0,", 0},
		{"LogNet: UChannel::Close: Sending CloseBunch. ChIndex == 0. Name: [UChannel] ChIndex: 0, Closing: 0 " +
			"[UNetConnection] RemoteAddr: 10.0.0.26:53013, IsServer: YES, PC: VH_YPlayerCtrl_BP_C_1", 0},
		{"LogNet: Join request: /Game/Maps/Launch_P?TEAM=0?Name=?SplitscreenCount=1", 0},
		// Unprefixed plain copy (older captures): still counted.
		{"LogNet: Join succeeded: 257", 1},
	}
	for _, c := range cases {
		if got, _ := playerEvent(c.line); got != c.want {
			t.Errorf("playerEvent(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

// Regression, 2026-09-24 19:18: with GAME_WINEDEBUG=-all,warn+seh the one join
// arrived FOUR times, once glued to Wine's trace. Skipping ":seh:" lines dropped
// all four and the reaper stopped a live match ("no player joined"). Lines
// verbatim from run/battle-logs/battle-20260924-191327-port7777.log.
func TestOneJoinThroughWineTraceCountsOnce(t *testing.T) {
	lines := []string{
		`[0014.20][ 72]LogNet: Join succeeded: 2570024:warn:seh:OutputDebugStringW L"[0014.20][ 72]LogNet: Join succeeded: 257\r\n"`,
		`0024:warn:seh:dispatch_exception L"[0014.20][ 72]LogNet: Join succeeded: 257\r\n"`,
		`0024:warn:seh:OutputDebugStringA "[0014.20][ 72]LogNet: Join succeeded: 257\r\n"`,
		`0024:warn:seh:dispatch_exception "[0014.20][ 72]LogNet: Join succeeded: 257\r\n"`,
	}
	inst := &Instance{StartedAt: time.Now()}
	w := newLogWriter(io.Discard, nil, "t", false, nil)
	w.onPlayer = inst.onPlayerEvent
	for _, l := range lines {
		_, _ = w.Write([]byte(l + "\n"))
	}
	players, joined, _ := inst.PlayerState()
	if players != 1 || !joined {
		t.Fatalf("players=%d joined=%v, want 1 true", players, joined)
	}
}

func TestReapReason(t *testing.T) {
	start := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	idle, noJoin, maxLife := time.Minute, 5*time.Minute, 45*time.Minute
	cases := []struct {
		name       string
		now        time.Time
		players    int
		everJoined bool
		since      time.Time
		stop       bool
	}{
		{"fresh launch waiting for its player", start.Add(2 * time.Minute), 0, false, start, false},
		{"nobody came", start.Add(5 * time.Minute), 0, false, start, true},
		{"player in the match", start.Add(30 * time.Minute), 1, true, start.Add(time.Minute), false},
		{"player just left", start.Add(10*time.Minute + 30*time.Second), 0, true, start.Add(10 * time.Minute), false},
		{"player left a minute ago", start.Add(11 * time.Minute), 0, true, start.Add(10 * time.Minute), true},
		{"hard cap even with a player", start.Add(45 * time.Minute), 1, true, start.Add(time.Minute), true},
	}
	for _, c := range cases {
		got := reapReason(c.now, start, c.players, c.everJoined, c.since, idle, noJoin, maxLife)
		if (got != "") != c.stop {
			t.Errorf("%s: reapReason = %q, want stop=%v", c.name, got, c.stop)
		}
	}
}

func TestInstancePlayerCountNeverGoesNegative(t *testing.T) {
	inst := &Instance{StartedAt: time.Now()}
	inst.onPlayerEvent(-1)
	inst.onPlayerEvent(1)
	inst.onPlayerEvent(-1)
	inst.onPlayerEvent(-1)
	players, joined, _ := inst.PlayerState()
	if players != 0 || !joined {
		t.Errorf("players=%d joined=%v, want 0 true", players, joined)
	}
}
