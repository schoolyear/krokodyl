# Krokodyl — Resilient Transfers (stall detection + resume)

## Problem
A transfer that loses the network mid-flight has no graceful handling. Once it's running, nothing watches for a stall — if Wi-Fi drops, the row freezes at a percentage with speed 0 and stays there until croc's own socket timeout finally errors (can be minutes, sometimes effectively never), and the two ends can come to rest at different states with no clean resolution. Worse, a dropped transfer is lost entirely: the partial bytes are discarded, so the only option is to restart from 0% — painful for the large files krokodyl is meant for.

## Evidence
- Maintainer observation (2026-06-12): "when the wifi quickly drops out or is slow it will be stuck and desynced?" — confirmed against the code.
- Grounded in this codebase: the only transfer timeout (`senderWaitTimeout`) fires **only while status is still "waiting"** for a receiver; once a transfer flips to sending/receiving there is **no stall watchdog**. The progress poller keeps emitting the last percentage with speed 0; the worker blocks in croc on the dead socket until croc's TCP timeout.
- Grounded: each receive stages into a fresh `<dest>/.krokodyl-partial-<id>` that is **deleted on failure** (`defer os.RemoveAll`), so a dropped transfer leaves nothing to resume from.
- Grounded: croc v10.4.4 already supports resume — `utils.MissingChunks` requests only the chunks the receiver still lacks (resume path around `croc.go:1615` / prompt at `1936`), and with our `NoPrompt`+`Overwrite` options it proceeds automatically **when the partial bytes are still on disk**.
- The sender 47% / receiver 22% mismatch the maintainer saw is **expected** (independent progress meters), not the bug; the bug is the no-stall-handling + no-resume.

## Users
- **Primary**: Anyone on real-world Wi-Fi (drops, congestion, sleep/wake, roaming between APs) transferring large files — the maintainer's two-machine loop and same-room hand-offs alike.
- **Not for**: Deliberate cancel (already handled) or a receiver that never existed (already handled by the waiting timeout).

## Hypothesis
We believe **a stall watchdog that fails a frozen transfer promptly and clearly on both ends, plus resume that continues a dropped transfer from where it stopped instead of restarting**, will **make transfers survive flaky networks** for **real-world Wi-Fi users moving large files**.
We'll know we're right when **a mid-transfer network drop ends both ends in a clear failed/recoverable state within ~30 s (never a permanent frozen row), and retrying a dropped large-file transfer resumes near where it left off rather than from 0%**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| No permanent freeze | A dropped transfer reaches a terminal/recoverable state ≤~30 s after bytes stop, on both ends | Manual: pull Wi-Fi mid-transfer on each side |
| Slow ≠ failed | A slow-but-moving transfer never trips the watchdog | Manual: throttled link, large file completes |
| Clear feedback | The stalled/failed row says what happened (connection lost), not a silent freeze | Manual |
| Resume works | Retrying a dropped ≥1 GB transfer continues from roughly the dropped point, not 0% | Manual: drop at ~50%, retry, observe start point |
| Resume integrity | A resumed file is byte-identical to the source | Manual: hash compare after resume |
| No regression | Clean transfers, cancel, and the existing nearby/code flows behave as before | Manual + existing harness |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Stall watchdog: while a transfer is actively sending/receiving, track byte movement; if no progress for a tunable window (~30 s) the transfer fails with a clear "connection lost / stalled" message and its worker is stopped. Runs independently on each end, so neither hangs. Any real byte movement resets it (slow links are fine).
2. Resume: preserve a dropped transfer's partial bytes instead of discarding them, and on retry continue the same transfer (croc resumes the missing chunks) rather than starting over; verify the finished file matches the source; clean up abandoned partials so they don't accumulate.

**Out of scope**
- Automatic retry/reconnect loops — retry stays a user action (Send again) for now; auto-retry is a later option.
- Mid-transfer relay failover or multipath — rely on croc's transport.
- Resuming across an app restart of the *sender* — start with same-session resume; cross-restart resume is a stretch goal.
- Changing the progress-meter semantics (independent per-side meters stay).

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Stall watchdog | A frozen transfer fails clearly on both ends within ~30 s; slow-but-moving transfers are untouched | complete | [.claude/plans/krokodyl-resilient-m1-stall-watchdog.plan.md](../plans/krokodyl-resilient-m1-stall-watchdog.plan.md) — validated on real cross-machine drop (error at 87%) |
| 2 | Resume | A dropped transfer's partial is preserved and a retry continues from where it stopped, byte-verified, with abandoned partials cleaned up | in-progress | [.claude/plans/krokodyl-resilient-m2-resume.plan.md](../plans/krokodyl-resilient-m2-resume.plan.md) |

## Open Questions
- [ ] **Stall window**: ~30 s default — fixed, or scaled to recent throughput (a 1 KB/s link legitimately moves little)? `TBD — /plan; default fixed 30s, reset on any byte increase`
- [ ] **Resume trigger**: does the existing "Send again" become the resume entry point, or a distinct "Resume" affordance on a failed/stalled row? `Assumed: failed-with-partial rows offer Resume; Send again stays for completed`
- [ ] **Partial retention**: where/how long are partial staging dirs kept, and when are they swept (age cap, on success, on explicit clear)? `TBD — /plan; needs a retention + cleanup policy so disk doesn't fill`
- [ ] **Resume identity**: resume needs the same code + same staging bytes; codes are per-send — must the original code be retained with the partial for resume to work? `TBD — /plan; likely persist code+staging together for resumable transfers`
- [ ] **Both-ends coordination**: each side watchdogs independently — acceptable that one may fail a few seconds before the other? `Assumed yes; independent failure is fine as long as neither hangs`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Watchdog kills a legitimately slow transfer | Medium | High (false failures) | Reset on any byte movement; generous window; consider throughput-scaled window |
| Retained partials fill the disk | Medium | Medium | Retention cap + sweep on success/age/clear; surface in UI |
| Resume corrupts the file (chunk-range bug) | Low–Medium | High | Rely on croc's hashing; byte-verify after resume; fall back to full resend on mismatch |
| Preserving partials leaks half-received files in the destination area | Low | Medium | Keep partials in the hidden staging dir, never the final path, until verified complete |
| croc resume needs the original code which we currently discard | High | Medium | Persist code + staging for resumable transfers (per open question) |
| Worker hangs even after kill on a wedged socket | Low | Medium | Kill is process-level (already used for cancel); OS reaps the socket |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
