# SSA cutover: the shared lowering, and why Perceus needs it

> **Status (2026-09-15): still PROPOSED, and the case for it has grown.** The
> re-evaluation date passed without a call; see `docs/SSA-DECISION.md` →
> "Where this stands (2026-09-15)".
>
> Two things have changed since this was written, pulling in opposite
> directions. **For:** #8822 is a profiled real program (`coreutils/sort.fern`,
> 4–5x GNU) whose diagnosis names `-backend ssa` as the direct answer to its
> dominant cost — evidence of the kind tripwire 1 was waiting for, which this
> plan did not have and explicitly did not claim. **Against:** the ownership
> work this plan motivates reached the lifted form WITHOUT a codegen cutover
> (`internal/ssa/{ownership,units,certify}`), so tripwire 4 — the one this plan
> does rest on — no longer needs the cutover to be answered.
>
> So the strongest version of this plan today is narrower than what is written
> below: not "IR → SSA → all native backends", but closing `x86_64ssa`'s
> helper-coverage gap far enough to MEASURE #8822's claim. The shared-lowering
> endpoint stays unscheduled.

**Owner:** compiler / IR.

## Which tripwire fired

The fourth:

> The flat-IR optimizer in `internal/ir/` grows enough ad-hoc cross-block
> analysis that we'd be reimplementing SSA badly — at which point doing it
> properly wins.

It has, and the RC work is where. Current inventory of hand-rolled
control-flow analysis over the flat op stream:

| | lines | what it hand-rolls |
|---|---|---|
| `internal/ir/rc_analysis.go` | 5458 | the whole ownership plan, over AST + checker tables |
| `internal/ir/verifystack.go` | 746 | operand-stack dataflow with its own scope stack |
| `internal/ir/verifyrc.go` | 374 | backward bracket matching, forward reachability that skips sibling arms |
| `internal/ir/rc_dropguided.go` | 250 | reuse-token flow that "dies at any control-flow join it cannot soundly cross" |
| `internal/ir/rc_cross_branch.go` | 136 | cross-block reuse pairing |
| `examples/self_host/irverifyrc.fern` | 416 | the same reachability walk, again, in Fern |
| `examples/self_host/irverifystack.fern` | 305 | the same stack dataflow, again, in Fern |

Two of those were written this week (#7783, #7791, #7785). Each contains a
`matchIfBackwards`, a `skipToMatchingEnd`, or a `reaches()` that exists solely
because there is no CFG to ask. `internal/ssa` already has dominator trees,
dominance frontiers, def-use chains and a liveness pass — 8,511 lines and 565
tests of exactly this, sitting off the production path.

**Tripwires 1–3 are NOT claimed.** They are about profiled performance (LICM,
GVN, SCCP as a demonstrated bottleneck) and nothing here measures that. This
plan rests on the fourth alone.

## The measurement that makes this actionable

`internal/ir` emits **15,516** reference-count operations across the 1,347
programs the conformance corpus lowers. Of those:

- **10,506** are attributable to a named local (`OpLoadLocal n; OpRcInc`)
- **5,010 — 32% — act on a value that exists only on the operand stack**: a
  call result, a field read. No name, no def-use edge, nothing to own it.

That is the ceiling. Every ownership analysis on this representation is
structurally blind to a third of the reference-count traffic:

- the verifier shipped in #7783 must skip them (its coverage counter reports
  them);
