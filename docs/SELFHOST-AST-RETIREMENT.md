# Retiring the self-host legacy AST emitters (#3457)

Status: **IN PROGRESS — slice 1 done; #3425 (the memory gate) CLOSED; slice 2
is now unblocked.** This doc is the single home for the #3457 endgame (retire
`asm.fern` / `asm_arm64.fern` / `wasm.fern`, the pre-IR AST→asm emitters, plus
the ~512-function merged-bundle budget). It exists because the analysis kept
being re-derived and mis-scoped — CLAUDE.md's "VERIFY tracker state against the
code first; #3457's blockers have repeatedly lagged reality" warning applies
especially here. Everything below was verified against the code (2026-07-26).

**Update 2026-07-26 — #3425 is closed.** The large-tier freelist port
predicted below LANDED on both native backends (x86 `asm_ir.fern` #5609, arm64
`asm_arm64.fern` #5614), and the direct proof it was meant to unblock now
passes: `TestSelfHostPerModuleFixpointX86_64` (env-gated,
`RUN_PERMODULE_FIXPOINT=1`) is **GREEN** — a self-host-BUILT compiler (gen1)
per-module-emits the whole compiler (35 units) in ~998 s with **no arena OOM**,
and gen0 == gen1 byte-identically across all 35 units (per-module emit is
self-reproducing). Measured gen1 peak: **~7.6 GB RSS per emit window** — under
the 8 GiB arena ceiling that the leaked large blocks previously blew past. So
**the arena wall is no longer the slice-2 blocker**; the only remaining slice-2
obstacle is the *CI-affordability* of the gen1 per-module fixpoint (serial
~16.6 min > a 13-min shard; 2-way parallel needs ~15 GB → OOM-risky on a 16 GB
runner). See "Slice 2" below for the now-concrete plan.

## Where the roadmap stands (context)

Goals 1 and 2 are **essentially complete**, so #3457 is the remaining frontier
before the native freeze (`docs/NATIVE-CONVERGENCE.md`):

- **Goal 1 (full IR subset).** The last *per-function* remnant — genuinely
  two-typevar `Result[T, E]` returns — closed 2026-07-26 (`parser.fern`
  `result_two_bare_vars`, clause (c′)). The only AST fallbacks left are the
  non-per-function ones **owned by #3457**: the merged-bundle path + its budget.
- **Goal 2 (Perceus reuse).** Substantially met; `docs/SELFHOST-PERCEUS-REUSE.md`
  §2/§3 (with its 2026-07-17 correction) is the record. The one genuinely-open
  delta (own-param enum/string field reuse) is *fundamentally* blocked — a
  parameter has no bind literal to prove field freshness — not a tractable slice.

## What actually still reaches the x86 AST emitter (measured, 2026-07-27)

The sections below reason about the AST emitter's *call sites*. That is the
wrong granularity for the x86 endgame: `asm.emit_module` is a thin shell whose
body is reached only when `asm_ir.emit_module_ir_gated` declines, so the
question is which PROGRAMS still make it decline. That had been re-derived by
inspection; it is now measured directly — run `internal/e2eselfhost` with
**`FERN_STRICT_IR=1`**, and every failing test is by construction one that still
needs the AST emitter.

That flag is in-tree (`asm_ir.fern`, #5646). It turns every bail site into an
`exit(3)` naming what bailed, instead of the silent fall-through; off by
default. It replaces the hand-patch-and-rebuild recipe this section used to
carry, and it also fixes that recipe's two footguns:

- **Placement.** The naive hand-patch instruments the `emitted == ""` fallback
  itself, which over-reports: `emitted` is also `""` when IR was never
  *requested*, and `TestSelfHostAsmIRPath` runs every case twice — with and
  without `-ir` — as a differential control, so such a probe trips on ~80
  deliberate AST runs that are not declines at all. The flag fires only on an
  ACTUAL bail inside the gate, so those runs are silent.

  **Correction (2026-07-29):** this bullet previously said the flag "is checked
  inside the `use_ir` branch, so it cannot see" the `-ir`-off runs. That is
  wrong, and the distinction matters when planning a sweep. `asm.emit_module` —
  the AST path's own entry — itself calls `emit_module_ir_gated`, gated on
  `asmcore.new_state().use_ir`, which defaults **true** regardless of the
  driver's `-ir` flag (`asm_run.fern` never toggles it). An `-ir`-off run
  therefore DOES enter the gate and CAN refuse; the sweep's single
  `asc-none-opt-nested` failure was reported against the `ir=false` leg for
  exactly this reason. What saves the sweep is that a bail has to really happen,
  not that the run is skipped.
- **Visibility.** `eprint` alone is not enough — the e2e harness helpers capture
  the driver's **stdout** only, so driver stderr is discarded unless a bespoke
  test binds `cmd.Stderr`. The flag aborts, which is what makes the signal
  survive.

Beyond probing, the flag exists because the fallback's premise is not free.
Falling back is only SAFE when the AST emitter can express what the IR path
declined; when it can't, the fallback emits wrong code rather than failing.
#5642 is the worked example — `match (a +? b)` had no `ExprBinary` case in
`lower_match`'s scrutinee-type recovery, so the enclosing function bailed to an
emitter with no checked-operator lowering at all, and the 46 resulting failures
read like several unrelated bugs (wrong match arm, payload read as zero,
SIGABRT) rather than one unsupported construct. Verified against that exact
regression: with #5642's `lower_match` fix reverted, the checked-operator case
in `TestSelfHostStrictIRX86_64` silently emits AST asm that exits **1** instead
of 10, and under the flag the same driver refuses with `FERN_STRICT_IR: f — the
IR path bailed to the AST emitter`.
`TestSelfHostStrictIR*` (`internal/e2eselfhost`) is the standing tripwire: a
corpus that must NOT refuse, plus an over-budget program that must.

### The Option/Result recovery audit (2026-07-29, #5646 option 3)

#5646 asks whether the checked operators were the only gap in the
shape-enumerating `Option`/`Result` recoveries and notes that nobody had looked.
Looked, with the flag: probe each shape that can yield an `Option`, run it under
`FERN_STRICT_IR` (a refusal is a genuine gap), and compare the answer against
the interpreter oracle.

**Six shapes lower correctly** — `bs[i].o`, `x.i.o`, `bs[i].o?`, `t.N.o`,
`b.method()`, `Some(f())`. **Four gaps, three since closed, all with SAFE AST
fallbacks** — the AST
emitter compiles each of them correctly, so unlike #5642 nothing is
miscompiled; only the routing is wrong:

| shape | status |
|---|---|
| `aoa[i][j]` — index whose base is an index | **closed** — shared `arr_tag_of` |
| `t.N[i]` — index whose base is a tuple element | **closed** — same |
| `match (f())` where `f` is a closure LOCAL | **open** — see below; first recorded as `fs[i]()`, which was the wrong characterisation |
| `None as Option[T]` — an ascription scrutinee | **closed** — shared `unary_opt_type` |

#### The one that is still open, characterised properly

It was first written down as "calling a fn-typed array element returning
`Option`". **The array is incidental.** Measured on the merged tree, with
`FERN_STRICT_IR` as the oracle for routing and the interpreter for the answer:

| shape | |
|---|---|
| `var f: () => Option[i32] = <lambda>; match (f())` | **bails** |
| `var f: () => Option[i32] = g;` (a NAMED fn) `match (f())` | lowers — `try_opt_type` resolves it via `opt_ret_type` |
| `function call(f: () => Option[i32])` … `match (f())` | lowers — the `closure_opt_rets` param seeding |
| `var o: Option[i32] = f(); match (o)` | **lowers** — so the call itself is fine |
| `var fs: (() => i32)[]; fs[0]()` | lowers — non-`Option` closure arrays are fine |

So it is not "fn-typed locals are unsupported", not "`Option` through a closure
is unsupported", and not about arrays. It is specifically **a match whose
scrutinee is a call through a closure local**, and binding the result to an
annotated local first is a working rewrite.

**Do not assume the fix is a `closure_opt_rets` entry for the local.** That was
tried — `mark_closure_opt_ret` recorded at the `mark_closure_local` site, with
the return recovered from the lambda's own `ret_type` — and it is **completely
inert**: byte-identical verdicts on every probe above, so the change was
reverted rather than landed. `clo_init` *is* true for a lambda init
(`irlower.fern`, the `ExprLambda(_) => { clo_init = true; }` arm), so the code
runs; the read side simply is not what declines. Whatever bails is upstream of
the scrutinee-type recovery, and the next attempt should **instrument the bail**
rather than reason about which resolver arm is missing — three successive
hypotheses about that were wrong.

#### Instrumented (2026-07-29) — the bail is `const_func("main$clo")`, a lift gap

The "instrument the bail" step above was done. `asm_ir_run -ir-probe` on the
canonical `var f: () => Option[i32] = () => Some(7); match (f())` reports
**`main: BAIL lower const_func`** — TWO bails, and the second is the load-bearing
one. Instrumenting `const_funcs_only_known` (asm_ir.fern) to print the offending
name shows the unresolved reference is **`main$clo`** — the *first-class
(escaping) closure* convention (`<cur_fn>$clo`, emitted by the `ExprLambda` arm
at `irlower.fern:9942`). So the lambda is being lowered as an escaping closure
whose `main$clo` body was never hoisted/registered, and `const_funcs_only_known`
rejects the module. This is **not** a scrutinee-type-recovery problem at all —
confirming "upstream of the scrutinee-type recovery".

Why only the INLINE match: in the working rewrite `var o = f(); match (o)` the
capture-free lambda is lifted to a registered `__lam_N` (a plain fn pointer), so
`f` binds a fn-name and no `$clo` is emitted. In `match (f())` the lambda is
**not** lifted, stays inline, and hits the escaping-closure arm. The lift's
call-site walks do not descend into a `match` statement: `subst_fcall_stmts`
(`irlower.fern`, ~line 39616) rewrites `f(...)` inside `StmtReturn` / `StmtVar` /
`StmtExpr` / `StmtAssign` / `StmtIf` / `StmtWhile` / `StmtFor` but has **no
`StmtMatch` arm** (the `_ =>` passes a match through untouched), and the
capture-free binding-lift's use-analysis has the same blind spot — so a binding
used only as `match (f())` is not recognised as call-only and is left inline.

Two candidate fixes, both to be validated against the whole-compiler byte-identity
fixpoint (the risk is changing what lifts for existing lambda-plus-match code):
1. **Complete the lift walks for `StmtMatch`** — traverse the scrutinee, arm
   bodies, and guards in `subst_fcall_stmts` and the parallel call-only
   use-analysis, so the binding lifts to `__lam_N` exactly as the `var o = f()`
   rewrite already does. Most direct; closes both the `const_func` and the
   `lower` bail at once.
2. **Desugar `match (<call-through-fn-local>)` → `var $s = f(); match ($s)`**
   before the lift pass — reuse the proven case-A path. Narrower blast radius
   (only this shape, which currently bails to AST anyway), but needs the
   fn-local/closure classification available at the desugar site.

The `const_func("main$clo")` finding was reproduced with a throwaway `eprint` in
`const_funcs_only_known` (reverted); re-add it there if re-deriving.

Two things worth carrying forward. First, **`TestSelfHostAsmIRPath` is 80/80
clean under the flag** once the ascription case closed — independent evidence the
per-function IR subset is as mature as CLAUDE.md claims, measured rather than
asserted. Second, **a
safe-fallback gap is invisible to the differential suites by construction**: the
exit codes agree, so only a routing assertion or the flag can see it. That is
the class of finding the flag exists for, and it is the opposite of #5642's
class — worth keeping distinct, because a wrong-answer bail is urgent and a
right-answer bail is only goal-1 debt.

To reproduce the sweep: `FERN_STRICT_IR=1 go test ./internal/e2eselfhost -run
TestSelfHostAsmIRPath`. When linking a driver's `.s` by hand to check an answer,
use `gcc -nostdlib -static` — a plain `gcc -o` collides with `Scrt1.o`'s
`_start` and every program then "exits 1", which reads exactly like a
miscompile.

Result: **27 failures, 24 of them genuine** (the other 3 are unrelated — the
`wasm-tools validate` component cases and an arm64 `R_AARCH64_CONDBR19`
relocation overflow). The run hit its 60-minute timeout with
`TestSelfHostModloadPerModuleWholeCompilerX86_64` still going, so treat 24 as a
lower bound. Re-encoding the bail reason in the exit code and re-running a
representative subset splits it three ways:

| Reason | Count | What it is | Status |
|---|---|---|---|
| `no-funcs` | 39 | SCRIPT-shaped source — top-level statements, no `main`, so nothing for `_start` to `call`. The gate's `funcs.len() == 0` arm fires before the `has_main` one. | **CLOSED** — `asmcore.synth_script_main` desugars it to `function main(): i32 { … }` (guarded by `TestSelfHostScriptMainIRX86_64`) |
| `ineligible-fn` | 105 | Builtin METHODS the AST emitter intercepts and the IR path did not lower. | **CLOSED.** Landed: `is_zero`/`is_positive`/`is_negative`/`is_even`/`is_odd` (#5659), `abs`/`sign` (#5661), `xs.sum`/`product` (#5664), `n.pow` (#5666), `xs.index_of`/`contains` (#5667), `is_empty` (#5669), `s.first_byte`/`last_byte` (#5671), `args_count` (desugared to `args().len()`), `arg_at` (a real op, kind 210 — the `args()[i]` desugar was rejected because it would allocate all of argv per loop iteration, O(1)→O(n); the register backends call a new rc-headered `__fern_arg_at_rc`, wasm a new `$__fern_arg_at` sharing the wasi args imports), `xs.min`/`max` (the `len==0` branch + the Option box moved INTO Fern-source helpers — `asmcore.rt_src_arr_i32_min_max_opt` — so the call site is a plain `call_direct` like `sum`; the helper's composite return type is registered for the scrutinee resolvers via `irlower.builtin_arr_opt_ret_type`, since `opt_ret_fns_of` cannot see a runtime helper). **No `ineligible-fn` items remain** *of the builtin-method kind listed here* — every builtin the AST emitter intercepts now lowers on the IR path. This does NOT mean the category is empty: 17 subtests still decline on non-builtin grounds — see "`ineligible-fn` is NOT closed" below. |
| `over-budget` | 4 | `import "std/array"` and friends push the merged module past the 512-function budget. Same gate as the whole-compiler bundle, reached by ordinary programs. | **IN PROGRESS.** `asm_load_run`'s default merged path now routes an over-budget-but-eligible program (`512 < merged funcs < 1500`) through a per-module IR concat instead of the AST emitter (`emit_per_module_concat` + `prune_to_reachable`, #5676; guarded by `TestSelfHostOverBudgetPerModuleIR`). The `< 1500` bound keeps the whole-compiler self-compile out (single-process concat of ~2040 funcs OOMs the arena). Remaining: `asm_ir_run`'s AST fallback (single-module — the concat's per-module split does not map) and lifting the budget for the whole-compiler bundle itself (needs the batched file-based per-module emit — slice 2). **`asm_modload_run` now has the concat too** (#5699 / #5704), with both predicted hazards resolved and three more that only showed up once a real program went through it. What landed:

  * The two predicted hazards: `all_structs` is seeded with the identical `builtin_view(dir, all_structs)` call (not merely builtins-appended-last), and cross-unit shapes are handled by `dedupe_shape_defs` — keep the FIRST `.weak __fern_shp_*` definition and drop the rest, since the references then bind to exactly the address the linker's weak merge would have produced.
  * A module pruned to ZERO reachable functions read as ineligible and aborted the whole concat (`core/map` in an HTTP program). Empty units are skipped.
  * `all_runtime_need_roots` was NOT the closed set it claims to be: 27 roots were missing — every socket / readiness / filesystem / process / stdin / wide-int-stringifier helper — so library units linked against undefined `__fern_tcp_send` etc. Nothing had noticed because the only program ever built per-module is the compiler, which marks none of them.
  * **The rescue WAS gated, and no longer is.** Diverting *every* over-budget program swept in the self-host CHECKER driver (built by this same driver, and in the window), routing it through IR and straight into the two IR-path over-frees documented below. Both are now fixed, the gate (`needs_ir_only_builtin` + its builtin-name table) is DELETED, and every over-budget program in the window routes per-module IR. `TestSelfHostCheckerCodesX86_64` / `TestSelfHostCheckerDifferentialX86_64` are now the pin: they build the checker driver through this driver, so they compile the checker on the IR path and fail loudly if either over-free comes back.
  * **arm64 needs none of this.** The driver's arm64 branch returns `asm_arm64.emit_module` directly, with none of the budget arithmetic or concat rescue the x86 branch carries — which reads like an unconditional drop to the AST emitter, and is not. `emit_module` tries `all_eligible` first, and unlike x86 the arm64 merged IR path has **no 512-function budget**, so it already takes the over-budget programs the x86 path has to rescue. Measured on the flagship handler, on `http_parse_request`, and on a trivial program: all three route IR on both targets. arm64 emits the whole closure rather than the treeshaken subset (~2.5 MB vs ~430 KB), which is a size difference, not a routing one. Pinned by `TestSelfHostOverBudgetRoutesIRArm64` — it only needs the x86-built driver, since asserting the routing reads the emitted assembly.
  * A fn value handed to a SIBLING module's function was passed unboxed while the callee always dereferenced a box (#5698) — the caller's boxing decision looked the callee up in `mod.funcs`. `irlower.lift_lambdas_view` threads a whole-program signature view through that lookup; an empty view keeps every other caller byte-identical.

  Net: the flagship edge-handler program (`std/http` + `std/tcp` + a `handle` function, ~925 merged functions) is compiled by the self-hosted compiler and **serves real HTTP** (`TestSelfHostHttpHandlerServesX86_64`).

  Also note the entry unit of an arbitrary stdlib-using program is often not per-module-eligible, in which case the concat correctly declines; check with `-per-module-emit <last-unit-index>` before blaming the router. |
| `no-main` | 0 | Functions but no `main`. Supported by the desugar; not exercised today. | n/a |

### `ineligible-fn` is NOT closed — 17 subtests still decline (measured 2026-07-28)

The `ineligible-fn` row above says "No `ineligible-fn` items remain". That is
true of the **builtin methods** it enumerates, and false as a general statement:
running the corrected probe (inside the `use_ir` branch — see above) over
`internal/e2eselfhost` aborts **17 subtests across 7 test functions** (**15
across 6** after #5755 retired return-type inference, then **10 across 5** after
#5758 closed the tuple-fn struct field, then **8 across 4** after #5790 closed
the closure-array struct field — see the struck rows), none of
them over-budget (they are all small single-module programs), so every one is an
eligibility decline. `asm_ir_run`'s AST fallback is therefore **live code**, not
something #3457 can delete yet.

| test | subtests | shape |
|---|---|---|
| ~~`TestSelfHostReturnInferenceIR`~~ | ~~`option-some`, `option-none`~~ | **GONE (E070, #5755)** — the shape was an UN-ANNOTATED fn whose return type had to infer to `Option[i32]`. Requiring return annotations made those programs *invalid*, so the gate never sees them and the test was deleted. Note what this is NOT: the IR path was not taught anything: 17 declines → 15 by removing programs, not by widening the subset. |
| ~~`TestSelfHostTupleFnStructFieldX86_64`~~ | ~~`bare`, `arg-elem0`, `two-arg`, `read-then-call`, `churn`~~ | **CLOSED (#5758)** — fn-typed TUPLE element in a struct field, `s.p.1()`. Re-probed 2026-07-28: both the direct form and the via-a-local form (`var t = s.p; t.1()`) now report `module: IR`. |
| ~~`TestSelfHostCloArrayFieldCallIRX86_64`~~ | ~~`capture-multi`, `with-arg`~~ | **CLOSED (#5790)** — closure-ARRAY struct field (`hs: (() => i32)[]`), capturing call. This row read `STILL OPEN` after #5758 (correctly — the tuple-side `clo` admission does not reach an array-of-fn field), but #5790 closed it separately by proving the field positively (`CLOARR:<Type>.<field>`) rather than by elimination. See "The closure-ARRAY gap is a DISPATCH gap" below. The param-built / loop-built construction shapes (#5787) still decline, but for **soundness** — they are unprovable at the struct literal — not as a subset gap. |
| `TestSelfHostStructMatchX86_64` | `expr_form`, `rename_expr` | `return match (p) { … }` over a struct — the match-EXPRESSION form |
| `TestSelfHostTryOptionPayloadIRX86_64` | `result-option`, `nested-chain` | `?` over an Option payload, incl. nested chains |
| `TestSelfHostAtBindingX86_64` | `struct_expr_guard_uses_at`, `tuple_expr_guard_uses_at` | `@` binding read from inside a `when` guard |
| `TestSelfHostAsmIRPath` | `asc-none-opt-nested` | nested `None` in an ascribed Option position |

Note the last row in context: the other ~80 `TestSelfHostAsmIRPath` subtests
pass under the probe, which is the control proving the probe placement is right
(they are the deliberate `-ir`-off differential runs, not declines).

**Attribution caveat — the table names TESTS, not verified declining shapes.**
Re-probing the exact case sources with `asm_ir_run -ir-probe` (2026-07-28)
splits the list in two:

| shape | `-ir-probe` verdict | status |
|---|---|---|
| fn-typed tuple element in a struct field (`s.p.1()`) | was `main: BAIL lower` → `module: AST` | **CLOSED by #5758** |
| closure-array struct field (`r.hs[1]()`) | was `main: BAIL lower` | **CLOSED** — see below |
| struct match-expression + `when` guard | `module: IR` | **NOT the declining construct** |
| `w @ P { a, b } when w.a > 0` | `module: IR` | **NOT the declining construct** |

So only the two struct-field-of-callable shapes were verified gaps, and both
present the same way: the lifted closure lowers fine (`main$clo0: ir`) while the
struct-field form bails.

**They did NOT close together, and the reason is worth keeping.** #5758 closed
the tuple one by admitting a `clo` element in the wide (`_d`) tuple-field
predicate. Re-probing afterwards, the closure-ARRAY field still reports
`main: BAIL lower`: `hs: (() => i32)[]` is an array-of-fn, not a
tuple-containing-fn, so it never reaches the tuple-side admission. Same
predicate FAMILY, different type path. Anyone reading "one cause" in an earlier
revision of this section would reasonably assume #5758 covered both — it does
not.

For the other rows the case source is IR-eligible, so the `exit(97)` came from
something else those tests compile, not from the listed program. Do not treat
them as known gaps without re-deriving the abort; all five test functions PASS
normally, so whatever declines there is served correctly by the fallback.

The lesson repeats the one above: an abort tells you a test's process reached
the fallback, not WHICH program did. Confirm each shape with `-ir-probe` before
building on it.

### Composite-type IR gaps closed by differential probing (2026-07-29)

The strict-IR table above enumerates the whole-suite declines; a parallel line
of work found and closed a family of per-function IR-lowering gaps that the
suite never surfaced as declines, via **differential probing** (native `-interp`
exit vs the self-host-IR-compiled binary's — the methodology CLAUDE.md flags as
the one probe placement misses, since it catches WRONG lowering, not just WHERE
a program lowers). Each is pinned by a `TestSelfHostAsmIRPath` differential case
asserting equal exit codes on the AST and IR paths:

| PR | shape now lowering on the IR path |
|---|---|
| #5752 | `return match (p) { … }` — match-EXPRESSION over a struct / tuple scrutinee |
| #5758 | fn-typed TUPLE element read from a struct field (`s.p.1()`), direct and via a local |
| #5776 | N-element destructure of an array-of-tuples element |
| #5778 | recursive-struct field predicate (a self-referential struct crashed the compiler; now cycle-guarded with a `visiting` set + depth cap), plus tuple / nested-Option payload arms in `opt_payload_ok_dv` |
| #5779 | nested-`Option` type spelling (`Option[Option[…]]`) resolved by `some_opt_type` recursion |
| #5781 | bare struct / bare enum as a tuple element inside a struct field |
| #5784 | `Option`-of-tuple and nested-`Option` struct fields |
| #5792 | struct field of array-of-arrays (`P[][]`), double-index read |
| #5797 | enum variant with an `Option` / `Result` payload, matched |

After these, a 7-cycle probe sweep over advanced composite shapes (closures,
nested matches with guards, generics, `dyn`, `try`/`?`, tuples, iterators) came
up clean — every shape routed IR. **Probe-driven per-function slice-3 gap
discovery has reached diminishing returns**: the remaining per-function
declines are the two documented soundness declines (param/loop-built `fn[]`
struct fields, #5787 — see below), not subset gaps. The remaining #3457 frontier
is slice 2 (CI-affordability of the gen1 per-module fixpoint) and slice 5, not
further per-function widening.

**The closure-ARRAY gap is a DISPATCH gap, not an eligibility one** (probed
2026-07-28, after #5758). Do not reason about it by analogy with the tuple
shape — the two fail at different layers:

| variant | verdict |
|---|---|
| `R { hs: [() => n] }`, never read | `ir` |
| …`r.hs.len()` | `ir` |
| …`[seven]` (named fn), `.len()` | `ir` |
| the same call through a LOCAL clo-array | `ir` |
| **`r.hs[0]()`** | **BAIL** |

So the field TYPE is admitted fine; only the inline CALL through a
struct-field closure array bails. It is `irlower.fern`'s element-call dispatch
(the `parser.ExprIndex(cidx)` arm): its `parser.ExprFieldAccess(_)` case sets
`idx_is_arr` only when `field_access_is_fnarr` says the field is a registered
fn-POINTER array (#5235). A field holding env BOXES matches neither
`is_clo_arr` nor `idx_is_arr`, falls through to `_` → `s.fail()` → AST.

**CLOSED — and the "build a CLOARR registry" plan an earlier revision of this
section proposed was wrong.** `field_access_is_closurearr` already existed for
exactly this question, used by the bind / whole-array-alias read sites (#5160
defect #2): it declines a registered fn-POINTER field first, so the pointer arm
keeps precedence, then accepts on the `"fn[]"` field spelling. The fix was one
`else if` in the element-call dispatch, not a new registry.

The negation ("absent from the FNPTR registry ⇒ closure box") *is* relied on
here, and it is sound only because of the construction gap below. Pinned on
both sides — `TestSelfHostCloArrayFieldCallIRX86_64` (closure elements) and
`TestSelfHostFnptrArrayFieldIRX86_64` (`direct`, `rc-soundness`).

### Open, self-host-only: a LOCAL-BUILT fn-pointer array field miscompiles

Differential-probed 2026-07-28 (interp / native x86-64 / self-host):

| construction | interp | native x86-64 | self-host |
|---|---|---|---|
| `R { hs: [seven] }` — array LITERAL | 7 | 7 | 7 |
| `var a = [seven]; R { hs: a }` | 7 | 7 | **crash** |
| `var a = []; a = a.append(seven); R { hs: a }` | 7 | 7 | **crash** |

`fnptr_scan` only credits a field constructed from an all-fn-value array
LITERAL; any other store marks it `bad`, so a local-built pointer array is
absent from the FNPTR registry. `field_access_is_closurearr` then claims it by
negation and the call dispatches env-first on a raw code pointer.

This is the "already miscompiled at construction" gap that
`field_access_is_closurearr`'s header refers to — confirmed real, and NOT
stale as it first appeared: the LITERAL form works (it is registered), which is
what makes the header look wrong until you probe the non-literal one.

**FIXED** by widening `fnptr_scan`: it now tracks locals proven bound to an
all-fn-value array literal (`fnloc` on `FnptrAcc`) and credits a field stored
from one, so the registry covers the construction rather than the syntax. The
proof is retracted on ANY assignment to the name and reset per function, because
a stale credit is the unsound direction — it would call an env box as raw code.
Pinned by `local-built` and `rebind-retracts-proof` in
`TestSelfHostFnptrArrayFieldIRX86_64`, the latter being the case an unsound
implementation fails.

**The closure side is now proved the same way (#5790), which turned the retract
into a re-proof.** `field_access_is_closurearr` used to be
closure-by-elimination — "`fn[]` and not FNPTR" — so it claimed every field the
pointer scan could not prove. It now requires a `CLOARR:<Type>.<field>` marker
emitted from the same walk, and a field with neither marker is simply unproven.
That flipped `rebind-retracts-proof` (`var a = [seven]; a = [() => n];`) from
"claimed by elimination" to unproven, which dropped it to the AST emitter —
correct on x86-64, **0 instead of 5 on wasm**. So the assignment no longer only
retracts: on the function's own statement list it also re-proves the CLOSURE
side from the assigned value. Two limits are deliberate:

- **Only the function body's own statement list.** An assignment nested in an
  `if` / loop / match arm / lambda body may not run, so a credit from it would
  claim a store the program never made. Nested assignments still only retract.
  `if (n > 100) { a = [() => n]; }` therefore stays unproven.
- **Only the closure side.** The mirror rebind `var a = [() => n]; a = [seven];`
  is *not* credited as a pointer array, because a local's element representation
  is fixed by its DECLARATION rather than by what is later stored into it —
  see the next section. Crediting it routed that shape onto the IR path and it
  trapped (measured); it stays unproven.

### Rebinding an `fn[]` LOCAL: one direction fixed, the cross-representation ones open

Probed 2026-07-29 on the x86-64 IR path (no struct field involved; the wasm IR
path traps where x86-64 SIGSEGVs):

| program | interp | before | after |
|---|---|---|---|
| `var a = [seven]; a = [nine]; a[0]()` | 9 | **SIGSEGV** | 9 |
| `var a = [() => n]; a = [() => m]; a[0]()` | 6 | 6 | 6 |
| `var a = [() => n]; a = [seven]; a[0]()` | 7 | **SIGSEGV** | SIGSEGV |
| `var a = [seven]; a = [() => n]; a[0]()` | 5 | **SIGSEGV** | SIGSEGV |

Three of the four directions were broken, for two different reasons.

**Fixed: the pointer-to-pointer rebind.** A bare fn NAME is only lowered to a fn
POINTER (`const_func`) at the sites that know the slot's `is_fnarr` flag — the
declaration and `.append`. The whole-literal reassignment is the third
construction site for the same array and had no such handling, so its elements
fell through to the generic expression path, which const-CALLS a 0-arg fn name
and stores the RESULT. The buffer then held integers where code addresses belong
and the plain `call_indirect` jumped to 9. `lower_stmt_assign` now emits
`const_func` per element, mirroring the other two sites. The fix is
representation-PRESERVING — the slot is already `is_fnarr` and stays so — which
is why it is also correct with the rebind inside a branch or a loop body: the
lowering never has to decide whether the assignment dominates the reads.

**Still open: a rebind that CHANGES the representation.** The slot metadata
(`mark_closurearr` / `mark_fnarr`) is recorded where the local is DECLARED and
the read dispatches on it, while the assignment stores the new literal in its own
natural representation. So after a cross-representation rebind the slot says one
thing and the buffer holds the other.

Re-marking the slot at the assignment was implemented and **reverted**: it fixes
the straight-line rebind but is flow-INsensitive, and measured on a branch that
does not run it turns two currently-correct programs into SIGSEGVs —

| program | interp | with re-marking |
|---|---|---|
| `var a = [seven]; if (n > 100) { a = [() => n]; } a[0]()` | 7 | **SIGSEGV** (was 7) |
| `var a = [() => n]; if (n > 100) { a = [seven]; } a[0]()` | 5 | **SIGSEGV** (was 5) |

Those two work today only because the untaken branch's mis-lowered store never
runs. Closing this properly needs either dominance information at the lowering
site or a uniform `fn[]` representation — the same two options #5787 lists for
the struct-field side, which is not a coincidence: it is the same ambiguity one
level out.

This is also why #5790's assignment re-proof credits the closure side only. The
symmetric version was implemented and measured: with the pointer side re-proved,
`var a = [() => n]; a = [seven]; R { hs: a }` returns the correct 7 — but only
because the local re-marking was in the tree at the time. Without it the field
would be credited `FNPTR` over a buffer of boxes.

**The negation this justifies is still load-bearing, and the shapes it cannot
prove still MISCOMPILE.** An earlier revision of this paragraph claimed they
"route AST rather than crashing". Probed 2026-07-28 — that was wrong:

| construction | interp | self-host (IR) | self-host (closure arm disabled → AST) |
|---|---|---|---|
| `mk(a: (() => i32)[]) { R { hs: a } }` — field from a PARAM | 7 | **crash** | **crash** |
| `a = a.append(seven)` in a loop, then `R { hs: a }` | 7 | **crash** | **crash** |

So they are pre-existing on BOTH paths — #5783 did not worsen them, and falling
through to the AST emitter does not rescue them.

**Neither dispatch default is safe without evidence**, which is why this cannot
be fixed by picking a better fallback: a raw pointer called env-first crashes,
and an env box called as a plain pointer crashes too. A `CLOARR` registry does
not close it either — the param case has no construction visible at the struct
literal to credit. The sound options are (a) positive evidence on BOTH sides
with anything unproven becoming a clean compile ERROR rather than a silent
miscompile (the `wasm_unsupported_builtin` pattern), or (b) making the field's
representation uniform so the question disappears. Until then, an `fn[]` struct
field is only safe when its construction is provable — which after #5790 means
an array literal, a local bound to one, or (closure side only) a local rebound
to one by a top-level assignment.

**Root cause of the tuple gap (closed by #5758), and the shape of that fix.** A callable
behind a struct field has an ambiguous REPRESENTATION that its declared type
cannot resolve: `() => i32` is spelled the same whether the value is a raw code
pointer or a `__mkclo$` env box, and the two dispatch differently (env-first vs
plain `call_indirect`). For a LOCAL the ambiguity is settled by slot metadata —
`mark_closurearr` / `mark_fnarr` / the `"clo"` element tag are recorded where
the value is BUILT, which is why the local-tuple form lowers. A struct field has
no slot, so `expr_tuple_elem_tag`'s `p.t.N` arm falls back to
`decl_field_type` + `tuple_type_elem_tag` — the declared type — and cannot tell
which it is, so the call site bails.

(That prediction held for the tuple side — #5758 admitted the `clo` element and
closed it — but note it admitted `clo` in the WIDE `_d` predicate ONLY. The
narrow `is_leaksafe_tuple_field` also gates CONSTRUCTOR REUSE via
`struct_fields_reusable_param` / `_cross`, so admitting `clo` there too would
widen reuse to structs carrying a closure box without the freshness gates the
adjacent fn-FIELD case requires — unsound, and not caught by any test. Keep the
two predicates asymmetric.)

The machinery for exactly this already exists one level out:
`irlower.fnptr_arr_fields_of` scans every function body for how each field is
POPULATED and emits a module-level `"FNPTR:<Type>.<field>"` registry, which
`field_access_is_fnarr` then reads at the use site (`#5235`). The fix is the
same construction one level deeper — a registry keyed by field *and tuple
element index*, populated from the struct-literal site (a `__mkclo$` value ⇒
closure box, a bare named fn ⇒ pointer), read by `expr_tuple_elem_tag`'s
struct-field arm and by the closure-array-field call path. Note both confirmed
shapes share this one cause, so one registry closes both.

### A whole output mode has no IR leg (not a decline reason — a missing path)

The table above enumerates reasons a program *declines* the IR path. That frame
misses a second class entirely: an output mode with **no IR path to decline**.
Mapping every remaining `asm.emit_module` / `asm_arm64.emit_module` /
`wasm.emit_module` call site (2026-07-28) gives:

| target | IR leg? | AST reached when |
|---|---|---|
| x86-64 | yes | `asm_ir.emit_module_ir_gated` declines (budget / ineligible fn) |
| arm64 ELF | yes, and it carries **no 512-function budget** | `all_eligible` false |
| arm64-darwin | yes — `emit_module_ir(lm, darwin)` threads `darwin` into `emit_runtime` | `all_eligible` false |
| wasm core | yes | `wasm_ir.should_use_ir_core` false (the `wasm_ir_deferrals_ok` set) |
| wasm component | yes, for the no-I/O + stdout/stderr/exit shapes | `component_needs_ok` false (any other WASI category) |

So the raw call-site count badly overstates the work: `asm.emit_module` and
`asm_arm64.emit_module` are both IR-*preferring shells* (each runs the gate
first and only falls through), so most of those sites are already IR routes.

**The last row was `none` until 2026-07-28** — `wasm.emit_module_mode` gated the
IR leg on `ir_ok = !component && …`, so every `emit-target-wasm-component*` case
was AST-only *unconditionally*, an output mode with no IR path to decline rather
than a decline reason to close. It now has one, for the subset the fixed
component framings can serve:

| shape | mode | core imports | framing |
|---|---|---|---|
| no I/O | 1 | none | `component_full` |
| stdout | 2 | get-stdout, blocking-write-and-flush | `component_full_io` |
| stderr | 2 | get-stderr, blocking-write-and-flush, get-stdout | `component_full_io_eprint` |
| exit | 2 | get-stdout, blocking-write-and-flush, wasi:cli/exit | `component_full_io_exit` |

The leg is a fork at two points inside `wasm_ir.emit_ir_module_units_mode`, not
a second emitter: the import section, and the entry (`_start` → `main` +
`_lang_run`). Everything between — memory, literals, type-id globals, heap + RC
runtime, every helper body, the funcref table — is mode-independent. What makes
that possible is that the preview1 surface the IR already emits is shimmable:
mode 2 *defines* `$fd_write` over `blocking-write-and-flush` and `$proc_exit`
over `wasi:cli/exit`, so every emitted call site links unchanged. The mode-2
`$fd_write` dispatches on its `fd` argument (2 → stderr, else stdout), which is
why eprint and print share one shim where the AST path needs a separate
`$__fern_eprint` body.

What remains AST-only is the rest of `component_shape`: fs / env / args /
random / clock. Those are not gated by the framing but by the *helpers* —
`readfile_func_p2`, `env_func_p2`, `args_func_p2`, `clock_funcs_p2`,
`random_func_p2` are preview2 rewrites living only in `wasm.fern`, with no IR
sibling. Each is an independent, well-scoped port against a working reference;
`component_needs_ok` is an allowlist over need names and fails closed, so a
shape stays on the AST path until its helper is ported and the name is added.

Two corrections to the framing that was here before:

- **The 512-budget is not only a whole-compiler problem.** Any program importing
  a decent slice of stdlib crosses it, so it is reached by ordinary test
  programs, not just the bootstrap. Lifting it still needs the memory work.
- **"Every compiler function is IR-eligible" (the 2026-07-26 issue comment) is
  true of the COMPILER'S OWN source and does not generalise.** The programs the
  test suite compiles hit a per-function gap that the compiler's sources happen
  not to use: ~20 builtin methods whose Fern helper *bodies* already exist and
  are shared (`asmcore.fern` — `__fern_arr_i32_sum`, `__fern_i32_pow`, …) and
  whose runtime *needs* are already listed in `asm_ir.all_runtime_need_roots`.
  Only the LOWERING is missing: `irlower.fern` contains no occurrence of
  `"sum"` / `"pow"` / `"is_empty"` / `"first_byte"`, while `asm.fern` intercepts
  each of them. So this is a bounded port against an existing reference, not new
  design — but it is the work that actually gates deleting `asm.fern`.

## The checker miscompile: a container-read over-free (found + fixed 2026-07-28)

#5680 and #5687 concluded that treeshaking `asm_modload_run`'s merged module
corrupts the compiled output. **That was a misattribution**, and it cost two
attempts. Measured:

| build of the checker | module funcs | `.Lir_*` markers | emitted via | result |
|---|---:|---:|---|---|
| no treeshake | 746 | 0 | AST emitter | correct |
| treeshaked | 497 | 12766 | IR path | mis-diagnoses |

The treeshake removes ~265 dead functions, which drops the checker **under the
512-function IR budget** — so it stops falling back to the AST emitter and routes
IR. The mis-diagnoses followed the PATH, not the pruning; every intermediate point
agreed (`funcs=507/515/523`, all IR, all wrong). Confirmed with **zero** functions
removed: changing both `> 512` gates in `asm_ir.fern` to `> 9999` and compiling
the full 763-function checker reproduced the same two failures —

- **spurious `E001`** on `enum E { A, B } function f(): (E, i32) { return (A, 1); }`
  (a bare variant in a tuple return), and
- **missing `E030`** on a match whose only guarded arm is `Red when 1 == 2`.

**Root cause: a missing Perceus retain on a CONTAINER READ.** An array-typed
`var` binding whose init reads a buffer out of a container it does not own was
marked `is_arr` (folded in from the *declared* type) but took no alias-inc, while
`emit_dec_sweep_except_list` decs **every** `is_arr` slot at function exit. So
`var vn: string[] = mod.enums[en].variant_names;` — in `check_module`'s E017 walk
and in `ambiguous_variants` — freed the enum table's variant-name buffer on the
first call; the next allocation recycled it, after which unit-variant lookups read
garbage. That is exactly the pair of symptoms above: a variant that no longer
resolves (`E001`) and a coverage set that no longer matches (`E030`).

The scalar-element and struct/enum-element field reads had carried this retain
since the RC-frontier slices (the `scalar_arr_field_type` /
`struct_arr_field_read_type` early paths in `lower_stmt_var`). Three shapes fell
through to the generic path, which had none: a **`string[]` field**, a **tuple
element** (`var xs: i32[] = t.0;`), and an **array-of-array element**
(`var row: i32[] = g[i];`). `lower_stmt_assign` had the matching hole on the
reassign side — its field-read arm was gated on the same three type predicates.

Both are now closed by a generic container-read retain (`ExprFieldAccess` /
`ExprIndex` init on an `is_arr` slot) in `lower_stmt_var` and
`lower_stmt_assign`, pinned by `TestSelfHostContainerReadAliasIRX86_64` — which
exits 99 (rc underflow) without it — and by
`TestSelfHostCheckerDifferentialX86_64` run with both budget gates lifted, which
goes from 2 divergences to 0. Do NOT spend time hardening the treeshake or the
concat against this; it was never their bug.

### The second over-free: a param-receiver `.append` reclaiming the CALLER's buffer

Widening the `asm_modload_run` rescue gate (so the checker itself routes IR)
left exactly **two** failures once the container-read retain landed — measured
across `TestSelfHostCheckerCodesX86_64` + `TestSelfHostCheckerDifferentialX86_64`
with the gate widened: **36 sub-test failures before the retain, 2 after.**

Both printed a mangled diagnostic CODE — `" E06"` for `E063`, `")E06"` for
`E065`: length 4 (correct), data pointer one byte LOW, message field intact.
One byte low lands on the last byte of the preceding `.rodata` literal, so the
16-byte `__fern_str_box` cell for the code had been freed and re-handed out.

Root cause: `xs = xs.append(v)` where `xs` is a **PARAM** took the sole-owner
reclaiming push (`arr_push_owned`), which frees the pre-grow buffer on a
grow-realloc. That buffer belongs to the CALLER — which sweeps it at its own
exit — so the callee freed it, the caller dec'd the freed block, and the
recycled block came back from the next `__fern_str_box`. The existing gate,
`is_aliased_name`, only sees aliases created INSIDE the function, so it cannot
see the caller's reference; the fix adds an explicit `slot < n_params`.

`slice_escape_diags` and `e065_diags` are the two call sites: each builds a
fresh `string[]` local and hands it to a walker that appends to it
(`localarr = localarr.append(v.name)`). Only programs that made the walker take
the append branch were affected, which is why the sibling corpus cases stayed
green.

The leak this trades for is the one the aliased branch already accepts: arrays
grow geometrically, so a param-receiver append leaks O(log n) pre-grow buffers
per call.

**A synthetic regression test CAN reproduce the param-append symptom, and now
does** (`TestSelfHostParamAppendReclaimIRX86_64` / `…Wasm`) — correcting the
earlier finding that only the checker corpus could pin it. Modelling the
aliasing is genuinely not enough, which is what the first attempts hit: freeing
the caller's buffer is harmless on its own (no rc underflow — the caller's count
really does reach zero, and nothing has been handed the recycled block yet). The
missing ingredient is that the callee must ALLOCATE AGAIN after the append and
that allocation must be the value it RETURNS. Then the returned object sits in
the block the param append released, and the caller's exit-sweep dec of its own
stale pointer frees the value it was just handed — the `Diag[]` that `slc_walk`
/ `e065_stmts` build while appending to `localarr` / `sbacked`. With that
ingredient a standalone ~25-line program exits 3 instead of 4 without the fix,
on both x86-64 and wasm.

The checker corpus stays the broad pin (it covers the container-read over-free
too, which has no synthetic case yet). The direct test is worth having beside it:
it fails in seconds on a standalone program rather than via a multi-minute
checker build reporting garbled diagnostic codes, and it covers wasm, which the
x86-only corpus does not. The aliasing-only shape is kept as a third case that
passes even unfixed, so the distinction stays documented in the test itself.

**Method note for the next bug of this shape.** Stubbing `emit_dec_sweep_except`
/ `_except_list` to a no-op is the fastest bisector: every divergence went green,
which localised the fault to RC accounting in one step and turned the search from
"which of 763 functions miscompiles" into "which slot is decced without a
matching inc". Path-probing (`-decide` / `-ir-probe`) cannot find this class —
it reports WHERE a program lowers, not whether the lowered code is right.

The second over-free was found by continuing the same bisection past the sweep
into its individual loops (array / struct / string / map), then down to the
single `__fern_rc_dec` branch, and finally to the FUNCTION: gate the dec on
`st.cur_fn.len() % N` and halve N each round until the candidate set is small
enough to `eprint(st.cur_fn)`. Four two-minute rounds narrowed 763 functions to
~40 names, and the two culprits were obvious by inspection from there. Note
that `cur_fn` is the MANGLED name (`checker__slice_escape_diags`), which is
what the length arithmetic sees.

**The helper lowerings are shared, but the helper BODIES are not — a debt these
slices accrued (found + paid 2026-07-27).** `irlower.fern` feeds all three
backends, so a `call_direct` to a Fern runtime helper is emitted on x86, arm64
AND wasm; only the x86 side (`asm_ir.fern`) was wired to emit the body. Measured
on the arm64 IR path: an `xs.sum()` program emitted `bl __fn___fern_arr_i32_sum`
with **no definition** — an undefined reference at link, latent since #5664
because no arm64/wasm IR test uses these builtins. Both backends are now handled:
arm64 marks the needs in `asm_arm64_ir.fern`'s `is_fern_helper` branch (the
bodies already existed in `asm_arm64.emit_runtime`, gated and unmarked), and wasm
— which had no body for any of them and would fail with `unknown func` at load —
initially **deferred** such a module to its AST path, which is where it went
before the lowerings existed (since replaced by real WAT bodies — see below). Any future helper-backed
builtin must do the same three-backend triage, not just the x86 five-part recipe.

**The wasm deferral is now retired (2026-07-27): wasm has real bodies.** The
deferral was a stopgap, and a poor one — it routed those modules to a path that
does not implement the builtins EITHER (the wasm AST emitter emits
`i32.const 0` for `xs.sum()`), so both paths were silently wrong. `wasm_ir`
now carries hand-written WAT for all six (`arr_i32_helpers`, gated on
`@uses_arr_i32_helpers`), and they compute the same answers the register
backends do — pinned by IR-ONLY cases in `TestSelfHostWasmIRPath`, since an
AST==IR differential could only be satisfied by the IR path being wrong the
same way. Two notes for anyone adding to that set: the WAT lives in
`wasm_ir.fern`, not `wasm.fern`, so it does not grow the file #3457 deletes;
and the two-argument helpers take their parameters **reversed** relative to
the register signature — `index_of(target, xs)`, `pow(exp, base)` — because
irlower pushes the argument first (the register callees bind params in reverse
push order, #5666) while a wasm `call` binds first-pushed to param 0. Symmetric
test data hides that; the cases are asymmetric on purpose. The wasm AST path is
left as it was — silently wrong — since #3457 deletes it; the IR path is the one
that has to be right.

Also worth recording, because it bears on whether script support should survive
at all: **the native compiler rejects script-shaped source** (`fern -interp` on
`return 42;` gives `P001: expected "function"`). Top-level statements parse only
in the self-host parser, so this is an existing native/self-host divergence
(#4451). The desugar preserves the self-host behaviour rather than regressing it
while the emitter is retired; whether to instead retire the script shape and
converge on the native surface is a separate roadmap call, deliberately not made
here.

## The AST-emitter call graph (what must go, and what blocks each)

The three legacy emitters are still reached through these entry points
(`asm.emit_module` / `asm_arm64.emit_module` / `wasm.emit_module`):

| Site | Path | Reachable today because… |
|---|---|---|
| `asm_run.fern:23` | merged AST (x86) | `TestSelfHostBootstrapsItself` / `TestSelfHostAsmRunX86_64` pipe programs through it |
| `asm_load_run.fern:376` (arm64 373) | merged AST | `TestSelfHostStage2FixedPoint{,Arm64}` fixpoint on this driver |
| `asm_modload_run.fern:335` (arm64 332) | merged AST default | ~~`TestSelfHostModloadFixpointX86_64`~~ **retired from routine CI (env-gated `RUN_MERGED_FIXPOINT`, #3457 slice 2)** — the x86 whole-compiler self-compile gate now runs `TestSelfHostModloadPerModuleWholeCompilerX86_64` (per-module IR). No routine test (x86 OR arm64) exercises the merged default: `TestSelfHostFixpointArm64` is env-gated the same way, with `TestSelfHostModloadPerModuleWholeCompilerArm64` the routine arm64 gate |
| `asm_ir_run.fern:158` (arm64 135/144) | AST *fallback* of the IR differential | reached when `emit_module_ir_gated` returns "" (an ineligible program) |
| inline `main.fern` | AST | `TestSelfHostStage2Bootstrap` / `…Stage2Compiler` build a one-off compiler over `asm.emit_module` |

Plus the IR path's own coupling to the AST files (the untangle target, slice 4):

- **x86: already clean.** `asm_ir.fern` is self-contained — its own
  `emit_ir_runtime` (asm_ir.fern:831–2897, ~2067 lines of hand-written bodies)
  and `emit_ir_runtime_fern_fn` (5327, IR-compiles the Fern runtime helpers via
  `emit_function_via_ir`). `asm.fern` imports `asm_ir`, not the reverse.
- **arm64: partly untangled.** `emit_module_ir_unit_arm64` lives in
  **`asm_arm64.fern`** (3968) and, on the entry unit, calls
  **`asm_arm64.emit_runtime`** (~4870 lines). That `emit_runtime` compiles the
  Fern runtime helpers with the **AST `emit_function`** (via
  `emit_runtime_fern_fn`, 32 call sites) — so the arm64 IR path still
  transitively depends on the AST emitter through that ONE `emit_runtime` call.
  **First untangle step DONE (#3457 slice 4):** the IR-path-only reclaim/drop
  bodies (`emit_arm64_reclaim_drop_bodies` + `_field_reclaim_one` /
  `_struct_drop_one` / `_struct_arr_elems_drop_one`, ~350 lines) moved verbatim
  from `asm_arm64.fern` to `asm_arm64_ir.fern` (byte-identical arm64 emit
  verified), so the remaining coupling of `emit_module_ir_unit_arm64` to the AST
  file is now just the single `emit_runtime` call. (`asm_arm64_ir.fern` — the
  arm64 IR *instruction selector*, `emit_function_via_ir` — is free of
  `asm_arm64`; the runtime is the remaining gap.) **Next:** give the arm64 IR
  path its own `emit_ir_runtime_fern_fn` using `emit_function_via_ir` (a
  near-verbatim port of x86 `asm_ir.fern:5609`) — a **behaviour-preserving, NOT
  byte-preserving** change (AST `emit_function` and IR `emit_function_via_ir`
  emit different bytes for the same helper), so it needs its own differential
  validation, not the byte-identity gate.
- **wasm: NOT clean, and larger — but hand-WAT, so movable byte-preservingly.**
  `wasm_ir.fern` reuses `wasm.fern`'s WAT runtime extensively (heap/RC,
  `str_*_helpers`, `to_string_helpers`, `divrem_helpers`), and the per-module IR
  framing entry points `wasm.emit_ir_module_units` / `wasm.emit_ir_rc_bodies_from`
  (`wasm_modload_run.fern:336-337`) **live in `wasm.fern`**, not `wasm_ir.fern`.
  Unlike arm64, the wasm runtime is hand-written WAT (no `emit_function`), so
  each IR-path-only helper block moves to `wasm_ir.fern` byte-preservingly.
  **Steps DONE (each byte-identical, verified locally + CI wasmtime):** (1) the
  transcendental f64-math block (`exp_func` / `log_func` / `pow_func` /
  `trig_reduce_and_polys` / `sin_func` / `cos_func`); (2) the 9 IR-only wasi
  `*_import` declarations (`random_get_import` / `stat_import` /
  `remove_file_import` / `readdir_fd_import` / `remove_dir_import` /
  `temp_dir_import` / `sleep_ms_import` / `stdin_fd_read_import` /
  `reader_fd_close_import`) — pure `(import …)` WAT strings, one gated call each in
  `emit_ir_module_units`. Both moved verbatim to `wasm_ir.fern`. (3) the
  randomness / byte-view `*_func` helpers (`random_bytes_ir_func` /
  `random_i32_func` / `str_bytes_func`) — self-contained WAT (only `call
  $random_get` / `$__fern_str_box` / `$__fern_arr_box` at the WAT level, no
  cross-helper Fern calls), one gated call each in `emit_ir_module_units`. Moved
  verbatim; byte-identical WAT for `random_bytes` / `random_i32` / `as_bytes`
  programs, wasmtime exits 8/9/5. (4) the filesystem / map-iter `*_func` /
  `*_helpers` group (`stat_func` / `remove_file_func` / `read_dir_func` /
  `remove_dir_func` / `temp_dir_func` / `sleep_ms_func` / `map_iter_helpers`) —
  each a pure `o = o + "…"` WAT string-builder with no Fern-level cross-helper
  call (the IoError box is emitted separately via the already-moved
  `wasm_ir.build_io_error_func`; everything else is a WAT-level `call $path_*` /
  `call $fd_readdir` / `call $poll_oneoff`), one IR-only call site each in
  `emit_ir_module_units`. Moved verbatim; byte-identical WAT for an FS-builtins
  program (stat/read_dir/remove_file/remove_dir_all/temp_dir/sleep_ms) and a
  `for (k,v) in m` map-iter program, both running under wasmtime. (5) the
  string-transform cluster (`substr_helper` / `strcmp_helper` / `str_trim_helper` /
  `str_reverse_helper` / `str_replace_helper` / `str_repeat_helper` /
  `str_predicate_helpers` / `str_lines_helper` / `str_chars_helper` /
  `str_case_helpers`) — 10 pure IR-only string-builder leaves, each with a single
  IR-framing call site and no Fern-level cross-call. Verified by a dependency-graph
  scan of the ~42 remaining IR-append helpers: all are leaves EXCEPT `str_split_helper`
  (→ `arr_push_helper`) and `arr_str_join_helper` (→ `str_join_helper`), whose shared
  callees stay in wasm.fern — those two are DEFERRED to a follow-up that moves their
  callees too. `string_from_bytes_helper` is also deferred: it is dual-use (also called
  from the AST-path `emit_module_mode`). Byte-identical WAT for a program exercising
  slice / cmp / trim / reverse / replace / repeat / starts_with / lines / chars /
  to_ascii_upper+lower, running under wasmtime. (6) the deferred entangled quartet:
  `str_split_helper` + its callee `arr_push_helper`, and `arr_str_join_helper` + its
  callee `str_join_helper` — moved TOGETHER so the internal `str_split → arr_push` /
  `arr_str_join → str_join` calls stay same-module (bare) in `wasm_ir.fern`, and the
  external callers that remain in `wasm.fern` (`strcat_helpers` → `str_join_helper`,
  and the `emit_ir_module_units` gates) are prefixed `wasm_ir.`. Byte-identical WAT
  for a `.split()` / array `.join()` / `.lines()` program (emitting `$__fern_str_split`
  / `$__fern_arr_push` / `$__fern_str_join` / `$__fern_arr_str_join` / `$__fern_str_lines`),
  running under wasmtime. (7) the stdio / slice / strbuf leaf cluster (`chr_helper` /
  `print_str_helper` / `eprint_str_helper` / `strbuf_helpers` / `arr_slice_helper`) —
  5 single-IR-call-site leaves with no Fern-level cross-call. Byte-identical WAT for a
  `chr()` / `write()` / `eprint()` / `strbuf_reset`+`append`+`take` / array-slice program
  (emitting `$__fern_chr` / `$__fern_print_str` / `$__fern_eprint_str` /
  `$__fern_strbuf_*` / `$__fern_arr_slice`), running under wasmtime. (8) the compute/format
  leaf cluster (`to_string_helpers` / `strcat_streq_helpers` / `divrem_helpers` /
  `string_from_bytes_helper`) — 4 leaves, most MULTI-SITE (called from both
  `emit_ir_module_units` and the AST-path `emit_module_mode` / `strcat_helpers`), so EVERY
  bare call site in `wasm.fern` is prefixed `wasm_ir.` (verified: zero bare refs remain,
  no double-prefix). This validates the multi-site prefix approach the remaining leaves need.
  Byte-identical WAT for an int-div/rem + f-string + string `+`/`==` + `string_from_bytes`
  program (emitting `$__fern_idiv` / `$__fern_irem` / `$__fern_strcat` / `$__fern_streq` /
  `$__fern_string_from_bytes`), running under wasmtime. (9) the process-environment leaf
  cluster (`args_func` / `arg_at_func` / `args_helpers` / `env_func` / `env_helpers` /
  `clock_funcs`) — 6 leaves, all MULTI-SITE (IR framing + AST `emit_module_mode`). The
  AST-path callers use an `if (io) { …_p2() } else { …() }` split for the component-model
  variant, so the prefix targets ONLY the base `args_func()` / `env_func()` / `clock_funcs()`
  calls, leaving the `_p2` / `_imports_p2` siblings (which stay in `wasm.fern`) untouched.
  Byte-identical WAT for an `args()` / `arg_at()` / `env()` / `monotonic_ns()` / `now_unix_ms()`
  program (emitting `$__fern_args` / `$__fern_arg_at` / `$__fern_env` / `$__fern_monotonic_ns`
  / `$__fern_now_ns` / `$__fern_now_unix_ms`), running under wasmtime. (10) the file-I/O +
  stdin/reader leaf cluster (`file_imports` / `readfile_func` / `writefile_func` /
  `open_file_func` / `writer_write_func` / `read_line_func` / `read_int_func` /
  `read_all_stdin_func` / `reader_read_chunk_func` / `reader_close_func`) — 10 leaves;
  `readfile_func` / `writefile_func` use the same `if (fs) { …_p2() } else { …() }` split as
  args/env (base-only prefix, `_p2` siblings untouched). Byte-identity had to be checked with
  SEPARATE per-op probes: coexisting stdin/file needs suppress each other's helper emission in
  one program, so `read_file`+`write_file` (→ `$__fern_read_file`/`write_file` + the
  path_open/fd_read/fd_write/fd_close imports), `open_writer`+`w.write`+`open_reader`+`read_chunk`+`close`
  (→ `$__fern_open_file`/`writer_write`/`reader_read_chunk`/`reader_close`), and bare `read_line()`
  / `read_int()` / `read_all_stdin()` were each emitted alone and diffed — all byte-identical.
  (11) the last self-contained leaves (`map_helpers` — the full `$__fern_map_*` runtime;
  `optarrarr_free_func` — the `$__fern_optarrarr_free` #4365 reclaim; `cabi_realloc_helper` —
  `$cabi_realloc`). `optarrarr_free_func` took an `heap_base: i32` arg (call site's arg
  preserved); `cabi_realloc_helper` was PRIVATE (`function`) and is now `pub`. `map_helpers` +
  `cabi_realloc_helper` byte-verified by direct probe (a `Map[i32,i32]` for-loop and a
  `tcp_connect`+`tcp_send`+`tcp_close` program); `optarrarr_free_func`'s reclaim path isn't
  triggered by a plain program, so it is covered by the dedicated
  `TestSelfHostOptAarrReclaimWasmIR` (builds a driver from these sources, runs the
  `Option[i32[][]]`-churn reclaim through wasmtime — GREEN) plus a byte-identical WAT for an
  `Option[i32[][]]` program. **With this, every self-contained IR-only WAT leaf that
  `emit_ir_module_units` gated via a simple `out = out + X()` had moved out of `wasm.fern`.**
  (12) the framing's direct helper dependencies: probing `emit_ir_module_units` for
  wasm.fern-resident calls surfaced 14 MORE runtime-helper emitters it invokes directly
  (not just the gated ones); 10 are pure leaves and moved here — `print_int_helper` /
  `print_int64_helper` / `heap_alloc_helpers` / `rc_runtime_helpers` / `divrem64_helpers` /
  `str_to_i32_helper` / `arr_push_owned_helper` / `arr_slice8_helper` / `map_w64_helpers` /
  `arr_dec_ptr2_func` (`print_int64_helper` was private → now `pub`). This batch also fixed a
  latent bug in the relocation methodology: the by-name mover's header regex was `[a-z_]+`
  (no digits), silently skipping digit-named functions (`divrem64` / `print_int64` /
  `arr_slice8` / `map_w64` / `str_to_i32` / `arr_dec_ptr2`) — now `[a-z_0-9]+`. Byte-identical
  WAT confirmed by direct probe for 6 (`$__fern_alloc` / `$__fern_rc_*` / `$__fern_arr_slice8` /
  `$__fern_print_int64` / `$__fern_idiv64`+`irem64` / `$__fern_print_int`); the other 4
  (`str_to_i32` / `arr_push_owned` / `map_w64` / `arr_dec_ptr2`) need parse/reclaim-specific
  triggers and are covered by the verbatim-move guarantee (same cut-paste operation, build +
  type-check + zero-bare-refs all green). (13) the 4 non-leaf framing deps + their 2 shared
  callees, moved TOGETHER so the internal calls stay same-module: `arr_push_f64_helper` /
  `arr_push_i64_helper` + their callee `arr_push_wide` (was private → `pub`), and `le32_escape` /
  `wat_escape` + their callee `hex_digit` (was private → `pub`). The escapers turned out to have
  only ~2 call sites each (not the pervasive use feared). Byte-identical WAT for a probe with a
  special-char string literal (exercises `wat_escape`/`le32_escape`/`hex_digit` — hex `\XX`
  escapes in the data section, runs correctly under wasmtime) and a `.append()` on `i64[]`/`f64[]`
  (emits `$__fern_arr_push_i64`/`$__fern_arr_push_f64`, which call the moved `arr_push_wide`).
  **With this, `emit_ir_module_units` has NO remaining wasm.fern-resident helper dependencies** —
  every runtime-helper emitter it invokes (gated or direct) now lives in `wasm_ir.fern`.
  (14) **the framing function `emit_ir_module_units` ITSELF is now relocated to `wasm_ir.fern`.**
  Re-probing confirmed it had reached ZERO wasm.fern-resident calls and referenced NO wasm.fern-local
  type (`Ctx` / `StrTable` / `WState`) — the `_p2` component-model variants live in `emit_module_mode`
  (the AST path), NOT in this IR framing, so nothing extra had to move with it. The relocation had one
  extra step beyond the leaf moves: the 336 `wasm_ir.`-prefixed calls INSIDE the moved body (to the
  helpers relocated in slices 1–13) had to be **un-prefixed** to bare same-module calls, since
  `wasm_ir` cannot name itself. Its three callers were updated: `emit_ir_module` (stays in `wasm.fern`)
  and the drivers `wasm_modload_run` / `wasm_units_probe` (their `wasm.emit_ir_module_units` →
  `wasm_ir.emit_ir_module_units`; all three already imported `wasm_ir`). Byte-identical WAT for three
  diverse programs (i64-array + escaped-string; map for-loop + print_int; random + str_trim), since
  `wasm_ir.foo()` and `foo()` compile to the same call. **The wasm IR path no longer depends on
  `wasm.fern` for module emission at all** — `emit_ir_module` (the thin entry that calls
  `wasm_ir.emit_ir_module_units`) is the only IR-path resident left in `wasm.fern`, and it can move
  trivially once the AST path retires. `emit_ir_rc_bodies_from`
  is LAST — it drags `Ctx` / `release_module_ctx` / `collect_method_types` and the
  shared struct predicates (co-owned by the AST emitter → a cycle until that
  shared layer is relocated).

## The gate: #3425 (self-host runtime memory) — CLOSED

The merged path is fast (one emit) but needs the AST emitter + 512-budget. The
per-module IR path is the replacement. Its **self-host-built (gen1) emit** was
arena-limited — the self-host runtime did not reclaim the whole-program
string/analysis allocations during a large-module emit (#3425). **That is now
fixed** (the large-tier freelist port, below), and the direct proof
(`TestSelfHostPerModuleFixpointX86_64`, still env-gated `RUN_PERMODULE_FIXPOINT=1`
for its ~16.6-min serial runtime) is GREEN: gen1 emits all 35 units with no
arena OOM, gen0 == gen1 byte-identically. **The arena wall is gone; the only
residual slice-2 obstacle is CI *time*, not memory.**

### #3425 was a bounded, reference-guided port (prediction confirmed)

The root cause was concrete: the **self-host** RC runtime (`asm_ir.fern`'s
`emit_ir_runtime`, mirrored in `asm.fern` / `asm_arm64.fern` / `wasm.fern`) used
a size-classed freelist of **65536 exact word-classes** (`__fern_freelist`, up to
~512 KB blocks). Anything larger had no class and **leaked into the bump arena**,
so a long-running emit that freed big blocks (per-function analysis temps,
strbuf-growth cast-offs) accumulated them until `__fern_alloc`'s bounds check
`exit(137)`d.

The **native** runtime already solved this with a two-tier segregated freelist
(a large tier atop the small classes). The self-host runtime lacked it — which
**is** the "gen0 fits, gen1 OOMs" asymmetry: gen0 (self-host source compiled by
the Go backend) runs the **native** runtime → has the large tier; gen1+
(compiled by a self-host-built compiler) run the **self-host** runtime → no large
tier → leak.

**The port landed (2026-07-26):**
- **x86 (`asm_ir.fern`, #5609):** a `__fern_large_freelist` array + a
  `.Lalloc_large` path in `__fern_alloc` and a `__fern_large_push` free helper,
  redirecting `__fern_arr_dec` and `__fern_str_free` off the leak. LINEAR
  512-KiB binning (`class = round_up(size, 512 KiB) >> 19`, 1..2048 for
  512 KiB..1 GiB) — deliberately no `bsr` / variable-count shift, because the
  self-host `x86_gas` assembler (exercised by `TestSelfHostX86Capstone`) has
  neither; the linear scheme uses only `leaq`/`andq $imm`/`shrq $imm`.
- **arm64 (`asm_arm64.fern`, #5614):** the same design in aarch64 (mask via
  `lsr`/`lsl`, wide immediates built from `1<<19`, tail-`b` to preserve x30).
- **Both re-baselined their fixpoints byte-identically and stay
  self-reproducing** — confirmed by the modload fixpoints (CI) and the gen1
  per-module fixpoint (env-gated, GREEN).
- **wasm deferred** (task #18): wasm uses `memory.grow` (growable linear
  memory), so a leaked large block grows RSS but never hits a fixed arena wall
  → no exit-137, no slice-blocking. It is an RSS optimisation only, and adding a
  large tier there shifts `heap_base` across the byte-identity surface for no
  correctness gain; low priority.
- **Remaining self-host free sites — x86 + arm64 DONE (2026-07-27).**
  The self-host runtime's `__fn___fern_str_arr_free` (`.Lsaf`) /
  `__fern_arrarr_free` (`.Laaf`) / `__fern_strarrarr_free` (`.Lssaf`) /
  `__fern_optarrarr_free` (`.Loaf`) / `__fern_snapshot_dec` (`.Lsd`) /
  `__fern_arr_push_owned` (`.Lapo`) *outer-buffer* frees previously
  `leak (sound)`ed a ≥512 KiB collection buffer; each now recycles it via
  `__fern_large_push` (the same redirect `arr_dec` / `str_free` already use). This
  bounds RSS for the general-purpose large-collection programs Fern now targets
  (the free sites fire on Perceus-proven-fresh, non-escaping locals). x86 in
  `asm_ir.fern` (#5651), arm64 in `asm_arm64.fern` (the #5609→#5614-style mirror:
  `.Lsaf`/`.Laaf`/`.Lssaf`/`.Loaf`/`.Lapo` `bl`+`b` through the frame that already
  saves x30; `.Lsd` a terminal tail-`b` since `__fern_large_push` preserves x0).
  - **Still leaking on BOTH backends** (parity, separate follow-ups): the
    `__fern_alloc_reuse` oversize-donor discard (`.Lsarelo`), and arm64-only, the
    `__fern_str_free` DATA buffer's ≥512 KiB path (`.Lstrfree`, a #5614 gap x86
    already closed). Neither is a collection *outer* buffer, so both are out of
    scope for the #5651 mirror.
  - **Measured: this is soundness-completeness, NOT an arena-wall win — the doc's
    original assessment was right.** A single-process `-per-module-emit-all` gen1
    emit still OOMs at the SAME batch boundary with these sites recycled as
    without (an A/B test: byte-for-byte identical exit-137 at batch `[8:16]`). So
    these sites free *small* collection buffers in the compiler's own emit; the
    ≥512 KiB large path is rarely taken. The emit-all single-process batch limit
    is **arena-structural** — the bump pointer only retreats on process exit, and
    the self-host runtime serves fewer cross-window allocations from the freelist
    than the (fuller) native runtime, so gen1 accumulates where gen0 does not.
    Fixing THAT would need arena checkpoint/reset between windows, not more free
    redirects; it is deferred (the serial per-module fixpoint stays the proof).

## Slices

- **Slice 1 — retire `bundle_demo.fern`. DONE** (#5603). Dead AST-only demo,
  coverage redundant with the modload fixpoint's file-based multi-module cases.

- **Slice 2 — flip the bootstrap/fixpoint to per-module. UNBLOCKED (#3425
  closed); the remaining question is CI time, not memory.**
  Make `TestSelfHostModloadFixpointX86_64` drive `-per-module-*` (as
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` already does for gen0) so no
  path emits the merged bundle. The gen0 (Go-built) per-module emit is already
  fast enough for CI; the *self-reproduction* proof needs the gen1 emit, which
  no longer OOMs but runs ~16.6 min **serially** — past a 13-min shard.

  **Measured cost model (2026-07-26, gen0 driver, `RUN_MEASURE_SPLIT`).** Two
  earlier hypotheses are REFUTED by direct measurement:
  - The whole-program **parse+infer floor is only ~1.34 s** — so a
    single-process "parse once, loop-emit" mode saves essentially nothing on its
    own. *emit-all as a parse-once win is refuted.*
  - The per-unit emit cost is ~20–28 s and is **nearly independent of the unit's
    own size**: a 3-function module emits in 20.3 s, a 922-function module in
    27.3 s. So the cost is NOT per-window lowering — it is the **~22
    whole-program side-tables** `emit_module_funcs` derives on every call
    (`array_ret_fns_of` / `borrowable_params_interproc` / `str_ret_fns_of` / …,
    each an O(all_funcs≈1000) scan; asm_ir.fern:5489–5519). They run once per
    unit × 35 units. *(The code comment there calling them "cheap relative to the
    lowering they feed" holds only for large modules; for the many small units of
    the whole-compiler emit they dominate.)*
  - Because **every** gen1 unit peaks 5.5–7.8 GB (the retained whole-program view
    + those per-unit tables), 2 units won't fit a 16 GB runner. *Memory-budgeted
    parallelism is refuted too — there is no "one big, 34 small" split to exploit.*

  So the real lever is **hoisting the ~22 whole-program side-tables to
  compute-once** (the same move `wasm_ir.lower_all_for` and the `cache` mechanism
  at asm_ir.fern:5481 already make), which *requires* a single-process
  **`-per-module-emit-all`** driver mode to share them across units. Together
  they cut the ~20 s/unit recompute → gen1 per-module could drop from ~16.6 min
  to ~2–3 min AND lower per-unit peak (no per-unit table alloc), making the
  per-module fixpoint a real CI guard. Plan:
  - (a) **Add `-per-module-emit-all -out-dir DIR`** to `asm_modload_run.fern`:
    parse+infer once, then loop every module/window emitting each unit to `DIR`,
    passing a **once-computed** whole-program-table bundle into
    `emit_module_ir_unit` → `emit_module_funcs`. In-driver windowing mirrors the
    harness `emitWindowSize` (func_budget 100 + 300 KB byte budget) so the units
    are **byte-identical to the per-process fixpoint's** — a free correctness
    check.
  - (b) Refactor `emit_module_funcs` to accept the precomputed table bundle
    (compute-at-call-site for the single-unit callers = byte-identical + same
    cost; compute-once for emit-all = the win).
  - (c) ~~Then point `TestSelfHostModloadFixpointX86_64` at emit-all~~ **DONE, but
    by RETIREMENT rather than flip (2026-07-27).** A routine per-module BYTE-IDENTITY
    fixpoint is ~12 min (2 whole-compiler emit-all passes) — past a CI shard — so
    instead of flipping the merged fixpoint to that, it is **env-gated
    (`RUN_MERGED_FIXPOINT`)**: retired from routine CI so no routine x86 test drives
    the whole compiler through the merged AST emitter. Routine coverage is now
    `TestSelfHostModloadPerModuleWholeCompilerX86_64` (per-module emit+link+self-compile,
    ~6 min); the per-module byte-identity proof is the env-gated
    `TestSelfHostPerModuleEmitAllFixpointX86_64` (#5672). The merged fixpoint stays
    runnable on demand as a backstop until slice 5 deletes `asm.fern`. The arm64
    sibling `TestSelfHostFixpointArm64` is env-gated the same way, with
    `TestSelfHostModloadPerModuleWholeCompilerArm64` its routine gate — so **no
    routine test (either backend) now compiles the whole compiler through the
    merged AST emitter.**
  - **gen0 parallel per-module is already CI-affordable (~3.3 min)** — the fast
    guard `TestSelfHostModloadPerModuleWholeCompilerX86_64` already exists; only
    the gen1 self-reproduction proof needs the hoist to become CI-cheap.

  **BLOCKER found + ISOLATED (2026-07-26) — a self-host RC miscount when a
  `string[][]` is extracted-from, then passed across a boundary to a function
  that re-extracts it. Fix is the flat representation (see below).** (The initial
  "nested-aggregate return" framing was refuted by a minimal repro, then a
  four-step bisect isolated the real trigger — both recorded below.) The plan
  above was implemented end-to-end and
  *works on the native (Go-built) driver*: the hoist (a `compute_wp_tables`
  bundling the 22 side-tables) + a **batched** `-per-module-emit-all -out-dir DIR
  -unit-range LO:HI` (batches of ~8 units per process, sharing the derivation,
  each a fresh process so the per-window emit's ~0.4 GB net working set — which
  is NOT reclaimed within one process, so all 35 units in one process OOM ~16 GB
  — is released on exit) emitted the whole compiler **byte-identically to the
  per-process path, ~2.1× faster** (238 s vs 560 s), no OOM. **But the
  self-host-BUILT compiler segfaults**: the merged path routes trivial programs
  through `emit_module_ir_gated → compute_wp_tables` (asm.fern:7345), and the
  self-host backend miscompiles the bundle. Isolated:
  - It is NOT the table values (the emitted output was byte-identical) — it is
    the **compiler's own codegen** of the bundle-carrying functions.
  - The bundle was carried first as a **24-field struct** and then as a
    **`string[][]`** — BOTH segfault the self-host-built compiler. `compute_wp_tables`
    is the ONLY function in the whole self-host source that returns a `string[][]`.
  - A read-after-consume UAF in `emit_module_ir_unit_wpt` (indexing the bundle
    after passing it to `emit_module_funcs`, which consumes it) was found and
    fixed; the segfault persisted regardless.

  **Minimal repro attempted — plain `string[][]` is NOT the cause (2026-07-26,
  correcting the first hypothesis).** A throwaway differential
  (`RUN_NESTED_AGG_REPRO`, `asm_run` self-host IR emit vs the interpreter oracle)
  exercised five `string[][]` shapes on the IR path: return a locally-built
  `string[][]` (`return t;`, not a literal), return-then-index, share the same
  `string[][]` across two reader calls, **extract elements into locals then let the
  container die**, and extract-in-caller-then-consume-the-container-then-use — the
  exact `emit_module_funcs` / `emit_module_ir_unit_wpt` patterns. **ALL FIVE PASS**
  (route "ir", exit-match the interpreter). So plain nested-aggregate
  return/extract/share/extract-then-free all lower correctly in isolation — the
  "self-host can't codegen `string[][]` return" hypothesis is **refuted**. The
  segfault is **contextual to the full self-compile**, not the `string[][]` shape.
  Leading remaining hypotheses (unverified):
  - **Whole-program-analysis shift.** Adding `compute_wp_tables` + the `wpt_*`
    accessors to the compiler changes `all_funcs`, so the ~22 side-tables (derived
    OVER `all_funcs`) reclassify some *other* function, exposing a latent codegen
    bug elsewhere — which would explain why no self-contained `string[][]` program
    reproduces it.
  - **Multi-boundary re-extraction of the same container.** In the compiler the
    one `wpt` is extracted 22× in `emit_module_ir_gated`, passed to
    `emit_module_ir_unit_wpt` (extracted 2×), passed again to `emit_module_funcs`
    (extracted 22×) — a depth the repro did not chain.

  **Bisect done (2026-07-26) — isolated to multi-boundary re-extraction; the
  analysis-shift hypothesis is cleared.** Four bisect steps against
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` (~5 min each), each toggling
  one piece of the refactor:
  1. `compute_wp_tables` added but **uncalled** → **PASS** (but vacuous: the
     self-host DCEs uncalled functions, so it was never emitted).
  2. A **live** `compute_wp_tables(all_funcs, all_structs)` call in
     `emit_module_ir_unit`, result used trivially → **PASS**. So its own
     codegen-when-called is fine, and the whole-program **analysis-shift**
     hypothesis is **refuted** (adding it + calling it changes nothing).
  3. `emit_module_ir_gated` derives its lowering tables by **extracting** them
     from a `wpt` bundle (`wpt[i]`) and feeding them to `lower_func` in the
     per-function loop → **PASS**. So single-site extract-then-use is fine.
  4. The only untested delta left is the **multi-boundary pass**: gated extracts
     `wpt`, then PASSES the same `wpt` onward to `emit_module_ir_unit_wpt` →
     `emit_module_funcs`, which **re-extract** it. The full refactor (which does
     this) segfaults; steps 1–3 (which don't) pass. **By elimination the trigger
     is passing an already-extracted-from `string[][]` across a call boundary to
     a function that re-extracts it** — a self-host RC miscount (double-dec /
     UAF) on the shared container the native memory model tolerates. (Confirming
     step — add JUST the onward-pass+re-extract — was not run; the elimination is
     strong but not yet a direct repro.)

  **So the fix is the flat representation, and it's now well-directed.** Extract
  the 24 columns **exactly once** and thread them as **individual `string[]`
  params** through `emit_module_funcs` — the shared `string[][]` never crosses a
  boundary and is never re-extracted, so the RC miscount can't arise.

  **Step 1 of the flat fix is IMPLEMENTED + VALIDATED (2026-07-26).**
  `compute_wp_bases` derives the 24 bases; `emit_module_funcs` now takes the 23 it
  needs as **individual `string[]` params** (applying the per-module `append_dyn`
  tails); `emit_module_ir_unit` / `module_runtime_needs` call `compute_wp_bases`,
  extract once, and pass individuals. `emit_module_ir_gated` is unchanged (still
  derives inline for its cache lowering). `TestSelfHostModloadPerModuleWholeCompilerX86_64`
  **PASSES** — the self-host-built compiler no longer segfaults, and the emit is
  byte-identical (same derivation + `append_dyn`, just relocated). This confirms
  the flat individual-params shape is the fix.
  - **Step 2 is DONE + VALIDATED (2026-07-26).** `emit_module_ir_unit_flat` takes
    the pre-computed bases (the public `emit_module_ir_unit` is now a thin wrapper
    that computes `compute_wp_bases` + delegates), and the batched
    `-per-module-emit-all` is re-added: it computes `compute_wp_bases` ONCE per
    process, extracts the bases into individual `string[]` locals, and passes them
    to every unit's `emit_module_ir_unit_flat`. `TestSelfHostPerModuleEmitAllX86_64`
    (env-gated `RUN_EMITALL_CHECK=1`, ~19 min) is GREEN: emit-all is
    **byte-identical to the per-process path across all 35 units**, **2.6× faster**
    (278 s vs 720 s), links into a working compiler, no OOM. The append_dyn
    COW-on-shared concern held — shared bases reuse correctly across the batch.

  **So slice 2's speedup is landed for gen0.** A CI-affordable **gen1** emit-all
  fixpoint was attempted and is **deferred (2026-07-27): the emit-all
  single-process batch limit is arena-structural, not leak-based.** A gen0
  emit-all → link → gen1 → gen1 emit-all fixpoint OOMs (exit 137) when gen1
  batches many units per process: the self-host bump arena's pointer only retreats
  on process exit, and the self-host runtime serves fewer cross-window allocations
  from the freelist than the fuller native runtime, so gen1 accumulates within a
  batch where gen0 (native runtime) does not. Recycling the remaining large
  collection-buffer free sites (above) did **not** move the OOM boundary (A/B
  byte-identical), confirming the accumulation is not those leaks. Making gen1
  emit-all CI-cheap would need arena checkpoint/reset between windows — deferred.
  **The env-gated serial `TestSelfHostPerModuleFixpointX86_64` remains the gen1
  self-reproduction proof** (the plan always allowed the env-gated route), and
  gen0's parallel per-module path (`TestSelfHostModloadPerModuleWholeCompilerX86_64`)
  is the fast CI guard. That is sufficient to proceed: repoint the
  bootstrap/fixpoint drivers off the merged bundle (slice 3) and delete the AST
  emitters (slice 5). Do NOT re-run the `string[][]` repro (proven to pass), the
  analysis-shift probe (refuted), or the emit-all-batch-size search (arena-bound).

  `internal/`-vs-self-host convergence item (#4451).

- **Slice 3 — replace the now-unreachable AST fallbacks. UNBLOCKED.** This
  header read "BLOCKED on self-host whole-program emit memory" long after its own
  body recorded the unblock: `-assume-eligible` (#5668) halved the per-window
  peak, the gen1 emit-all fixpoint went green, and since #5804 it runs ungated on
  every lane. The blocker was real and the investigation below stands — but it is
  history, not current state. What remains is the mechanical part named at the end
  of this slice: repoint the drivers off the merged bundle, swap
  `asm.emit_module` for per-module-or-error, then delete.

  The one construct that still reaches the AST emitter on x86 is the
  whole-compiler self-compile: `asm_modload_run.fern`'s per-module rescue is
  bounded `nfuncs > 512 && nfuncs < 1500`, and the compiler's ~2040 merged
  functions fall past the upper bound to `write(asm.emit_module(mwb))`. Raising
  that bound is NOT the fix — a single-process concat of that many functions OOMs,
  which is why the batched `-per-module-emit-all` exists. The swap has to make
  that fall-through an error and repoint its callers, which also retires the
  env-gated `RUN_MERGED_FIXPOINT` backstops; that is slice 5's call, not a
  drive-by.

  Grounding the call graph in code: `asm.emit_module` is a *dispatcher* —
  it calls `asm_ir.emit_module_ir_gated`, which returns IR asm when the whole
  module is eligible, else `""` → the AST emit loop. For a normal program every
  function is IR-eligible, so `emit_module_ir_gated` already returns IR and the
  **AST emitter body is never reached**. The *only* thing that still reaches it is
  the **whole-compiler self-compile**: the merged bundle is ~1000 functions and
  trips the `mod.funcs.len() > 512` gate (`asm_ir.fern:5785`), which bails to AST.
  That gate is the single remaining AST trigger, and its comment says it stands
  "**until the native large-tier freelist is fixed**" (#3425) — now done. So two
  direct unlocks were probed against `TestSelfHostModloadFixpointX86_64` (the
  merged 3-generation self-compile):
  1. **Lift the budget (cached merged IR).** Route the whole ~1000-func bundle
     through the existing cached IR emit. → **Stage 1 (native runtime) fits;
     stage 2 (self-host runtime, mmc) OOMs (exit 137).**
  2. **Stream the merged IR emit (no cache).** `emit_module_funcs` already
     lowers+emits one function at a time when handed an empty `cache`
     (`asm_ir.fern:5676`), so the eligibility pass discards each result and the
     emit re-lowers per function — the whole-program IR is never resident. →
     **Still OOMs stage 2 (exit 137), same wall.**

  So the peak is **not** the whole-program IR cache (streaming removed it and the
  OOM stayed). It is the self-host runtime's inability to hold a whole-compiler
  emit's *working set* — the ~22 whole-program side-tables + the ~470 MB output
  buffer + per-function lowering churn — within the 8 GiB arena. The **native**
  runtime holds it (stage 1 passes every time); the **self-host** runtime does not
  (stage 2 OOMs). This is the same asymmetry as the emit-all gen1 finding, and the
  same root: the self-host runtime reclaims less completely than the native one
  (per emit-all measurement a single 100-func window already peaks ~7.6 GB —
  emit memory scales far worse than the output size implies).

  **Therefore slice 3 cannot proceed by making the merged path route IR.** The
  whole-compiler emit must stay **windowed** (per-module).

  **The per-window peak profiled + HALVED (2026-07-27).** A peak-RSS profile of
  the Go-built per-module driver (poll `/proc/<pid>/VmHWM`) localized the peak
  precisely, and it is neither the parse nor per-function accumulation:
  - It is a **module-INDEPENDENT baseline** — a 5-func window and a 50-func
    window of the same module both peak the same (`irlower` ~4.07 GB either way),
    and a tiny 15-func module (`util`) peaks ~3.05 GB. The parse+infer floor is
    only ~382 MB.
  - Bracketing the phases: the **per-module IR-eligibility pre-check**
    (`all_eligible_lib_known_view`) is the dominant contributor. It **fully
    re-lowers every function of the module** purely to verify it lowers — a
    SECOND whole-module lowering pass on top of the emit's own. (The interproc
    borrow/consume fixpoints add only ~350 MB combined; they are NOT the cost.)
  - On the self-host bump arena (no GC) the eligibility pass and the emit pass
    **stack**, which is exactly the gen1 ~7.6 GB ≈ 2 × ~3.8 GB single-pass.

  **Fix landed: `-assume-eligible`** skips the pre-check (`asm_modload_run.fern`).
  Byte-identical — skipping a pure verification pass cannot change an eligible
  module's asm (proven across all 15 whole-compiler modules) — and it ~halves the
  peak: `irlower` window 4.07 GB → 1.81 GB, `util` 3.05 GB → 1.75 GB. Default OFF
  preserves the clean "not IR-eligible" diagnostic; the known-eligible bootstrap
  opts in, with any regression still caught loudly by the fixpoint/smoke-run.
  Guarded by `TestSelfHostAssumeEligibleByteIdenticalX86_64`.

  **CONFIRMED: this unblocks the gen1 emit-all fixpoint (2026-07-27).**
  `TestSelfHostPerModuleEmitAllFixpointX86_64` — a gen0 → link → gen1 →
  gen1-emit-all byte-identity fixpoint — now runs green at **batch=8**, the exact
  per-process batch that OOM'd (exit 137) before `-assume-eligible`: 35 units in
  5 batches, **no OOM**, gen0 == gen1 byte-identically. Halving each unit's arena
  advance halved the per-batch accumulation, so batch=8 fits. Bonus: skipping the
  redundant eligibility lowering also makes it **~3.3× faster** (gen0 emit-all
  ~80 s vs ~270 s pre-fix; whole test ~9 min vs the ~16.6 min serial fixpoint).
  Env-gated (`RUN_EMITALL_FIXPOINT=1`), because its batch=8 is the A/B it exists
  to hold. The gen1 self-reproduction proof itself now runs on every lane as the
  ungated `TestSelfHostPerModuleEmitAllFixpointBatch4X86_64` — same fixpoint,
  batch=4 for the arena headroom.

  **So the slice-3 memory blocker is resolved for the per-module path.** The
  remaining work to retire the AST emitters is the mechanical part: repoint the
  bootstrap/fixpoint drivers off the merged bundle onto the (now memory-fit,
  self-reproducing) per-module path, then `asm.emit_module` → per-module-or-error
  swap, then delete. (`asm_ir_run.fern:158` stays the differential oracle
  regardless; retiring it means trusting the IR path outright, which the fixpoint
  + differential suites must justify.)

- **Slice 4 — untangle the arm64/wasm IR runtime from the AST files.**
  Independent of #3425, but **delivers no standalone deletion** (the driver still
  imports the AST file for the merged path until 2/3 land), so it is prep, best
  done *with* or *after* the memory work — not in isolation.
  - **4a arm64 (~5k lines).** Port x86's structure: give `asm_arm64_ir.fern` its
    own `emit_ir_runtime` (duplicate `asm_arm64.emit_runtime`'s hand-written
    bodies) + `emit_ir_runtime_fern_fn` (arm64 sibling of asm_ir's, IR-compiles
    the Fern helpers via `asm_arm64_ir.emit_function_via_ir`), move
    `emit_module_ir_unit_arm64` there, repoint `asm_modload_run.fern:233`. This
    **changes arm64 IR codegen** (the 31 Fern helpers become IR-compiled, matching
    x86) → re-baselines the arm64 fixpoint; it is NOT a byte-preserving move
    (`emit_function`'s transitive closure IS the AST emitter, so it can't move
    with the runtime). Note the helper sets have **diverged**: arm64 currently
    Fern-emits 11 helpers x86 does not (`i32_pow/gcd/lcm`, `i64/u64_to_string`,
    `arr_i32_{sum,product,min,max,index_of}`, `arr_str_index_of`) and x86 IR-emits
    3 arm64 keeps hand-written (`clock`, `env`, `random_bytes`) — reconcile
    per-helper (most of the 11 are likely `has_need`-gated off on the IR path;
    verify before porting). Validate: build the driver on x86 (compiles → module
    boundaries resolve), emit whole-compiler arm64 asm and diff (only the 31
    helper-body regions should change), run small arm64 programs under
    `qemu-aarch64` for correctness, then `TestSelfHostFixpointArm64` (the arm64
    byte-identity fixpoint) as the final gate.
  - **4b wasm (larger).** Same shape but move `emit_ir_module_units` /
    `emit_ir_rc_bodies_from` + the shared WAT runtime helpers out of `wasm.fern`
    into `wasm_ir.fern`.

- **Slice 5 — delete `asm.fern` → `asm_arm64.fern` → `wasm.fern` + the
  512-budget.** Repoint every driver + Go test module list; retire the AST-side
  differential tests (their oracle role ends when the IR path is trusted). Gated
  on all the above + #3425.

## Per-window emit peak: measured (2026-07-28)

**CORRECTION (same day).** The first version of this section measured the
per-module emit **without `-assume-eligible`**, which is not how the bootstrap
runs it. That flag skips an IR-eligibility pre-check that *fully re-lowers every
function of the module* — a second whole-module lowering pass on top of the
emit's own (see `asm_modload_run.fern`'s `-assume-eligible` comment and #5668,
which landed it precisely because it halves per-unit peak). Every number in the
first version was therefore ~2x the real figure, and — worse — its central
claim that the floor is *module-dependent* was **an artifact of that second
pass**, not a property of the emit. The corrected measurements are below. The
headline survives; the model does not.

Method: build gen0 (`fern -target x86-64 -o gen0 asm_modload_run.fern`),
`-per-module-emit-all -assume-eligible` in batches, assemble + link the 36 units
into gen1, then emit single windows sampling the kernel's `VmHWM`. Peak RSS in
MB; `-func-range LO:HI` picks the window. Output is byte-identical with and
without the flag, so the two configs are directly comparable.

**The floor is a whole-program constant, not module-shaped.** 1-function
windows, gen1, `-assume-eligible`:

| module | own funcs | peak MB (correct) | peak MB (no flag — artifact) |
|---|---:|---:|---:|
| util | 15 | 2218 | 2955 |
| lexer | 46 | 2217 | 2969 |
| asm | 18 | 2217 | 4271 |
| asm_ir | 80 | 2217 | 4356 |
| parser | 407 | 2217 | 5438 |
| irlower | 928 | 2217 | 7046 |

Flat to within 1 MB across a 60x range of module sizes. So the earlier
"`asm` has 18 functions and a higher floor than `lexer`'s 46, therefore the
whole-program side-tables model is too simple" reasoning was **wrong** — that
spread was the pre-check re-lowering each module in proportion to its own size.
With the pre-check off, the floor is exactly the whole-program constant the
side-table model predicts, and that model is **supported**, not refuted.

**Window size still barely matters at the budget in use.** Parser (407 funcs),
gen1, `-assume-eligible`:

| window | peak MB | emitted bytes |
|---|---:|---:|
| 0:1 | 2217 | 2.0 KB |
| 0:25 | 2219 | 23 KB |
| 0:100 | 2323 | 521 KB |
| 0:200 | 2697 | 1.9 MB |
| 0:407 | 5363 | 4.3 MB |

At the **100-function budget the emit actually uses**, peak is 2323 MB against a
2217 MB floor — the window contributes ~4.6%, so shrinking it can recover at
most that. Windowing is not the lever for the floor.

But note the tail, which the first version got wrong: a full 407-function window
costs 5363 MB, **+142%** over the floor, not the "+45%" the no-flag numbers
implied. So windowing very much *is* what keeps a large module affordable — the
100-func/300 KB budget is doing real work and must not be raised casually. The
correct statement is narrow: *at or below the current budget* the peak is ~95%
floor, so there is little left to win by windowing harder.

**Parse+modload is ~1.1 GB of the 2.2 GB floor** (`-per-module-count` 1096 MB on
gen1; 218 MB on gen0), so the emit call itself adds ~1.1 GB of whole-program
setup. That remainder is now **attributed** — see "The floor, attributed" below:
it is not spread across the ~24 derivations but concentrated in two
interprocedural analyses, and mostly in one. **And the dominant cause is now
FIXED** — see "The floor, fixed" below: the two analyses' cost was a
substring-slice storm in `param_is_borrowable`, not the fixpoint iteration, and a
no-alloc byte-compare dropped the gen0 emit-all one-unit peak from 1663 MB to
520 MB with byte-identical output.

**The self-host runtime is a multiplier.** gen0 (native runtime) vs gen1
(self-host runtime), parser, `-assume-eligible`: 1654 -> 2217 MB at 0:1, 1763 ->
2323 at 0:100, 2030 -> 5363 at 0:407. The flat-floor shape holds on both; gen1
runs ~1.3-2.6x above gen0, and that multiplier is what #3425-style reclamation
work buys back.

**Consequences for the plan.**
- The whole-program floor (~2.2 GB gen1) is the target, and it is a single
  constant — halving it is worth ~1.1 GB on every window of every module.
- Windowing harder buys ~5% at the current budget; raising the budget is
  expensive (407 funcs costs 2.4x the floor). Leave the budget where it is.
- `-assume-eligible` is load-bearing: without it every per-window peak roughly
  doubles. Any new per-module driver path must pass it.

## The floor, attributed: one interprocedural analysis (2026-07-28)

The section above measured the per-window peak as a whole-program floor and left
"attribute it to specific code" as the next step. Done — and it is **not** spread
across the ~24 side-table derivations. It is essentially **two functions**, and
mostly **one**.

Method: bisect by stubbing derivations in `compute_wp_bases` and re-measuring
peak RSS. gen0, `-per-module-emit 2 -func-range 0:1 -assume-eligible` (parser,
a 1-function window, so the floor is all that is being measured):

| configuration | peak MB | delta |
|---|---:|---:|
| baseline | 1650 | — |
| all 24 derivations stubbed empty | 463 | −1187 |
| the LAST 12 derivations stubbed | 1636 | −14 |
| `borrowable_params_interproc` + `consume_safe_params_interproc` stubbed | 469 | −1181 |
| `consume_safe_params_interproc` alone stubbed | 1317 | −333 |

So the split is:

| component | MB | share of peak |
|---|---:|---:|
| `irlower.borrowable_params_interproc` | ~848 | 51% |
| `irlower.consume_safe_params_interproc` | ~333 | 20% |
| the other 22 derivations, combined | ~6 | 0.4% |
| rest of the emit call | ~245 | 15% |
| parse + modload | ~218 | 13% |

The same bisect on `irlower` (module 11) gives the same numbers to within ~50 MB
(1604 baseline, 415 all-stubbed), consistent with the floor being
module-independent.

**Why these two.** `borrowable_params_interproc` is a *greatest-fixpoint*
analysis: it seeds every param optimistically borrowable, then re-runs a
whole-program escape walker until the registry signature stops changing (its own
header explains the from-above design and why it replaced the from-below one).
`consume_safe_params_interproc` runs over the same corpus with the result. Each
pass walks every function body and allocates; the self-host arena reclaims none
of it within a process, so the cost is (passes x whole-program walk), paid on
every `compute_wp_bases` call. Note the measurement establishes the *cost*, not
the pass count — nobody has instrumented how many passes the whole-compiler call
graph actually takes, and that is the obvious next thing to look at.

**Consequences.**
- The floor is not a diffuse "side-table" cost to be shaved 24 ways. It is one
  iterative analysis to make cheaper, reclaim inside, or avoid re-running.
- This is exactly what `-per-module-emit-all` already buys: it calls
  `compute_wp_bases` **once per process** and shares the bases across every unit
  (`emit_module_ir_unit_flat`'s `b` param), so the 1.2 GB is paid once instead of
  per unit. The per-process `-per-module-emit` path pays it 36 times over.
- ~~Cheapest wins, in rough order: cap or memoise the fixpoint's repeated walks;
  reclaim between passes~~ — **both closed, and by measurement, not argument**:
  the next section found the cost was never the walks, and the section after that
  built the memoised variant anyway and priced it. What remains of this bullet is
  the last clause: persist the computed bases across processes so the per-process
  path pays what emit-all pays.
- ~~A 2x reduction in `borrowable_params_interproc` alone is worth ~425 MB on
  every window of every module~~ — this framing is what sent the memoisation
  attempt down the wrong road; the analysis was never allocating what the
  attribution implied.

## The floor, fixed: a substring-slice storm, not the fixpoint (2026-07-28)

The attribution above landed the cost on the two interprocedural analyses and
guessed the mechanism was "(passes × whole-program walk), allocating each pass."
That guess was **wrong about the mechanism** — a far cheaper fix than capping or
memoising the fixpoint was available, because the allocation was not the walk.

An independent per-table sweep (a throwaway `-wpb-bench N` driver mode computing
only the first N of `compute_wp_bases`' tables; gen0, whole compiler, 2074 funcs)
reproduced the same split — `borrowable_params_interproc` +862 MB,
`consume_safe_params_interproc` +337 MB, the other 22 tables ~12 MB combined —
then a pass-count probe showed the fixpoints converge in only **6** and **3**
passes. Six passes cannot allocate ~860 MB of *retained* garbage; the peak was a
single sweep's **transient** churn. The allocator was `param_is_borrowable`,
which matched a registry key with `e[0:bar] == name` — a substring **slice** that
allocates a fresh string — scanned linearly across the whole borrowability
registry per call, and called for every call-arg on every body walk of both
fixpoints. That is millions of throwaway substrings per emit.

**Fix (this slice):** compare the key prefix by byte, with an O(1) length guard
first — no allocation. Semantics identical (`e[0:bar] == name` ⟺ `bar ==
name.len()` && bytes equal). Measured on gen0, whole compiler:

| metric | before | after |
|---|---:|---:|
| `borrowable_params_interproc` table peak | +862 MB | **+13 MB** |
| `consume_safe_params_interproc` table peak | +337 MB | **+0 MB** |
| emit-all one-unit peak | 1663 MB | **520 MB (−69%)** |

All 36 whole-compiler units emit **byte-identically** before vs after, so this is
a pure heap win with zero codegen change. The floor is now parse+modload+infmods
(~390 MB gen0) plus the cheap tables and one window; the emit-side analysis cost
is no longer the lever. The hypothesised "cap/memoise the fixpoint" and
"reclaim between passes" work is now unnecessary for this floor. **Next:**
re-measure the *gen1* floor (the ~1.3–2.6× self-host-runtime multiplier still
applies on top) and whether the per-module fixpoint can come off its env gate.

## The gen1 floor, and the memoisation priced (2026-07-28)

Answering the "Next" above, and closing the memoisation hypothesis with a built
variant rather than an estimate. Same method throughout (`-assume-eligible`;
gen1 = a compiler linked from gen0's own `-per-module-emit-all` units, so it runs
the self-host runtime).

**The gen1 floor followed gen0 down.** Parser module, gen1:

| window | before the slice fix | now |
|---|---:|---:|
| `0:1` (the floor) | 2241 | **1602** |
| `0:100` (the budget in use) | 2347 | **1706** |
| `0:407` (whole module) | 5388 | **2436** |

`irlower` at `0:1` measures 1601 MB against parser's 1602, so the floor is still
the module-independent constant the side-table model predicts.

**Parse+modload is now the floor.** `-per-module-count` alone (parse + modload,
no emit) peaks **1122 MB** on gen1 — **70%** of the 1602 MB floor, leaving only
~480 MB for the whole emit call. The doc's earlier ~390 MB gen0 figure has become
the dominant term on gen1. Anything further spent on emit-side analysis is
chasing the remaining 30%; **the next real target is module loading**, not the
side tables.

Note also how much the 407-function window fell (5388 → 2436). The old warning
that "windowing is very much what keeps a large module affordable" is now much
weaker — a whole 407-func module costs 1.5x the floor, where it used to cost 2.4x.

**The gen1 emit-all fixpoint is now cheap enough to reconsider its env gate.**
Driving gen1 `-per-module-emit-all` over the whole compiler in batches, and
diffing against gen0's units:

| batch size | wall | max per-batch peak | result |
|---|---:|---:|---|
| 8 (what `TestSelfHostPerModuleEmitAllFixpointX86_64` uses) | **118 s** | **7909 MB** | gen0 == gen1, all 36 units |
| 4 | 154 s | 6754 MB | gen0 == gen1, all 36 units |

118 s for the gen1 half, against the ~9 min the whole test cost when it was
env-gated (`RUN_EMITALL_FIXPOINT`). **But look at the peak before de-gating it:**
batch=8 reaches 7909 MB against the **8 GiB arena** — ~99% of the ceiling, i.e.
the configuration is one compiler-source addition away from the exit-137 wall
again. Halving the batch costs +36 s and buys only 1.15 GB (the accumulation is
dominated by the largest units, not the count). The per-unit floor is no longer
what limits batching — the bump arena's never-retreating pointer is, exactly as
the 2026-07-27 entry concluded.

Note what that does *not* mean: **do not just lower
`TestSelfHostPerModuleEmitAllFixpointX86_64` to batch=4.** Its batch=8 is
load-bearing — the test exists to hold `-assume-eligible` against *the exact
configuration that OOM'd without it*, and re-tuning the batch deletes the
regression it guards. If the fixpoint is de-gated for routine CI, add a batch=4
run for that and leave the batch=8 A/B env-gated as the backstop.

**DONE.** `TestSelfHostPerModuleEmitAllFixpointBatch4X86_64` is that batch=4 run
and it is UNGATED; the batch=8 sibling keeps `RUN_EMITALL_FIXPOINT`. Both drive
the shared `runEmitAllFixpoint`, so the batch size is the only difference between
them. This is the guard slices 3 and 5 rest on: repointing the driver at the
per-module path and then DELETING the AST emitters is only safe while something
proves a self-host-BUILT compiler emits the same units the Go-built one does, and
while that proof was gated it only ran when someone remembered it.

Measured 2026-07-29, 36 units in 9 batches, gen0 == gen1 both times:

| host | gen0 | gen1 | whole test |
|---|---:|---:|---:|
| 4-core dev box, cold driver build | 59.4 s | 160.9 s | 363.7 s |
| x86_64 CI lane, modload driver warm | 40.1 s | 79.3 s | **130.2 s** |

The CI number is its entry in `.github/selfhost-test-weights.txt` — note that an
unlisted test defaults to weight **1**, so without an entry a two-minute test
would be scheduled as trivial and stack behind another fixpoint. At 130 it sits
just under `TestSelfHostModloadPerModuleWholeCompilerX86_64` (210), so LPT gives
it its own bucket without it becoming the max-shard wall: on the first ungated
run it landed alone in shard 0 (`shard 0/20: 1 selfhost-pkg + 0 residual tests`),
which finished in 177 s against a ~9.7 min slowest shard.

**The memoisation, priced (rejected).** The "cap or memoise the fixpoint's
repeated walks" bullet was implemented and measured rather than left open. Both
fixpoints carry a settled per-param flag forward (sound: borrowable flags are
monotone 1->0 as its registry shrinks, consume-safe 0->1 as its grows), so a
settled param is never re-walked. On top of the slice fix, byte-identical
throughout:

| configuration | peak MB | note |
|---|---:|---|
| current main | 508 | parser `0:1`, gen0 |
| + drop the quadratic `borrowable_sig` convergence signature | 507 | no effect |
| + memoise both fixpoints' settled flags | **477** | −6% |
| current main, whole-compiler emit-all | 3358 / 21 s | |
| + memoisation | 3326 / 20 s | −1% |

So the whole memoisation is worth ~30 MB and ~1 s, and deleting the quadratic
signature build is worth nothing measurable. **Not landed** — a monotonicity
invariant spanning two fixpoints, for 6% on a 1-function window and 1% on the
emit that matters, is not a trade this floor needs; it would also have to be
maintained forever against a bar no cheap test can enforce (see below). Recorded
here so the option is closed by measurement instead of being re-derived a third
time.

## The load pipeline leaks, on BOTH runtimes (2026-07-28)

Following the section above's "the next real target is module loading", the
1121 MB is **not** a parsing algorithm being expensive, and **not** (only) a
self-host representation cost. Loading leaks essentially all of it, on the
native runtime too.

Method: a throwaway `-load-bench N` driver mode running the full
read→tokenize→parse→`load_imports`→`bundle_per_module` pipeline N times, each
result dropped before the next. A runtime that reclaims holds a flat peak.

| loads | gen0 (native runtime) | gen1 (self-host runtime) |
|---|---:|---:|
| 1 | 218 MB | 1120 MB |
| 2 | 428 MB | 2239 MB |
| 3 | 632 MB | 3358 MB |

Exactly linear on both: **+210 MB per load native, +1119 MB per load
self-host, zero reuse.** So there are two independent facts, and the second one
is the one that had been assumed to be the whole story:

1. the load's working set is never reclaimed on **either** runtime; and
2. separately, the self-host runtime allocates ~5.3x the bytes for the same
   work (the emit call, by contrast, sits at 1.66x — inside the documented
   1.3-2.6x band, so the multiplier is concentrated in loading).

**Where it leaks.** Per-load leak by stage, gen0, measured on large inputs
(a small entry file leaks under 1 MB and rounds to zero — the first version of
this bisect used the ~700-line driver as its input and wrongly read
tokenize/parse as leak-free):

| stage | `irlower.fern` (41k lines) | `parser.fern` (14k lines) |
|---|---:|---:|
| tokenize only | 44 MB | 16 MB |
| + `parse_module` | 64 MB | 24 MB |

So ~69% of it is the **lexer** (`Token[]`) and ~31% the parser's AST, scaling at
~1.6 KB leaked per source line. `load_imports` is not itself at fault — it just
calls the pair 16 times.

### A loop-append reclamation bug (real; a fix was tried and REVERTED)

**CORRECTION, and the third time this section has over-generalised from a
probe.** Everything below reproduces. It is *not* the cause of the per-load leak
this section set out to explain: with a candidate fix in (`rhsTainted`'s
receiver-only arm for `__method_Array_push`), the probe goes to allocs==frees,
and the whole-compiler load leak is **unchanged** — 218/428/632 MB before,
221/429/637 MB after, still +208 MB per load, and `tokenize`-only on
`irlower.fern` stays at 40 MB per call either way.

**That candidate fix is also UNSOUND at compiler scale, and was reverted.** It
passes `internal/ir`, `internal/e2e` (1560 s, 0 failures), and every unit suite —
then breaks `TestSelfHostStdTestE2EArm64` with 7 failures whose signature is
freed-and-reused memory inside the self-host compiler: truncated symbols
(`unknown mnemonic '__fn_m'`, `symbol '__fn_' is already defined`), a
definition missing while its call site survives (`undefined reference to
__fn_test__assert_eq__i32`), and two segfaults in the compiler itself. Suspected
mechanism, unproven: `escapeOwned` deliberately does NOT taint a consuming-match
binding, so once the receiver stops inheriting the element's taint, the buffer's
deep drop and the binding's own exit sweep can both dec the same element.

**The load leak, measured EXACTLY (2026-07-29) — it is not "a buffer per call".**
Every earlier figure in this section came from RSS. Rebuilding the load-bench
driver with `FERN_LEAKCHECK=1` gives block counts instead, and they say something
much blunter (both scale exactly x2 at N=2, so this is per-load, not startup):

| workload | allocs | frees | leaked | freed |
|---|---:|---:|---:|---:|
| `tokenize` only, `irlower.fern` | 1,275,345 | 106,086 | **1,169,259** | 8.3% |
| full load, whole compiler | 6,517,275 | 1,137,197 | **5,380,078** | 17.4% |

**Over 80% of every allocation in the lex/parse path is never freed** — ~28
blocks per source line, which is the whole token population (each token is an
enum box plus a payload string), not merely the `Token[]` buffer holding them.
So the target is not a single missing drop site: it is that element reclamation
for the token/AST graph essentially does not happen. Anyone picking this up
should start from these counters, not RSS, and watch the FREED FRACTION rather
than megabytes.

**And it has a 12-line repro** — `examples/probes/enum_array_element_leak.fern`.
An array of enum values carrying heap payloads, built and dropped: `allocs=19024
frees=13024` (68.5% freed, ~3000 blocks leaked per 5000 elements per round). So
element non-reclamation does NOT need the whole compiler to demonstrate, which
makes this a seconds-long edit/measure loop rather than a multi-minute one. Note
the compiler is far worse (8.3% freed for `tokenize`) than this probe (68.5%),
so the probe is a starting point, not the whole story — verify any fix against
BOTH, per the method note above.

**Four leak shapes characterised, two causes found, and BOTH fixes proven
UNSOUND (2026-07-29). Nothing here is fixed — read this before trying again.**

Probing out from this section's probe found four distinct shapes that strand
elements, measured with `FERN_LEAKCHECK=1` on x86-64:

| shape | freed |
|---|---:|
| `var t = <heap str>; xs = xs.append(t)` | 35.6% |
| ...the same, into an enum payload (this section's probe) | 69.1% |
| `var e = out[0]` — a counted view outliving its owner | 66.7% |
| a builder fn returning an appended array | 37.1% |

Inlining the element (`xs.append(<expr>)`) is clean in every case; binding it to
a local first is what leaks.

**Cause 1 (shapes 1, 2, 4) — the escape taint.** `computeFreeEligible` taints a
direct-Ident source at an INC-ing sink (`escapeOwned`, and the `Array_push`
arm), justified by the move-on-construction pairing. That justification does not
hold in a loop: `markConstructionMoves` only fires under its dominance guards
(top-level statement, no preceding return), and for `Array_push` there is no
`markConstructionMoves` case at all — `b.rc.moveSites` is never set for a push
element, so the sink's inc ALWAYS fires and the taint strands it. Via
`rhsTainted`'s any-arg rule the taint also reaches the BUFFER through `xs =
xs.append(t)`, downgrading its deep `__fern_drop_arr_str` to a shallow dec that
frees no elements at all.

**Cause 2 (shape 3) — drop ORDER.** Locals are swept in DECLARATION order, so a
counted view (declared after the container it reads from, not freeEligible, so
released with a plain non-freeing dec) outlives the owner whose deep walk would
free the element: the walk decs the element to rc 1 and frees the buffer, then
the view's plain dec takes it to rc 0 with nothing able to free it.

**Both fixes take the probes to 100% freed, pass the ENTIRE non-e2e suite — and
segfault the self-host compile.** `TestSelfHostLoadFixpointX86_64` passes on
main (319s) and fails with each. Bisected across full runs of that gate:

| configuration | result |
|---|---|
| main | pass |
| taint fix + reverse-order sweep + `computeFreshLocals` unfold | segfault, stage 2 |
| minus the `computeFreshLocals` unfold | segfault, stage 2 |
| the two taint sinks only | segfault, stage 1 |
| the `Array_push` taint sink alone | segfault, stage 2 |
| **reverse-order sweep alone** (re-run ISOLATED on an idle host) | **segfault, stage 1** |

So the two taint sinks fail independently of each other AND of the ordering
change, and reverse declaration order — the standard scope-exit order — is
independently unsound here too. The last row was re-run with nothing else on the
machine (0/15 GB used) to rule out contention; CLAUDE.md's note that arena
exhaustion reports exit 137 rather than SIGSEGV also argues against an OOM
reading.

**Hypotheses tested and REFUTED**, so they need not be re-tried:
- *Constructor-reuse donation.* `computeReuseSources` requires `freeEligible`
  and already excludes `movedLocals` because a box moved into a live container
  stays reachable through it. Adding the equivalent guard for the COUNTED case
  (a `sinkEscaped` set, keeping the dec but blocking donation) does NOT rescue
  the compile.
- *`StructLit` not inc'ing string fields.* It does — the inc site is exactly
  `needsRcIncOnAlias(f.Value) && !moveSites[f.Value]`, with no field-type gate,
  and escaping-container probes come back 100% freed with interp-matching exit
  codes.
- *A borrowed string param becoming overwrite-droppable.* Plain borrowed params
  never enter `freeEligible` at all — that loop gates on
  `own` / owned-by-default / `consumedParams`.

**The e2e signature of the taint fix**, useful for recognising a repeat: it
turns 6 `internal/e2e` tests red that pass on main —
`TestWasmSelfHostF64Coerce`, `TestWasmSelfHostF64ToI64`,
`TestSelfHostFloatBitsIR{X86_64,Wasm}`, `TestX86_64TrmcDeepStack`,
`TestFetchDeadlineX86_64`. Their surface (wasm float coercion, a fetch
DEADLINE) looks unrelated to refcounting and is not; do not dismiss them.

**Narrowing the drop-ORDER half (follow-up, same day).** Two further negative
results, both worth not re-deriving.

FIRST — a TRAP in the obvious implementation, which is NOT the cause but will
cost a cycle if hit. `info.Locals` can hold SEVERAL entries under one name
(shadowing — the reason `localNameUnique` exists); they share a slot, and their
TYPES can differ. The exit sweep's `seen` map means forward iteration lets the
FIRST entry pick the drop helper, so iterating the raw slice BACKWARDS silently
lets the LAST one win and a shadowed slot is dropped as the WRONG TYPE. That is
not a reordering at all — it changes which helper each slot gets, and is by
itself an adequate cause for a segfault. Any attempt must SELECT forward and
EMIT in reverse, as two separate loops.

SECOND — doing exactly that does NOT fix it. With selection identical to main
and only the emission order reversed, the counted-view leak is still repaired
(3000/3000 allocs/frees vs main's 3000/2000, exit code unchanged) and the
self-host driver is STILL miscompiled. So it is genuinely the ORDER of the
releases that is load-bearing — not the choice of helper, and not shadowing.

**Use this reproducer, not the load fixpoint.** `TestWasmSelfHostF64ToI64/expr`
and all five `TestWasmSelfHostF64Coerce` cases fail under a reverse-emission
sweep, and they beat `TestSelfHostLoadFixpointX86_64` on both axes: the symptom
is WRONG OUTPUT rather than a segfault (`(a * 2.0) as i64` prints `0`, want
`5000000000`), and once the driver binary is built each case fails in
0.02-0.13 s instead of a ~5-minute compile. Build the driver once, then iterate.

**The drop-order culprit is ONE function: `parser__parse_primary`.** Found by
bisection rather than by guessing at shapes, and worth recreating the same way
if this is picked up again. Gate the reversal on a hash of the function name —
`fnv32a(fn.Name) % 1000` in `[LO,HI)` from two env vars, neutral (byte-identical
to main) when unset — then binary-search the range against the fast reproducer
above. Ten driver builds, ~20 minutes, converges to a single bucket:

    [0,1000) FAILS -> [0,500) FAILS -> [0,250) FAILS -> [0,125) passes
    -> [125,187) FAILS -> [156,171) FAILS -> [163,167) FAILS -> bucket 166

Bucket 166 holds exactly two functions, and reversing each ALONE separates them
cleanly: `parser__mono_stmt` reversed is **fine** (`ok  104.987s`);
`parser__parse_primary` reversed **fails** (`FAIL  108.160s`). Everything else in
the compiler can have its sweep reversed without breaking the build.

**What its sweep looks like.** `parse_primary` is 306 lines with **58 distinct
rc-tracked locals** in the exit sweep, 22 of them `eligible=true`. The
structural feature is that a destructuring `var (a, b, c) = f()` emits a hidden
`__destruct_<line>_<col>` TUPLE local that is `eligible=true` (a deep walk that
decs elements and frees the buffer) declared IMMEDIATELY BEFORE its component
locals, which are `eligible=false` (plain, non-freeing decs):

    [6] __destruct_1600_29  eligible=true   (ParamDecl, string, i32, i32, Par)
    [7] ltpr_param          eligible=false  ParamDecl
    [8] ltpr_destr_names    eligible=false  string
    [9] ltpr_p              eligible=false  Par

That is an owner-before-views ordering encoded in DECLARATION ORDER — the deep
walk runs first, the plain decs after. It is the same owner/view relationship as
the counted-view leak in the table above, but wanting the OPPOSITE order, which
is a plausible reason a blanket reversal cannot satisfy both. It also has six
locals declared twice in disjoint branches (`elems`, `iife`, `look`, `no_args`,
`er_expr`, `er_p`); since `b.locals` is keyed by NAME they share one slot and are
swept once.

**Small-program extraction has now failed five times.** Neither a struct holding
an owned array, nor that shape split across mutually-exclusive branches, nor a
`var (a, b) = f()` destructuring of a `(string, i32[])` diverges between forward
and reverse — all match their interp oracle with identical leakcheck totals. The
effect needs `parse_primary`'s scale, so the next attempt should bisect WITHIN
the function (drop-index ranges rather than function-name buckets) instead of
trying to write the small program from the outside.

**What is still unknown:** what the escape taint protects BEYOND the exit sweep,
and why `parse_primary` specifically depends on release order. The remaining
suspects, in order, are the eligible-tuple/ineligible-component pairs above and
the six name-shared slots.

**Method note for the next attempt: `internal/e2e` is not the gate for an RC
change — `internal/e2eselfhost` is.** Compiling the whole self-host compiler is
what exercises RC at a scale where an over-release shows; the native e2e suite
passed this change cleanly.

The probe and the lexer share a *symptom* (an array built by `xs =
xs.append(...)` in a loop, leaking one buffer per call) but not a *mechanism* —
`lexer.tokenize` RETURNS its `out`, so the array escapes and is legitimately
tainted in the callee; its leak is at whoever drops the returned value, which is
still unattributed. I never checked that the two shared a mechanism before
writing "ROOT CAUSE" at the top of this section.

**Method note, since this keeps recurring:** a minimal repro that reproduces the
symptom is not evidence about the original program until you fix it and re-measure
the ORIGINAL. That check takes one driver rebuild and would have caught this, the
"512 KiB threshold", and the struct-vs-enum framing.

### The loop-append bug itself

Minimal repro: `examples/probes/loop_append_drop_leak.fern`.

**Measure this class of bug with `FERN_LEAKCHECK=1`, not RSS.** The native
backend already has an exact leak detector (#5362 slice 1) that prints
`leakcheck: allocs=N frees=M live_bytes=K` at exit. Every RSS-derived conclusion
in the first two versions of this section was wrong in some way, and each one
collapsed the moment the counters were used instead:

| shape, 4 calls | allocs / frees | verdict |
|---|---|---|
| `var xs: i32[] = [1, 2, 3];` | 4 / 4 | reclaims |
| `xs = xs.append(1);` straight-line | 8 / 8 | reclaims |
| `var ys: i32[] = xs.append(1);` (new binding) | 8 / 8 | reclaims |
| **`while (i < 1) { xs = xs.append(i); i = i + 1; }`** | **8 / 4** | **1 block leaked per call** |

One iteration is enough. The leak is exactly **one block per call** — the
array's final buffer — at every size tested (n=2000/20000/200000 all leak 4
blocks in 4 calls; only `live_bytes` scales, 49 KB / 393 KB / 6.3 MB). The
growth reallocs are all freed correctly; it is the function-exit drop of the
loop-carried binding that never happens.

`lexer.tokenize` is `out = out.append(tok)` inside a `while (true)`, which is
what drew attention here — but see the correction above: its leak survives this
fix, because it RETURNS `out`. Same surface shape, different mechanism.

**What the candidate fix was.** `rhsTainted` treating `__method_Array_push` as
aliasing its RECEIVER only, joining `__method_Array_set` (and `Map_set`) which were already
special-cased for exactly this. Without it, a loop counter's own `i = i + 1` — a
`Binary` RHS, tainted by the conservative default — was passed as the appended
element and tainted the receiving array, so the exit sweep fell through
`__fern_arr_dec` (which returns the buffer) to a flat `__fern_rc_dec` (which does
not). Sound for the same reason `set` is: the ELEMENT's aliasing is handled by
the escape sink, which documents Args[0] as "the receiver array (threaded /
reassigned), not retained" and runs `escapeOwned` on Args[1].

Measured on the two shapes whose IR-level baselines this changes, both still
exiting 3 against `fern -interp`: frees 600 -> 800 of 1000 allocs
(`out.append(src[0])`), and 200 -> 400 of 600 (`out.append(row)`), live_bytes
16000 -> 6400 in both.

**Three of this section's earlier claims were RSS artifacts, now retracted:**

- ~~"nothing leaks up to the 256 KiB class, then ~1.2-2.4x the array size per
  round from 512 KiB up"~~ — there is **no size threshold**. Small leaks were
  simply invisible under a ~9 MB baseline.
- ~~"an array of structs with a string field does not leak, `Token` is an enum"~~
  — shape is irrelevant; struct and enum leak identically at equal size.
- ~~"the leak is dominated by element payloads, `i32[]` leaks 14x less"~~ — the
  payload objects leak only because they are elements of a buffer that is never
  dropped, so their drop never runs. `i32[]` leaks the same *one block*; it just
  has no payloads riding on it.

**Suspects eliminated along the way.** `__fern_free`'s large tier bins to 3
significant bits up to 1 GiB and is fine. `__fern_alloc_reuse` is cleared for the
native backend — its class-mismatch path calls `__fern_free` before allocating,
so it cannot leak the donor. (The self-host runtime's `.Lsarelo` remains open,
but this leak reproduces natively, so it is not the cause.) The fault is in RC
insertion for a loop-carried owned binding, not in the allocator.

**Scope — not self-host-only.** It reproduces on the native backend, so it
affects `fern` itself and anything that parses repeatedly in one process.
`fern-lsp` re-loads on every edit, which is exactly the long-running,
allocation-heavy workload CLAUDE.md now puts in scope. `internal/ir` already has
a `loop_var_drop_test.go`, so this is adjacent to analysis that exists — start
there.

**Small-program tests cannot guard these analyses — measured.** Building a driver
with the borrowable fixpoint's cap forced to 1 (un-converged, hence unsound in
the over-borrow direction) *does* change the registry — a param forwarded to a
callee stays borrowable because pass 1 still sees the optimistic seed — yet a
suite of targeted probes (borrow cycle, forward-declared callee, direct escape,
escape through a forwarder, consume-rebind cycle, and a variant stashing a
loop-local into a container that outlives it) all pass, on **byte-identical**
asm. At that program size the flag has no codegen effect. The gate that catches a
wrong registry is whole-compiler byte-identity plus the checker corpus; do not
accept a small-program test as evidence for a change to these fixpoints.

## Recommended order

1. **#3425 — DONE** (large-tier freelist port, x86 #5609 + arm64 #5614; the
   self-host collection-buffer siblings x86 #5651 + arm64 #5652; gen1 per-module
   fixpoint GREEN). The freelist port was a real unblocker but, as the slice-3
   investigation now shows, **not sufficient** to let the merged whole-compiler
   emit fit the self-host runtime.
2. **THE REAL BLOCKER (all of slice 3/5 gate on it): self-host whole-program
   emit memory.** Both direct ways to make the merged bundle route IR — lifting
   the 512-func budget, and streaming the emit with no cache — OOM the self-host
   runtime at stage 2 (see Slice 3). The whole-compiler emit only fits when
   **windowed** (per-module), and a single 100-func window peaked ~7.6 GB.
   **The profiling pass this step called for is DONE (2026-07-28) — see
   "Per-window emit peak: measured" below, including its same-day correction —
   and the biggest attributed piece is now FIXED.** At the 100-func budget the
   emit uses, ~95% of the peak was a whole-program floor a 1-function window
   already pays. That floor split ~half parse+modload, ~half the
   `compute_wp_bases` emit setup; the emit-side half was pinned to two
   interproc-fixpoint tables and a single substring-slice storm in
   `param_is_borrowable`, whose no-alloc rewrite dropped the **gen0** emit-all
   one-unit peak from 1663 MB to **520 MB** (byte-identical, all 36 units). The
   gen1 multiplier (~1.3-2.6x) still applies. **Both follow-ups are now DONE —
   see "The gen1 floor, and the memoisation priced".** The gen1 floor fell 2241 ->
   **1602 MB**, of which **1122 MB is parse+modload alone**, so module loading —
   not the emit-side side tables — is what is left to attack. The gen1 emit-all
   fixpoint now takes **118 s** and stays byte-identical to gen0, cheap enough to
   de-gate, except that at its current batch=8 it peaks **7909 MB against the
   8 GiB arena**; de-gate at batch=4 (154 s, 6754 MB) rather than at batch=8.
3. **Slice 3 driver repoint + Slice 5 deletion** — mechanical once (2) lands.
   The safety net it needs is now standing: the gen1 self-reproduction fixpoint
   runs UNGATED as `TestSelfHostPerModuleEmitAllFixpointBatch4X86_64`, so a
   repoint that breaks byte-identity fails CI instead of waiting for someone to
   remember `RUN_EMITALL_FIXPOINT=1`.
4. **Slice 4a/4b** (arm64/wasm runtime untangle) alongside/after — a ~5k-line,
   qemu-gated duplication that unlocks nothing on its own; avoid until the
   endgame is reachable.

Do NOT re-probe the budget-lift or streaming merged-IR paths — both are recorded
above as OOMing the self-host runtime at stage 2; they are ruled out, not untried.
