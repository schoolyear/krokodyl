package main

import (
	"strings"
	"testing"
)

func TestHandshakeRoundTrip(t *testing.T) {
	in := bleHandshake{
		DeviceID: "machine-1", Name: "Brave Otter", Role: roleHost,
		SSID: "krokodyl-7f3a", PSK: "lemur-otter-vivid", ControlPort: 53201,
		Fingerprint: strings.Repeat("ab", 32), Code: "0570-infant-chief-able",
	}
	data, err := encodeHandshake(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxHandshakeBytes {
		t.Fatalf("encoded %d bytes exceeds budget", len(data))
	}
	out, err := decodeHandshake(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != handshakeVersion {
		t.Errorf("version not stamped: %d", out.Version)
	}
	if out.DeviceID != in.DeviceID || out.SSID != in.SSID || out.PSK != in.PSK ||
		out.ControlPort != in.ControlPort || out.Fingerprint != in.Fingerprint || out.Code != in.Code {
		t.Errorf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestHandshakeFitsBudgetAtMaxFields(t *testing.T) {
	// Worst case: every field at its cap must still fit the sanity budget
	// (the radio layer chunks to MTU below this).
	h := bleHandshake{
		DeviceID:    strings.Repeat("d", maxMachineIDLen),
		Name:        strings.Repeat("n", maxPeerNameLen),
		Role:        roleHost,
		SSID:        strings.Repeat("s", maxSSIDLen),
		PSK:         strings.Repeat("p", maxPSKLen),
		ControlPort: 65535,
		Fingerprint: strings.Repeat("ab", 32),
		Code:        strings.Repeat("c", maxCodeLen),
	}
	data, err := encodeHandshake(h)
	if err != nil {
		t.Fatalf("max-field handshake must still encode: %v", err)
	}
	if len(data) > maxHandshakeBytes {
		t.Errorf("max-field handshake is %d bytes — exceeds %d budget", len(data), maxHandshakeBytes)
	}
}

func TestDecodeHandshakeRejects(t *testing.T) {
	good := bleHandshake{DeviceID: "m1", Name: "x", Role: roleJoin}
	mutate := func(fn func(*bleHandshake)) []byte {
		h := good
		fn(&h)
		h.Version = handshakeVersion
		data, _ := encodeHandshake(h)
		return data
	}
	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"oversize", make([]byte, maxHandshakeBytes+1)},
		{"not json", []byte("{nope")},
		{"wrong version", []byte(`{"v":99,"id":"m1"}`)},
		{"no device id", mutate(func(h *bleHandshake) { h.DeviceID = "" })},
		{"bad role", mutate(func(h *bleHandshake) { h.Role = "boss" })},
		{"bad port", mutate(func(h *bleHandshake) { h.ControlPort = 70000 })},
		{"short fingerprint", mutate(func(h *bleHandshake) { h.Fingerprint = "abcd" })},
		{"non-hex fingerprint", mutate(func(h *bleHandshake) { h.Fingerprint = strings.Repeat("zz", 32) })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeHandshake(tt.payload); err == nil {
				t.Errorf("expected rejection for %s", tt.name)
			}
		})
	}
}

func TestDecodeHandshakeSanitizesName(t *testing.T) {
	h := bleHandshake{DeviceID: "m1", Name: "evil\r\n\x1b[31mX‮spoof", Role: roleHost}
	data, _ := encodeHandshake(h)
	out, err := decodeHandshake(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.Name, "\r\n\x1b") || strings.ContainsRune(out.Name, '‮') {
		t.Errorf("name not sanitized: %q", out.Name)
	}
}

func TestHandshakeToIdentityFeedsRegistry(t *testing.T) {
	h := bleHandshake{
		DeviceID: "m1", Name: "Brave Otter", Role: roleHost,
		ControlPort: 4321, Fingerprint: strings.Repeat("ab", 32),
	}
	id := h.toIdentity()
	if id.ID != "m1" || id.Port != 4321 || id.MachineID != "m1" || id.Fingerprint != h.Fingerprint {
		t.Errorf("toIdentity dropped fields: %+v", id)
	}
}

func TestResolveRoleExactlyOneHost(t *testing.T) {
	tests := []struct {
		name               string
		selfID, peerID     string
		selfPref, peerPref pairingRole
		wantSelf           pairingRole
	}{
		{"both undecided, self lower id hosts", "a", "b", roleUndecided, roleUndecided, roleHost},
		{"both undecided, self higher id joins", "b", "a", roleUndecided, roleUndecided, roleJoin},
		{"self wants host", "b", "a", roleHost, roleUndecided, roleHost},
		{"peer wants host", "a", "b", roleUndecided, roleHost, roleJoin},
		{"both want host, lower id wins", "a", "b", roleHost, roleHost, roleHost},
		{"both want host, higher id yields", "b", "a", roleHost, roleHost, roleJoin},
		{"both want join, lower id forced to host", "a", "b", roleJoin, roleJoin, roleHost},
		{"self join peer undecided", "a", "b", roleJoin, roleUndecided, roleJoin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRole(tt.selfID, tt.peerID, tt.selfPref, tt.peerPref)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantSelf {
				t.Errorf("resolveRole = %q, want %q", got, tt.wantSelf)
			}
		})
	}
}

