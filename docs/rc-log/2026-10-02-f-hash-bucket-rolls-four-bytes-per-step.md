# 2026-10-02 — hash_bucket rolls four bytes per step

`util.hash_bucket`. Refs #8171. No emitted byte changes: the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing
against a compiler built from main at 00ffd2c1, and the `checker.fern`
binaries the two stage-2 compilers emit are byte-identical.

## What the profile named

`hash_bucket` was 968.1 M self Ir on the stage-2 compile of
`checker.fern`, the third largest self cost after `__fern_alloc` and
`ssa_lift.lift_impl`: every name table in the compiler (`NameIndex`,
`checker.sig_table`, the irlower struct tables, `ownership.Index`,
`asmcore`'s literal table) buckets through it, 2.6 M times on this
compile. The loop rolled one byte per step and masked the accumulator to
30 bits after every byte, so each byte paid the loop test, the mask and
the i32 normalisation on top of its bounds-checked load and multiply.

## What changed

The roll takes four bytes per step, with the polynomial's powers of 31
folded into the step's constants, a byte loop for the tail, and the mask
applied once at the end. Integer arithmetic wraps in Fern
(`docs/INTEGER-SEMANTICS.md`), so the mask was never needed for the roll
itself, only for the sign of the index the `%` produces.

The buckets names land in change. That is not observable: every index
over `hash_bucket` chains its entries in ascending order and a lookup
returns the first entry of a name, whichever bucket it is in, and no
table walks its buckets in bucket order. The sweep and the identical
checker binary are the witnesses.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 00ffd2c1 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 37.86 G | 37.69 G (−0.44%) |
| stage 2, `hash_bucket` self Ir | 968.1 M | 803.4 M |

## What the loop still pays

The compiled step is 62 instructions for four bytes. Each `s[i]` reloads
the string's length and data pointer from the header for its bounds
check, though the loop guard already holds the length in a register, and
every i32 add and multiply is followed by a `movslq` normalisation the
next add or multiply does not need. Both are the self-host SSA backend's
to cut, not this function's: a header load reused across the block, and
a normalisation deferred until a consumer needs the 64-bit form. Static
count in the stage-2 binary puts `movslq` under 3% of instructions, so
the second is a small lever; the first reaches every bounds-checked
index in a loop.

## Witnessed

`TestSelfHostAsmLoadRunStdlibRootVsFlags`, `TestSelfHostIRVerifyRc`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, and the emit-hash
sweep.

## Next

On the same profile, self cost: `__fern_alloc` 1.12 G, `ssa_lift.lift_impl`
1.09 G, `__fern_str_eq` 1.02 G, `hash_bucket` 803 M, `__fern_arr_push`
816 M. The `str_eq` callers are in the entry before last; the
`__fern_arr_slice` callers (`live_out_row`, `invariant`,
`irlower.noesc_set_kill`, `ir.cp_kill_after`) in the previous one.
