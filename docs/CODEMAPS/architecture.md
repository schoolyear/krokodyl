<!-- Generated: 2026-06-13 (v0.17.3) | Files scanned: ~50 | Token estimate: ~800 -->
# Architecture

Type: single cross-platform desktop app. Wails v2 (Go backend + Svelte 5 webview frontend, one binary). P2P transport = croc.

## Process model — one binary, two modes
```
krokodyl(.exe)
  main.go: isWorkerProcess(os.Args)?
   ├─ no  → wails.Run(App)            # GUI process (long-lived)
   └─ yes → runWorker() → exit        # --transfer-worker child (one transfer, then dies)
```
Flag check happens BEFORE wails.Run() (Wails never returns).

## Transfer data flow
```
UI (App.svelte)
  └─ Wails binding → App.SendFiles/ReceiveFile (app.go)
       └─ runRecoverableAttempts (recovery.go)   # retry loop + budget
            └─ runWorkerJob (app.go)  ──spawn──►  worker subprocess (worker.go)
                 stdin:  JSON workerJob                 │ croc send/recv
                 stdout: JSON workerEvent ◄─────────────┘ (progress @200ms)
            └─ startStallWatchdog (stall.go)            # kill-only on no-bytes
       └─ tm.update() (transfers.go)  → Wails EventsEmit("transfer:updated")
  └─ EventsOn("transfer:updated"|"nearby:*"|"transfer:overwrite") → re-render
```

## Nearby (LAN, zero-code) flow
```
discovery.go  multicast :42791  (advertise id/name/ctrlPort/certFP/addrs, 2s)
   ▼ peer list (names sanitized: control chars + BiDi stripped)
nearby.go     TLS 1.3 control channel (cert-FP pinned; per-source backoff
   sender ─ offer(files,size) ─► receiver ─ accept/decline ─► code handoff ─► croc
   ▼ after croc finishes: received content CHECKED against the offer
app.go        describeOfferMismatch → transfer:verify keep/discard prompt
netaddr.go    rank real-LAN > virtual; sender tries candidates real-first (4s each)
```
Trust model: pinning authenticates the channel, not the identity — human
confirmation is the backstop (offer prompt, verify prompt, resend confirm).

## Service boundaries (Go, package main, repo root)
| Concern | Files |
|---|---|
| Entry / mode dispatch | main.go |
| GUI orchestration, Wails API | app.go |
| Transfer worker (child) | worker.go, cmd/guiharness (stderr-bug harness) |
| Resilience | recovery.go, stall.go |
| Transfer state | transfers.go |
| Resume staging | staging.go |
| LAN discovery + control | discovery.go, nearby.go, netaddr.go, names.go |
| Persistence | settings.go, history.go |
| Frontend | frontend/src |

## Key invariants
- Terminal transfer states immutable (late events can't resurrect).
- `stagingDirForCode` deterministic → resume; changing it orphans partials.
- Worker `cmd.Stderr = nil` (Windows GUI-subsystem dead-handle fix).
- macOS ad-hoc codesign only (no notarization — hard constraint).
- User prompts keyed by per-prompt UUID (stale answers can't decide later prompts).
- Peer resend is two-phase: `ResendTransfer` → NeedsConfirm → `ConfirmResend`.
- Peer display strings pass `sanitizeDisplayName` at decode; settings writes
  go through `updateSettings` (settingsMu, atomic tmp+rename).

See: [backend.md](backend.md) · [frontend.md](frontend.md) · [data.md](data.md) · [dependencies.md](dependencies.md)
