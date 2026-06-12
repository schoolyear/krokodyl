# Krokodyl — Reliability & AirDrop-grade UX

## Problem
Krokodyl (desktop GUI for croc-based P2P file transfer) is currently unreliable in core flows and effectively unusable on macOS. Transfers can appear stuck forever, folder receives fail, progress is never shown, and the macOS release is blocked at install/launch. Until this is fixed, the app cannot be recommended to anyone — especially the non-technical recipients it exists for.

## Evidence
Grounded in code analysis of the current `main` branch (2026-06-11). Behavior-level findings:

**Reliability (all platforms)**
- Starting a second transfer detaches the first transfer's status updates from the UI; the first transfer can show "waiting/sending" forever and the Send button stays locked (stale reference into a reallocated transfer list + unsynchronized cross-goroutine writes — a confirmed data race pattern).
- Receiving a folder with nested subdirectories fails: received files are looked up by base name in the staging area root, so nested files are "not found" and the transfer errors out.
- Moving received files from the staging area to the chosen destination uses an operation that fails across drives/volumes (e.g. temp on C:, destination on D:; or tmpfs → home on Linux/macOS), erroring the whole transfer after a successful download.
- Receiving changes the process-wide working directory; two concurrent receives corrupt each other's destination.
- Progress is never reported during transfer: the bar jumps 0% → 100%. No transfer can be cancelled. A sender with no receiver waits forever with no timeout and no way out.
- The overwrite-confirmation flow builds its "diff" by reading both full files into memory (multi-GB file → out-of-memory) and then only reports "File content differs."
- If the user never answers an overwrite prompt, the transfer goroutine blocks forever.
- Transfer history is lost on every app restart.

**Maintainer-reported failures (2026-06-11)**
- Non-local transfers (different networks, via relay) do not work even with a correct code. Root cause unconfirmed — candidates: outdated croc library, relay connectivity/options, engine bugs above. `TBD — needs repro + upstream croc changelog review during /plan`.
- Large file transfers fail. Root cause unconfirmed — candidates: cross-volume move after staging (doubles disk usage and fails across drives), memory-loading overwrite diff, croc version. `TBD — needs repro with multi-GB file`.
- On macOS, file transfer itself did not work at all (beyond the install/launch blockade below).
- Typing a receive code auto-triggers the receive as soon as the pattern matches — fires while still typing and re-fires on edits after a full code is entered. Confirmed in code: every keystroke re-tests the pattern and calls receive again.
- Dependencies are stale; maintainer wants everything current, especially the croc transfer library (several releases behind; upstream fixes may cover relay and big-file behavior).

**macOS (user report: "doesn't work at all")**
- The release pipeline ships an unsigned, un-notarized app archive. On modern macOS, Gatekeeper blocks it ("damaged" / "unverified developer") — for a normal user this is indistinguishable from a dead app. No workaround instructions are published. **Maintainer decision: no Apple Developer account, notarization will not be pursued — the unsigned path must be made as smooth as possible instead.**
- The app configures no macOS application menu, so Cmd+V paste does not work in input fields — users cannot paste the receive code even if they get the app running.
- The release workflow has a fallback path that ships a bare binary instead of an app bundle, which cannot be launched by double-click.
- CI pins an older Go toolchain than the module requires — fragile, may silently produce broken or failed mac builds.

**UX gap vs. AirDrop benchmark**
- No drag-and-drop to send; only a file-picker dialog, single file at a time.
- Sending requires reading a code aloud / messaging it out-of-band; no QR, no copy-affordance prominence, no device discovery on local network (croc supports local relay; UI never surfaces it).
- Sender is locked to one transfer at a time; UI blocks until the receiver finishes.
- Window is hard-capped at 1000×800 and cannot be maximized.
- User report (2026-06-11): "krokodyl is broken right now … on mac it doesn't work at all."

Field telemetry/analytics: none exist. Failure-rate numbers below are targets, baseline is `TBD — needs validation via instrumented error logging in first fixed release`.

## Users
- **Primary**: A person at a desktop/laptop (Windows, macOS, Linux) who wants to send a file or folder to another person's machine without cloud upload, accounts, or setup — and the often non-technical person receiving it. Trigger: "I need to get this file to you right now."
- **Not for**: Server-to-server automation, CLI power users (croc itself serves them), mobile users (out of scope for now).

