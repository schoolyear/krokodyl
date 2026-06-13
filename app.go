package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// OverwritePrompt is the payload of a transfer:overwrite event asking the
// user whether an existing destination file should be replaced.
type OverwritePrompt struct {
	// PromptID identifies this specific question. A multi-file transfer asks
	// once per conflicting file, so responses are matched by prompt — never by
	// transfer — or a stale answer (double-click, Enter+Escape) could silently
	// decide the next file's fate.
	PromptID   string `json:"promptId"`
	TransferID string `json:"transferId"`
	FileName   string `json:"fileName"`
	OldSize    int64  `json:"oldSize"`
	NewSize    int64  `json:"newSize"`
	OldModTime string `json:"oldModTime"`
	NewModTime string `json:"newModTime"`
}

// App is the Wails-bound application core: it owns transfer state, spawns
// per-transfer worker subprocesses, runs nearby discovery, and exposes the
// methods the frontend calls.
type App struct {
	ctx context.Context
	tm  *transferManager

	mu      sync.Mutex
	workers map[string]*exec.Cmd
	// overwriteResponses is keyed by per-prompt id (not transfer id) so a
	// stale answer can never decide a later prompt.
	overwriteResponses map[string]chan string
	cancels            map[string]chan struct{}
	// expectations holds, per receive transfer, what the accepted nearby
	// offer promised — so the finished receive can be checked against it.
	expectations map[string]*receiveExpectation

	// ble bootstraps offline (no-network) pairing. Defaults to a no-op radio
	// unless built with -tags krokodyl_ble; Nearby-Direct then falls back to
	// guided manual hotspot pairing.
	ble bleRadio

	// webRecv is the opt-in phone→desktop upload server; nil unless the user
	// has "Receive from phone" turned on.
	webRecv *webReceiver
	// localSend speaks the LocalSend protocol so that app can send to us;
	// started/stopped with the same opt-in as webRecv. lsOffers holds pending
	// LocalSend accept prompts (reusing the nearby:offer UI), keyed by offer id.
	localSend *localSendReceiver
	lsOffers  map[string]chan bool
	// kdeStop / warpStop stop the gated KDE Connect and Warpinator adapters
	// (nil unless built with their tags and currently receiving).
	kdeStop  func()
	warpStop func()

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
	TransferEventVerify    string = "transfer:verify"
	NearbyEventUpdated     string = "nearby:updated"
	NearbyEventState       string = "nearby:state"
	NearbyEventOffer       string = "nearby:offer"
)

// VerifyPrompt asks the user whether to keep a nearby receive whose content
// does not match the offer they accepted.
type VerifyPrompt struct {
	PromptID   string `json:"promptId"`
	TransferID string `json:"transferId"`
	Detail     string `json:"detail"`
}

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
	a.expectations = make(map[string]*receiveExpectation)
	a.lsOffers = make(map[string]chan bool)
	a.ble = newBLERadio()

	// Files dropped anywhere on the window start a send immediately.
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		if len(paths) == 0 {
			return
		}
		if _, err := a.SendFiles(paths); err != nil {
			logrus.WithError(err).Error("failed to send dropped files")
		}
	})

	// Sweep partial dirs left by transfers that were never resumed.
	if sp, err := settingsPath(); err == nil {
		sweepPartials(sp, time.Now().Unix())
	}

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
		// Advertise every reachable address so a peer on the real LAN can
		// connect even when a virtual adapter (Hyper-V/WSL/Docker) would
		// otherwise be the only one it learned about.
		Addrs: localUnicastIPs(),
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

// GetNearbyPrefs returns the persisted nearby preferences for the frontend.
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
		err := updateSettings(path, func(s *appSettings) {
			s.NearbyVisible = &visible
		})
		if err != nil {
			logrus.WithError(err).Warn("could not save visibility setting")
		}
	}

	// Guard the nearby fields: this runs on a frontend-call goroutine while
	// shutdown may touch the same fields on another.
	a.mu.Lock()
	stop := a.stopDiscovery
	if stop == nil || a.nearby == nil {
		a.mu.Unlock()
		return
	}
	// Becoming visible again bumps the generation so peers that still hold a
	// goodbye-suppression window for us accept the return announcement
	// immediately instead of waiting it out.
	if visible {
		a.nearbyGen++
		a.nearbyIdentity.Gen = a.nearbyGen
	}
	identity, nearby, emitState := a.nearbyIdentity, a.nearby, a.nearbyEmitState
	a.mu.Unlock()

	stop()
	newStop := startDiscovery(identity, nearby, emitState, visible)

	a.mu.Lock()
	a.stopDiscovery = newStop
	a.mu.Unlock()
}

