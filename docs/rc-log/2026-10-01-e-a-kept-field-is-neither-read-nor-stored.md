# 2026-10-01 — a kept field is neither read nor stored while its donor is unique

`ssarc.reuse_construct`, `ssarc.keep_slots`, `ssarc.block_body`,
`irverifyrc.read_keep_site`. Refs #8171. Follows
`2026-09-28-an-update-keeps-its-fields-in-place.md`.

## What the keep still paid

`s = S { ...s, f: v }` with a unique `s` left every carried-over field in its
slot (2026-09-28): no take, no retain, no release. The field was still READ at
its `record_get`, the value held across the allocation call, and STORED back
into the slot it came from. For `X86Asm`, 27 fields, every update loaded 23
values, spilled them over `__fern_alloc_reuse`, and wrote them back: about
100 instructions of a 250-instruction update doing nothing.

## Change

A kept field the construction alone reads (one use in the whole function,
`use_counts`; no take or hold at the read) is a *late* read
(`Keeps.late`): nothing is emitted at its position. The construction itself
does the donor's whole handling, in this order:

1. `__fern_alloc_reuse(token, n)`, the result stored, its shape written;
2. under `token == 0` (the donor was shared, the box is fresh): each kept
   slot is filled — a late one copied from the donor's slot and retained in
   the same expression, any other kept operand stored from its local;
3. `keep_release`: a unique donor gives up the children the construction
   replaces, before their slots are overwritten; a shared one is released
   whole, its kept fields already copied;
4. the replaced slots are stored.

While the donor is unique, steps 2 and 3's copies do not run and the kept
slots are never touched. The donor's release used to precede the allocation;
for a keep whose donor dies at an earlier instruction (`builds`), the release
moves from that instruction to the construction, which only delays it.

The first cut emitted the late reads under `token == 0` at the donor's drop,
into their locals, and stored them under the same test after the allocation.
Every such local then needs a phi at the join, and the lift seeds a slot
never written on a path with the function's zero: the x86-64 backend
materialised one zero store per late value AT FUNCTION ENTRY, on every call.
`x86_gas_emit_op` has 39 updates of 27 fields, so it paid about 2,000
instructions per call and the stage-2 compile grew from 41.5 G to 46.9 G.
Copying inside the construction's own arm leaves no value crossing a join.

## Verifier

`irverifyrc` recognised the keep by the token select ending right before the
allocation's token load. The select is now after the call, so `read_site`
tries `read_keep_site` first: the result stored and shaped, an optional
`token == 0` block skipped, then `load token; const 0; ne; if` with the
decline release in its else arm; the gate and the token source still come
from the earlier `keep_token` select, found by walking back from the call.
`IfSpan` carries its `end` now (`match_if_forwards`). `irverify_run` checks
218 to 220 build the new shape. Running the compiler's own build under
`FERN_IR_VERIFY=1` stays silent.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, both
rows on the same source. "Native-built" is the compiler `bin/fern` builds,
"stage 2" the one the self-host compiler builds from the same commit.

| | main (1a46ecb) | kept fields untouched |
|---|--:|--:|
| native-built, total Ir | 70.03 G | 69.95 G |
| stage 2, total Ir | 41.46 G | 40.16 G (−3.1%) |
| stage 2, `x86_gas_mem_op` self Ir | 418 M | 154 M |
| stage 2, `x86_queue_fixup_at` self Ir | 253 M | 169 M |
| stage 2, `x86_place_label` self Ir | 198 M | 70 M |
| stage 2, `x86_branch_record` self Ir | 185 M | 124 M |

The stage-2 compiler rebuilds itself byte for byte (stage 3). Allocation
counts are unchanged: the same pairings fire, and a copy allocates nothing.

`TestSelfHostSemanticReuseDifferentialX86_64` gains `update-keeps-field-read-later`:
a carried-over field read again after the update keeps its read at its
position and is stored from its local only under `token == 0`.
`TestSelfHostSSAPhysicalRC` pins the op order on a hand-built update (checks
235 to 240) and runs the rc verifier over it.

## Next

On the stage-2 profile after this: the assembler's per-round
`x86_str_contains` tests on operands (`x86_gas_mem_op`) and the
`x86_gas_trim` copies in `x86_gas_prepare`; `ssa_lift.lift_impl` at 2.7%;
`str_eq` at 2.5%, most of it `util.index_of_str` over name tables.
