# Strategy: compatibility with AirDrop-alternative apps

Goal: krokodyl should receive files from "the popular AirDrop clones." The
honest way to achieve "works with all of them" is NOT to reimplement every
app's protocol — most popular ones are **closed** and can't be interoped by
anyone. It's a **3-tier strategy** where one universal layer covers everything
and native adapters are added only where an *open* protocol exists.

## The landscape (2026)

| App | Protocol | Open? | krokodyl path |
|---|---|---|---|
| **Any phone browser** | HTTP | n/a | **Tier 1** — built (QR web upload) |
| **LocalSend** | LocalSend v2 (multicast + HTTP) | ✅ open | **Tier 2** — built |
| **Snapdrop / PairDrop** | WebRTC + signaling server | partly | covered by Tier 1 (they're web apps; the phone browser is the client) |
| **KDE Connect** | JSON packets over TLS (paired) | ✅ open ([Valent protocol ref](https://valent.andyholmes.ca/documentation/protocol.html)) | **Tier 2 candidate** |
| **Warpinator** | gRPC + protobuf + mDNS | ✅ open-ish (no formal spec) | **Tier 2 candidate** |
| **Apple AirDrop** | AWDL (closed) | ❌ | Tier 3 — impossible (see mobile-native-share spike) |
| **Google Quick Share / Nearby Share** | Nearby Connections, Ukey2 (closed) | ❌ | Tier 3 — impossible |
| **MobileTrans** (Wondershare) | proprietary commercial | ❌ | Tier 3 — closed, no interop |
| **SHAREit / Xender / Send Anywhere** | proprietary commercial | ❌ | Tier 3 — closed |

## The 3 tiers

### Tier 1 — Universal browser fallback (covers EVERYTHING)  ✅ built
krokodyl's QR + web-upload server (`webreceive.go`). Every phone — and every
one of these apps' users — has a browser. Scan the QR (or open the URL), upload
from the browser. **Zero per-app code, works regardless of which app the user
prefers, even the closed ones.** This is the real meaning of "compatible with
all of them": you don't integrate SHAREit, you give its users a browser upload
that always works.

### Tier 2 — Open-protocol adapters (native "it just appears in their app")
For apps with a documented open protocol, a receive adapter lets krokodyl show
up *inside* that app with no QR step. Each adapter plugs into the same seam:
opt-in lifecycle (`StartPhoneReceive`), human-accept consent (the `nearby:offer`
prompt), and the shared sanitized `saveUploadedFile`.
- **LocalSend** — ✅ built (`localsend.go`).
- **KDE Connect** — ✅ built + **E2E-validated**, GATED (`-tags krokodyl_kdeconnect`,
  off by default). Protocol codec tested (`kdeconnect_proto.go`); the real
  network adapter (`kdeconnect.go`) is exercised end-to-end over real loopback
  TLS sockets by `kdeconnect_e2e_test.go` (a counterpart speaking the actual
  KDE Connect wire protocol): identity exchange → share.request → payload TLS
  fetch → sanitized save, all proven. Remaining for real-APP interop: device
  pairing + cert pinning (replaces the current InsecureSkipVerify) — gated off
  until that's done + checked against the KDE Connect app.
- **Warpinator** — ✅ built + **E2E-validated**, GATED (`-tags krokodyl_warpinator`,
  off by default). gRPC stubs generated from `warpinator/warp.proto` (via buf,
  committed in `warpinator/warppb/`); the real gRPC server (`warpinator.go`) is
  exercised end-to-end by `warpinator_e2e_test.go` (a stub sender streams a
  chunked file): ProcessTransferOpRequest → consent → receiver pulls
  StartTransfer → chunks assembled → saved, all proven over real gRPC.
  Remaining for real-APP interop: zeroconf discovery + the group-code cert
  auth — see `.claude/spikes/warpinator-runbook.md`.

**What "validated" means here:** the transfer logic of each adapter is proven
to work over real sockets/gRPC against a conformant counterpart. The only thing
not yet done is bug-for-bug interop with the proprietary app itself (needs the
app + a second device + the auth/discovery layer), which is the documented
remaining work — not a gap in the transfer code.

### Tier 3 — Closed protocols (not supported, by necessity)
AirDrop, Quick Share/Nearby Share, MobileTrans, SHAREit, Xender, Send Anywhere.
Proprietary, often encrypted/account-gated, no public API; reverse-engineering
is fragile, security-sensitive, and breaks on vendor updates. **krokodyl does
not attempt these — their users are served by Tier 1.**

## Architecture that makes this true
All receive paths converge on one pipeline:
```
discovery (multicast / LocalSend announce / KDE / Warpinator mDNS)
   → human accept (nearby:offer prompt, one consent UI)
   → saveUploadedFile (sanitized, traversal-safe, deduped)
   → completed FileTransfer in the UI
```
Adding a Tier-2 adapter = a new discovery+protocol front-end feeding that same
pipeline. No adapter gets its own file-writing or consent logic.

## Recommendation
Tier 1 + LocalSend (both built) already deliver "works with all the popular
apps" for any real user. KDE Connect and Warpinator are nice-to-have native
integrations, but each is a sizeable protocol stack that can't be validated
without the actual app + devices — so they should be built deliberately
(gated/flagged until hardware-tested), not assumed working.
