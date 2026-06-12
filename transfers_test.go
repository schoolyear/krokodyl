package main

import (
	"sync"
	"testing"
)

func TestTransferManagerAddAndSnapshot(t *testing.T) {
	tm := newTransferManager(nil)

	tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusPreparing})
	tm.add(FileTransfer{ID: "receive-1", Status: FileTransferStatusPreparing})

	snap := tm.snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 transfers, got %d", len(snap))
	}
	if snap[0].ID != "receive-1" {
		t.Errorf("expected newest transfer first, got %s", snap[0].ID)
	}
}

func TestTransferManagerUpdateEmitsCopy(t *testing.T) {
	var emitted []FileTransfer
	tm := newTransferManager(func(ft FileTransfer) {
		emitted = append(emitted, ft)
	})

	tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusPreparing})
	tm.update("send-1", func(ft *FileTransfer) {
		ft.Status = FileTransferStatusWaiting
		ft.Code = "1234-alpha-beta-gamma"
	})

	if len(emitted) != 2 {
		t.Fatalf("expected 2 emissions (add + update), got %d", len(emitted))
	}
	if emitted[1].Status != FileTransferStatusWaiting {
		t.Errorf("expected emitted status waiting, got %s", emitted[1].Status)
	}

	// Mutating the emitted copy must not affect manager state.
	emitted[1].Status = FileTransferStatusError
	got, _ := tm.get("send-1")
	if got.Status != FileTransferStatusWaiting {
		t.Errorf("manager state mutated through emitted copy: %s", got.Status)
	}
}

func TestTransferManagerUpdateUnknownIDIsNoop(t *testing.T) {
	calls := 0
	tm := newTransferManager(func(FileTransfer) { calls++ })

	tm.update("nope", func(ft *FileTransfer) {
		t.Error("mutator must not run for unknown id")
	})
	if calls != 0 {
		t.Errorf("expected no emissions, got %d", calls)
	}
}

func TestTransferManagerTerminalStateIsFinal(t *testing.T) {
	tests := []struct {
		name     string
		terminal FileTransferStatus
	}{
		{"cancelled stays cancelled", FileTransferStatusCancelled},
		{"completed stays completed", FileTransferStatusCompleted},
		{"error stays error", FileTransferStatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := newTransferManager(nil)
			tm.add(FileTransfer{ID: "x", Status: FileTransferStatusSending})
			tm.update("x", func(ft *FileTransfer) { ft.Status = tt.terminal })

			// A late worker event must not resurrect the transfer.
			tm.update("x", func(ft *FileTransfer) { ft.Status = FileTransferStatusSending })

			got, _ := tm.get("x")
			if got.Status != tt.terminal {
				t.Errorf("terminal status overwritten: got %s, want %s", got.Status, tt.terminal)
			}
		})
	}
}

func TestTransferManagerConcurrentUpdates(t *testing.T) {
	tm := newTransferManager(nil)
	tm.add(FileTransfer{ID: "a", Status: FileTransferStatusSending})
	tm.add(FileTransfer{ID: "b", Status: FileTransferStatusReceiving})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			tm.update("a", func(ft *FileTransfer) { ft.Progress = n })
		}(i)
		go func() {
			defer wg.Done()
			tm.snapshot()
		}()
	}
	wg.Wait()

	got, ok := tm.get("a")
	if !ok || got.Status != FileTransferStatusSending {
		t.Errorf("transfer a corrupted by concurrent updates: %+v", got)
	}
}
