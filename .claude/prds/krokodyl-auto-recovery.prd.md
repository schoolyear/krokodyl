# Krokodyl — Automatic Recovery (self-healing transfers on flaky links)

## Problem
On an unstable connection a transfer drops repeatedly (every ~30–60 s), and today each drop ends the transfer: it errors, and the user must manually click "Send again" — which they shouldn't have to, and which doesn't reliably continue from where it stopped. A large file over a flaky link becomes impossible: it never survives long enough in one shot, and manual retries lose time and patience. The app should recover by itself — reconnect and continue from the partial — until the transfer finishes or the link is truly dead.

## Evidence
- Maintainer log + screenshots (2026-06-12): repeated mid-transfer failures at 39%, 52%, 76% — mix of `connection lost — transfer stalled` and `receiving failed: EOF` — on the same file between the same two machines. "it happend again twice, 2 different errors… no able to restart and continue from there, no automatic handling if this happens.. please make it."
- Grounded: the stall watchdog (shipped) detects the drop and the manual resume (shipped) preserves the receiver's partial and reuses the code — so the *pieces* for continuation exist; what's missing is doing it **automatically** and **repeatedly** without user clicks.
- Grounded: a re-run of both sides with the same code rendezvouses in croc's relay room, and croc resumes the missing chunks from the preserved partial — so recovery is "re-run with same code", no new protocol.

## Users
- **Primary**: Anyone moving a large file over a real-world flaky link — Wi-Fi that drops, VM↔host virtual networks, congested or roaming connections. The maintainer's two-machine setup is the live example.
- **Not for**: User-cancelled transfers (stay cancelled), or a peer that is genuinely gone/offline (fail clearly after bounded attempts — can't recover what isn't there).

## Hypothesis
We believe **automatically re-attempting a dropped transfer — reconnecting with the same code and continuing from the preserved partial, with backoff and a give-up-only-when-no-progress policy** will **let large transfers complete across repeated drops without any manual clicks** for **users on flaky links**.
We'll know we're right when **a file that drops several times mid-transfer still finishes on its own (progress accumulating across reconnects), the UI shows "Reconnecting…" rather than a dead error during recovery, and a truly dead link gives up cleanly after a bounded number of no-progress attempts**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Survives repeated drops | A large file that drops 3–5× completes without manual action | Manual: flaky link / toggle network mid-transfer repeatedly |
| Progress accumulates | Each reconnect resumes near the last percentage, not from 0% | Manual: watch % across reconnects |
| No manual clicks | Recovery needs zero user interaction while progress is being made | Manual |
| Clear recovering state | UI shows "Reconnecting (n)…" during recovery, error only after giving up | Manual |
| Bounded give-up | A dead link stops after N no-progress attempts with a clear final message | Manual: kill the peer entirely |
| No regression | Normal transfers, cancel, and "device gone" still behave correctly | Manual + harness |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Auto-retry on drop: when a transfer fails for a non-user reason (stall/EOF/connection lost — not cancel, not "files missing"), automatically re-attempt with the same code, the receiver continuing from its preserved partial. Both ends retry independently and rendezvous in the relay room.
2. Smart give-up: keep retrying as long as attempts make forward progress (bytes advance); stop after a bounded number of consecutive **no-progress** attempts, with a clear final error. Backoff between attempts.
3. Recovering UI: the transfer shows a "Reconnecting…" / retrying state (not a red error) while recovery is in progress; the error appears only when recovery is abandoned. Cancel works during recovery.

**Out of scope**
- Recovering a user-cancelled transfer — cancel is final.
- Infinite retries — bounded by the no-progress policy.
- Recovering when the destination/source files are gone — fail clearly.
- Changing the transport (still croc relay/local); no new NAT traversal beyond Milestone-1 address work.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Self-healing transfers | A dropped transfer auto-reconnects and resumes from its partial, repeatedly, with a "Reconnecting…" state, until it completes or gives up after bounded no-progress attempts | in-progress | [.claude/plans/krokodyl-auto-recovery-m1-self-healing.plan.md](../plans/krokodyl-auto-recovery-m1-self-healing.plan.md) |

## Open Questions
- [ ] **Retry budget**: how many consecutive no-progress attempts before giving up, and what backoff (fixed vs increasing)? `TBD — /plan; default ~5 no-progress attempts, short increasing backoff`
- [ ] **Both-ends timing**: each side retries independently — do their backoff windows need to overlap to rendezvous in the relay room, or does croc's room persistence cover it? `TBD — needs croc relay-room lifetime check`
- [ ] **Progress definition for "made progress"**: more bytes received than the previous attempt's peak? `Assumed yes — compare against the best percentage seen so far`
- [ ] **Sender vs receiver roles**: does the sender auto-re-send (re-open its room) and the receiver auto-re-receive, both on their own timers? Or does one drive? `TBD — /plan; likely both retry independently, code is the rendezvous`
- [ ] **Nearby vs code flow**: for nearby, does recovery re-use the control-channel offer or just re-run croc with the stored code? `Assumed: just re-run croc with the stored code; no re-offer needed`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Retry storms / busy-loop on a hard-down link | Medium | Medium | No-progress cap + backoff; give up cleanly |
| Two ends never overlap to reconnect | Medium | High | Align retry windows; rely on croc relay room persistence; tune backoff so both are present |
| Resume corrupts on repeated partial reuse | Low–Medium | High | croc hashing + byte-verify on completion; fall back to clean restart if a resume can't proceed |
| "Reconnecting" masks a real permanent failure too long | Medium | Low | Bounded attempts; show attempt count; cancel always available |
| Auto-retry fights the stall watchdog (double handling) | Medium | Medium | Watchdog failure becomes the *trigger* for auto-retry, not a terminal error, until the budget is spent |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
