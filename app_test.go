package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestApp builds an App with just enough state for unit-testing the
// orchestration paths that never spawn a worker process.
func newTestApp() *App {
	return &App{
		tm:                 newTransferManager(nil),
		workers:            make(map[string]*exec.Cmd),
		overwriteResponses: make(map[string]chan string),
		cancels:            make(map[string]chan struct{}),
	}
}

func TestOverwritePromptResponseDelivered(t *testing.T) {
	a := newTestApp()

	promptID, ch := a.registerOverwritePrompt()
	a.resolveOverwrite(promptID, "yes")

	select {
	case got := <-ch:
		if got != "yes" {
			t.Errorf("response = %q, want %q", got, "yes")
		}
	default:
		t.Fatal("response was not delivered to the prompt channel")
	}
}

func TestOverwriteStaleResponseCannotAnswerNextPrompt(t *testing.T) {
	// The regression this guards: a duplicate answer to prompt 1 (double
	// click, Enter+Escape) must not decide prompt 2 of the same transfer.
	a := newTestApp()

	first, firstCh := a.registerOverwritePrompt()
	a.resolveOverwrite(first, "yes")
	<-firstCh

	second, secondCh := a.registerOverwritePrompt()

	// Stale duplicate for the already-answered prompt: must be a no-op.
	a.resolveOverwrite(first, "yes")
	select {
	case got := <-secondCh:
		t.Fatalf("stale response %q answered the second prompt", got)
	default:
	}

	// The real answer still gets through.
	a.resolveOverwrite(second, "no")
	select {
	case got := <-secondCh:
		if got != "no" {
			t.Errorf("response = %q, want %q", got, "no")
		}
	default:
		t.Fatal("second prompt never received its own answer")
	}
}

func TestCancelTransferBeforeWorkerRegisters(t *testing.T) {
	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusWaiting})
	cancelCh := a.registerCancel("send-1")

	a.CancelTransfer("send-1")

	if got, _ := a.tm.get("send-1"); got.Status != FileTransferStatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	select {
	case <-cancelCh:
	default:
		t.Error("cancel channel was not closed")
	}
	// A second cancel must be a harmless no-op (channel already popped).
	a.CancelTransfer("send-1")
}

func TestRunRecoverableAttemptsSucceedsFirstTry(t *testing.T) {
	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusSending})

	ok := a.runRecoverableAttempts("send-1", make(chan struct{}), func(n, basePct int) (int, string, error) {
		return 100, "", nil
	})
	if !ok {
		t.Error("expected success")
	}
}

func TestRunRecoverableAttemptsStopsOnTerminalTransfer(t *testing.T) {
	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusSending})

	calls := 0
	ok := a.runRecoverableAttempts("send-1", make(chan struct{}), func(n, basePct int) (int, string, error) {
		calls++
		// Something external (cancel, declined offer) ends the transfer
		// while the attempt is in flight.
		a.failTransfer("send-1", "declined")
		return 0, "boom", os.ErrClosed
	})
	if ok {
		t.Error("expected failure")
	}
	if calls != 1 {
		t.Errorf("attempt ran %d times after the transfer went terminal, want 1", calls)
	}
}

func TestRunRecoverableAttemptsGivesUpWithoutProgress(t *testing.T) {
	// Mutates package-level recoveryBackoffFn — must not run in parallel.
	orig := recoveryBackoffFn
	recoveryBackoffFn = func(int) time.Duration { return 0 }
	defer func() { recoveryBackoffFn = orig }()

	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusSending})

	calls := 0
	ok := a.runRecoverableAttempts("send-1", make(chan struct{}), func(n, basePct int) (int, string, error) {
		calls++
		return 0, "link down", os.ErrClosed // never any progress
	})
	if ok {
		t.Error("expected give-up")
	}
	if calls != maxNoProgressAttempts {
		t.Errorf("attempts = %d, want %d", calls, maxNoProgressAttempts)
	}
	if got, _ := a.tm.get("send-1"); got.Status != FileTransferStatusError {
		t.Errorf("status = %q, want error after give-up", got.Status)
	}
}

func TestRunRecoverableAttemptsCancelDuringBackoff(t *testing.T) {
	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusSending})

	cancelCh := make(chan struct{})
	close(cancelCh) // backoff sleep returns immediately as "cancelled"

	calls := 0
	ok := a.runRecoverableAttempts("send-1", cancelCh, func(n, basePct int) (int, string, error) {
		calls++
		return 10, "drop", os.ErrClosed
	})
	if ok {
		t.Error("expected failure")
	}
	if calls != 1 {
		t.Errorf("attempt ran %d times after cancel, want 1", calls)
	}
}

