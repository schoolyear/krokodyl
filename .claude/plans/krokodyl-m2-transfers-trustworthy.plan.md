# Plan: Transfers Trustworthy — engine correctness, deps refresh, big files, relay

**Source PRD**: .claude/prds/krokodyl-reliability-airdrop-ux.prd.md
**Selected Milestone**: 2 — Transfers trustworthy
**Complexity**: Large

## Summary
Update every dependency (croc v10.2.2 → v10.4.4 first — 8 releases behind, prime suspect for the relay/non-local failures), then rebuild the transfer engine around a **worker-subprocess architecture**: each transfer runs in a child process of the same binary. This is forced by croc's API surface (verified against v10.4.4 source): no cancel/context support, receive writes to the **process-global** working directory, and progress is only available by polling exported `Client` fields. A subprocess gives real cancel (kill), per-transfer cwd isolation (concurrent receives), crash isolation, and clean progress reporting — in one move. Staging moves into the destination directory (same volume) which fixes cross-volume failures and big-file behavior; nested-folder receive paths get fixed and tested; the frontend gets single-fire auto-receive and cancel buttons.

## Grounded API facts (verified in module cache, v10.4.4)
- `croc.New(Options)`, `GetFilesInfo(fnames, zipfolder, ignoreGit, exclusions)`, `Client.Send(filesInfo, emptyFolders, totalFolders)`, `Client.Receive()` — **signatures unchanged** from v10.2.2; upgrade is drop-in for compiles.
- `croc/v10@v10.4.4` requires **go ≥ 1.25** → go.mod toolchain bump.
- No `OutputDirectory` option; receive writes to cwd. No cancel/context API.
- Progress pollable via exported fields: `Client.TotalSent`, `TotalChunksTransferred`, `CurrentFileChunks`, `FilesToTransfer`, `FilesToTransferCurrentNum`.
- `models.DEFAULT_RELAY` ("croc.schollz.com"), `DEFAULT_RELAY6`, `DEFAULT_PORT`, `DEFAULT_PASSPHRASE` exist — stop hardcoding relay strings; also set `RelayAddress6` (currently unset; croc CLI sets both — IPv6-network candidate for the relay failures).

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Naming | `app.go` (`SendFile`, `ReceiveFile`, `RespondToOverwrite`) | Exported PascalCase methods on `App` = the JS-visible API; keep frontend-facing names stable |
| Errors | `app.go` (status→error + `EventsEmit`) | Failures set `FileTransferStatusError` and emit `transfer:updated`; user-visible reason required (PRD: actionable errors) |
| Logging | `main.go:79` (`setupFileLogging`) | logrus, structured, mirrored to file; worker logs must reach the same file |
| Events | `app.go:58-61` | String event constants `transfer:*`; add new ones there |
| Frontend state | `App.svelte:26-35` | Svelte 5 runes (`$state`); `EventsOn('transfer:updated')` drives the list |
| i18n | `frontend/src/locales/*.json` (6 files) | Every new UI string in all 6 locales |
| Tests | — | **None exist.** This milestone introduces Go tests (`*_test.go`, stdlib, table-driven per Go rules); no framework to mirror — state explicitly |

## Files to Change
| File | Action | Why |
|---|---|---|
| `go.mod` / `go.sum` | UPDATE | go 1.25 toolchain; croc v10.4.4; wails v2.12.0; logrus latest; `google/uuid` becomes direct dep; drop `pkg/errors` (archived) for `fmt.Errorf("%w")` |
| `frontend/package.json` / lock | UPDATE | All frontend deps current (svelte, vite, plugins, svelte-i18n, typescript) |
| `.github/workflows/release.yml` | UPDATE | Wails CLI pin → v2.12.0 (stays in sync with go.mod) |
| `worker.go` | CREATE | Worker mode: runs one croc send/receive in a child process, polls progress, emits JSON lines on stdout |
| `transfers.go` | CREATE | Transfer manager: race-safe state (locked map, uuid IDs), status transitions, event emission — extracted from `app.go`, unit-testable |
| `staging.go` | CREATE | Post-receive move logic: staging dir inside destination, nested relative paths preserved, EXDEV copy-fallback — pure functions, unit-tested |
| `app.go` | UPDATE | Spawn/track/kill workers; cancel + timeout; parent-side conflict prompts after download; remove `getFileDiff` full-file read; use `models.DEFAULT_*` relay constants |
| `main.go` | UPDATE | Dispatch to worker mode before `wails.Run` when worker flag present |
| `transfers_test.go`, `staging_test.go`, `worker_test.go` | CREATE | State transitions under `-race`, nested/EXDEV move cases, JSON protocol round-trip |
| `frontend/src/App.svelte` | UPDATE | Auto-receive fires exactly once per complete code; cancel button per active transfer; transfer speed text; overwrite modal shows sizes/mtime (content diff removed) |
| `frontend/src/locales/*.json` (6) | UPDATE | New strings: cancel, cancelled status, speed, timeout/relay error messages |

