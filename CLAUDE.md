# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

krokodyl is a Wails v2 desktop app (Go + Svelte 5) for AirDrop-style P2P file transfer over the croc protocol.

**Read `docs/CODEMAPS/*.md` first** — token-lean architecture/backend/frontend/data/dependency maps.

## Commands

```bash
wails dev                                   # dev w/ hot reload (Vite + Go); browser debug at http://localhost:34115
wails build                                 # production build → build/bin/krokodyl(.exe)
wails build -platform linux/amd64 -tags webkit2_41   # Linux MUST pass this tag (needs libwebkit2gtk-4.1-dev)

go test -race ./...                         # full test gate — every subsystem has a _test.go sibling
go test -race -run TestName ./...           # single test
go test -race recovery_test.go recovery.go  # single subsystem (package main, files compiled together)

cd frontend && npm install                  # frontend deps
cd frontend && npm run check                # svelte-check — TYPE + A11Y gate; expect 0 errors / 0 warnings
cd frontend && npm run build                # frontend-only build

gofmt -w . && go vet ./...                  # format + static-check gate (run before commit / go-review)

go build -ldflags "-X main.buildStamp=<hash>"   # inject build identifier (default "dev")
go run . --transfer-worker                  # run a transfer worker standalone (reads one JSON job on stdin) — debugging aid
```

**Release:** merge the branch to `main` (PR), then tag main HEAD and push — `git tag -a vX.Y.Z <sha> -F -` then `git push origin vX.Y.Z` — which triggers the Actions build (Linux/Windows/macOS) + publish. Patch-bump from the last tag (v0.17.1 → v0.17.2). The Wails CLI version in `.github/workflows/release.yml` (`@v2.12.0`) MUST match `wails/v2` in `go.mod`.

## Architecture (big picture)

**One binary, two modes.** `main.go` checks `--transfer-worker` in `os.Args` *before* `wails.Run()`. Present → `runWorker()` (worker.go) runs one transfer and exits; absent → GUI starts. The flag check must stay before Wails init — Wails owns the main loop and never returns.

**Transfer = child subprocess** (`runWorkerJob` in app.go). The GUI spawns the same binary with `--transfer-worker`, writes a JSON `workerJob` to stdin (closed immediately), and reads JSON `workerEvent` lines from stdout (`files`/`progress`/`done`/`error`). Three reasons for the subprocess model:
- **Kill-based cancel** — croc has no cancel API; `CancelTransfer` → `cmd.Process.Kill()`.
- **cwd isolation** — croc writes to cwd; each receive worker does `os.Chdir(stagingDir)`. The GUI never changes its own cwd.
- **Crash isolation** — a croc segfault kills only that transfer, not the GUI.

**Resilience layer** (recovery.go, stall.go). `runRecoverableAttempts` retries failed workers with backoff (doubling, capped 10s) under a *recovery budget* keyed on peak progress %: if no new peak across N attempts, give up. The stall watchdog (`startStallWatchdog`) is **kill-only** — it kills the stuck worker and lets the recovery loop interpret the resulting error; it never marks the transfer failed itself. `overallProgress(basePct, sessionPct)` offsets per-session worker progress so the bar doesn't reset to 0% on resume. Resume works because `stagingDirForCode` derives a *deterministic* staging dir from the transfer code + croc pre-allocation / `MissingChunks`.

**transferManager** (transfers.go) — mutex-guarded map, emits *copies* of `FileTransfer` (UI can't corrupt live state), terminal states are immutable (late worker events can't resurrect a cancelled/failed transfer), order is newest-first.

**LAN discovery** (discovery.go, nearby.go, netaddr.go, names.go). `schollz/peerdiscovery` multicast on **:42791** advertises id/name/control-port/cert-fingerprint/addresses every 2s (5s expiry, self-echo health check). nearby.go runs an ephemeral TLS control channel (self-signed ECDSA cert, **SHA-256 fingerprint pinning** against the multicast payload) carrying offer → accept/decline → croc-code handoff. netaddr.go ranks real LANs (192.168/10.x) above virtual adapters (172.16 / Hyper-V / WSL / Docker); sender tries candidates real-LAN-first with a 4s per-address timeout. Hide/unhide uses a `Gen` counter + a 3s bye-suppression window so a deliberate unhide is instant while stray re-announces are absorbed. names.go makes random "Adjective Animal" instance names via crypto/rand.

**Persistence** (settings.go, history.go). `settings.json` + `history.json` in the OS config dir, mode `0o600`. A stable per-install `MachineID` (UUID) lets the resend feature re-target the same device across renames. History caps at 50 terminal entries with transfer codes stripped. `historyMu` serializes disk writes; `sweepPartials` only deletes dirs whose basename starts with `partialDirPrefix` (defense against tampered settings).

**Frontend** (frontend/src). Svelte 5 **runes** (`$state`/`$derived`) — most UI lives in one large `App.svelte`. Backend calls go through generated `wailsjs/` bindings; live updates arrive via `EventsOn` (`transfer:updated`, `transfer:overwrite`, `nearby:updated`, `nearby:state`, `nearby:offer`, `transfer:cleared`). svelte-i18n with 6 locales (en/nl/fr/es/hu/zh), theme store persisted to localStorage, `TitleBar.svelte` renders native Windows chrome + macOS traffic-light padding.

## Frontend conventions

- A11y is a maintained baseline (WCAG 2.1 AA). New interactive UI needs an `aria-label`, inherits the global `:focus-visible`, and ≥24px target size.
- Modals use the `modalDialog` Svelte action (focus-trap + Escape + focus-restore) in App.svelte.
- Tabs/segmented controls use roving `tabindex` on the items — a keydown handler on the `role=tablist` container trips `svelte-check` (`a11y_interactive_supports_focus`).
- White-on-green text uses `--color-primary-strong`, not `--color-primary` (contrast).

## Gotchas & constraints

- **No Apple notarization — hard constraint** (no Apple Developer account). macOS ships ad-hoc codesigned (`codesign --sign -`); users right-click → Open once. Don't add notarization steps.
- **Windows GUI-subsystem stderr is invalid.** A `-H windowsgui` binary launched by double-click has a dead stderr handle; if a worker inherits it, croc's progress writes fail and stall the transfer. Worker `cmd.Stderr` MUST stay `nil`; the parent logs to a file via `bestEffortWriter`. **Terminal-launched runs have a valid stderr and hide this bug** — verify transfer changes by launching the built GUI, not `go run`. `cmd/guiharness` reproduces the exact scenario.
- **Test nearby/transfer on TWO real machines.** Loopback / single-instance testing misses cross-network, "room not ready", and virtual-adapter (Hyper-V VM↔host) failures.
- **`wailsjs/` is generated — never hand-edit.** Re-run the Wails generator when a Go struct exposed to the frontend changes.
- **`stagingDirForCode` determinism is load-bearing.** Changing its hash/salt orphans every in-flight partial transfer; there is no version field, so any change is breaking.
- **Linux build without `-tags webkit2_41`** builds against WebKit 4.0 and fails at runtime.
- **`app.go` (~1190) and `App.svelte` (~1600) exceed the 800-line guideline** — extract when you touch them, don't keep growing them.

## Working in this repo (environment)

- **CI occasionally fails on a transient `sum.golang.org` TLS timeout** — that's a flake, not a code break; re-run the job.
- Commit subjects use conventional prefixes: `feat`/`fix`/`refactor`/`docs`/`test`/`chore`/`perf`/`ci`.
