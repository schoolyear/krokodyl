package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSendFile_Integration tests SendFile without waiting for goroutine completion
func TestSendFile_Integration(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	
	app := &App{}
	app.startup(context.Background())
	
	// Create a temporary file for testing
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	content := "Hello, World!"
	
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	
	// Test SendFile - expect it to set up the transfer correctly
	transferID, err := app.SendFile(testFile)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	
	if transferID == "" {
		t.Error("transfer ID should not be empty")
	}
	
	// Verify transfer was added
	if app.Len() != 1 {
		t.Errorf("expected 1 transfer, got %d", app.Len())
	}
	
	transfers := app.GetTransfers()
	transfer := transfers[0]
	
	if transfer.ID != transferID {
		t.Errorf("transfer ID mismatch: expected %s, got %s", transferID, transfer.ID)
	}
	
	if transfer.Name != "test.txt" {
		t.Errorf("transfer name mismatch: expected test.txt, got %s", transfer.Name)
	}
	
	if transfer.Size != int64(len(content)) {
		t.Errorf("transfer size mismatch: expected %d, got %d", len(content), transfer.Size)
	}
	
	if transfer.Status != FileTransferStatusPreparing {
		t.Errorf("transfer status should be preparing, got %s", transfer.Status)
	}
}

// TestReceiveFile_Integration tests ReceiveFile without waiting for goroutine completion  
func TestReceiveFile_Integration(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	
	app := &App{}
	app.startup(context.Background())
	
	code := "test-code"
	destination := t.TempDir()
	
	// Test ReceiveFile - expect it to set up the transfer correctly
	transferID, err := app.ReceiveFile(code, destination)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	
	if transferID == "" {
		t.Error("transfer ID should not be empty")
	}
	
	// Verify transfer was added
	if app.Len() != 1 {
		t.Errorf("expected 1 transfer, got %d", app.Len())
	}
	
	transfers := app.GetTransfers()
	transfer := transfers[0]
	
	if transfer.ID != transferID {
		t.Errorf("transfer ID mismatch: expected %s, got %s", transferID, transfer.ID)
	}
	
	if transfer.Code != code {
		t.Errorf("transfer code mismatch: expected %s, got %s", code, transfer.Code)
	}
	
	if transfer.Status != FileTransferStatusPreparing {
		t.Errorf("transfer status should be preparing, got %s", transfer.Status)
	}
}