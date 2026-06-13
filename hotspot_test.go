package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestGenerateHotspotCredentials(t *testing.T) {
	for i := 0; i < 50; i++ {
		c := generateHotspotCredentials()
		if !strings.HasPrefix(c.SSID, "krokodyl-") {
			t.Errorf("SSID should be identifiable: %q", c.SSID)
		}
		if len(c.SSID) > maxSSIDLen {
			t.Errorf("SSID %q exceeds %d", c.SSID, maxSSIDLen)
		}
		if len(c.PSK) < minPSKLen || len(c.PSK) > maxPSKLen {
			t.Errorf("PSK %q length %d outside WPA2 range %d-%d", c.PSK, len(c.PSK), minPSKLen, maxPSKLen)
		}
		// Must encode into a handshake within budget alongside everything else.
		if _, err := encodeHandshake(bleHandshake{DeviceID: "m1", Name: "x", Role: roleHost, SSID: c.SSID, PSK: c.PSK}); err != nil {
			t.Errorf("creds break handshake budget: %v", err)
		}
	}
}

func TestHotspotStartCommandsCarryCreds(t *testing.T) {
	creds := hotspotCredentials{SSID: "krokodyl-otter", PSK: "brave-otter-vivid-lemur"}
	cmds, auto := hotspotStartCommands(creds)

	switch runtime.GOOS {
	case "windows", "linux":
		if !auto || len(cmds) == 0 {
			t.Fatalf("%s should be automatable", runtime.GOOS)
		}
		joined := ""
		for _, c := range cmds {
			joined += c.String() + "\n"
		}
		if !strings.Contains(joined, creds.SSID) || !strings.Contains(joined, creds.PSK) {
			t.Errorf("start commands must include SSID and PSK:\n%s", joined)
		}
	default:
		if auto {
			t.Errorf("%s has no scriptable hotspot path; must be guided manual", runtime.GOOS)
		}
	}
}

func TestHotspotJoinCommandsPerOS(t *testing.T) {
	creds := hotspotCredentials{SSID: "krokodyl-otter", PSK: "brave-otter-vivid-lemur"}
	cmds, auto := hotspotJoinCommands(creds)
	switch runtime.GOOS {
	case "linux", "darwin":
		if !auto || len(cmds) == 0 {
			t.Errorf("%s join should be automatable", runtime.GOOS)
		}
	case "windows":
		if auto {
			t.Error("windows join needs a profile XML; must be guided manual")
		}
	}
}

func TestHotspotManualStepsAreLocaleKeys(t *testing.T) {
	for _, role := range []pairingRole{roleHost, roleJoin, roleUndecided} {
		steps := hotspotManualSteps(role)
		if len(steps) == 0 {
			t.Fatalf("role %q has no manual steps", role)
		}
		for _, k := range steps {
			if !strings.HasPrefix(k, "offline.manual.") {
				t.Errorf("manual step %q is not an i18n key", k)
			}
		}
	}
}

func TestDescribeHotspotPlan(t *testing.T) {
	plan := describeHotspotPlan(roleHost, hotspotCredentials{SSID: "krokodyl-otter", PSK: "pw12345678"})
	if !strings.Contains(plan, runtime.GOOS) {
		t.Errorf("plan should name the OS: %q", plan)
	}
}
