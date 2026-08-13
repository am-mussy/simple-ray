package domain

import "testing"

func TestValidateStateRejectsUnprivilegedPanelPort(t *testing.T) {
	state := validDomainState()
	state.PanelPort = 2053
	if err := ValidateState(state); err == nil {
		t.Fatal("unprivileged panel port was accepted")
	}
}

func TestValidateStateRejectsUnsafeRoutingFields(t *testing.T) {
	base := validDomainState()
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{name: "unsupported architecture", mutate: func(s *State) { s.Architecture = "mips64" }},
		{name: "invalid public address", mutate: func(s *State) { s.PublicAddress = "not-an-ip" }},
		{name: "control in public address", mutate: func(s *State) { s.PublicAddress = "203.0.113.1\x1b]0;owned\x07" }},
		{name: "traversing panel path", mutate: func(s *State) { s.PanelBasePath = "/../admin" }},
		{name: "invalid Reality target", mutate: func(s *State) { s.RealityTarget = "missing-port.example" }},
		{name: "control in Reality SNI", mutate: func(s *State) { s.RealitySNI = "example.com\nforged" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := base
			test.mutate(&state)
			if err := ValidateState(state); err == nil {
				t.Fatalf("unsafe state was accepted: %#v", state)
			}
		})
	}
}

func validDomainState() State {
	return State{
		SchemaVersion: SchemaVersion,
		VPNCTLVersion: ProductVersion,
		XUIVersion:    XUIVersion,
		Architecture:  "amd64",
		PublicAddress: "203.0.113.1",
		InboundID:     1,
		InboundRemark: "vpnctl-vless-reality",
		ListenPort:    443,
		PanelPort:     853,
		PanelBasePath: "/adminpath",
		PanelListen:   "127.0.0.1",
		RealityTarget: "example.com:443",
		RealitySNI:    "example.com",
	}
}
