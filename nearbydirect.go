package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Nearby-Direct = offline, no-infrastructure transfer. When there is no shared
// network, Bluetooth Low Energy finds the other device and carries a tiny
// handshake; one device then hosts a Wi-Fi hotspot whose credentials ride that
// handshake, the other joins, and the EXISTING multicast discovery + croc
// transfer runs over it. Bytes never cross Bluetooth (croc is TCP/IP; BLE bulk
// is far too slow for the big-file promise) — BLE only bootstraps the network.
//
// This file is the hardware-free core: the handshake wire format, role
// negotiation, and the session state machine. The BLE radio (nearbyble.go) and
// the hotspot bring-up (hotspot_*.go) drive it but are validated separately on
// real hardware.

const (
	// handshakeVersion guards against two builds with incompatible payloads
	// silently mis-parsing each other.
	handshakeVersion = 1
	// Sanity cap on the handshake blob, and the bound a malformed/hostile peer
	// is held to. The radio layer must deliver the whole blob within the
	// negotiated BLE ATT MTU (the gated tinygo driver requires an MTU large
	// enough for one write; MTU-fragmenting reassembly is a documented
	// hardware-validation TODO, not yet implemented).
	maxHandshakeBytes = 1024
	maxSSIDLen        = 32 // 802.11 SSID
	maxPSKLen         = 63 // WPA2-PSK max (8 min enforced on host generation)
	minPSKLen         = 8
)

// pairingRole is which side of an offline pairing this device plays.
type pairingRole string

const (
	roleUndecided pairingRole = ""     // no preference yet
	roleHost      pairingRole = "host" // brings up the hotspot
	roleJoin      pairingRole = "join" // joins the host's hotspot
)

func (r pairingRole) valid() bool {
	return r == roleUndecided || r == roleHost || r == roleJoin
}

// hasControlChars reports whether s contains any ASCII control character
// (C0 range or DEL) — rejected in fields that flow into OS commands.
func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// bleHandshake is exchanged over the BLE control characteristic. Short JSON
// keys keep it inside one GATT write. Host-only fields (SSID/PSK/Code) are
// empty until the host has its hotspot up.
type bleHandshake struct {
	Version     int         `json:"v"`
	DeviceID    string      `json:"id"`           // stable per-install id
	Name        string      `json:"n"`            // friendly device name
	Role        pairingRole `json:"r"`            // stated preference
	SSID        string      `json:"s,omitempty"`  // host hotspot SSID
	PSK         string      `json:"p,omitempty"`  // host hotspot password
	ControlPort int         `json:"cp,omitempty"` // nearby TLS control port
	Fingerprint string      `json:"fp,omitempty"` // control-channel cert pin (hex)
	Code        string      `json:"c,omitempty"`  // croc transfer code
}

// encodeHandshake serializes and enforces the single-write size budget.
func encodeHandshake(h bleHandshake) ([]byte, error) {
	if h.Version == 0 {
		h.Version = handshakeVersion
	}
	data, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("encode handshake: %w", err)
	}
	if len(data) > maxHandshakeBytes {
		return nil, fmt.Errorf("handshake %d bytes exceeds %d-byte budget", len(data), maxHandshakeBytes)
	}
	return data, nil
}

