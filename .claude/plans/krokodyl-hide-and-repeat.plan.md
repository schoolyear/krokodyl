# Plan: Instant Hide & Reliable Repeat-Transfer

**Source PRD**: .claude/prds/krokodyl-hide-and-repeat.prd.md
**Selected Milestones**: 1 — Hide sticks instantly, 2 — Reliable repeat (combined; both live in the nearby subsystem and share `discovery.go`/`app.go`)
**Complexity**: Medium

## Summary
Fix the hide bounce by making the goodbye **authoritative on the receiver**: after a peer says goodbye, suppress re-adding it for a short window so late/in-flight normal announcements can't resurrect it. Make repeat reliable by giving every install a stable **opaque machine ID** (persisted, privacy-safe — not hostname/MAC), announcing it in discovery, recording it on send transfers, and matching repeats on it instead of the now-random display name; when the target machine isn't reachable, fall back to a normal code send with a clear message instead of failing.

## Root causes (already traced in this codebase)
- **Bounce**: `SetNearbyVisible(false)` → old discovery `stop()` fires `broadcastGoodbye` + closes the announce loop, but a normal announce already in flight arrives at the *other* device just after the goodbye → receiver removes (good) then re-adds (bad), then waits out `peerTTL` (~5 s) → the reported 4–6 s. Sender-side timing can't recall in-flight UDP; the fix belongs on the receiver.
- **Repeat targeting**: `ResendTransfer` → `findPeerByName(t.Peer)`. Display names became random per launch, so after the receiver restarts the name no longer matches → "not nearby". No stable key exists to match on.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Registry state | `discovery.go` `peerRegistry` (mutex map, `observe`/`sweep`/`snapshot`) | Add a `byeUntil` map guarded by the same mutex |
| Persisted settings | `settings.go` `appSettings`, `loadSettings`/`saveSettings` | Add `MachineID`, generated once and persisted |
| Stable-id generation | `app.go` `uuid.NewString()`; `names.go` crypto/rand | Opaque random token, persisted on first run |
| Payload validation | `discovery.go` `decodeIdentity` (size/port/fingerprint checks) | Validate new `machineId` length; tolerate absent (older peers) |
| Resend + fallback | `app.go` `ResendTransfer`, `SendFiles`, `SendToPeer` | Match by machine id; gone → `SendFiles` (code) + message |
| Transfer fields | `transfers.go` `FileTransfer.Peer`, `Paths`, `Resendable` | Add `PeerMachineID` (persisted, stripped of nothing sensitive) |
| Tests | `discovery_test.go`, `settings_test.go` table-driven `-race` | Suppression-window + machine-id persistence tests |

## Files to Change
| File | Action | Why |
|---|---|---|
| `discovery.go` | UPDATE | `byeUntil` suppression window in `observe`; carry `MachineID` in identity/payload + validate; goodbye sends a couple packets across the window |
| `discovery_test.go` | UPDATE | bye suppresses re-add within window; re-appears after window; machine-id in payload round-trip |
| `settings.go` | UPDATE | `MachineID` field + `ensureMachineID(path)` (generate once, persist) |
| `settings_test.go` | UPDATE | machine id is created once and stable across loads |
| `transfers.go` | UPDATE | `FileTransfer.PeerMachineID` |
| `app.go` | UPDATE | resolve+persist machine id at startup; put it in identity + `NearbyPeer`; record `PeerMachineID` on peer sends; `ResendTransfer` matches by machine id with code fallback + message |
| `frontend/src/App.svelte` | UPDATE | make the repeat control unmistakable (labelled, slightly more prominent); surface the "device gone → sent with a code instead" message |
| `frontend/src/locales/*.json` (6) | UPDATE | fallback message string(s) |

No engine/worker/protocol-crypto changes; control-channel pinning untouched.

## Tasks

