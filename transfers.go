package main

import (
	"sync"
)

type FileTransfer struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Files    []string           `json:"files"`
	Size     int64              `json:"size"`
	Progress int                `json:"progress"`
	Speed    int64              `json:"speed"` // bytes per second, 0 when idle
	Status   FileTransferStatus `json:"status"`
	Code     string             `json:"code,omitempty"`
	Peer     string             `json:"peer,omitempty"`  // device name for zero-code transfers
	Error    string             `json:"error,omitempty"` // human-readable failure reason
	// Paths are the source files of a send, kept (and persisted) so the
	// transfer can be repeated with one click.
	Paths      []string `json:"paths,omitempty"`
	Resendable bool     `json:"resendable,omitempty"`
	// PeerMachineID is the stable id of the device a peer send went to, so a
	// repeat can reach the same machine even after it restarts and renames.
	PeerMachineID string `json:"peerMachineId,omitempty"`
}

type FileTransferStatus string

const (
	FileTransferStatusPreparing FileTransferStatus = "preparing"
	FileTransferStatusWaiting   FileTransferStatus = "waiting"
	FileTransferStatusSending   FileTransferStatus = "sending"
	FileTransferStatusReceiving FileTransferStatus = "receiving"

	FileTransferStatusError     FileTransferStatus = "error"
	FileTransferStatusCancelled FileTransferStatus = "cancelled"
	FileTransferStatusCompleted FileTransferStatus = "completed"
)

// isTerminal reports whether a transfer can no longer change state.
func (s FileTransferStatus) isTerminal() bool {
	return s == FileTransferStatusError ||
		s == FileTransferStatusCancelled ||
		s == FileTransferStatusCompleted
}

// transferManager owns all transfer state. Workers and UI calls mutate
// transfers exclusively through update(), which serializes access and emits a
// copy — pointers into the internal map never escape, so concurrent transfers
// cannot corrupt each other's state.
type transferManager struct {
	mu        sync.Mutex
	transfers map[string]*FileTransfer
	order     []string // newest first, matching the UI list
	emit      func(FileTransfer)
}

func newTransferManager(emit func(FileTransfer)) *transferManager {
	if emit == nil {
		emit = func(FileTransfer) {}
	}
	return &transferManager{
		transfers: make(map[string]*FileTransfer),
		emit:      emit,
	}
}

func (m *transferManager) add(t FileTransfer) {
	m.mu.Lock()
	cp := t
	m.transfers[t.ID] = &cp
	m.order = append([]string{t.ID}, m.order...)
	m.mu.Unlock()

	m.emit(t)
}

// update applies fn to the transfer with the given id and emits the result.
// Unknown ids and transfers already in a terminal state are no-ops, so late
// worker events cannot resurrect a cancelled transfer.
func (m *transferManager) update(id string, fn func(*FileTransfer)) {
	m.mu.Lock()
	t, ok := m.transfers[id]
	if !ok || t.Status.isTerminal() {
		m.mu.Unlock()
		return
	}
	fn(t)
	cp := *t
	m.mu.Unlock()

	m.emit(cp)
}

// reset drops every transfer. The caller is responsible for telling the UI
// (a per-transfer emit cannot express "the whole list is now empty").
func (m *transferManager) reset() {
	m.mu.Lock()
	m.transfers = make(map[string]*FileTransfer)
	m.order = nil
	m.mu.Unlock()
}

func (m *transferManager) get(id string) (FileTransfer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.transfers[id]
	if !ok {
		return FileTransfer{}, false
	}
	return *t, true
}

func (m *transferManager) snapshot() []FileTransfer {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]FileTransfer, 0, len(m.order))
	for _, id := range m.order {
		if t, ok := m.transfers[id]; ok {
			out = append(out, *t)
		}
	}
	return out
}