// decodeHandshake parses and validates an untrusted handshake from the radio.
// Display strings are sanitized exactly like multicast peer names (a hostile
// device could otherwise forge log lines or spoof the pairing UI).
func decodeHandshake(payload []byte) (bleHandshake, error) {
	var h bleHandshake
	if len(payload) == 0 || len(payload) > maxHandshakeBytes {
		return h, fmt.Errorf("handshake size %d out of bounds", len(payload))
	}
	if err := json.Unmarshal(payload, &h); err != nil {
		return h, fmt.Errorf("malformed handshake: %w", err)
	}
	if h.Version != handshakeVersion {
		return h, fmt.Errorf("unsupported handshake version %d (want %d)", h.Version, handshakeVersion)
	}
	if h.DeviceID == "" || len(h.DeviceID) > maxMachineIDLen {
		return h, fmt.Errorf("handshake has invalid device id")
	}
	if !h.Role.valid() {
		return h, fmt.Errorf("handshake has invalid role %q", h.Role)
	}
	if len(h.SSID) > maxSSIDLen {
		return h, fmt.Errorf("handshake SSID too long")
	}
	// SSID/PSK are untrusted radio input that flow into OS command arguments;
	// reject control characters so nothing odd reaches the hotspot tooling.
	if h.SSID != "" && hasControlChars(h.SSID) {
		return h, fmt.Errorf("handshake SSID has control characters")
	}
	if h.PSK != "" {
		if len(h.PSK) < minPSKLen || len(h.PSK) > maxPSKLen {
			return h, fmt.Errorf("handshake PSK length %d outside WPA2 range %d-%d", len(h.PSK), minPSKLen, maxPSKLen)
		}
		if hasControlChars(h.PSK) {
			return h, fmt.Errorf("handshake PSK has control characters")
		}
	}
	if h.ControlPort < 0 || h.ControlPort > 65535 {
		return h, fmt.Errorf("handshake has invalid control port %d", h.ControlPort)
	}
	if h.Fingerprint != "" {
		if len(h.Fingerprint) != 64 {
			return h, fmt.Errorf("handshake fingerprint must be 64 hex chars")
		}
		if _, err := hex.DecodeString(h.Fingerprint); err != nil {
			return h, fmt.Errorf("handshake fingerprint is not hex")
		}
	}
	if len(h.Code) > maxCodeLen {
		return h, fmt.Errorf("handshake code too long")
	}
	if len(h.Name) > maxPeerNameLen {
		// Slice by bytes then repair: a multi-byte rune split at the boundary
		// is dropped by ToValidUTF8 so the clamped name is never invalid UTF-8
		// (same approach as discovery.go's multicast name handling).
		h.Name = strings.ToValidUTF8(h.Name[:maxPeerNameLen], "")
	}
	h.Name = sanitizeDisplayName(h.Name)
	if h.Name == "" {
		h.Name = "unknown device"
	}
	return h, nil
}

// toIdentity converts a host's handshake into the discoveryIdentity the shared
// peerRegistry expects — this is how a Bluetooth-found peer becomes
// indistinguishable from a multicast-found one downstream.
func (h bleHandshake) toIdentity() discoveryIdentity {
	return discoveryIdentity{
		ID:          h.DeviceID,
		Name:        h.Name,
		Port:        h.ControlPort,
		Fingerprint: h.Fingerprint,
		MachineID:   h.DeviceID,
	}
}

// rolePreferenceScore ranks how strongly a side wants to host: an explicit
// host preference beats undecided, which beats an explicit join.
func rolePreferenceScore(p pairingRole) int {
	switch p {
	case roleHost:
		return 2
	case roleJoin:
		return 0
	default:
		return 1
	}
}

// resolveRole decides THIS device's role from both sides' stated preferences,
// guaranteeing exactly one host. The side wanting to host more strongly wins;
// ties break deterministically on device id (lower id hosts) so both devices
// independently compute the same assignment without another round trip.
func resolveRole(selfID, peerID string, selfPref, peerPref pairingRole) (pairingRole, error) {
	if selfID == "" || peerID == "" {
		return roleUndecided, fmt.Errorf("resolveRole needs both device ids")
	}
	if selfID == peerID {
		return roleUndecided, fmt.Errorf("resolveRole: identical device ids %q", selfID)
	}
	selfScore := rolePreferenceScore(selfPref)
	peerScore := rolePreferenceScore(peerPref)

	selfHosts := false
	switch {
	case selfScore > peerScore:
		selfHosts = true
	case selfScore < peerScore:
		selfHosts = false
	default:
		selfHosts = selfID < peerID // deterministic tiebreak
	}
	if selfHosts {
		return roleHost, nil
	}
	return roleJoin, nil
}

// offlineState is the Nearby-Direct session lifecycle.
type offlineState string

