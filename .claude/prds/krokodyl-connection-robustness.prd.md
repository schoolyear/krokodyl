# Krokodyl — Connection Robustness (multi-homed hosts, connect failures, relay fallback)

## Problem
Cross-machine transfers fail to even connect in common real setups. On a host with virtual network adapters (Hyper-V, WSL2, Docker, VPN), krokodyl announces an unroutable address (e.g. the Hyper-V `172.18.80.1` NAT switch), so the other machine dials a dead end and the transfer never starts — the recipient "never gets any message". When the local path is blocked by these split virtual networks, the relay fallback isn't reliably taking over either, surfacing as an immediate `receiving failed: EOF`. The result: a transfer that should work silently can't connect.

## Evidence
- Sender log (other machine → main, 2026-06-12): `could not reach device: dial tcp 172.18.80.1:51415: i/o timeout` (×2). `172.18.80.1` is a Hyper-V Default Switch host-only address — not reachable from the peer's real LAN.
- Receiver log (×3): `receiving failed: EOF` at 0% — the croc handshake never completed; the two ends couldn't establish a connection (local path blocked, relay didn't land).
- Maintainer (2026-06-12): "from hyper-v vm to main machine failed, main machine never got any message"; "we must make the most stable, catch every edge case ever".
- Grounded in this codebase: discovery records a peer's address from the multicast packet source (`peerdiscovery.Discovered.Address`); on a multi-homed host the announcement can go out a virtual adapter, so the advertised control-channel address is a virtual/NAT IP. The sender dials that single address with no alternative.
- Working as intended (not this PRD): `connection lost — transfer stalled` is the stall watchdog firing correctly.

## Users
- **Primary**: Developers and power users whose machines have Hyper-V / WSL2 / Docker Desktop / VPN adapters (very common) transferring on a real LAN; VM-to-host transfers; anyone behind a NAT/virtual-network split.
- **Not for**: Deliberately offline machines, or transfers where no network path exists at all (we can only fail clearly there).

## Hypothesis
We believe **advertising only reachable addresses (and trying every candidate), making the connect phase resilient (retry + reliable relay fallback), and reporting connect failures clearly** will **let cross-machine transfers actually connect on multi-homed / virtual-network hosts** for **developer and VM-heavy setups**.
We'll know we're right when **a VM↔host and a Hyper-V/WSL/Docker-laden host↔host transfer connects and completes (or cleanly resumes), the recipient always receives the offer when a real network path exists, and connect failures explain the cause instead of a bare EOF**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Connect on multi-homed host | Offer reaches the peer and transfer connects on a host with Hyper-V/WSL/Docker adapters present | Manual on the maintainer's Hyper-V machine |
| VM↔host transfer | A Hyper-V VM ↔ host transfer connects and completes | Manual VM test |
| No unroutable dials | The sender never fails solely because it dialed a virtual/NAT-only address while a reachable one existed | Log review (no lone `dial … i/o timeout` to 172.x/virtual when a LAN addr was available) |
| Clear connect errors | A genuine connect failure says why (no path / firewall / both behind NAT), not bare `EOF` | Manual |
| Relay fallback | When the local path is blocked, the relay path connects | Manual: two machines on networks with no direct route |
| No regression | Same-LAN and code-flow transfers still work | Manual + harness |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Reachable-address advertising: enumerate the host's interfaces, exclude loopback/down/link-local and known-virtual ranges, and advertise the real LAN address(es). Where ambiguity remains, advertise **multiple** candidate addresses.
2. Multi-candidate connect: the sender tries each advertised address (and the packet-source address) until one connects, rather than failing on the first unroutable one.
3. Connect-phase resilience: a short retry on transient connect/handshake failures; ensure the relay fallback is actually exercised when the local path can't connect; turn bare `EOF` into an actionable message.
4. Diagnostics: log the candidate addresses, which one connected, and the concrete reason on failure, so future edge cases are debuggable from the log.

**Out of scope**
- Hole-punching / STUN/TURN beyond what croc's relay already provides — rely on croc's relay for NAT traversal.
- Manual IP/interface entry UI — auto-selection only for now (revisit if needed).
- IPv6-only networks as a primary target — keep IPv6 working where croc already supports it, but don't expand scope.
- Guaranteeing connectivity when no network path exists — we can only fail clearly.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Reachable addresses | Discovery advertises real LAN address(es), not virtual/NAT adapters; the sender tries every candidate and connects via a reachable one | in-progress | [.claude/plans/krokodyl-connrobust-m1-reachable-addresses.plan.md](../plans/krokodyl-connrobust-m1-reachable-addresses.plan.md) |
| 2 | Connect resilience | Transient connect/handshake failures retry; relay fallback reliably engages when local can't connect; bare EOF becomes an actionable message; rich connect diagnostics in the log | pending | — |

## Open Questions
- [ ] **Virtual-range detection**: exclude by well-known ranges (Hyper-V `172.16–31.x` Default Switch, WSL, Docker `172.17.x`, link-local `169.254.x`) or by interface attributes (no default route, virtual driver name), or both? `TBD — /plan; attributes are more robust than hardcoded ranges`
- [ ] **Multi-address transport**: does the control-channel payload carry a list of IPs, and the sender races/sequences them? And should croc's own local-discovery be pointed at the right interface too? `TBD — /plan`
- [ ] **Relay fallback trigger**: is EOF specifically the "local failed, go relay" signal, or do we always run relay in parallel? croc has both local and relay — confirm our options actually fall back. `TBD — needs croc-options review`
- [ ] **Same-host VM**: a Hyper-V VM and its host share the virtual switch — is that path even expected to work via relay, or only via the host-only network? `TBD — VM test will tell`
- [ ] How aggressive should connect retry be (count/backoff) before failing? `Assumed: a few quick retries over ~10–15 s`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Excluding a "virtual" range hides a legitimate adapter (some LANs use 172.x) | Medium | Medium | Prefer interface attributes + advertise multiple, rather than hard-excluding a whole range; let the sender try all |
| Trying many addresses slows the common case | Low–Medium | Low | Try the most-likely (packet source / default-route iface) first; short per-candidate timeout |
| Relay fallback adds latency/dependency on the public relay | Medium | Medium | Keep local-first; relay only when local can't connect; surface relay errors |
| croc library limits our control over interface/relay behavior | Medium | High | Review croc options (RelayAddress, OnlyLocal/DisableLocal, IP); may need to set the local IP croc binds/announces |
| "Every edge case" is unbounded | High | Low | Target the observed failure classes + defensive clear errors + diagnostics; iterate from real logs, not exhaustively upfront |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
