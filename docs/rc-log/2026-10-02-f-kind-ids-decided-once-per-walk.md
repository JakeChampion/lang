# 2026-10-02 — kind ids decided once per walk

`ir.reduce_strength_ex`, `irverifyrc.verify_rc_fn`, `ssarc.place_views`,
`ircore.inline_leaf`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 0d7a8d32, and the `checker.fern`
binaries the two stage-2 compilers emit are byte-identical.

## What the profile named

`ir.kind_id` maps a kind's name to its tag by length and then by a chain
of string compares. Four walks asked it per op, for names that never
change:

- `strength_rewrite` compared the op after each readable constant
  against thirteen names, one `kind_id` each, 1.02 M lookups on the
  stage-2 compile of `checker.fern`, nearly all of them for an op the
  pass does not rewrite.
- `irverifyrc.is_reuse_alloc` asked for `call_direct` on every op the rc
  verifier scanned, 528 k lookups.
- `ssarc.place_views` built a fresh `op_str_slice()` record for every op
  to read its tag, 910 k record allocations.
- `ircore.inline_leaf` asked for seven names per op of each candidate.

## What changed

Each walk decides its tags once. `reduce_strength_ex` builds a
`StrengthKinds` record at entry and `strength_rewrite` compares against
its fields; `verify_rc_fn` hands `is_reuse_alloc` the `call_direct` tag;
`place_views` compares against `kind_id("str_slice")` bound before the
loop; `inline_leaf` binds its seven tags beside the `call_direct` one it
already bound. The hoisted local is the idiom `cp_loop_body_kill`,
`licm_header_end` and `ssa_lift.lift_impl` already use.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 0d7a8d32 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 37.86 G | 37.55 G (−0.83%) |
| stage 2, `kind_id` and its length arms, self Ir | 234 M | 92 M |
| stage 2, `__fern_str_eq` self Ir | 1,044.7 M | 1,007.4 M |
| stage 2, `__fern_alloc` self Ir | 1,114.7 M | 1,093.7 M |
| stage 2, `ir.op_str_slice` self Ir | 38.5 M | 0 |

## Witnessed

`TestSelfHostIRStrengthPeephole` (the strength goldens),
`TestSelfHostIRVerifyRc`, `TestSelfHostInlineLeaf` and the other
`TestSelfHostInline*`, `TestSelfHostSSAUnits`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, and the emit-hash
sweep.

## Next

`kind_id` keeps 92 M: `is_terminator_kind` and `is_foldable_binary_kind`
still look their names up per call, and `is_kind(op, "name")` sites do
the same wherever a walk reaches them. A tag constant per kind, defined
once beside the name table and inlined as a leaf, would retire the
lookup everywhere rather than walk by walk. On the same profile, self
cost: `__fern_alloc` 1.09 G, `ssa_lift.lift_impl` 1.09 G,
`__fern_str_eq` 1.01 G (`Scope.lookup` 1.65 M compares, the assembler's
mnemonic tests 9.5 M between `x86_gas_unary_ext`, `mov_size`,
`lea_size`, `rep_pfx`, `is_pushpop`, `alu_ext`, `incdec_ext`,
`classify`, `cc_code`, `is_branch` and `bitscan_op`), `hash_bucket`
803 M, `__fern_arr_box` 411 M (32 M record and array boxes, 7.6 M of
them under `__fern_arr_push`).
