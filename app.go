package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schollz/croc/v10/src/utils"
	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type OverwritePrompt struct {
	TransferID string `json:"transferId"`
	FileName   string `json:"fileName"`
	OldSize    int64  `json:"oldSize"`
	NewSize    int64  `json:"newSize"`
	OldModTime string `json:"oldModTime"`
	NewModTime string `json:"newModTime"`
}

// App struct
type App struct {
	ctx context.Context
	tm  *transferManager

	mu                 sync.Mutex
	workers            map[string]*exec.Cmd
	overwriteResponses map[string]chan string
	cancels            map[string]chan struct{}

	historyMu sync.Mutex

	nearby          *peerRegistry
	nearbySrv       *nearbyServer
	deviceName      string
	machineID       string
	nearbyGen       int
	nearbyIdentity  discoveryIdentity
	nearbyEmitState func(DiscoveryState)
	stopDiscovery   func()
}

const (
	TransferEventUpdated   string = "transfer:updated"
	TransferEventCleared   string = "transfer:cleared"
	TransferEventOverwrite string = "transfer:overwrite"
	NearbyEventUpdated     string = "nearby:updated"
	NearbyEventState       string = "nearby:state"
	NearbyEventOffer       string = "nearby:offer"
)

// senderWaitTimeout bounds how long a sender waits for a receiver to show up
// before the transfer fails instead of hanging forever.
const senderWaitTimeout = time.Hour

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.tm = newTransferManager(func(t FileTransfer) {
		runtime.EventsEmit(ctx, TransferEventUpdated, t)
		if t.Status.isTerminal() {
			go a.persistHistory()
		}
	})

	// Restore past transfers so history survives restarts. Reversed so
	// add()'s prepend keeps newest first.
	if path, err := historyPath(); err == nil {
		hist := loadHistory(path)
		for i := len(hist) - 1; i >= 0; i-- {
			a.tm.add(hist[i])
		}
	}

	a.workers = make(map[string]*exec.Cmd)
	a.overwriteResponses = make(map[string]chan string)
	a.cancels = make(map[string]chan struct{})

	// Files dropped anywhere on the window start a send immediately.
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		if len(paths) == 0 {
			return
		}
		if _, err := a.SendFiles(paths); err != nil {
			logrus.WithError(err).Error("failed to send dropped files")
		}
	})

	a.startNearby()
}

// startNearby brings up the zero-code stack: the TLS control channel first
// (its port and certificate fingerprint go into the discovery announcement),
// then multicast discovery itself. Without a control channel we do not
// announce at all — peers must never see a device they cannot reach.
func (a *App) startNearby() {
	// A friendly, readable, per-process name ("Brave Otter") so devices —
	// and two windows on one machine — are individually recognizable. The OS
	// hostname stays in the logs as the stable machine anchor.
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "krokodyl"
	}
	a.deviceName = randomDeviceName()
	if path, err := settingsPath(); err == nil {
		a.machineID = ensureMachineID(path)
	}
	logrus.Infof("nearby identity: %q (host %s)", a.deviceName, hostname)

	emitState := func(state DiscoveryState) {
		runtime.EventsEmit(a.ctx, NearbyEventState, state)
	}

	srv, port, fingerprint, err := startNearbyServer(
		func(offer NearbyOffer) {
			runtime.EventsEmit(a.ctx, NearbyEventOffer, offer)
		},
		a.acceptPeerTransfer,
	)
	if err != nil {
		logrus.WithError(err).Warn("nearby control channel unavailable, zero-code sending disabled")
		emitState(DiscoveryState{Available: false})
		return
	}
	a.nearbySrv = srv

	a.nearbyGen = 1
	identity := discoveryIdentity{
		ID:          uuid.NewString(),
		Name:        a.deviceName,
		Port:        port,
		MachineID:   a.machineID,
		Fingerprint: fingerprint,
		Gen:         a.nearbyGen,
	}
	a.nearby = newPeerRegistry(identity.ID, func(peers []NearbyPeer) {
		runtime.EventsEmit(a.ctx, NearbyEventUpdated, peers)
	})

	visible := true
	if path, err := settingsPath(); err == nil {
		visible = loadSettings(path).nearbyVisible()
	}

	a.nearbyIdentity = identity
	a.nearbyEmitState = emitState
	a.stopDiscovery = startDiscovery(identity, a.nearby, emitState, visible)
}

