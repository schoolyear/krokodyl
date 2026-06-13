# Offline Nearby-Direct (Bluetooth-discovered, Wi-Fi transfer)

## Problem
krokodyl can only find and reach peers over a shared local IP network (multicast discovery + croc over TCP/IP). Two people in the field with no internet **and** no shared Wi-Fi — two laptops in a desert, a conference with locked-down Wi-Fi, a power cut — currently cannot transfer at all: discovery sees nothing and croc has no route. The product promises "AirDrop-like, no cloud, no size limit," but silently fails in exactly the infrastructure-free situation where a direct tool should shine.

## Evidence
- User request (verbatim intent): "the airdrop needs to work for Bluetooth too if I'm in a desert we have no internet, this is missing."
- Architectural fact: `discovery.go` binds multicast on the local interface; `worker.go` runs croc, which needs an IP route. No shared L2/L3 network → both are dead. (Verified in code this session.)
- Reference: LocalSend — the leading open offline P2P app — *requires* a shared network and documents "make a manual hotspot" for this case. Confirms the gap is universal in this app class, and that the fix is network bootstrapping, not a new transport. ([localsend/protocol](https://github.com/localsend/protocol))

## Users
- **Primary**: an existing krokodyl user, two devices, no internet and no shared Wi-Fi, who wants to move files now without setting up infrastructure by hand. Trigger: opens krokodyl, sees no nearby devices, has no network to join.
- **Not for**: cross-room/cross-building transfers (out of BLE + single-hotspot range); phone↔desktop (krokodyl is desktop-only); users who already share a Wi-Fi/LAN (today's nearby flow already serves them, offline included).

## Hypothesis
We believe **a Bluetooth-discovered, Wi-Fi-carried "Nearby-Direct" mode** — where BLE finds the other device and hands over the credentials to join a one-tap host hotspot, after which the existing nearby+croc transfer runs over that hotspot — will let two devices **transfer with zero pre-existing network** for **users in the field with no internet**.
We'll know we're right when **two laptops with Bluetooth, no internet, and no shared Wi-Fi complete a multi-hundred-MB transfer at Wi-Fi speed**, with the only manual step being an OS-level hotspot/permission grant where the platform forbids automation.

## Why this shape (constraints, not choices to revisit)
- **Bytes never cross Bluetooth.** croc is TCP/IP-only and BLE bulk is ~0.1–1 Mbps — it would destroy the big-files promise. BLE carries only discovery + a ≤512B handshake (device name, role, SoftAP SSID+PSK, control port, cert fingerprint, croc code).
- **Transfer leg = SoftAP/hotspot, not Wi-Fi Direct.** Cross-platform Wi-Fi Direct is infeasible (macOS exposes no public AWDL/Wi-Fi-Direct API). One device hosts an access point; the other joins; existing multicast + croc then works relay-free.
- **BLE role constraint** (`tinygo.org/x/bluetooth`): a process is central **or** peripheral, not both at once → discovery needs explicit role negotiation (deterministic host/join by device-id ordering, or alternating advertise/scan).
- **Trust model unchanged:** BLE radio is unauthenticated, so the human-confirm + TLS cert-fingerprint-pinning backstop stays. The fingerprint pre-shared over BLE is still verified on the TLS control channel once on Wi-Fi.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Field transfer with no infra | 2 laptops, no internet/Wi-Fi, transfer completes | Manual two-machine test (hardware) |
| Transfer speed once paired | Indistinguishable from today's LAN nearby | Same croc path — Wi-Fi-bound, not BT |
| Graceful degradation | No BT / permission denied → clear message + fall back to shared-network nearby | Unit + manual |
| Core logic correctness | Handshake codec, role negotiation, state machine, transport seam | `go test -race` (no hardware) |
| Regression | Existing online/LAN nearby + code transfers unchanged | Existing suite stays green |

## Scope
**MVP** — "Nearby-Direct" mode the user can turn on when no network exists:
1. A discovery-source abstraction so peers can arrive from multicast **or** BLE (today's multicast becomes one implementation).
2. BLE presence advertise + scan with deterministic role negotiation (host vs join).
3. A versioned ≤512B BLE handshake payload (name, role, SSID, PSK, control port, fingerprint, code) with encode/decode + validation, sanitized like every other peer-supplied string.
4. Host side: bring up a SoftAP (programmatic where the OS allows: Windows Mobile Hotspot, Linux nmcli; macOS = guided manual Internet Sharing with on-screen steps). Join side: connect to it (auto where allowed, else guided).
5. Once both are on the hotspot, reuse the **existing** nearby control channel + croc transfer unchanged, with the BLE-pre-shared fingerprint verified on TLS.
6. UI: a mode entry point, pairing/role state, a join-instructions modal (SSID/PSK/QR + manual steps per OS), progress, and graceful-fallback messaging — WCAG 2.1 AA, all 6 locales.
7. Feature flag + capability detection: absent/denied Bluetooth or hotspot → mode is offered as "manual hotspot" guidance and the app stays fully usable on shared networks.

**Out of scope**
- File bytes over Bluetooth — infeasible and off-promise (speed).
- Cross-platform Wi-Fi Direct / AWDL — no public macOS API.
- Fully automatic hotspot on macOS — OS restricts it; guided manual only.
- Mobile/phone peers — desktop app.
- Multi-peer (>2) offline mesh — one host, one joiner for MVP.
- BLE pairing/bonding/encryption beyond the existing TLS-fingerprint trust model.

## Delivery Milestones
<!-- Business outcomes, not engineering tasks. /plan turns each into a plan. -->

| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | Discovery-source seam | Peers can come from pluggable sources; multicast refactored behind it, zero behavior change online | pending | — |
| 2 | BLE handshake protocol (HW-free core) | Versioned payload codec + role negotiation + offline state machine, fully unit-tested | pending | — |
| 3 | BLE radio integration | Real advertise/scan/connect via tinygo bluetooth; capability detection + graceful absence | pending | — |
| 4 | Hotspot bootstrap + handoff | Host SoftAP + join (auto where allowed, guided elsewhere); existing nearby+croc runs over it | pending | — |
| 5 | Nearby-Direct UI | Mode toggle, pairing/role/join-instructions UI, fallback messaging; AA + 6 locales | pending | — |

## Open Questions
- [ ] Role negotiation: deterministic by lower/higher device-id (simple, no flapping) vs. user picks "Host"/"Join" explicitly (clearer, one tap)? Likely: explicit choice in UI, device-id tiebreak as fallback.
- [ ] How much hotspot creation can actually be automated per OS without elevation? (Windows Mobile Hotspot WinRT may need a signed-in account; Linux nmcli needs NetworkManager; macOS = none.) Determines how much is "guided" vs "one-tap."
- [ ] Does adding `tinygo.org/x/bluetooth` build cleanly in the release CI matrix (CGo on macOS for CoreBluetooth)? Must not break the existing headless build.
- [ ] Should Nearby-Direct auto-activate when discovery finds nothing for N seconds, or stay an explicit user action? (Explicit for MVP — avoids surprising radio/hotspot activation.)

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| BLE radio path can't be CI/headless-validated | Certain | Med | Split scope: unit-test the codec/negotiation/state machine; gate radio behind capability detection; flag hardware path for two-machine validation in the test plan |
| `tinygo/bluetooth` CGo breaks release build (esp. macOS) | Med | High | Behind a build tag / lazy-init; verify the build matrix before wiring deep; keep app fully functional with the dependency absent |
| Programmatic hotspot blocked by OS/permissions | High (macOS certain) | Med | Guided-manual fallback with SSID/PSK/QR + per-OS steps; never assume automation |
| BLE central-XOR-peripheral causes discovery deadlock | Med | Med | Explicit host/join role (not simultaneous), device-id tiebreak, timeout→retry with role swap |
| Feature creep into a second transport stack | Med | High | Hard rule: BLE only bootstraps an IP network; the transfer engine (croc) and control channel (TLS) are reused unchanged |
| Unauthenticated BLE handshake spoofed on LAN | Med | High | Keep human-confirm + TLS fingerprint pinning; treat BLE-supplied fingerprint as a hint verified on the TLS channel, not as trust |

---
*Status: DRAFT — requirements only. Implementation planning via /plan. Running autonomously: gates self-answered from the provided brief; radio + hotspot paths explicitly require two-machine + OS-permission validation the loop cannot perform.*
