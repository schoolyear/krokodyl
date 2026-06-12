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

func TestExpiredPartials(t *testing.T) {
	now := int64(1_000_000)
	refs := []partialRef{
		{Dir: "fresh", At: now - 10},
		{Dir: "old", At: now - partialMaxAge - 10},
		{Dir: "edge", At: now - partialMaxAge + 5}, // just inside
	}
	keep, expired := expiredPartials(refs, now, partialMaxAge)
	if len(keep) != 2 || len(expired) != 1 {
		t.Fatalf("expected 2 keep / 1 expired, got %d / %d", len(keep), len(expired))
	}
	if expired[0].Dir != "old" {
		t.Errorf("wrong entry expired: %+v", expired)
	}
}

func TestRecordAndForgetPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	recordPartial(path, `D:\dl\.krokodyl-partial-abc`, 1000)
	recordPartial(path, `D:\dl\.krokodyl-partial-abc`, 2000) // dedupe
	recordPartial(path, `D:\dl\.krokodyl-partial-def`, 1000)
	if got := loadSettings(path).Partials; len(got) != 2 {
		t.Fatalf("expected 2 tracked partials (deduped), got %d", len(got))
	}

	forgetPartial(path, `D:\dl\.krokodyl-partial-abc`)
	got := loadSettings(path).Partials
	if len(got) != 1 || got[0].Dir != `D:\dl\.krokodyl-partial-def` {
		t.Errorf("forget left wrong state: %+v", got)
	}
}

func TestSweepPartialsRemovesStaleDirs(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "settings.json")

	stale := filepath.Join(base, ".krokodyl-partial-stale")
	fresh := filepath.Join(base, ".krokodyl-partial-fresh")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}

	now := int64(2_000_000)
	recordPartial(path, stale, now-partialMaxAge-100)
	recordPartial(path, fresh, now-10)

	sweepPartials(path, now)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale partial dir should have been removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh partial dir should remain")
	}
	if got := loadSettings(path).Partials; len(got) != 1 || got[0].Dir != fresh {
		t.Errorf("sweep left wrong tracking: %+v", got)
	}
}

func TestSweepPartialsRefusesNonPartialPath(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "settings.json")

	// A tampered settings entry pointing at a non-partial directory must
	// never be deleted, even when expired.
	precious := filepath.Join(base, "important-data")
	if err := os.MkdirAll(precious, 0o755); err != nil {
		t.Fatal(err)
	}

	now := int64(2_000_000)
	recordPartial(path, precious, now-partialMaxAge-100)

	sweepPartials(path, now)

	if _, err := os.Stat(precious); err != nil {
		t.Error("sweep must not delete a path that is not a krokodyl partial dir")
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
