# Warpinator adapter — finish + validate runbook

**Status: SCAFFOLD.** The hardware-free pieces are in `warpinator_proto.go`
(discovery constants, message shapes, the offer-summary helper) with unit
tests. The gRPC transport is NOT implemented here because it requires `protoc`
code generation and the real Warpinator app on a second device to validate —
neither possible in a headless build. This is the path to finish it; do not
claim Warpinator works until step 5 passes.

## Why it's not done in-repo
Warpinator's transport is gRPC over protobuf with zeroconf/mDNS discovery and a
cert-exchange auth step keyed by a shared "group code" (default `Warpinator`).
gRPC needs generated Go stubs (`protoc-gen-go` + `protoc-gen-go-grpc`); hand-
writing them is error-prone and unverifiable without the app. Adding a heavy
gRPC dependency that can't be exercised would bloat the shipped module for no
validated benefit.

## Steps (on a dev machine with two devices)

1. **Generate stubs** from the committed `warpinator/warp.proto`:
   ```bash
   protoc --go_out=. --go-grpc_out=. warpinator/warp.proto
   ```
   This produces `warpinator/warppb/*.pb.go`. Add `google.golang.org/grpc` and
   `google.golang.org/protobuf` to go.mod.

2. **Discovery**: advertise + browse `_warpinator._tcp` via a zeroconf library
   (e.g. github.com/grandcat/zeroconf), with the TXT records from
   `warpServiceTXT(hostname)` and port `warpDefaultPort` (42000).

3. **Auth**: implement the group-code cert exchange (CertServer on the auth
   port, `warpDefaultAuth`=42001) — the sender fetches the receiver's cert,
   encrypted with the group code. Pin it; this is Warpinator's real security
   boundary (analogous to LocalSend's fingerprint pin).

4. **Serve `Warp`** behind a build tag `krokodyl_warpinator` (gated off, like
   the KDE Connect and BLE adapters). Wire the RPCs:
   - `Ping`, `GetRemoteMachineInfo` → return `warpOurInfo(deviceName)`.
   - `ProcessTransferOpRequest` → `warpOfferSummary(req)` → the existing
     `localSendOffer` consent prompt → accept/decline. On accept, call back
     `StartTransfer` to pull the `FileChunk` stream and write each via the
     shared `saveUploadedFile` (relative_path sanitized — it already is by
     `safeUploadName`).
   - Start/stop with the same opt-in as the other receivers
     (`StartPhoneReceive`/`StopPhoneReceive`); reuse the `kdeStop`-style stored
     stop func.

5. **Validate**: install Warpinator on a phone/Linux box on the same LAN, enable
   krokodyl's "Receive from a phone", confirm krokodyl appears, send a file,
   confirm the consent prompt + the file landing in downloads. Only after this
   passes should the tag be considered shippable.

## Guardrails (carry over from the other adapters)
- All file writes go through `saveUploadedFile` (sanitized, traversal-safe).
- Consent via the shared `nearby:offer` prompt — never auto-accept.
- Bound concurrency; size-cap streams; time out idle connections.
- Pin the peer cert from the group-code exchange — no `InsecureSkipVerify` in a
  shippable build.
