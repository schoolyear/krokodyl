---
name: krokodyl-patterns
description: Repo workflows for krokodyl (Wails + Go + Svelte 5). Use when adding a backend method exposed to the frontend, editing UI strings, changing package deps, writing Go transfer logic, or committing/releasing — encodes the file co-change rules git history shows always move together.
version: 1.0.0
source: local-git-analysis
analyzed_commits: 68
---

# krokodyl Patterns

Wails v2 desktop app: Go `package main` (flat, at repo root) + `frontend/` (Svelte 5 + Vite). Patterns below are derived from git co-change history — these files **move together**; changing one without the others is the common mistake.

## Commit Conventions

Going-forward standard is **conventional commits**: `feat:`/`fix:`/`refactor:`/`docs:`/`test:`/`chore:`/`perf:`/`ci:`. (Older history is freeform — do not imitate it.) Subject imperative, lower-case, no trailing period.

## Code Architecture

```
*.go                  # package main, flat at root (app.go, worker.go, recovery.go, …)
*_test.go             # one test sibling per source file (table-driven, run -race)
cmd/guiharness/       # GUI-subsystem stderr-bug repro harness
frontend/src/         # App.svelte (hub), components/, stores/, locales/, i18n.ts
frontend/wailsjs/     # GENERATED Go↔TS bindings — never hand-edit
docs/CODEMAPS/        # token-lean architecture maps (read first)
```
Churn hubs: `app.go` (~1190 LOC) and `frontend/src/App.svelte` (~1600 LOC). Both exceed the 800-line guideline — extract when touched.

## Workflows (co-change rules)

### Add / change a Go method exposed to the frontend
History: `app.go` → `wailsjs/go/main/App.js` + `App.d.ts` + `models.ts` always change together.
1. Edit the `App` method (or exposed struct) in `app.go` (or transfers.go).
2. Regenerate bindings (`wails dev` / `wails build` runs the generator) — **never hand-edit `wailsjs/`**.
3. Import + call the new binding in `App.svelte`; consume any new `EventsOn` event.

### Add or edit a UI string
History: all six `frontend/src/locales/{en,nl,fr,es,hu,zh}.json` change as a set.
1. Add the key to **every** locale file (en is source of truth; translate the rest).
2. Reference via `$_('key')` (svelte-i18n); use `{ values: { … } }` for interpolation.

### Change frontend dependencies
History: `frontend/package.json` co-changes with `frontend/package.json.md5` (+ `package-lock.json`).
1. Edit `package.json`, run `npm install` (refreshes lockfile).
2. The `package.json.md5` checksum (Wails `frontend:install` cache key) updates — commit it too, or `wails build` re-installs every time.

### Write Go transfer / resilience logic
1. Add code to the relevant file (recovery.go, stall.go, worker.go, …).
2. Add/extend its `*_test.go` sibling (table-driven).
3. `go test -race ./...` (full gate) or `go test -race <file>_test.go <file>.go` (single subsystem).
4. `gofmt -w . && go vet ./...` before commit.

### New interactive UI (a11y baseline)
Needs `aria-label`, the global `:focus-visible`, ≥24px target. Modals use the `modalDialog` action; tabs use roving `tabindex` (container keydown trips `svelte-check`). Verify: `cd frontend && npm run check` → 0 warnings.

### Release
Merge branch to `main` (PR) → tag main HEAD `git tag -a vX.Y.Z <sha> -F -` → `git push origin vX.Y.Z` (triggers Actions build+publish). Patch-bump from last tag. Keep the Wails CLI pin in `release.yml` in sync with `wails/v2` in `go.mod`.

## Testing Patterns

- Framework: stdlib `go test`, table-driven, **always `-race`**.
- Layout: one `_test.go` per source file in `package main` (no separate test dir).
- Frontend gate: `svelte-check` (`npm run check`) for type + a11y; expect 0/0.
- Manual: nearby/transfer features need **two real machines** (loopback hides cross-net / room-not-ready / virtual-adapter bugs) and the **GUI-launched** binary (terminal stderr hides the Windows GUI-subsystem stall).