func TestRunRecoverableAttemptsOffsetsBaseProgress(t *testing.T) {
	// Mutates package-level recoveryBackoffFn — must not run in parallel.
	orig := recoveryBackoffFn
	recoveryBackoffFn = func(int) time.Duration { return 0 }
	defer func() { recoveryBackoffFn = orig }()

	a := newTestApp()
	a.tm.add(FileTransfer{ID: "send-1", Status: FileTransferStatusSending})

	var bases []int
	ok := a.runRecoverableAttempts("send-1", make(chan struct{}), func(n, basePct int) (int, string, error) {
		bases = append(bases, basePct)
		if n == 0 {
			return 60, "drop", os.ErrClosed // first attempt reached 60%
		}
		return 100, "", nil
	})
	if !ok {
		t.Fatal("expected eventual success")
	}
	want := []int{0, 60}
	if len(bases) != len(want) || bases[0] != want[0] || bases[1] != want[1] {
		t.Errorf("basePct per attempt = %v, want %v (bar must continue, not reset)", bases, want)
	}
}

func TestScanWorkerEvents(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"files","files":["a.txt"],"size":10}`,
		`this is not json`,
		`{"type":"progress","sent":5,"size":10,"progress":50}`,
		`{"type":"error","message":"room not ready"}`,
		`{"type":"done"}`,
	}, "\n")

	var got []workerEvent
	errMsg, scanErr := scanWorkerEvents(strings.NewReader(input), func(ev workerEvent) {
		got = append(got, ev)
	})

	if scanErr != nil {
		t.Fatalf("scan error: %v", scanErr)
	}
	if errMsg != "room not ready" {
		t.Errorf("errMsg = %q, want %q", errMsg, "room not ready")
	}
	// Malformed line skipped, error event captured separately: 3 forwarded.
	if len(got) != 3 {
		t.Fatalf("forwarded %d events, want 3 (%+v)", len(got), got)
	}
	if got[0].Type != "files" || got[1].Type != "progress" || got[2].Type != "done" {
		t.Errorf("event order = %s,%s,%s", got[0].Type, got[1].Type, got[2].Type)
	}
}

func TestResendCode(t *testing.T) {
	tests := []struct {
		name       string
		transfer   FileTransfer
		wantCode   string
		wantResume bool
	}{
		{
			name:       "failed transfer with preserved code resumes",
			transfer:   FileTransfer{Status: FileTransferStatusError, ResumeCode: "kept-code"},
			wantCode:   "kept-code",
			wantResume: true,
		},
		{
			name:     "failed transfer without code starts fresh",
			transfer: FileTransfer{Status: FileTransferStatusError},
		},
		{
			name:     "completed transfer never resumes even with a code",
			transfer: FileTransfer{Status: FileTransferStatusCompleted, ResumeCode: "kept-code"},
		},
		{
			name:     "cancelled transfer never resumes",
			transfer: FileTransfer{Status: FileTransferStatusCancelled, ResumeCode: "kept-code"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resume := resendCode(tt.transfer)
			if code != tt.wantCode || resume != tt.wantResume {
				t.Errorf("resendCode() = (%q, %v), want (%q, %v)", code, resume, tt.wantCode, tt.wantResume)
			}
		})
	}
}

func TestResendTransferGuards(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.tm.add(FileTransfer{ID: "gone-files", Status: FileTransferStatusCompleted,
		Resendable: true, Paths: []string{filepath.Join(dir, "deleted.txt")}})
	a.tm.add(FileTransfer{ID: "not-resendable", Status: FileTransferStatusCompleted})
	a.tm.add(FileTransfer{ID: "peer-gone", Status: FileTransferStatusCompleted,
		Resendable: true, Paths: []string{existing}, Peer: "Brave Otter"})

	if out := a.ResendTransfer("missing-id"); out.Started || out.Message == "" {
		t.Errorf("unknown id: %+v", out)
	}
	if out := a.ResendTransfer("not-resendable"); out.Started {
		t.Errorf("non-resendable started: %+v", out)
	}
	if out := a.ResendTransfer("gone-files"); out.Started || !strings.Contains(out.Message, "deleted.txt") {
		t.Errorf("missing files outcome = %+v, want message naming deleted.txt", out)
	}
	// Peer transfer with no nearby registry: the device is unreachable.
	if out := a.ResendTransfer("peer-gone"); out.Started || !strings.Contains(out.Message, "Brave Otter") {
		t.Errorf("peer-gone outcome = %+v, want message naming the peer", out)
	}
}
