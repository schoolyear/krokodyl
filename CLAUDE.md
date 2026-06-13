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
go build -tags krokodyl_ble ./...           # compile the gated Bluetooth radio — also part of the gate
go build -tags krokodyl_kdeconnect ./...    # gated KDE Connect adapter (E2E test: -tags krokodyl_kdeconnect)
go build -tags krokodyl_warpinator ./...    # gated Warpinator gRPC adapter (E2E test: -tags krokodyl_warpinator)

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

**Frontend** (frontend/src). Svelte 5 **runes** (`$state`/`$derived`) — most UI lives in one large `App.svelte`. Backend calls go through generated `wailsjs/` bindings; live updates arrive via `EventsOn` (`transfer:updated`, `transfer:overwrite`, `transfer:verify`, `nearby:updated`, `nearby:state`, `nearby:offer`, `transfer:cleared`). Blocking user prompts (overwrite, verify) are keyed by per-prompt UUID through `registerOverwritePrompt`/`resolveOverwrite` — reuse that plumbing for new prompts. svelte-i18n with 6 locales (en/nl/fr/es/hu/zh), theme store persisted to localStorage, `TitleBar.svelte` renders native Windows chrome + macOS traffic-light padding.

**Offline Nearby-Direct** (no-network transfer). `discoverysource.go` defines a `discoverySource` seam feeding `peerRegistry.observe` — multicast is one source, Bluetooth another; downstream (control channel + croc) never knows how a peer was found. `nearbydirect.go` is the hardware-free core (handshake codec, `resolveRole` host/join negotiation, `offlineSession` state machine — all unit-tested). **Bytes never cross Bluetooth** (croc is TCP/IP; BLE bulk is far too slow): BLE only carries discovery + a small handshake, then one device hosts a Wi-Fi hotspot (`hotspot.go`) whose credentials ride the handshake and the existing nearby+croc transfer runs over it. The real BLE radio (`nearbyble_on.go`, tinygo/bluetooth) is behind **`//go:build krokodyl_ble`** — OFF by default, so shipped builds never compile the Bluetooth dep or an unvalidated radio path; the default build is a no-op radio that degrades to guided manual hotspot pairing (`GetOfflineGuidance`). Constraints: **macOS BLE is central-only** (can't advertise → a Mac can only join); hotspot automation is per-OS (Windows/Linux scriptable, macOS guided manual); the handshake assumes one BLE write fits the negotiated MTU (chunking is a hardware-validation TODO). The radio + hotspot exec paths are **unvalidated without two machines + OS Bluetooth permission** — keep them gated and don't claim they work.

**LocalSend interop** (`localsend.go`). krokodyl speaks the open LocalSend v2 protocol **both directions** so the LocalSend app discovers krokodyl and vice-versa — multicast announce on **224.0.0.167:53317** + the v2 HTTP API (info/register/prepare-upload/upload/cancel). **Send side:** `readAnnouncements` feeds heard LocalSend devices into the shared `peerRegistry` via `observe` with a synthetic `discoveryIdentity{Kind:"localsend", ID:"ls-"+fingerprint, ...}`, so they appear in the same Nearby Devices list as krokodyl peers (badged "LocalSend" in the UI). `NearbyPeer.Kind`/`discoveryIdentity.Kind` discriminate; `sendToPeer` branches on it — `Kind=="localsend"` routes to `performLocalSendUpload` (prepare-upload → peer's user accepts → upload each file, pinned to the announced fingerprint via `localSendClient`) instead of the croc control-channel handoff. LS peers are non-`Resendable` (no krokodyl MachineID to re-target). Started with the same opt-in as the web receiver (`StartPhoneReceive`). Consent reuses the nearby-offer prompt: `prepare-upload` blocks on `localSendOffer` → emits `nearby:offer` → `RespondToNearbyOffer` routes to BOTH the croc nearby server and the LocalSend receiver (id matches one). Per-file tokens are issued only after accept, single-use, constant-time checked; uploads stream via the shared `saveUploadedFile`. Guardrails to keep: one pending offer at a time, session TTL, register-back is LAN-IP-only + semaphore-bounded (no SSRF/flood), copy session state under the mutex before streaming (concurrent uploads share the maps). **Multicast on Windows — two hard-won rules (`runMulticast`):**
- **RECEIVE: bind `0.0.0.0:port` + `ipv4.PacketConn.JoinGroup` per interface. Do NOT use `net.ListenMulticastUDP`** — on Windows it binds the *group address* and then receives NOTHING (verified on a real box: not even loopback), so krokodyl was deaf to the phone's announce, never registered back, and the phone never saw it. The official LocalSend app binds `0.0.0.0`, which is why it worked where we didn't. `SO_REUSEADDR` (via `controlReuseAddr` + `setReuseAddr`, build-tagged win/unix) lets us share the port with the LocalSend app.
- **SEND: `ipv4.PacketConn.SetMulticastInterface` per real interface before each `WriteTo`.** Do NOT use `DialUDP`/bound-local-addr — it does not reliably set `IP_MULTICAST_IF` on Windows, so the announce silently leaves via a virtual switch.
- Use **every real interface** (`multicastInterfaces`), not the OS default — on a Hyper-V/WSL/NAT box the default multicast interface is often a virtual switch the phone can't reach.
- **Port coexistence:** if TCP 53317 is busy (the official LocalSend app is running), bind an ephemeral port and announce it (`bound`) — LocalSend peers use the announced port, so the two run side by side.

**Cross-app receive strategy** (`.claude/spikes/cross-app-compatibility-strategy.md`). "Works with all the AirDrop clones" = a tiered model, NOT one protocol per app: Tier 1 the universal QR/browser web-upload (covers every phone/app incl. closed ones like AirDrop/QuickShare/MobileTrans/SHAREit — their users use a browser); Tier 2 native open-protocol adapters (LocalSend ✅; **KDE Connect** = tested codec + `kdeconnect.go` gated `//go:build krokodyl_kdeconnect`, **E2E-validated over real loopback TLS** (`kdeconnect_e2e_test.go`), OFF by default + flagged InsecureSkipVerify-pending-pairing — don't ship enabled; **Warpinator** = generated gRPC stubs (`warpinator/warppb/`, via buf) + `warpinator.go` gated `//go:build krokodyl_warpinator`, **E2E-validated over real gRPC** (`warpinator_e2e_test.go`); both adapters' transfer paths are proven against a conformant counterpart — only real-APP interop (KDE pairing/cert-pin, Warpinator zeroconf+auth) remains. Tier 3 closed protocols = not attempted. Every adapter feeds the SAME pipeline: discovery → `nearby:offer` consent → `saveUploadedFile`. Don't reverse-engineer closed protocols; don't give an adapter its own file-write/consent path.

**Phone → desktop receive** (`webreceive.go`). Apple AirDrop / Android Quick Share **cannot be received** in this app — closed, app-only protocols (AWDL needs monitor-mode Wi-Fi + isn't on Windows; Quick Share is Ukey2-encrypted Nearby Connections). See `.claude/spikes/mobile-native-share-feasibility.md`; don't re-attempt them. The shipped path is a **token-gated local HTTPS upload** (TLS via `ephemeralCertificate`, self-signed → one-time browser "not private" warning the user taps through; never plaintext). It is **always-on while the device is visible** (`ensureReceiving`, started at launch — not a manual toggle), so phones/LocalSend/etc. find krokodyl out of the box; `StartPhoneReceive` just returns the QR + URL. Files stream to the download dir via `safeUploadName` (path-stripped, traversal-rejected, deduped) and appear as completed transfers. A 128-bit `crypto/rand` token gates every request: it rides the QR page URL (`?t=`, so the QR can open the **reloadable** page — re-fetchable after the cert interstitial without re-scanning) and the upload POST prefers it in the `X-Krokodyl-Token` header (kept out of the upload request's URL / Referer / logs). The token-in-the-page-URL is the accepted QR tradeoff; do NOT re-add a single-use/cookie/redirect scheme — it broke real mobile browsers behind the self-signed-cert interstitial. Guardrails that must stay: `ReadHeaderTimeout` (slow-loris), `MaxBytesReader` caps a request, `maxConcurrentUploads` bounds simultaneous streams (429 when saturated), `Referrer-Policy: no-referrer` + `Cache-Control: no-store` on the page, and `stopReceiving` closes every server on visibility-off / shutdown.

## Frontend conventions

- A11y is a maintained baseline (WCAG 2.1 AA). New interactive UI needs an `aria-label`, inherits the global `:focus-visible`, and ≥24px target size.
- Modals use the `modalDialog` Svelte action (focus-trap + Escape + focus-restore) in App.svelte.
- Tabs/segmented controls use roving `tabindex` on the items — a keydown handler on the `role=tablist` container trips `svelte-check` (`a11y_interactive_supports_focus`).
- Color tokens: green TEXT uses `--color-accent-text`; white text sits on `--color-primary-strong` / `--color-danger-strong` backgrounds — never raw `--color-primary`/`--color-red`. Verify any contrast claim by computing the WCAG ratio (a review once mis-measured a passing token as failing).
- UI strings: add every key to ALL six locale files (`en nl fr es hu zh`) — svelte-i18n renders raw key ids for missing entries.

## Gotchas & constraints

- **Nearby trust model:** cert pinning authenticates the channel, NOT the identity — human confirmation is the backstop. Peer-supplied display strings MUST pass `sanitizeDisplayName` at decode; nearby receives are verified against the accepted offer (`describeOfferMismatch`); peer resends are two-phase (`ResendTransfer` → NeedsConfirm → `ConfirmResend`). Don't add a path around these.
- **No Apple notarization — hard constraint** (no Apple Developer account). macOS ships ad-hoc codesigned (`codesign --sign -`); users right-click → Open once. Don't add notarization steps.
- **Windows GUI-subsystem stderr is invalid.** A `-H windowsgui` binary launched by double-click has a dead stderr handle; if a worker inherits it, croc's progress writes fail and stall the transfer. Worker `cmd.Stderr` MUST stay `nil`; the parent logs to a file via `bestEffortWriter`. **Terminal-launched runs have a valid stderr and hide this bug** — verify transfer changes by launching the built GUI, not `go run`. `cmd/guiharness` reproduces the exact scenario.
- **Test nearby/transfer on TWO real machines.** Loopback / single-instance testing misses cross-network, "room not ready", and virtual-adapter (Hyper-V VM↔host) failures.
- **Windows Firewall blocks inbound until krokodyl has an allow rule — this is the #1 "phone can't see/reach the laptop" cause, NOT a code bug.** Default inbound action is *block*; with no rule, every inbound packet (LocalSend, QR webserver, nearby, croc) is silently dropped — discovery and reachability both fail in every direction even though sends succeed. The NSIS installer adds a PROGRAM-scoped inbound allow rule for `krokodyl.exe` (covers all ports) and removes it on uninstall. **Running the raw dev `build/bin/krokodyl.exe` has NO installer rule** — add one once (elevated): `New-NetFirewallRule -DisplayName krokodyl -Direction Inbound -Action Allow -Program "<path>\krokodyl.exe" -Profile Any` (persists across rebuilds since the path is stable). Note: `netsh advfirewall show ... state` can read OFF while the runtime `ActiveStore` is ON — trust ActiveStore / the fact that you get app firewall prompts.
- **`wailsjs/` is generated — never hand-edit.** Re-run the Wails generator when a Go struct exposed to the frontend changes.
- **`stagingDirForCode` determinism is load-bearing.** Changing its hash/salt orphans every in-flight partial transfer; there is no version field, so any change is breaking.
- **Linux build without `-tags webkit2_41`** builds against WebKit 4.0 and fails at runtime.
- **`app.go` (~1400) and `App.svelte` (~1750) exceed the 800-line guideline** — extract when you touch them, don't keep growing them.
- **Never `npm audit fix --force`** — it downgrades svelte-i18n breaking. The 5 esbuild-chain advisories are dev-server-only (the app ships static files); wait for vite to bump esbuild.

## Testing notes

- Unit tests must NEVER reach `runWorkerJob` — it spawns `os.Executable()` (the TEST binary) as a worker. Use `newTestApp()` (app_test.go) and injectable closures instead.
- `App` methods that emit go through `a.emitEvent` (nil-ctx safe); raw `runtime.EventsEmit` panics in tests.
- `recoveryBackoffFn` / `offerPromptWait` are package vars for collapsing waits in tests — tests that mutate them must not run parallel.

## Working in this repo (environment)

- **CI occasionally fails on a transient `sum.golang.org` TLS timeout** — that's a flake, not a code break; re-run the job.
- Commit subjects use conventional prefixes: `feat`/`fix`/`refactor`/`docs`/`test`/`chore`/`perf`/`ci`.
