package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logrus.StandardLogger().Out
	logrus.SetOutput(&buf)
	t.Cleanup(func() { logrus.SetOutput(prev) })
	return &buf
}

func storedWeaponPrimary(t *testing.T, pid string, loadoutID int32) int32 {
	t.Helper()
	var v int32
	if err := currentMmogPlayerStateDB().QueryRow(`SELECT weapon_primary_id FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`, pid, loadoutID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// An honest edit -- the starter ship's own fitted items and its default look --
// must raise NOTHING: a false positive in enforce mode loses a real fit.
func TestLoadoutOwnershipHasNoFalsePositivesForAStarterFit(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "cccccccccccccccccccccccccccccccc"
	_ = buildMmogPlayerGetPayload(pid)
	logs := captureWarnings(t)
	for _, starter := range starterShipLoadouts() {
		m := protocol.AppendInt32Field(nil, "LoadoutID", starter.loadoutID())
		m = protocol.AppendInt32Field(m, "WeaponPrimary", starter.weaponPrimaryItemID())
		m = protocol.AppendInt32Field(m, "WeaponSecondary", starter.weaponSecondaryItemID())
		m = protocol.AppendStringField(m, "DisplayInfo", starter.displayInfo())
		if err := persistMmogPlayerMutation(pid, "YA_UpdateShipLoadout", m); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(logs.String(), "unowned") {
		t.Fatalf("an honest starter fit was flagged:\n%s", logs.String())
	}
}

func TestLoadoutOwnershipWarnsThenEnforces(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "cccccccccccccccccccccccccccccccc"
	_ = buildMmogPlayerGetPayload(pid)
	starter := starterShipLoadouts()[0]
	// Any catalog weapon the new player does not own, in either id form.
	owned := ownedItemSet(pid)
	var unownedWeapon int32
	for _, id := range sortedMarketCatalogItemIDs() {
		if (id>>24)&0xff == 5 && !owned[id] && !owned[sharedGearID(id)] {
			unownedWeapon = id
			break
		}
	}
	if unownedWeapon == 0 {
		t.Fatal("no unowned weapon in the catalog to test with")
	}
	send := func() {
		m := protocol.AppendInt32Field(nil, "LoadoutID", starter.loadoutID())
		m = protocol.AppendInt32Field(m, "WeaponPrimary", unownedWeapon)
		m = protocol.AppendStringField(m, "DisplayInfo", "-1#-1#-1#-1;"+strconv.Itoa(int(vanityPaidEmblem))+";-1;-1;-1")
		if err := persistMmogPlayerMutation(pid, "YA_UpdateShipLoadout", m); err != nil {
			t.Fatal(err)
		}
	}

	logs := captureWarnings(t)
	send() // default mode: warn
	if storedWeaponPrimary(t, pid, starter.loadoutID()) != unownedWeapon {
		t.Error("warn mode must still store the fit")
	}
	for _, want := range []string{"unowned item fitted", "unowned cosmetic"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("warn mode did not log %q:\n%s", want, logs.String())
		}
	}

	t.Setenv("DN_LOADOUT_OWNERSHIP", "enforce")
	_, _ = currentMmogPlayerStateDB().Exec(`UPDATE player_ship_loadouts SET weapon_primary_id=?, display_info='' WHERE user_id=? AND loadout_id=?`,
		starter.weaponPrimaryItemID(), pid, starter.loadoutID())
	send()
	if got := storedWeaponPrimary(t, pid, starter.loadoutID()); got != starter.weaponPrimaryItemID() {
		t.Errorf("enforce mode stored unowned weapon %d", got)
	}
	var info string
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT COALESCE(display_info,'') FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`, pid, starter.loadoutID()).Scan(&info)
	if strings.Contains(info, strconv.Itoa(int(vanityPaidEmblem))) {
		t.Error("enforce mode stored an unpaid cosmetic")
	}
}
