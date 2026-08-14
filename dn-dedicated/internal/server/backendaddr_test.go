package server

import (
	"strings"
	"testing"
)

// Off unless explicitly asked for, so a normal launch argv does not change.
func TestBackendAddressArgsAreOffByDefault(t *testing.T) {
	t.Setenv("DN_PASS_BACKEND_ADDRS", "")
	if got := backendAddressArgs(); len(got) != 0 {
		t.Errorf("passed %v with the switch unset", got)
	}
}

// And when asked for, they carry the addresses the rest of the stack uses --
// not hardcoded ones that could drift from what mmogbrain listens on.
func TestBackendAddressArgsUseTheConfiguredAddresses(t *testing.T) {
	t.Setenv("DN_PASS_BACKEND_ADDRS", "1")
	t.Setenv("SERVER_IP", "10.0.0.73")
	t.Setenv("GATEWAY_ADDR", ":65443")
	t.Setenv("FIRMAMENT_ADDR", ":48843")

	got := strings.Join(backendAddressArgs(), " ")
	for _, want := range []string{
		"-GatewayAddress=10.0.0.73",
		"-GatewayPort=65443",
		"-YFirmamentAddress=10.0.0.73",
		"-YFirmamentPort=48843",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestPortOf(t *testing.T) {
	for addr, want := range map[string]string{
		":65443":          "65443",
		"10.0.0.73:48843": "48843",
		"48843":           "48843",
		"":                "FB",
	} {
		if got := portOf(addr, "FB"); got != want {
			t.Errorf("portOf(%q) = %q, want %q", addr, got, want)
		}
	}
}
