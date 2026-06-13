package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// KDE Connect speaks newline-delimited JSON "packets" over a TLS socket. This
// file is the hardware-free protocol core (packet + identity + share-request
// codec) so it compiles and is unit-tested in the normal build; the live
// network/pairing/payload adapter is in kdeconnect.go behind a build tag.
//
// Protocol reference: https://valent.andyholmes.ca/documentation/protocol.html

const (
	kcProtocolVersion = 7
	kcDefaultPort     = 1716
	kcPacketIdentity  = "kdeconnect.identity"
	kcPacketPair      = "kdeconnect.pair"
	kcPacketShareReq  = "kdeconnect.share.request"
	// Cap a single packet line so a hostile peer can't force unbounded
	// buffering before we even parse it.
	kcMaxPacketBytes = 64 * 1024
)

// kcPacket is the envelope: id (timestamp), type, and a type-specific body.
type kcPacket struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

type kcIdentity struct {
	DeviceID             string   `json:"deviceId"`
	DeviceName           string   `json:"deviceName"`
	DeviceType           string   `json:"deviceType"`
	ProtocolVersion      int      `json:"protocolVersion"`
	IncomingCapabilities []string `json:"incomingCapabilities"`
	OutgoingCapabilities []string `json:"outgoingCapabilities"`
	TCPPort              int      `json:"tcpPort,omitempty"`
}

// kcShareRequest is the body of a share.request. The file bytes arrive
// separately on the payloadTransferInfo port (size = payloadSize).
type kcShareRequest struct {
	Filename    string `json:"filename"`
	PayloadSize int64  `json:"payloadSize"`
	PayloadInfo struct {
		Port int `json:"port"`
	} `json:"payloadTransferInfo"`
}

// ourKCIdentity builds krokodyl's identity advertising that we receive shares.
func ourKCIdentity(deviceID, name string, tcpPort int) kcIdentity {
	return kcIdentity{
		DeviceID:             deviceID,
		DeviceName:           name,
		DeviceType:           "desktop",
		ProtocolVersion:      kcProtocolVersion,
		IncomingCapabilities: []string{kcPacketShareReq},
		OutgoingCapabilities: []string{},
		TCPPort:              tcpPort,
	}
}

// encodeKCPacket marshals a typed packet as one newline-terminated line. The
// id is a millisecond timestamp (KDE Connect uses it for de-dup, so it must
// not be a constant). A package var so tests can pin it.
var kcNow = func() int64 { return time.Now().UnixMilli() }

func encodeKCPacket(packetType string, body interface{}) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: encode body: %w", err)
	}
	data, err := json.Marshal(kcPacket{ID: kcNow(), Type: packetType, Body: raw})
	if err != nil {
		return nil, fmt.Errorf("kdeconnect: encode packet: %w", err)
	}
	return append(data, '\n'), nil
}

// decodeKCPacket parses one packet line (size-capped, must carry a type).
func decodeKCPacket(line []byte) (kcPacket, error) {
	var p kcPacket
	if len(line) == 0 || len(line) >= kcMaxPacketBytes {
		return p, fmt.Errorf("kdeconnect: packet size %d out of bounds", len(line))
	}
	if err := json.Unmarshal(line, &p); err != nil {
		return p, fmt.Errorf("kdeconnect: malformed packet: %w", err)
	}
	if p.Type == "" {
		return p, fmt.Errorf("kdeconnect: packet missing type")
	}
	return p, nil
}

// parseKCIdentity / parseKCShareRequest extract typed bodies.
func parseKCIdentity(p kcPacket) (kcIdentity, error) {
	var id kcIdentity
	if p.Type != kcPacketIdentity {
		return id, fmt.Errorf("kdeconnect: not an identity packet: %s", p.Type)
	}
	if err := json.Unmarshal(p.Body, &id); err != nil {
		return id, fmt.Errorf("kdeconnect: bad identity body: %w", err)
	}
	if id.DeviceID == "" {
		return id, fmt.Errorf("kdeconnect: identity missing deviceId")
	}
	return id, nil
}

func parseKCShareRequest(p kcPacket) (kcShareRequest, error) {
	var s kcShareRequest
	if p.Type != kcPacketShareReq {
		return s, fmt.Errorf("kdeconnect: not a share request: %s", p.Type)
	}
	if err := json.Unmarshal(p.Body, &s); err != nil {
		return s, fmt.Errorf("kdeconnect: bad share body: %w", err)
	}
	if s.Filename == "" || s.PayloadInfo.Port < 1 || s.PayloadInfo.Port > 65535 || s.PayloadSize <= 0 {
		return s, fmt.Errorf("kdeconnect: share request missing filename/payload port/size")
	}
	return s, nil
}
