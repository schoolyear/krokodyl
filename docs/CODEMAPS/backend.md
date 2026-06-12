<!-- Generated: 2026-06-13 (v0.17.3) | Files scanned: ~28 Go | Token estimate: ~900 -->
# Backend (Go, package main)

No HTTP server. The "API" is `App` methods bound to the frontend by Wails + an internal stdin/stdout worker protocol.

## Wails-exposed App methods (app.go) → flow
```
SendFiles(paths[]) / SendFile / SendToPeer(peerId,paths)
   → runRecoverableAttempts → runWorkerJob(send) → worker croc-send → tm.update
ReceiveFile(code,dir)
   → stagingDirForCode → runRecoverableAttempts → runWorkerJob(recv) → tm.update
CancelTransfer(id)        → popCancel + killWorker (Process.Kill)
ResendTransfer(id)        → guards → peer send returns NeedsConfirm+name+addr (no start)
ConfirmResend(id)         → user verified target → actually starts the peer resend
RespondToOverwrite(promptId,ans) → resolves overwrite AND verify prompts (shared plumbing)
RespondToNearbyOffer(...) → accept/decline/busy on TLS control channel
GetTransfers / ClearHistory / GetNearbyPeers / GetNearbyPrefs / SetNearbyVisible(bool)
SelectFile(s) / SelectDirectory / GetDefaultDownloadPath   # native dialogs
GetDeviceName / GetBuildStamp
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
app.go      1394  GUI orchestration, Wails API, recovery glue, offer-mismatch verify, history persist
worker.go    238  child process: croc send/recv + 200ms progress poller
discovery.go 408  multicast advertise/listen, liveness, hide/unhide Gen, name sanitize
nearby.go    438  TLS 1.3 control server/client, FP pinning, offer/accept, per-source backoff
staging.go   208  stagingDirForCode, validateRelPath (+Win reserved/trailing), atomic copyFile
settings.go  196  settings.json via updateSettings (settingsMu, atomic, 0o600), MachineID, sweep
recovery.go   84 · stall.go 69 · transfers.go 133 · netaddr.go 103 · history.go 76 · names.go 64 (sanitizeDisplayName) · main.go 137
```
Test injection points: recoveryBackoffFn, offerPromptWait (package vars).
Unit tests must never reach runWorkerJob (would spawn the test binary).
