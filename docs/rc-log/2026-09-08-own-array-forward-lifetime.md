# Borrowed-array to own forwarding: lifetime measurements

The #8920 collection pilot found a native ownership-transfer defect while
validating the self-host #8874 fix. Values stayed correct in the small native
case, but the borrowed forwarding frame neither paid for its first transfer
nor recorded ownership of the replacement returned by the consuming callee.

## Reproduction and measurement

Source: `examples/ownership/borrowed_forward_lifetime.fern`. Each churn makes
the requested number of calls; every call performs 32 indexed updates through
an `own` array parameter while retaining the original borrowed input. The
second churn reports its additional allocator high-water growth. The next
output line is the underflow counter, sampled before printing. Both complete
old and new contents are checked. Any underflow returns fixed exit99, never
the counter's low byte.

Measured on native Linux ARM64 in `lang-coreutils-bench:24.04`, not under QEMU.
These are allocation and correctness measurements, not timing or coreutils
throughput claims. GNU/uutils comparisons do not apply to this compiler ABI
regression fixture.

Build: `FERN_LEAKCHECK=1 bin/fern -target arm64-linux -o bin/own-forward-counts examples/ownership/borrowed_forward_lifetime.fern`.
Run that same binary with the single argument 8, 32 or 128. Only that argument
changes between scales. The 8-call pipeline was verified before scaling.

| Calls per churn | Before growth / underflows | After growth / underflows | Before final live bytes | After final live bytes |
| ---: | ---: | ---: | ---: | ---: |
| 8 | 512 / 16 | 0 / 0 | 1136 | 96 |
| 32 | 2048 / 64 | 0 / 0 | 4208 | 96 |
| 128 | 8192 / 256 | 0 / 0 | 16496 | 96 |

Before: exit 99 on every row. After: exit 0 on every row. Allocation events are
39, 135, 519 on both sides. Frees change from 4 on every before row to 36, 132, 516.
Final live bytes include the diagnostic's arguments and output formatting;
96 is a constant residual, not a claim of whole-program zero leaks. High-water
growth is not peak live memory. No performance timing was collected.

Base revision: `7bbf0a4212496997e0847c6af98c02718c03ccb8`.
Candidate: that revision plus this focused native transfer fix.

SHA-256 provenance:

- Fixture: `6d0430abe86c3c0d026c5738530cf7632578fc2f574c37d7ac85adfe17ce8340`
- Before native compiler: `3af286a8d694bc3e083676d4adacae7f342703dc675b98764626cb9a13b69602`
- After native compiler: `c7be373d3ce2a5c358e8d9cac253e199d5a85b30a3b75ac337f918b86e5d94dc`

## Fix and validation

An explicit own argument now retains a threaded borrowed array only while
its runtime ownership flag is clear. The own-call assignment sets that flag
without releasing the consumed old reference, including identity results.
Subsequent updates transfer the owned replacement without another retain.
There is no unconditional parameter-entry retain to force every update to copy.

The original regression failed with underflows on x86-64, ARM64 and Wasm.
The corrected matrix passes 11 cases on each target: repeated updates,
identity return, no transfer, fresh replacement, i64/f64 elements, early
return, scalar return, string/nested elements, and reuse after first copy.
The reuse case asserts that later scalar updates do not advance the heap.
An IR regression pins both halves of the ownership-flag protocol.

Full-suite completion is a merge prerequisite and is recorded in the PR,
not inferred from this targeted matrix. Early PR publication lets CI run
that suite while local full-suite validation is still pending. This fix does not claim
to resolve the separate self-host pointer-element lifetime gaps.
