# Plan: See Each Other — live nearby-device list

**Source PRD**: .claude/prds/krokodyl-nearby-devices.prd.md
**Selected Milestone**: 1 — See each other
**Complexity**: Medium

## Summary
Each running krokodyl instance announces itself on the local network (UDP multicast via `schollz/peerdiscovery` — already in the module graph through croc) and listens for others. A peer registry with liveness expiry feeds a live "Nearby" section on the Send tab through the existing event pipeline. Milestone 1 is *presence only* — clicking a device does nothing yet (that's milestone 2). Includes a blocked-discovery health check so an empty list never silently lies.

## Mechanism decisions (the load-bearing choices)
- **Transport**: `peerdiscovery` multicast+broadcast on a dedicated krokodyl port (constant, e.g. 42791) — never croc's own ports, so the croc CLI and our discovery don't trample each other.
- **Identity**: per-app-run UUID + `os.Hostname()` as display name; JSON payload `{id, name}`. Self filtered by UUID — which also makes two instances on one machine (the maintainer's test setup) list each other while excluding themselves.
- **Liveness**: announce every 2 s; peer expires 6 s after last sighting (meets the ≤5 s appear metric with margin); registry emits an event only when membership/names actually change.
- **Health check**: we listen with `AllowSelf` — hearing our *own* announcements proves the socket works. No self-echo within ~5 s → emit "discovery unavailable" state (firewall/AP-isolation) so the UI explains instead of showing a dead empty list.
- **Workers excluded**: discovery runs only in the GUI parent, never in `--transfer-worker` processes.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Module layout | `transfers.go` / `staging.go` / `settings.go` | One concern per file → `discovery.go`; manager struct with mutex-guarded map |
| Events | `app.go` `TransferEventUpdated` const + `EventsEmit` | New `NearbyEventUpdated` const; emit copies, never internal pointers |
| Lifecycle | `app.go` `startup`/`shutdown` | Start announcer/listener in startup, stop in shutdown |
| Emit-decoupling | `transfers.go` `newTransferManager(emit func(...))` | Registry takes an emit callback — unit-testable without Wails |
| Errors/Logging | `app.go` logrus patterns | Best-effort subsystem: log + degraded state, never crash the app |
| Frontend events | `App.svelte` `EventsOn('transfer:updated')` | Same subscription pattern for `nearby:updated` |
| i18n | 6 locale files | `nearby.*` keys in all locales |
| Tests | `transfers_test.go` table-driven + `-race` | Registry add/refresh/expire/self-filter/change-detection tests |

## Files to Change
| File | Action | Why |
|---|---|---|
| `discovery.go` | CREATE | Announcer, listener, peer registry w/ expiry, health state, event emission |
| `discovery_test.go` | CREATE | Registry logic: appear, refresh, expire, self-filter, no-op change suppression; payload round-trip |
| `app.go` | UPDATE | Wire discovery into startup/shutdown; `GetNearbyPeers()` binding; `NearbyEventUpdated`/`NearbyEventState` consts |
| `go.mod` | UPDATE | `schollz/peerdiscovery` indirect → direct |
| `frontend/src/App.svelte` | UPDATE | "Nearby" section on Send panel: live device chips (name + monogram), empty state, unavailable state with code-flow hint |
| `frontend/src/locales/*.json` (6) | UPDATE | `nearby.title/empty/unavailable/hint` |

## Tasks

### Task 1: Peer registry + payload (`discovery.go` core, pure logic)
- **Action**: `nearbyPeer{ID, Name, Addr, lastSeen}`; `peerRegistry` (mutex map) with `observe(payload, addr)`, `sweep(now)` expiry, `snapshot()` sorted by name; change detection → emit callback only on real change. JSON payload codec with size sanity check.
- **Mirror**: `transferManager` shape.
- **Validate**: `go test -race ./...` — table-driven registry tests.

### Task 2: Announce + listen loops
- **Action**: `startDiscovery(identity, emit)` → two goroutines: peerdiscovery announce (2 s interval, payload = identity JSON) and listen (Notify → registry.observe; AllowSelf on, self filtered by UUID but recorded for the health check); sweeper ticker (1 s). Health: no self-sighting in 5 s → state `unavailable`, recovers if echoes appear. `stopDiscovery()` for shutdown. Dedicated port constant.
- **Mirror**: worker poller goroutine + `stop func()` pattern (`worker.go` `startProgressPoller`).
- **Validate**: build; single-instance run shows own health OK (self-echo), zero peers.

### Task 3: App wiring + binding
- **Action**: startup: build identity (uuid + hostname), start discovery with emit → `EventsEmit(NearbyEventUpdated, peers)` / `NearbyEventState`. shutdown: stop. `GetNearbyPeers()` returns snapshot for initial render. Skip entirely in worker mode (already structurally true — workers never reach startup).
- **Mirror**: existing startup sequence + event consts block.
- **Validate**: `go vet`, race tests; bindings regenerate via `wails build`.

### Task 4: Nearby UI (Send panel)
- **Action**: Section above the drop zone: title, horizontal wrap of device chips (monogram circle from first letter + hostname). States: ≥1 peer → chips (non-interactive this milestone, `aria-disabled`, subtle "soon" cursor); 0 peers + discovery OK → quiet empty hint ("No nearby devices — open krokodyl on the other machine, same network"); unavailable → amber hint ("Discovery blocked on this network — use a code below"). Live via `EventsOn('nearby:updated')` + initial `GetNearbyPeers()`. 320 px + short-height behavior per M1 rules.
- **Mirror**: transfer-list rendering + status-driven styling.
- **Validate**: svelte-check 0/0; visual check at 320/800×450.

### Task 5: Live two-instance verification (the metric)
- **Action**: `wails build`; launch two instances on this machine → each lists the other (hostname) ≤5 s; kill one → other's list drops it ≤6 s; firewall prompt behavior observed and recorded in PRD open question. GUI-harness loopback regression.
- **Validate**: stopwatch + observation; PRD updated with firewall finding.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# two local instances: mutual listing ≤5s, departure ≤6s
# harness loopback still green
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Windows Firewall blocks UDP listener silently | Medium | Self-echo health check → explicit "unavailable" UI state; finding recorded for PRD |
| Two same-host instances can't share the multicast socket | Medium | peerdiscovery uses reuse-addr (croc relies on this); explicit Task 5 test; fallback: add broadcast mode |
| Announce spam on hostile LANs (payload abuse) | Low | Payload size cap + JSON validation before registry; name length clamp |
| Chip section crowds the 320 px layout | Low | Wrapping chips + M1 narrow rules; section collapses when empty |

## Acceptance
- [ ] Two instances on this machine list each other ≤5 s; departure detected ≤6 s
- [ ] Self never listed; names = hostname; list stable (no flicker) under steady announcements
- [ ] Blocked discovery shows the explicit unavailable state, code flow visibly intact
- [ ] Registry tests green under `-race`; harness loopback regression green
- [ ] Device chips visibly present but inert (milestone 2 wires clicks)