// NearbyPrefs is the frontend's view of the nearby-related settings.
type NearbyPrefs struct {
	Visible  bool   `json:"visible"`
	LastPeer string `json:"lastPeer"`
}

func (a *App) GetNearbyPrefs() NearbyPrefs {
	prefs := NearbyPrefs{Visible: true}
	if path, err := settingsPath(); err == nil {
		s := loadSettings(path)
		prefs.Visible = s.nearbyVisible()
		prefs.LastPeer = s.LastPeer
	}
	return prefs
}

// SetNearbyVisible toggles whether this device announces itself. Turning
// visibility off restarts discovery in listen-only mode (the goodbye burst
// makes us vanish from peers immediately); we keep seeing others and can
// still send.
func (a *App) SetNearbyVisible(visible bool) {
	if path, err := settingsPath(); err == nil {
		s := loadSettings(path)
		s.NearbyVisible = &visible
		if err := saveSettings(path, s); err != nil {
			logrus.WithError(err).Warn("could not save visibility setting")
		}
	}

	if a.stopDiscovery == nil || a.nearby == nil {
		return
	}
	a.stopDiscovery()
	// Becoming visible again bumps the generation so peers that still hold a
	// goodbye-suppression window for us accept the return announcement
	// immediately instead of waiting it out.
	if visible {
		a.nearbyGen++
		a.nearbyIdentity.Gen = a.nearbyGen
	}
	a.stopDiscovery = startDiscovery(a.nearbyIdentity, a.nearby, a.nearbyEmitState, visible)
}

// rememberLastPeer persists the most recent zero-code target, best-effort.
func (a *App) rememberLastPeer(name string) {
	path, err := settingsPath()
	if err != nil {
		return
	}
	s := loadSettings(path)
	if s.LastPeer == name {
		return
	}
	s.LastPeer = name
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not save last peer")
	}
}

// acceptPeerTransfer runs after the user accepted an offer and the code
// arrived: start receiving into the remembered destination.
func (a *App) acceptPeerTransfer(senderName, code string) {
	dest, err := a.GetDefaultDownloadPath()
	if err != nil {
		logrus.WithError(err).Error("cannot receive nearby transfer: no destination")
		return
	}
	if _, err := a.startReceive(code, dest, senderName); err != nil {
		logrus.WithError(err).Error("could not start nearby receive")
	}
}

// SendToPeer offers files to a nearby device and, once accepted, sends them
// with an internally negotiated code — never shown to either user.
func (a *App) SendToPeer(peerID string, paths []string) (string, error) {
	if a.nearby == nil {
		return "", fmt.Errorf("nearby sending is not available")
	}
	peer, ok := a.nearby.get(peerID)
	if !ok {
		return "", fmt.Errorf("device is no longer nearby")
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no files selected")
	}

	var names []string
	var totalSize int64
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("failed to stat %s: %w", p, err)
		}
		names = append(names, info.Name())
		if !info.IsDir() {
			totalSize += info.Size()
		}
	}
	name := names[0]
	if len(names) > 1 {
		name = fmt.Sprintf("%s and %d more", names[0], len(names)-1)
	}

	transfer := FileTransfer{
		ID:            "send-" + uuid.NewString(),
		Name:          name,
		Files:         names,
		Size:          totalSize,
		Status:        FileTransferStatusWaiting,
		Peer:          peer.Name,
		PeerMachineID: peer.MachineID,
		Paths:         paths,
		Resendable:    true,
	}
	a.tm.add(transfer)

	go a.performPeerSend(transfer.ID, peer, paths, names, totalSize)

	return transfer.ID, nil
}