- the ownership signature table (#7786) cannot summarise what it cannot name;
- per-path unit accounting — the Roc-grade certifier, the single largest
  soundness gap in `docs/rc-log/`'s live defect list — is not reachable at all.

It is not an effort problem. Koka analyses Core, Lean analyses LCNF, Roc
analyses LIR, Pen analyses MIR. **Every other Perceus implementation runs its
ownership pass over a named-binding IR.** Fern is the only one that does not,
and it is the only one whose ownership analysis is interleaved with lowering.

## What the current docs get wrong

`SSA-DECISION.md` describes the SSA backends as covering "a subset of the
language", and its reconciliation section records the arm64 corpus
differential's first run finding "four wrong answers and 56 heap SIGSEGVs".
Both are stale.

Measured 2026-08-26 and pinned in `internal/e2e/arm64_ssa_differential_test.go`:

> over 286 corpus programs there is **no SSA coverage gap left at all**: 281
> compared, **0 ssa-refused**, 5 baseline-rejected (two deliberately-invalid
> probes and three that need `subprocess`, which no compiled target provides).

And `internal/e2e/testdata/arm64-ssa-diff-known-divergences.txt` is **all
header and no rows** — "the whole corpus either agrees or is refused". The four
wrong answers and 56 SIGSEGVs were fixed.

**arm64 SSA is corpus-complete and behaviourally identical to the shipping
backend.** The cutover is much closer than the shelve doc reads.

## Actual readiness, per backend

| backend | CLI-wired | corpus differential | state |
|---|---|---|---|
| `arm64ssa` | yes | yes | 281/281 compared, 0 refused, 0 divergences |
| `wasmssa` | yes | no | **single user function only** — measured below |
| `x86_64ssa` | **yes, since 2026-09-01** | **yes, since 2026-09-02** | 328/348 compared, 0 refused, 0 divergences — the 20 others are programs the flat backend cannot build either |

The spread is much wider than "arm64 is ahead". Two backends are
corpus-complete and agree on every program compared, and one cannot compile a
program with two functions in it.

Two concrete blockers, and only two:

1. **wasm compiles one function.** Measured 2026-08-29, not inferred:

   ```
   function main(): i32 { ... }                  -> compiles, runs, correct
   function helper(x: i32): i32 { return x+1; }
   function main(): i32 { return helper(9); }    -> REFUSED
   ```
   ```
   wasm/ssa: wasmssa: OpCall to "helper" is neither self-recursion
   (callee == "main") nor a declared import
   ```

   `wasmssa` supports exactly one user function plus declared imports. Any
   program that calls another user function is refused, which is every real
   program — `examples/wasm/shape_area.fern` fails on its first `show_area`
   call.

   So the corpus differential `SSA-DECISION.md` asks for would come back
   almost entirely `ssa-refused`: honest, and nearly empty as a gate. **The
   wasm work is multi-function support in `wasmssa` — a call and
   module-assembly problem — not a test harness.** Build the differential
   after, as the proof, not before as the discovery.

   (When it is built, note the artifact asymmetry: the default backend emits
   a WASI command, `-backend ssa` a core module exporting `main`, so stdout is
   not comparable without `-component-wrap-cli` or a run-mode adapter.)
2. **x86-64 was not reachable — and the reason given here was wrong.** This
   said `x86_64ssa` had "no module-level assembly emitter matching arm64's
   `EmitAsmModule`", making it "real work, not a wiring line".
   `x86_64ssa.EmitAsmModule` was already there (`gas.go:53`), multi-function,
   System V ABI, runtime helpers and all, with its own tests
   (`gas_call_test.go`). Nothing selected it. The CLI gate listed only
   `arm64-linux` and `wasm32-wasi`, which is #6979's complaint — a code-size
   harness measuring a pipeline the CLI never runs.

   Wired 2026-09-01. Emitted instructions against the shipping stack machine,
   on shapes it covers: `call_overhead` 713 → 137 (**−80.7%**), `int_loop`
   114 → 48 (−57.8%), `array_index` 573 → 250 (−56.3%).

   **Coverage has two numbers, and they are far apart.** Over all 317
   `examples/**/*.fern` outside `self_host`, measured 2026-09-02 with
   `EnumSentinel` landed:

   | | emits asm | links + runs |
   |---|---|---|
   | before `EnumSentinel` | 58 | 8 |
   | after | **256** | **9** |

   Quote both or neither. `-backend ssa` without `-o` stops after instruction
   selection, and that is the 58 → 256 column: `EnumSentinel` really was the
   emit-stage long pole, and it is gone. Ask for a binary and the LINK stage
   decides, and there the wall is the runtime-helper table, so end-to-end moves
   by one program. An early measurement of this work reported "58 of 317
   compile" without saying which of the two it was; it was the emit column, and
   read as end-to-end it overstates the state by 50 programs.

   The blockers after `EnumSentinel`, by count:

   | | |
   |---|---|
   | 247 | one or more runtime helpers with no entry in `runtimeHelperEmitters` — 84 distinct symbols, a median of 13 per program; see the unlock curve below rather than reading a per-name count as a work item |
   | 34 | float reinterprets (`reinterpret_f64_to_i64` and siblings) |
   | 15 | library files with no `main` — not a backend refusal |
   | 5 | E066 / checker errors — not a backend refusal either |

   A sixth row, `7 | more than 6 params — no stack-argument ABI`, is gone as of
   #8087: arguments past the six SysV registers are pushed by the caller and
   read from `[rbp+16]` up by the callee, as the AArch64 side has always done.
   Those seven programs now block on whatever they need next, and the
   end-to-end compared count moved 28 → 30.

   Each of those labels is a `call` the emitter writes with nothing behind it.
   `checkNoDanglingCalls` (ported from arm64ssa 2026-09-02) refuses them at emit
   time, naming every missing helper at once, so a coverage gap reads as one
   instead of arriving from the assembler as `undefined label`.

   **Re-measured twice on 2026-09-05, and #8570 acted on both.** The corpus is
   337 programs. It went 29 → 39 comparable with `print`, `eprint`,
   `__alloc_reuse` and `__fern_drop_arr_str`; the measurement that mattered was
   what the remaining refusals then looked like, which was no longer a flat
   co-occurrence wall but ONE symbol:

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | `remove_dir_all` | 208 | **155** |

   Those 155 reach `remove_dir_all` through `std/test`'s import graph without
   ever calling it — a symbol the linker needs and the program never runs — so
   that one helper (with the `__fern_io_error` it reports through) took the leg
   to **194 comparable, 0 divergences**.

   **2026-09-06: the float reinterprets** (`reinterpret_f64_to_i64` and its
   three siblings) were the one refusal left that was not a missing helper —
   the emitter's `fConvSeq` had no arm for them. They are identities or a
   `cvtsd2ss` / `cvtss2sd` hop on the f64-bits-in-a-GPR model, and 31
   programs had been refused for them alone; 20 now compare, the other 11
   moved on to the helpers they were also missing. The leg is at **215 of 340
   comparable, 0 divergences**. The first run after the arms found one real
   divergence, which was not in them: the lifter left `f64→i64` at the i32
   default width, so the constant the folder rewrote `f64_bits(NaN)` into was
   sign-extended from bit 31 by `MovImm`'s maskFix — invisible while the op
   was refused, and invisible on arm64, whose `MovImm` never masks. The lift
   stamps Width 64 on it now.

   **2026-09-13: the string builder** (`buf_new` … `buf_free` and the internal
   `__fern_buf_reserve`, #8773) got emitters, laid out word for word as the
   flat backend's so a take is the same zero-copy handoff. Every stdlib path
   through a builder — `std/strings`, `std/csv`, `std/table`, `std/textwrap`
   — had been refused for those seven names together. The leg is at **219 of
   347 comparable, 0 divergences; 108 refused**.

   **2026-09-16: `args`, `env` and `stat`** got emitters, so `sort.fern` and
   the seven other coreutils that needed only those build (#8822). Two
   things the slice exposed in the LEG itself: `examples/cli/yes.fern`
   builds now, prints until it is killed, and the x86-64 leg ran it
   unbounded — 89 minutes of a 90-minute timeout on one program — so the
   leg now runs each binary through the same bounded, capture-capped runner
   the arm64 leg has (`runSSADiffBinary`, 15 s, 1 MiB per stream), with a
   wall expiring on one side its own outcome; and the lane completes in
   under three minutes on a 4-core container. The leg is at **223 of 348
   comparable, 0 divergences; 105 refused**.

   **2026-09-16, later: `__memcpy` and the f64 math family** (`__abs_f64`,
   `__sqrt_f64`, the three `roundsd` forms, `__round_f64`, and the five
   transcendentals through the flat backend's kernel bundle, which now
   writes through a caller-supplied line writer so both backends carry the
   same bytes) got emitters. Tallied by the exact name the refusal prints
   rather than by prefix, `__memcpy` alone blocked 16 programs and the two
   families together 31; the run unlocked 30. The leg is at **253 of 348
   comparable, 0 divergences; 75 refused**. Of the 75, the median is
   missing 3 helpers and 35 are missing one or two:

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | `read_file` | 25 | 14 |
   | the Map family (`map_new`, `__method_Map_set` … `__method_MapIter_*`, `__fern_map_hash_seed`, `__fern_map_drop`) and the memory trio (`__alloc`, `__free`, `__memset`, `__fern_drop_arr_ptr`) | 24 | 0 |
   | `write_file` | 13 | 0 |
   | `temp_dir` | 11 | 0 |
   | `monotonic_ns` | 10 | 3 |
   | `random_bytes` | 7 | 5 |
   | the socket family (`tcp_*`, `poll`, the pollables) | 6 | 0 |

   So the next slice is the file helpers — `read_file` with `write_file`
   and `temp_dir` — and the one after it the Map family with the trio.

   **2026-09-16, later still: the file, clock and random helpers**
   (`read_file`, `read_file_bytes`, `write_file`, `remove_file`, `temp_dir`,
   `monotonic_ns`, `now_unix_ms`, `sleep_ms`, `random_bytes`, `random_i32`)
   got emitters, and with them the module emitter gained what arm64ssa's has
   had: a helper written in Fern (`internal/fernrt`, here `read_file`'s
   `__fern_utf8_valid`) is lifted through the same SSA pipeline and emitted
   as a function of the module. The run also exposed that the x86-64 leg
   compared every program's stdout: `bench_test.fern` prints measured
   microseconds, which is not a function of the compiler, so the leg now
   reads the same stdout-unstable list as the arm64 leg
   (`testdata/ssa-diff-stdout-unstable.txt`, renamed from its arm64 name,
   since the property is the program's). The leg is at **282 of 348
   comparable, 0 divergences; 46 refused**, and of the 46 the median is
   missing 9 helpers and 17 are missing one or two:

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | the Map family (`map_new`, `__method_Map_set` … `__method_MapIter_*`, `__fern_map_hash_seed`, `__fern_map_drop`) and the memory trio (`__alloc`, `__free`, `__memset`, `__fern_drop_arr_ptr`) | 24 | 0 |
   | the socket family (`tcp_*`, `poll`, the pollables) | 6 | 0 |

   So the next slice is the Map family with the trio, which is the last
   large group; after it the leg's refusals are the socket family and a
   handful of singletons.

   **2026-09-16, the Map family.** The Map is core/map.fern on every
   backend; what the x86-64 SSA emitter lacked was the runtime names that
   Fern bottoms out in — `__alloc` (the bump sequence behind a label),
   `__free` (nothing, as `__fern_box_free` already is on this heap),
   `__memset`, `__fern_map_hash_seed` (drawn once through `random_i32`,
   cached in `.bss`), `__fern_map_drop` (a reference drop, since nothing is
   reclaimed) and `__fern_drop_arr_ptr` (the element walk `__fern_drop_arr_str`
   already had, now one emitter over the element drop) — and the call-site
   half of `ir.CodegenAliases`, so `map_new` names `map_new_impl` the way the
   driver's reachability walk already assumed. The leg is at **301 of 348
   comparable, 0 divergences; 27 refused**, 21 of them missing one or two
   helpers:

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | the socket family (`tcp_*`, `poll`, the pollables) | 6 | 0 |
   | `read_dir` | 5 | 4 |
   | `write` | 5 | 5 |
   | `__method_Reader_read_line` | 4 | 4 |
   | `__fern_heap_bump_bytes` | 4 | 3 |
   | `tcp_connect`, `create_dir_all`, `hostname` | 1–2 | 0 |

   The next slice is the four singletons (`write`, `read_dir`,
   `__method_Reader_read_line`, `__fern_heap_bump_bytes`: 16 programs), then
   the socket family.

   **2026-09-16, the four singletons.** `write` (one write(2), as `print`
   is), `__fern_heap_bump_bytes` (cursor minus a base the reservation now
   records), `__method_Reader_read_line` (a byte at a time into a .bss line
   buffer, then a right-sized string) and `read_dir` (two getdents64 passes
   over a scratch block, count then fill, in the order the kernel reports,
   as the flat backend lists) got emitters. The leg is at **317 of 348
   comparable, 0 divergences; 11 refused**: the socket family (`tcp_*`,
   `poll`, the pollables: 6 programs, always together) and one program each
   for `create_dir_all`, `hostname`, `isatty`, `putchar` and
   `__fern_rc_underflow_count`. Of the 348, 20 are baseline-rejected (the
   flat backend cannot build them either), so 317 of the 328 buildable
   programs now run under both backends and agree.

   **2026-09-16, the socket family and the last singletons — no refusals
   left.** `tcp_listen`, `tcp_connect`, `tcp_accept`, `tcp_send`, `tcp_recv`,
   `tcp_close`, `tcp_pollable`, `poll` (through poll(2), which takes the
   millisecond timeout directly) and the wasm pollable stand-ins, plus
   `isatty` and its handle forms, `hostname`, `putchar`, `create_dir_all`
   and `__fern_rc_underflow_count` (with `__fern_rc_dec` now counting an
   over-release instead of wrapping the count into the static sentinel) got
   emitters. Every one of the 328 programs the flat backend can build now
   builds under `-backend ssa` too: **327 agree, 0 refused, 1 known
   divergence**. The one is `examples/ownership/borrowed_forward_lifetime.fern`,
   a reclamation probe: it prints `__heap_bump_bytes` growth across a churn
   loop, which the flat backend's freelist absorbs and this backend's bump
   heap did not. The leg carries the arm64 leg's exact-in-both-directions
   known-divergences list (`testdata/x86-ssa-diff-known-divergences.txt`),
   and that row was its only entry until the freelist port landed the same
   day (#9423, below). A new refusal from here on is a regression, and the
   floor is the whole buildable corpus.

   **2026-09-16, the freelist.** `x86_64ssa` now reclaims through the same
   size-class freelist as arm64ssa and the flat backend
   (`docs/SSA-RC-RUNTIME.md`): every block that can be released comes out of
   `__alloc`, compiled code reaches it through a register-preserving
   trampoline, and boxes, arrays, closures, maps and mispaired reuse tokens
   go back through `__free`. The reclamation probe agrees, so the corpus is
   **328 compared, 0 refused, 0 divergences** and the known-divergences file
   is empty. With every string producer allocating through `__alloc`,
   `__fern_str_append` grows a uniquely held accumulator in place on this
   backend (the lift used to rename it to `__str_concat` for both SSA
   backends), which took `examples/bench/string_build.fern` from 10x the
   flat backend to 4x — the slowdown gate had been passing on that program
   only when the machine was quiet enough to keep the absolute gap under its
   floor — and `__fern_str_dec` frees at rc == 1, which took it to 1.6x.
   This backend's heap now reclaims everything the flat backend's does, and
   arm64ssa's does too since the same day, when its string producers moved
   onto `__alloc` and it got the same two helpers.

   **Where the wall was on 2026-09-06**, over the 105 then refused: every one
   names a helper with no emitter, and no single symbol unlocks more than
   three.

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | `__memcpy` | 48 | 0 |
   | `__method_string_as_bytes` | 28 | 0 |
   | `args` | 26 | 2 |
   | the Map family (`map_new`, `__method_Map_set`, `__fern_map_hash_seed`, `__memset`) and the memory trio (`__alloc`, `__free`, `__fern_drop_arr_ptr`) | 24 each | 0 |
   | `stdin`, `read_file`, `__method_Reader_close`, `__method_Reader_read_chunk` | 21-24 | 0 |
   | `monotonic_ns`, `__fern_heap_bump_bytes`, `__sqrt_f64` | 3 | 3 each |

   The median refused program is missing 5 helpers (13 before #8570), and 30
   are missing one or two. So the next slice is still a GROUP — the Map
   methods, or the memory trio — and the 2026-09-02 lesson below is the one
   to size it by.

   **The 2026-09-02 measurement below is kept because its LESSON stands**: size
   this work by removal, never by which name appears most. It is what the table
   above measures directly.

   - `x86_64ssa` had **13** helper emitters then and has **44** now; `arm64ssa`
     has **~120**.
   - The median blocked program is missing **13** helpers at once, not one.
     Only 22 programs are missing fewer than 11; 48 are missing exactly 11.
   - So the per-program FIRST-error histogram above is not a work order.
     `__fern_drop_arr_str` heads it at 195 programs and implementing it alone
     unlocks **zero**, because every one of those programs is missing a dozen
     others too. Sizing this work by which name appears most is the same
     co-occurrence error that has bitten this area before: size it by removal.

   The unlock curve is a step function with two cliffs and a long flat tail:

   | helpers implemented (most-needed first) | programs unlocked |
   |---|---|
   | 10 | 7 |
   | 11 | 54 |
   | 14 | 91 |
   | 16 | 140 |
   | 19 | 152 |
   | 36 | 163 |
   | 50 | 210 |
   | 84 | 247 (all) |

   Nothing moves until the eleventh, and 19 helpers gets 62% of the way. The
   flat stretch from 19 to 36 is the `Map` method family and the `Reader`/host
   builtins, which arrive as a block or not at all.

   **`examples/bench` is the cheap corner**, and the one with checked-in
   baselines (`.github/perf-baseline-selfhost.txt`). Nine of its programs need
   only one or two helpers each — `__fern_arr_push_grow`, `__str_idx`,
   `__str_slice`, `__fern_memchr`, `__fern_ascii_run`, `__fern_count_byte`,
   `__fern_arr_cow_inplace` — and eleven of twenty-two fall to a set of eleven.

   **What porting one costs.** The kernels are not translations of the arm64
   bodies: the native x86-64 backend already has SSE2 versions of every one
   (`internal/codegen/x86_64/x86_64.go`, `emitMemchrRuntime` and siblings), but
   it boxes strings as two words with SSO where this backend uses one word with
   the length at `[ptr-4]`, and it allocates against a different heap. So each
   port is the native kernel with its argument unboxing and allocation replaced
   — mechanical, but hand-written assembly, and this path still has **no corpus
   differential**. arm64's first differential run found four wrong answers and
   56 SIGSEGVs; #8044 found a wrong-answer bug in the rc helpers from compiling
   a single enum program. The net should exist before the helpers land on it.

   `dyn` is separately excluded (`ir.DynSupported()` is not passed): the ops are
   implemented but `EmitAsmModule` takes no vtable declarations, so the tables
   they read would be missing at link time.

   What remains for this step is therefore the runtime-helper table (the whole
   of the next slice), the stack-argument ABI, vtables,
   and **the corpus differential** — the discovery mechanism, not a formality:
   arm64's first run found four wrong answers and 56 SIGSEGVs, and nothing on
   x86-64 has been differentially tested at all.

   And note what the instruction counts above are NOT. `docs/SSA-REGALLOC-PLAN.md`
   §"Where that leaves phase 4" records size and correctness as settled on arm64
   and **speed as the open blocker**: seven of seventeen benchmarks run
   1.11×–1.49× slower under SSA (`sort_ints` 1.49×, `map_int` 1.28×), geomean
   ~0.92×. Fewer instructions is not faster, and on this evidence the two have
   already diverged once.

## The cutover point

Unchanged from the shelve doc's own rule, which is right: **IR → SSA → *all*
native backends, never one in isolation.** Reason 3 of the shelve still holds —
a half-migrated backend re-introduces the dual-path parity hazard.

The staging that follows from the readiness table:

1. **Give wasm multi-function support**, then build its corpus differential as
   the proof. Not the other way round: measured above, `wasmssa` refuses any
   program that calls a second user function, so a differential built first
   would report `ssa-refused` almost everywhere and gate nothing.
2. **Make x86-64 reachable.** A module-level asm emitter for `x86_64ssa`,
   then the same corpus differential. This is the long pole.
3. **Flip the shared lowering** once all three are differentially clean,
   behind a flag, with the differential oracle byte-identical across interp +
   every backend as the gate.
4. **Move RC insertion onto SSA.** This is the step the whole plan is for.
   Ownership stops being pattern-matching over an op stream and becomes
   ordinary dataflow with names, def-use and phis. The 32% blind spot closes
   by construction.
5. **Then** #7786 is standard interprocedural dataflow, and #7787 / #7789 /
   #7792 become tractable rather than heroic. The certifier (#7782 slice 3)
   lands where it is natural.

Steps 1 and 2 are ordinary engineering with a measurable finish line. Step 4 is
the one that changes what is possible.

## The cheaper route to step 4 — measured, and it works

Steps 1-3 exist to make SSA the **codegen** path. But the Perceus unlock does
not need that — it needs SSA as an **analysis** representation.

`ssa.LiftFromIR` already exists, and arm64's differential is evidence the lift
is faithful over the whole corpus: 281 programs lifted, optimised, re-emitted,
and behaviourally identical to the flat backend. Nothing about that result
depends on shipping the SSA backend.

So there is a second path to the thing this plan is actually for:

> Lift IR → SSA for **analysis only**. Run ownership as real dataflow — names,
> def-use, dominance, phis — and map the decisions back onto op positions for
> the existing emitters to consume. Keep all four shipping backends exactly as
> they are.

That closes the 32% blind spot, gives #7786 a representation it can summarise,
and makes per-path unit accounting reachable — without touching `wasmssa`'s
single-function limit or writing x86-64 a module assembler.

### Measured 2026-08-29: the lift already gives every RC operation a name

The half of this that could have killed it is whether a lift faithful for
*codegen* is also faithful for *ownership*. It is, and the mechanism is already
in `lift.go`: `ir.OpRcInc` / `OpRcDec` / `OpRcIsUnique` each become an `OpCall`
whose arguments are **SSA `Value`s popped off the lift's abstract stack**. What
the flat IR leaves anonymous on the operand stack, the lift has already bound
to a name.

Over the same corpus the 32%-blind-spot figure came from:

| | |
|---|---|
| functions that lift | **15,718 / 15,821 — 99.3%** |
| RC operations in the lifted SSA | **22,125** |
| …with a named operand | **22,125 — 100.0%** |
| `__fern_rc_inc` / `_dec` / `_is_unique` | 5,103 / 10,335 / 6,687, none unnamed |

(That population counts all three helpers, so it is not the same denominator as
the 15,516 figure above, which counted the inc/dec pair only.)

**The 32% blind spot is 0% after lifting.** Not reduced — gone, by
construction, because in SSA there is no anonymous operand.

The 103 functions that do not lift are a short list, and the tail is short too:
80 `OpStoreLocal`, 16 `call`, 4 `OpIf`, 2 `add`, 1 `OpCallDyn`. That is a
finishable list, not a research programme.

### What is left to build, and what it costs

Only the return trip: the decisions have to map back onto op positions the
existing emitters consume. The forward direction — can ownership even be
*expressed* over this representation — is answered.

Checked 2026-08-29, `ssa.Op` carries **no provenance**: no source-op index, no
position, and `LiftFromIR` records none. That is the entire gap, and it is
mechanical — the lift already holds the IR op index `i` at every case (its own
error messages print it), so populating a `SrcOp` field is an assignment per
case, not a design problem.

**One constraint falls out of that, and it decides the shape:** run the
ownership analysis on the **unoptimised** lift. `ssa.Optimize`'s passes —
constfold, CSE, LICM — synthesise ops that have no IR origin, so provenance
stops being total the moment they run. Ownership needs the CFG, def-use and
dominance, and `BuildUses` / `BuildDomTree` produce those from the raw lift
directly. Nothing in the analysis wants the optimiser.

So the shape is: lift → analyse → map decisions back by `SrcOp` → emit into the
existing op stream. The optimiser stays where it is, on the codegen path,
unaffected either way.

An insertion point needs slightly more than per-op provenance — "release x at
the end of this block" names a program point rather than an existing op — but
that expresses as *before/after the IR op this SSA op came from*, with block
boundaries mapping to the structured scopes the flat IR already has.

**Recommendation: do this before committing to the backend cutover.** It needs
no change to `wasmssa`'s single-function limit and no module assembler for
x86-64, it runs on a lift that arm64's differential shows is behaviourally
faithful across 281 whole programs, and it closes the ceiling that
`SSA-DECISION.md`'s tripwire 4 is really about. The cutover then becomes a
separate question, decided on codegen merits alone, with Perceus no longer
waiting on it.

## The conflict that has to be resolved with it

`docs/SELFHOST-SSA-DECISION.md` (#4391, 2026-07-03) decided the **opposite**
for the self-host: the stack IR is its single production lowering, SSA
`build_func` demoted to opt-in, `SELFHOST-SSA-ALWAYS.md` shelved. The stated
reason is exactly right and applies here in reverse:

> Every week both advanced was a week of work one of them would delete.

If native cuts over to SSA while the self-host stays on the stack IR, that
divergence returns at a larger scale — and **goal 2 is caught in the middle**,
because the Perceus port is precisely what has to be mirrored across whichever
representation each side settles on. The 71k-line `irlower.fern` is the current
price of having no pass to mirror; forking the representations makes that
permanent.

**So #4391 is reopened by this plan, not after it.** The encouraging part: the
shelved self-host plan records SSA reaching **100% per-function coverage**
there via `-ssa-scan`, blocked only on a Phase-4 whole-compiler memory wall. The
mirror may be nearer than the native subset language suggests.

## What this buys, stated as the goal

Fern already scores 8/10 on algorithmic completeness against the field —
drop-guided/frame-limited reuse (only Koka also has it), cross-kind reuse
donors (looser than Lean's relaxed reuse), fip/fbip verified against emitted
ops (Koka's Core-level check cannot do that), four backends. See #7784.

The gap to first is not more optimisations. It is structure (4/10) and
soundness (6/10), and both are downstream of the representation. **Koka has the
algorithms and no verifier; Roc has the verifier and not the algorithms.**
Nothing in the field is both. That is the position this plan is for, and the
32% blind spot is what currently makes it unreachable.

## Explicitly not claimed

- No performance argument. Tripwires 1–3 are unmeasured and this plan does not
  rest on them.
- No claim that wasm or x86-64 SSA are correct — that is what steps 1 and 2
  are for, and arm64's first differential run is the reason to assume nothing.
- No estimate. The steps have finish lines; how long they take is not
  something this document knows.