## Hypothesis
We believe **a macOS-launchable build plus a reliable transfer engine (incl. big files and cross-network relay transfers, on current upstream libraries) plus a redesigned zero-friction send/receive experience (drag-and-drop, live progress, cancel, prominent code sharing — AirDrop-inspired, original design)** will **make first-try transfers succeed for non-technical users** on **all three desktop platforms**.
We'll know we're right when **two first-time users complete a file transfer between platforms (incl. macOS) in under 60 seconds without instructions, and ≥95% of attempted transfers complete or fail with an actionable error message**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| macOS launch success (download → app opens, unsigned) | 100% with at most one documented step (e.g. right-click → Open) on a clean machine | Manual test matrix on macOS 13–15, Intel + Apple Silicon |
| macOS transfer success (send + receive) | Same as other platforms | Manual cross-platform test matrix incl. macOS↔Windows |
| Transfer completion or actionable-error rate | ≥95% | Instrumented error logging (to add); manual test matrix: file, folder, nested folder, cross-drive, overwrite, concurrent |
| Cross-network (relay) transfer with code | Works between two different networks | Manual test: two machines, separate networks (e.g. LAN + hotspot) |
| Large file transfer | ≥5 GB file completes with live progress | Manual test on each platform |
| Code entry auto-receive | Fires exactly once, only on complete valid code | Manual test: type slowly, paste, edit after entry |
| Time for first-time pair to complete transfer | <60 s, no instructions | Moderated hallway test, n≥3 pairs — `TBD — needs validation via usability test` |
| Stuck transfers (no terminal state, no cancel path) | 0 | Manual test matrix incl. receiver-never-arrives, app-closed-mid-prompt |
| Progress feedback during transfer | Visible, monotonic | Manual verification on ≥100 MB file |

## Scope
**MVP** — the minimum to test the hypothesis:
1. macOS build that a normal user can download, open, and use **without notarization** (unsigned path: at most one documented step to open; paste works; transfers actually work on macOS).
2. Transfer engine correctness: concurrent transfers stay independent and update correctly; folders (incl. nested) receive intact; destination on any drive works; **big files (multi-GB) complete**; **cross-network relay transfers with a code work**; no stuck states (timeouts, cancel, prompt abandonment handled); errors surface with a human-readable reason; code-entry auto-receive fires exactly once on a complete valid code.
3. All dependencies current — especially the croc transfer library — and the release pipeline builds against them.
4. AirDrop-grade core flow on a **redesigned UI**: visually polished, AirDrop-inspired feel without copying Apple's design; drag-and-drop to send, multi-file selection, live progress with speed, one-tap code copy, cancel button, sensible defaults (auto-suggest Downloads, remember last destination).

**Out of scope**
- Apple Developer signing / notarization — maintainer decision: no account, not wanted. Unsigned distribution with smooth first-open guidance instead.
- Mobile apps / web receiver — different platform investment; revisit after desktop is trustworthy.
- Local device discovery & zero-code sending ("true AirDrop") — ~~deferred~~ now its own initiative: see [krokodyl-nearby-devices.prd.md](krokodyl-nearby-devices.prd.md).
- Self-hosted relay configuration UI — power-user feature; defaults suffice for the hypothesis.
- Transfer encryption changes — croc's existing PAKE-based security is retained as-is.
- Replicating AirDrop's visual design — inspiration only; krokodyl keeps its own identity.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Mac alive | A macOS user can download, open (unsigned, ≤1 documented step), and actually transfer files; paste and dialogs work; release pipeline reliably produces this artifact | in-progress | [.claude/plans/krokodyl-m1-mac-alive.plan.md](../plans/krokodyl-m1-mac-alive.plan.md) |
| 2 | Transfers trustworthy | Current upstream libraries (croc first); any file/folder transfer — including multi-GB and cross-network via relay — either completes with visible progress or fails fast with a clear, actionable message; nothing gets stuck; concurrent transfers don't interfere; auto-receive fires once on complete code only | in-progress | [.claude/plans/krokodyl-m2-transfers-trustworthy.plan.md](../plans/krokodyl-m2-transfers-trustworthy.plan.md) |
| 3 | AirDrop-grade flow on new UI | Redesigned, polished interface (AirDrop-inspired, original); send = drag file onto window (or pick several); receive = paste code, watch live progress; cancel anytime; code copy is one click | in-progress | [.claude/plans/krokodyl-m3-airdrop-flow.plan.md](../plans/krokodyl-m3-airdrop-flow.plan.md) |
| 4 | Polish & trust | History survives restart; window resizes freely; all 6 locales complete; a11y issues fixed; usability test passes <60 s target | in-progress | (implemented in loop with M3) |

