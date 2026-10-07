package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

// capturedUpdatePayloadSlot is the captured Agosta save with its
// LoadoutSlotNum set to slot: the save the client makes for LOADOUT B.
func capturedUpdatePayloadSlot(t *testing.T, slot byte) []byte {
	t.Helper()
	const field = "4c6f61646f7574536c6f744e756d5601000000" // LoadoutSlotNum [i32] 1
	if !strings.Contains(capturedUpdateShipLoadout, field) {
		t.Fatal("fixture has no LoadoutSlotNum 1")
	}
	raw, err := hex.DecodeString(strings.Replace(capturedUpdateShipLoadout, field,
		strings.TrimSuffix(field, "01000000")+hex.EncodeToString([]byte{slot, 0, 0, 0}), 1))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func agostaLoadout(t *testing.T, pid string) mmogShipLoadoutSeed {
	t.Helper()
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		if l.precastLoadoutID == 33489262 {
			return l
		}
	}
	t.Fatal("no Agosta loadout")
	return mmogShipLoadoutSeed{}
}

// LOADOUT B did nothing: the client indexes a ship's loadout list by the
// category and every ship had one entry (0xAA8F60). With B each ship's second
// entry follows its first, under its own ID, and a save with LoadoutSlotNum 2
// is B's alone -- before, every save went to the one row whatever its slot.
func TestLoadoutBIsASecondLoadoutSavedApart(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	t.Setenv("DN_LOADOUT_B", "1")
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	a := agostaLoadout(t, pid)
	aID, bID := a.entryID(), a.entryID()+"_B"
	aLookBefore := a.displayInfo()

	if err := persistUpdateShipLoadout(database, pid, capturedUpdatePayloadSlot(t, 2)); err != nil {
		t.Fatal(err)
	}
	const saved = "335872027#335872028#335872026#335872025;352649226;369426573;386203669;402980885"
	if got := agostaLoadout(t, pid).displayInfo(); got != aLookBefore {
		t.Errorf("saving LOADOUT B changed A's look to %q", got)
	}
	var bLook string
	var bWeapon int32
	if err := database.QueryRow(`SELECT display_info, weapon_primary_id FROM player_ship_loadout_variants
		WHERE user_id=? AND loadout_id=33489262 AND slot=2`, pid).Scan(&bLook, &bWeapon); err != nil {
		t.Fatalf("no LOADOUT B row: %v", err)
	}
	if bLook != saved || bWeapon != 84213772 {
		t.Errorf("LOADOUT B row = %q / %d, want the saved look and weapon", bLook, bWeapon)
	}

	get := string(buildMmogPlayerGetPayload(pid))
	ai, bi := strings.Index(get, aID), strings.Index(get, bID)
	if ai < 0 || bi < 0 {
		t.Fatalf("YA_PlayerGet lacks A (%v) or B (%v)", ai >= 0, bi >= 0)
	}
	if next := strings.Index(get[ai+len(aID):], "Default__"); next < 0 || ai+len(aID)+next != bi {
		t.Error("LOADOUT B does not directly follow its A in ShipLoadouts")
	}
	if !strings.Contains(get[bi:], saved) {
		t.Error("LOADOUT B went out without its saved look")
	}

	b, ok := battleLoadoutFor(pid, bID)
	if !ok || b.displayInfo() != saved || b.variant != 1 {
		t.Errorf("/battle/loadout for B: ok=%v look=%q", ok, b.displayInfo())
	}
	if a2, ok := battleLoadoutFor(pid, aID); !ok || a2.variant != 0 || a2.displayInfo() != aLookBefore {
		t.Error("/battle/loadout for A no longer gives A")
	}

	// A save of A still goes to A.
	if err := persistUpdateShipLoadout(database, pid, capturedUpdatePayload(t)); err != nil {
		t.Fatal(err)
	}
	if got := agostaLoadout(t, pid).displayInfo(); got != saved {
		t.Errorf("LOADOUT A save not stored: %q", got)
	}
}

// On by default; DN_LOADOUT_B=0 turns it off, a list limits it.
func TestLoadoutBSwitch(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	bID := agostaLoadout(t, pid).entryID() + "_B"
	t.Setenv("DN_LOADOUT_B", "")
	if !strings.Contains(string(buildMmogPlayerGetPayload(pid)), bID) {
		t.Error("LOADOUT B is not on by default")
	}
	for _, setting := range []string{"0", "someone_else"} {
		t.Setenv("DN_LOADOUT_B", setting)
		if strings.Contains(string(buildMmogPlayerGetPayload(pid)), bID) {
			t.Errorf("DN_LOADOUT_B=%q sent a LOADOUT B", setting)
		}
	}
	t.Setenv("DN_LOADOUT_B", "x, "+pid)
	if !strings.Contains(string(buildMmogPlayerGetPayload(pid)), bID) {
		t.Error("a listed player got no LOADOUT B")
	}
}

// Heroes keep one loadout (GUESS, see shipLoadoutHasVariants).
func TestHeroesHaveNoLoadoutB(t *testing.T) {
	for _, h := range heroShipLoadouts {
		if shipLoadoutHasVariants(mmogShipLoadoutSeed{precastLoadoutID: h.loadoutID}) {
			t.Fatalf("hero %s gets a LOADOUT B", h.name)
		}
	}
}

// A ship bought in-session gets its LOADOUT B in the claim push, after A,
// so B works without a relog.
func TestClaimPushCarriesLoadoutB(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	t.Setenv("DN_LOADOUT_B", "")
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	a := agostaLoadout(t, pid)
	push, ok := buildMmogShipClaimPush(pid, a.precastLoadoutID)
	if !ok {
		t.Fatal("no claim push for the Agosta")
	}
	ai, bi := strings.Index(string(push), a.entryID()), strings.Index(string(push), a.entryID()+"_B")
	if ai < 0 || bi < 0 || bi < ai {
		t.Errorf("claim push: A at %d, B at %d; want both, A first", ai, bi)
	}
}
