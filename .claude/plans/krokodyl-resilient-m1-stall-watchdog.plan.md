# Plan: Stall Watchdog — no more frozen transfers

**Source PRD**: .claude/prds/krokodyl-resilient-transfers.prd.md
**Selected Milestone**: 1 — Stall watchdog
**Complexity**: Small–Medium

## Summary
While a transfer is actively sending/receiving, watch its byte movement; if nothing moves for ~30 s, fail it with a clear "connection lost" message and kill its worker, on each end independently — so a Wi-Fi drop ends in a prompt, honest failure instead of a row frozen forever. Any real byte movement resets the timer, so slow-but-moving transfers are never touched. (Resume is Milestone 2, separate plan.)

## How it hooks in
The worker already emits `progress` events carrying `Sent` (bytes) and `Progress` (%). The parent consumes them in `performSendWithCode`/`performReceive` via the `onEvent` callback while `runWorkerJob` blocks. A watchdog goroutine started alongside watches a shared tracker; on stall it calls `failTransfer` (which wins because terminal state is final — the later worker-death `failTransfer` becomes a no-op) and `killWorker`. The watchdog only arms once the transfer is actually moving (status sending/receiving), so the long legitimate "waiting for receiver" phase is left to the existing `senderWaitTimeout`.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Stop-func goroutine | `worker.go` `startProgressPoller` (`done` chan + `sync.Once` stop) | Watchdog goroutine returns a `stop()`; ticker-driven |
| Terminal-state-final | `transfers.go` `update` ignores terminal transfers | Watchdog's `failTransfer` is authoritative; later worker-error fail is a no-op |
| Fail + kill | `app.go` `failTransfer`, `killWorker` | Same helpers; same message-then-status flow |
| Active statuses | `App.svelte` `ACTIVE_STATUSES`; `FileTransferStatus*` consts | Arm only on sending/receiving |
| Pure-logic + tests | `transfers_test.go`, `discovery_test.go` table-driven `-race` | Extract the stall decision into a testable struct |

## Files to Change
| File | Action | Why |
|---|---|---|
| `stall.go` | CREATE | `stallTracker`: pure logic — record movement, report stalled(now); no goroutines/IO so it's unit-testable |
| `stall_test.go` | CREATE | Table-driven: arms on movement, resets on byte/percent increase, trips only after the window, never trips while only "waiting" |
| `app.go` | UPDATE | Start/stop a watchdog around `runWorkerJob` in `performSendWithCode` and `performReceive`; feed it from the existing `onEvent`; stall → `failTransfer("connection lost…")` + `killWorker`; add `stallTimeout`/`stallCheckInterval` consts |

No worker, protocol, frontend, or locale changes — the failed row already renders its message; the watchdog just produces a terminal `error` state with a clear reason.

## Tasks

### Task 1: stallTracker (pure logic) + tests
- **Action**: `stallTracker` with `observe(sent int64, progress int, active bool, now time.Time)` and `stalled(now time.Time) bool`. Arms when `active` first seen; records `lastMovement` when `sent` increases or `progress` increases; `stalled` true when armed and `now - lastMovement > stallTimeout`. Not armed → never stalled (covers the waiting phase). Guard against counter resets (treat only increases as movement).
- **Mirror**: table-driven `_test.go` style.
- **Validate**: `go test -race ./...` — arms-on-move, resets-on-progress, trips-after-window, never-trips-unarmed, slow-but-moving-never-trips.

### Task 2: Wire watchdog into send + receive
- **Action**: In `performSendWithCode` and `performReceive`, create a tracker (mutex-guarded) and a watchdog goroutine (ticker every `stallCheckInterval` ≈ 5 s) before `runWorkerJob`; stop it after. The existing `onEvent` updates the tracker on `progress` (and flips active on sending/receiving). On `stalled`: `failTransfer(id, "connection lost — transfer stalled")` + `killWorker(id)` + stop the watchdog. Confirm the post-`runWorkerJob` `failTransfer` is then a no-op (terminal). Constants: `stallTimeout = 30 * time.Second`, `stallCheckInterval = 5 * time.Second`.
- **Mirror**: `startProgressPoller` stop-func; `failTransfer`/`killWorker`.
- **Validate**: `go build && go vet && go test -race`; manual: start a real transfer, drop Wi-Fi → row goes to error with the message within ~30 s on **both** ends; restore-then-slow → keeps going, no false fail.

### Task 3: Regression
- **Action**: GUI-harness loopback (fast transfer completes, watchdog never trips); relaunch instances; quick code review of the goroutine lifecycle (no leak: watchdog always stopped via defer; no double-fail).
- **Validate**: harness green; `-race` clean (watchdog + onEvent share the tracker under a mutex).

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback (completes, no false stall)
# manual: pull Wi-Fi mid-transfer each side → error w/ message ≤~30s, no frozen row
# manual: throttled link, large file → completes, watchdog never trips
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Watchdog false-trips a slow transfer | Medium | Reset on any byte/percent increase; 30 s window; movement check is "increase", not "rate" |
| Receive side reports no `Sent` movement (only `%`) | Medium | Tracker treats either `Sent` increase or `Progress` increase as movement |
| Data race on the shared tracker | Medium | Mutex-guard the tracker; `go test -race` gate |
| Watchdog goroutine leak | Low | `defer stop()`; `sync.Once` close like `startProgressPoller` |
| Double `failTransfer` (watchdog + worker death) | Low | Terminal state is final in `transferManager.update` — first wins, second is a no-op |

## Acceptance
- [ ] Mid-transfer network drop → terminal `error` with a clear message ≤~30 s, on both ends; no permanent frozen row
- [ ] Slow-but-moving transfer never trips the watchdog
- [ ] `-race` suite + GUI-harness loopback green; no goroutine leak
- [ ] No worker/protocol/frontend changes; existing flows unaffected
