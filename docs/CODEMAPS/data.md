<!-- Generated: 2026-06-13 (v0.17.3) | Files scanned: settings/history + protocols | Token estimate: ~700 -->
# Data: State & Protocols

No database. State = two JSON files on disk + several on-wire protocols.

## On-disk (OS config dir, mode 0o600)
```
settings.json (settings.go)
  LastDestination, LastPeer, MachineID(uuid, stable per-install),
  NearbyVisible(*bool — nil=never set, default visible),
  partials[]{ Code, Dir, At }      # for resume; swept after partialMaxAge 24h

history.json (history.go)
  []FileTransfer  — terminal only, cap 50 (maxHistoryEntries), newest-first
  Code + Speed STRIPPED on save (security/size). ResumeCode kept (internal).
```
Load: app.go reverses oldest-first file → `tm.add` prepends → newest-first UI.
History writes serialized by `historyMu`; ALL settings mutations go through
`updateSettings(path, fn)` under `settingsMu` with atomic tmp+rename saves.
`sweepPartials` deletes only basenames starting `partialDirPrefix`.

## FileTransfer (transfers.go → models.ts)
```
id(send*/receive*), name, files[], size, progress, speed,
status: waiting|preparing|sending|receiving|reconnecting|completed|error|cancelled,
code?, peer?, error?, resendable?, paths[]?, peerMachineId?, resumeCode?
```

## Worker protocol (process-local, stdin/stdout JSON)
```
job   { mode, code, paths[]?, stagingDir? }
event { type:files|progress|done|error, files[], size, sent, progress, speed, message }
```

## Discovery payload (UDP multicast :42791, every 2s)
```
{ id, name, controlPort, certFingerprint(sha256), addresses[≤8], machineId? }
expiry 5s · self-echo health check · hide via Gen counter + 3s bye-suppression
```

## Nearby control channel (TLS 1.3, ephemeral self-signed ECDSA P-256)
```
cert FINGERPRINT-PINNED against discovery payload (no CA; channel auth only).
offer{senderName,files[≤512B name],size(≤64KiB msg)} → accept|decline|busy → croc code
names sanitized at decode (control chars + BiDi stripped)
per-source backoff: an offer that expires unanswered → that IP busy for 2 min
addresses ranked real-LAN(192.168/10) > virtual(172.16/Hyper-V/WSL); 4s/candidate, 75s total
accepted offer's {files,size} kept as receiveExpectation → checked post-receive
  (describeOfferMismatch: top-level names ⊆ offered, size ≤ offered×1.25+16MB)
  → transfer:verify keep/discard prompt on mismatch
```

## croc (transport)
Code-phrase PAKE; relay + LAN; resume via pre-allocated staging files + `utils.MissingChunks`.

## Migrations
None. JSON is schema-on-read; no version field. ⚠ `stagingDirForCode` hash change = breaking (orphans partials).
