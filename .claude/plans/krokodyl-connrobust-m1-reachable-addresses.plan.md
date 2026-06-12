# Plan: Reachable Addresses — connect across virtual/multi-homed hosts

**Source PRD**: .claude/prds/krokodyl-connection-robustness.prd.md
**Selected Milestone**: 1 — Reachable addresses
**Complexity**: Medium

## Summary
Today a peer is dialed at a single address — the multicast packet's source — which on a Hyper-V/WSL/Docker host can be a virtual NAT address (`172.18.80.1`) that the other machine can't route to, so the offer dies with `dial … i/o timeout`. Fix: each host enumerates its own up, non-loopback, non-link-local unicast IPv4 addresses and advertises the **full list** in its discovery payload; the sender then tries every candidate (real-LAN-looking ones first) until the TLS+offer handshake succeeds. We don't need to perfectly classify "virtual" — advertising all candidates and letting the sender find the one that connects is what makes it robust. (Connect retry / relay fallback / EOF messaging is Milestone 2.)

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Payload struct | `discovery.go` `discoveryIdentity` + `decodeIdentity` validation | Add `Addrs []string`; validate count/length; tolerate empty (older peers) |
| Peer record | `discovery.go` `NearbyPeer`, `observe` | Carry `Addrs` onto the peer alongside the packet-source `Addr` |
| Cert-pinned dial | `nearby.go` `sendNearbyOffer` (TLS + `VerifyPeerCertificate` fingerprint pin) | Reuse per-candidate; pin still enforced on whichever address connects |
| Pure + tested helpers | `stall.go`/`staging.go` pure funcs with `_test.go` | `localUnicastIPs()` + `orderedCandidates()` unit-testable; a loopback dial test for multi-candidate |
| Logging | `app.go` logrus | Log candidate list + which address connected + per-candidate failure reason |

## Files to Change
| File | Action | Why |
|---|---|---|
| `netaddr.go` | CREATE | `localUnicastIPs()` (enumerate interfaces, filter) + `orderedCandidates(addrs, source)` (dedup + ordering) — pure/testable |
| `netaddr_test.go` | CREATE | Ordering/dedup table tests; `localUnicastIPs` excludes loopback/link-local and doesn't error |
| `discovery.go` | UPDATE | `discoveryIdentity.Addrs` + `NearbyPeer.Addrs`; populate, validate (cap N + length), store; bump `maxPayloadBytes` (512→~1024) for the address list |
| `nearby.go` | UPDATE | `sendNearbyOffer` takes a candidate list; tries each (short per-dial timeout) until handshake succeeds; aggregates errors; fingerprint pin unchanged |
| `nearby_test.go` | UPDATE | Multi-candidate: `[unroutable, 127.0.0.1]` connects via the reachable one; all-bad returns a useful error |
| `app.go` | UPDATE | Build this host's `Addrs` at `startNearby`; pass `peer.Addrs` (+ packet-source `Addr` as fallback) into the offer; log the chosen address |

## Tasks
1. **Address enumeration + ordering** (`netaddr.go`, pure): `localUnicastIPs()` → up, non-loopback, non-link-local (`169.254/16`), IPv4 unicast addresses. `orderedCandidates(payloadAddrs, packetSource)` → dedup; order real-LAN-looking (`192.168/16`, `10/8`) first, other private (incl. Hyper-V `172.16/12`) next, packet-source appended if not already present. Tests: ordering, dedup, empty.
2. **Advertise addresses**: `discoveryIdentity.Addrs` populated from `localUnicastIPs()` at `startNearby`; include in payload; `decodeIdentity` validates (≤8 addrs, each ≤45 chars); store on `NearbyPeer`. Bump `maxPayloadBytes` to 1024 and re-check the bye/health paths still fit. Tests: decode with/without addrs, oversized rejected.
3. **Multi-candidate dial**: `sendNearbyOffer(candidates []string, port, fingerprint, req, code)` loops candidates with a short per-dial timeout (~4 s), returns on first successful pinned handshake, else an aggregated error naming what was tried. Test: loopback server reached via the 2nd candidate after an unroutable 1st.
4. **Wire in**: `app.go` passes `orderedCandidates(peer.Addrs, peer.Addr)` to the offer; logs candidates + the address that connected (diagnostics for future edge cases).
5. **Validate**: builds, `-race`, harness loopback; manual on the Hyper-V machine (VM↔host and host↔host) — the offer now connects via a reachable address instead of timing out on `172.18.80.1`.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback regression
# manual on Hyper-V host: VM<->host + host<->host transfer connects (no lone 172.x dial timeout)
# log shows candidate list + which address connected
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Real LAN genuinely uses 172.x and we deprioritize it | Medium | Order, don't exclude — every candidate is still tried; 172.x just goes later |
| Many candidates slow the common case | Low–Medium | Short per-dial timeout; best-guess first; stop on first success |
| Payload exceeds multicast size with many IPs | Low | Cap to ≤8 addrs; bump `maxPayloadBytes`; IPv4 only keeps it small |
| Self-dial (host lists its own addr, two instances one host) | Low | Pin + offer handshake still validates the right peer; loopback works |
| Some adapters report transient/APIPA addresses | Low | Exclude link-local; only up interfaces |

## Acceptance
- [ ] Host advertises all reachable LAN IPv4 addresses; sender tries each and connects via a reachable one
- [ ] Hyper-V/WSL/Docker-laden host no longer fails solely by dialing a virtual/NAT address
- [ ] VM↔host and host↔host connect on the maintainer's machine; log shows the winning address
- [ ] `-race` + harness green; same-LAN/code flows unaffected; fingerprint pin still enforced
