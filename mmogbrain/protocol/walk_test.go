package protocol

import "testing"

// Array entries come back in order, whatever the value width.
func TestScalarsWalksArrayEntriesInOrder(t *testing.T) {
	var b []byte
	var stack []int
	b = AppendStringField(b, "RT", "YA_ConvertShipXP")
	b, stack = AppendArrayStart(b, stack, "ShipXps")
	for _, e := range [][2]int32{{33489262, 400}, {33489423, 85}} {
		b, stack = AppendUnnamedObjectStart(b, stack)
		b = AppendInt32Field(b, "ShipID", e[0])
		b = AppendStringField(b, "ShipXp", strconvItoa(e[1]))
		b, stack = AppendObjectEnd(b, stack)
	}
	b, _ = AppendObjectEnd(b, stack)
	b = AppendRootEnd(b)
	var got []int64
	for _, s := range Scalars(b) {
		switch {
		case s.Name == "ShipID":
			got = append(got, s.Num)
		case s.Name == "ShipXp" && s.IsStr:
			n := int64(0)
			for _, c := range s.Str {
				n = n*10 + int64(c-'0')
			}
			got = append(got, n)
		}
	}
	want := []int64{33489262, 400, 33489423, 85}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func strconvItoa(v int32) string {
	if v == 0 {
		return "0"
	}
	var d []byte
	for ; v > 0; v /= 10 {
		d = append([]byte{byte('0' + v%10)}, d...)
	}
	return string(d)
}
