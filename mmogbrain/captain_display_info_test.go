package main

import "testing"

// An empty captain slot makes a captain-screen button that does nothing, so
// empty slots (and the old default's untemplated suit) get the gender's
// default; everything the client saved is kept.
func TestRepairedCaptainDisplayInfo(t *testing.T) {
	cases := map[string]string{
		// The old server default: untemplated suit, no headwear.
		"GENDER_MALE;#iiS=872349903#iiH=872349840#iiHw=-1#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0": "GENDER_MALE;#iiS=872349706#iiH=872349840#iiHw=872349736#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0",
		// A bought suit is kept; only the empty headwear is filled.
		"GENDER_MALE;#iiS=872349895#iiH=872349841#iiHw=-1#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572537#iiMn=855572504#bIam=0": "GENDER_MALE;#iiS=872349895#iiH=872349841#iiHw=872349736#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572537#iiMn=855572504#bIam=0",
		// Female defaults.
		"GENDER_FEMALE;#iiS=-1#iiH=-1#iiHw=-1#iiEw=-1#iiMs=-1#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0": "GENDER_FEMALE;#iiS=872349703#iiH=872349769#iiHw=872349736#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0",
		// Complete and unknown strings are untouched.
		"GENDER_FEMALE;#iiS=872349703#iiH=872349769#iiHw=872349736#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0": "GENDER_FEMALE;#iiS=872349703#iiH=872349769#iiHw=872349736#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0",
		"something else": "something else",
	}
	for in, want := range cases {
		if got := repairedCaptainDisplayInfo(in); got != want {
			t.Errorf("repairedCaptainDisplayInfo(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := normalizedCaptainDisplayInfo(""); got != defaultCaptainDisplayInfo || repairedCaptainDisplayInfo(got) != got {
		t.Errorf("the default captain must need no repair: %q", got)
	}
}
