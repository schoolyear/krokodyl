<!-- Generated: 2026-06-13 (v0.17.3) | Files scanned: go.mod + package.json | Token estimate: ~570 -->
# Dependencies

## External services
None hosted by this project. Transport uses croc's relay (public `croc.schollz.com` by default) + direct LAN. No DB, no cloud, no telemetry.

## Go (go 1.25.0 — direct requires)
```
github.com/wailsapp/wails/v2   v2.12.0  desktop shell (webview + IPC). CLI ver MUST match.
github.com/schollz/croc/v10    v10.4.4  P2P transfer (PAKE, relay, chunked resume)
github.com/schollz/peerdiscovery v1.7.6 LAN multicast discovery (:42791)
github.com/sirupsen/logrus     v1.9.4   structured logging (file sink)
github.com/google/uuid         v1.6.0   MachineID / transfer ids
```
Notable indirect (via croc): pake/v3 (PAKE), go-qrcode, machineid, progressbar. crypto/tls + crypto/ecdsa (stdlib) for the nearby control channel.

## Frontend (npm)
```
dep:    svelte-i18n            ^4.0.1   6-locale i18n
devDep: svelte                 ^5.36.7  runes
        vite                   ^6.3.5   bundler/dev server
        @sveltejs/vite-plugin-svelte ^5.1.1
        typescript             ^5.9.3
        svelte-check           ^4.3.0   type + a11y gate (npm run check)
        svelte-preprocess, tslib
```

## Build / CI toolchain
```
Wails CLI  v2.12.0 (pinned in .github/workflows/release.yml — sync w/ go.mod)
GitHub Actions matrix: ubuntu + windows + macos, Node 22
Linux system deps: gcc, libgtk-3-dev, libwebkit2gtk-4.1-dev  (→ -tags webkit2_41)
macOS: ad-hoc codesign (--sign -) + ditto zip. NO notarization (no Apple Dev acct).
Release trigger: push v* tag → build all 3 → softprops/action-gh-release.
```

## Generated (do not edit)
`frontend/wailsjs/**` — Wails-generated Go↔TS bindings; regenerate on Go struct change.

## Known vuln posture (2026-06-13)
- `govulncheck`: 0 reachable vulnerabilities in Go deps.
- `npm audit`: 5 advisories, all in the esbuild build toolchain (vite chain) —
  dev-server-only, never shipped; no non-breaking fix exists upstream yet.
