# Spike: receiving from Apple AirDrop / Android Quick Share

**Question:** can krokodyl appear as a target in a phone's native share sheet
(Apple AirDrop, Android Quick Share) so a user sends a file from the phone and
it lands in krokodyl?

**Verdict: NO — not in a shippable, cross-platform desktop app.** Evidence and
the shipped alternative below.

## Why native interop can't ship

### Apple AirDrop
- AirDrop runs over **AWDL** (Apple Wireless Direct Link) — a closed,
  undocumented Apple protocol. There is no public API to advertise as a
  receiver.
- The only open implementation is the SEEMOO lab research stack
  ([opendrop](https://github.com/seemoo-lab/opendrop) + [owl](https://github.com/seemoo-lab/owl)):
  - **macOS or Linux only — Windows is explicitly unsupported.** (No AWDL on Windows.)
  - OWL requires a **Wi-Fi card with monitor mode + frame injection** — most
    consumer adapters/drivers can't do this.
  - **No peer authentication; auto-accepts any file** (research-grade, unsafe).
  - "may be incompatible with future AWDL versions" — Apple changes it at will.
- A Wails desktop app cannot embed this and have it "just work," least of all
  on Windows. **Infeasible.**

### Android Quick Share / Nearby Share
- Uses Google's **Nearby Connections** — Protobuf wire format, **encrypted with
  Google's Ukey2**, multi-transport (BT/Wi-Fi/Wi-Fi Direct/WebRTC/NFC).
- Account-gated in recent versions; no open Go library to receive it; the only
  reverse-engineering is fragile and security-sensitive (a published
  [RCE attack chain](https://www.safebreach.com/blog/rce-attack-chain-on-quick-share/)).
- **Not shippable-reliable.**

### The deeper boundary
A phone's native share sheet can only target an **installed mobile app**.
krokodyl is a desktop app — there is no krokodyl phone app to register as a
share target. Native-sheet interop is therefore out of reach by construction,
independent of the protocol problems above.

## What ships instead (the working path)

**QR + web upload.** krokodyl runs a small, token-gated HTTP server on the
local network only while the user enables "Receive from phone." The phone
scans a QR code, a mobile browser upload page opens, the user picks files, and
they stream into krokodyl's normal staging/destination pipeline.

- Works from **any** iOS/Android phone, **no app install**.
- Runs over the shared Wi-Fi — or the offline hotspot from the Nearby-Direct
  feature, so it also works with no internet.
- Real "send from my phone to krokodyl," which is the user's actual goal —
  just not via the closed native protocols.

This is the same approach every cross-platform tool in this space uses
(LocalSend, PairDrop, Snapdrop): a local HTTP endpoint, not AirDrop/QuickShare
reimplementation.

*Status: native interop closed as infeasible (evidence above). Implementation
proceeds on the web-upload receiver.*
