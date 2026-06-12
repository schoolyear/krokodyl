package main

import (
	"path/filepath"
	"testing"
)

func TestHistoryRoundTripKeepsTerminalOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	in := []FileTransfer{
		{ID: "send-1", Name: "done.txt", Status: FileTransferStatusCompleted, Code: "1234-a-b-c"},
		{ID: "send-2", Name: "active.txt", Status: FileTransferStatusSending},
		{ID: "receive-1", Name: "failed.txt", Status: FileTransferStatusError, Error: "relay unreachable"},
		{ID: "receive-2", Name: "stopped.txt", Status: FileTransferStatusCancelled},
	}
	if err := saveHistory(path, in); err != nil {
		t.Fatal(err)
	}

	out := loadHistory(path)
	if len(out) != 3 {
		t.Fatalf("expected 3 terminal transfers, got %d", len(out))
	}
	for _, tr := range out {
		if !tr.Status.isTerminal() {
			t.Errorf("non-terminal transfer persisted: %+v", tr)
		}
		if tr.Code != "" {
			t.Errorf("code should be stripped from history: %+v", tr)
		}
	}
	if out[1].Error != "relay unreachable" {
		t.Errorf("error message lost in round trip: %+v", out[1])
	}
}

func TestHistoryKeepsResendData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	in := []FileTransfer{{
		ID:         "send-1",
		Name:       "build.zip",
		Status:     FileTransferStatusCompleted,
		Peer:       "LAPTOP-X",
		Paths:      []string{`C:\out\build.zip`},
		Resendable: true,
	}}
	if err := saveHistory(path, in); err != nil {
		t.Fatal(err)
	}

	out := loadHistory(path)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	if !out[0].Resendable || len(out[0].Paths) != 1 || out[0].Paths[0] != `C:\out\build.zip` {
		t.Errorf("resend data lost in round trip: %+v", out[0])
	}
	if out[0].Peer != "LAPTOP-X" {
		t.Errorf("peer lost in round trip: %+v", out[0])
	}
}

func TestSaveHistoryCapsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	var in []FileTransfer
	for i := 0; i < maxHistoryEntries+20; i++ {
		in = append(in, FileTransfer{ID: string(rune('a' + i%26)), Status: FileTransferStatusCompleted})
	}
	if err := saveHistory(path, in); err != nil {
		t.Fatal(err)
	}

	if out := loadHistory(path); len(out) != maxHistoryEntries {
		t.Errorf("expected cap of %d entries, got %d", maxHistoryEntries, len(out))
	}
}

func TestClearHistoryRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := saveHistory(path, []FileTransfer{{ID: "x", Status: FileTransferStatusCompleted}}); err != nil {
		t.Fatal(err)
	}

	if err := clearHistory(path); err != nil {
		t.Fatal(err)
	}
	if out := loadHistory(path); out != nil {
		t.Errorf("expected empty history after clear, got %v", out)
	}

	// Clearing an already-absent file is not an error.
	if err := clearHistory(path); err != nil {
		t.Errorf("clearing missing history should be a no-op, got %v", err)
	}
}

func TestLoadHistoryMissingFile(t *testing.T) {
	if out := loadHistory(filepath.Join(t.TempDir(), "nope.json")); out != nil {
		t.Errorf("expected nil for missing file, got %v", out)
	}
}
