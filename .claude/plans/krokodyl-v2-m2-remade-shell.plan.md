# Plan: Remade Shell — frameless titlebar, platform materials, refined motion

**Source PRD**: .claude/prds/krokodyl-modern-ux.prd.md
**Selected Milestone**: 2 — Remade shell
**Complexity**: Medium–Large

## Summary
Replace the stock OS window chrome with an app-owned shell: frameless window with a custom titlebar on Windows (drag region + min/max/close), hidden-inset titlebar with native traffic lights on macOS, untouched native titlebar on Linux (PRD open question resolved: weakest framework support there). On Windows 11 the window picks up the system Mica/acrylic material behind semi-transparent app surfaces. On top of that: a motion polish pass (tab indicator, hover/press states, drag-over feedback) gated by `prefers-reduced-motion`. Flow logic untouched — this is shell + visual layer.

## Per-OS shell decision (the core of this milestone)
| OS | Chrome | Material |
|---|---|---|
| Windows | Frameless + custom titlebar (CSS drag region, min/max/close buttons) | `BackdropType: Auto` → Mica on Win11, solid fallback below |
| macOS | Native, `TitleBarHiddenInset` — traffic lights float over app content (top padding) | Solid theme background (translucency deferred — no test hardware) |
| Linux | Native titlebar, no change | Solid theme background |

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Per-OS branching | `main.go` `appMenu()` (`runtime.GOOS == "darwin"`) | OS-conditional options at startup |
| Wails options | `main.go` `options.App` literal + `mac.Options` block | Extend in place; platform sub-structs (`windows.Options`) |
| Drop-target CSS | `App.svelte` `.drop-zone { --wails-drop-target: drop }` | Same mechanism for `--wails-draggable: drag` on the titlebar |
| Components | `frontend/src/components/ThemeSwitcher.svelte` | Small single-purpose Svelte component, scoped styles |
| Runtime calls | `App.svelte` wailsjs runtime imports | `WindowMinimise`/`WindowToggleMaximise`/`Quit`/`Environment` from same runtime module |
| Tokens | `style.css` `:root` | Material-aware surface tokens added beside existing ones |
| i18n | 6 locale files | aria-labels for window controls in all locales |
| Tests | `cmd/guiharness` | Loopback regression after shell change (shell must not break worker spawning) |

## Files to Change
| File | Action | Why |
|---|---|---|
| `main.go` | UPDATE | `Frameless` (Windows only), `windows.Options{BackdropType: Auto, WebviewIsTransparent, WindowIsTranslucent}`, `mac.Options{TitleBar: TitleBarHiddenInset()}`, transparent `BackgroundColour` where translucent |
| `frontend/src/components/TitleBar.svelte` | CREATE | Drag region (`--wails-draggable: drag`), brand block, window controls (Windows only); controls excluded from drag |
| `frontend/src/App.svelte` | UPDATE | Mount TitleBar; platform class from `Environment()` (`.platform-windows` / `.platform-darwin` top padding); topbar contents fold into titlebar row; motion polish (tab indicator slide, button press, drag-over pulse) |
| `frontend/src/style.css` | UPDATE | Translucent surface tokens for material mode (`.platform-windows` translucent bg), reduced-motion guard, motion tokens |
| `frontend/src/locales/*.json` (6) | UPDATE | `titlebar.minimize/maximize/close` aria-labels |

## Tasks

### Task 1: Window options per OS (`main.go`)
- **Action**: `Frameless: runtime.GOOS == "windows"`; add `Windows: &windows.Options{BackdropType: windows.Auto, WebviewIsTransparent: true, WindowIsTranslucent: true}`; mac: `TitleBar: mac.TitleBarHiddenInset()` added to existing `mac.Options`; `BackgroundColour` transparent on Windows (material shows through), unchanged elsewhere.
- **Mirror**: existing options literal + `appMenu()` OS-branching.
- **Validate**: `go build`; app opens frameless on this Win11 machine with material visible.

### Task 2: TitleBar component
- **Action**: `TitleBar.svelte`: left = brand (croc + name), right = ThemeSwitcher + language + (Windows only) minimize/maximize/close buttons calling wailsjs runtime `WindowMinimise`/`WindowToggleMaximise`/`Quit`. Whole bar `--wails-draggable: drag`; interactive children `--wails-draggable: no-drag`. Double-click bar = toggle maximize (Wails handles on drag region). Platform passed as prop. Replaces current `.topbar`.
- **Mirror**: ThemeSwitcher component shape; drop-target CSS mechanism.
- **Validate**: drag window by bar, controls work, controls absent on darwin/linux, aria-labels localized.

### Task 3: Platform detection + layout integration (`App.svelte`)
- **Action**: On mount, `Environment()` → set `platform` state + `.platform-{os}` class on a root wrapper. darwin: top padding clears inset traffic lights. Fold old topbar into TitleBar mount. 320 px behavior preserved (titlebar wraps controls under brand — reuse M1 narrow rules).
- **Mirror**: existing onMount init sequence.
- **Validate**: svelte-check 0/0; 320 px matrix shot of new titlebar.

### Task 4: Material-aware surfaces + motion polish
- **Action**: `.platform-windows` (translucent capable): `html` background transparent, `--color-bg` surfaces gain alpha so Mica shows; non-Windows keeps solid tokens. Motion: animated tab indicator (transform-based), button press scale (0.98), drop-zone drag-over pulse, card hover lift — all wrapped in `@media (prefers-reduced-motion: no-preference)`. Win10 fallback: if material absent the translucent colors sit on the solid window color — verify acceptable contrast (fallback to solid via same class toggle if not).
- **Mirror**: token system in `style.css`; compositor-friendly properties only (transform/opacity) per web rules.
- **Validate**: contrast pass both themes; matrix re-shots (320/800×450/1280) in browser for layout; material verified in the real window on this machine.

### Task 5: Regression + review
- **Action**: full builds; GUI-harness loopback; quick code review pass (shell code only); relaunch instances.
- **Validate**: commands below all green; transfer works in frameless window; window snap (Win+arrows), resize edges, taskbar behaviors intact.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback regression
# manual on this Win11 machine: drag/snap/resize/min/max/close, Mica visible, 320px titlebar
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Frameless breaks snap/resize/shadow on Windows | Medium | Wails frameless keeps system resize borders by default; manual pass covers snap/aero behaviors; fallback flag = revert `Frameless` |
| Translucency flags on Win10 → black/ugly window | Medium | `BackdropType: Auto` degrades; CSS solid fallback class; can't test Win10 locally — note for matrix |
| mac HiddenInset overlaps content | Medium | `.platform-darwin` top padding; needs mac hardware confirmation (existing outstanding item) |
| Drag region swallows clicks on controls | Low | Explicit `no-drag` on interactive elements |
| Maximized frameless covers taskbar | Low | Wails handles maximize bounds; manual check |

## Acceptance
- [ ] Windows: frameless, draggable custom titlebar, working min/max/close, Mica/Auto material behind translucent surfaces, snap/resize intact
- [ ] macOS: hidden-inset titlebar config in place (visual confirm deferred to mac hardware pass)
- [ ] Linux: untouched native chrome
- [ ] Motion polish in, fully disabled under reduced-motion
- [ ] 320 px titlebar layout clean; all locales have control labels
- [ ] Harness loopback green; no flow-logic diffs
