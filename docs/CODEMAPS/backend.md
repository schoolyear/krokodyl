<!-- Generated: 2026-06-12 | Files scanned: ~27 Go | Token estimate: ~850 -->
# Backend (Go, package main)

No HTTP server. The "API" is `App` methods bound to the frontend by Wails + an internal stdin/stdout worker protocol.

## Wails-exposed App methods (app.go) → flow
```
SendFiles(paths[]) / SendFile / SendToPeer(peerId,paths)
   → runRecoverableAttempts → runWorkerJob(send) → worker croc-send → tm.update
ReceiveFile(code,dir)
   → stagingDirForCode → runRecoverableAttempts → runWorkerJob(recv) → tm.update
CancelTransfer(id)        → popCancel + killWorker (Process.Kill)
ResendTransfer(id)        → lookup history → verify files → re-send (peer by MachineID, fallback name)
RespondToOverwrite(id,ans)→ unblocks worker finalize (answers "transfer:overwrite")
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
nearby:updated     peer list changed
nearby:state       discovery available / unavailable
nearby:offer       incoming offer (sender name + addr)
transfer:cleared   history wiped
```

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
app.go      1187  GUI orchestration, Wails API, recovery glue, history persist
worker.go    238  child process: croc send/recv + 200ms progress poller
discovery.go 405  multicast advertise/listen, liveness, hide/unhide Gen
nearby.go    377  TLS control server/client, fingerprint pinning, offer/accept
staging.go   156  deterministic stagingDirForCode, validateRelPath, moveStagedFile (EXDEV copy fallback)
settings.go  159  settings.json, MachineID, partial sweep
recovery.go   80 · stall.go 69 · transfers.go 133 · netaddr.go 101 · history.go 74 · names.go 41 · main.go 137
```