## Tasks

### Task 1: Dependency refresh (isolate the relay hypothesis first)
- **Action**: `go get github.com/schollz/croc/v10@v10.4.4 github.com/wailsapp/wails/v2@v2.12.0 github.com/sirupsen/logrus@latest && go mod tidy`; bump `go` directive to 1.25. Frontend: update all devDeps + svelte-i18n to current (`npm outdated` → bump → install). Update Wails CLI pin in release.yml to v2.12.0. Replace hardcoded `"croc.schollz.com:9009"`/`"pass123"` with `models.DEFAULT_RELAY`/`DEFAULT_PORT`/`DEFAULT_PASSPHRASE` and set `RelayAddress6` — exactly as the croc CLI does.
- **Mirror**: existing `croc.Options` literal stays otherwise identical.
- **Validate**: `go build ./... && go vet ./...`; `cd frontend && npm run build && npm run check`; `wails build` produces a working exe. **Manual checkpoint: retry a cross-network transfer — if the croc update alone fixes relay, record it in the PRD open question before continuing.**

### Task 2: Transfer manager extraction (`transfers.go` + tests)
- **Action**: Move `FileTransfer`, statuses, and list handling out of `app.go` into a `transferManager` with: `map[string]*FileTransfer` + `sync.Mutex`, uuid v4 IDs (replaces `Len()`-based IDs — collision-prone), `update(id, fn)` mutator that emits a **copy** via a callback (decouples from Wails runtime for testability), ordered snapshot for `GetTransfers`. Kills the stale-pointer bug (`&a.transfers[0]`) and the data race by construction. New statuses: `cancelled`; keep existing strings stable for the frontend.
- **Mirror**: status constants block `app.go:48-56`; event emission pattern.
- **Validate**: `go test -race ./...` — table-driven tests: concurrent updates, snapshot isolation, unknown-id update is a no-op.

### Task 3: Worker mode (`worker.go` + `main.go` dispatch)
- **Action**: `main()` checks `--transfer-worker` flag first (hidden; before `wails.Run`). Worker reads one JSON job spec from stdin (`{mode, code, paths, stagingDir, relay...}`), runs croc send or receive, and writes JSON event lines to stdout: `{type: progress|code|files|done|error, ...}` — progress polled from `Client.TotalSent` / `FilesToTransfer` sizes every 200 ms from a ticker goroutine. Receive workers `os.Chdir(stagingDir)` — safe: per-process cwd. Worker logs to the shared log file (Task: reuse `setupFileLogging`). Exit codes: 0 done, 1 error (message already emitted).
- **Mirror**: croc invocation as in current `performSend`/`performReceive`; logging via `setupFileLogging` (`main.go:79`).
- **Validate**: `go test ./...` for protocol encode/decode; manual: run worker from terminal with a job spec, observe JSON lines.

### Task 4: Parent integration — spawn, progress, cancel, timeouts (`app.go`)
- **Action**: `performSend`/`performReceive` become: create staging dir (receive: `<destination>/.krokodyl-partial-<id>` — same volume as destination), spawn `os.Executable()` with `--transfer-worker`, stream-scan stdout lines → `transferManager.update` → `transfer:updated` events (now with real progress + bytes/sec). New bound method `CancelTransfer(id)`: kills the worker process, status → `cancelled`, staging cleaned. Timeouts: sender with no receiver → cancel after 1 h (constant); failed-to-start (relay unreachable) surfaces croc's error text plus an actionable hint ("check internet connection / firewall"). On app shutdown (`OnShutdown`), kill all live workers and clean staging. Windows: spawn with no console window (GUI subsystem — verify no flash).
- **Mirror**: event constants block; error→status pattern.
- **Validate**: `go build && go vet && go test -race ./...`; manual: cancel mid-transfer both directions; close app mid-transfer → no orphan process (check Task Manager), staging dir removed.