func TestResolveRoleSymmetricExactlyOneHost(t *testing.T) {
	// Whatever the inputs, the two devices must independently agree on exactly
	// one host — never both-host, never both-join.
	prefs := []pairingRole{roleUndecided, roleHost, roleJoin}
	for _, sp := range prefs {
		for _, pp := range prefs {
			selfRole, err := resolveRole("alpha", "bravo", sp, pp)
			if err != nil {
				t.Fatal(err)
			}
			// The peer computes with mirrored arguments.
			peerRole, err := resolveRole("bravo", "alpha", pp, sp)
			if err != nil {
				t.Fatal(err)
			}
			if selfRole == peerRole {
				t.Errorf("prefs self=%q peer=%q → both got %q (must differ)", sp, pp, selfRole)
			}
		}
	}
}

func TestResolveRoleErrors(t *testing.T) {
	if _, err := resolveRole("", "b", roleUndecided, roleUndecided); err == nil {
		t.Error("missing self id must error")
	}
	if _, err := resolveRole("a", "a", roleUndecided, roleUndecided); err == nil {
		t.Error("identical ids must error")
	}
}

func TestOfflineSessionHappyPath(t *testing.T) {
	s := newOfflineSession()
	steps := []offlineState{
		offlineDiscovering, offlinePaired, offlineBootstrapping, offlineNetworkReady, offlineHandoff,
	}
	for _, want := range steps {
		if err := s.advance(want); err != nil {
			t.Fatalf("advance to %s failed: %v", want, err)
		}
		if st, _, _ := s.snapshot(); st != want {
			t.Fatalf("state = %s, want %s", st, want)
		}
	}
}

func TestOfflineSessionRejectsIllegalJumps(t *testing.T) {
	s := newOfflineSession()
	// idle → network_ready must not be allowed (skips pairing/bootstrap).
	if err := s.advance(offlineNetworkReady); err == nil {
		t.Error("illegal skip should be rejected")
	}
	if st, _, _ := s.snapshot(); st != offlineIdle {
		t.Errorf("rejected transition must not change state, got %s", st)
	}
}

func TestOfflineSessionFailAndTerminal(t *testing.T) {
	s := newOfflineSession()
	s.advance(offlineDiscovering)
	s.fail("ble unavailable")
	st, _, msg := s.snapshot()
	if st != offlineFailed || msg != "ble unavailable" {
		t.Errorf("fail not recorded: state=%s err=%q", st, msg)
	}
	// cancel must not resurrect a terminal session.
	s.cancel()
	if st, _, _ := s.snapshot(); st != offlineFailed {
		t.Errorf("cancel must leave terminal state, got %s", st)
	}
	// no transitions out of failed.
	if err := s.advance(offlineDiscovering); err == nil {
		t.Error("must not advance out of failed")
	}
}

func TestOfflineSessionCancelResets(t *testing.T) {
	s := newOfflineSession()
	s.advance(offlineDiscovering)
	s.advance(offlinePaired)
	s.setRole(roleHost)
	s.cancel()
	st, role, _ := s.snapshot()
	if st != offlineIdle || role != roleUndecided {
		t.Errorf("cancel should reset to idle/undecided, got %s/%s", st, role)
	}
}
