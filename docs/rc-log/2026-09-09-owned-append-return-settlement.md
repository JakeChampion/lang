# Count settlement for returned owned-array appends

Follow-up to #8874 under #8920, on main `75d39db93` after #8963 and #8966.
This fixes the append failure recorded in #8973. The enum-return failures in
that report remain open and continue to gate merging.

## Operation contract

An `own` scalar-array parameter supplies one counted buffer reference.
`return xs.append(value)` can return a different buffer when push grows or
copies. The push helper does not release the old buffer. Meanwhile, caller
transfer clears the argument slot and the return sweep keeps the receiver
slot. Before this repair, neither frame released the old allocation.

The existing consuming expression-append path now settles its result through
the same array-store operation as a rebind. That operation compares old and
new pointers, releases the old counted reference when they differ, and stores
the result before the return sweep keeps the slot. The three scalar push
representations share this settlement. Declaration-based ownership and the
existing copy/consume decision select the path; no escape scan or AST
ownership inference was added.

## Validation

The original `OwnParamLiftedFieldLeakCheck/array-field-lift-control` fails on
main and passes with this repair. New table cases exercise a returned append
for i32, i64 and f64 arrays. They preserve a shared original snapshot, grow
the result repeatedly, check wide values, and repeat the complete operation
without further heap growth after warmup. The harness checks underflow after
the test frame exits.

The combined `WithAliasIR`, `OwnArrayLifetimeNativeOracle`,
`MovedPayloadSkip` and `OwnParamLiftedField` selection passed in 100.799 s.
All selected self-host x86-64, ARM64 and Wasm executions ran without skips;
native controls also passed. `make lint-all` and `git diff --check` passed.
The full suite will run in CI before merging.

All 15 stock driver builds and smoke executions passed in 61.968 s. The
unchanged strict complete-report size gate passed. Against the recorded #8963
source build, 14 linked driver sizes are identical, including the full
compiler at 11,532,364 bytes. `wasm_runio_run.fern` grows from 6,974,428 to
6,978,524 bytes. The only production addition is the shared settlement helper
and its three call sites; it reuses the existing store/drop implementation.
No baseline or tolerance was changed to accept this correctness repair.

The existing conditional payload-return row already balances on main:
250 allocations and 250 frees, with zero live bytes. Its former expectation
required 200 frees. The row now requires the improved balance while retaining
its value and post-frame underflow checks on all three targets. The dynamic
projection flag transfers the payload on the return path and releases it on
the fallthrough path. Restoring the old leak would be a regression.

## Remaining integration failures

`RcEnumBorrowHelperX86_64/callee_returns_the_payload` and
`RcEnumAliasBindX86_64/payload_out_via_alias_refused` still leak extra scalar
array buffers. Counted borrowed returns preserve the parent's reference, but
legacy escape refusal strands the parent enum. A non-borrowable parameter
does not establish that the caller has no surviving owner. Restoring the old
uncounted return therefore risks caller snapshot corruption.

Those leak assertions remain unchanged. Their repair must reconcile parent
and projection ownership without adding more AST escape heuristics. This
change does not claim that #8874, #8973 or the typed-IR migration is complete.
