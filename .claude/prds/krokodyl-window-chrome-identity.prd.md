# Krokodyl — Native Window Chrome, Clear History & Readable Identity

## Problem
The custom frameless titlebar shipped in the modern-UX work behaves unlike a real Windows window: the controls sit on the right mixed in with app actions, the draggable area is cramped and shares space with content, double-click-to-maximize and other native expectations aren't reliable, and the page scrollbar overlays the title region. Separately, transfer history can only grow — there is no way to clear it — and every device shows up under its raw OS hostname (`LAPTOP-24W12341`), which is unreadable and cannot tell two krokodyl windows on the same machine apart.

## Evidence
- Maintainer request (2026-06-12): window buttons "should always be top left on windows and have its own bar … double click maximizes, minimizes like normal behaviour … like actual windows buttons", "the scrollbar should not overlay it", "missing button to clear history", and a request for a "random human readable name" identifier instead of `laptop-24w12341` that can also "identify individual krokodyl windows".
- Current build: window controls render on the right of a shared brand/controls row (custom TitleBar from the remade-shell milestone), no dedicated chrome bar, and the global `::-webkit-scrollbar` styling applies over the full viewport including the titlebar row.
- Current build: history list has cancel/resend per row but no list-level clear action; `saveHistory` persists up to 50 entries with no user-facing reset.
- Current build: nearby device identity uses `os.Hostname()` verbatim; two instances on one machine share the same name and are only distinguishable by IP in the accept prompt.

## Users
- **Primary**: Windows desktop users who expect native window behavior (drag bar, double-click maximize, conventional control buttons) and a tidy history; multi-machine / multi-window users (the maintainer's own dev setup) who need to tell devices and individual windows apart at a glance.
- **Not for**: macOS/Linux window-control layout changes — those keep their native chrome (mac traffic lights, Linux native titlebar) unchanged from prior work.

## Hypothesis
We believe **a dedicated native-feeling window bar (conventional min/max/close buttons with correct hit-targets, full-width drag region, double-click maximize, scrollbar that never overlaps it), a clear-history action, and a friendly auto-generated device name** will **make krokodyl feel like a finished native app and make devices/windows instantly identifiable** for **Windows desktop and multi-window users**.
We'll know we're right when **the window behaves indistinguishably from a standard Windows window for move/maximize/minimize/close, the scrollbar never crosses the title bar, history can be cleared in one action, and every instance shows a readable name that disambiguates even two windows on one machine**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Native window behavior | Drag, double-click-maximize, snap, minimize, close all work like a standard Windows window | Manual on Windows 11 |
| Scrollbar isolation | Page scrollbar never overlaps the title bar at any window height/content length | Manual resize + long-history check |
| Button hit-targets | Controls clickable across their full visual area; hover/active states like native | Manual click test incl. edges |
| Clear history | History emptied (UI + persisted file) in one action with confirm | Manual; restart confirms persistence |
| Readable identity | Every instance has a human-readable name; two windows on one host show distinct names | Manual: two instances side by side |
| Identity stability | A given window keeps its name for its lifetime (and optionally across restarts) | Manual restart check — see Open Questions |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Dedicated Windows window bar: its own row reserved for window chrome, conventional min/max/close buttons with native-style appearance and full hit-targets, full-width drag region, double-click-to-maximize, standard minimize/restore/close behavior.
2. Scrollbar correctness: page scroll never renders over the title bar (title bar outside the scroll region, or scroll confined to content).
3. Clear-history action: a visible control that empties history (with a confirmation), clearing both the on-screen list and the persisted store.
4. Readable device identity: generate a friendly random name (e.g. adjective-animal style) at startup, shown as the device's nearby name and capable of distinguishing individual krokodyl windows; raw hostname still available as secondary detail where useful.

**Out of scope**
- Changing macOS / Linux window-control placement or style — they keep native chrome.
- User-editable / custom device names — auto-generated only for now (revisit later).
- Per-history-item delete — only bulk clear in this round.
- Title-bar theming beyond matching the existing app theme.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Native window bar | Windows gets a dedicated chrome bar: conventional buttons, full drag region, double-click maximize, native behaviors; scrollbar never overlaps it | in-progress | [.claude/plans/krokodyl-window-chrome-identity.plan.md](../plans/krokodyl-window-chrome-identity.plan.md) |
| 2 | Clear history | One-click (confirmed) clear that empties the list and the persisted history | in-progress | [.claude/plans/krokodyl-window-chrome-identity.plan.md](../plans/krokodyl-window-chrome-identity.plan.md) |
| 3 | Readable identity | Friendly auto-generated name per instance, disambiguating windows even on one machine; surfaced as the nearby name | in-progress | [.claude/plans/krokodyl-window-chrome-identity.plan.md](../plans/krokodyl-window-chrome-identity.plan.md) |

## Open Questions
- [ ] Button **position**: the request says "top left", but the Windows convention is top-right (min/max/close). Honor the literal request (left) or follow OS convention (right)? `TBD — confirm at /plan; default assumption: follow the explicit "top left" request unless corrected`
- [ ] Identity **persistence**: should a window's readable name persist across restarts (stored per-install) or be fresh each launch? Fresh-each-launch best distinguishes "individual windows"; persistent best identifies "a machine". `TBD — needs maintainer decision; leaning fresh-per-process with hostname as the stable machine anchor`
- [ ] Should the readable name **replace** or **augment** the hostname in the nearby list and accept prompt? `Assumed augment: readable name primary, hostname/IP as secondary detail`
- [ ] Clear-history **confirmation**: modal confirm vs. undo toast? `Assumed modal confirm (destructive, persisted)`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Frameless drag region vs. button clicks conflict (drag swallows clicks) | Medium | Medium | Explicit no-drag on controls; manual edge-click test |
| Reserving a chrome bar row eats vertical space on short windows | Medium | Low | Compact bar height; fold into existing short-height rules |
| Double-click maximize unreliable on frameless Wails window | Low–Medium | Medium | Verify Wails drag-region double-click; manual fallback button always present |
| Readable name collision (two instances roll the same name) | Low | Low | Append a short suffix / seed from process to keep distinct |
| Scrollbar fix changes layout (content scroll container) regresses responsive work | Medium | Medium | Re-run the responsive screenshot matrix from the modern-UX M1 work |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
