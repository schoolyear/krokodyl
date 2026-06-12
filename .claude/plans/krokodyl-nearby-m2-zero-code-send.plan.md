# Plan: Zero-Code Send — click device, receiver accepts, transfer runs

**Source PRD**: .claude/prds/krokodyl-nearby-devices.prd.md
**Selected Milestone**: 2 — Zero-code send
**Complexity**: Large

## Summary
Clicking a nearby device opens the file picker; the sender offers the transfer to the peer over a direct TLS control connection (sender name, file names, total size). The receiver sees an accept/decline prompt; on accept, the code — generated internally, never shown to anyone — flows back over the same connection and both sides run the existing worker engine (croc's local path handles same-LAN transport). Decline and no-answer paths resolve cleanly on both ends. The code flow stays untouched as the remote fallback.

## Protocol decisions
- **Control channel**: each instance runs a TCP listener on a dynamic port, announced in the discovery payload (`{id, name, port}`). One JSON request/response per connection: offer in → `{accepted}` out; on accept the response carries nothing — the **code travels sender→receiver only after accept**, as a second message on the same connection.
- **Encryption**: ephemeral in-memory self-signed TLS (Go stdlib). Protects the code from passive LAN sniffing; active-MITM residual risk documented and addressed by PRD milestone 4 (identity display) — same trust posture as PairDrop on a LAN.
- **Timeouts**: receiver prompt auto-declines after 60 s; sender gives up waiting at 75 s. Sender transfer shows "waiting for {name} to accept".
- **One offer at a time per receiver** — a second incoming offer while one is pending is declined busy.
- **Engine reuse**: accept → receiver calls the existing receive path (default/remembered destination); sender runs the existing send worker with the pre-agreed code. Overwrite prompts, progress, cancel — all unchanged.

## Files to Change
| File | Action | Why |
|---|---|---|
| `nearby.go` | CREATE | TLS listener + offer server (pending-offer registry), offer client (dial/send/await), ephemeral cert, wire types |
| `nearby_test.go` | CREATE | Loopback integration: offer→accept→code handoff; decline; busy; malformed payloads; timeout |
| `discovery.go` | UPDATE | Identity/payload gains `port`; validation (1-65535) |
| `discovery_test.go` | UPDATE | Port validation cases |
| `transfers.go` | UPDATE | `FileTransfer.Peer` field — "to/from {device}" in UI |
| `app.go` | UPDATE | Start control listener before discovery; `SendToPeer(peerID, paths)`, `RespondToNearbyOffer(offerId, accept)` bindings; `nearby:offer` event; `performSend` takes explicit code; receiver auto-starts receive on accept |
| `frontend/src/App.svelte` | UPDATE | Chips clickable (+ keyboard); incoming-offer modal (sender, files, size, accept/decline); peer label on transfer rows; code spotlight hidden for peer sends |
| `frontend/src/locales/*.json` (6) | UPDATE | Offer modal, waiting/declined/busy strings, to/from labels |

## Tasks
1. **Wire types + pending offers** (`nearby.go` core): `offerRequest{senderName, files, size}`, `offerAnswer{accepted, busy}`, `codeMessage{code}`; pending-offer map with buffered chans (mirrors `overwriteResponses`); unit-tested.
2. **TLS listener + server flow**: ephemeral cert at startup; accept → decode (size caps) → emit `nearby:offer` → await user response (60 s auto-decline / busy if one pending) → reply; on accept send `codeMessage`, then receiver-side auto-start receive into remembered destination.
3. **Offer client + sender flow**: `SendToPeer` → registry lookup → create transfer (`Peer` set, status `waiting`, code spotlight suppressed) → dial (75 s overall deadline) → offer → on accept receive code → run existing send worker with that code; declined/busy/timeout → clear error states.
4. **Discovery payload port** + validation + tests.
5. **Frontend**: chip click/Enter → `SelectFiles` → `SendToPeer`; offer modal (reuses modal pattern); transfer rows show `→ name` / `from name`; strings ×6.
6. **Validation**: race tests incl. loopback TLS integration; builds; live two-instance zero-code transfer on this machine (the metric: 0 typed chars, ≤3 clicks); decline + timeout paths; harness loopback regression (code flow untouched).

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Two same-host senders collide on croc local relay ports | Medium | croc falls back to public relay automatically; live test covers |
| Plaintext-ish trust gap (active MITM) | Low | TLS now; PRD M4 adds identity surfacing; never auto-accept |
| Offer modal racing overwrite modal on receiver | Low | Independent state vars; visual stacking checked |
| Firewall blocks new TCP listener | Medium | Same health posture as discovery; offer dial fails fast → clear sender error |

## Acceptance
- [ ] Two instances, zero typed characters: chip → files → accept → transfer completes (existing progress/cancel UI)
- [ ] Decline, busy, and 60/75 s timeout paths leave both sides in clean terminal states
- [ ] Code flow (manual codes) fully intact; code spotlight never shows for peer sends
- [ ] `-race` suite green incl. loopback offer integration; harness regression green
