# 2026-10-07 — the id tables are filled, not appended

The tables that map a value, a block, a slot or a bucket to an index or a
position, across the register path, the op-list passes, the x86 emitter's
preferences and the checker's name tables. Refs #8171. No emitted byte
changes: the compiler before and after builds `checker.fern` for x86-64,
arm64 and wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

Each table began as an empty array and was grown to its size by one append
per entry, each a capacity test and, every doubling, a fresh box and a
copy: the dominator pass's seven position tables, the allocator's phi
hints, sole readers, spill starts and wanted-by table, the leaf splice's
value map, the branch threader's truth table, the literal-first-byte
table, the op-list passes' slot tables (loop marks, the stored and
slot-for tables of the invariant hoist, the known and written tables of
the constant propagation, the read, store and tee counts of the dead-tee
pass), the x86 emitter's view-slot and argument-preference tables, the
string-literal buckets and both native assemblers' label buckets, the
checker's signature, variant, method and sum tables, the IR label
registries, the IR verifier's name chains, the ownership summary's index
and the flattener's declaration-name tables. The arm64 emitter's view-slot
and argument-preference tables are hand-kept copies of the x86 emitter's
and change with them.

Each is now one allocation of its final size: `__alloc_i32` or
`__alloc_bool` where the empty entry is zero, and `util.minus_ones` where
it is -1, which `ssadeps.indices` was a private copy of and is gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin;
the baseline is main at 6b2a49b7, the first main that carries #11745.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.651 G | 17.548 G (−0.58%) |
| `ssadeps.indices`, self | 29.9 M | gone |
| `util.minus_ones`, self | — | 77.4 M |
| `__fern_arr_push`, self | 303.5 M | 283.5 M |
| `ssa.thread_bool_joins` (the truth table inlines into it), self | 50.7 M | 31.3 M |
| `asm_ir.emit_ssa_function_x86` (both tables inline into it), self | 74.5 M | 55.3 M |
| `ir.optimize_ops` (the loop marks inline into it), self | 210.8 M | 193.7 M |
| `ssa.assign_spill_slots`, self | 41.2 M | 25.1 M |
| `ssa.phi_mates`, self | 19.7 M | 8.8 M |

The baseline moved between this entry and the one before it: main at
6b2a49b7 compiles `checker.fern` in 17.651 G where b5a688d3 took
16.863 G, the +4.7% that #11760 records.

## What is left

`util.minus_ones` is 77 M for a fill of -1: a store per entry behind the
in-place write's uniqueness test, which cannot change inside the loop. A
proof that the loop's array is the frame's only reference would drop the
test from every such loop, the fills here among them.

Sentinel fills that still append are in the semantic passes (`ssarc`,
`ssaunits`, `seminline`, `sempair`, `ssasem`, `semsource`, `suspend`,
`ssabounds`) and the parser; none was over 3 M on this compile. The rest of
what the command below finds is byte emission in the assemblers and binary
writers (`watbin`, `wit_decode`, the two native assemblers), which encodes
output rather than filling a table.

```
grep -nE "^\s*[a-z_]+ = [a-z_]+\.append\((false|true|0|0 - 1)\);" compiler/*.fern
```
