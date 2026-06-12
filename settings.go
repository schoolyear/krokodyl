package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// appSettings holds the small bits of state worth remembering between runs.
type appSettings struct {
	LastDestination string `json:"lastDestination,omitempty"`
	LastPeer        string `json:"lastPeer,omitempty"`
	// MachineID is an opaque, stable per-install identifier (not hostname or
	// MAC) used to recognize "the same machine" across restarts and name
	// changes when repeating a transfer.
	MachineID string `json:"machineId,omitempty"`
	// Pointer so "never set" (default visible) is distinct from "off".
	NearbyVisible *bool `json:"nearbyVisible,omitempty"`
	// Partials tracks preserved partial-transfer directories so abandoned
	// ones can be swept on a later run instead of accumulating on disk.
	Partials []partialRef `json:"partials,omitempty"`
}

type partialRef struct {
	Dir string `json:"dir"`
	At  int64  `json:"at"` // unix seconds when preserved
}

// partialMaxAge bounds how long an abandoned partial is kept before sweep.
const partialMaxAge = 24 * 60 * 60 // seconds

// expiredPartials splits refs into those still within maxAge and those past
// it, given the current unix time. Pure, so it can be tested directly.
func expiredPartials(refs []partialRef, nowUnix, maxAge int64) (keep, expired []partialRef) {
	for _, r := range refs {
		if nowUnix-r.At > maxAge {
			expired = append(expired, r)
		} else {
			keep = append(keep, r)
		}
	}
	return keep, expired
}

func (s appSettings) nearbyVisible() bool {
	return s.NearbyVisible == nil || *s.NearbyVisible
}

func settingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "krokodyl", "settings.json"), nil
}

// loadSettings returns saved settings, or zero settings when none exist or
// the file is unreadable — settings are convenience state, never an error
// the user should see.
func loadSettings(path string) appSettings {
	var s appSettings
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		logrus.WithError(err).Warn("ignoring corrupt settings file")
		return appSettings{}
	}
	return s
}

// ensureMachineID returns the stable per-install machine id, generating and
// persisting one on first use. A persistence failure is non-fatal: the
// returned id is still usable for this run.
func ensureMachineID(path string) string {
	s := loadSettings(path)
	if s.MachineID != "" {
		return s.MachineID
	}
	s.MachineID = uuid.NewString()
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not persist machine id")
	}
	return s.MachineID
}

// recordPartial remembers a preserved partial dir (best-effort).
func recordPartial(path, dir string, nowUnix int64) {
	s := loadSettings(path)
	for _, r := range s.Partials {
		if r.Dir == dir {
			return // already tracked
		}
	}
	s.Partials = append(s.Partials, partialRef{Dir: dir, At: nowUnix})
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not record partial transfer")
	}
}

// forgetPartial drops a partial dir from tracking (after it is resumed,
// completed, or deleted), best-effort.
func forgetPartial(path, dir string) {
	s := loadSettings(path)
	kept := s.Partials[:0]
	for _, r := range s.Partials {
		if r.Dir != dir {
			kept = append(kept, r)
		}
	}
	s.Partials = kept
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not forget partial transfer")
	}
}

// sweepPartials deletes abandoned partial dirs older than partialMaxAge and
// prunes them from settings. Best-effort; called at startup.
func sweepPartials(path string, nowUnix int64) {
	s := loadSettings(path)
	if len(s.Partials) == 0 {
		return
	}
	keep, expired := expiredPartials(s.Partials, nowUnix, partialMaxAge)
	if len(expired) == 0 {
		return
	}
	for _, r := range expired {
		if err := os.RemoveAll(r.Dir); err != nil {
			logrus.WithError(err).Warnf("could not remove stale partial %s", r.Dir)
		}
	}
	s.Partials = keep
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not prune swept partials")
	}
}

func saveSettings(path string, s appSettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
