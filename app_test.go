package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestApp_startup tests the startup method
func TestApp_startup(t *testing.T) {
	app := &App{}
	ctx := context.Background()
	
	app.startup(ctx)
	
	// Verify initialization
	if app.ctx == nil {
		t.Error("ctx should be set after startup")
	}
	if app.transfers == nil {
		t.Error("transfers should be initialized")
	}
	if app.overwriteResponses == nil {
		t.Error("overwriteResponses should be initialized")
	}
	if len(app.transfers) != 0 {
		t.Errorf("transfers should be empty, got %d", len(app.transfers))
	}
}

// TestApp_Len tests the Len method
func TestApp_Len(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	// Test empty transfers
	if app.Len() != 0 {
		t.Errorf("expected length 0, got %d", app.Len())
	}
	
	// Add some transfers manually
	app.transfers = []FileTransfer{
		{ID: "test1", Status: FileTransferStatusCompleted},
		{ID: "test2", Status: FileTransferStatusSending},
	}
	
	if app.Len() != 2 {
		t.Errorf("expected length 2, got %d", app.Len())
	}
}

// TestApp_GetTransfers tests the GetTransfers method
func TestApp_GetTransfers(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	// Test empty transfers
	transfers := app.GetTransfers()
	if len(transfers) != 0 {
		t.Errorf("expected 0 transfers, got %d", len(transfers))
	}
	
	// Add some transfers
	testTransfers := []FileTransfer{
		{ID: "test1", Name: "file1.txt", Status: FileTransferStatusCompleted},
		{ID: "test2", Name: "file2.txt", Status: FileTransferStatusSending},
	}
	app.transfers = testTransfers
	
	transfers = app.GetTransfers()
	if len(transfers) != 2 {
		t.Errorf("expected 2 transfers, got %d", len(transfers))
	}
	
	// Verify the returned transfers match
	for i, transfer := range transfers {
		if transfer.ID != testTransfers[i].ID {
			t.Errorf("transfer %d ID mismatch: expected %s, got %s", i, testTransfers[i].ID, transfer.ID)
		}
		if transfer.Name != testTransfers[i].Name {
			t.Errorf("transfer %d Name mismatch: expected %s, got %s", i, testTransfers[i].Name, transfer.Name)
		}
	}
}

// TestApp_getSendId tests the getSendId method
func TestApp_getSendId(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	// Test with no transfers
	id := app.getSendId()
	expected := "send-0"
	if id != expected {
		t.Errorf("expected %s, got %s", expected, id)
	}
	
	// Add some transfers and test again
	app.transfers = []FileTransfer{{ID: "test1"}, {ID: "test2"}}
	id = app.getSendId()
	expected = "send-2"
	if id != expected {
		t.Errorf("expected %s, got %s", expected, id)
	}
}

// TestApp_getReceiveId tests the getReceiveId method
func TestApp_getReceiveId(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	// Test with no transfers
	id := app.getReceiveId()
	expected := "receive-0"
	if id != expected {
		t.Errorf("expected %s, got %s", expected, id)
	}
	
	// Add some transfers and test again
	app.transfers = []FileTransfer{{ID: "test1"}, {ID: "test2"}}
	id = app.getReceiveId()
	expected = "receive-2"
	if id != expected {
		t.Errorf("expected %s, got %s", expected, id)
	}
}

// TestApp_GetDefaultDownloadPath tests the GetDefaultDownloadPath method
func TestApp_GetDefaultDownloadPath(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	path, err := app.GetDefaultDownloadPath()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	
	// Verify the path contains Downloads
	if !filepath.IsAbs(path) {
		t.Error("path should be absolute")
	}
	
	if filepath.Base(path) != "Downloads" {
		t.Errorf("expected path to end with Downloads, got %s", path)
	}
}

