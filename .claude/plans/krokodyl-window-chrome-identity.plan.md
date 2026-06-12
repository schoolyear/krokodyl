# Plan: Native Window Chrome, Clear History & Readable Identity

**Source PRD**: .claude/prds/krokodyl-window-chrome-identity.prd.md
**Selected Milestones**: 1 — Native window bar, 2 — Clear history, 3 — Readable identity (combined; they share the shell files and are each small)
**Complexity**: Medium

## Summary
Rework the Windows titlebar into a dedicated, native-feeling chrome bar: a slim window-control strip (min/max/close, full hit-targets, double-click-to-maximize, full-width drag region) that sits *above* the app's brand/content, with the page scroll confined below it so the scrollbar never crosses the bar. Add a confirmed "clear history" action that wipes the on-screen list and the persisted store. Generate a friendly random per-instance name (e.g. "Brave Otter") at startup and use it as the nearby device name, with the OS hostname/IP kept as secondary detail.

## Decisions (pending your confirm — see report)
- **Button side**: PRD open question. Default in this plan = **right** (true Windows convention; the PRD's literal "top left" looks like a misremember of "their own bar, native like Windows buttons"). One-constant flip if you want left.
- **Identity persistence**: **fresh per process** (best disambiguates individual windows, which is the stated need); hostname remains the stable machine anchor shown as secondary detail (IP already shown in the accept prompt).
- **Clear-history confirm**: modal confirm (destructive + persisted), reusing the existing modal pattern.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Per-OS chrome | `main.go` `Frameless: runtime.GOOS == "windows"`, `mac.TitleBarHiddenInset()` | Windows-only frameless; mac/Linux untouched |
| Titlebar component | `frontend/src/components/TitleBar.svelte` | `--wails-draggable: drag` on bar, `no-drag` on controls; `WindowMinimise`/`WindowToggleMaximise`/`Quit` runtime calls |
| Window runtime | `wailsjs/runtime` exports `WindowToggleMaximise`, `WindowIsMaximised` | Already available; no new bindings needed for window ops |
| Bound methods | `app.go` `GetTransfers`, `CancelTransfer`, `persistHistory` | Exported PascalCase `App` methods → JS bindings; mutate via `transferManager`, persist best-effort |
| History store | `history.go` `saveHistory`/`loadHistory`, `historyMu` | JSON at `UserConfigDir/krokodyl/history.json`; clear = empty list + remove/empty file under the same lock |
| Random words | `croc/v10/src/utils.GetRandomName()` (already used for codes) | Mnemonic word generator already in tree; new name uses an embedded adjective+animal list for friendlier output |
| Modal/confirm | `App.svelte` overwrite + nearby-offer modals | `.modal-backdrop`/`.modal` + action buttons |
| i18n | 6 locale files | New keys in all locales |
| Tests | `history_test.go`, `discovery_test.go` table-driven `-race` | New `names_test.go`; clear-history + name-format tests |

## Files to Change
| File | Action | Why |
|---|---|---|
| `frontend/src/components/TitleBar.svelte` | UPDATE | Split into a dedicated Windows chrome strip (buttons + drag) separate from the brand row; native button sizing/hover; double-click bar → toggle maximize |
| `frontend/src/App.svelte` | UPDATE | Layout becomes header (titlebar, fixed) + scrollable content; clear-history button + confirm modal; show readable device self-name where useful |
| `frontend/src/style.css` | UPDATE | Confine scroll to content region so the scrollbar never overlaps the titlebar; keep slim scrollbar styling on the content scroller only |
| `names.go` | CREATE | Friendly random name generator (adjective + animal, crypto/rand pick) |
| `names_test.go` | CREATE | Name format + distinctness across calls |
| `app.go` | UPDATE | Use readable name for discovery identity; `ClearHistory()` bound method; expose self-name to frontend |
| `history.go` | UPDATE | `clearHistory(path)` helper (empty list + reset file) |
| `history_test.go` | UPDATE | Clear-history round-trip (file emptied) |
| `frontend/src/locales/*.json` (6) | UPDATE | `history.clear`, `history.clear_confirm`, identity strings |

No changes to the transfer engine, discovery protocol, or worker.

## Tasks

### Task 1 — Native Windows chrome bar (Milestone 1)
- **Action**: In `TitleBar.svelte`, when `platform === 'windows'`, render a thin top strip holding the window controls (default right; constant `CONTROLS_SIDE` flips to left) with native-style square buttons (full-height hit-targets, hover greys, close hovers red) and the rest of the bar as drag region; `ondblclick` on the drag region → `WindowToggleMaximise()`. Brand + theme/lang controls move into a normal app row beneath the chrome strip (not draggable-critical). Keep mac/Linux on the existing single-row layout (no chrome strip).
- **Mirror**: existing `--wails-draggable` drag/no-drag split; existing win-btn styling.
- **Validate**: `wails build`; on Windows — drag by bar, double-click maximizes/restores, min/restore/close work, snap (Win+arrows) works, buttons clickable to their full edges.

### Task 2 — Scrollbar isolation (Milestone 1)
- **Action**: Restructure layout so the titlebar is a non-scrolling header and a single content wrapper below it owns vertical scroll (`min-height:100vh` column: `header` auto + `.content` `flex:1; overflow-y:auto`). Move `::-webkit-scrollbar` rules onto the content scroller so the bar is never under the scrollbar. Preserve 320px / short-height behavior.
- **Mirror**: token-based scrollbar styling already in `style.css`.
- **Validate**: long history at small heights — scrollbar starts below the titlebar, never overlaps it; re-run responsive screenshot matrix (320/800×450/1280, both themes).

### Task 3 — Clear history (Milestone 2)
- **Action**: `history.go` `clearHistory(path)` (truncate to `[]` / remove file). `app.go` `ClearHistory()`: clear the `transferManager` (new `reset()` that drops all + emits empty) and call `clearHistory` under `historyMu`. `App.svelte`: a "Clear" control by the History heading (shown only when history non-empty) → confirm modal → `ClearHistory()` then refresh list. Strings ×6.
- **Mirror**: `persistHistory` lock usage; modal pattern; `transferManager` emit-on-change.
- **Validate**: `go test -race`; UI — clear empties list, confirm required, persists across restart (history file empty).

### Task 4 — Readable identity (Milestone 3)
- **Action**: `names.go` `randomDeviceName()` → "Adjective Animal" from embedded lists, picked with `crypto/rand`; fresh per process. `app.go` `startNearby`: discovery identity `Name` = readable name (was `os.Hostname()`); keep hostname for logs/secondary. Optional `GetDeviceName()` binding so the UI can show "You are: Brave Otter". Two instances → distinct names (different rand picks; collision risk low, acceptable per PRD).
- **Mirror**: existing identity construction in `startNearby`; `GetNearbyPrefs` binding shape.
- **Validate**: `go test -race` (name matches `^[A-Z][a-z]+ [A-Z][a-z]+$`, 100 calls reasonably distinct); two live instances show two different readable names in each other's nearby list.

### Task 5 — Validation pass
- **Action**: full builds; `go test -race`; GUI-harness loopback regression; relaunch two instances; responsive matrix re-shoot for the scroll/layout change.
- **Validate**: commands below all green; manual Windows window behaviors confirmed.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback regression (engine untouched, must stay green)
# manual Windows: drag, dbl-click maximize, min/close, snap, full-edge button clicks
# manual: scrollbar below titlebar w/ long history at 450px height
# manual: clear history (confirm + persist), two instances show distinct readable names
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Restructuring scroll container regresses responsive layout | Medium | Re-run M1 responsive screenshot matrix; additive layout, base styles intact |
| Frameless double-click maximize unreliable | Low–Medium | Dedicated maximize button always present as fallback; verify Wails dblclick on drag region |
| Drag region swallows button clicks | Medium | Explicit `no-drag` on the control strip; full-edge click test |
| Two instances roll same readable name | Low | crypto/rand pick from large list; acceptable per PRD; hostname/IP disambiguates in prompt |
| Button side wrong vs. your intent | Medium | Single `CONTROLS_SIDE` constant; confirm before/at execution |

## Acceptance
- [ ] Windows: dedicated chrome bar, native min/max/close + double-click maximize + full drag, behaviors indistinguishable from a standard window
- [ ] Scrollbar never overlaps the titlebar at any height/content length; responsive matrix still clean
- [ ] Clear-history empties list + persisted store behind a confirm; survives restart
- [ ] Every instance shows a readable name; two windows on one host are distinct
- [ ] mac/Linux chrome unchanged; engine/discovery untouched; `-race` + harness green
