# 2026-10-02 — the emit state reaches the literal table owned

`asmcore.add_string_lit`, `shape_ref`, `const_agg_pack` and the sixteen
emitter functions between them and the per-block emit loop in `asm_ir` and
`asm_arm64_ir`. Refs #8171. No emitted byte changes: the stage0-built
compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad) and `checker.fern` byte for
byte on main at 9cc02c45 with #11025 and again at 219635ac, and the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing on
both.

## What the profile named

`add_string_lit` interns a string literal or a shape into `EmitState`'s
`string_lits` / `slit_next` / `slit_head` and rebuilds the state with the
three fields appended. The state reached it as a borrowed parameter, since
every caller up to the emit loop takes `s: asmcore.EmitState` borrowed and
threads it back out (`s = f(s)`), so the appends found their arrays shared
and copied the whole literal table, some 15 k strings, once per interned
shape: 2.42 G of `__fern_arr_inc_elems` under `add_string_lit`, 3.17 G
inclusive, 15,185 times, on the whole-compiler emit (previous entry).

## What changed

The functions on every path from the per-block emit loop to
`add_string_lit` take the state `own`: `shape_ref`, `const_agg_pack`,
`const_agg_shape` (the shape choice hoisted out of `const_agg_pack` so the
state is handed on once), and in each backend `ssa_inst`, `ssa_addr_inst`,
`ssa_agg_lit`, `emit_dyn_dispatch_chain`, the dyn box and downcast ops, the
enum-variant payload decs, and on arm64 `emit_rt_subprocess` and
`emit_rt_heap`. The callers already threaded a local at its last use, so
the marks are the whole change bar four lines where the state was read in
the argument after being moved (`shape_ref(s, s.struct_decls.decls[i].name)`),
which read the name first.

The stage0 pin's checker (c891ebc) decided which sites had to move: each
build listed the borrowed arguments (E051) and the reads after a move
(E050), and the set closed after two rounds. The pin, not the current
checker, is the gate here, because it is what builds the compiler in CI.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from main at 9cc02c45 with #11025, and this
change on top.

| | before | this change |
|---|--:|--:|
| total Ir | 202.02 G | 198.10 G (−1.9%) |
| `asmcore.add_string_lit`, inclusive | 3.16 G | 0.66 G |
| `__fern_arr_inc_elems` under `add_string_lit` | 2.42 G (15,185 copies) | 0.49 G (2,373 copies) |

The copies that remain come through `ssa_addr_inst`'s direct `add_string_lit`
calls where the state is still shared at the call, 2,373 of 15,185 interns.

## Witnessed

`TestSelfHostConstAggregate*`, the dyn dispatch, box and downcast tests,
`TestSelfHostSubprocess*`, `TestSelfHostHeap*`, `TestSelfHostArm64DarwinBuilds`,
`TestSelfHostArm64LinuxBuilds`, `TestSelfHostCLIX86_64`,
`TestSelfHostIRPerModuleDriver`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus` (22 tests,
green), the lint ratchet, both emit identities and the sweep.

## Next

The largest copy on the profile is now `irlower.borrow_reg_set`'s `with`
over 4093 buckets, 1.82 G of `__fern_arr_inc_elems` over 21,370 calls:
taking the registry `own` removes it (202.85 G against 206.70 G in the
previous entry's measurement) and waits for the stage0 pin to carry
cec88134, since the fixture that calls it from a struct literal argument
is compiled by the pin (#11020). Then `semsource.define` 0.88 G and
`util.NameIndex.added` 0.50 G, the same shape at smaller scale.
