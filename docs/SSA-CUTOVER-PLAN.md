# SSA cutover: the shared lowering, and why Perceus needs it

**Status:** PROPOSED, as input to the `docs/SSA-DECISION.md` re-evaluation due
**2026-09-01**. That doc says: *"If a tripwire fired, write an
`SSA-CUTOVER-PLAN.md`"*. One has. This is that document.
**Owner:** compiler / IR.
**Supersedes nothing yet** — `SSA-DECISION.md` and `SELFHOST-SSA-DECISION.md`
stand until the re-eval acts on this.

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
| `x86_64ssa` | **yes, since 2026-09-01** | **yes, since 2026-09-02** | 194/337 compared, 0 divergences; 123 refused — the runtime-helper table is still the wall, in groups now rather than one symbol |

The spread is much wider than "arm64 is ahead". One backend is corpus-complete,
one compares three fifths of the corpus and agrees on all of it, and one cannot
compile a program with two functions in it.

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

   **Where the wall is now**, over the 123 still refused with a baseline to
   compare against: back to groups, with no single symbol unlocking anything on
   its own.

   | | refused for it | refused for it ALONE |
   |---|---|---|
   | `__memcpy` | 56 | 0 |
   | `__free` | 39 | 0 |
   | `__method_string_as_bytes` | 33 | 0 |
   | the Map family (`map_new`, `__method_Map_set`, `__fern_map_hash_seed`, `__memset`) | 28 each | 0 |

   So the next slice is a GROUP — the Map methods, or the memory trio — and the
   2026-09-02 lesson below is the one to size it by.

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
   of the next slice), the float reinterprets, the stack-argument ABI, vtables,
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