### Task 1 — Authoritative hide (Milestone 1)
- **Action**: Add `byeUntil map[string]time.Time` to `peerRegistry`. On a `bye` observation: delete peer, set `byeUntil[id] = now + byeSuppression` (e.g. 3 s), emit. On a normal observation: if `now < byeUntil[id]`, ignore (a late announce can't resurrect a departed peer); once past the window, clear the entry and proceed normally. Have `broadcastGoodbye` emit 2–3 packets spaced so at least one lands. `sweep` also prunes stale `byeUntil` entries.
- **Mirror**: `observe`/`sweep` mutex + change-emit pattern.
- **Validate**: `go test -race` — bye then immediate normal announce → peer stays gone; normal announce after the window → peer reappears. Live: press hidden, watch other screen — gone ≤1 s, no reappearance over 10 s.

### Task 2 — Stable machine identity (Milestone 2a)
- **Action**: `settings.go` `MachineID` (opaque `uuid`); `ensureMachineID(path)` creates+persists on first run, returns existing otherwise. `app.go startNearby`: load it, put in `discoveryIdentity.MachineID` and `NearbyPeer.MachineID`. `decodeIdentity`: accept and length-check it; tolerate empty from older peers (those just won't be repeat-matchable). Stays opaque — never hostname/MAC.
- **Mirror**: `settings` load/save; identity construction.
- **Validate**: `go test -race` — machine id stable across `loadSettings`; two instances on one host share the same machine id (same install) — note: same-host windows are the same *machine*, which is correct for "same machine" repeats.

### Task 3 — Repeat matches machine, falls back to code (Milestone 2b)
- **Action**: `transfers.go` add `PeerMachineID`; `SendToPeer`/`performPeerSend` record `peer.MachineID` on the transfer (persisted via history). `ResendTransfer`: if the original was a peer send, find a live peer by `PeerMachineID` (new `findPeerByMachineID`); if present → `SendToPeer`; if absent → `SendFiles` (code flow) and set a message on the new transfer like "{name} isn't nearby — created a code instead" so the user isn't stuck. Missing-files preflight stays. Keep `findPeerByName` only as a last-resort fallback for old history entries lacking a machine id.
- **Mirror**: existing `ResendTransfer` structure + `SendFiles`/`SendToPeer`.
- **Validate**: `go test -race`; live: send to other window → restart that window (new random name) → repeat still reaches it (machine id); close that window → repeat creates a code with the explanatory message.

### Task 4 — Frontend clarity (Milestones 2)
- **Action**: Make the row repeat control clearly a "Send again" action (labelled button, primary-tinted, not just a faint icon). Surface the fallback message (already flows via the transfer's error/info field or a toast). Strings ×6.
- **Mirror**: existing row action buttons + toast.
- **Validate**: svelte-check 0/0; build; visible obvious control.

### Task 5 — Validation pass
- **Action**: full builds; `go test -race`; GUI-harness loopback regression; relaunch two instances; manual hide + repeat + restart + gone scenarios.
- **Validate**: commands below green; PRD metrics met.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback regression
# manual: hide → gone <=1s, no reappearance 10s
# manual: send → restart receiver → repeat reaches same machine
# manual: send → close receiver → repeat makes a code + clear message
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Suppression window hides a genuinely-returned peer briefly | Low | Short window (~3 s); peer reappears right after; toggling visible re-announces |
| Goodbye lost entirely on lossy Wi-Fi | Low–Medium | Receiver suppression + a couple goodbye packets; TTL still the crash fallback |
| Machine-id privacy on shared LAN | Low | Opaque random token; not hostname/MAC; only for matching, shown nowhere |
| Old history entries (no machine id) can't machine-match | Low | Name fallback retained for legacy rows; new sends carry the id |
| DHCP IP reuse mis-targets | N/A | We match on the opaque id, not IP |

## Acceptance
- [ ] Hidden removes the device from others ≤1 s with zero reappearance (10 s watch)
- [ ] Repeat reaches the same machine even after it restarts (new random name)
- [ ] Device gone → repeat creates a code with a clear message; never silent
- [ ] Repeat control is obvious/labelled
- [ ] `-race` + harness green; discovery liveness (appear/depart) unregressed; mac/Linux + crypto untouched
