# 2026-10-01 — a kept field is not stored again

`ssarc.reuse_construct`, `ssarc.block_body`. Refs #8171.

## The stores a functional update did not need

A record update that reuses its donor in place, `Wide { ...w, d: w.d + i }`,
kept most of its fields in the sense `block_keeps` already established: a
field read from the donor's slot of the same index, whose unit the donor
holds, is neither retained at the construction nor released with the donor
when the donor is unique, and the box that comes back from
`__fern_alloc_reuse` is the donor's own. The construction then stored every
field anyway, the kept ones included: a load of the box, a load of the
value and a store per field, writing into each kept slot the word it
already held. The x86-64 assembler's `X86Asm` is such a record, and nearly
every emitter step is a functional update of one or two of its fields.

`reuse_construct` now takes the construction's kept slots. The fields not
kept are stored as before; the kept ones are stored under one test, the
returned box against the token slot, so a construction answered with a
fresh box (a shared donor, or a size class the helper could not match)
still takes every field. The test is on the box the helper returned rather
than on its decision, so the mismatch arm costs nothing to cover.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, both
rows on the same source. "Stage 2" is the compiler the self-host compiler
builds from the commit; the self cost rows are the stage-2 compiler's own
functions, which is where the assembler's record updates run.

| | main (4977cbe) | kept fields left in place |
|---|--:|--:|
| stage 2, total Ir | 41.47 G | 41.14 G (−0.8%) |
| stage 2, `x86_gas_mem_op` self Ir | 417.9 M | 337.2 M |
| stage 2, `x86_place_label` self Ir | 198.4 M | 156.9 M |
| stage 2, `x86_jcc_label_at` self Ir | 183.5 M | 150.3 M |
| stage 2, `x86_queue_fixup_at` self Ir | 252.4 M | 226.2 M |
| native-built, total Ir | 70.08 G | 70.09 G (+0.02%, the extra branch it emits) |

The twenty-five functions whose self cost moved most are all assembler
steps and `ssarc.emit`, each a functional update of a wide record; nothing
moved the other way beyond noise.

## The rule that is NOT the one

A first cut marked kept every field whose operand was a `record_get` of the
donor at the same index, without asking `block_keeps` about units: it
measured the same and miscompiled. A read that TAKES the field nulls the
donor's slot, so the slot does not hold the value any more and the store is
the only thing putting it back. `update-local-in-two-slots` and
`update-keeps-no-field-held-twice` in the reuse differential are exactly
that shape, and the stage-2 compiler built with the first cut could not
compile `checker.fern`. The kept slots `block_keeps` answers are the only
ones whose unit stays in the slot; the store elision follows that one
analysis and introduces no second.

## Witnessed

`TestSelfHostSemanticSourceRC` (`kept_loop`, `kept_shared`: a unique donor
through a loop, and a donor held twice so the construction takes a fresh box
and every field), `TestSelfHostSemanticReuseDifferentialX86_64`,
`TestSelfHostInferredReuseIsIdentical`, `TestSelfHostSSAUnits`,
`TestSelfHostSSASemantic`, `TestSelfHostSSAPhysicalRC`, the
`selfhost-emit-hashes` sweep (1,959 rows, 123 differing, the same 41 programs on each of the three targets, the FAILED set unchanged: every
module with a kept construction changes, as it must), and
`TestSelfHostPerModuleEmitAllFixpointX86_64`.

## Next

On the stage-2 profile after this: `x86_gas_mem_op` is still the largest
self cost at 0.8%, its per-round `x86_str_contains` tests on operands; and
the `x86_gas_trim` copies in `x86_gas_prepare` (0.45%).
