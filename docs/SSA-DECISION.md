# SSA: ship-or-shelve decision

**Status:** OPEN for codegen; SHIPPED for analysis. The layer is **not**
shelved. On the codegen side, size and correctness are SETTLED in the SSA
backend's favour — 45.3% of flat's `.text` over the corpus, 286/0 on the run
differential — and speed is the single remaining blocker on an arm64 default
flip (#4112 phase 4). The x86-64 side is blocked on coverage instead (#8822).
**Next decision point:** not a date — which of those two tracks to fund.
**Owner:** compiler / IR.

Read "Where this stands (2026-09-15)" below FIRST. It corrects two things in
the 2026-09-02 section above: that section says tripwires 1–3 are unfired (one
has), and it quotes the benchmark geomean as evidence against the backend when
the ratio runs the other way — 0.92x means SSA is ~8% FASTER on average.

## The question

`internal/ssa/` is a fully-built SSA framework — construction (dominator
tree, pruned SSA via dominance frontiers, phi insertion) plus a real pass
suite (`constfold`, `cse`, `licm`, `trivialphi`, `blockmerge`,
`branchfold`, `strength`, `sccp`). It is the second-largest test surface in
the repo (~469 tests). **But it is not on any production path.** The
shipping backends — `internal/codegen/{arm64,x86_64,wasmbin}` — all consume
the flat, structured-control-flow `ir.Program` and run only the
peephole-grade passes in `internal/ir/` (`fold`, `dce`, `copyprop`,
`constprop`, `inline`, `strength`, `tco`, `defunctionalise`). Every consumer
of the SSA layer is behind `-backend ssa`: `wasmssa`, `arm64ssa`, and — since
2026-09-01 — `x86_64ssa`. None is the default for any target.

So we are carrying a substantial, well-tested optimization framework whose
benefit to shipped artifacts is currently zero. This doc forces the call:
either route a production backend through SSA on a date, or formally shelve
the migration so the code's status is explicit rather than ambient.

## Decision: shelve, with a tripwire

We **shelve** the SSA-on-production migration for now and keep `-backend ssa` (wasm) as
the experimental proving ground. Rationale:

1. **It is not load-bearing for current priorities.** The active fronts are
   the RC + Perceus + FBIP memory work (`RC-PERCEUS-PLAN.md`,
   `RC-STRINGS-PLAN.md`) and self-hosting. Neither is bottlenecked on
   mid-level SSA optimizations; both are bottlenecked on *correctness* of
   reclamation and codegen parity. Spending the cutover budget now competes
   directly with the project's stated goals.

2. **The target workload doesn't yet demand it.** Fern's stated use case is
   short-lived CLI tools and edge handlers. For those, startup latency and
   binary size dominate; aggressive loop optimization (LICM, SCCP, GVN-class
   CSE) is not where the wins are. The peephole + TCO + inline pipeline
   already collapses the representative example in the README to a single
   `const; return`.

3. **A half-migrated backend is worse than none.** Routing one production
   backend through SSA while the others stay on flat IR re-introduces the
   exact dual-path parity hazard we work hard to avoid elsewhere (every
   feature must land on every backend). A cutover only makes sense if it is
   the shared lowering all native backends consume.

4. **Shelving ≠ deleting.** The framework stays, stays tested, and stays
   exercised through `-backend ssa` (wasm). We are deferring the *production cutover*,
   not abandoning the investment.

## Tripwires (any one flips this back to "schedule the cutover")

- A profiled, real Fern program (not a microbenchmark) where the missing
  SSA-class optimization — LICM, global CSE/GVN, or SCCP — is the
  demonstrated bottleneck, and the flat-IR peephole passes provably cannot
  reach it.
- The self-hosted compiler's own generated code becomes a measured
  bottleneck for `make distcheck` / bootstrap turnaround in a way SSA passes
  would address.
- `-backend ssa` (wasm) reaches feature parity with `wasmbin` and outperforms it on the
  e2e corpus by a margin that justifies making it the default wasm path
  (which would make SSA production by definition).
- The flat-IR optimizer in `internal/ir/` grows enough ad-hoc
  cross-block analysis that we'd be reimplementing SSA badly — at which
  point doing it properly wins.

## Re-evaluation input (2026-08-29)

**Tripwire 4 has fired**, and two claims in this document are stale. See
`docs/SSA-CUTOVER-PLAN.md` for the evidence: the ad-hoc control-flow analysis
inventory in `internal/ir` (and its self-host mirrors), the measurement that
32% of the IR's reference-count operations act on unnamed operand-stack values,
and the readiness of the three SSA backends.

Stale here: arm64 SSA is no longer "a subset" — 281 of 286 corpus programs
compared, 0 refused, 0 divergences, and the known-divergences file is empty.
The four wrong answers and 56 SIGSEGVs recorded below were fixed.

Tripwires 1-3 remain unfired and unmeasured.

## Re-evaluation input (2026-09-02) — the date passed; the call is OPEN

The 2026-09-01 date arrived and nothing was recorded against it. This section
is the "fresh numbers" half of the procedure below, so the date does not rot a
second time. **It does not make the decision**, which is the owner's: neither
"re-shelve and set the next date" nor "schedule the cutover" is chosen here.

Tripwire state is unchanged from 2026-08-29: **4 fired, 1-3 unfired and
unmeasured.** `docs/SSA-CUTOVER-PLAN.md` was written, as a fired tripwire
requires.

What moved since, all on the x86-64 backend the plan named as the long pole:

- **It is reachable from the CLI** (#8012). The plan's stated blocker — no
  module-level assembly emitter — was stale; `x86_64ssa.EmitAsmModule` had
  been there all along and nothing selected it.
- **`EnumSentinel` landed** (#8044), which was the emit-stage long pole, along
  with a wrong-answer bug it exposed in the rc helpers: five of them returned
  the rc header where the IR contract says they return their pointer.
- **Coverage has two numbers and they are far apart.** Emitting assembly:
  58 → 256 of 317 corpus programs. Producing a runnable binary: **8 → 9.**
- **The helper gap is 84 symbols** (#8047), with a step-function unlock curve —
  nothing moves until the eleventh, 19 reach 152 programs, then it flattens.
  `x86_64ssa` has 13 helper emitters against `arm64ssa`'s ~120.

Two things that bear on the choice and are easy to lose:

- **Fewer instructions is not faster.** `docs/SSA-REGALLOC-PLAN.md` records
  size and correctness as settled on arm64 and **speed as the open blocker**:
  seven of seventeen benchmarks run 1.11x-1.49x slower under SSA, geomean
  ~0.92x. On x86-64 the allocated path is now smaller on call-heavy loops too
  (199 instructions against 213 on the enum-match loop in
  `TestX86_64SSABackendCLI`), but the reason it was ever larger is untouched:
  with no call-clobber awareness it still saves every caller-saved allocatable
  register around every call — 34 push/pop against the stack machine's 6 on
  that program.
- **The alternative route is further along than this document suggests.** SSA
  as an ANALYSIS representation rather than a codegen path is substantially
  built: `Op.SrcOp` provenance with a totality gate
  (`internal/e2e/ssa_lift_provenance_test.go`), and
  `internal/ssa/{ownership,ownership_solve,ownership_returns,units,certify}.go`
  are 2,100+ lines of interprocedural ownership over the lifted form. That
  route feeds roadmap goal 2 (Perceus in the self-host) directly, where the
  codegen cutover does not.

## Where this stands (2026-09-15)

**The codegen question is still OPEN, and the "tripwires 1–3 unfired" line
above is STALE.** This section corrects it rather than deciding on top of it.

**Tripwire 1 has fired.** #8822 — open, labelled `backend` / `codegen` /
`performance` — is a profiled real program, not a microbenchmark:
`coreutils/sort.fern` is 4–5x slower than GNU, callgrind attributes ~1500
instructions per comparison, and the issue's own diagnosis names the cause as
"every local is a memory slot and every temporary goes through push/pop". It
then names the remedy explicitly:

> `-backend ssa` is the register-allocating emitter and **would answer (1)
> directly**, but it does not cover the I/O surface: building `sort.fern` with
> it reports 17 undefined call targets.

The letter of tripwire 1 names LICM / GVN / SCCP and this is register
allocation, so it is arguably tripwire 1's *spirit* rather than its text. The
distinction does not rescue the "unfired and unmeasured" claim: a profiled real
Fern program where the SSA emitter is the demonstrated answer is exactly the
evidence that clause was waiting for, and it has existed since 2026-09-07.

**And the work is live.** `docs/COREUTILS-SSA-ARGS-2026-09-08.md`,
`COREUTILS-SSA-BRANCHES-2026-09-08.md` and
`COREUTILS-X86-SSA-SLICES-2026-09-08.md` are a stream of SSA-backend work under
epic #8278, and the index-helper inlining landed on both native SSA backends on
2026-09-15. Each of those documents is careful to say it proposes **no
default-backend switch** — so the active work is not itself a cutover, but it
is the opposite of a shelved layer.

**Read the benchmark ratios carefully — the 2026-09-02 note above does not.**
In `SSA-REGALLOC-PLAN.md`'s tables a ratio is SSA's time as a fraction of the
flat backend's, so below 1.0 is FASTER (`int_loop 0.44x` is listed as a win).
The geometric mean of ~0.92x therefore says SSA is about **8% faster on
average and much smaller** — it is not evidence against the backend, and
quoting it beside "seven benchmarks are slower" as though it were is a
misreading.

What the current evidence supports:

- **The analysis route is real, funded and already built** — `Op.SrcOp`
  provenance with a totality gate, plus
  `internal/ssa/{ownership,ownership_solve,ownership_returns,units,certify}.go`,
  2,100+ lines of interprocedural ownership over the lifted form, reaching the
  32% of reference-count operations that act on unnamed operand-stack values.
  It answers tripwire 4 and feeds roadmap goal 2 directly. Nothing here is in
  tension with the codegen question; both roles use the same lift.
- **Size is settled, and it is not marginal.** Over the corpus the SSA backend
  emits **45.3% of flat's `.text`**, with 280 of 281 programs individually
  smaller and the ratio improving with program size — the ten largest land at
  10–31% (`miniparse` 863,904 → 86,636 bytes). All 17 benchmarks are smaller.
  Since binary size is epic #4109's whole objective, this is the goal being met.
- **Correctness is settled** — 286/0 on the `asm_run` corpus differential,
  285/0/1 on `interp_run`.
- **Speed is the single open blocker, and it is converging.** Two rounds of
  fixes have landed since the 09-02 note: static `.rodata` closure cells plus
  inlined raw pokes (`map_int` 3.25x → 1.61x, `map_string` 2.25x → 1.30x,
  `tokenize` 1.13x → 0.97x, peak RSS halved onto the flat backend's), and
  `helperClobbers` call-clobber-aware caller-save (`map_probe_chain` 1.91x →
  1.56x, `pvec_with` 2.09x → 1.87x, `.text` to 95.79% of what it was,
  `ordmap_insert`'s stack traffic down 25%). Call-clobber awareness is written,
  not outstanding.

**Why this still is not a default flip**, in the plan's own words: *"an average
is the wrong test for a default. Flipping it ships a 20–49% slowdown to anyone
whose workload looks like `cmp.sort` or `core/map`."* That lands squarely on
this project — CLAUDE.md records the self-hosted compiler as the biggest
workload and `core/map` as its most demanding consumer, so defaulting today
would slow the compiler. The worst remaining rows are `ordmap_insert` 2.28x,
`pmap_insert` 2.02x, `pvec_with` 1.87x, `map_int` 1.65x, `map_probe_chain`
1.56x.

**Two tracks, two different blockers — do not conflate them:**

| track | blocker | state |
| ----- | ------- | ----- |
| **arm64 default flip** (#4112 phase 4) | codegen quality in loop bodies, with seven named reproducers | size and correctness settled; speed converging, worst rows are persistent collections paying per-call RC helpers |
| **x86-64** (#8822) | was coverage; **now loop-body codegen quality too** — `sort.fern` builds under `-backend ssa` as of 2026-09-16 and runs no faster (the measurement below) | `sort` and seven other coreutils build; `wc`/`cat`/`head`/`tail` want four more helpers; the corpus differential's remaining refusals are groups (`__memcpy`, `__alloc`, the Map family) |

The next decision point is which of those to fund first. They are independent,
and the x86-64 one is cheaper to make answerable: coverage work is mechanical,
where loop-body codegen quality is open-ended.

### Measured 2026-09-16: `sort` builds under x86-64 SSA, and it is not faster

With `args`, `env` and `stat` given emitters (the handle family and
`read_chunk` landed the day before), `coreutils/sort.fern` builds under
`-target x86-64-linux -backend ssa`, and so do `uniq`, `tr`, `cut`, `comm`,
`fold`, `nl` and `paste`. #8822's claim is measurable, and measured it does not
hold as the backend stands. 2M-line inputs, `LC_ALL=C`, best of three on the
4-core dev container:

| workload | flat | SSA | GNU 9.4 |
| --- | --- | --- | --- |
| `sort`, 11 lowercase letters a line | 2.99 s | 3.12 s | 0.70 s |
| `sort -n`, signed 10-digit integers | 7.76 s | 7.79 s | 1.08 s |

The three outputs are byte-identical on both inputs. callgrind on `sort -n`
over 200k lines: 5.07e9 instructions flat, 4.83e9 SSA, 5% fewer. The static
count goes the other way on this program, 61,899 instructions flat against
65,475 SSA; the 45% figure above is the examples corpus, where the flat
backend's push/pop traffic dominates small functions.

`magcompare` under SSA has its hot values in registers (rbx, r12, r14), so
the allocator did what it is for. What it emits around them is the cost:
every block ends in a `jmp` to the label that follows it (712 such jumps in
the file, 3,465 block-end jumps in all), a constant is moved into a register
before each compare (294), the string length is reloaded from `[base - 4]`
on every bounds check (281, loop-invariant in the loops that matter), and a
`movsxd` follows each `movzx byte` (161). None of that is allocation; it is
instruction selection and block layout, and it leaves the function at 362
instructions against the flat backend's 434 rather than the several-fold
reduction the issue's arithmetic assumed.

**Later the same day, the four shapes closed** (fallthrough to the block
laid out next, the constant as the compare's or binary op's immediate, the
bounds check comparing against the length in memory, no fixup after a
zero-extending byte load): `sort` 2.61 s against the flat backend's 2.75 s,
`sort -n` 6.81 s against 7.78 s, outputs byte-identical, 4.19e9 instructions
on the 200k-line `sort -n` against the flat build's 5.07e9, and 61,287 static
instructions against the flat backend's 61,899. `magcompare` is 314
instructions. What it still pays: a `jmp` where neither arm is the next
block (layout, not selection), a `movsxd` after each i32 add (the width
contract), a copy from the scratch a load lands in to the value's home, and
two spilled parameters reloaded from the frame every iteration. That is the
next slice, and arm64's emitter has the first two of the four already
(0 jumps to the next label, 4 constants moved before a compare, on the same
program) but reloads the length 401 times.

**Layout, the next day.** The `jmp` where neither arm is the next block was
arm64's `layoutOrder`, a walk that follows each block's fallthrough successor
(a jump's target, a conditional's false arm) as far as it goes and parks the
other arm for a later chain, applied at render time to the shared abstract
program. It now lives in `x86_64ssa` as `LayoutOrder` and both renderers emit
in that order. On `coreutils/sort.fern` under x86-64 SSA: 2,005 `jmp` → 1,211,
60,277 static instructions → 59,483, `sort -n` on 200k lines 4.19e9 → 4.12e9
instructions, and 2M lines `sort -n` 6.57 s → 6.48 s, `sort` 3.04 s → 2.96 s
(flat: 7.81 s and 2.97 s), outputs byte-identical. The remaining three
shapes (the `movsxd` after an i32 add, the copy out of a load's scratch, the
two reloaded parameters) are still the next slice.

**So the two tracks have one blocker.** Coverage was what kept x86-64's
claim unmeasurable; measured, the x86-64 emitter is blocked on the same
thing as arm64's phase 4: loop-body code quality, now with a profiled real
program on each ISA, and the first four items of it are worth 13% of the
instructions and the crossover from slower than flat to faster. The coverage that remains (`wc`, `cat`, `head` and
`tail` want the Reader/Writer `stat`, Reader `seek` and `sleep_ms`; the
corpus wants `__memcpy`, `__alloc` and the Map family) still widens the
differential, but it is not what makes `sort` faster.

### Measured 2026-09-16: the rc primitives inlined on x86-64

callgrind on `examples/bench/ordmap_insert.fern` under x86-64 SSA, once the
freelist and string reclaim were in, put 24% of the run's instructions inside
`__fern_rc_is_unique`, `__fern_rc_dec` and `__fern_rc_inc` — six-instruction
guard chains reached by a call each, with the caller-saves the allocator
plants around a call whose callee it knows nothing about. The arm64 plan
declined to inline them for size and took call-clobber-aware saves instead,
and its persistent-collection rows stayed at 1.9-2.3x. The x86-64 renderer
now renders the three at their call sites (`rcinline.go`), exactly as the
helper bodies read, and the trade measured on this machine, best of five,
ratio to the flat backend:

| bench | before | after | static instructions |
| --- | --- | --- | --- |
| `ordmap_insert` | 1.80x | **1.23x** | +13.4% |
| `pvec_with` | 2.04x | **1.32x** | +4.4% |
| `pmap_insert` | 1.76x | **1.28x** | +4.8% |
| `map_int` | 1.22x | 1.22x | 0% |
| `map_probe_chain` | 1.16x | 1.15x | |
| `sort_ints` | 0.91x | 0.82x | |
| `coreutils/sort.fern` | | | +2.3% |

`ordmap_insert`: 7.41e8 instructions before, 5.64e8 after, flat 4.90e8. The
size cost lands where the sites are — a program with 165 to 200 of them
grows by the guard chain at each — and is the same cost the flat backend
carries; the epic's size goal is measured against the flat backend, and the
module is still smaller than its flat twin. arm64ssa still calls the helpers;
whether it takes the same trade is its own measurement.

**The allocation fast path, the same day.** `enum_match` ran 3.3x the flat
build's time on 1.34x its instructions, with no page faults and fewer
mispredicted branches: the profile was `make` and the enum drop with
`__ssa_alloc_pres`, `__alloc`, `__free` and `__fern_box_free` per node, and
the allocation trampoline (nine pushes and a `pushfq`/`popfq` around every
allocation) was the cost. A constant-size `OpAlloc` names its freelist class
at compile time, so it pops that class's list inline and takes the
trampoline only when the list is empty; a `__fern_box_free` of a constant
size pushes inline (`allocinline.go`, the exact 16-byte tier only). Best of
five, ratio to the flat backend:

| bench | before | after | static |
| --- | --- | --- | --- |
| `ordmap_insert` | 1.21x | **0.95x** | +7.4% |
| `pmap_insert` | 1.28x | **1.12x** | +4.9% |
| `pvec_with` | 1.32x | **1.12x** | +4.9% |
| `enum_match` | 3.25x | **2.33x** | +3.4% |
| `struct_drop` | 2.03x | 1.82x | +0.7% |
| `map_int`, `array_append`, `string_build` | | unchanged | 0% |

`enum_match` and `struct_drop` still pay the drop side: the per-type drop
helpers call `__fern_str_dec` and `__fern_box_free`, which call `__free`,
which computes the class again for a size it was handed in a register.

**Constant shift counts and power-of-two divisors, the same day.** With the
allocator out of the way `enum_match` ran 1.33e8 instructions to the flat
build's 1.30e8 and still took 2.33x the time: three `idiv` per iteration for
`k % 4`. `internal/ir`'s strength reduction deliberately leaves signed
division by a power of two alone (an arithmetic shift rounds the wrong way),
and the flat backends lower it themselves with a sign bias; the shared SSA
emitter now does the same (`emitPow2DivRem`), for both SSA backends, and a
constant shift count is the instruction's own immediate rather than a
register copied into `cl` (or an AArch64 register) — the foldable set
admits shifts and power-of-two divisors. `enum_match` 2.33x → **1.00x**;
`map_int` 190 static instructions fewer; `coreutils/sort.fern` 15 `idiv` →
2 and 668 `push rcx` → 487. `struct_drop` (1.88x) is unmoved: its cost is
the drop side above.

### Per-backend disposition

| backend | disposition |
| ------- | ----------- |
| `internal/codegen/wasmssa` | **RETIRED** (#9397). The relooper-era emitter, one fixed page of memory that never grows, and the only SSA backend with no corpus differential — its cover was its own hand-written cases. It existed to satisfy the "keep the layer exercised end-to-end" clause below, which `arm64ssa` now discharges far better. This is independent of the codegen question: the coreutils work is native, wasm is not where a comparison-bound utility runs, and nothing in #8278 or #8822 touches it. `-backend ssa` no longer accepts a wasm target. |
| `internal/codegen/x86_64ssa` | **KEPT, and its coverage gap is live work.** It is the backend #8822 names as the direct answer to the epic's dominant cost, blocked on 17 undefined call targets in the I/O surface. `arm64ssa` also imports its layout and call-coalescing model, so it could not be cut on its own even if that were wanted. |
| `internal/codegen/arm64ssa` | **KEPT and load-bearing**, twice over: the emit target for `internal/semir`'s typed pre-RC ownership pipeline (`-backend typed-ssa`, `cmd/fern/typedssa.go`), and the subject of the coreutils perf stream. Its 281-program corpus differential is what keeps the SSA layer honest end-to-end. |

The self-host mirror — the stack IR is the single self-host production
lowering — was taken on 2026-07-03 and is untouched by any of this:
`docs/SELFHOST-SSA-DECISION.md`.

## Maintenance contract

The framework is maintained for BOTH roles — analysis, and an emitter that is
not defaulted but is actively measured:

- Keep `internal/ssa/` building and its tests green in CI (they already run).
- Keep the **arm64 corpus differential**
  (`internal/e2e/arm64_ssa_differential_test.go`) green, with an empty
  known-divergences file. This is the clause that replaces "keep
  `-target wasm32-wasi -backend ssa` in the e2e matrix": it exercises the lift
  and the register allocator over 281 programs rather than a handful of
  hand-written cases, so it is a real gate rather than a reminder that the
  layer compiles.
- `LiftFromIR` must stay **total** over what `internal/ir` emits — the
  provenance gate (`internal/e2e/ssa_lift_provenance_test.go`) is the one that
  matters now, because an analysis that cannot see a construct silently
  under-reports rather than refusing to emit.
- New IR ops / language features are **not** required to land in the SSA
  *emitters*. They ARE required to lift, for the same reason.
- A gap that blocks a `-backend ssa` build of a real program is logged against
  #8822, not silently absorbed: the helper-coverage gap is the thing standing
  between the epic and its measurement.

## Reconciliation: the native `x86_64ssa` / `arm64ssa` backends (2026-07-03, #4391)

Since this doc was written, two native SSA backends grew —
`internal/codegen/x86_64ssa` (#4266) and `internal/codegen/arm64ssa`
(#4310) — joining `wasmssa`. This is **not** the September re-eval happening
early: it is an **extension of the "keep an experimental proving ground"
maintenance contract above**, not a production cutover. The decision stands —
SSA remains shelved for production; these backends are the sanctioned proving
ground on the native side, exactly as `-backend ssa` (wasm) is.

**Their gate (so the status is explicit, not ambient):**

- **Not on any production path.** The shipping backends stay
  `internal/codegen/{arm64,x86_64,wasmbin}` over the flat `ir.Program`. No
  compile reaches `x86_64ssa` / `arm64ssa` unless a test / experimental flag
  selects it.
- **Differential-gated.** Each carries its own emit + run tests
  (`internal/codegen/x86_64ssa/*_test.go`, `arm64ssa/gas_run_test.go`, …) and
  must stay byte-identical-in-behaviour to the flat-IR backends and the
  interpreter across their covered subset. A divergence is a bug in the
  proving ground, not a reason to ship it.

  On **arm64** that is now enforced by a corpus differential rather than by
  hand-written cases: `internal/e2e/arm64_ssa_differential_test.go` runs every
  `examples/**` program through both `-target arm64-linux` and
  `-target arm64-linux -backend ssa` and compares exit status and stdout, with a
  refusal counted as the documented coverage endpoint rather than as a pass.
  Its first run found four wrong answers and 56 heap SIGSEGVs; the
  divergence file (`internal/e2e/testdata/arm64-ssa-diff-known-divergences.txt`)
  has no rows today. `x86_64ssa` has the same shape of leg
  (`internal/e2e/x86_64_ssa_differential_test.go`) over the same corpus, but it
  compares 30 programs to arm64's 281 — the rest are refused for missing
  helpers. **`-backend ssa` (wasm) still has no corpus differential**; its only
  cover is its own hand-written cases.
- **Not required to carry new features.** As with `-backend ssa` (wasm), a language
  feature these backends can't yet express is a logged gap, not a blocker.

**The re-evaluation does not change this.** Whenever the call is made (or a
further tripwire fires), the cutover point is still the shared
one — IR → SSA → *all* native backends — never one backend in isolation. The
self-host mirror of this decision (the IR path is the single self-host
production lowering; SSA `build_func` demoted to opt-in) is
`docs/SELFHOST-SSA-DECISION.md`.
