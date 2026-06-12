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
