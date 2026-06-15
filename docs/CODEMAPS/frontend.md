<!-- Generated: 2026-06-15 (v0.17.3, feat/offline-bluetooth-discovery) | Files scanned: 17 src | Token estimate: ~820 -->
# Frontend (Svelte 5 + TS + Vite 6)

SPA bundled into the Go binary. Svelte 5 runes (`$state`/`$derived`), no Redux/Pinia.

## Component tree
```
main.ts  mount(App, #app)
└─ App.svelte (~1900 lines — most UI/state here)
   ├─ TitleBar.svelte        brand · ThemeSwitcher · lang <select>
   ├─ ThemeSwitcher.svelte   ☀️/🌙 toggle
   ├─ Firewall banner        (Windows, when FirewallNeedsFix) → "Fix" → FixFirewall (UAC)
   ├─ Tabs: Send | Receive   (ARIA tablist, roving tabindex)
   ├─ Phone-receive bar       persistent above both tabs → startPhoneReceive (always-on)
   │   Send:    code spotlight · nearby peer list (LocalSend peers badged) · visibility toggle
   │   Receive: code input (no auto-submit) · destination picker
   ├─ Transfer list          status badge · progress bar · cancel/resend · "Open in folder" (completed → RevealInExplorer)
   └─ Modals (use:modalDialog focus-trap): overwrite · nearby offer · phone-QR ·
      clear-history · verify (offer-mismatch keep/discard) · resend-confirm · offline-guidance
   └─ Toast (role=alert/status, aria-live)
```

## State
```
$state   transfers, nearbyPeers, activeTab, modals, toast, nearbyVisible, buildStamp,
         phoneReceive, firewallBlocked, firewallFixing
$derived waitingSend, sortedPeers
stores/theme.ts  custom writable → localStorage + prefers-color-scheme; class on <html>
```

## Backend bridge (generated — DO NOT edit wailsjs/)
```
calls:   import { SendFiles, ReceiveFile, ... } from wailsjs/go/main/App
events:  EventsOn(name, cb) from wailsjs/runtime/runtime
models:  wailsjs/go/models.ts  FileTransfer(+path) · NearbyPeer(+kind) · NearbyPrefs · ResendOutcome · PhoneReceiveInfo
extra methods: StartPhoneReceive · FirewallNeedsFix · FixFirewall · RevealInExplorer
```
Subscribed events: `transfer:updated`, `transfer:overwrite`, `transfer:verify`, `nearby:updated`, `nearby:state`, `nearby:offer`, `transfer:cleared`.
LocalSend peers carry `kind:"localsend"` (badged in UI; same send click path, backend routes by kind).
Peer resend is two-phase: `ResendTransfer` → `needsConfirm` modal → `ConfirmResend`.

## Data flow
```
user action → await App.method() → state update
backend EventsEmit → EventsOn handler → state update → $derived recompute → re-render
```

## i18n (i18n.ts, svelte-i18n)
6 locales `src/locales/*.json`: en nl fr es hu zh. Auto-detect system locale, fallback en. Override via TitleBar `bind:value={$locale}`.

## Styling (style.css)
Global CSS vars; light = warm paper, dark = graphite (primary croc-green #2FBF8F). Fluid `clamp()` type; breakpoints <480/<380px & <560px height. `.sr-only`, global `:focus-visible`, WCAG 2.1 AA.
Contrast tokens: green TEXT = `--color-accent-text`; white text only on `--color-primary-strong`/`--color-danger-strong`. `document.lang` follows locale; lists are semantic ul/li.

## Gotchas
- Direction by id prefix `send`/`receive`.
- `EventsOn`/toast timers not cleaned on destroy.
- File paths platform-specific (`\` vs `/`).
