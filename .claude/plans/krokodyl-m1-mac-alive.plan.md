# Plan: Mac Alive — krokodyl launches and works on macOS (unsigned)

**Source PRD**: .claude/prds/krokodyl-reliability-airdrop-ux.prd.md
**Selected Milestone**: 1 — Mac alive
**Complexity**: Medium

## Summary
Make the macOS release usable by a normal person without notarization: the release pipeline always produces a proper `.app` bundle (ad-hoc signed, zip-packaged), the app gets a native macOS menu so Cmd+V/copy/paste work, file logging is added so the reported "transfer doesn't work on Mac" failure can finally be diagnosed, and first-open instructions ship with every release. No transfer-engine changes here — those are milestone 2; this milestone ends with a macOS smoke test that feeds the open root-cause question.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Naming | `app.go:71` (`SendFile`), `app.go:381` (`SelectFile`) | Exported PascalCase methods on `App`; single `main` package |
| Errors | `app.go:73-74` | Returned errors wrapped with `errors.Wrapf(err, "context")` |
| Logging | `app.go:119`, `main.go:39` | `logrus.WithError(err).Error("message")`; structured logrus throughout |
| Options wiring | `main.go:20-36` | Flat `options.App` literal in `main()`; extend in place |
| CI | `.github/workflows/release.yml:10-28` | Per-OS matrix with `include` overrides; OS-conditional steps via `if: matrix.os` |
| Tests | — | **No tests exist in the repo.** No pattern to mirror; this milestone validates via build commands + manual macOS matrix |

## Files to Change
| File | Action | Why |
|---|---|---|
| `main.go` | UPDATE | Add macOS application menu (App + Edit) so paste/copy/select-all work in WKWebView; add `mac.Options`; initialize file logging |
| `.github/workflows/release.yml` | UPDATE | Match Go toolchain to go.mod; pin Wails CLI; always produce `.app` (remove bare-binary fallback — fail hard instead); ad-hoc codesign; package with `ditto` zip; add `workflow_dispatch` for pipeline testing; put macOS first-open instructions in release body |
| `README.md` | UPDATE | "Install on macOS" section: download, unzip, right-click → Open (one step), why the warning appears |
| `.claude/prds/krokodyl-reliability-airdrop-ux.prd.md` | UPDATE | Milestone row status (done by this command); after smoke test, record macOS root-cause findings under Open Questions |

`build/darwin/Info.plist` already exists and is sane (templated by Wails) — no change.

## Tasks

### Task 1: macOS menu + Mac options in `main.go`
- **Action**: Build a `*menu.Menu` containing `menu.AppMenu()` and `menu.EditMenu()` (darwin only — guard with `runtime.GOOS == "darwin"`; on Windows an app menu would add an unwanted menu bar). Pass via `Menu:` in `options.App`. Add `Mac: &mac.Options{}` with sensible defaults (about dialog title/message from existing app name).
- **Mirror**: extend the existing flat `options.App` literal (`main.go:20-36`).
- **Validate**: `go build ./...` and `go vet ./...` pass; manual: Cmd+V pastes into the receive-code field on macOS.

### Task 2: file logging for diagnosability
- **Action**: In `main()`, before `wails.Run`, set logrus output to `io.MultiWriter(os.Stderr, logFile)` where `logFile` lives under `os.UserCacheDir()/krokodyl/krokodyl.log` (create dir; on failure, log warning and continue with stderr only — never crash on logging setup). Purpose: Finder-launched apps have no terminal; today macOS transfer errors vanish.
- **Mirror**: logrus usage as in `app.go:119`; error wrapping as `app.go:73-74`.
- **Validate**: `go build ./...`; run app, confirm log file appears and receives the startup line on Windows; macOS confirmation in smoke test.