// rememberLastPeer persists the most recent zero-code target, best-effort.
func (a *App) rememberLastPeer(name string) {
	path, err := settingsPath()
	if err != nil {
		return
	}
	if err := updateSettings(path, func(s *appSettings) { s.LastPeer = name }); err != nil {
		logrus.WithError(err).Warn("could not save last peer")
	}
}

// receiveExpectation is what an accepted nearby offer promised; the finished
// receive is checked against it because croc delivers whatever the sender put
// in the room — not necessarily what was offered.
type receiveExpectation struct {
	Names []string
	Size  int64
}

// acceptPeerTransfer runs after the user accepted an offer and the code
// arrived: start receiving into the remembered destination, keeping the
// offer's file list and size to verify the received content against.
func (a *App) acceptPeerTransfer(offer NearbyOffer, code string) {
	dest, err := a.GetDefaultDownloadPath()
	if err != nil {
		logrus.WithError(err).Error("cannot receive nearby transfer: no destination")
		return
	}
	expect := &receiveExpectation{Names: offer.Files, Size: offer.Size}
	if _, err := a.startReceive(code, dest, offer.SenderName, expect); err != nil {
		logrus.WithError(err).Error("could not start nearby receive")
	}
}

// SendToPeer offers files to a nearby device and, once accepted, sends them
// with an internally negotiated code — never shown to either user.
func (a *App) SendToPeer(peerID string, paths []string) (string, error) {
	return a.sendToPeer(peerID, paths, "")
}

// sendToPeer offers files to a nearby device. An empty code means generate a
// fresh one; a supplied code resumes a dropped peer transfer (the receiver
// continues from the partial it kept under that code).
func (a *App) sendToPeer(peerID string, paths []string, code string) (string, error) {
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

	go a.performPeerSend(transfer.ID, peer, paths, names, totalSize, code)

	return transfer.ID, nil
}

