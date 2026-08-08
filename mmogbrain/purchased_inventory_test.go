package main

import (
	"strconv"
	"testing"
)

// "tried to buy it but it never updated" -- reported live 2026-08-08 after
// unlocking module 83820825 on the Rurik.
//
// The unlock itself was fine: YA_UnlockItem arrived, persistUnlockItem charged
// the free XP and wrote the player_purchases row. But the owned-item inventory
// in YA_PlayerGet was built from starterOwnedInventorySeeds() alone -- a static
// list -- so the bought item had nowhere to surface. That array is the client's
// only route to module ownership (UYInventoryManager::UpdateItemsFromInventory
// reads it), which is also why grantUnlockedShipLoadout exists separately for
// hulls: a purchases row does not convince the client of anything on its own,
// established when it kept re-sending YA_UnlockItem for an id already in
// player_purchases.

func TestPurchasedItemAppearsInTheOwnedInventory(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"

	// The exact module the operator bought.
	const bought int32 = 83820825

	before := string(buildMmogPlayerGetPayload(pid))
	if countWireStringField(before, "ItemID", strconv.Itoa(int(bought))) != 0 {
		t.Fatalf("item %d is already in the starter inventory; pick one that is not "+
			"or this test proves nothing", bought)
	}

	database := currentMmogPlayerStateDB()
	if _, err := database.Exec(
		`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		 VALUES(?,?,?,?,?)`, pid, bought, "weapon", 1000, "freexp"); err != nil {
		t.Fatalf("record purchase: %v", err)
	}

	after := string(buildMmogPlayerGetPayload(pid))
	if countWireStringField(after, "ItemID", strconv.Itoa(int(bought))) == 0 {
		t.Errorf("item %d was bought but does not appear in the owned-item inventory; "+
			"the client cannot learn it owns it", bought)
	}
}

// Buying something the player already had must not duplicate the entry -- the
// client reads Amount per entry, and two entries for one item is not "two of
// them", it is a malformed list.
func TestBuyingAStarterItemDoesNotDuplicateItsEntry(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"

	seeds := starterOwnedInventorySeeds()
	if len(seeds) == 0 {
		t.Fatal("no starter inventory to exercise")
	}
	existing := seeds[0].itemID
	if existing == 0 {
		t.Fatal("starter seed has no item id")
	}

	// player_purchases has a foreign key onto player_state, so the account has
	// to exist before anything can be bought against it.
	mmogPlayerStateForPID(pid)

	database := currentMmogPlayerStateDB()
	if _, err := database.Exec(
		`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		 VALUES(?,?,?,?,?)`, pid, existing, "weapon", 1000, "freexp"); err != nil {
		t.Fatalf("record purchase: %v", err)
	}

	payload := string(buildMmogPlayerGetPayload(pid))
	if n := countWireStringField(payload, "ItemID", strconv.Itoa(int(existing))); n != 1 {
		t.Errorf("item %d appears %d times in the inventory, want 1", existing, n)
	}
}

// A player who has bought nothing must get exactly the starter list, so this
// change cannot quietly alter a fresh account.
func TestFreshAccountInventoryIsUnchanged(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"

	payload := string(buildMmogPlayerGetPayload(pid))
	seen := 0
	for _, seed := range starterOwnedInventorySeeds() {
		if seed.itemID == 0 {
			continue
		}
		if countWireStringField(payload, "ItemID", strconv.Itoa(int(seed.itemID))) == 0 {
			t.Errorf("starter item %d is missing from a fresh account's inventory", seed.itemID)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("no starter items checked")
	}
	t.Logf("%d starter items present", seen)
}
