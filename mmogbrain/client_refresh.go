package main

import (
	"strconv"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// In-session refreshes for state the server changes behind the client's back.
//
// The client re-reads most of its state only at login. Each request it makes
// updates what that request changed (a purchase reply carries the owned list,
// and after it the client re-requests YA_GetPlayerPurchases and
// YA_GetPlayerProgression on its own -- measured in the 2026-10-09 logs). What
// it does NOT re-read is anything a request changes INDIRECTLY, and that only
// reached the client after a restart (operator, 2026-10-09):
//
//   - researching a hull (YA_UnlockItem) makes the ship's own look and, for a
//     T3+ ship, officer briefings owned (appendOwnedInventoryEntriesCounted:
//     officerBriefingsOwnedThroughShips, the ship's hull parts). Those are
//     owned-list items, and the UnlockItem reply carries no owned list.
//   - a bundle's Elite days (grantMarketBundle) and the elite contract slot.
//   - ships granted from the admin dashboard.

// pushInventoryRefresh sends the whole owned list as YA_PushInventory. The
// client's arm (dispatcher 0x2A318B1, read 2026-10-09): root "inventory" ->
// 0x142A6CED0 into player-data +0x39E8 (the same fill the YA_PurchaseItem
// reply uses, which is verified live) when present, the +0x26C0
// inventory-updated event, then 0x2A1FCF0 / 0x2A1FD80, which re-request
// progression and purchases.
func pushInventoryRefresh(pid string) {
	squadHubInstance.push(pid, buildMmogPushInventoryPayload(pid))
}

// buildMmogMembershipChangedPush is YA_MembershipChanged, the client's own
// message for a membership change (dispatcher 0x2A317F6): it hands the ROOT
// to the same parser YA_PlayerGet's "Membership" object goes through
// (0x2A85120 on player-data +0x3898, which reads "ExpireTime" as unix seconds),
// fires the player-data-changed event (+0xE40), then re-requests
// YA_GetDailyContractsData (0x2A33640).
//
// Only an ACTIVE membership is sent. An expiry in the past -- above all 0 --
// is what drove the client into its stack overflow at login
// (buildMmogPlayerDataPayload's Membership comment); nothing is lost, since
// an expired membership changes nothing the client shows.
func buildMmogMembershipChangedPush(pid string, now time.Time) ([]byte, bool) {
	expiresAt := membershipExpiresAt(pid)
	if int64(expiresAt) <= now.Unix() {
		return nil, false
	}
	var b []byte
	b = protocol.AppendStringField(b, "RT", "YA_MembershipChanged")
	b = protocol.AppendStringField(b, "ExpireTime", strconv.Itoa(int(expiresAt)))
	return b, true
}

// pushMembershipChanged tells the client its new Elite expiry, then its
// contracts: with Elite the fourth (elite) contract slot counts
// (currentContracts), and the contract re-request the client makes after
// YA_MembershipChanged returns only the catalogue, not the player's entries.
func pushMembershipChanged(pid string) {
	payload, ok := buildMmogMembershipChangedPush(pid, time.Now())
	if !ok {
		return
	}
	squadHubInstance.push(pid, payload)
	squadHubInstance.push(pid, buildMmogContractRefreshPush(pid))
}
