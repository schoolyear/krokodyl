<!-- Generated: 2026-06-12 | Files scanned: ~50 | Token estimate: ~750 -->
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
   ▼ peer list
nearby.go     TLS control channel (cert-FP pinned)
   sender ─ offer(files,size) ─► receiver ─ accept/decline ─► code handoff ─► croc
netaddr.go    rank real-LAN > virtual; sender tries candidates real-first (4s each)
```

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

See: [backend.md](backend.md) · [frontend.md](frontend.md) · [data.md](data.md) · [dependencies.md](dependencies.md)