func (a *App) performPeerSend(id string, peer NearbyPeer, paths, names []string, totalSize int64) {
	code := utils.GetRandomName()

	// Open our croc room first, then offer: the receiver starts receiving
	// the moment the user accepts, and joining a room that doesn't exist
	// yet fails with "room not ready". The worker idles on the relay while
	// the human decides; decline/timeout kills it below.
	go a.performSendWithCode(id, paths, code)

	answer, err := sendNearbyOffer(peer.Addr, peer.Port, peer.Fingerprint, offerRequest{
		SenderName: a.deviceName,
		Files:      names,
		Size:       totalSize,
	}, code)

	abort := func(message string) {
		// Terminal state first: the worker's exit must not overwrite it.
		a.failTransfer(id, message)
		// The worker goroutine may not have registered itself yet; wait
		// briefly so the kill actually lands.
		for i := 0; i < 20; i++ {
			a.mu.Lock()
			_, registered := a.workers[id]
			a.mu.Unlock()
			if registered {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		a.killWorker(id)
	}

	if err != nil {
		abort(err.Error())
		return
	}
	if !answer.Accepted {
		if answer.Busy {
			abort(fmt.Sprintf("%s is busy with another transfer offer", peer.Name))
		} else {
			abort(fmt.Sprintf("%s declined the transfer", peer.Name))
		}
		return
	}

	a.rememberLastPeer(peer.Name)
}

// RespondToNearbyOffer resolves the incoming-offer prompt.
func (a *App) RespondToNearbyOffer(offerID string, accept bool) {
	if a.nearbySrv != nil {
		a.nearbySrv.respond(offerID, accept)
	}
}

func (a *App) GetNearbyPeers() []NearbyPeer {
	if a.nearby == nil {
		return nil
	}
	return a.nearby.snapshot()
}

// registerCancel creates the signal channel a transfer goroutine selects on
// while blocked (e.g. waiting for an overwrite answer). Closing it via
// popCancel makes cancellation reach every blocking point exactly once.
func (a *App) registerCancel(id string) chan struct{} {
	ch := make(chan struct{})
	a.mu.Lock()
	a.cancels[id] = ch
	a.mu.Unlock()
	return ch
}

func (a *App) unregisterCancel(id string) {
	a.mu.Lock()
	delete(a.cancels, id)
	a.mu.Unlock()
}

// popCancel removes and returns the cancel channel so it can be closed at
// most once even when CancelTransfer and shutdown race.
func (a *App) popCancel(id string) (chan struct{}, bool) {
	a.mu.Lock()
	ch, ok := a.cancels[id]
	if ok {
		delete(a.cancels, id)
	}
	a.mu.Unlock()
	return ch, ok
}

// shutdown kills live transfer workers so closing the app never leaves
// orphan processes or blocked transfers behind.
func (a *App) shutdown(_ context.Context) {
	if a.stopDiscovery != nil {
		a.stopDiscovery()
	}
	if a.nearbySrv != nil {
		a.nearbySrv.close()
	}

	a.mu.Lock()
	cmds := make(map[string]*exec.Cmd, len(a.workers))
	for id, cmd := range a.workers {
		cmds[id] = cmd
	}
	cancels := a.cancels
	a.cancels = make(map[string]chan struct{})
	a.mu.Unlock()

	for _, ch := range cancels {
		close(ch)
	}
	for id, cmd := range cmds {
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				logrus.WithError(err).Warnf("failed to kill worker for transfer %s", id)
			}
		}
	}
}

func (a *App) GetTransfers() []FileTransfer {
	return a.tm.snapshot()
}

func (a *App) SendFile(filePath string) (string, error) {
	return a.SendFiles([]string{filePath})
}

func (a *App) SendFiles(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no files selected")
	}

	var names []string
	var totalSize int64
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("failed to stat %s: %w", p, err)
		}
		names = append(names, info.Name())
		if !info.IsDir() {
			totalSize += info.Size()
		}
	}

	name := names[0]
	if len(names) > 1 {
		name = fmt.Sprintf("%s and %d more", names[0], len(names)-1)
	}

	transfer := FileTransfer{
		ID:         "send-" + uuid.NewString(),
		Name:       name,
		Files:      names,
		Size:       totalSize,
		Status:     FileTransferStatusPreparing,
		Paths:      paths,
		Resendable: true,
	}
	a.tm.add(transfer)

	go a.performSend(transfer.ID, paths)

	return transfer.ID, nil
}

// ResendOutcome always carries a human-readable message so the frontend can
// give feedback for every case — success, "device gone", or missing files —
// without depending on promise-rejection semantics.
type ResendOutcome struct {
	Started bool   `json:"started"`
	Message string `json:"message"`
}