### Task 5: Post-receive moves + overwrite prompts, parent-side (`staging.go` + `app.go`)
- **Action**: After worker `done`: walk staging **recursively with relative paths** (fixes nested-folder bug — current code joins base names onto the staging root), detect conflicts against destination, prompt per conflicting file via existing `transfer:overwrite` event — modal data now `{fileName, oldSize, newSize, oldModTime, newModTime}` (no content diff — removes the full-file-read OOM on multi-GB files). Move with `os.Rename`, **EXDEV/cross-device fallback to copy+fsync+delete** (covers symlinked/odd destinations even with same-volume staging). Prompt channel buffered (size 1) and abandoned-prompt safe: modal cannot be left unanswered in UI, but `OnShutdown` drains/answers "no" to unblock workers' parents. Big-file path: same-volume rename = instant, no double disk usage.
- **Mirror**: existing `transfer:overwrite` event + `RespondToOverwrite` flow (`app.go:405-416`).
- **Validate**: `go test ./...` — table-driven: nested tree move, conflict list correctness, EXDEV fallback (simulated via injected rename func), prompt-decline skips file.
- **Note**: `getFileDiff` deleted.

### Task 6: Frontend — single-fire auto-receive, cancel, speed (`App.svelte` + locales)
- **Action**: (1) Auto-receive: track `lastAutoSubmitted`; fire `receiveFile()` only when regex matches **and** code ≠ lastAutoSubmitted **and** not already receiving; reset on manual edit below-complete. Fixes "auto searches even when full code is entered". (2) Cancel button on transfers in non-terminal states → `CancelTransfer(id)`; new `cancelled` status icon/color. (3) Show `bytes/sec` + transferred/total from progress events next to the bar. (4) Overwrite modal: replace diff box with old/new size + modified time rows. All new strings added to en, es, fr, hu, nl, zh.
- **Mirror**: Svelte 5 runes state + `getStatusInfo` switch (`App.svelte:156-166`); locale key structure.
- **Validate**: `npm run check && npm run build`; manual: type a full code slowly — exactly one receive fires; editing the code after completion does not re-fire.

### Task 7: Regression matrix (gates the milestone)
- **Action**: Manual matrix on Windows + macOS (artifact from M1 pipeline): single file; folder with nested subfolders; 5 GB file (`fsutil file createnew big.bin 5368709120`) with visible monotonic progress + speed; destination on second drive/volume; two simultaneous receives; cancel both directions; overwrite accept/decline; **cross-network relay transfer (two different networks)**; wrong code → clear error. Record macOS transfer result + log against the PRD open questions (root causes: confirm/refute croc-version hypothesis for relay, engine bugs for Mac).
- **Validate**: checklist below; PRD success-metric rows for relay, big file, auto-receive.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build        # Windows artifact boots, transfers work
# pipeline (after commit, when user says go):
gh workflow run release.yml --ref <branch>
```
Manual matrix: Task 7 list. Milestone passes when every row completes or fails with an actionable message, nothing gets stuck, and relay + 5 GB + nested folder + concurrent + cancel all behave.

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| croc v10.4.4 behavior changes beyond signatures (protocol, defaults) | Medium | Task 1 is isolated + manually smoke-tested before engine work starts; pin exact version |
| Relay failures NOT fixed by update (hypothesis wrong) | Medium | Task 1 checkpoint records result; fallback investigation: `RelayAddress6`, firewall, `Debug: true` croc logs via worker log file |
| Worker subprocess spawning flagged by AV / console flash on Windows | Low | GUI-subsystem binary spawns without console; same-binary spawn is a common Wails pattern; test in matrix |
| Wails v2.12.0 regressions (menu, dialogs, events) | Low–Medium | M1 features re-checked in matrix; can pin back to v2.10.2 without touching engine work |
| Worker JSON protocol drift parent↔child | Low | Single shared Go types file + round-trip tests |
| 1 h sender timeout wrong for real usage | Low | Named constant, easy to tune; cancel always available |
| Frontend dep major bumps (vite/svelte plugins) break build | Medium | Bump conservatively; `npm run check` + build gate each bump; lockfile committed |

## Acceptance
- [ ] All tasks complete; `go test -race` green; builds green on all three platforms via pipeline
- [ ] Regression matrix passes incl. 5 GB, nested folders, cross-network relay, concurrent receives, cancel
- [ ] Auto-receive fires exactly once per complete code
- [ ] No stuck states: cancel works, shutdown kills workers, sender timeout fires
- [ ] PRD open questions updated with confirmed root causes (relay, big files, macOS)
- [ ] Patterns mirrored, not reinvented
