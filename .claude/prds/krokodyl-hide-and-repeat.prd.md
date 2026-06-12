# Krokodyl — Instant Hide & Reliable Repeat-Transfer

## Problem
Two nearby-flow rough edges remain. (1) Turning visibility **off** doesn't stick: the device vanishes from other machines instantly, then **pops back for ~4–6 seconds** before finally disappearing — so "hidden" looks broken. (2) Repeating a transfer the user just did ("send the same file to the same machine again", the core dev-loop need) is not dependable from history: the affordance is easy to miss, and it can fail to re-target the intended machine, with no graceful story when that machine is no longer around.

## Evidence
- Maintainer report (2026-06-12): pressing hidden — "I do instantly go away but I pop back up again and it takes around 4/6 seconds to go away"; and "I'm missing in history that I can do the exact same action again, so [send] same file to same machine etc. must have handling if other device no longer there".
- Grounded in this codebase: going invisible fires a short "goodbye" burst (peer removed instantly) but the just-stopped announce loop's in-flight/late normal announcements race it — the other side removes the peer, then re-adds it from a late announce, then waits out the liveness TTL (~5 s) before it finally expires. That timing matches the reported 4–6 s bounce.
- Grounded: a one-click resend already exists on completed send rows, but (a) it is a small, easily-missed icon and (b) it re-targets a nearby device by its **display name**, and display names were just changed to a **fresh random name each launch** — so after the other machine restarts, the name no longer matches and the resend can't find it. There is currently no stable per-machine identity to match on.

## Users
- **Primary**: Multi-machine / multi-window developers (the maintainer's own loop) who hide a window from the picker and who repeatedly resend the same artifact to the same other machine; and same-room users who want a clean "go invisible".
- **Not for**: Cross-internet repeats — code flow already covers remote; this is about the local nearby flow.

## Hypothesis
We believe **making "hidden" propagate within ~1 second and stay hidden, plus a clear, dependable "repeat this transfer" in history that re-targets the same machine when it's present and explains itself clearly when it isn't** will **remove the last friction in the hide and repeat-send loops** for **multi-machine local users**.
We'll know we're right when **toggling hidden removes the device from other screens within ~1 s with no reappearance, and repeating a past send is an obvious one-action step that reaches the same machine (even after that machine restarted) or fails with a clear, actionable message when the machine is gone**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Hide propagation | Device disappears from other devices ≤1 s after pressing hidden | Manual, two machines / two instances |
| No bounce-back | After hide, the device does **not** reappear at all | Manual, watch the other screen for 10 s |
| Repeat discoverability | A user can find and trigger "repeat" on a past send without guidance | Manual / hallway check |
| Repeat reaches same machine | Repeat re-targets the original machine even if it restarted (new random name) | Manual: send, restart receiver, repeat |
| Graceful absence | If the target machine is gone, repeat gives a clear, actionable message (offer code fallback) — never a silent failure | Manual: send, close receiver, repeat |
| No regression | Code-flow repeats and normal sends unaffected | Manual + existing harness |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Instant, sticky hide: turning visibility off removes this device from every other device within ~1 s and it stays gone (goodbye is authoritative; late/in-flight announcements can't resurrect a just-departed peer).
2. Dependable repeat-from-history: a clear, obvious "repeat" action on past sends that re-sends the same file(s); reaches the **same machine** even across that machine's restarts (matching on something stable, not the per-launch display name); and when the machine is no longer reachable, surfaces a clear message and an easy fallback (e.g. resend via code) instead of failing silently.

**Out of scope**
- Auto-retry / queueing a repeat until the machine comes back — explicit user action only for now.
- Re-receiving (codes are single-use by protocol) — repeat applies to sends.
- Persistent "trusted device" pairing — separate concern.
- Changing the random readable-name feature itself — names stay friendly/random; matching just shouldn't *depend* on them.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Hide sticks instantly | Pressing hidden removes the device from others within ~1 s with zero reappearance | in-progress | [.claude/plans/krokodyl-hide-and-repeat.plan.md](../plans/krokodyl-hide-and-repeat.plan.md) |
| 2 | Reliable repeat | Obvious one-action repeat of a past send that reaches the same machine across restarts, with clear handling + code fallback when the machine is gone | in-progress | [.claude/plans/krokodyl-hide-and-repeat.plan.md](../plans/krokodyl-hide-and-repeat.plan.md) |

## Open Questions
- [ ] **Stable machine identity for re-targeting**: per-run peer IDs rotate and display names are now random per launch — what stable key should "same machine" match on? Options: a persisted per-install machine ID, the LAN IP (semi-stable via DHCP), or the OS hostname kept as a hidden anchor alongside the friendly name. `TBD — decide at /plan; leaning a persisted per-install machine ID surfaced in discovery`
- [ ] **Repeat affordance**: keep the inline ↻ but make it a labelled "Send again" button, or add a more prominent repeat entry point? `Assumed: labelled, obvious control on the row`
- [ ] **Hide authority mechanism**: is a longer/repeated goodbye burst enough, or should receivers suppress re-adds for a short window after a goodbye? `TBD — /plan; correctness over cleverness`
- [ ] Should "device gone" offer to **immediately switch to the code flow** (show a code) or just message and stop? `Assumed: offer the code fallback`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Goodbye still races announces on lossy Wi-Fi (UDP unreliable) | Medium | Medium | Make goodbye authoritative on the receiver side (suppress re-add window), not just sender-side timing |
| Adding a stable machine ID leaks more identifying info on shared LANs | Low–Medium | Low | Keep it an opaque random per-install token, not hostname/MAC; only used for matching |
| Repeat re-targets the wrong machine after IP reuse (DHCP) | Low | Medium | Prefer an explicit per-install token over IP; confirm via the existing accept prompt (name + IP shown) |
| Changing hide/announce timing regresses discovery liveness | Medium | Medium | Re-run two-instance appear/depart tests; keep TTL as crash fallback |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
