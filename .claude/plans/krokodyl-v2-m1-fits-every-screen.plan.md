# Plan: Fits Every Screen — adaptive layout 320px → 4K

**Source PRD**: .claude/prds/krokodyl-modern-ux.prd.md
**Selected Milestone**: 1 — Fits every screen
**Complexity**: Medium

## Summary
Make every flow usable from phone-shaped windows (320 px wide) through 4K, including short windows (≤500 px tall), in all 6 locales and both themes. Lower the OS window minimums to match. Validation is empirical: a scripted screenshot matrix (Playwright against the built frontend) at each breakpoint × theme × longest-locale, fixing every clip/overlap it surfaces, then re-shooting until clean.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Responsive units | `frontend/src/App.svelte` (existing `clamp()` font/padding usage) | Fluid `clamp(min, vw, max)` over fixed sizes |
| Breakpoint style | `frontend/src/App.svelte` `@media (max-width: 600px)` / `(max-width: 480px)` blocks | Width media queries collapsing grids to single column |
| Tokens | `frontend/src/style.css` `:root` block | Spacing/radius/shadow/motion via CSS custom properties |
| Window config | `main.go` `options.App` literal | Min sizes set beside Width/Height |
| Tests | — | No automated UI tests exist; this milestone's gate is the screenshot matrix (manual-scripted), consistent with prior visual verification in this repo |

## Files to Change
| File | Action | Why |
|---|---|---|
| `main.go` | UPDATE | `MinWidth` 520→320, `MinHeight` 500→420 — phone-shaped windows allowed |
| `frontend/src/App.svelte` | UPDATE | Narrow (<380), compact (<480), short-height (<560) and large (>1600) handling: topbar stacking, segmented control compaction, drop-zone scaling, code-chip wrapping, modal at 320, history density |
| `frontend/src/style.css` | UPDATE | Any token additions needed (e.g. fluid space scale); no palette changes |

No backend, engine, or locale-content changes.

## Tasks

### Task 1: Lower window minimums
- **Action**: `MinWidth: 320`, `MinHeight: 420` in `main.go`. Defaults stay 800×600.
- **Mirror**: existing options literal.
- **Validate**: `go build ./...`; built app resizes down to 320×420.

### Task 2: Adaptive CSS pass
- **Action**: In `App.svelte` styles (visual layer only — no logic edits):
  - **<380 px**: topbar wraps (brand row + controls row), brand tagline hidden, segmented buttons shrink (smaller padding, keep labels), drop-zone padding compresses, code chip font steps down and wraps, buttons full-width where stacked, modal padding/margins tighten.
  - **<480 px** (existing block, extend): destination group already stacks — verify inputs/buttons hit 44 px touch targets.
  - **height <560 px**: vertical gaps compress, drop-zone min padding, subtitle hidden, history min-height reduced — everything reachable by scroll, nothing clipped.
  - **>1600 px**: content column capped (surface ~560, history ~760 already) — verify type doesn't look lost; bump main max-width modestly and center.
  - Overflow guards: `min-width: 0` on grid children, `overflow-wrap` on filenames/codes/error text.
- **Mirror**: existing media-query blocks and clamp() conventions in the same file.
- **Validate**: `npm run check` 0/0; matrix in Task 3.

### Task 3: Screenshot matrix (the gate)
- **Action**: Serve `frontend/dist` via `vite preview`; Playwright resize + screenshot at:
  - widths × heights: 320×600, 360×740, 480×800, 768×1024, 1280×800, 2560×1330, and short 800×450, 320×450
  - × themes: dark, light
  - × locales: `en`, `hu` (longest strings; spot-check `fr`)
  - × tabs: send (with simulated waiting-send code panel via injected state if feasible — else send tab default) and receive; plus overwrite modal at 320 (open via DOM state injection)
  Inspect every shot for clipping, overlap, unreachable controls, broken wrap. Fix → re-shoot → repeat until clean.
- **Mirror**: same preview+Playwright flow used for the M3 visual check.
- **Validate**: final matrix clean; key shots kept temporarily for the report (deleted before commit).

### Task 4: Built-app sanity
- **Action**: `wails build`; run; resize live to minimum and full-screen; switch locale to `hu` at 320 width.
- **Validate**: no clipping, all controls reachable, transfer still works (quick loopback via `cmd/guiharness` to prove no regressions).

## Validation
```bash
go build ./... && go vet ./...
cd frontend && npm run check && npm run build && cd ..
# matrix: vite preview + Playwright loop (Task 3)
wails build
# regression: GUI harness loopback transfer still green
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Wails min-size vs DPI scaling oddities (logical vs physical px) | Low–Medium | Verify on this 125%/150% Windows machine at min size |
| Long locale strings (hu/fr) break 320 px assumptions | Medium | hu included in matrix; ellipsis/wrap rules where needed |
| Browser-preview ≠ WebView2 rendering | Low | Same Chromium engine family; Task 4 sanity in real app |
| CSS churn regresses M3 look | Medium | Additive media queries only; base styles untouched; before/after shots compared |

## Acceptance
- [ ] All tasks complete; matrix clean at every breakpoint × theme × locale combo listed
- [ ] Window resizes to 320×420; defaults unchanged
- [ ] svelte-check 0/0; builds green; harness loopback green
- [ ] No logic/flow changes — visual layer only
