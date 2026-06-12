# Plan: Send Again + Trust Polish (milestones 3 & 4 combined)

**Source PRD**: .claude/prds/krokodyl-nearby-devices.prd.md
**Selected Milestones**: 3 — Send again; 4 — Trust & polish
**Complexity**: Medium

## Summary
M3: send-history entries remember their source paths (persisted — the dev loop must survive restarts) and grow a one-click "send again": same files, same flow (device offer if it was a peer send and that device is still around — matched by name, since per-run IDs rotate; code flow otherwise). Missing files abort with an explicit list — never a silent partial send. M4: nearby visibility toggle (listen-only mode via discovery's broadcast-off restart, setting persisted), last-used device sorted first with a "recent" marker; sender identity + IP in the accept prompt shipped already in M2.

## Decisions
- **Paths in history**: stored and persisted (`resendable` flag derived). PRD privacy question resolved per its own assumption — single-user desktop app.
- **Strict resend preflight**: any missing file → clear error naming it/them; no partial sends.
- **Peer matching on resend**: by device *name* (per-run IDs rotate across restarts; hostnames are stable).
- **Invisible mode** = stop broadcasting, keep listening (can still send to others, like AirDrop's receiving-off); self-echo health check suspended while muted.

## Files
| File | Action | Why |
|---|---|---|
| `transfers.go` | UPDATE | `Paths []string` + `Resendable` on FileTransfer |
| `app.go` | UPDATE | populate paths; `ResendTransfer(id)`; visibility toggle bindings; last-peer memory |
| `settings.go` (+test) | UPDATE | `NearbyVisible`, `LastPeer` fields |
| `history.go` | UPDATE (none expected) | verify paths survive save/load; codes still stripped |
| `discovery.go` (+test) | UPDATE | announce on/off restart support (`DisableBroadcast`) |
| `history_test.go` / new tests | UPDATE | resend preflight, paths round-trip |
| `frontend/src/App.svelte` | UPDATE | ↻ send-again button on terminal send rows; visibility toggle in Nearby header; last-used chip first + marker |
| locales ×6 | UPDATE | resend/visibility strings |

## Validation
Suite `-race`; builds; harness regression; live: resend a finished send (1 click), delete source file → resend errors naming it; toggle invisible in window A → A's chip vanishes in B (goodbye), A still sees B and can send; restart → last device still first, history resendable.

## Acceptance
- [ ] 1-click resend from history, surviving app restart; missing files named, nothing partial
- [ ] Peer resend re-offers to same-named device; clear error when it's gone
- [ ] Visibility toggle: hidden from others ≤1 s (goodbye), still sees/sends; persisted
- [ ] Last target device listed first with recent marker
- [ ] Tests green `-race`; harness green; code flow untouched
