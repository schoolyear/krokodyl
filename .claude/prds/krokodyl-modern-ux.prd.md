# Krokodyl v2 — Adaptive, Modern, OS-Native Experience

## Problem
Krokodyl now transfers reliably, but the experience is "good utility", not "best-in-class". The layout is tuned for one window shape and degrades on small, narrow, short, or very large screens. The design is clean but conventional (standard titlebar, flat panels). And the app ignores the conveniences a desktop OS offers — no notifications when a transfer finishes in the background, no jump from history to the received file, no awareness of a copied code. Every one of these is friction AirDrop users never feel.

## Evidence
- Maintainer request (2026-06-12): "more responsive for different resolutions and smaller screens, even phones; entirely remake the design; quality-of-life improvements; use all OS features; best app to work with, like AirDrop."
- Current window minimum is 520×500 — phone-width (~360px) window shapes clip the layout. `TBD — confirm exact break points via resize pass`
- Transfer completion is only visible inside the window; minimized app gives zero signal a 5-minute transfer finished. Observed behavior of current build.
- History entries are dead ends: no "open file", "show in folder", or "clear" actions.
- Receiving requires manual tab switch + focus + paste even when a croc code is already on the clipboard.

## Users
- **Primary**: Desktop users (Windows/macOS/Linux) moving files between their own machines or to another person — often multitasking, app minimized mid-transfer; includes users on small laptops, split-screen/snapped narrow windows, and 4K displays.
- **Not for**: Phone users as a native app — the current framework ships desktop binaries only. Phone-*shaped* windows are in scope; iOS/Android apps are not (see Open Questions).

## Hypothesis
We believe **an adaptive layout (320px-width to 4K, height-aware), a remade modern interface (custom titlebar, depth/material, refined motion), and OS-native conveniences (completion notifications, open-from-history, clipboard code detection, keyboard-first flow, remembered window state)** will **remove the remaining friction between "I want this file there" and "done"** for **desktop users**.
We'll know we're right when **every core flow works at every window size without clipping or dead space, background transfers announce themselves, and a received file is open on screen ≤2 clicks after completion**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Layout integrity across sizes | No clipping/overlap/dead-end scroll at 320, 480, 768, 1024, 1440, 2560 px widths and ≤500 px heights | Resize pass + screenshots per breakpoint, both themes |
| Background completion awareness | OS notification on complete/error when window unfocused/minimized | Manual: minimize during transfer |
| File reachable from history | Received file opens or is revealed in ≤2 clicks | Manual click-count |
| Clipboard-assisted receive | Code on clipboard → receive starts with ≤1 click after opening Receive | Manual: copy code, switch app |
| Keyboard-only flow | Send and receive completable without mouse (tab/enter/shortcuts) | Manual keyboard pass |
| Window state | Size/position restored across restarts | Manual restart check |
| Design quality bar | Passes anti-template checklist (hierarchy, depth, motion, intentional themes) | Design review against checklist |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Adaptive layout: 320 px-wide through 4K, short-height handling, lowered window minimums, high-DPI sanity.
2. Visual remake v2: custom titlebar (frameless), platform backdrop/material where available, refined spacing/type/motion system, both themes re-tuned; identity (croc green, original look) retained, not replaced.
3. OS conveniences: native completion/error notifications (when unfocused), "open file" + "show in folder" from history entries, clipboard code detection with one-tap receive, window size/position persistence, keyboard shortcuts for the core loop.
4. Quality-of-life pack: clear-history action, auto-focus the right input per tab, copy feedback polish, respects reduced-motion, tooltips on icon-only controls.

**Out of scope**
- Native iOS/Android apps — framework is desktop-only; separate initiative if ever.
- Browser/web receiver for phones — transfer protocol has no browser implementation; revisit only with upstream support.
- System tray / background agent mode — framework lacks first-class tray support in the shipped major version; revisit on framework upgrade.
- Explorer/Finder context-menu integration ("Send with krokodyl") — installer/registry work, separate initiative.
- Auto-update mechanism — distribution stays manual download for now.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Fits every screen | All flows usable 320 px→4K and short heights; lowered minimum window; no clipping in any locale, both themes | complete | [.claude/plans/krokodyl-v2-m1-fits-every-screen.plan.md](../plans/krokodyl-v2-m1-fits-every-screen.plan.md) |
| 2 | Remade shell | Frameless custom titlebar, platform material/backdrop, refined motion/spacing/type; design review passes anti-template checklist | in-progress | [.claude/plans/krokodyl-v2-m2-remade-shell.plan.md](../plans/krokodyl-v2-m2-remade-shell.plan.md) |
| 3 | OS-native conveniences | Notifications when unfocused; open/reveal from history; clipboard code one-tap receive; window state remembered; keyboard-first core loop | pending | — |
| 4 | QoL pack | Clear history, smart focus, tooltips, reduced-motion support, copy polish | pending | — |

## Open Questions
- [ ] "Even phones" — acceptable as phone-*shaped* desktop windows only? Native mobile would mean a different framework and a rewrite. `TBD — maintainer decision; assumed window-shapes-only for now`
- [ ] Frameless titlebar on Linux: framework support is weaker there — accept standard titlebar on Linux if needed? `TBD — verify during /plan`
- [ ] Notifications without a notification framework: per-OS shell mechanisms vary in reliability (esp. unsigned apps on macOS). Acceptable best-effort? `TBD — verify per-OS during implementation`
- [ ] Clipboard detection privacy: only check clipboard on window focus + Receive tab, never continuously? `Assumed yes — privacy-first default`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Frameless window regressions (drag, resize, snap, mac traffic lights) | Medium | High | Per-OS manual matrix; fall back to native titlebar per-platform via config |
| Backdrop/material APIs differ per OS version | Medium | Medium | Feature-detect; solid theme fallback |
| Notification mechanisms flaky for unsigned apps (esp. macOS) | Medium | Medium | Best-effort + in-app toast always; document limitation |
| Redesign #2 destabilizes just-fixed flows | Medium | High | Keep M2-engine and flow logic untouched; visual layer only; regression pass incl. GUI harness |
| Scope creep ("etc...") | High | Medium | MVP list above is the contract; new ideas go to backlog section of PRD |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
