# Plan: AirDrop-grade Flow on New UI

**Source PRD**: .claude/prds/krokodyl-reliability-airdrop-ux.prd.md
**Selected Milestone**: 3 — AirDrop-grade flow on new UI
**Complexity**: Large

## Summary
Redesign the interface (AirDrop-inspired feel, original identity) and remove the remaining send/receive friction: drag-and-drop onto the window sends immediately, multiple files can be picked at once, the last destination is remembered, code copy is one obvious tap, live progress/cancel (built in M2) get a polished surface. Backend grows three small capabilities (multi-path send, file-drop wiring, settings persistence); everything else is frontend.

## Files to Change
| File | Action | Why |
|---|---|---|
| `app.go` | UPDATE | `SendFiles([]string)` (worker already takes paths[]), `SelectFiles` multi-dialog, remembered-destination integration |
| `settings.go` (+test) | CREATE | Tiny JSON settings (last destination) in `os.UserConfigDir()/krokodyl/` |
| `main.go` | UPDATE | `DragAndDrop` options + `runtime.OnFileDrop` → `SendFiles`; drop hard window max-size (free resize) |
| `frontend/src/App.svelte` | REWRITE (visual) | New layout: hero drop zone, send/receive surfaces, polished history list, prominent code chip |
| `frontend/src/style.css` | UPDATE | Design tokens (type scale, spacing rhythm, depth, motion), light+dark intentional |
| `frontend/src/locales/*.json` (6) | UPDATE | New strings (drop hint, multi-file, etc.) |

## Design direction (anti-template, original)
Calm "hand-off surface": one focal drop zone with depth and a soft motion affordance on drag-over; code presented as a large copyable chip (the single most important artifact when sending); history as quiet receipts, not dashboard cards. Existing theme switcher kept; both themes tuned. Compositor-friendly motion only (transform/opacity). No replication of Apple AirDrop visuals — no radar circles, no Apple iconography.

## Tasks
1. Backend: `SendFiles`, `SelectFiles` (OpenMultipleFilesDialog), settings.go persistence (remember last destination; `GetDefaultDownloadPath` prefers it), drag-drop options + OnFileDrop → SendFiles, remove window max constraints. Tests for settings round-trip. Validate: build/vet/test -race, bindings regen.
2. Frontend redesign: tokens in style.css, App.svelte layout rework (drop zone w/ `--wails-drop-target`, drag-over state, multi-select button, code chip copy, polished transfer list w/ progress+speed+cancel from M2), locale additions ×6. Validate: svelte-check 0/0, build, wails build, manual drag-drop test.
3. Hygiene: README screenshot note (optional), PRD row update.

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Wails OnFileDrop quirks per-OS (drop target CSS required) | Medium | Use documented `--wails-drop-target: drop` property; whole main surface as target; manual test on Windows now, macOS in matrix |
| Redesign regresses existing flows | Medium | Keep component logic from M2 intact; only structure/styles move; svelte-check + manual pass |

## Acceptance
- [ ] Drag file(s) onto window → send starts, code shown prominently
- [ ] Multi-file pick works; receive remembers last destination
- [ ] Redesigned UI passes svelte-check/build; both themes intentional; no AirDrop replication
- [ ] All M2 behaviors (cancel, progress, single-fire auto-receive) still work
