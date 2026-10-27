# Stale ring readiness qualification

PR #13689 addresses native child #13688 under main-red parent #12918.
Source `234d92f49dd409d7cd49d9e6035e032474d889fb` precedes the clean build and final validation.
The parent remains open until actual main/nightly recovery completes.
Pending, queued, cancelled, failed, neutral, or skipped substantive validation is not a pass.

## Cause and change

The ring emitter retains a descriptor while its receive buffer contains unread bytes.
Application reads can empty that buffer before the next wait.
The old wait selected a nonblocking entry from the cached list length alone.
When the next request arrived later, the stale descriptor caused a probe followed by another blocking entry.

The new predicate checks current interest, poll flags, buffered bytes, and end conditions.
Wait selection uses this predicate after pending arm/resume work.
It does not discard descriptors, completion entries, or buffered data.
Zero-timeout behavior, submission, deadlines, and signal handling retain their existing paths.
Both native emitters consume the regenerated shared runtime bundle.

## Actual measurements

The existing workloads retain 5,000 measured requests, 1,000 warmup requests, and their original slack formula.
That formula permits four syscalls per measured whole second plus the existing two-second allowance.
The measurement still counts every traced syscall.
The ring pins decrease only after actual lower counts satisfy the unchanged fractional-slack check.
Epoll retains its original pins.

| Workload | Fresh main before | Final source | Final pin |
| --- | ---: | ---: | ---: |
| Ring plain | 10,081 | 10,001 | 2 per request |
| Ring two-fetch | 40,555 | 40,000 | 8 per request |
| Epoll plain | No change proposed | 15,000 | 3 per request |
| Epoll two-fetch | No change proposed | 45,000 | 9 per request |

The separate 64-request delayed-client regression fails before the fix with 193 syscalls against an allowance of 128 plus slack 8.
Its final observation is 129 syscalls, including 65 ring entries, within the unchanged slack formula.
A positive ring-entry assertion prevents an epoll fallback from qualifying this test.
Snapshot boundaries can include one neighboring blocking entry.
These finite observations do not establish a universal syscall or timing bound.

The runtime-only development phase still uses the original pins.
Its two failures report lower counts of 2 and 8, without the earlier excess-call failure.
Those records explain the stricter backend-specific pins.
They are not final-source test passes.

## Final validation

The selected Go runs report 58 PASS nodes, zero failures, and zero skips.
These comprise 14 serve nodes, two epoll controls, 16 reactor/runtime nodes, 25 fixture/census nodes, and one direct ring-driver root.
The serve checks include delayed requests, allocations, peer contexts, pipelined bursts, zero-copy responses, receive deadlines, and shutdown paths.
The direct driver requires exact listener, unread-data, writable, re-watch, half-close, 40-connection, and close results.

Runtime module checks pass for five target sets.
Darwin module and unit-registration checks validate source/contracts, not native Darwin execution.
Reactor cases execute on x86-64, ARM64/QEMU10, and Wasm.
Actual ring execution here qualifies the native x86-64 controls.
No native ARM ring qualification follows from QEMU or source checks.

All 282 image TAP cases pass on native, Wasm, and standalone ARM64/QEMU10.
The complete runs report 79, 55, and 40 balanced zero-live heap censuses respectively.
All 1,335 selected full-run source hashes and the compiler hash remain unchanged.
The 40 standalone ARM executables do not qualify the whole Go ARM harness.

The clean standard two-stage build reports actual make/version exits of zero and a successful stage1 smoke.
Its wall time is 89.117 seconds, not a CPU or performance comparison.
The shipped compiler SHA256 is `120622b5f74a846feb7220898b02b5c322ddfc5fa8d5a619eea2b9a87a8977d8`.
The raw compiler SHA256 is `9af36cebc7575f5228485eb4386865f1bfd9a1e5e6093b9f3cc645170603d7a4`.
All inspected compiler and stdlib source hashes remain unchanged during the build.

The default allocation report records 35,061,110 balanced events against the unchanged 34,951,426 baseline, approximately 0.31% higher.
It passes the unchanged comparison and ten-percent ceiling without an RSS row.
The ring per-request allocation control also passes its existing allowance, with 92 allocations over 100,000 requests in this run.
These counters do not measure CPU time, peak live storage, RSS, or physical/process bounds.

Both unchanged complexity gates pass without skips.
All 2,064 selected Fern files pass the format check.
Go build/vet, workflow lint, test names, CI selftests, runtime bundles, Go formatting, and retained collector controls pass.
The embedded quiet fixture/census command passes, and a separate verbose run records its individual skip status.
The 25 verbose nodes count once in the total above.

## Evidence and preserved failures

`summary.json` and `final-tool-exits.json` report actual results and recovered tool exits.
`serve-final.*` and `epoll-final.*` retain source hashes and exact syscall breakdowns.
The full-image, reactor/runtime, driver, fixture/census, auxiliary, build, and allocation records retain their original outputs.
The build log contains the bootstrap script's phase RSS diagnostic.
This diagnostic does not establish a bound on application memory.

`current-before.*` preserves the unchanged fresh-main failures.
`idle-gap-before.*` preserves the controlled regression failure.
The diagnostic overlays add ptrace observations while retaining workloads and assertions.
Their timing is intrusive and does not qualify performance.
The stale-ready observation identifies 5,000 nonblocking entries with no actual cached readiness.

The first pin-validation builds contain an ordinary Go integer-type mismatch in the new test variable.
`pins-before-int64.go.txt` and both failed build records preserve that mistake.
The explicit final `int64` declarations correct it without a compiler or runtime workaround.
The corrected development runs remain distinct from the committed final runs.

Diagnostic Go sources use `.go.txt` suffixes to prevent accidental package discovery.
The original wait source uses `.fern.txt` to preserve its historical bytes without creating a new checked module.
The artifact inventory identifies temporary observed files, without claiming an independently verified ptrace-exec binary digest.
No historical published dataset, baseline, tolerance, or runtime dependency changes accompany this fix.
