# Krokodyl Nearby — zero-code device picker + one-click resend

## Problem
Sending between two machines you control (or to a person in the same room) still requires generating, sharing, and pasting a transfer code — the most error-prone, highest-friction step in the app. AirDrop and PairDrop set the expectation: nearby targets just appear, you click one. Separately, repeating a transfer you just did (the dev-on-two-machines loop: build → send → test → fix → send again) costs the full pick-files-share-code ritual every single time.

## Evidence
- Maintainer request (2026-06-12): "like pairdrop and airdrop you can see all devices close to you so no need to share long code… also option to redo the transfer… when devving on 2 machines but don't want to click many times again."
- Maintainer's own two-instance testing this session required copying a code between windows on the *same machine* — friction observed live.
- The transfer library's local mode already does LAN peer discovery internally (multicast) — the capability exists below the UI but is never surfaced as a device list.
- Current history stores file *names*, not what's needed to repeat a send — "send again" is impossible today.

## Users
- **Primary**: (a) A developer with 2+ machines on one LAN shuttling builds/artifacts repeatedly between them; (b) two people in the same room/office network doing a one-off hand-off without wanting to read codes aloud.
- **Not for**: Cross-internet transfers — different networks keep using the code flow (LAN discovery cannot cross networks; that's physics, not scope-cutting). Not for unattended/headless receivers — a human accepts each incoming transfer.

## Hypothesis
We believe **a live "nearby devices" list (same network, app open on both ends) with one-click send + receiver accept prompt, plus a one-click "send again" on past sends** will **eliminate the code ritual for local transfers and collapse repeat transfers to a single click** for **same-LAN users and multi-machine developers**.
We'll know we're right when **a local send completes with zero characters typed in ≤3 clicks, peers appear within 5 seconds of both apps being open, and re-sending yesterday's files takes exactly 1 click**.

## Success Metrics
| Metric | Target | How measured |
|---|---|---|
| Local send friction | 0 typed characters, ≤3 clicks (pick device → pick files → receiver accepts) | Manual two-machine test |
| Discovery latency | Both peers listed ≤5 s after both apps open | Manual, two machines + two instances on one machine |
| Resend friction | 1 click from history entry to running transfer | Manual |
| Receiver safety | 100% of incoming nearby sends require explicit accept; decline leaves no file | Manual incl. decline path |
| Fallback intact | Code flow unchanged and visible when no peers found | Manual on isolated network |
| Same-machine instances | Two instances on one host see each other and can transfer | Manual (the maintainer's own test setup) |

## Scope
**MVP** — the minimum to test the hypothesis:
1. Nearby list: devices running krokodyl on the same network appear automatically with a human-readable name (hostname default); list updates live; shows nothing silently when network blocks discovery (state the situation + keep code flow prominent).
2. Send to device: click device → pick files (or drag onto the device entry) → receiver gets accept/decline prompt with sender name, file names, total size → accept starts the transfer through the existing engine; decline informs the sender.
3. One-click resend: send-history entries gain "send again" — re-sends the same file paths (current contents on disk); flags files that no longer exist instead of failing silently. Works with both device picker and code flow.
4. Identity basics: device shows OS hostname; visible only while app is open.

**Out of scope**
- Internet-wide device discovery — requires accounts/rendezvous server; codes already cover remote.
- Auto-accept / trusted devices ("always allow") — security posture first; revisit after MVP feedback.
- Device avatars, custom names, paired-device persistence — polish after the mechanism proves itself.
- Receiving-side "receive again" — codes are single-use by protocol design.
- mobile/web peers — unchanged from prior PRDs.

## Delivery Milestones
| # | Milestone | Outcome | Status | Plan |
|---|---|---|---|---|
| 1 | See each other | Two krokodyl instances on one LAN (or one machine) list each other by name within 5 s; list reacts to peers leaving | complete | [.claude/plans/krokodyl-nearby-m1-see-each-other.plan.md](../plans/krokodyl-nearby-m1-see-each-other.plan.md) |
| 2 | Zero-code send | Click device → files → receiver accept prompt → transfer runs on existing engine; decline path clean; code flow untouched as fallback | complete | [.claude/plans/krokodyl-nearby-m2-zero-code-send.plan.md](../plans/krokodyl-nearby-m2-zero-code-send.plan.md) |
| 3 | Send again | One click on any past send repeats it (device picker or code); missing files surfaced clearly | in-progress | [.claude/plans/krokodyl-nearby-m3-m4-resend-trust.plan.md](../plans/krokodyl-nearby-m3-m4-resend-trust.plan.md) |
| 4 | Trust & polish | Sender identity shown with local IP in accept prompt; visibility on/off toggle; remembered last target device preselected | in-progress | [.claude/plans/krokodyl-nearby-m3-m4-resend-trust.plan.md](../plans/krokodyl-nearby-m3-m4-resend-trust.plan.md) |

## Open Questions
- [ ] Windows Firewall will prompt on first listener/multicast use — acceptable one-time prompt, or pre-explain in-app? `TBD — observe during milestone 1 on this machine`
- [ ] Networks with AP/client isolation (offices, dorms, guest Wi-Fi) silently block multicast — is an in-app "can't see devices? use a code" hint enough? `Assumed yes for MVP`
- [ ] Resend stores absolute file paths in history on disk — privacy acceptable for a local file? `Assumed yes (single-user desktop app); flag in review`
- [ ] Same-LAN strangers appear in each other's lists (shared networks) — is hostname + IP in the accept prompt sufficient guardrail for MVP? `Assumed yes; auto-accept stays out of scope`

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Multicast blocked on user's network → empty list looks broken | Medium–High | High (feature looks dead) | Explicit empty-state explaining why + code flow always visible; never hide code UI |
| Spoofable peer announcements on shared LANs | Medium | Medium | Mandatory accept prompt with name + IP + size; no auto-accept in MVP; transfer itself keeps croc's PAKE security |
| Two instances on one machine collide (ports/identity) | Medium | Medium (breaks maintainer's test loop) | Per-instance identity + dynamic ports; explicit same-host test in matrix |
| Firewall prompt declined once → discovery dead forever, silently | Medium | Medium | Detect and surface "discovery unavailable" state instead of empty list |
| Stale resend paths (moved/deleted files) | High over time | Low | Pre-flight existence check; clear per-file error, partial send allowed |

---
*Status: DRAFT — requirements only. Implementation planning pending via /plan.*
