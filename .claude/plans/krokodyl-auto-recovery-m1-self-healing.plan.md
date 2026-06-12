# Plan: Self-Healing Transfers — auto-reconnect & resume on drops

**Source PRD**: .claude/prds/krokodyl-auto-recovery.prd.md
**Selected Milestone**: 1 — Self-healing transfers
**Complexity**: Medium–Large

## Summary
Wrap each transfer in a recovery loop: on a non-user drop (stall/EOF/connection lost), automatically re-run the worker with the **same code** — the receiver continuing from its preserved partial — showing a "Reconnecting…" state instead of erroring. Keep retrying while attempts make forward progress; give up only after a bounded number of **no-progress** attempts. Both ends retry independently and rendezvous in croc's relay room (the waiting side holds the room). The pieces (stall watchdog, partial preservation, code reuse) already exist; this milestone makes recovery automatic and repeated.

## Key behavioral changes
- The stall watchdog stops being terminal: it **kills the stalled worker** so the attempt ends, but the **recovery loop decides** retry-vs-fail (today it calls `failTransfer` directly).
- A failed attempt → if its peak progress beat the best-so-far, reset the no-progress counter; else increment it. Give up after `maxNoProgressAttempts`.
- New non-terminal status `reconnecting` drives a calm recovering UI; cancel works throughout.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Worker run + events | `app.go` `performSendWithCode` / `performReceive` (`runWorkerJob` + `onEvent`) | Loop around `runWorkerJob`; track per-attempt peak progress |
| Stop-func goroutine | `app.go` `startStallWatchdog` | Keep, but kill-only (no `failTransfer`) |
| Cancel plumbing | `app.go` `registerCancel`/`popCancel`/`cancelCh` (receive) | Extend to send so backoff is cancellable on both sides |
| Terminal-state-final | `transfers.go` `update` ignores terminal | `reconnecting` is non-terminal; final give-up sets `error` |
| Pure + tested | `stall.go`, `netaddr.go` pure helpers + tests | `recovery.go`: budget + backoff, unit-tested |
| Status consts | `transfers.go` `FileTransferStatus*` | Add `FileTransferStatusReconnecting` |
| Frontend active states | `App.svelte` `ACTIVE_STATUSES`, `getStatusInfo` | Add `reconnecting` (spinner, "Reconnecting…", cancel shown) |
| i18n | 6 locale files | `status.reconnecting` + recovering copy |

## Files to Change
| File | Action | Why |
|---|---|---|
| `recovery.go` | CREATE | `recoveryBudget` (record attempt peak → keep/giveup) + `recoveryBackoff(attempt)` — pure, testable |
| `recovery_test.go` | CREATE | Give-up only after N no-progress; resets on progress; backoff increases & caps |
| `transfers.go` | UPDATE | `FileTransferStatusReconnecting` (non-terminal) |
| `app.go` | UPDATE | Recovery loop in `performSendWithCode` + `performReceive`; watchdog kill-only; register cancel for send; per-attempt peak tracking; reconnecting status + final give-up message; receive reuses the code-derived staging each attempt |
| `frontend/src/App.svelte` | UPDATE | `reconnecting` in `ACTIVE_STATUSES`; `getStatusInfo` icon/color; show attempt hint; cancel during recovery |
| `frontend/src/locales/*.json` (6) | UPDATE | `status.reconnecting` |

## Tasks
1. **recovery.go (pure)**: `recoveryBudget{bestPct, noProgress, maxNoProgress}` with `record(attemptPeakPct int) (giveUp bool)` — if peak > best → best=peak, noProgress=0, keep; else noProgress++ and giveUp when > max. `recoveryBackoff(attempt int) time.Duration` — increasing, capped (~2s→10s). Tests cover both.
2. **Watchdog kill-only**: `startStallWatchdog` kills the worker on stall but no longer `failTransfer`s — the loop owns the outcome. (Stall just ends the attempt.)
3. **Send recovery loop** (`performSendWithCode`): register a cancel channel; loop attempts; each attempt tracks peak `Progress`; on `err` (non-cancel) consult `recoveryBudget`; if keep → set `reconnecting`, `recoveryBackoff` sleep (cancellable), re-run same job (same code); if giveup → `failTransfer("couldn't reconnect — connection kept dropping")`; on success → completed.
3b. **Receive recovery loop** (`performReceive`): same loop; re-run with the same code-derived staging dir each attempt (partial preserved across attempts → croc resumes); success → `finalizeReceive` (cleans staging); cancel → cleanup.
4. **Frontend**: render `reconnecting` as an active, calm state (spinner, "Reconnecting…", optional attempt count), cancel button shown; status color (amber/primary, not red). Strings ×6.
5. **Validate**: race tests; harness loopback (no spurious retries on a clean transfer); manual flaky-link test (toggle network repeatedly mid-transfer) → transfer auto-resumes and completes; kill peer entirely → gives up after the budget with a clear message; cancel during reconnecting works.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback: completes in one attempt, no retry triggered
# manual (2 machines): drop the link 3-5x mid-transfer → auto-reconnects, progress climbs, finishes
# manual: kill the peer → "couldn't reconnect" after N no-progress attempts
# manual: cancel while "Reconnecting…" → stops cleanly
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Two ends never overlap to rendezvous | Medium | Waiting side holds the relay room (sender wait 5 min); similar backoff on both; the first to retry waits for the other |
| Retry storm on a hard-down link | Medium | No-progress budget + increasing backoff; clear give-up |
| Watchdog kill-only leaves a path with no terminal state | Low | Loop always ends in completed/cancelled/error; final give-up sets error |
| Resume corrupts across many reconnects | Low–Medium | croc hashing + final byte-verify; clean restart if a resume can't proceed |
| "Reconnecting" hides a permanent failure | Low | Bounded attempts + attempt count shown + cancel always available |
| Double-handling watchdog vs loop | Medium | Single rule: watchdog only kills; loop is the sole decider of retry/fail |

## Acceptance
- [ ] A transfer dropped repeatedly mid-flight auto-reconnects, resumes from its partial, and completes with no manual action
- [ ] Progress accumulates across reconnects (not 0% each time); finished file byte-identical to source
- [ ] UI shows "Reconnecting…" during recovery; error only after bounded no-progress give-up
- [ ] Cancel works during recovery; user-cancel never auto-retries; "device gone"/missing-files still fail clearly
- [ ] `-race` + harness green; clean transfers never trigger a retry
