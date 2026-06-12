package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// maxHistoryEntries caps the persisted transfer history so the file and the
// UI list stay small.
const maxHistoryEntries = 50

func historyPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "krokodyl", "history.json"), nil
}

// loadHistory returns previously persisted transfers, oldest first. Missing
// or corrupt files yield an empty history — it is convenience state.
func loadHistory(path string) []FileTransfer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var transfers []FileTransfer
	if err := json.Unmarshal(data, &transfers); err != nil {
		logrus.WithError(err).Warn("ignoring corrupt history file")
		return nil
	}
	return transfers
}

// saveHistory persists transfers (newest first, as snapshot() returns them),
// keeping only terminal entries up to the cap.
func saveHistory(path string, transfers []FileTransfer) error {
	terminal := make([]FileTransfer, 0, len(transfers))
	for _, t := range transfers {
		if t.Status.isTerminal() {
			// Codes are single-use and grant access to nothing after the
			// transfer ends, but there is no reason to keep them around.
			t.Code = ""
			t.Speed = 0
			terminal = append(terminal, t)
		}
	}
	if len(terminal) > maxHistoryEntries {
		terminal = terminal[:maxHistoryEntries]
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(terminal)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// clearHistory removes the persisted history. A missing file is already a
// clear state, so its absence is not an error.
func clearHistory(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