### Task 3: release pipeline correctness (`.github/workflows/release.yml`)
- **Action**:
  1. `setup-go` → `go-version-file: go.mod` (today: CI pins 1.21, go.mod demands 1.24 — works only via silent toolchain download).
  2. Node 18 → 22 (18 is EOL; Vite 6 supports 20/22).
  3. Pin Wails CLI to the version in go.mod (`wails/v2@v2.10.2`) instead of `@latest` — reproducible builds.
  4. macOS prepare step: **delete the bare-binary fallback branch** — if `build/bin/krokodyl.app` is missing, `exit 1` with a clear message. A bare mach-o binary is not double-clickable; shipping it is worse than failing.
  5. Ad-hoc sign: `codesign --force --deep --sign - build/bin/krokodyl.app` (no Apple account needed; gives the bundle a stable identity so Gatekeeper's right-click → Open path works cleanly on Apple Silicon).
  6. Package: `ditto -c -k --keepParent build/bin/krokodyl.app krokodyl-macos-universal.zip` (preserves bundle structure/xattrs; replaces tar.gz — zip opens with one click in Safari/Finder).
  7. Add `workflow_dispatch:` trigger so the pipeline is testable from a branch without tagging a release.
  8. Release step: add a `body` block to `softprops/action-gh-release` with macOS first-open instructions (kept alongside `generate_release_notes: true`); update asset-prep paths for the new zip name.
- **Mirror**: existing matrix/`if:` step structure (`release.yml:46-66`).
- **Validate**: YAML parses (`python -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" .github/workflows/release.yml` or actionlint if available); then a `workflow_dispatch` run on the branch must produce 3 artifacts incl. `krokodyl-macos-universal.zip` containing `krokodyl.app`.

### Task 4: README "Install on macOS"
- **Action**: Add an install section: download zip → unzip → right-click app → Open → Open (first time only). One sentence on why ("app is not notarized; macOS warns on unidentified developers"). Mention the log file location for bug reports. Keep existing dev docs untouched.
- **Mirror**: existing README heading style.
- **Validate**: rendered markdown reads correctly; instructions match the artifact name from Task 3.

### Task 5: macOS smoke test (acceptance for this milestone + data for milestone 2)
- **Action**: On real macOS hardware: download release artifact from the workflow_dispatch run → unzip → right-click Open (count steps; must be ≤1 beyond normal open) → paste a code (Cmd+V must work) → send a file Mac→Windows and receive Windows→Mac. If transfer fails, collect `~/Library/Caches/krokodyl/krokodyl.log` and record findings under the PRD's macOS open question.
- **Mirror**: PRD success-metric rows (macOS launch, macOS transfer).
- **Validate**: checklist below.

## Validation
```bash
# local (Windows dev box)
go build ./...
go vet ./...
cd frontend && npm run build && cd ..
wails build   # sanity: Windows artifact still builds

# pipeline (from branch)
gh workflow run release.yml --ref <branch>
gh run watch   # expect: linux + windows + macos artifacts; macos zip contains krokodyl.app
```
Manual macOS matrix (clean machine, Intel + Apple Silicon if available):
- [ ] unzip → right-click → Open works; no "damaged" dead-end
- [ ] App menu present; Cmd+V pastes into code field; Cmd+C copies the code; Cmd+Q quits
- [ ] File/directory dialogs open
- [ ] Mac→Win and Win→Mac transfer attempted; result + log captured (pass not required for M1 if engine bug — root cause recorded in PRD)

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| No macOS hardware in this dev loop — CI changes land blind | High | `workflow_dispatch` trigger makes pipeline testable per-branch; smoke test gates the milestone |
| `codesign --deep` deprecation warnings on newer Xcode | Medium | Warning only; ad-hoc signing of a flat Wails bundle is unaffected. Fallback: sign nested-first without `--deep` |
| Zip download still quarantined — Gatekeeper prompt remains | Certain (accepted in PRD) | That's the documented one step (right-click → Open); instructions in release body + README |
| Pinned Wails CLI version drifts from go.mod over time | Low | Single source: comment in workflow pointing at go.mod; milestone 2 (dependency refresh) bumps both together |
| Menu change affects Windows/Linux layout | Low | Menu built darwin-only at runtime; verify Windows build in validation |

## Acceptance
- [ ] All tasks complete
- [ ] Local validation commands pass; workflow_dispatch run produces all 3 artifacts, macOS zip contains a signed `.app`
- [ ] macOS smoke test executed; launch + paste criteria met; transfer result recorded in PRD (fix itself may defer to milestone 2)
- [ ] Patterns mirrored, not reinvented
