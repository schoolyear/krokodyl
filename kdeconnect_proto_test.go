package main

import "testing"

func TestKCPacketRoundTrip(t *testing.T) {
	body := ourKCIdentity("dev-1", "Brave Otter", kcDefaultPort)
	line, err := encodeKCPacket(kcPacketIdentity, body)
	if err != nil {
		t.Fatal(err)
	}
	if line[len(line)-1] != '\n' {
		t.Error("packet must be newline-terminated")
	}
	p, err := decodeKCPacket(line)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != kcPacketIdentity {
		t.Errorf("type = %q", p.Type)
	}
	id, err := parseKCIdentity(p)
	if err != nil {
		t.Fatal(err)
	}
	if id.DeviceID != "dev-1" || id.TCPPort != kcDefaultPort || id.ProtocolVersion != kcProtocolVersion {
		t.Errorf("identity round-trip wrong: %+v", id)
	}
	// We must advertise that we receive shares, or no sender will offer.
	found := false
	for _, c := range id.IncomingCapabilities {
		if c == kcPacketShareReq {
			found = true
		}
	}
	if !found {
		t.Error("identity must advertise the share.request incoming capability")
	}
}

func TestDecodeKCPacketRejects(t *testing.T) {
	tests := []struct {
		name string
		line []byte
	}{
		{"empty", []byte{}},
		{"oversize", make([]byte, kcMaxPacketBytes+1)},
		{"not json", []byte("{nope")},
		{"no type", []byte(`{"id":1,"body":{}}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeKCPacket(tt.line); err == nil {
				t.Errorf("expected rejection for %s", tt.name)
			}
		})
	}
}

func TestParseKCShareRequest(t *testing.T) {
	line, _ := encodeKCPacket(kcPacketShareReq, kcShareRequest{
		Filename:    "report.pdf",
		PayloadSize: 1024,
		PayloadInfo: struct {
			Port int `json:"port"`
		}{Port: 1739},
	})
	p, _ := decodeKCPacket(line)
	s, err := parseKCShareRequest(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Filename != "report.pdf" || s.PayloadInfo.Port != 1739 || s.PayloadSize != 1024 {
		t.Errorf("share request parse wrong: %+v", s)
	}
}

func TestParseKCShareRequestRejectsBadPayload(t *testing.T) {
	// Missing payload port must be rejected (we'd have nowhere to fetch bytes).
	line, _ := encodeKCPacket(kcPacketShareReq, kcShareRequest{Filename: "x"})
	p, _ := decodeKCPacket(line)
	if _, err := parseKCShareRequest(p); err == nil {
		t.Error("share request without payload port must be rejected")
	}
	// Wrong-type packet must be rejected.
	idLine, _ := encodeKCPacket(kcPacketIdentity, ourKCIdentity("d", "n", 1716))
	idp, _ := decodeKCPacket(idLine)
	if _, err := parseKCShareRequest(idp); err == nil {
		t.Error("identity packet must not parse as a share request")
	}
}