const (
	offlineIdle          offlineState = "idle"
	offlineDiscovering   offlineState = "discovering"   // BLE advertise/scan
	offlinePaired        offlineState = "paired"        // handshake exchanged, role known
	offlineBootstrapping offlineState = "bootstrapping" // host raising AP / join connecting
	offlineNetworkReady  offlineState = "network_ready" // both on the hotspot
	offlineHandoff       offlineState = "handoff"       // existing nearby+croc takes over
	offlineFailed        offlineState = "failed"
)

// offlineTransitions is the allowed state graph. cancel() can return any
// non-terminal state to idle and is handled outside this table.
var offlineTransitions = map[offlineState][]offlineState{
	offlineIdle:          {offlineDiscovering},
	offlineDiscovering:   {offlinePaired, offlineFailed},
	offlinePaired:        {offlineBootstrapping, offlineFailed},
	offlineBootstrapping: {offlineNetworkReady, offlineFailed},
	offlineNetworkReady:  {offlineHandoff, offlineFailed},
	offlineHandoff:       {offlineFailed},
	offlineFailed:        {},
}

func (s offlineState) terminal() bool {
	return s == offlineFailed || s == offlineHandoff
}

// offlineSession tracks one Nearby-Direct attempt. Pure state — the radio and
// hotspot layers call advance()/cancel() as real events happen; illegal jumps
// are rejected so a buggy driver can't skip consent or bootstrap steps.
type offlineSession struct {
	mu    sync.Mutex
	state offlineState
	role  pairingRole
	err   string
}

func newOfflineSession() *offlineSession {
	return &offlineSession{state: offlineIdle, role: roleUndecided}
}

func (o *offlineSession) snapshot() (offlineState, pairingRole, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state, o.role, o.err
}

// advance moves to next if the transition is allowed, else returns an error
// and leaves the state unchanged.
func (o *offlineSession) advance(next offlineState) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, allowed := range offlineTransitions[o.state] {
		if allowed == next {
			o.state = next
			return nil
		}
	}
	return fmt.Errorf("illegal offline transition %s -> %s", o.state, next)
}

// setRole records the negotiated role; only meaningful before bootstrapping.
func (o *offlineSession) setRole(r pairingRole) {
	o.mu.Lock()
	o.role = r
	o.mu.Unlock()
}

// fail records an error and moves to the terminal failed state from anywhere.
func (o *offlineSession) fail(reason string) {
	o.mu.Lock()
	o.state = offlineFailed
	o.err = reason
	o.mu.Unlock()
}

// cancel returns a non-terminal session to idle (user backed out). Terminal
// states are left untouched.
func (o *offlineSession) cancel() {
	o.mu.Lock()
	if !o.state.terminal() {
		o.state = offlineIdle
		o.role = roleUndecided
		o.err = ""
	}
	o.mu.Unlock()
}

// OfflineGuidance is what the Nearby-Direct UI needs when there is no shared
// network: whether automatic Bluetooth pairing is compiled/usable on this
// build, plus freshly generated hotspot credentials and per-OS manual steps
// (as i18n keys) for the guided fallback.
type OfflineGuidance struct {
	BluetoothAvailable bool     `json:"bluetoothAvailable"`
	SSID               string   `json:"ssid"`
	PSK                string   `json:"psk"`
	HostSteps          []string `json:"hostSteps"`
	JoinSteps          []string `json:"joinSteps"`
}

// GetOfflineGuidance powers the "no network?" flow. In shipped builds
// BluetoothAvailable is false, so the UI shows the guided manual hotspot path;
// with -tags krokodyl_ble on capable hardware it reports true and the
// automatic BLE pairing path is offered.
func (a *App) GetOfflineGuidance() OfflineGuidance {
	creds := generateHotspotCredentials()
	bleOK := a.ble != nil && a.ble.available()
	return OfflineGuidance{
		BluetoothAvailable: bleOK,
		SSID:               creds.SSID,
		PSK:                creds.PSK,
		HostSteps:          hotspotManualSteps(roleHost),
		JoinSteps:          hotspotManualSteps(roleJoin),
	}
}