func (a *App) performPeerSend(id string, peer NearbyPeer, paths, names []string, totalSize int64, code string) {
	if code == "" {
		code = utils.GetRandomName()
	}

	// Open our croc room first, then offer: the receiver starts receiving
	// the moment the user accepts, and joining a room that doesn't exist
	// yet fails with "room not ready". The worker idles on the relay while
	// the human decides; decline/timeout kills it below.
	go a.performSendWithCode(id, paths, code)

	candidates := orderedCandidates(peer.Addrs, peer.Addr)
	logrus.Infof("offering to %s via candidates %v", peer.Name, candidates)
	answer, err := sendNearbyOffer(candidates, peer.Port, peer.Fingerprint, offerRequest{
		SenderName: a.deviceName,
		Files:      names,
		Size:       totalSize,
	}, code)

	abort := func(message string) {
		// Terminal state first: the worker's exit must not overwrite it, and
		// runWorkerJob kills any worker that registers after this point (so
		// there is no registration race to wait out).
		a.failTransfer(id, message)
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

// RespondToNearbyOffer resolves the incoming-offer prompt. The same prompt UI
// serves both croc nearby offers and LocalSend offers; the offer id matches at
// most one, the other call is a no-op.
func (a *App) RespondToNearbyOffer(offerID string, accept bool) {
	if a.nearbySrv != nil {
		a.nearbySrv.respond(offerID, accept)
	}
	a.resolveLocalSendOffer(offerID, accept)
}

// GetNearbyPeers returns the currently visible nearby devices.
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
	a.mu.Lock()
	stop := a.stopDiscovery
	srv := a.nearbySrv
	webRecv := a.webRecv
	a.webRecv = nil
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
	if srv != nil {
		srv.close()
	}
	if webRecv != nil {
		webRecv.close()
	}
	a.mu.Lock()
	ls := a.localSend
	a.localSend = nil
	kdeStop := a.kdeStop
	a.kdeStop = nil
	warpStop := a.warpStop
	a.warpStop = nil
	a.mu.Unlock()
	if ls != nil {
		ls.close()
	}
	if kdeStop != nil {
		kdeStop()
	}
	if warpStop != nil {
		warpStop()
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

// GetTransfers returns all transfers, newest first.
func (a *App) GetTransfers() []FileTransfer {
	return a.tm.snapshot()
}

// SendFile starts a code-based send of a single file.
func (a *App) SendFile(filePath string) (string, error) {
	return a.SendFiles([]string{filePath})
}

// SendFiles starts a code-based send and returns the transfer id; the
// shareable code arrives on the transfer:updated event once the room is open.
func (a *App) SendFiles(paths []string) (string, error) {
	return a.sendFilesWithCode(paths, "")
}

// sendFilesWithCode starts a code-flow send. An empty code means generate a
// fresh one; a supplied code is reused to resume a dropped transfer (the
// receiver re-enters the same code and continues from its preserved partial).
func (a *App) sendFilesWithCode(paths []string, code string) (string, error) {
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

	go a.performSend(transfer.ID, paths, code)

	return transfer.ID, nil
}

// ResendOutcome always carries a human-readable message so the frontend can
// give feedback for every case — success, "device gone", or missing files —
// without depending on promise-rejection semantics. NeedsConfirm means
// nothing started yet: the frontend must show the target's name and address
// and call ConfirmResend. That human check is the defense against a spoofed
// machine id (device identity comes from unauthenticated multicast, so a
// hostile LAN peer could otherwise claim the target's id and receive the
// files).
type ResendOutcome struct {
	Started      bool   `json:"started"`
	Message      string `json:"message"`
	NeedsConfirm bool   `json:"needsConfirm,omitempty"`
	PeerName     string `json:"peerName,omitempty"`
	PeerAddr     string `json:"peerAddr,omitempty"`
}

// ResendTransfer repeats a past send with the same source files. Code sends
// start immediately; peer sends return NeedsConfirm so the user verifies the
// target device first (see ResendOutcome).
func (a *App) ResendTransfer(id string) ResendOutcome {
	return a.resendTransfer(id, false)
}

// ConfirmResend actually starts a peer resend the user confirmed.
func (a *App) ConfirmResend(id string) ResendOutcome {
	return a.resendTransfer(id, true)
}

// resendTransfer resolves a repeat send. Peer sends re-target the same
// machine by its stable machine id (so a restart + rename still matches). If
// that machine is no longer nearby it starts nothing and says so — a doomed
// code send stuck on "waiting" forever is worse than a clear message.
// Missing source files abort with an explicit list rather than sending a
// silent partial.
func (a *App) resendTransfer(id string, confirmed bool) ResendOutcome {
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

	code, resume := resendCode(t)

	// Not a peer send — plain code resend (waiting for a code is the point).
	if t.Peer == "" {
		if _, err := a.sendFilesWithCode(t.Paths, code); err != nil {
			return ResendOutcome{Message: err.Error()}
		}
		if resume {
			return ResendOutcome{Started: true, Message: "Resuming — enter the same code on the other device to continue."}
		}
		return ResendOutcome{Started: true, Message: "Sending again with a new code."}
	}

	peerID, ok := a.findPeerForResend(t.PeerMachineID, t.Peer)
	if !ok {
		return ResendOutcome{Message: t.Peer + " isn't nearby anymore — open krokodyl there to send again."}
	}
	if !confirmed {
		// The machine-id match is a lookup, not authentication. Surface who
		// and where the files would go; the user confirms before anything is
		// offered.
		peer, found := a.nearby.get(peerID)
		if !found {
			return ResendOutcome{Message: t.Peer + " isn't nearby anymore — open krokodyl there to send again."}
		}
		return ResendOutcome{NeedsConfirm: true, PeerName: peer.Name, PeerAddr: peer.Addr}
	}
	if _, err := a.sendToPeer(peerID, t.Paths, code); err != nil {
		return ResendOutcome{Message: err.Error()}
	}
	if resume {
		return ResendOutcome{Started: true, Message: "Resuming transfer to " + t.Peer + "."}
	}
	return ResendOutcome{Started: true, Message: "Sending again to " + t.Peer + "."}
}

// resendCode decides whether repeating t is a resume of a dropped transfer —
// failed with its code preserved — in which case the same code is reused so
// the receiver continues from the partial it kept under that code.
func resendCode(t FileTransfer) (code string, resume bool) {
	if t.Status == FileTransferStatusError && t.ResumeCode != "" {
		return t.ResumeCode, true
	}
	return "", false
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

func (a *App) performSend(id string, paths []string, code string) {
	if code == "" {
		code = utils.GetRandomName()
	}
	// Code-flow sends surface the code so the user can share it; peer sends
	// keep it internal (performSendWithCode is called directly there).
	a.tm.update(id, func(t *FileTransfer) {
		t.Code = code
		t.Status = FileTransferStatusWaiting
	})
	a.performSendWithCode(id, paths, code)
}

func (a *App) performSendWithCode(id string, paths []string, code string) {
	// Keep the code so a dropped send can be resumed with the same one.
	a.tm.update(id, func(t *FileTransfer) { t.ResumeCode = code })

	cancelCh := a.registerCancel(id)
	defer a.unregisterCancel(id)

	job := workerJob{Mode: "send", Code: code, Paths: paths}
	ok := a.runRecoverableAttempts(id, cancelCh, func(n, basePct int) (int, string, error) {
		return a.runSendAttempt(id, job, connectGrace(n), basePct)
	})
	if !ok {
		return
	}

	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusCompleted
		t.Progress = 100
		t.Speed = 0
	})
}

// runSendAttempt runs one send worker. Displayed progress is offset by
// basePct (the best reached in earlier attempts) so a resume continues the
// bar instead of restarting at 0. Returns the peak overall % this attempt
// reached so the recovery loop can judge whether it made headway.
func (a *App) runSendAttempt(id string, job workerJob, grace time.Duration, basePct int) (int, string, error) {
	peak := basePct
	tracker := &stallTracker{}
	stopWatchdog := a.startStallWatchdog(id, tracker, grace)
	defer stopWatchdog()

	workerErrMsg, err := a.runWorkerJob(id, job, func(ev workerEvent) {
		switch ev.Type {
		case "files":
			a.tm.update(id, func(t *FileTransfer) {
				t.Files = ev.Files
				t.Size = ev.Size
			})
		case "progress":
			display := overallProgress(basePct, ev.Progress)
			if display > peak {
				peak = display
			}
			tracker.observe(ev.Sent, ev.Progress, ev.Sent > 0, time.Now())
			a.tm.update(id, func(t *FileTransfer) {
				t.Progress = display
				t.Speed = ev.Speed
				if ev.Sent > 0 {
					t.Status = FileTransferStatusSending
				}
			})
		}
	})
	return peak, workerErrMsg, err
}

// ReceiveFile starts a code-based receive into destinationPath.
func (a *App) ReceiveFile(code, destinationPath string) (string, error) {
	// Code receives carry no expectation: the user typed the code, there was
	// no offer to verify against.
	return a.startReceive(code, destinationPath, "", nil)
}

func (a *App) startReceive(code, destinationPath, peerName string, expect *receiveExpectation) (string, error) {
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

	if expect != nil {
		a.mu.Lock()
		a.expectations[transfer.ID] = expect
		a.mu.Unlock()
	}

	go a.performReceive(transfer.ID, code, destinationPath)

	return transfer.ID, nil
}

// popExpectation removes and returns the offer expectation for a transfer.
func (a *App) popExpectation(id string) *receiveExpectation {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp := a.expectations[id]
	delete(a.expectations, id)
	return exp
}

func (a *App) performReceive(id, code, destinationPath string) {
	cancelCh := a.registerCancel(id)
	defer a.unregisterCancel(id)

	// Staging inside the destination keeps it on the same volume (instant
	// final rename) AND is keyed by the code, so a retry with the same code
	// reuses the partial bytes and croc resumes the missing chunks.
	stagingDir := stagingDirForCode(destinationPath, code)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		a.failTransfer(id, fmt.Sprintf("cannot write to destination: %s", err))
		return
	}

	a.tm.update(id, func(t *FileTransfer) {
		t.Status = FileTransferStatusReceiving
		t.ResumeCode = code
	})
	if sp, err := settingsPath(); err == nil {
		recordPartial(sp, stagingDir, time.Now().Unix())
	}

	// Each attempt re-runs into the same code-derived staging dir, so a
	// reconnect resumes from the partial instead of restarting.
	job := workerJob{Mode: "receive", Code: code, StagingDir: stagingDir}
	ok := a.runRecoverableAttempts(id, cancelCh, func(n, basePct int) (int, string, error) {
		return a.runReceiveAttempt(id, job, connectGrace(n), basePct)
	})
	if !ok {
		a.popExpectation(id) // nothing to verify — the receive never finished
		// Cancelled → nothing to resume, drop the partial. Gave up → keep it
		// so a manual Send again can still try later.
		if t, ok := a.tm.get(id); ok && t.Status == FileTransferStatusCancelled {
			a.cleanupStaging(stagingDir)
		}
		return
	}

	a.finalizeReceive(id, stagingDir, destinationPath, cancelCh)
}

// runReceiveAttempt runs one receive worker into the (preserved) staging dir.
// Displayed progress is offset by basePct so a resume continues the bar.
func (a *App) runReceiveAttempt(id string, job workerJob, grace time.Duration, basePct int) (int, string, error) {
	peak := basePct
	tracker := &stallTracker{}
	stopWatchdog := a.startStallWatchdog(id, tracker, grace)
	defer stopWatchdog()

	workerErrMsg, err := a.runWorkerJob(id, job, func(ev workerEvent) {
		if ev.Type == "progress" {
			display := overallProgress(basePct, ev.Progress)
			if display > peak {
				peak = display
			}
			tracker.observe(ev.Sent, ev.Progress, true, time.Now())
			a.tm.update(id, func(t *FileTransfer) {
				t.Progress = display
				t.Speed = ev.Speed
				t.Size = ev.Size
				// Flip back from "reconnecting" once data flows again.
				t.Status = FileTransferStatusReceiving
			})
		}
	})
	return peak, workerErrMsg, err
}

// cleanupStaging removes a staging dir and stops tracking it as a resumable
// partial. Used when a transfer completes or is cancelled (not when it drops).
func (a *App) cleanupStaging(stagingDir string) {
	if err := os.RemoveAll(stagingDir); err != nil {
		logrus.WithError(err).Warnf("could not remove staging dir %s", stagingDir)
	}
	if sp, err := settingsPath(); err == nil {
		forgetPartial(sp, stagingDir)
	}
}

// finalizeReceive moves downloaded files from staging into the destination,
// prompting for overwrites, with relative paths preserved so nested folder
// structures arrive intact.
func (a *App) finalizeReceive(id, stagingDir, destinationPath string, cancelCh chan struct{}) {
	// Reached only after a fully successful croc receive — the partial is
	// complete, so always clean it up (files are moved out below).
	defer a.cleanupStaging(stagingDir)

	staged, err := listStagedFiles(stagingDir)
	if err != nil {
		a.failTransfer(id, err.Error())
		return
	}
	if len(staged) == 0 {
		a.failTransfer(id, "no files were received")
		return
	}

	// A nearby receive was consented to on the basis of a specific offer, but
	// croc delivers whatever the sender put in the room. If the content
	// differs from the offer, the user decides again before anything leaves
	// staging.
	if exp := a.popExpectation(id); exp != nil {
		if mismatch := describeOfferMismatch(staged, exp); mismatch != "" {
			if !a.promptReceiveMismatch(id, mismatch, cancelCh) {
				a.failTransfer(id, "discarded: "+mismatch)
				return // deferred cleanup removes the staged files
			}
		}
	}

	var moved []string
	var totalSize int64
	for _, sf := range staged {
		select {
		case <-cancelCh:
			// Cancelled mid-finalize: status is already cancelled; the
			// deferred cleanup removes the staging dir.
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

// emitEvent forwards to the Wails runtime unless there is no UI context
// (unit tests construct App without one). Events are advisory; dropping them
// without a webview is correct.
func (a *App) emitEvent(name string, payload ...interface{}) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, payload...)
}

// registerOverwritePrompt creates a fresh per-prompt response channel keyed
// by a unique prompt id. Keying by prompt — not transfer — means a stale
// response can never be consumed by a later prompt of the same transfer.
func (a *App) registerOverwritePrompt() (promptID string, ch chan string) {
	promptID = uuid.NewString()
	ch = make(chan string, 1)
	a.mu.Lock()
	a.overwriteResponses[promptID] = ch
	a.mu.Unlock()
	return promptID, ch
}

func (a *App) removeOverwritePrompt(promptID string) {
	a.mu.Lock()
	delete(a.overwriteResponses, promptID)
	a.mu.Unlock()
}

// resolveOverwrite delivers the user's answer to the prompt's channel.
// Unknown ids (answered, cancelled, or stale duplicates) are no-ops.
func (a *App) resolveOverwrite(promptID, response string) {
	a.mu.Lock()
	responseChan, ok := a.overwriteResponses[promptID]
	if ok {
		delete(a.overwriteResponses, promptID)
	}
	a.mu.Unlock()

	if ok {
		select {
		case responseChan <- response:
		default:
		}
	}
}

// promptOverwrite asks the frontend whether an existing file should be
// replaced and blocks until the user answers or the transfer is cancelled
// (cancel/shutdown count as "no").
func (a *App) promptOverwrite(id string, sf stagedFile, existing os.FileInfo, cancelCh chan struct{}) bool {
	promptID, responseChan := a.registerOverwritePrompt()
	defer a.removeOverwritePrompt(promptID)

	a.emitEvent(TransferEventOverwrite, OverwritePrompt{
		PromptID:   promptID,
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

// RespondToOverwrite resolves an overwrite prompt by its prompt id (from the
// OverwritePrompt payload). Late or duplicate responses are ignored. The
// verify prompt shares the same response plumbing.
func (a *App) RespondToOverwrite(promptID string, response string) {
	a.resolveOverwrite(promptID, response)
}

// offerSizeSlack tolerates legitimate overshoot before flagging a mismatch:
// folder sends understate the offered size (only top-level files are
// stat-summed), so only a substantial excess is suspicious.
const offerSizeSlack = 16 * 1024 * 1024 // bytes, on top of +25%

// describeOfferMismatch compares what croc actually delivered against what
// the accepted offer promised. Empty string means acceptable. Folder offers
// legitimately expand into many nested files, so the comparison is on
// top-level names (must all have been offered) and total size (bounded
// overshoot).
func describeOfferMismatch(staged []stagedFile, exp *receiveExpectation) string {
	offered := make(map[string]bool, len(exp.Names))
	for _, n := range exp.Names {
		offered[n] = true
	}

	var extras []string
	seen := make(map[string]bool)
	var total int64
	for _, sf := range staged {
		total += sf.Size
		top := sf.RelPath
		if i := strings.IndexByte(top, byte(filepath.Separator)); i >= 0 {
			top = top[:i]
		}
		if !offered[top] && !seen[top] {
			seen[top] = true
			extras = append(extras, top)
		}
	}

	if len(extras) > 0 {
		shown := extras
		if len(shown) > 5 {
			shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("+%d more", len(extras)-5))
		}
		return fmt.Sprintf("the sender delivered items that were not offered: %s", strings.Join(shown, ", "))
	}
	if limit := exp.Size + exp.Size/4 + offerSizeSlack; total > limit {
		return fmt.Sprintf("the sender delivered far more data (%d bytes) than offered (%d bytes)", total, exp.Size)
	}
	return ""
}

// promptReceiveMismatch asks the user whether to keep a receive whose content
// differs from the accepted offer. Cancel/shutdown count as "discard".
func (a *App) promptReceiveMismatch(id, detail string, cancelCh chan struct{}) bool {
	promptID, responseChan := a.registerOverwritePrompt()
	defer a.removeOverwritePrompt(promptID)

	logrus.Warnf("transfer %s: received content differs from the accepted offer: %s", id, detail)
	a.emitEvent(TransferEventVerify, VerifyPrompt{
		PromptID:   promptID,
		TransferID: id,
		Detail:     detail,
	})

	select {
	case response := <-responseChan:
		return response == "yes"
	case <-cancelCh:
		return false
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

// startStallWatchdog watches a transfer's byte movement and fails it if it
// freezes (e.g. Wi-Fi drops mid-transfer) instead of leaving the row stuck
// until croc's own socket timeout. Returns a stop function; safe to call once
// the transfer ends. failTransfer here wins because terminal state is final,
// so the worker's own later error becomes a no-op.
// startStallWatchdog kills a frozen worker so the attempt ends promptly
// instead of hanging on a dead socket. It does NOT mark the transfer failed —
// the recovery loop owns that decision (it may reconnect and resume). The
// killed worker surfaces as an attempt error the loop then handles.
//
// connectGrace bounds how long the attempt may sit with no movement at all
// (waiting to connect/reconnect). Once bytes start flowing the stall timeout
// takes over.
func (a *App) startStallWatchdog(id string, tracker *stallTracker, connectGrace time.Duration) func() {
	done := make(chan struct{})
	var once sync.Once

	go func() {
		start := time.Now()
		ticker := time.NewTicker(stallCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				stalled := false
				if tracker.isArmed() {
					stalled = tracker.stalled(now)
				} else {
					stalled = now.Sub(start) > connectGrace
				}
				if stalled {
					logrus.Infof("transfer %s attempt ended (no movement) — recovery will decide", id)
					a.killWorker(id)
					return
				}
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}

// sleepOrCancel waits d, returning false if the transfer is cancelled first.
func (a *App) sleepOrCancel(cancelCh chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-cancelCh:
		return false
	}
}

// runRecoverableAttempts re-runs attempt() — one worker run — until it
// succeeds, the user cancels, or the recovery budget is spent. Between failed
// attempts that still make progress it shows "reconnecting" and backs off.
// Returns true only on success.
func (a *App) runRecoverableAttempts(id string, cancelCh chan struct{}, attempt func(n, basePct int) (peakPct int, errMsg string, err error)) bool {
	budget := newRecoveryBudget()
	for n := 0; ; n++ {
		// Each attempt's worker reports progress only for the bytes it moves
		// this session; on a resume that restarts at 0. Offsetting by the
		// best progress so far makes the bar continue (85% -> 100%) instead
		// of dropping back to 0.
		peak, errMsg, err := attempt(n, budget.bestPct)
		if err == nil {
			return true
		}

		// Any terminal state is final — user cancel, a declined nearby offer
		// failing the transfer, or anything else that already decided the
		// outcome. Retrying a dead transfer only spawns pointless workers.
		if t, ok := a.tm.get(id); ok && t.Status.isTerminal() {
			return false
		}

		logrus.Infof("transfer %s attempt %d failed (peak %d%%): %s", id, n, peak, errMsg)
		if budget.record(peak) {
			a.failTransfer(id, "couldn't reconnect — the connection kept dropping")
			return false
		}

		a.tm.update(id, func(t *FileTransfer) {
			t.Status = FileTransferStatusReconnecting
			t.Speed = 0
		})
		if !a.sleepOrCancel(cancelCh, recoveryBackoffFn(n)) {
			return false // cancelled during backoff
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
		stdin.Close()
		return "failed to start transfer worker", err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
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

	// The transfer may have gone terminal (cancel, declined offer) in the
	// window before this worker registered — a kill issued then found nothing
	// to kill. Re-checking here closes that race from the other side.
	if t, ok := a.tm.get(id); ok && t.Status.isTerminal() {
		cmd.Process.Kill()
		cmd.Wait()
		return "transfer was cancelled", fmt.Errorf("transfer %s ended before its worker started", id)
	}

	if err := json.NewEncoder(stdin).Encode(job); err != nil {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return "failed to hand job to transfer worker", err
	}
	stdin.Close()

	errMsg, scanErr := scanWorkerEvents(stdout, onEvent)

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

// scanWorkerEvents consumes the worker's stdout event stream: JSON events go
// to onEvent, "error" events are captured as the worker's failure message,
// and malformed lines are logged and skipped (one bad line must not kill the
// transfer). Returns the captured error message and any stream read error.
func scanWorkerEvents(r io.Reader, onEvent func(workerEvent)) (errMsg string, scanErr error) {
	scanner := bufio.NewScanner(r)
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
	return errMsg, scanner.Err()
}

// SelectFile opens the native single-file picker.
func (a *App) SelectFile() (string, error) {
	selection, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to send",
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}

	return selection, nil
}

// SelectFiles opens the native multi-file picker.
func (a *App) SelectFiles() ([]string, error) {
	selection, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select files to send",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open file dialog: %w", err)
	}

	return selection, nil
}

// SelectDirectory opens the native folder picker.
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

// GetBuildStamp returns the build identifier so two machines can confirm they
// run the same build.
func (a *App) GetBuildStamp() string {
	return buildStamp
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
	err = updateSettings(path, func(s *appSettings) { s.LastDestination = destination })
	if err != nil {
		logrus.WithError(err).Warn("could not save settings")
	}
}