// ResendTransfer repeats a past send with the same source files. Peer sends
// re-target the same machine by its stable machine id (so a restart + rename
// still matches). If that machine is no longer nearby it starts nothing and
// says so — a doomed code send stuck on "waiting" forever is worse than a
// clear message. Missing source files abort with an explicit list rather than
// sending a silent partial.
func (a *App) ResendTransfer(id string) ResendOutcome {
	t, ok := a.tm.get(id)
	if !ok {
		return ResendOutcome{Message: "That transfer is no longer available."}
	}
	if !t.Resendable || len(t.Paths) == 0 {
		return ResendOutcome{Message: "This transfer can't be repeated."}
	}

	var missing []string
	for _, p := range t.Paths {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, filepath.Base(p))
		}
	}
	if len(missing) > 0 {
		return ResendOutcome{Message: "Can't repeat — these files no longer exist: " + strings.Join(missing, ", ")}
	}

	// Not a peer send — plain code resend (waiting for a code is the point).
	if t.Peer == "" {
		if _, err := a.SendFiles(t.Paths); err != nil {
			return ResendOutcome{Message: err.Error()}
		}
		return ResendOutcome{Started: true, Message: "Sending again with a new code."}
	}

	peerID, ok := a.findPeerForResend(t.PeerMachineID, t.Peer)
	if !ok {
		return ResendOutcome{Message: t.Peer + " isn't nearby anymore — open krokodyl there to send again."}
	}
	if _, err := a.SendToPeer(peerID, t.Paths); err != nil {
		return ResendOutcome{Message: err.Error()}
	}
	return ResendOutcome{Started: true, Message: "Sending again to " + t.Peer + "."}
}

// findPeerForResend prefers the stable machine id and falls back to the
// display name for legacy history entries that predate machine ids.
func (a *App) findPeerForResend(machineID, name string) (string, bool) {
	if a.nearby == nil {
		return "", false
	}
	if machineID != "" {
		for _, p := range a.nearby.snapshot() {
			if p.MachineID == machineID {
				return p.ID, true
			}
		}
		return "", false
	}
	return a.findPeerByName(name)
}

func (a *App) findPeerByName(name string) (string, bool) {
	if a.nearby == nil {
		return "", false
	}
	for _, p := range a.nearby.snapshot() {
		if p.Name == name {
			return p.ID, true
		}
	}
	return "", false
}

func (a *App) performSend(id string, paths []string) {
	code := utils.GetRandomName()
	// Code-flow sends surface the code so the user can share it; peer sends
	// keep it internal (performSendWithCode is called directly there).
	a.tm.update(id, func(t *FileTransfer) {
		t.Code = code
		t.Status = FileTransferStatusWaiting
	})
	a.performSendWithCode(id, paths, code)
}

func (a *App) performSendWithCode(id string, paths []string, code string) {
	timeout := time.AfterFunc(senderWaitTimeout, func() {
		if t, ok := a.tm.get(id); ok && t.Status == FileTransferStatusWaiting {
			a.failTransfer(id, "no receiver connected, transfer timed out")
			a.killWorker(id)
		}
	})
	defer timeout.Stop()

	job := workerJob{Mode: "send", Code: code, Paths: paths}
	workerErrMsg, err := a.runWorkerJob(id, job, func(ev workerEvent) {
		switch ev.Type {
		case "files":
			a.tm.update(id, func(t *FileTransfer) {
				t.Files = ev.Files
				t.Size = ev.Size
			})
		case "progress":
			a.tm.update(id, func(t *FileTransfer) {
				t.Progress = ev.Progress
				t.Speed = ev.Speed
				if ev.Sent > 0 {
					t.Status = FileTransferStatusSending
				}
			})
		}
	})
	if err != nil {
		a.failTransfer(id, workerErrMsg)
		return
	}

	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusCompleted
		t.Progress = 100
		t.Speed = 0
	})
}

func (a *App) ReceiveFile(code, destinationPath string) (string, error) {
	return a.startReceive(code, destinationPath, "")
}

func (a *App) startReceive(code, destinationPath, peerName string) (string, error) {
	info, err := os.Stat(destinationPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("destination is not a usable directory: %s", destinationPath)
	}

	a.rememberDestination(destinationPath)

	transfer := FileTransfer{
		ID:     "receive-" + uuid.NewString(),
		Code:   code,
		Status: FileTransferStatusPreparing,
		Name:   "Receiving...",
		Files:  []string{},
		Peer:   peerName,
	}
	if peerName != "" {
		// Zero-code transfer: the code is plumbing, not something to show.
		transfer.Code = ""
	}
	a.tm.add(transfer)

	go a.performReceive(transfer.ID, code, destinationPath)

	return transfer.ID, nil
}

