# `own` on a string parameter: two releases and none, cancelling (#8804)

`own x: string` is declared, checked and lowered, and no backend counted it
right. Nothing in `internal/stdlib`, `examples/self_host` or `coreutils`
declares one — all 190 `own` params in the tree are arrays or structs — which
is why it stayed latent. Found opening #8785's call-boundary row.

## Measured, `-O`, `acc = grow(acc, "12345678")` where `grow(own a, s)` does
`a = a + s; return a;`

| target | before | after |
|---|---|---|
| x86-64 | correct output, then OOM-killed at 200 000 rounds, 12.6 GB peak | 0.009 s, 2.9 MB peak |
| arm64 | `__rc_underflow_count()` 1 at 3 rounds, SIGSEGV at 500 | 0, correct |
| wasm | underflow 1 at 2 rounds, trap at 500 | 0, correct |

Fresh heap bytes over 64 / 200 / 800 rounds — 4x the appends, so a quadratic
accumulator costs about 16x:

| target | before | after |
|---|---|---|
| x86-64 | 164 800 -> 2 801 632 (**17.0x**) | 73 968 -> 83 040 (1.12x) |
| arm64 | crash | 145 792 -> 164 464 (1.13x) |
| wasm | trap | 73 440 -> 81 424 (1.11x) |

## The two halves

**The callee never released, on a single-word ABI.** `computeFreeEligible`'s
owned-parameter switch admitted a string parameter only under
`ast.UseTwoWordStrings(b.ptrW)`. Native x86-64 is single-word, so the parameter
was absent from `freeEligible` and the exit sweep's
`if !flagGated && !b.rc.freeEligible[p.Name] { continue }` skipped it. The
caller moved in its only reference; nobody released it.

That entry is also `isSelfStrAppendLocal`'s gate, so the same absence sent
`a = a + s` through `OpStrConcat`. Restoring it is the whole of the 17x -> 1.1x
column: no new fast path was added, an existing one stopped being refused.

**The caller released as well, on every ABI.** The string arm of the
overwrite-dec in `assign()` did not consult `callConsumesIdent`. The ARRAY arm
forty lines above already declines for exactly this reason. `acc = f(.., acc,
..)` is the one shape E051 admits for a plain local in an `own` position, and
it is a move.

## The trap

The two halves cancelled, so **each backend looked like a different bug**, and
on x86-64 the shape that mattered most looked correct. Fixing either one alone
makes the other worse: admitting the callee release without the caller-side
suppression turns the x86-64 leak into the two-word double free; suppressing
the caller's dec without the callee's release turns the two-word crash into a
leak. Both, or neither.

A `-run` filter over the existing rc suites finds neither: the whole `own`
corpus is `own p: i32[]` / `own p: string[]` (`rc_own_self_append_test.go`),
where `ArrayType` was admitted unconditionally.

## Witnessed vs contract-only

Witnessed on all three backends by
`internal/e2e/rc_own_string_param_test.go` (underflow count AND the heap-bump
ratio, self-checking) and pinned in the lowering under all three string ABIs by
`internal/ir/own_string_param_test.go`. Reintroducing either half fails 5 IR
assertions and all 3 e2e backends, in the three distinct ways above.

## Next lead

#8785's other two rows are untouched and stay open. A PLAIN (borrowed) string
parameter still copies — the caller keeps ownership and the buffer is genuinely
shared for the call's duration, so the fix there is a verdict change, not a
gate. And `return a + s` written directly, without the `a = a + s` rebind,
still lowers to `OpStrConcat`: `isSelfStrAppendLocal` recognises only the
assignment shape, and a return-position accumulator is the same fact in a
different statement.

`std/io_buffered`'s `BufWriter` is the consumer that matters and this does not
reach it: its accumulator is a struct FIELD behind a receiver, which is
#8785's other half.