### Status notes (2026-06-11)
**All four milestones are code-complete, reviewed (2 dedicated review passes, 1 CRITICAL + 3 HIGH found and fixed), and validated locally**: `go test -race` green (24 tests), svelte-check 0/0, Windows build boots, real loopback transfer between two worker processes succeeded on croc v10.4.4, redesigned UI visually verified in both themes, locale parity script confirms all 6 languages complete.

**Outstanding — requires maintainer hardware/networks (cannot be done from this machine):**
- M1: macOS smoke test (unzip → right-click Open → paste → transfer); pipeline run via `workflow_dispatch` after push
- M2: regression matrix — cross-network relay transfer, ≥5 GB file, second-drive destination, concurrent receives, cancel mid-transfer, Mac↔Win
- M3: live drag-and-drop check on Windows/macOS (code path verified, OS-level drop needs a hand)
- M4: moderated <60 s usability test

**Findings recorded:** local (same-machine) transfers work on croc v10.4.4 with relay defaults from `models.*` + IPv6 relay configured; cross-network root cause confirmation still pending the two-network test.

**Defect found in maintainer two-instance test (2026-06-11, screenshot):** 215 MB send failed instantly with "write /dev/stderr: The handle is invalid". Root cause: GUI-subsystem parent has no console, so its stderr handle is invalid; that handle was inherited by the transfer worker, and the transfer library's progress output to stderr aborted the send. Side effect: GUI-launched app lost all file logging (stderr written before the log file in a fail-fast multi-writer). Earlier loopback validation ran workers from a terminal (valid stderr) and therefore missed it. Fix + GUI-launched re-test required; lesson recorded: transfer validation must run from a GUI-launched parent, never a terminal.

**Defect FIXED and verified (2026-06-11):** worker no longer inherits the parent's stderr handle (gets the null device); parent logging made fail-safe (log file written first, console writes best-effort). Verified authentically with a new GUI-subsystem test harness (`cmd/guiharness`, invalid std handles — the double-click environment): real 200 MB transfer between two worker processes completed byte-exact (209,715,200 bytes) with live progress on both sides. GUI-launched parent logging confirmed working in the log file. The harness is kept in-repo for future regression runs of the full matrix.

## Open Questions
- [x] ~~Apple Developer Program membership for signing + notarization?~~ **Resolved: no — maintainer has no account and doesn't want one. Unsigned distribution with smooth first-open guidance is the requirement.**
- [ ] Root cause of cross-network (relay) transfer failures: outdated croc, relay options, or engine bugs? `TBD — needs repro + croc changelog review during /plan`
- [ ] Root cause of big-file failures: cross-volume move, staging copy doubling disk usage, memory-loading diff, or croc version? `TBD — needs repro with multi-GB file`
- [ ] Was the macOS transfer failure the same engine bugs or something macOS-specific (temp dir volume, permissions)? `TBD — needs test on macOS hardware after milestone 1`
- [ ] Is local-network discovery (zero-code, true AirDrop parity) a must for the maintainer's vision, or is the code flow acceptable long-term? Affects post-MVP roadmap only.
- [ ] Should send history/codes be treated as sensitive (codes grant one-time file access)? Affects whether history persistence stores codes.
- [ ] Baseline failure rate unknown — accept manual test matrix as MVP gate, add opt-in error reporting later?

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Unsigned macOS app: Gatekeeper friction is permanent (accepted) | Certain | Medium | Make the one-step open path bulletproof: clear in-download instructions, README, release notes; test on every macOS version in matrix |
| Public croc relay (default) outage or throttling | Medium | High (all non-LAN transfers fail) | Surface relay errors clearly; keep croc local-network path enabled; relay fallback list |
| Dependency big-bang update (croc, Wails, frontend) breaks working behavior | Medium | Medium | Update first in milestone 2, before engine rework; regression matrix before/after; race detector in CI |
| Cross-network/big-file failures persist after dependency update | Medium | High (core promise unmet) | Treat as milestone 2 gate; reproduce before and after update to isolate cause |
| UI overhaul lands too close to AirDrop's design | Low | Medium (identity/legal taste) | Inspiration-not-replica rule in scope; own palette, iconography, layout |
| Upstream croc library API limits progress/cancel hooks | Low–Medium | Medium (milestone 3 degraded) | Validate hook feasibility first in milestone 2; fall back to coarse progress if needed |
| Single maintainer bandwidth | Medium | Medium | Milestones independently shippable; 1 and 2 deliver value alone |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