func (a *App) performReceive(id, code, destinationPath string) {
	cancelCh := a.registerCancel(id)
	defer a.unregisterCancel(id)

	// Staging inside the destination keeps it on the same volume, so the
	// final per-file rename is instant and never crosses devices.
	stagingDir := filepath.Join(destinationPath, ".krokodyl-partial-"+id)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		a.failTransfer(id, fmt.Sprintf("cannot write to destination: %s", err))
		return
	}
	defer os.RemoveAll(stagingDir)

	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusReceiving
	})

	job := workerJob{Mode: "receive", Code: code, StagingDir: stagingDir}
	workerErrMsg, err := a.runWorkerJob(id, job, func(ev workerEvent) {
		if ev.Type == "progress" {
			a.tm.update(id, func(t *FileTransfer) {
				t.Progress = ev.Progress
				t.Speed = ev.Speed
				t.Size = ev.Size
			})
		}
	})
	if err != nil {
		a.failTransfer(id, workerErrMsg)
		return
	}

	a.finalizeReceive(id, stagingDir, destinationPath, cancelCh)
}

// finalizeReceive moves downloaded files from staging into the destination,
// prompting for overwrites, with relative paths preserved so nested folder
// structures arrive intact.
func (a *App) finalizeReceive(id, stagingDir, destinationPath string, cancelCh chan struct{}) {
	staged, err := listStagedFiles(stagingDir)
	if err != nil {
		a.failTransfer(id, err.Error())
		return
	}
	if len(staged) == 0 {
		a.failTransfer(id, "no files were received")
		return
	}

	var moved []string
	var totalSize int64
	for _, sf := range staged {
		select {
		case <-cancelCh:
			// Cancelled mid-finalize: status is already cancelled, staging
			// cleanup happens in performReceive's defer.
			return
		default:
		}

		destPath := filepath.Join(destinationPath, sf.RelPath)
		if existing, err := os.Stat(destPath); err == nil {
			if !a.promptOverwrite(id, sf, existing, cancelCh) {
				logrus.Infof("user chose not to overwrite %s", sf.RelPath)
				continue
			}
		}

		if err := moveStagedFile(stagingDir, destinationPath, sf.RelPath, nil); err != nil {
			a.failTransfer(id, err.Error())
			return
		}
		moved = append(moved, sf.RelPath)
		totalSize += sf.Size
	}

	a.tm.update(id, func(t *FileTransfer) {
		switch len(moved) {
		case 0:
			t.Name = "File received"
		case 1:
			t.Name = moved[0]
		default:
			t.Name = fmt.Sprintf("%s and %d more", moved[0], len(moved)-1)
		}
		t.Files = moved
		t.Size = totalSize
		t.Status = FileTransferStatusCompleted
		t.Progress = 100
		t.Speed = 0
	})
}

// promptOverwrite asks the frontend whether an existing file should be
// replaced and blocks until the user answers or the transfer is cancelled
// (cancel/shutdown count as "no").
func (a *App) promptOverwrite(id string, sf stagedFile, existing os.FileInfo, cancelCh chan struct{}) bool {
	responseChan := make(chan string, 1)
	a.mu.Lock()
	a.overwriteResponses[id] = responseChan
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.overwriteResponses, id)
		a.mu.Unlock()
	}()

	runtime.EventsEmit(a.ctx, TransferEventOverwrite, OverwritePrompt{
		TransferID: id,
		FileName:   sf.RelPath,
		OldSize:    existing.Size(),
		NewSize:    sf.Size,
		OldModTime: existing.ModTime().Format(time.RFC1123),
		NewModTime: sf.ModTime.Format(time.RFC1123),
	})

	select {
	case response := <-responseChan:
		return response == "yes"
	case <-cancelCh:
		return false
	}
}

func (a *App) RespondToOverwrite(transferID string, response string) {
	a.mu.Lock()
	responseChan, ok := a.overwriteResponses[transferID]
	if ok {
		delete(a.overwriteResponses, transferID)
	}
	a.mu.Unlock()

	if ok {
		select {
		case responseChan <- response:
		default:
		}
	}
}

// CancelTransfer kills the worker process behind a transfer and signals every
// blocking point (overwrite prompts, finalize loop). Receive staging is
// cleaned up by the goroutine that created it.
func (a *App) CancelTransfer(id string) {
	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusCancelled
		t.Speed = 0
	})
	if ch, ok := a.popCancel(id); ok {
		close(ch)
	}
	a.killWorker(id)
}