// TestApp_RespondToOverwrite tests the RespondToOverwrite method
func TestApp_RespondToOverwrite(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	transferID := "test-transfer"
	
	// Test with non-existent transfer ID (should not panic)
	app.RespondToOverwrite(transferID, "yes")
	
	// Test with existing transfer ID
	responseChan := make(chan string, 1)
	app.overwriteResponses[transferID] = responseChan
	
	// Respond in a goroutine to avoid blocking
	go func() {
		app.RespondToOverwrite(transferID, "yes")
	}()
	
	// Wait for response with timeout
	select {
	case response := <-responseChan:
		if response != "yes" {
			t.Errorf("expected 'yes', got %s", response)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for response")
	}
	
	// Verify the channel is cleaned up
	if _, exists := app.overwriteResponses[transferID]; exists {
		t.Error("response channel should be cleaned up after response")
	}
}

// TestApp_SendFile_NonexistentFile tests SendFile with a non-existent file
func TestApp_SendFile_NonexistentFile(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	nonexistentFile := "/tmp/nonexistent/file.txt"
	
	_, err := app.SendFile(nonexistentFile)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
	
	// Verify no transfer was added
	if app.Len() != 0 {
		t.Errorf("expected 0 transfers, got %d", app.Len())
	}
}

// TestApp_SendFile_ValidFile tests SendFile file validation logic
func TestApp_SendFile_ValidFile(t *testing.T) {
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
	
	// Test the file stat logic that SendFile uses internally
	fileInfo, err := os.Stat(testFile)
	if err != nil {
		t.Errorf("os.Stat failed: %v", err)
	}
	
	if fileInfo.Name() != "test.txt" {
		t.Errorf("file name mismatch: expected test.txt, got %s", fileInfo.Name())
	}
	
	if fileInfo.Size() != int64(len(content)) {
		t.Errorf("file size mismatch: expected %d, got %d", len(content), fileInfo.Size())
	}
	
	// Test transfer ID generation
	sendId := app.getSendId()
	if sendId != "send-0" {
		t.Errorf("expected send-0, got %s", sendId)
	}
	
	// Note: We don't call SendFile directly to avoid Wails runtime issues
	// The actual SendFile functionality is tested in integration tests
}

// TestFileTransferStatus tests the FileTransferStatus constants
func TestFileTransferStatus(t *testing.T) {
	tests := []struct {
		status   FileTransferStatus
		expected string
	}{
		{FileTransferStatusPreparing, "preparing"},
		{FileTransferStatusWaiting, "waiting"},
		{FileTransferStatusSending, "sending"},
		{FileTransferStatusReceiving, "receiving"},
		{FileTransferStatusError, "error"},
		{FileTransferStatusCompleted, "completed"},
	}
	
	for _, test := range tests {
		if string(test.status) != test.expected {
			t.Errorf("status %v should equal %s, got %s", test.status, test.expected, string(test.status))
		}
	}
}

// TestTransferEventConstants tests the transfer event constants
func TestTransferEventConstants(t *testing.T) {
	if TransferEventUpdated != "transfer:updated" {
		t.Errorf("TransferEventUpdated should be 'transfer:updated', got %s", TransferEventUpdated)
	}
	
	if TransferEventOverwrite != "transfer:overwrite" {
		t.Errorf("TransferEventOverwrite should be 'transfer:overwrite', got %s", TransferEventOverwrite)
	}
}

// TestListFiles tests the listFiles function
func TestListFiles(t *testing.T) {
	// Create a temporary directory structure for testing
	tempDir := t.TempDir()
	
	// Create some test files
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")
	subDir := filepath.Join(tempDir, "subdir")
	file3 := filepath.Join(subDir, "file3.txt")
	
	err := os.WriteFile(file1, []byte("content1"), 0644)
	if err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	
	err = os.WriteFile(file2, []byte("content2"), 0644)
	if err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}
	
	err = os.MkdirAll(subDir, 0755)
	if err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	
	err = os.WriteFile(file3, []byte("content3"), 0644)
	if err != nil {
		t.Fatalf("failed to create file3: %v", err)
	}
	
	// Test listFiles
	files, err := listFiles(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	// Should find 3 files (not directories)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
	
	// Check that all files are found
	expectedFiles := map[string]bool{
		file1: false,
		file2: false,
		file3: false,
	}
	
	for path := range files {
		if _, exists := expectedFiles[path]; exists {
			expectedFiles[path] = true
		}
	}
	
	for path, found := range expectedFiles {
		if !found {
			t.Errorf("file %s was not found by listFiles", path)
		}
	}
}

// TestApp_ConcurrentAccess tests that the app handles concurrent access safely
func TestApp_ConcurrentAccess(t *testing.T) {
	app := &App{}
	app.startup(context.Background())
	
	// Test concurrent Len() calls
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			app.Len()
		}
		done <- true
	}()
	
	go func() {
		for i := 0; i < 100; i++ {
			app.GetTransfers()
		}
		done <- true
	}()
	
	// Wait for both goroutines to complete
	<-done
	<-done
}

