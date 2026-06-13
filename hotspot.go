package main

import (
	"fmt"
	"runtime"
	"strings"
)

// Once two devices have paired over BLE, one hosts a Wi-Fi hotspot (SoftAP) and
// the other joins it; the existing multicast discovery + croc transfer then
// runs over that hotspot with no internet or relay. Programmatic hotspot
// control is per-OS and partly forbidden (macOS), so this file provides:
//   - credential generation (testable, pure)
//   - per-OS command construction (testable, pure)
//   - guided-manual instructions for platforms/cases where automation is
//     unavailable or denied
//
// Actually executing the commands is OS- and permission-dependent and is NOT
// validated headlessly — runHotspotCommand is injectable so the construction
// can be unit-tested without touching the radio.

// hotspotCredentials are the SSID and WPA2 passphrase a host advertises (over
// BLE, or shows on screen for a manual join).
type hotspotCredentials struct {
	SSID string
	PSK  string
}

// generateHotspotCredentials builds a recognisable SSID and a memorable,
// WPA2-valid passphrase (8–63 chars) from the same word lists as device names,
// so a manual typist has an easy time. crypto/rand via pick().
func generateHotspotCredentials() hotspotCredentials {
	// SSID like "krokodyl-otter" — identifiable in any Wi-Fi list.
	ssid := "krokodyl-" + strings.ToLower(pick(nameAnimals))
	// Four words ≈ 20 chars: easily inside WPA2's 8–63 and easy to read aloud.
	psk := strings.ToLower(strings.Join([]string{
		pick(nameAdjectives), pick(nameAnimals), pick(nameAdjectives), pick(nameAnimals),
	}, "-"))
	return hotspotCredentials{SSID: clampSSID(ssid), PSK: clampPSK(psk)}
}

func clampSSID(s string) string {
	if len(s) > maxSSIDLen {
		return s[:maxSSIDLen]
	}
	return s
}

func clampPSK(s string) string {
	if len(s) > maxPSKLen {
		return s[:maxPSKLen]
	}
	for len(s) < minPSKLen { // pathological short word lists only
		s += "0"
	}
	return s
}

// osCommand is a constructed (not yet executed) shell command.
type osCommand struct {
	Name string
	Args []string
}

func (c osCommand) String() string { return c.Name + " " + strings.Join(c.Args, " ") }

// hotspotStartCommands returns the command(s) to bring up a SoftAP for creds on
// the current OS, or (nil, false) when the OS has no scriptable path (macOS →
// guided manual). Pure: builds args, runs nothing.
func hotspotStartCommands(creds hotspotCredentials) (cmds []osCommand, automatable bool) {
	switch runtime.GOOS {
	case "windows":
		// Legacy hosted-network path. Modern Windows prefers the WinRT Mobile
		// Hotspot API; netsh remains the scriptable fallback.
		return []osCommand{
			{Name: "netsh", Args: []string{"wlan", "set", "hostednetwork", "mode=allow",
				"ssid=" + creds.SSID, "key=" + creds.PSK}},
			{Name: "netsh", Args: []string{"wlan", "start", "hostednetwork"}},
		}, true
	case "linux":
		return []osCommand{
			{Name: "nmcli", Args: []string{"device", "wifi", "hotspot",
				"ssid", creds.SSID, "password", creds.PSK}},
		}, true
	default: // darwin and anything else
		return nil, false
	}
}

// hotspotJoinCommands returns the command(s) to join an existing SoftAP, or
// (nil, false) where no reliable scriptable path exists.
func hotspotJoinCommands(creds hotspotCredentials) (cmds []osCommand, automatable bool) {
	switch runtime.GOOS {
	case "linux":
		return []osCommand{
			{Name: "nmcli", Args: []string{"device", "wifi", "connect", creds.SSID,
				"password", creds.PSK}},
		}, true
	case "darwin":
		return []osCommand{
			{Name: "networksetup", Args: []string{"-setairportnetwork", "en0", creds.SSID, creds.PSK}},
		}, true
	case "windows":
		// Windows needs a generated profile XML before `netsh wlan connect`,
		// so a one-liner isn't reliable — fall back to guided manual join.
		return nil, false
	default:
		return nil, false
	}
}

// hotspotManualSteps returns i18n keys for the guided manual instructions
// appropriate to the current OS and role. The frontend renders the localized
// text plus the SSID/PSK. Keeping this as keys (not prose) preserves the
// six-locale contract.
func hotspotManualSteps(role pairingRole) []string {
	// Only these OSes have localized step keys; anything else (an exotic GOOS)
	// falls back to the generic instruction rather than emitting a key with no
	// locale entry (which would render as a raw id).
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
	default:
		return []string{"offline.manual.generic"}
	}
	base := "offline.manual." + runtime.GOOS + "."
	switch role {
	case roleHost:
		return []string{base + "host_1", base + "host_2", base + "host_3"}
	case roleJoin:
		return []string{base + "join_1", base + "join_2"}
	default:
		return []string{"offline.manual.generic"}
	}
}

// describeHotspotPlan summarizes, for logs/diagnostics, how this OS will
// bootstrap the network for a role — automatable or guided-manual.
func describeHotspotPlan(role pairingRole, creds hotspotCredentials) string {
	var cmds []osCommand
	var auto bool
	switch role {
	case roleHost:
		cmds, auto = hotspotStartCommands(creds)
	case roleJoin:
		cmds, auto = hotspotJoinCommands(creds)
	}
	if !auto {
		return fmt.Sprintf("%s/%s: guided manual hotspot (no scriptable path)", runtime.GOOS, role)
	}
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = c.String()
	}
	return fmt.Sprintf("%s/%s: %s", runtime.GOOS, role, strings.Join(parts, " && "))
}