func (a *App) killWorker(id string) {
	a.mu.Lock()
	cmd, ok := a.workers[id]
	a.mu.Unlock()

	if ok && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			logrus.WithError(err).Warnf("failed to kill worker for transfer %s", id)
		}
	}
}

func (a *App) failTransfer(id, message string) {
	logrus.Errorf("transfer %s failed: %s", id, message)
	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusError
		t.Error = message
		t.Speed = 0
	})
}

// runWorkerJob spawns this binary in worker mode, streams its events to
// onEvent and blocks until the worker exits. It returns the most descriptive
// error message available when the worker fails.
func (a *App) runWorkerJob(id string, job workerJob, onEvent func(workerEvent)) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "could not locate application executable", err
	}

	// Stderr stays nil so the worker gets the null device. The parent is a
	// GUI-subsystem binary: when launched without a console its own stderr
	// handle is invalid, and inheriting it makes croc's progress output
	// fail the whole transfer ("write /dev/stderr: The handle is invalid").
	// Worker diagnostics go to the shared log file instead.
	cmd := exec.Command(exePath, workerFlag)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "failed to start transfer worker", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "failed to start transfer worker", err
	}

	if err := cmd.Start(); err != nil {
		return "failed to start transfer worker", err
	}

	a.mu.Lock()
	a.workers[id] = cmd
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.workers, id)
		a.mu.Unlock()
	}()

	if err := json.NewEncoder(stdin).Encode(job); err != nil {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return "failed to hand job to transfer worker", err
	}
	stdin.Close()

	errMsg := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			logrus.WithError(err).Warn("ignoring malformed worker event")
			continue
		}
		if ev.Type == "error" {
			errMsg = ev.Message
			continue
		}
		onEvent(ev)
	}
	scanErr := scanner.Err()

	if err := cmd.Wait(); err != nil {
		if errMsg == "" {
			errMsg = fmt.Sprintf("transfer worker stopped unexpectedly: %s", err)
		}
		return errMsg, err
	}
	if scanErr != nil {
		// The worker exited cleanly but its event stream broke — do not
		// trust the transfer as completed.
		return "transfer worker output was interrupted", scanErr
	}
	if errMsg != "" {
		return errMsg, fmt.Errorf("worker reported error: %s", errMsg)
	}
	return "", nil
}

func (a *App) SelectFile() (string, error) {
	selection, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to send",
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}

	return selection, nil
}

func (a *App) SelectFiles() ([]string, error) {
	selection, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select files to send",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open file dialog: %w", err)
	}

	return selection, nil
}

func (a *App) SelectDirectory() (string, error) {
	selection, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select destination directory",
	})
	if err != nil {
		return "", fmt.Errorf("failed to open directory dialog: %w", err)
	}

	return selection, nil
}

// GetDefaultDownloadPath prefers the destination used last time, falling
// back to the user's Downloads folder.
func (a *App) GetDefaultDownloadPath() (string, error) {
	if path, err := settingsPath(); err == nil {
		saved := loadSettings(path).LastDestination
		if info, err := os.Stat(saved); err == nil && info.IsDir() {
			return saved, nil
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, "Downloads"), nil
}

// GetDeviceName returns this instance's friendly readable name.
func (a *App) GetDeviceName() string {
	return a.deviceName
}

// ClearHistory empties the transfer list and the persisted history file.
func (a *App) ClearHistory() {
	a.tm.reset()
	runtime.EventsEmit(a.ctx, TransferEventCleared)

	path, err := historyPath()
	if err != nil {
		return
	}
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if err := clearHistory(path); err != nil {
		logrus.WithError(err).Warn("could not clear persisted history")
	}
}

// persistHistory writes the terminal transfers to disk, best-effort.
func (a *App) persistHistory() {
	path, err := historyPath()
	if err != nil {
		return
	}

	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if err := saveHistory(path, a.tm.snapshot()); err != nil {
		logrus.WithError(err).Warn("could not save transfer history")
	}
}

// rememberDestination persists the chosen destination, best-effort.
func (a *App) rememberDestination(destination string) {
	path, err := settingsPath()
	if err != nil {
		return
	}
	s := loadSettings(path)
	if s.LastDestination == destination {
		return
	}
	s.LastDestination = destination
	if err := saveSettings(path, s); err != nil {
		logrus.WithError(err).Warn("could not save settings")
	}
}
