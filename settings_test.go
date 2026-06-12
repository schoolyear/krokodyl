package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	in := appSettings{LastDestination: `D:\Shared\Incoming`}
	if err := saveSettings(path, in); err != nil {
		t.Fatal(err)
	}

	out := loadSettings(path)
	if out.LastDestination != in.LastDestination {
		t.Errorf("round trip mismatch: %q != %q", out.LastDestination, in.LastDestination)
	}
}

func TestSettingsNearbyVisibleDefaultsTrue(t *testing.T) {
	var s appSettings
	if !s.nearbyVisible() {
		t.Error("unset visibility must default to visible")
	}

	off := false
	s.NearbyVisible = &off
	if s.nearbyVisible() {
		t.Error("explicit false must read as invisible")
	}
}

func TestSettingsVisibilityAndLastPeerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	off := false
	in := appSettings{LastPeer: "LAPTOP-X", NearbyVisible: &off}
	if err := saveSettings(path, in); err != nil {
		t.Fatal(err)
	}

	out := loadSettings(path)
	if out.nearbyVisible() {
		t.Error("visibility=false lost in round trip")
	}
	if out.LastPeer != "LAPTOP-X" {
		t.Errorf("last peer lost: %q", out.LastPeer)
	}
}

func TestEnsureMachineIDIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	first := ensureMachineID(path)
	if first == "" {
		t.Fatal("expected a machine id to be generated")
	}
	second := ensureMachineID(path)
	if first != second {
		t.Errorf("machine id must be stable across calls: %q != %q", first, second)
	}
	if loadSettings(path).MachineID != first {
		t.Error("machine id was not persisted")
	}
}

func TestEnsureMachineIDPreservesOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := saveSettings(path, appSettings{LastDestination: `D:\Drop`}); err != nil {
		t.Fatal(err)
	}

	ensureMachineID(path)
	out := loadSettings(path)
	if out.LastDestination != `D:\Drop` {
		t.Errorf("ensureMachineID clobbered existing settings: %+v", out)
	}
	if out.MachineID == "" {
		t.Error("machine id not added")
	}
}

func TestLoadSettingsMissingFileReturnsZero(t *testing.T) {
	out := loadSettings(filepath.Join(t.TempDir(), "nope.json"))
	if out.LastDestination != "" {
		t.Errorf("expected zero settings, got %+v", out)
	}
}

func TestLoadSettingsCorruptFileReturnsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := loadSettings(path)
	if out.LastDestination != "" {
		t.Errorf("expected zero settings for corrupt file, got %+v", out)
	}
}
