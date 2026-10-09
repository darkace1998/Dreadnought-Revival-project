package main

import (
	"bytes"
	"strconv"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Buying a bundle changes four things the client keeps its own copy of: the
// ships, the owned list, the balances and Elite. Each must reach an online
// client in-session; before 2026-10-09 the Elite days (and with them the
// elite contract slot) only appeared after a restart.
func TestBundlePurchaseRefreshesTheClientInSession(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	h.connected(pid)
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	b, _ := marketBundleByID(marketBundleIDBase + 2) // Renegade Stash: a hero, cosmetics, credits, 30 Elite days
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=? WHERE user_id=?`, b.priceGP(), pid); err != nil {
		t.Fatal(err)
	}
	req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
	req = append(req, protocol.AppendStringField(nil, "offer", "999"+strconv.Itoa(int(b.id)))...)
	if got := protocol.ExtractStringField(buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req)), "result"); got != "bought" {
		t.Fatalf("bundle purchase: %q", got)
	}

	pushed := map[string][]byte{}
	for _, p := range h.drainPushes(pid) {
		pushed[protocol.FirstStringField(p, "RT")] = p
	}
	for _, rt := range []string{"YA_ClaimItem", "YA_FleetUpdate", "YA_PushInventory", "YA_RewardCurrencies",
		"YA_MembershipChanged", "YA_ContractRefresh"} {
		if pushed[rt] == nil {
			t.Errorf("no %s push after buying a bundle", rt)
		}
	}
	if inv := pushed["YA_PushInventory"]; inv != nil && !bytes.Contains(inv, []byte("Items")) {
		t.Error("YA_PushInventory carries no inventory.Items")
	}
	// ExpireTime at the ROOT, as the numeric string YA_PlayerGet sends.
	want := protocol.AppendStringField(nil, "ExpireTime", strconv.Itoa(int(membershipExpiresAt(pid))))
	if m := pushed["YA_MembershipChanged"]; m != nil && !bytes.Contains(m, want) {
		t.Errorf("YA_MembershipChanged lacks %q", want)
	}
}

// Only an active membership is pushed: an ExpireTime of 0 sent the client
// into a stack overflow at login, and a lapsed one changes nothing it shows.
func TestMembershipChangedOnlyForAnActiveMembership(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, ok := buildMmogMembershipChangedPush(pid, now); ok {
		t.Error("pushed a membership the player never had")
	}
	if _, err := database.Exec(`INSERT INTO player_membership(user_id,expires_at) VALUES(?,?)`, pid, now.Unix()-60); err != nil {
		t.Fatal(err)
	}
	if _, ok := buildMmogMembershipChangedPush(pid, now); ok {
		t.Error("pushed an expired membership")
	}
	if _, err := database.Exec(`UPDATE player_membership SET expires_at=? WHERE user_id=?`, now.Unix()+3600, pid); err != nil {
		t.Fatal(err)
	}
	if _, ok := buildMmogMembershipChangedPush(pid, now); !ok {
		t.Error("an active membership was not pushed")
	}
}
