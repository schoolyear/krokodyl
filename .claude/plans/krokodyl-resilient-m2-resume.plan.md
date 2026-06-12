# Plan: Resume — continue a dropped transfer from where it stopped

**Source PRD**: .claude/prds/krokodyl-resilient-transfers.prd.md
**Selected Milestone**: 2 — Resume
**Complexity**: Large

## Summary
When a transfer fails mid-flight (stall/drop), keep the receiver's partial instead of deleting it, and let a retry continue with the **same code into the same staging dir** so croc resumes the missing chunks (~13% instead of 100%). Confirmed feasible: croc pre-allocates the file to full size (`Truncate`, croc.go:1743) and, on a retry where the on-disk size already matches the total, computes `MissingChunks` and requests only those — and with our `Overwrite:true` it does this headlessly, no prompt. Also shorten the absurd 1-hour "waiting for receiver" timeout.

## Confirmed mechanism (grounded in croc v10.4.4)
- Receiver opens the existing file; `truncate = stat.Size() != total` (croc.go:1718). croc pre-allocated it to full size, so on retry `truncate=false` → `MissingChunks(...)` (1723) → resume.
- `Overwrite:true` (our option) skips the interactive "Resume?" prompt (gated by `!Overwrite` at 1924) and proceeds — so resume is non-interactive.
- Therefore resume needs only: (a) the partial file preserved at its dir, (b) the **same code**, (c) re-running both sides. No croc/option changes.

## The hard part — coordination
Both ends must retry with the **matching code**:
- **Sender** resume re-runs `Send` with the **same code + same files** (today `performSend` generates a fresh code — must reuse the stored one).
- **Receiver** resume re-runs `Receive` with the **same code into the preserved staging dir** (today each receive makes a fresh `.krokodyl-partial-<id>` and deletes it on failure).
- Receiver maps a code back to its preserved partial: keep an index `code -> stagingDir` for failed receives; a receive for that code reuses the dir.
- Nearby flow: a resume re-offer carries the same code; the receiver accepts and resumes into its preserved partial.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Staging lifecycle | `app.go` `performReceive` (`defer os.RemoveAll(stagingDir)`) | Make deletion conditional on success/cancel, not failure |
| Resend entry | `app.go` `ResendTransfer`, `ResendOutcome` | Resume reuses this entry point; outcome message tells the user "Resuming at X%" |
| Persisted fields | `transfers.go` `FileTransfer.Paths/PeerMachineID`; `history.go` | Add resumable fields; persist them; partials live on disk |
| Cleanup sweeps | `discovery.go` `sweep`; `app.go` `persistHistory` | Startup sweep of stale `.krokodyl-partial-*` dirs (age cap) |
| Code reuse | `app.go` `performSend` (`utils.GetRandomName`) | Resume path passes the stored code instead of generating |
| Tests | `staging_test.go`, `transfers_test.go` `-race` | Pure helpers (partial index, cleanup policy) unit-tested |

## Files to Change
| File | Action | Why |
|---|---|---|
| `transfers.go` | UPDATE | `FileTransfer`: `StagingDir`, `Resumable`, retained `Code` for resumable sends/receives |
| `app.go` | UPDATE | Preserve staging on failure; `code->stagingDir` index for receives; resume in `ResendTransfer` (reuse code + dir); reuse stored code on sender resume; shorten waiting timeout; shutdown/startup partial cleanup |
| `staging.go` (+ test) | UPDATE | Helper to resolve/prepare a resume staging dir; partial-age cleanup policy (pure, tested) |
| `history.go` (+ test) | UPDATE | Persist resumable fields; never persist stale partial refs that no longer exist on disk |
| `worker.go` | (verify only) | `Overwrite:true` already enables headless resume — no change; add a brief comment |
| `frontend/src/App.svelte` | UPDATE | Stalled/errored-with-partial rows show **Resume** (distinct from Send again); outcome toast "Resuming at X%"; |
| `frontend/src/locales/*.json` (6) | UPDATE | Resume label + messages |

## Tasks
1. **Preserve partials**: in `performReceive`, only `RemoveAll` the staging on success/cancel; on failure keep it and record `StagingDir`+`Code`+`Resumable` on the transfer. Maintain a `code -> stagingDir` index. (Tests: index add/lookup; deletion only on terminal-success.)
2. **Receiver resume**: a receive whose code has a preserved partial reuses that dir (no fresh dir, no delete-first). croc resumes via `MissingChunks`. (Manual: drop at 50%, re-receive same code, observe resume.)
3. **Sender resume**: `ResendTransfer` on a resumable send reuses the stored code (not a new one) + same paths. For nearby, the resume re-offer carries the stored code. (Manual: sender side resumes.)
4. **Cleanup policy**: startup + periodic sweep of `.krokodyl-partial-*` older than an age cap or with no matching resumable transfer; remove on successful completion and on Clear history. Surface nothing if clean. (Tests: cleanup decision pure-function.)
5. **Waiting timeout**: cut `senderWaitTimeout` from 1h to a sane bound (e.g. 5 min) so an unanswered send doesn't sit forever; clear message on timeout. (Covers image-1 "stuck waiting".)
6. **Frontend**: Resume affordance + "Resuming at X%" feedback; integrity note. Strings ×6.
7. **Validation**: race tests; harness loopback (no partial left after success); manual cross-machine drop-then-resume with a large file; byte-verify resumed file matches source; fallback to full resend if resume can't proceed.

## Validation
```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
cd frontend && npm run check && npm run build && cd ..
wails build
# GUI harness loopback: completes, no leftover partial
# manual (2 machines): drop at ~50%, Resume → continues near 50%, file hash == source
# manual: successful transfer leaves no .krokodyl-partial-* dir; Clear history sweeps stragglers
```

## Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| croc resume corrupts/refuses for an edge case (multi-file, folders) | Medium | High | Byte-verify after resume; fall back to full resend on mismatch; start with single-file resume |
| Both ends don't both resume (one restarts) → mismatch | Medium | Medium | Reuse the exact same code on both ends; sender reuses stored code; receiver reuses dir |
| Abandoned partials fill disk | Medium | Medium | Age-capped sweep + remove on success/clear; partials only in hidden staging |
| Partial preserved but final file half-written in destination | Low | Medium | Partials stay in `.krokodyl-partial-*`, never moved to final path until verified complete |
| Resume across app restart (partial on disk, transfer not in memory) | Medium | Low | MVP = same-session resume; persisted resumable rows are best-effort, validated against on-disk partial |
| Shorter waiting timeout annoys slow code-sharing | Low | Low | 5 min is generous for a hand-off; still cancellable |

## Acceptance
- [ ] Drop a large transfer at ~X%, Resume → continues near X% (not 0%), finished file byte-identical to source
- [ ] Successful/cancelled transfers leave no partial; abandoned partials are swept
- [ ] Sender and receiver both resume with the same code; nearby and code flows both work
- [ ] Unanswered send times out in minutes, not an hour, with a clear message
- [ ] `-race` + harness green; existing flows unaffected
