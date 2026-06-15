<!-- Generated: 2026-06-15 (v0.17.3, feat/offline-bluetooth-discovery) | Files scanned: ~36 Go | Token estimate: ~1000 -->
# Backend (Go, package main)

Two local HTTPS servers (phone web-upload + LocalSend v2 API) but no public API. The "API" is `App` methods bound to the frontend by Wails + an internal stdin/stdout worker protocol.

## Wails-exposed App methods (app.go) → flow
```
SendFiles(paths[]) / SendFile / SendToPeer(peerId,paths)
   → runRecoverableAttempts → runWorkerJob(send) → worker croc-send → tm.update
   → SendToPeer where peer.Kind=="localsend": performLocalSendUpload (LocalSend
     HTTP prepare-upload+upload, NO croc/worker)
ReceiveFile(code,dir)
   → stagingDirForCode → runRecoverableAttempts → runWorkerJob(recv) → tm.update
CancelTransfer(id)        → popCancel + killWorker (Process.Kill)
ResendTransfer(id)        → guards → peer send returns NeedsConfirm+name+addr (no start)
ConfirmResend(id)         → user verified target → actually starts the peer resend
RespondToOverwrite(promptId,ans) → resolves overwrite AND verify prompts (shared plumbing)
RespondToNearbyOffer(...) → accept/decline/busy on croc-nearby AND LocalSend offers (id matches one)
StartPhoneReceive / StopPhoneReceive → QR + HTTPS upload server (always-on while visible)
FirewallNeedsFix / FixFirewall → Windows: detect+add inbound allow rule (elevated, one UAC)
RevealInExplorer(id)      → open the transfer's saved/sent file in the OS file manager
GetTransfers / ClearHistory / GetNearbyPeers / GetNearbyPrefs / SetNearbyVisible(bool)
SelectFile(s) / SelectDirectory / GetDefaultDownloadPath   # native dialogs
GetDeviceName / GetBuildStamp / GetOfflineGuidance
```

## Receive servers (always-on while visible — ensureReceiving/stopReceiving)
```
webreceive.go  HTTPS QR upload (ephemeral port, 128-bit token, mobile-first page)
localsend.go   LocalSend v2: multicast 224.0.0.167:53317 + HTTP API
               receive (info/register/prepare-upload/upload) AND send
               (performLocalSendUpload); UPPERCASE-fingerprint pin (pinFingerprint
               case-insensitive); peers learned from announce + /register
               (observeLocalSendPeer) into the shared peerRegistry (Kind=localsend)
both feed the SAME pipeline: discovery → nearby:offer consent → saveUploadedFile
gated adapters (off by default): kdeconnect.go, warpinator.go
```

## Worker protocol (worker.go ↔ app.go) — JSON lines
```
parent→worker (stdin, then closed):
  workerJob { mode:"send"|"receive", code, paths[]?, stagingDir? }
worker→parent (stdout, one JSON/line):
  workerEvent { type:"files"|"progress"|"done"|"error",
                files[], size, sent, progress(0-99), speed, message }
exit 0 = success, 1 = error. progress caps 99; "done" = 100.
```
Parent reads with 64KB-line / 1MB-max scanner. Bad JSON line → warn + skip.

## Event emission (Go → frontend, Wails runtime)
```
transfer:updated   FileTransfer copy on every state change
transfer:overwrite overwrite decision request (file conflict)
transfer:verify    received content differs from accepted offer (keep/discard)
nearby:updated     peer list changed
nearby:state       discovery available / unavailable
nearby:offer       incoming offer (sender name + addr)
transfer:cleared   history wiped
```
All prompts keyed by per-prompt UUID via registerOverwritePrompt/resolveOverwrite.
Emits go through a.emitEvent (nil-ctx safe for tests).

## Resilience (recovery.go, stall.go)
```
runRecoverableAttempts:
  loop attempt → runWorkerJob → on err: budget.record(peakPct)
                 give up if no new peak in N tries
  backoff = min(2^attempt s, 10s); status "reconnecting" between tries
  overallProgress(basePct, sessionPct) → bar continues, no 0% reset
startStallWatchdog: KILL-ONLY. connectGrace (pre-first-byte) vs stall timeout (armed).
```

## State manager (transfers.go)
`transferManager`: mutex map, `add` prepends (newest-first), `snapshot`/`reset`, `update` no-op if terminal, emits struct COPIES.

## Key files
```
app.go       ~1430 GUI orchestration, Wails API, recovery glue, verify, history, firewall/reveal methods
worker.go    238  child process: croc send/recv + 200ms progress poller
discovery.go ~430 multicast advertise/listen, liveness, hide/unhide Gen, NearbyPeer.Kind, localSendPeerTTL
nearby.go    438  TLS 1.3 control server/client, FP pinning, offer/accept, per-source backoff
localsend.go ~860 LocalSend v2 receive+send, multicastInterfaces (skips virtual), observeLocalSendPeer
webreceive.go ~560 HTTPS QR upload server, mobile-first uploadPageHTML, saveUploadedFile, safeUploadName
staging.go   208  stagingDirForCode, validateRelPath (+Win reserved/trailing), atomic copyFile
settings.go  ~215 settings.json via updateSettings, MachineID + DeviceName (ensureDeviceName), sweep
selfcert.go  ~110 persistentCertificate (self-cert.pem, stable fingerprint across restarts)
recovery.go 84 · stall.go 69 · transfers.go ~140 · netaddr.go 103 · history.go 76 · names.go 64 · main.go 137
build-tagged (per-OS): firewall_{windows,other}.go · reveal_{windows,other}.go · reuseaddr_{windows,other}.go
gated (off by default): kdeconnect.go (krokodyl_kdeconnect) · warpinator.go (krokodyl_warpinator) · nearbyble_on.go (krokodyl_ble)
```
Test injection points: recoveryBackoffFn, offerPromptWait (package vars).
Unit tests must never reach runWorkerJob (would spawn the test binary).