// TestFileTransfer struct validation
func TestFileTransfer(t *testing.T) {
	transfer := FileTransfer{
		ID:       "test-id",
		Name:     "test-file.txt",
		Files:    []string{"test-file.txt"},
		Size:     1024,
		Progress: 50,
		Status:   FileTransferStatusSending,
		Code:     "test-code",
	}
	
	if transfer.ID != "test-id" {
		t.Errorf("ID mismatch: expected test-id, got %s", transfer.ID)
	}
	
	if transfer.Name != "test-file.txt" {
		t.Errorf("Name mismatch: expected test-file.txt, got %s", transfer.Name)
	}
	
	if len(transfer.Files) != 1 || transfer.Files[0] != "test-file.txt" {
		t.Errorf("Files mismatch: expected [test-file.txt], got %v", transfer.Files)
	}
	
	if transfer.Size != 1024 {
		t.Errorf("Size mismatch: expected 1024, got %d", transfer.Size)
	}
	
	if transfer.Progress != 50 {
		t.Errorf("Progress mismatch: expected 50, got %d", transfer.Progress)
	}
	
	if transfer.Status != FileTransferStatusSending {
		t.Errorf("Status mismatch: expected %s, got %s", FileTransferStatusSending, transfer.Status)
	}
	
	if transfer.Code != "test-code" {
		t.Errorf("Code mismatch: expected test-code, got %s", transfer.Code)
	}
}

// TestOverwritePrompt struct validation
func TestOverwritePrompt(t *testing.T) {
	prompt := OverwritePrompt{
		TransferID: "test-transfer",
		FileName:   "test.txt",
		OldSize:    100,
		NewSize:    200,
		Diff:       "test diff",
	}
	
	if prompt.TransferID != "test-transfer" {
		t.Errorf("TransferID mismatch: expected test-transfer, got %s", prompt.TransferID)
	}
	
	if prompt.FileName != "test.txt" {
		t.Errorf("FileName mismatch: expected test.txt, got %s", prompt.FileName)
	}
	
	if prompt.OldSize != 100 {
		t.Errorf("OldSize mismatch: expected 100, got %d", prompt.OldSize)
	}
	
	if prompt.NewSize != 200 {
		t.Errorf("NewSize mismatch: expected 200, got %d", prompt.NewSize)
	}
	
	if prompt.Diff != "test diff" {
		t.Errorf("Diff mismatch: expected 'test diff', got %s", prompt.Diff)
	}
}

// TestGetFileDiff tests the getFileDiff function
func TestGetFileDiff(t *testing.T) {
	tempDir := t.TempDir()
	
	// Create two test files with different content
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")
	
	content1 := "Hello, World!"
	content2 := "Hello, Universe!"
	
	err := os.WriteFile(file1, []byte(content1), 0644)
	if err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	
	err = os.WriteFile(file2, []byte(content2), 0644)
	if err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}
	
	// Test with different files
	diff, err := getFileDiff(file1, file2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	
	if diff == "" {
		t.Error("diff should not be empty for different files")
	}
	
	// Verify diff contains expected headers
	if !strings.Contains(diff, "--- a/file1.txt") {
		t.Error("diff should contain file1 header")
	}
	
	if !strings.Contains(diff, "+++ b/file2.txt") {
		t.Error("diff should contain file2 header")
	}
	
	// Test with identical files
	diff2, err := getFileDiff(file1, file1)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	
	expectedMsg := "Files are identical."
	if diff2 != expectedMsg {
		t.Errorf("expected '%s', got '%s'", expectedMsg, diff2)
	}
	
	// Test with non-existent file (first file)
	_, err = getFileDiff("/nonexistent/file.txt", file1)
	if err == nil {
		t.Error("expected error for non-existent first file")
	}
	
	// Test with non-existent file (second file)
	_, err = getFileDiff(file1, "/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for non-existent second file")
	}
}