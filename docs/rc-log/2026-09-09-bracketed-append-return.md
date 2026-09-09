# Preserve cleanup for a borrowed append receiver

Follow-up to #8974, relating to #8973, #8874 and #8920.

An owned parameter still owns its original buffer when an expression append
borrows it under a share bracket. The append result is a separate counted
buffer. Returning that result must not remove the original parameter from exit
cleanup. The existing append decision and declared ownership now select that
cleanup obligation; no new escape or last-use analysis is introduced.

## Independent regressions

On parent 0fdbd963f, an owned function returning `xs.append(xs[0])` produces
two allocations, one free and 40 live bytes. The additional read causes the
append to borrow its receiver, so the parent's consuming settlement correctly
does not apply. The return keep-set was the remaining source of the leak.

The new contracts cover i32, i64 above 32-bit range, u64 above signed 64-bit
range, fractional f32/f64 values, shared snapshots, repeated unique growth,
the bracketed receiver read and conditional payload returns under churn.
All repaired array paths require exact allocation/free balance. Thirty-two
case/target combinations execute across x86-64, x86-64 sanitizer, ARM64 and
Wasm, with independently pinned interpreter outcomes. The neighboring
field-lift and moved-payload suites passed together with them in 43.949 s,
without skips. Lint-all passes.

Before the append repairs, all six new x86-64 append cases failed the balance
assertion on main 75d39db93. On #8974, the extra bracketed case still failed;
the follow-up fixes it without changing an expected census.

Linked-size probes build and smoke-execute the drivers without a persistent
driver cache. The x86-64 IR driver remains 7,426,364 bytes and the full compiler
11,532,364 bytes, identical to the measured main control. The candidate probe
passed in 13.179 s. These are file sizes, not native runtime benchmarks; the
Linux ARM64 host uses QEMU to execute x86-64 output. No baseline changed.

## Enum return safety: still an integration blocker

The live-parent test borrows an enum into a helper, releases its returned array,
allocates another array and then reads the original enum. The correct result
is zero on all targets. An experimental restoration of the old uncounted
payload return changes the normal x86-64 result to 3: allocator reuse has
overwritten the caller's child. Sanitizer quarantine prevents reuse and misses
this mutation, so both ordinary churn and sanitizer execution matter.

The two existing enum census failures remain unchanged. A non-borrowable
parameter or a fresh container alone does not prove that no parent reference
survives. Counted returns preserve safety, but the old conservative root-release
analysis strands that parent and its child reference. Resolving those failures
requires reconciling parent and child cleanup, not removing the return retain.
This follow-up does not claim that all of #8973 is fixed and must not merge
while the remaining non-Netlify gates fail.
