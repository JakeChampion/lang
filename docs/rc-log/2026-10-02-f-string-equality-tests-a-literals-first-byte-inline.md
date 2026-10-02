# 2026-10-02 — string equality tests a literal's first byte inline

`ssa.literal_first_bytes`, `asm_ir.ssa_str_eq`, `asm_arm64_ir.ssa_str_eq`.
Refs #8171. A codegen change: every string comparison against a non-empty
literal gains three instructions on both native backends, so the emit-hash
sweep differs by design; its 1,965 rows still all emit or refuse as on
main (1,706 emitted, 259 refused).

## What the profile named

`__fern_str_eq` was 1,044.7 M self Ir on the stage-2 compile of
`checker.fern`, 41.6 M calls at 23 Ir each, and the second largest self
cost in the compiler. The call sites compare the two length words inline
and call only when they agree, which rejects nothing in a chain of
same-length literals: the assembler's mnemonic tables (`x86_gas_classify`
and the helpers it asks, 9.5 M calls between them), the lexer's keyword
list, the parser's operator table and the checker's `Scope.lookup` all
walk such chains, and the helper's own entry (the box identity, the
lengths again, the buffer identity, the length dispatch) runs before a
single byte is compared. `ir.kind_id`'s length arms test the first byte
by hand before each compare, which is the shape the compiler should
produce itself.

## What changed

`ssa.literal_first_bytes` is a per-value table of the first byte of the
literal a value is the address of (kind 47), or -1. Both native SSA
frames carry it, and `ssa_str_eq` reads it: when either operand is a
non-empty literal, the other operand's first byte is compared with the
literal's after the lengths, and the call is made only when both agree.
A comparison of two non-literals, or against the empty literal, emits
what it did. The wasm backend calls its helper as before.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 0d7a8d32 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 37.86 G | 37.53 G (−0.88%) |
| stage 2, `__fern_str_eq` self Ir | 1,044.7 M | 635.2 M |
| stage 2, `__fern_str_eq` calls | 41.6 M | 28.9 M |

The 12.7 M calls the byte test rejects cost the three instructions at
every one of the 41.6 M sites with a literal operand instead; the net is
the total row.

## Witnessed

`TestSelfHostSSAStrEqTestsLiteralFirstByteInline` (new: the byte compare
appears once per non-empty literal on either side and not for two
non-literals; near misses on the first and last byte are rejected and the
matches found, on both ISAs), `TestSelfHostSSAStrEqComparesLengthsInline`,
the `TestSelfHostSSA*`, `TestSelfHostStr*` and `TestSelfHostArm64*`
backend suites, `TestSelfHostSemanticSourceRC`, the x86-64 and arm64
fixture lanes, and the lint ratchet.

## Next

Of the 28.9 M calls left, the chains whose literals share a first byte
(`movb` / `movw` / `movl` / `movq`, `setcc` and `jcc` families, the
`shr_s` / `shr_u` kinds) pay the call for every arm; a first-word compare
for literals of four bytes and more would reject those too. On the same
profile, self cost: `__fern_alloc` 1.11 G, `ssa_lift.lift_impl` 1.09 G,
`hash_bucket` 968 M, `__fern_arr_push` 816 M, `__fern_str_eq` 635 M.
