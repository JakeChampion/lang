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
| `match (f())` where `f` is a closure LOCAL | **partly closed** — the alias shape lowers; the direct shape is a LIFT gap, not a recovery one (see below). First recorded as `fs[i]()`, which was the wrong characterisation |
| `None as Option[T]` — an ascription scrutinee | **closed** — shared `unary_opt_type` |

#### The lambda-bound case, characterised properly (now CLOSED — see the 2026-07-29 subsection below)

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

**Resolved by instrumentation (2026-07-29) — it is NOT an `Option` recovery gap
at all.** Putting an `eprint` on the scrutinee-type bail
(`if (srt == "") { return s.fail(); }` in `lower_stmt_match`) shows that site is
**never reached**. The direct shape bails in `emit_module_ir_gated`'s
`const_func` check instead:

```
FERN_STRICT_IR: main (function value main$clo not defined)
```

The lowered IR references a hoisted closure `<fn>$clo` that the lift did **not**
append to the module. So the remaining work is in the lift's
registration of the hoisted closure, not in any resolver arm — which is exactly
why a scrutinee-side fix leaves the direct shape untouched.

That correction cost four wrong hypotheses about which resolver arm was missing,
every one of them plausible from reading the code. The lesson generalises: when
`FERN_STRICT_IR` says a function bailed, **instrument to find WHICH bail** before
theorising, because the flag names the function, not the site — and the four
sites in `emit_module_ir_gated` (lowering, unknown call symbol, undefined
function value, budget) fail for completely unrelated reasons.

What the closure-local `closure_opt_rets` entry DOES close is the alias shape
(`var g = f; match (g())`), landed with `mark_closure_opt_ret`. The detail that
makes it work is that the lift runs BEFORE lowering, so the init is a
`__mkclo$<cloname>` marker call whose callee ident is not a module function —
`<cloname>`, after the 8-char prefix, is. Reading the callee name directly
recovers nothing and the whole helper goes inert; an earlier revision did
exactly that and was reverted for being byte-identical on every probe.

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

**CLOSED (fix option 1).** `subst_fcall_stmts` gained a `StmtMatch` arm
(scrutinee + arm bodies + guards), so the leftover-`f` guard no longer declines
the lift: `match (f())` rewrites to `match (__lam_N())` and the binding lifts
like the `var o = f()` path. Both the `const_func` and the `lower` bail clear.
Pinned by `match-closure-local-opt` / `match-capturing-closure-local-opt` in
`strictIRCorpus` (route IR under FERN_STRICT_IR on x86-64 AND wasm); the batch=4
whole-compiler emit-all fixpoint stays gen0==gen1 byte-identical. Watch for the
latent native-codegen footgun this surfaced: reusing the `StmtAssign` arm's
local name `a` for the new `MatchArm` binding made the IR field resolver mis-key
`a.target` to `MatchArm` and abort codegen — bind a distinct name (`marm`).
**Follow-up CLOSED (annotated named-fn) + one artifact ruled out.** The
"Result-payload lambda" (`var f: () => Result[i32,i32] = () => Ok(5)`) is NOT a
gap — it is native-INVALID (`E003`: `() => Ok(5)` infers `() => Result` with no
`Err` type), so it never reaches the gate; the valid Result shape (fn-form with
an explicit return) already routes IR. The **annotated** named-fn-bound form
(`var f: () => Option[i32] = g; match (f())`, Option AND Result) now routes IR:
`lower_stmt_var` seeds `mark_closure_opt_ret` from `closure_init_opt_ret` for a
fn-typed local, so `closure_opt_ret` at the match recovery names the payload.
Pinned by `match-fnlocal-named-opt` / `match-fnlocal-named-result` (x86-64 AND
wasm); fixpoint stays gen0==gen1. **The seed is GATED on the fn-type annotation**
because the bare unannotated `var f = g` form miscompiles on the IR path
(SIGSEGV, caught pre-landing), so the match form deliberately stays on the AST
fallback.

**The bare `var f = g` miscompile is BROADER than the match, and it is a
WRONG-ANSWER bug, not goal-1 debt** (characterised 2026-07-29, asm-verified).
Even `var f = g; return f()` with NO match routes IR (no bail) and SIGSEGVs,
while native interp and the annotated form both return correctly. Root cause is
the `const_fns` design (#2954): a bare 0-arg receiver-less fn-name is a *const
accessor*, auto-CALLED, so `var f = g` lowers to `call __fn_g` (f = g()'s result)
instead of `leaq __fn_g` / `const_func` (g's address). The later `f()` then
calls that result as a code pointer → SIGSEGV. The checker types `f` as
`() => i32` (a fn value) while the `const_fns` lowering treats it as a call —
a checker↔lowering disagreement. It is latent only because the compiler's own
sources annotate fn-value locals (the self-compile fixpoint never hits it). The
fix must align the checker's fn-value inference with the `const_fns` lowering
(deep, byte-identity-risky — touches the #2954 const-accessor semantics), so it
is a focused follow-up, not part of the match-recovery work.

**The naive fix is INSUFFICIENT — attempted 2026-07-29, reverted.** The obvious
move is to widen `parser.infer_fnvalue_locals_module`'s `fnv_rewrite_stmt`
(#3640 slice B.2) from its struct-return gate to *any non-callable* return, so
`var f = g` (g called, g's return not a fn) is annotated `type_name: "fn"` and
the existing #3574 fn-value-bind path emits `const_func`. This DOES fix the
SIGSEGV for scalar returns (`var f = g; f()` → 42), and correctly preserves the
const-accessor (`var x = getC; x + 1` → 100, not called so not annotated) and
closure-return (callable return excluded) cases. But it MISCOMPILES a
string/array/Option-etc. return whose result is method-chained: `var f = g;
f().len()` (g: string) returns 0, not 2 — the fn-value CALL dispatches correctly
now, but the call RESULT's type is not recovered, so `.len()` mis-dispatches
(and `i32`'s `f().to_string()` disagrees native-vs-IR too). So the dispatch fix
and the call-result type recovery are TWO problems; widening the annotation
gate alone regresses the chained-method cases. A correct fix needs the general
fn-value-call return-type recovery first (the same class as the `match (f())`
scrutinee recovery, generalised to every use position), then the annotation
widening on top.

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

### At-scale inventory: who actually depends on the fallback (2026-07-29)

The figures below this heading came from the old hand-patch recipe on a run that
hit a 60-minute timeout and had to report its count as a lower bound. With the
flag the sweep is cheap enough to run properly, **paired with a control run**:

```
RE=$(paste -sd'|' shard-list)
                     go test ./internal/e2eselfhost -run "^($RE)$"   # control
FERN_STRICT_IR=1     go test ./internal/e2eselfhost -run "^($RE)$"   # strict
```

The control is not optional. Six of the ten failing parents are wasm and two are
arm64-under-qemu, and CLAUDE.md warns qemu legs are the flaky part of a local
sweep — without the control, a pre-existing local failure is indistinguishable
from a fallback dependent. Measured on shard 0 of 8
(`scripts/selfhost-shard-tests 0 8`, 137 tests / 1020 subtests):

| run | pass | fail |
|---|---:|---:|
| control | 1020 | **0** |
| `FERN_STRICT_IR=1` | 944 | **76** (66 leaves, 10 parents) |

A clean control means every failure is caused by the flag, so all 66 are genuine
AST-fallback dependents:

| cluster | leaves | |
|---|---:|---|
| `TestSelfHostRcOptionBoxWasm` | 27 | wasm rc/leak suites — by far the largest single block |
| `TestSelfHostRcStrBoxWasm` | 16 | |
| `TestSelfHostStructGenEnumFieldWasmIR` | 7 | generic ENUM fields, wasm |
| `TestSelfHostGenStructFieldGenStructWasmIR` | 5 | generic STRUCT fields, wasm |
| `TestSelfHostIoErrorIRWasm` | 5 | |
| `TestSelfHostRcConstructContainersX86_64` | 3 | the only x86 cluster |
| `TestSelfHostStdTestE2EArm64` | 2 | |
| `TestSelfHostSortArm64`, `…PerModuleWholeCompilerX86_64` | parent-level | the whole-compiler one is the known over-budget bootstrap |
| `TestSelfHostWasmComponentIRPath/clock-tostring-falls-back` | 1 | **not a gap** — the case was named for, and asserted, an AST fallback. Closed 2026-07-29 (#5826): wide `.to_string()` lowers, so the case is now `clock-tostring` and asserts IR |

**61 of the 66 leaves are wasm.** The AST fallback is far more load-bearing on
the wasm IR path than on x86, which fits `wasm_ir_deferrals_ok` sitting as an
extra gate above the shared eligibility — but the *size* of the gap was not
previously quantified.

#### Correction: 46 of the 66 are ONE builtin, not 46 gaps

The first version of this section read the rc/leak block as a cluster of wasm
gaps. It is not. Running one of its programs against the driver directly — which
is the only way to see the *reason*, per the note below — names it outright:

```
FERN_STRICT_IR: main (call to unknown symbol __fern_rc_underflow_count)
```

There are two source spellings of the same debug builtin. `irlower` knows
`__rc_underflow()`; the AST emitters know `__fern_rc_underflow_count()`. **The
entire rc/leak corpus — 25 files — uses the AST-only one**, so every one of
those programs bails. That covers `RcOptionBoxWasm` (27), `RcStrBoxWasm` (16)
**and** the x86 `RcConstructContainersX86_64` (3): 46 of the 66 leaves, one
cause. Deleting the call from a failing program makes it lower; re-spelling it
`__rc_underflow()` makes it lower **and** produce the identical answer on both
backends.

The consequence is the part that matters for #3457. Those 25 files exist to pin
**refcounting**, and every one of them has been exercising the AST emitter — on
x86-64 not even the production route. The suites that are supposed to guard
Perceus behaviour are guarding the backend being retired. Whatever else happens,
that has to be fixed before the AST emitters can go, or the corpus loses its
subject at exactly the moment it is most needed.

The genuinely feature-level wasm gaps in the sample are much smaller than the
raw count suggested: generic struct / enum FIELDS (12 leaves) and
`read_file`/IoError (5), both confirmed to bail with the builtin absent.

#### What happens if you just alias the spelling (measured, and NOT landed)

Accepting `__fern_rc_underflow_count` as an alias of `__rc_underflow` in
`irlower` is a one-line change, and it does what you would hope on x86-64:

| | result |
|---|---|
| x86-64 rc suites, under `FERN_STRICT_IR`, `-count=1` | **64 pass, 0 fail** |
| wasm rc suites, same | 172 pass, **16 fail** |

So **IR-path refcounting is correct on x86-64 for everything that corpus
covers** — the corpus simply never checked it. The wasm failures split two ways,
and the split matters:

- **4 leaves still bail** (`option-alias-clean`, `freelist-reuse`,
  `freelist-distinct-class`, `reclaim-large-block`) — they call *other*
  unlowered debug builtins, so the same class of problem one level down.
- **7 `*-retained` leaves return exactly +1.** Every one.

The +1 is **not an over-release**, which is what it looks like and what I first
wrote down. Those programs call TWO debug builtins, and splitting them settles
it — but the split has to be done carefully. Deleting one builtin's call changes
the program's liveness, so the buffer's rc at the surviving call is no longer the
rc under test; two probes built that way agreed with each other and with the
conclusion below, but neither actually measured it.

The faithful form keeps every statement and every use, and changes only which
value is returned:

```fern
var ua = __fern_rc_is_unique(a);
var uf = __fern_rc_underflow_count();
var t  = ua + both[0][1] + both[1][0] + uf;   // all uses preserved
if (t > 1000) { return 99; }                  // t stays live
return ua;                                     // ... or uf
```

| returned | AST path | IR path |
|---|---|---|
| `__fern_rc_underflow_count()` | 0 | **0 — agree**, no over-release |
| `__fern_rc_is_unique(a)`, `a` retained in `both` | 0 | **1 — differs** |

Baseline, on a driver built WITHOUT the alias: the program gives **5 on both
invocations**, because `-ir` refuses to lower it and falls back. With the alias
it lowers and gives 6. So the difference is real and the alias is what exposes
it — not a pre-existing failure.

So the property the corpus is named for holds on the wasm IR path; what differs
is `__fern_rc_is_unique` reporting a container-retained array as unique, where
the AST path reports it shared. That is consistent with wasm arrays having no rc
header (`[len@0, cap@4, elems@8]`) and `arr_share_inc`/`_dec` being intercepted
as no-ops there — an alias-retain accounting difference between the two wasm
backends.

##### How much does that matter? Less than it first looks — checked, not assumed

`__fern_rc_is_unique` is not only a debug counter: it is the **runtime
uniqueness guard for constructor reuse** (#4350), whose stated job is to make a
future hole in the static escape walk *"DEGRADE (fresh box) instead of
corrupting (in-place write over a shared box)"*. A guard that wrongly answers
"unique" is a guard that has stopped guarding, so the obvious next inference is
that wasm IR has a reuse-safety hole.

**That inference does not survive checking.** The guard's subject is a
struct-update literal — `d` is a STRUCT. On a container-retained struct the two
wasm paths *agree*:

| shape | AST | IR |
|---|---|---|
| array retained in an array (`var both = [a, b]`) | 0 | **1 — differs** |
| struct retained in an array (`var keep = [d]`) | 1 | 1 — agree |

and agreeing on 1 is plausibly correct there, since the struct is copied into the
array rather than aliased. So the disagreement is specific to arrays, where the
element genuinely is a pointer alias — and arrays are not what the reuse guard
gates.

Stated precisely: this is a real backend disagreement about alias-retain
accounting for arrays, **whose safety impact is unestablished**. It is not
demonstrably reachable by constructor reuse, and nothing here shows a live
corruption.

##### Reachability: hunted, not found

The obvious way for a missing retain to bite is a free-while-still-referenced:
the container keeps a pointer, the original binding's sweep frees the buffer.
Four shapes designed to trigger exactly that, all **real programs with no debug
builtins**, all against the interpreter as oracle:

| shape | interp | wasm AST | wasm IR |
|---|---|---|---|
| container escapes the function (`return both`) | 55 | 55 | 55 |
| original rebound after the store (`a = [99, 99]`) | 22 | 22 | 22 |
| loop-scoped element appended each iteration | 22 | 22 | 22 |
| rebind followed by allocation churn (would reuse a freed block) | 21 | 21 | 21 |

**No miscompile.** That is a negative result, so it bounds rather than proves —
but it is four independent attempts at the specific failure the missing retain
predicts.

##### Settled: the array is ALIASED, so the IR answer is the wrong one

An earlier revision of this section floated the opposite — that if the array were
**moved** into the container, the container would hold the only live reference,
rc would genuinely be 1, and the IR answer of 1 would be *correct* with the AST
path over-counting. That reading is **disproven**, and the test is one line:

```fern
var a: i32[] = [11, 22];
var both: i32[][] = [a];
return a[1] + both[0][1];        // 44 — BOTH references readable
```

44 on the interpreter and on the wasm IR path. `a` is still live after the
store, so `a` and `both[0]` are two simultaneous references to one buffer: an
**alias**, not a move. Two live references means `__fern_rc_is_unique(a)` must be
**0** — the AST path is right and the wasm IR path is wrong.

Copy-on-write is unaffected and correct (`a = a.with(1, 50)` then
`a[1] + both[0][1]` gives 72 on both wasm paths — the container's view is
untouched), which is why the reachability probes above find no miscompile: the
operations that would corrupt do not consult this answer. The
**constructor-reuse guard does**, so the degradation is real even while
currently unreachable.

So the job is to **fix the IR path** — retain on a container store — not to
adjust the 7 wasm expectations, which are correct as written. That is the
opposite of what the previous revision suggested; it was written before this
probe existed.

##### It is NOT a wasm issue: both IR paths are wrong, both AST emitters right

Every revision above framed this as a *wasm backend disagreement*, because wasm
is where the failing tests live. Running the same probe on x86-64 shows that
framing is wrong:

| path | `is_unique(a)` after `var both = [a, b]` |
|---|---|
| x86-64 AST | **0** — correct |
| wasm AST | **0** — correct |
| x86-64 IR | **1** — wrong |
| wasm IR | **1** — wrong |

**The IR lowering does not retain an array stored into a container, on any
backend. Both AST emitters do.** So this is a shared IR-level gap, and it is live
on x86-64 — the production route — not a wasm quirk.

The x86 rc corpus passes only because it happens to contain no `is_unique`
-on-container case: `TestSelfHostRcConstructContainersX86_64/array-of-arrays` is
the same shape as the failing wasm row but calls the underflow counter *without*
the uniqueness check. So the 64/64 x86 result quoted above is real but does not
cover this; the wasm suite is simply the only place the question is asked.

That makes the constructor-reuse guard (#4350) degraded on the IR path generally.
No live corruption is reachable from the probes above — copy-on-write does not
consult it — but the defence-in-depth it exists to provide is not being provided
on either backend.

**The alias is therefore reverted, not landed**: it would turn those 7 wasm
cases red, and hiding them behind a skip would bury a real backend disagreement.
Landing it needs the `is_unique` disagreement resolved first. The x86 half is
already clean, so whoever picks this up gets 64 subtests of IR-path rc coverage
essentially for free once wasm agrees.

##### The array row is fixed; the remaining 6 are NOT the same gap

The array-into-array leaf is closed (#5861): the IR array-literal lowering now
alias-incs a bare-ident array element, so `is_unique` reports a
container-retained array as shared on both IR backends, matching both AST
emitters. The other 6 `*-retained` rows — array/string/tuple into a TUPLE, tuple
into an array, string into an array/Option — were reverted with it, and they are
**not** the same missing retain one level over. Tuple construction is explicit
that storing the pointer with no alias-inc is deliberate: `op_tuple_make_k`
leaves the box leak-mode and excludes the source slot from the exit sweep
(`returned_moved_arr_slots`), citing #4598 — a use-after-free from getting
exactly that interaction wrong. Two coherent pairings exist here, and each is
balanced on its own terms:

| pairing | construction | release | used by |
|---|---|---|---|
| (A) | alias-inc | container deep-drops the element | struct fields, array literals |
| (B) | no inc (move) | container deep-drops the element | tuple elements |

Adding incs across the (B) sites without moving them to (A) wholesale is how
#4598 comes back.

##### Before adopting (A) anywhere else: native's (A) LEAKS in a loop body

Which pairing to converge on is not a free choice, because the AST/native answer
the 6 rows encode — `is_unique == 0`, i.e. pairing (A) — is measurably leaky on
the reference implementation. `FERN_LEAKCHECK=1` on x86-64 at HEAD `9e27eda5`,
100 000 iterations of a loop body that builds a container from a bare ident:

| shape | allocs | frees | live_bytes |
|---|---|---|---|
| array into array | 300 000 | 300 000 | **0** |
| string into array | 100 000 | 100 000 | **0** |
| string into tuple | 100 000 | 100 000 | **0** |
| array into tuple | 200 000 | 100 001 | 3 199 968 |
| array into Option | 200 000 | 100 001 | 3 199 968 |
| tuple into array | 200 000 | 100 000 | 1 600 000 |
| tuple into tuple | 200 000 | 100 000 | 1 600 000 |
| string into Option | 100 000 | 0 | 3 200 000 |

Linear in the iteration count — 1 000 / 10 000 / 100 000 iterations give
31 968 / 319 968 / 3 199 968 live bytes — so it is unbounded growth, not a fixed
overhead. Sizing the array up from 3 to 20 elements moves the per-iteration
figure from 32 to 96 bytes, which identifies the leaked block as **the array**,
not the container box.

Three shapes isolate the cause, and only one line differs between the first two
(`examples/probes/loop_construction_move_leak.fern` runs all three; the figures
below are the pre-fix ones, and all three are balanced now):

| shape | result |
|---|---|
| `var xs = [1,2,3]; var t = (xs, 99);` in a loop body | allocs=6 frees=4 **live_bytes=64** |
| `var t = ([1,2,3], 99);` in a loop body | allocs=6 frees=6 live_bytes=0 |
| `var xs = [1,2,3]; var t = (xs, 99);` at top level | allocs=2 frees=2 live_bytes=0 |

A bare ident at its last use is *supposed* to be moved into the construction —
the inc is skipped and `__drop_tuple` releases the element, which is pairing (B)
— but `markConstructionMoves` only walks the function's **top-level**
statements ("The caller has already established the dominance guards (top-level
statement, no preceding return)"). Inside a loop body the move never fires, so
the alias-inc is emitted and nothing releases the source's own reference per
iteration. Reading the lowered IR confirms it exactly: the loop body emits
`__fern_rc_inc` on `xs` and `__drop_tuple_…` on the previous iteration's tuple,
whose `__fern_arr_dec` takes the array 2 → 1 and never frees it; the function
exit emits the flat `__fern_rc_dec` on `xs` that the loop body is missing, which
is why only the *final* iteration is reclaimed and the leak is (n−1), not n.

The existing churn tests do not catch this because they build the container in a
called function (`tuple-array-churn-clean`'s `mk()`), and a function-scoped
construction is reclaimed at that function's exit. The leaking shape — construct
in the loop body itself — is untested on every backend.

So the conclusion the previous section reaches ("fix the IR path — retain on a
container store") holds for the array row that landed, but **must not be applied
mechanically to the other 6**: for the tuple/Option rows it would import
native's leak rather than fix a self-host gap. The IR path's `is_unique == 1`
there corresponds to the move-on-construction behaviour that is leak-free. The
order of work is: fix the native move-on-construction dominance gap first, then
re-ask what those 6 rows should assert.

##### Fixed where the construction is the source's last use; two causes remain

`markLoopBodyConstructionMoves` (#5879) extends move-on-construction into loop
bodies under three guards the top-level walk does not need: the ident must name
a var declared **earlier in the same body** (one declared outside the loop lives
across iterations, and moving it would be a use-after-free rather than a leak),
the body must contain no `return` / `break` / `continue` (which would make the
construction conditional), and the name must be declared exactly once in the
function (`moved` is name-keyed). Array-into-tuple is now balanced at 100 000
iterations — allocs=200 000 frees=200 000 live_bytes=0, from 3.2 MB leaked.

The shortcut that looks equivalent is **not**: having the loop-body
re-declaration drop (`emitVarReinitDropOld`) mirror the exit sweep instead of
bailing on `!freeEligible` would release an alias-WITHOUT-inc loop var
(`var a1: i32[] = a0;`, measured clean at allocs=1 frees=1) once per iteration
and over-release the shared buffer. That shape is now a fixture in
`TestX86_64LeakCheckLoopConstructionMove` precisely so the shortcut cannot be
reintroduced silently.

**Correction — the per-shape table that stood here was wrong.** It claimed the
Option rows leak because `markConstructionMoves` "has no `*ast.Call` case, so an
enum-variant construction is never a move site". There **is** an `*ast.Call`
case (`rc_analysis.go`, the Slice-1b variant-constructor arm, gated on
`lookupVariant` + `enumRcPayloadsEligible`), and after #5879 the move does fire
for `Some(xs)`: `movedLocals: xs`, `moveSites` set. The original table was read
off the eight-shape sweep, where the *container type* happened to correlate with
the real discriminator; it does not.

The real discriminator is **whether the construction is the source's last use**,
and it cuts across container kinds. A controlled pair at 1 000 iterations —
identical but for what the loop body reads afterwards:

| shape | reads after | result |
|---|---|---|
| `var o = Some(xs);` | `1` | allocs=2000 frees=2000 live_bytes=0 |
| `var o = Some(xs);` | `xs[1]` | allocs=2000 frees=1001 **live_bytes=31968** |
| `var o = (xs, 99);` | `o.0[2]` | allocs=2000 frees=2000 live_bytes=0 |
| `var o = (xs, 99);` | `xs[1]` | allocs=2000 frees=1001 **live_bytes=31968** |

Option and tuple behave identically. Reading the source *after* the construction
makes it genuinely aliased, so the inc is correct and the move is not available
— and nothing releases the source's own reference per iteration. That is the
same asymmetry #5879 fixed, in the one configuration where a move cannot be the
answer.

So there are exactly **two** remaining causes, not four shapes:

**A. Source read after the construction.** The inc is right; the missing half is
a per-iteration release of the alias-inc'd source. This is precisely where
`emitVarReinitDropOld` bails on `!freeEligible`, and precisely why the shortcut
above is unsafe — the fix has to distinguish an alias-inc'd source from an
alias-WITHOUT-inc one, which is what the two shapes look like from the drop
site.

**B. A tuple-VALUED element.** `var t = (3, 4); var c = [t];` leaked
allocs=2000 frees=1000 live_bytes=16000 **whether or not** `t` is at its last
use — last-use is irrelevant because neither path released it. Drop-side,
independent of the move analysis.

The **array** half is now fixed. `arrElemStructDropName` gated the generated
per-element walk on `tupleNeedsDrop`, which is false for a tuple of plain
scalars — correctly, since such a tuple's drop body has no element to traverse.
But the tuple BOX still has to be freed, and the flat fallback
(`__fern_drop_arr_ptr`, a per-element `__fern_rc_dec`) only decrements: freeing
needs the size, which only the generated `__drop_arr_tuple_<mangled>` loop
supplies. Hence exactly one leaked box per iteration — 16 bytes for
`(i32, i32)`, 8 rc header + 8 payload, which is what identified it. Dropping the
`tupleNeedsDrop` condition (the registry check stays) routes every tuple element
through the generated walk; `[t]` is now allocs=2000 frees=2000 live_bytes=0.
Pinned by `TestX86_64LeakCheckTupleElemArray` + the arm64 leg, with a
string-carrying tuple as the control (already covered, since `tupleNeedsDrop` is
true there).

The **tuple-in-tuple** sibling is now fixed too. `dropFnNameFor`'s `TupleType`
arm carried the identical `tupleNeedsDrop` bail, so a tuple-valued ELEMENT of a
generated `__drop_tuple_` fell through to a flat dec that decrements the inner
box's rc but never frees it (freeing needs the size, which only the generated
body carries). Removing that bail routes every tuple shape to `genTupleDropFn`,
whose body for a scalar tuple emits no element drops — just the
`is_unique`-gated `box_free`, which is precisely the free that was missing.
`(t, 99)` and a `(i32, i32)` struct field both go from `100 / 50 / 800` to fully
balanced. With both gates gone `tupleNeedsDrop` has no callers, so it is
**deleted**, along with its row in the `rc_caps.go` capability table and the
now-false claim in `genTupleDropFn`'s header that it "assumes at least one
element drop is emitted".

An earlier revision of this section justified a narrower fix (two targeted call
sites, gate retained) with the claim that widening `dropFnNameFor` "broke the
self-host interp driver outright". **That was a misread and is retracted.** What
happened: `test-e2e-selfhost-x86_64-shard11` came back red with
`TestSelfHostInterpDriverX86_64` reporting every program `exited -1` and
`default-multi` at 1080 s. The x86 self-host shards are
`continue-on-error: true` by design (see the RUNNER PREEMPTION note in
`.github/workflows/test-e2e-selfhost.yml`) precisely because they are
intermittently reclaimed or time out mid-run against the 20-minute job cap; the
`-1`s are harness teardown after the wall, not 15 independent failures. The same
shard failed identically on the narrowed commit, and running
`TestSelfHostInterpDriverX86_64` locally against the WIDE version passes in
165 s with 0 failures.

Two lessons worth keeping. **The aarch64 self-host shards are the strict signal**
— the workflow says so outright ("the aarch64 shards run the IDENTICAL test set
… and stay strict — so a real self-host regression still turns this workflow red
on aarch64"), so read those before concluding anything from an x86 shard. And a
red check that agrees with a hypothesis you already hold is exactly when to
verify it: the flake looked like confirmation of a blast-radius worry about
`dropFnNameFor`'s twelve call sites (the Map value drop glue whose tag the
runtime reads, closure capture thunks, vtable drop routing), and that coincidence
cost a full rewrite.

Both were found by probing the *mechanism* rather than re-reading the sweep: the
first table inferred cause from which tests were red, which is how the container
type got mistaken for the discriminator. Dumping `movedLocals` / `moveSites` and
then building a controlled pair that varies one line settled it in two runs.

Two caveats on reading this. It is **one shard of eight**, so it is a sample, not
the whole suite. And a test failing here means its program bails *somewhere* —
not that the feature in its name is unsupported; an incidental helper can be the
cause.

Practical note for the next sweep: the harness captures driver **stdout** only,
so the refusal message never reaches the test log — you learn *which test*
bails, not *which bail*. To get the reason, run the driver directly on the
program (`FERN_STRICT_IR=1 <driver> < prog.fern`). This is the same
which-function-not-which-site limit recorded above, one level up.

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

### CLOSED: a ZERO-ARG named fn in an fn-typed TUPLE element (found + fixed 2026-07-29)

Differential-probed against the interpreter oracle (x86-64 IR path, `-ir-probe`
confirms `module: IR` for every row — these are IR miscompiles, not AST gaps):

| program | interp | self-host |
|---|---:|---:|
| `var t: ((() => i32), i32) = (a1, 4); t.0() + t.1` — `a1(): i32` | 7 | **SIGSEGV** |
| same, but `a1(x: i32): i32` (ONE param) | 8 | 8 |
| `var xs: ((() => i32), i32)[] = [(a1, 4)]; xs[0].0() + xs[0].1` | 7 | **SIGSEGV** |
| same, one param | 8 | 8 |
| `(() => 3, 4)` — a LAMBDA element | 7 | 7 |
| `[(() => 3, 4)]` | 7 | 7 |

So the split is **arity of the named function**, not tuple nesting: a lambda
element is always wrapped, a named fn with params is wrapped, and a ZERO-ARG
named fn is not.

The cause is the guard in the lift's `ExprTuple` arm, which wraps a bare ident
element only when the module fn has `params.len() > 0`. Its comment states the
reason: *"a zero-arg receiver-less fn is a `const` desugar whose VALUE must
flow"*. That is a REAL constraint, not an oversight — Fern desugars `const X =
expr` into a zero-arg function, so a bare `X` in expression position means
call-it, and wrapping it would break every const read. The self-host defaults a
bare zero-arg name to the const-call (see parser.fern's #3640 slice B.2 note on
exactly this ambiguity).

But the element's DECLARED type resolves the ambiguity: `((() => i32), i32)`
says element 0 is a fn VALUE, so no const-call reading is possible. The tag side
already knows this — `tuple_elem_tags` maps an "fn" segment to `"clo"`, which is
why the call dispatches env-first — and it is the VALUE side that disagrees: the
element holds the const-CALL result (an `i32`), and dispatching env-first on it
treats `3` as a box pointer.

**FIXED.** The value side now agrees with the tag side that already assumes a
box — and without threading a type through the shared expression walker, which is
what made this look expensive. `StmtVar.type_name` is already in hand at the
binding, so `wrap_ann_fn_tuple_elems` runs there as a pre-pass: for an ANNOTATED
tuple (or annotated array of tuples) it boxes exactly the elements whose declared
segment is a fn (`tuple_type_elem_tag(...) == "clo"`) and whose value is a bare
unshadowed zero-arg module fn. Elements with >= 1 param are left to the ordinary
tuple walk that already boxes them, so nothing is double-wrapped.

Restricting it to ANNOTATED tuples is what keeps `const` reads intact: an
unannotated `var t = (K, 4)` has no declared fn segment, so the pre-pass does not
fire, and a bare `K` still reads as the const's value. Pinned by
`TestSelfHostTupleFnZeroArg{IRX86_64,IRWasm}`, which carries four const-read
regression cases (const in a tuple, a plain const read, a const and a fn value in
the same annotated tuple, and an unannotated tuple carrying a const) next to the
crash cases — an over-eager version of this fix breaks those four, so they are the
real gate on it.

The Option/Result sibling found in the same sweep is **also CLOSED**, by the same
fix one container over: `var o: Option[() => i32] = Some(a1)` (and `Ok` / `Err`)
left a zero-arg payload unboxed while the match-arm bind dispatches it env-first.
`wrap_ann_fn_variant_payload` is the variant twin of the tuple pre-pass — `Some`
and `Ok` read the first type argument, `Err` the SECOND, which is the case a fix
that always indexes argument 0 silently gets wrong. One wrinkle worth recording:
a generic ARG is not coarsened to `"fn"` the way a tuple segment is, so the
payload renders either as `"fn"` or as its full `"() => i32"` spelling and the
test has to accept both — the first version of this fix looked correct and did
nothing at all because it only matched `"fn"`.

**Two further positions had the identical split, found by continuing the same
sweep, and are closed the same way:**

| position | evidence used |
|---|---|
| a fn-typed RETURN — `function get(): () => i32 { return a1; }` | `fd.ret_type` |
| a USER-enum variant field — `enum E { Wrap(() => i32) }` + `Wrap(a1)` | the variant's struct decl |

The user-enum one is the cleanest of the family: the variant's field is already
declared `"fn"` in `structs`, which is strictly better evidence than any
annotation, and the walk was ALREADY consulting it — only the shared
`lift_arg_is_fn_value` arity gate stood in the way. Splitting off
`lift_arg_is_fn_value_declared` for positions whose declared type is known to be
a fn is the whole fix there.

So the family is four positions and one cause: a fn value's REPRESENTATION is
decided by whoever writes it, the dispatch is decided by the declared type, and
wherever those two were derived independently they disagreed for exactly the
zero-arg case. Each fix is the same move — read the declared type at the site
that already has it.

The two remaining crashes route **AST**, so by the project rule they need no AST
fix — but they are worth ADMITTING to the IR path, because the four fixes above
would then make them correct rather than crashing. Both are now localised, with
`FERN_STRICT_IR=1` (#5793) naming the bail and minimal contrast pairs isolating
the trigger. No rebuild was needed for any of this, so do not re-derive it:

**(1) `t.0()` on a tuple bound from a MATCH PAYLOAD.**
`FERN_STRICT_IR` says: `main (call to unknown symbol i32.0)` — the callee fell
through to method dispatch on the element's *type*. The contrast pairs place it
exactly:

| program | verdict |
|---|---|
| `Option[(i32, i32)]` → `t.0 + t.1` | IR, correct |
| `Option[((() => i32), i32)]` → `t.1` (read the NON-fn element) | **IR, correct** |
| `Option[((() => i32), i32)]` → `t.0()` (CALL the fn element) | **bails** |
| `var t: ((() => i32), i32) = (a1, 4); t.0()` (a LOCAL, not a payload) | IR, correct |

Row 2 is the informative one: the bound slot's `mark_tuple_elems` tags ARE being
recorded, since reading the non-fn element resolves its width correctly. And row 4
shows the `"clo"`-element CALL path works for a var-bound tuple. So neither the
tagging nor the call lowering is missing in general — what fails is only the
combination, a `"clo"` element call on a slot bound from a match payload. Note the
payload is BORROWED from the Option box (the arm-bind comments say so), where a
var-bound tuple is owned; that asymmetry is the first thing to check.

**(2) A fn element in a NESTED array.** `FERN_STRICT_IR` names only `main`, no
reason. `var g: i32[][] = [[3]]; g[0][0]` lowers fine, so it is not the nesting —
it is the element type, i.e. the array-of-array element classifier
(`mark_arrarr_elem` / `arrarr_elem`) not admitting a fn/`"clo"` element the way
`tuple_elems_lowerable` already admits one.

Both are IR-WIDENING work (goal 1), not AST work, and both are small enough to be
worth doing before reaching for the uniform-representation rewrite.

Positions PROBED CLEAN in the same sweep, so they need no work: a plain struct
field (`S { f: a1 }`), a call argument (`takes(a1)`), a fn-value local
(`var g: () => i32 = a1`), and a fn-POINTER array (`var xs: (() => i32)[] = [a1]`,
which goes through the const_func path instead).

**The sweep was then widened AWAY from fn values and came back empty** (2026-07-29,
22 programs, differential against the interpreter, all routing IR). Recorded as a
negative result so the next probing pass starts somewhere else: nested
struct-in-array field reads, struct update syntax (`S { ...s, b: 5 }`),
slice-of-slice and string-slice chains, `defer`, match guards over enum payloads,
multi-element tuple returns, arrays of tuples, `Option` struct fields, i64
arithmetic, method chaining, closures returned from a function, closures captured
per-iteration in a loop, string building in a loop, struct array-field iteration,
struct-payload enums, and deep recursion.

That is worth stating plainly: the fn-value REPRESENTATION ambiguity was a
concentrated cluster, not a sample of a broadly buggy IR path. Eight miscompiles
came out of it (#5799, #5850, #5865, #5881 and the #5001/#5007/#5009/#5026
closure-dispatch group before them), and everything probed outside it agreed with
the oracle first time.

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
representation uniform so the question disappears.

**Option (a) is DONE.** #5790 supplied the positive evidence on both sides, and
`irlower.check_fn_array_fields` is the pre-emit gate that reports what neither
side proves — wired into `asm_ir_run` and `wasm_ir_run` next to the arity /
undefined-callee gates. Both shapes in the table above now produce
`error: cannot determine whether the fn[] field R.hs holds function pointers or
closures` instead of a SIGSEGV, on both backends.

Two narrowings keep it from rejecting working programs, both pinned by
`TestSelfHostFnArrayFieldGate{X86_64,Wasm}`:
  - only a field this module CONSTRUCTS (the `FNFLD:` marker). A field read here
    but built in a sibling module is not this module's call to make, which matters
    for per-module emit.
  - only a field this module READS THROUGH AN ELEMENT. A bare `.len()` never
    reaches an element, so the representation cannot matter — an unprovable field
    that is only length-queried still compiles.

The resolution is deliberately syntactic (`fnfld_obj_type` over a param/annotated-
var/struct-literal binding map) because the gate runs before any `LowerState`
exists. Anything it cannot resolve yields "" and the gate stays silent, which is
the conservative direction for a rejector: a missed read is the status quo ante,
a false positive would break a working build. Verified against
`TestSelfHostAsmIRPath`, the IR fixpoint, the closure / fnptr / tuple-fn suites,
the wasm IR path, and both stage-2 compilers — no program lost.

Option (b) is still the better end state (it would also close the
cross-representation LOCAL rebind, which no gate can classify), but it is a
representation change across every backend. Until then, an `fn[]` struct
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
| `asm_modload_run.fern:335` (arm64 332) | merged AST default | ~~`TestSelfHostModloadFixpointX86_64`~~ **retired from routine CI (env-gated `RUN_MERGED_FIXPOINT`, #3457 slice 2)** — the x86 whole-compiler self-compile gate now runs `TestSelfHostModloadPerModuleWholeCompilerX86_64` (per-module IR). **CORRECTED (2026-07-29):** this row used to claim "no routine test (x86 OR arm64) exercises the merged default". That was wrong — `TestSelfHostModloadPerModuleWholeCompilerX86_64`'s step 7 drives the per-module-BUILT compiler over the whole compiler source with no flags, which IS the merged default. Retiring the *fixpoints* from routine CI did not retire the merged path from routine CI. Since #3457 slice 3 the driver REFUSES a merged bundle this size unless `-merged` is passed, so the remaining AST consumers are greppable: that smoke run, plus the two env-gated fixpoints (`TestSelfHostFixpointArm64` / `TestSelfHostModloadFixpointX86_64`). The arm64 routine gate is `TestSelfHostModloadPerModuleWholeCompilerArm64` |
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
  `asm_arm64`; the runtime is the remaining gap.) **Second untangle step DONE
  (#3457 slice 4a): the arm64 IR path no longer uses the AST `emit_function` for
  its Fern runtime helpers.** `asm_arm64_ir.emit_ir_runtime_fern_fn` is the
  near-verbatim port of x86 `asm_ir.emit_ir_runtime_fern_fn` (parse the
  `rt_src_*` helper → compute its side-tables → lower via
  `emit_function_via_ir`). Wiring is a mode flag rather than the ~5k-line
  `emit_runtime` duplication first sketched here: a new `EmitState.ir_runtime`
  bit (default false), which `asm_arm64.emit_runtime_fern_fn` reads to route to
  the IR variant, set true by BOTH arm64 IR entry points (`emit_module_ir` /
  `emit_module_ir_unit_arm64`) and left false on the AST fallback. This is
  **behaviour-changing on arm64, NOT byte-preserving** (AST `emit_function` and
  IR `emit_function_via_ir` select different instructions for the same helper),
  so the byte-identity gate does not apply; it is validated by x86 byte-identity
  (the shared `asmcore` field is inert on x86 — no x86 file reads `ir_runtime`)
  plus the arm64 self-reproduction fixpoint + e2e on CI (which still hold, since
  gen0 and gen1 both emit via IR). **Remaining for deletion (slice 5):** the
  IR path still *calls* `asm_arm64.emit_runtime` (it reuses the hand-written
  helper bodies), so `asm_arm64.fern` cannot be deleted until `emit_runtime`
  itself moves to `asm_arm64_ir.fern` — now a mechanical move, since the AST
  `emit_function` coupling it carried is gone from the IR path.
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
    `TestSelfHostModloadPerModuleWholeCompilerArm64` its routine gate.

    **CORRECTION (2026-07-29).** This bullet used to end "so **no routine test
    (either backend) now compiles the whole compiler through the merged AST
    emitter**". That did not hold: step 7 of
    `TestSelfHostModloadPerModuleWholeCompilerX86_64` runs the per-module-BUILT
    compiler over the whole compiler source with no flags, which is exactly the
    merged default. Retiring the *fixpoints* from routine CI retired the fixpoints,
    not the merged path. Found by making the driver refuse that bundle (slice 3,
    below) and watching which tests needed the `-merged` opt-out.
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
  which is why the batched `-per-module-emit-all` exists.

  **The per-module-or-error swap was ATTEMPTED and REVERTED (2026-07-29). Its
  premise — "only the whole-compiler self-compile lands past 1500" — is FALSE.**
  The refusal was implemented (error past both bounds, `-merged` opt-out, both
  halves pinned by a test) and CI broke on two tests that are neither the
  bootstrap nor a fixpoint — `TestSelfHostStage2Compiler` and
  `TestSelfHostStage2Bootstrap`, on different shards. Both build a **stdin-driven
  Fern compiler** out of the library modules (`lexer` + `parser` + `asm` and their
  closure) and compile a table of small programs with it. Measured: **1958 merged
  functions**, past both bounds, compiling correctly through the AST emitter
  before the change.

  So the merged AST emitter is still load-bearing for ORDINARY large programs, not
  just for the bootstrap. Refusing by function count regresses any user program
  whose merged closure crosses 1500 — the count cannot distinguish "the compiler
  compiling itself" from "a big program". Adding `-merged` to that test too would
  have kept CI green while leaving the regression in place for real callers, which
  is why the change was reverted rather than patched.

  **What has to happen first:** the per-module rescue must COVER the >=1500 band
  before the fall-through can become an error. Today it is bounded `< 1500`
  because `emit_per_module_concat` runs single-process and a concat that large
  OOMs — so the batched path (`-per-module-emit-all`, which already exists and is
  self-reproducing) has to become reachable from the ordinary compile path, not
  just from a driver flag the test harness passes. That is the real slice-3 task;
  the error swap is the step AFTER it, not instead of it.

  **CORRECTION (2026-07-29): the `< 1500` cap is NOT the OOM the paragraph above
  says it is.** Raising it to 2500 and running `TestSelfHostStage2Compiler` (1958
  merged funcs) produces **21 duplicate-symbol errors and ZERO memory
  indicators** — no `signal: killed`, no exit 137, no allocation failure. The
  17.6 MB stream assembles fine once the symbols are fixed. So the batched-emit
  work this paragraph scopes is aimed at a wall that isn't there at 1958 funcs.
  Two real blockers sit behind that bound instead, and they are different from
  each other:

  1. **A symbol collision in the concat's dedup — FIXED.** A unit emits four
     kinds of one-per-PROGRAM `.weak` symbol: `__fern_shp_<T>`,
     `__fn___struct_drop_<T>`, `__fn___struct_arr_elems_drop_<T>`,
     `__fn___field_reclaim_<T>`. `dedupe_shape_defs` special-cased the first and
     dropped exactly ONE following line — correct for a shape (the two-line
     `.weak SYM` / `SYM: .ascii …` pair), useless for the other three, which are
     multi-line function BODIES. All 21 errors were those helpers (and their
     `.Lstd_*` / `.Lsaed_*` local labels) for three `parser__` struct types the
     stage-2 compiler's units share. Replaced by one uniform rule — on a
     duplicate `.weak SYM`, skip the whole definition, stopping at the next
     column-0 non-`.L` line — which deletes the special case rather than adding
     to it, and is renamed `dedupe_weak_defs` since it was never shape-specific.
     **Soundness checked, not assumed**: extracting every duplicate definition
     from the emitted stream shows 13 duplicated symbols and **0 whose copies
     differ**, so keeping the first loses nothing. (That mattered: unlike an
     interned shape string, a struct-drop BODY depends on the emitting unit's
     struct-decl view, so identical copies is a property to verify rather than
     take for granted.)
  2. ~~**A runtime segfault in the built compiler — OPEN, and the actual gate.**~~
     **RETRACTED (same day): that segfault was a bug in blocker 1's own fix, and
     it is fixed.** The emitter indents its DIRECTIVES, so a unit's prologue is
     `    .globl _start` / `    .text`. The new skip dropped ANY indented line, so
     a duplicate `.weak` at the tail of one unit's shape block swallowed the next
     unit's prologue — putting the entry unit's code inside the previous unit's
     `.rodata` and leaving `_start` a LOCAL rodata symbol (`nm`: `r _start`). ld
     then could not find an entry ("cannot find entry symbol _start", defaulting
     to 0x401000) and the binary jumped to garbage: RIP=**0x1** with the initial
     argv/envp stack untouched, i.e. no real code had run. Corrected stop rule: a
     body line is an INSTRUCTION (indented AND not starting with `.`) or a `.L…`
     label; anything else, including an indented directive, ends the skip.

     Measured by hand at 1958 merged funcs after the correction:

     | | before | after |
     |---|---|---|
     | `_start` linkage | `r` (local, rodata) | **`T` (global, .text)** |
     | link | `cannot find entry symbol _start` | clean |
     | stage-1 compiler | segfault, 0 bytes | runs, 302 bytes |
     | stage-2 program | — | **exit 7** |

  3. **The whole-compiler self-compile IS still memory-bound — so the OOM story
     is half right, for a different program.** With the bound raised past it,
     step 7 of `TestSelfHostModloadPerModuleWholeCompilerX86_64` (the
     per-module-built compiler over the whole compiler source, no flags) routes
     into a single-process concat and **exits 137** — `__fern_alloc`'s arena
     bounds check, not a host OOM-kill. So blocker 3 is the real remaining gate,
     and the batched-emit reachability plan above is the right fix for it.

  **Correcting this section's own correction:** "the cap is NOT the OOM" was
  measured on the **stage-2 compiler (1958 funcs)**, which has no memory problem,
  and over-generalised from it. Function count is a poor proxy either way — 1958
  fits and ~2040 does not, because what exhausts the arena is the COMPILE
  WORKLOAD, not the emitted function count.

  **So the bound stays at `< 1500`** and the dedup + prologue fixes land on their
  own: they are a live latent bug at ANY size (a program in the current 512..1500
  band whose modules share a struct type hits the same assembler error today) and
  they are necessary but not sufficient. What survives, and is genuinely narrower
  than the original framing: only the WHOLE-COMPILER case needs the batched path;
  ordinary large programs up to at least 1958 funcs now work through the concat.

  **What the attempt did establish**, and what survives it: exactly three callers
  needed the opt-out, and one is a ROUTINE test — step 7 of
  `TestSelfHostModloadPerModuleWholeCompilerX86_64` drives the per-module-built
  compiler over the whole compiler source with no flags. Two places in this
  document claimed no routine test exercised the merged default; both are
  corrected above. So the merged AST emitter has at least THREE live non-bootstrap
  consumers on x86 — that smoke run plus both stage-2 compiler tests — where the
  docs previously implied it had none.

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

**RESOLVED, the drop-ORDER half (2026-07-29) — read "The drop order half,
solved" below before anything else in this section.** Cause 2 ("drop ORDER")
was never a cause: it was the visible symptom of a one-reference under-count
in `computeConsumedParams`, which excluded ENUM (union) params from the
consumed-threaded promotion while their reassignment still emitted the
overwrite dec. Admitting `ast.EnumType` there fixes it; every claim below
about the sweep's order being load-bearing, and about reverse declaration
order being "independently unsound", is superseded. The LEAK half (cause 1,
the escape taint) is still open, and the load leak this section set out to
explain is unchanged by the fix.

**Four leak shapes characterised, two causes found, and both fixes proven
UNSOUND (2026-07-29). The LEAK half is still unfixed — read this before trying
again; the ORDER half is answered by the header above.**

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
change. The last row was re-run with nothing else on the machine (0/15 GB used)
to rule out contention; CLAUDE.md's note that arena exhaustion reports exit 137
rather than SIGSEGV also argues against an OOM reading. (The reading this row
was given at the time — "reverse declaration order is independently unsound" —
is WRONG, and "The drop order half, solved" below says why: the reversal only
exposed an under-count that the forward order left as a silent leak. With that
under-count fixed, an all-functions reverse sweep is behaviourally inert.)

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
(Superseded by the parse-only probe in "The drop order half, solved": a
lexer+parser+util+astwalk driver rebuilds in ~3 s, so the DRIVER BUILD stops
being the bottleneck too — and it detects the corruption in the AST directly
rather than through the emitted code.)

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

**Small-program extraction has now failed five times.** (It succeeded on the
seventh, once the search moved from the destructuring function to its CALLEE —
see "The drop order half, solved".) Neither a struct holding
an owned array, nor that shape split across mutually-exclusive branches, nor a
`var (a, b) = f()` destructuring of a `(string, i32[])` diverges between forward
and reverse — all match their interp oracle with identical leakcheck totals. The
effect needs `parse_primary`'s scale, so the next attempt should bisect WITHIN
the function (drop-index ranges rather than function-name buckets) instead of
trying to write the small program from the outside.

**Narrowed again: the order of exactly TWO drops.** Bisecting WITHIN
`parse_primary` — reverse only the `[lo,hi)` slice of its 58-entry sweep, keep
the rest forward, then binary-search each edge — converges in twelve builds:

    [0,58) FAILS -> [29,58) FAILS -> [43,58) passes -> [36,58) FAILS
    -> [38,58) FAILS -> max LO = 38; then [38,48) -> [38,43) -> [38,40) FAILS,
    [38,39) passes  =>  MINIMAL FAILING WINDOW = [38,40)

Two adjacent drops, and they are the predicted pair:

    [38] __destruct_1751_17  eligible=true   (Expr, Par)  <- hidden tuple, deep walk
    [39] inner_expr          eligible=false  Expr         <- its component, plain dec

Swapping ONLY those two miscompiles the compiler. Everything else in the sweep,
and every other function, tolerates reversal. The source site is
`parser.fern:1751`:

    var (inner_expr, inner_p) = parse_expr(p);
    ...
    elems = elems.append(inner_expr);

which is what ties the two causes together: `append(inner_expr)` is a
direct-Ident source at an INC-ing sink, so the **escape taint** is what makes
the component ineligible, and that ineligibility is what makes the **drop
order** matter. They are not two independent bugs.

(Half right. The taint IS what selects the plain dec, but the reason either
order can be wrong is the under-count established in "The drop order half,
solved" — and it is inherited from the OTHER branch of this same `if`, the
`return parse_postfix(p, inner_expr)` at line 1767, not from the `append` two
lines up.)

**A 15-line repro of the leak, with attribution** —
`examples/probes/destructure_taint_leak.fern`. It carries the identical
eligibility signature (`__destruct` eligible tuple at [0], tainted component at
[1]) and leaks 1500 of 3500 allocations (48000 bytes live) while exiting 4, the
same as the interpreter. Attribution is exact: rewriting the sink as
`elems.append(E{ v: inner_expr.v })` — same work, no direct Ident at the sink —
frees 4000/4000. That is a **fifth** leak shape for the table above, and the
first with a one-line proof of its own cause.

**But the eligibility signature is NOT sufficient for the miscompile.**
Reversing the probe's `[0,2)` — the exact analogue of the failing window —
changes nothing (3500/2000, exit 4 either way). So the ordering sensitivity
needs something `parse_primary` has and a 15-line function does not; the
signature reproduces the leak, not the crash. Six small-program extractions of
the ordering effect have now failed.

**What was still unknown** — what the escape taint protects BEYOND the exit
sweep, and what `parse_primary` supplies beyond the two-drop signature — is
answered in the next subsection. Neither of the two suspects named here (the
~25 return sites, the six name-shared slots) was it: the sweep is byte-for-byte
the same 58 entries at all 25 sites, `exclude` is `""` at every one, and the
difference was never in `parse_primary` at all. It was in its CALLEE.

### The drop order half, solved (2026-07-29)

**One missing entry inc, in `computeConsumedParams`.** `parse_postfix` takes
`base: Expr` — a BORROWED union param — and rebinds it into a node that keeps
the old value:

    base = e_unary_at("as_" + tyname, base, asline, ascol);

`e_unary_at` incs `operand` when it stores it in the returned `ExprUnary` (+1),
and the reassignment's overwrite dec releases the slot's old value (−1). That
pair is only balanced when the SLOT owns a reference — true for a `var`, and
true for a param the consumed-threaded promotion entry-incs. It was NOT true
here: `computeConsumedParams` promoted `StructType` / `TupleType` params only,
so a reassigned ENUM param stayed on the borrow baseline and the dec released a
reference the caller never handed over.

Measured with `__rc_get` spliced into a copy of `parser.fern`, compiling
`return (a) as i32;`:

    RC before postfix inner=2      <- tuple element + destructure bind inc
    PF base rc=2  (before e_unary_at)
    PF new base rc=1
    RC after postfix inner=2       <- want 3: the ExprUnary node is a third owner

So the box carried 3 live references at rc 2. `parse_primary`'s sweep then
decs it twice, and the SIGN of the bug is decided purely by which dec runs
last: forward order leaves rc 0 on a live box (a leak, and the program is
correct by luck), reversed order lets the tuple's deep walk see rc 1, take the
`is_unique` branch, and FREE a box the returned AST still points at. That is
the whole "ordering is load-bearing" effect — one under-count, two ways to be
wrong.

**The fix** is one line: admit `ast.EnumType` alongside `StructType` /
`TupleType` in `computeConsumedParams`. The comment there had explicitly parked
enums ("the self-host accumulators are all structs"), yet the paragraph
immediately below it already describes this exact failure mode for scalar-only
structs and closes it via the borrow-verdict escape hatch.

**Verification, in both directions.** With the fix, reversing EVERY function's
entire exit sweep (not just the [38,40) window) is byte-for-byte behaviourally
inert on the parser: `parser.fern` parses to the same 408 funcs / 17656 idents /
same checksum, and the f64 program that segfaulted parses identically. Without
the fix the same all-reversed build gives 17652 idents, a different checksum,
and a SIGSEGV. Sweep order is no longer load-bearing.

**A 40-line repro that miscompiles on `main`, no probe harness needed** —
`internal/e2e/rc_self_reassign_field_test.go`'s `unionThreadedParamSrc`
(x86-64 / arm64 / wasm). A union-typed param rebound into a node that keeps it
reads back a payload the interpreter gets right and the native backends do not:
pre-fix the fixture returns its value-mismatch code 100 on x86-64 and arm64 and
traps on wasm; post-fix all three return 0. This is the shape the six earlier
extraction attempts missed: they varied
the SINK (`append`, a struct field) inside the destructuring function, when the
under-count is in the CALLEE that rebinds a borrowed param.

**Methodology that made the difference: probe the PARSER, not the compiler.**
Every previous attempt used `wasm_run` / the load fixpoint — a ~4 min emit per
data point. A driver importing only `lexer` + `parser` + `util` + `astwalk`
builds in **~3 s** and detects AST corruption directly: walk every function
body with `astwalk.collect_idents_stmt` and checksum the names. A use-after-free
in the parser changes the ident count or the checksum, and on the tiny f64 input
it segfaults outright. Two further instruments finished the job:
`FERN_RC_FREE_DEBUG=1` (now settable from the CLI, needs `-cc gcc` — the
in-process assembler has no `ud2`) quarantines freed blocks instead of recycling
them, so a corrupted run that turns CORRECT under it proves "freed and reused"
rather than "leaked"; and `__rc_get(x)` spliced into a scratch copy of
`parser.fern` reads the refcount directly (its checker signature is
`(u8[]) -> i32`, so measuring a struct / union value needs the E038 arg check
waived for that one callee — a throwaway edit, not committed).

**What this does NOT fix.** The load leak. Same `FERN_LEAKCHECK` counters on a
full `parser.fern` parse before and after (allocs 904802 both; frees 178756 →
174230 — slightly FEWER, since the over-releases that used to free early are
gone). Cause 1 (the escape taint) and the four/five leak shapes above are
untouched and still open — see the next subsection, which attributes the
biggest of them.

**And it is NOT a self-host bug.** The self-host's `consumed_params_of`
(`irlower.fern`) carries the same struct/tuple restriction the native side just
lifted, so it is tempting to conclude the self-host compiler has the same
under-count. It does not, and the tables are not the place to look:
`consumed_params_of` / `free_eligible_of` / `array_set_incs_of` feed ONLY
`rc_plan_dump` (the #4482 differential harness) — nothing in self-host lowering
reads them. Measured, not inferred: an `asm_ir_run` driver compiles
`unionThreadedParamSrc` to a binary that exits 0, the interpreter's answer. The
self-host's own reclamation is gated on the much more conservative
`slot_is_reclaimable_*` predicates, which never admit a param slot, so it emits
no param overwrite dec to be unbalanced. What the restriction DOES produce is a
dump-level divergence, which is what `TestSelfHostRcPlanDiff` exists to pin.

### The load leak, attributed: a callee that retains its parameter (2026-07-29)

The biggest single shape behind "over 80% of every allocation in the lex/parse
path is never freed" is now pinned to one rule, with a 13-line probe —
`examples/probes/retained_param_leak.fern`. Passing an owned value to a function
that RETAINS it into what it returns leaks **exactly one reference per call**:
the caller's.

    function mkT(name: string, line: i32): Tk { return Tk { name: name, line: line }; }
    var s: string = "id" + r.to_string();
    var t: Tk = mkT(s, r);                   // <- leaks one reference, every call

The `Tk { name: name }` field init is a COUNTED store (the StructLit alias inc),
so the returned struct owns a reference. The caller's own reference is then
escape-tainted out of every release site — `computeFreeEligible` taints an
argument that flows into a call — so nothing decs it, and the string's rc sits
one above its true owner count forever.

Measured with `FERN_LEAKCHECK=1` on 1000 rounds, 3 allocs per round (the
`to_string` buffer, the concat, the struct box):

| shape | leaked |
|---|---:|
| `var t = Tk { name: s, line: r };` — inline literal | 1000 (baseline) |
| `var t = mkT(s, r);` — via the retaining helper | **2000** |
| `var t = mkT("id" + r.to_string(), r);` — fresh arg, no local | **2000** |
| `mkT` stores `name + ""` (a fresh copy) instead of the param | 1000 |

So it is the CALL that leaks, not the binding (a fresh argument expression leaks
identically to a bound local), and it leaks exactly when the callee retains the
PARAMETER ITSELF — swapping the sink for a copy takes it back to the baseline.
Marking the param `own` does not help either: the leak is on the caller's side
of the boundary, not in the ownership transfer.

**Why this is most of the lexer's leak.** `tokenize` builds every token through
one of eight `*_tok` helpers (`ident_tok(name, line, col)`, `punct_tok`,
`string_tok`, …), each of which retains its string param into the returned
`Token`. Tokenizing `parser.fern` (117315 tokens) leaks **466815 of 511278
blocks — 91.3%**. The four leak shapes in the table further up are all
variations on the same missing release; this is the one that scales with the
token count.

**Where a fix has to go.** Not at the call site: the caller's local must live to
ITS last use, so the release belongs to the normal precise-drop / exit-sweep
path, which means the argument must stop being escape-tainted. That needs an
interprocedural summary the analysis does not have yet — "for callee f,
parameter i is retained only through COUNTED constructions (or not at all)" —
which is `findReturnsNoParamEscape`'s neighbourhood (it already computes the
strictly stronger "returns nothing aliasing a param"). The counted-store
distinction is exactly the one `escapeOwned` already draws at the `Array_push` /
`Array_set` sinks, so the shape of the rule is precedented; what is new is
carrying it across a function boundary.

**FIXED for the counted case (2026-07-29, same day).** `inferParamCountedRetain`
is the summary this section called for, in its narrowest form: a STRING
parameter every one of whose appearances is a field / element value of a
StructLit, TupleLit or ArrayLit is retained only through counted constructions,
so a caller may release its own reference. Two sites read it — the escape walk's
blanket string taint (an argument in such a position is no longer tainted) and
`rhsTainted`'s Call arm (a result whose only param-aliasing is counted is a
fresh owned value, the sibling of the `findReturnsNoParamEscape` rule, which
demands the strictly stronger "aliases NO param" and so can never admit a node
constructor).

Both halves are needed and neither is sufficient: lifting only the argument
taint emits the caller's `str_dec` but frees nothing, because the STRUCT that
received the counted reference is itself still tainted through the call result
and never dropped.

**The scalar-argument rule is where this gets subtle, and the naive version is
UNSOUND — measured, not theorised.** A scalar parameter carries no heap, so an
`i32` argument looks like it should never disqualify a call; without that, a
tainted scalar (params are tainted by default) re-taints every call result and
the whole lift does nothing. But exempting scalars unconditionally frees a value
the caller still shares:

    function grow(m: Map[i32, i32], k: i32): Map[i32, i32] { m = m.insert(k, k * 7); return m; }
    var g: Map[i32, i32] = grow(base, i + 2);

`base` is untainted, so the ONLY thing keeping `g` ineligible was the tainted
scalar `i + 2` — and `g` shares `base`'s buffer. Exempt the scalar and `g`'s drop
frees it under the caller (`TestX86_64MapIntermediateReclaim`'s
param-receiver negative, plus its arm64 and wasm siblings — the wasm one traps
with "pointer not aligned"). So the exemption is conditioned on EVERY
pointer-shaped parameter of the callee being counted-retained too.

That condition is what bounds the win. Measured on `parser.fern`, parse output
byte-identical (same 408 funcs / 17656 idents / same checksum / same underflow
count):

| workload | frees before | frees after |
|---|---:|---:|
| `tokenize` only (117315 tokens) | 44463 / 511278 | 44566 / 508639 |
| full load (tokenize + parse) | 174230 / 904802 | 175664 / 901734 |

That is +1434 blocks on the full load — real, and the targeted shape is fully
fixed (the probe goes 1000 → 2000 freed of 3000), but a fraction of a percent of
the leak, not the near-doubling the unsound version showed. **The 40k-block
version of this number was the unsound one**; it is recorded here because the
gap between the two is exactly the value sitting behind a more precise
result-aliasing analysis (below), and because a future attempt will otherwise
re-derive the same too-good measurement and ship it.

**What the narrowness costs, and the obvious widenings.** One `name.len()`, one
`var s = name`, one `xs.append(name)`, one onward call — any use outside a
construction literal — and the summary is false, which also drags down the
scalar exemption for that whole callee.

The highest-value widening looked like the projection rule: `l = l.advance()` —
a `Lex` param read field-by-field into a fresh `Lex { src: l.src, i: l.i + 1, … }`
— is safe (every field init incs) yet fails the occurrence test, because `l.src`
is a FieldAccess and the summary only recognises a bare ident in a construction
slot.

**That widening was BUILT AND MEASURED (2026-07-29) and is NOT landed. Read this
before rebuilding it.** Crediting a one-level projection — safe when the read
lands in a counted slot, or when the field is scalar (`projFieldIsScalar`, which
can retain nothing wherever it goes) — is ~60 lines and passes every gate: the
three map negatives, the leak tests, units, and byte-identical parse output. It
is worth **+3579 frees on the full load** (175664 → 179243 of 901734) and
**ZERO on `tokenize`** (44566, unchanged).

Two reasons it was not landed, both worth inheriting:

1. **It does not touch the lexer at all**, which is where the leak is.
   `skip_trivia(l: Lex)` is the representative shape and it disqualifies on
   appearances the projection rule does not cover: METHOD RECEIVERS (`l.at_end()`,
   `l.peek_byte()`), an INDEX TARGET (`l.src[we]`, whose result is a scalar byte
   and so is a pure read the rule cannot see through), and a self-reassignment
   (`l = l.advance_to(we)`). Covering those needs per-method retention summaries
   plus a real "pure read" classifier — a different, bigger piece of work than a
   projection tweak, and the honest prerequisite for the 40k blocks.

2. **No regression test has teeth on it.** Three attempts failed to build one: an
   `internal/ir` drop-count assertion (the caller's local is a fresh StructLit,
   so it is swept with or without the summary) and a `leakcheck` frees comparison
   (helper vs inline form measure identical either way). An aggregate +2% on one
   benchmark with no small program that isolates it is a change nothing would
   catch the regression of — and, per the method note in this file, an aggregate
   improvement you cannot reproduce in a minimal shape is not yet understood.

So the sequence for the next attempt WAS: build the pure-read + method-receiver
classifier first, verify it moves `tokenize`'s freed fraction, then revisit the
projection rule.

**That sequence is now REFUTED — do not build that classifier (2026-07-29).**
Taking the doc's own advice to check the premise before writing the analysis:
`examples/probes/lexer_shape_control.fern` is a faithful mimic of
`lexer.tokenize` carrying every appearance the widening list blamed — a struct
param threaded by self-reassignment, METHOD RECEIVERS on it (`l.at_end()`,
`l.peek()`), INDEX reads through its field (`l.src[l.i]`), a field-by-field
rebuild (`Lx { src: l.src, … }`), a UNION token built by per-variant helpers
that retain a string param, `out = out.append(tok)` in a `while (true)` with the
array returned from inside the loop, and payloads cut by string slice.

It reclaims **100%**: allocs == frees, zero live bytes. Three versions were
measured — single-struct token, union token, and union-plus-array-payload-variant
(the real `TokFString` shape) — all clean. So the constructs the classifier would
have taught the summary about are ALREADY not the problem, and a per-method
retention summary would have been aimed at a non-leak.

What that leaves: `lexer.tokenize` strands ~4 blocks per token (464073 of 508639
on `parser.fern`, 117315 tokens) for a reason no small program has yet
reproduced. The remaining structural differences between the control and the
real thing are scale (a ~400-line function with dozens of locals and branches,
against the control's ~20), the FStringPart sub-array actually being POPULATED,
and the source string arriving from `io.read_all_stdin` rather than a literal.
Bisect INSIDE the real lexer next — call its real functions from a probe driver
and measure each — rather than writing another mimic or another analysis. The
mimics are now 3-for-3 at not reproducing it, which is itself the strongest
evidence about where not to look.

**The bisection, done (2026-07-29).** Three edits to a scratch copy of
`lexer.fern`, each measured with `FERN_LEAKCHECK=1` on `parser.fern`
(117315 tokens). The baseline is `allocs=508639 frees=44566`:

| edit to the real lexer | allocs / frees |
|---|---|
| baseline — `tokenize(src)` returns `Token[]` | 508639 / 44566 |
| the token array is dropped by a wrapper instead of reaching the caller | 508639 / 44566 |
| `tokenize` itself returns `out.len()`, so `out` NEVER escapes (all 6 return sites) | 508639 / 44566 |
| every `*_tok` constructor stores a LITERAL, discarding its scanned string param | 511278 / 44463 |

Read those rows carefully, because each kills a candidate:

- **Not the escape.** Whether the array is returned, dropped by a wrapper, or
  never leaves `tokenize` at all, the numbers do not move by a single block. The
  "it escapes via return, so it is legitimately tainted in the callee" reading at
  the top of this section explains none of the strand.
- **Not the payload strings.** Discarding them *raises* allocs (the slice still
  happens at the call site; only the retention goes away) and leaves frees
  unchanged. The ~4 blocks per token are the token BOXES, not their text.
- **The counted-retain summary does fire here.** That last row is exactly the
  pre-#5830 baseline (511278 / 44463), because a constructor storing a literal no
  longer counts as retaining its param — which is what accounts for the 2639
  fewer allocs and 103 more frees the fix is worth on this workload.

So the strand is the `Token[]` ELEMENTS, independent of escape and of payload: an
array built by `out = out.append(tok)` in a loop whose deep drop never walks its
elements. That is the shape "A loop-append reclamation bug" above describes, and
whose `rhsTainted` `Array_push` receiver-only candidate was measured at +0.3% on
the full parse (#5830).

**Re-measured on `tokenize` ALONE (2026-07-29), and it is marginal there too:
frees 44566 -> 44567 of 508639. ONE BLOCK.** The reasoning that it should matter
more where the elements are 91% of the strand does not survive contact with the
number, so the buffer's escape taint is not what is holding the elements either.
Along with the escape and payload rows above, that closes off every mechanism
this section has proposed for the lexer strand: not the return escape, not the
payload strings, not the buffer taint, and not the threading shape.

**Read from the emitted code (2026-07-29), and the whole strand reduces to one
question.** `fern -target x86-64 lexprobe.fern > lexprobe.s`, then grep
`lexer__tokenize`'s body:

    164  Lrcop_dec              (inline flat rc decs)
      8  call __fern_arr_dec    (BUFFER-ONLY array release)
      0  __fern_drop_arr_ptr / __drop_arr*   (the element-walking form)

At all 8 exit sites `out` is released by the buffer-only form, so the `Token[]`
elements are never walked — which is exactly the ~4 blocks per token: the union
box, its payload struct box, and the payload string, none of them ever dec'd.
`emitDec` routes an array through the walking form only when
`arrElemIsRcTracked(elem) && eligible`, and `Token` is an EnumType so the element
test passes. **Therefore `freeEligible[out]` is false.**

And the escape is NOT why: re-emitting the `tokenize_local` variant (all 6
`return out;` rewritten to `return out.len();`, so `out` cannot escape) gives the
SAME 8 `__fern_arr_dec` and no walk. So `out` is tainted by something inside
`tokenize` other than its return.

**ANSWERED (2026-07-29), and the prize is quantified.** Instrumenting the taint
sites (a `note(rule, name)` call at each, env-gated) says `out` is tainted by the
**assign-RHS** rule — `out = out.append(tok)`, where `rhsTainted`'s any-arg rule
carries the ELEMENT's taint onto the buffer. Applying the `Array_push`
receiver-only arm clears it, and an element-walking `__drop_arr_enum_lexer__…`
appears in the asm.

But that alone is worth ONE block at runtime (#5847), because of the second
half: at the 6 real `return out;` sites the array is MOVED to the caller
(move-on-return excludes it from the sweep entirely), so the only walking drop
the arm buys sits on the fall-through exit that never executes.

Both conditions together are what unlock it. On `parser.fern`, `tokenize` with
the arm AND `out` rewritten not to escape (`return out.len();`):

| build | frees of 508639 | live |
|---|---:|---|
| baseline | 44566 (8.7%) | 18.6 MB |
| arm only, `out` escapes | 44567 | 18.6 MB |
| arm + `out` non-escaping | **179100 (35%)** | **12.75 MB** |

**4x the frees.** That is the size of this one shape, and it explains why every
single-variable probe in this section read flat: neither condition moves
anything without the other, so escape-only and taint-only experiments both
measure noise.

What it means for a real fix: `tokenize` legitimately returns its array, so the
callee-side rewrite is not available. The site that must become eligible is the
CALLER's binding of the returned `Token[]` — the value the caller receives and
drops. **The `Array_push` arm is a PREREQUISITE here — it was UNSOUND for a
specific reason (2026-07-29), and that reason is now fixed; the arm is IN and
the caller-side condition is all that remains — which #5880 has since landed;
see "THE 4x IS DONE" and "DONE — option 1 landed"
below.** The chase is kept because the wrong turns in it are the point.
Re-applying it used to fail
`TestArrayPushProjectionSourceFreeEligible`
(`internal/ir/push_counted_store_test.go`), whose second half is exactly this
invariant:

    var row: i32[] = [k, k + 1];
    var out: i32[][] = [];
    out = out.append(row);      // DIRECT IDENT element -> moved, NOT inc'd
    var e: i32[] = out[0];

**CORRECTION, same day, before anyone acts on it.** The first version of this
entry said a direct-Ident element "takes the moveSites shape: `emitArrayPush`
transfers the reference instead of inc'ing it". **That is wrong.**
`markConstructionMoves` has cases for StructLit / ArrayLit / TupleLit /
MakeClosure and NONE for `Array_push`, so a pushed element is never a moveSite
and `emitArrayPush`'s `needsRcIncOnAlias(Args[1]) && !moveSites[Args[1]]` inc
ALWAYS fires for an aliased element. The escape-sink comment at
`rc_analysis.go`'s `__method_Array_push` case says the same thing in the source.
So the "moved element" mechanism, and the moveSite gate proposed from it, are
both fiction.

What is actually known, and no more than this: re-applying the arm flips
`TestArrayPushProjectionSourceFreeEligible`'s direct-Ident half from 0 deep array
drops to 2, and its projection half from 2 to 4. Whether those extra drops are an
over-release or merely a conservative baseline the test pins by convention is
NOT established — the test's own comment calls it "the conservative direct-ident
baseline", which is the language of a convention, not of a proven UAF. Nobody has
run the arm's emitted code and observed a corruption; the original "unsound"
verdict came from 7 self-host failures whose mechanism was never traced.

**The experiment is done (2026-07-29): the extra drops RECLAIM, they do not
over-release.** Both fixtures rebuilt as standalone programs (200 rounds each),
measured with `FERN_LEAKCHECK=1` and checked against `fern -interp`:

| fixture | baseline | with the arm |
|---|---|---|
| direct-ident (`out = out.append(row); var e = out[0]`) | 600 allocs / 200 frees, 16000 live | 600 / **400**, 6400 live |
| projection source (`out.append(rows[0])`) | 1000 / 600, 16000 live | 1000 / **1000**, **0 live** |

Exit codes match the interpreter in both (1 = 1), frees never exceed allocs, and
the projection case reaches a perfect 1000/1000 with zero live bytes. The
`MapIntermediateReclaim` negatives on x86-64 / arm64 / wasm — the probe that
caught #5830's unsound scalar rule — pass with the arm applied, as do the
retained-param and union-threaded-param tests.

So `TestArrayPushProjectionSourceFreeEligible` is pinning a CONVENTION (its own
comment calls it "the conservative direct-ident baseline"), not a safety
property. If the self-host gate agrees, the correct change is to UPDATE that test
to the reclaiming counts rather than to gate the arm — and the caller-side half
of #5854's 4x becomes the only thing left between here and the lexer's number.

**SETTLED — the original verdict was RIGHT, and the arm is UNSOUND *as it stood*
(2026-07-29).** (Superseded later the same day: the arm is unsound only on top of
the non-retaining grow copy, which is now fixed, and the arm is IN — see "DONE —
option 1 landed" below. The failure and its signature are kept here because they
are what the fix has to keep green.)
Applying it and pushing to CI reproduces the historical signature EXACTLY:
`TestSelfHostStdTestE2EArm64`, **7 failures**, `undefined reference to
__fn_test__assert_eq__i32` — a definition missing while its call sites survive,
i.e. freed-and-reused memory inside the self-host compiler. Same test, same
count, same symbol as the verdict recorded earlier in this section.

Everything above pointing the other way was measuring the wrong thing. With the
arm applied ALL of these pass and NONE catches it: the two fixtures run
standalone (they reclaim), the `MapIntermediateReclaim` negatives on all three
backends, and `TestSelfHostLoadFixpointX86_64` — twice, the second time on a
deliberately frozen tree, 369 s.

**So the load fixpoint does NOT subsume `TestSelfHostStdTestE2EArm64`.** That is
the operational rule to take from this whole episode: the x86-64 whole-compiler
self-compile can be green while the arm64 stdtest link is broken by the same
change, so any RC change touching array taint must run
`TestSelfHostStdTestE2EArm64` (arm64, `internal/e2eselfhost`, ~220 s under qemu)
before it is believed. CLAUDE.md's "leave arm64 to CI" guidance is right for
speed and wrong for this class.

**The mechanism is FUNCTION-NAME STRINGS, and it reproduces LOCALLY in 312 s
(2026-07-29).** `go test ./internal/e2eselfhost/ -run TestSelfHostStdTestE2EArm64`
with the arm applied fails on this host — no CI round trip needed, which is the
first practical consequence of the rule above. The local output carries three
symptoms CI's tail did not show, and they are one thing:

| symptom | reading |
|---|---|
| `unknown mnemonic '__fn_m'` (map_eq_and_predicates) | a TRUNCATED function name |
| `symbol '__fn_' is already defined` (array_combinators) | an EMPTY function name |
| `undefined reference to __fn_test__assert_eq__i32` (option/result_combinators) | a definition missing, call sites intact |

Every one is a corrupted function NAME: `__fn_` + a truncated or empty string.
So the arm is not corrupting arbitrary heap — it is freeing the self-host
emitter's function-name STRINGS while they are still referenced, and the
recycled bytes come back as a short or empty name. A name that recycles to ""
also explains the third row: the definition is emitted under a corrupted symbol,
so the call sites' correct name resolves to nothing.

**Narrowed to the emit site (2026-07-29).** `emit_function`
(`asm_arm64.fern:3806`) writes the symbol as

    s = s.write("__fn_" + asmcore.sanitize_label(label_name) + ":\n");

and `sanitize_label` (`asmcore.fern:17`) is a scan over its argument:

    var out: string = "";
    while (i < name.len()) { ... out = out + name[i : i + 1]; ... }

A truncated `__fn_m` and an empty `__fn_` are precisely what that emits when
`name`'s BUFFER HAS BEEN FREED AND RECYCLED — the length word reads back 1 or 0
and the loop copies that many bytes. So the corruption is in the INPUT name, read
after free, not in the emitter's own accumulator, and not in `sanitize_label`
itself.

`label_name` is `fd.name` (or `base_type_name(fd.receiver_type) + "." + fd.name`),
i.e. a string owned by the module AST's `FuncDecl`. The hypothesis to test first
is therefore that the arm lets some array of names or decls deep-drop elements
the module still owns — `asmcore.fern:3634`
(`names = names.append(mod.funcs[fi].name)`) and its siblings at `:3433` / `:3436`
are the exact `xs = xs.append(<projection>)` shape the arm untaints, over
`mod.funcs`.

A cheap discriminator before any bisect: build the arm, run the 312 s local
repro, and dump `fd.name.len()` at the top of `emit_function`. If the first
corrupted symbol is preceded by a name whose length reads 0 or 1, the
read-after-free reading is confirmed and the search is over which of those
arrays was dropped; if the lengths are all sane, the corruption is downstream in
`write` / the EmitState buffer instead, and the candidates above are wrong. #5854's 4x
stays blocked on this, but it is now a search with a named target rather than an
open question.

### SOLVED: a shared `string[]` buffer, grown without a retain (2026-07-29)

The mechanism is a **use-after-free in `__fern_arr_push_grow`'s copy path**,
which the arm merely EXPOSES. It is not in `sanitize_label`, not in the
`EmitState` buffer, and not in any of the `asmcore.fern` arrays named above —
those are `string[]`s built by projection-push, which inc every element and are
balanced. Distilled to 25 lines in `examples/probes/alias_grow_uaf.fern`
(exit 0 native + interp, exit 1 with the arm; an `FERN_RC_FREE_DEBUG=1` build
traps on `ud2` at the stale holder).

**The chain, each link measured.**

1. The one function that matters is `irlower.lift_lambdas_view` — established by
   bisecting the arm itself, not by reading code. `rhsTainted`'s arm was gated on
   a per-function allow-list (`FERN_ARM_LIST`) and the 147 functions whose
   rcPlan the arm changes (an `RcPlanHook` dump of the driver, diffed between
   two `cmd/fern` builds) were halved down. **Enabling the arm for
   `irlower__lift_lambdas_view` ALONE reproduces all four failures with the
   identical signature**; the other 146 together are clean.
2. In that function the arm makes BOTH halves of an aliased pair reclaimable —
   `freeEligible` gains `gfns,lgfns` (plus `worklist`, `r`, `cbody`):

       var lgfns: string[] = gfns;                       // alias inc -> buffer rc 2
       while (gj < acc.funcs.len()) { lgfns = lgfns.append(acc.funcs[gj].name); … }

3. `lgfns = lgfns.append(..)` is the **self-append MOVE form**, which
   `emitArrayPush` deliberately routes to the plain `__fern_arr_push_grow`
   rather than the retaining `_ptr` / `_str` variants (`ir.go`'s "EXCEPTION"
   comment). Its copy path memcpy's the element pointers with **no retain** —
   sound only because the form it was written for frees the old buffer with a
   buffer-only `__fern_arr_dec` right after ("the old buffer's pointer elements
   were transferred to the new buffer").
4. The copy path is taken here for the OTHER reason: `rc != 1`. The old buffer
   is not freed — `gfns` still owns it, `__fern_arr_dec` just takes it 2 → 1.
   So two live buffers now share every element pointer under a single count.
5. Both locals are reclaimable, so both walk-drop at exit via
   `__fern_drop_arr_str`, which decs each element. **Every shared name gets two
   decs for one inc.**
6. Only the names with no other owner actually die. A parsed name is still held
   by the token stream / AST idents; a **monomorphised clone's** name is a fresh
   concat from `clone_bg` (`fd.name + "__" + sanitize_key(key)`) owned by
   nothing but its `FuncDecl`, so rc 2 → 0 and the block is freed and recycled.
   That is why the corrupted symbols are always the mono clones at the tail of
   `mod.funcs` (`test__assert_eq__i32`, `test__assert_eq_map__string__string`, …)
   and never an ordinary function.

That accounts for all three faces in the table above from one cause: a recycled
block whose length word reads back 0 gives `__fn_`, one that reads 1 gives
`__fn_m`, and the `undefined reference` is the same thing seen from the call
sites — they spell the name correctly (it comes from the rewritten call
expression, a different string), the DEFINITION is what lost it. In
`option_combinators` the entire 18150-line emit differs by exactly one line.

**The chain is causal, not correlational — the shape was removed and remeasured**
(the method note further up this file, applied). With the arm still enabled for
`lift_lambdas_view` and ONLY the alias replaced by an element-by-element copy
(`var lgfns: string[] = []` + a loop over `gfns`, which inc's each element into
the new buffer), all four cases go back to **byte-identical** with baseline:

| build | option | result | array | map_eq |
|---|---|---|---|---|
| arm on `lift_lambdas_view` alone | corrupt | corrupt | corrupt | corrupt |
| same, alias replaced by a copying loop | same | same | same | same |

**Why every earlier probe read clean.** Three conditions have to hold at once,
and dropping any one of them makes the program correct:

| condition | what fails without it |
|---|---|
| both aliases reclaimable | one walk-drop, one dec — balanced (this is main's state today, and why the shape is currently unreachable) |
| the grow takes the COPY path with the old buffer surviving | in-place grow shares nothing |
| the element outlives its own buffer only via a second holder | an rc≥3 name absorbs the extra dec silently |

It is also **x86-64 only** in this shape: the same probe under `-target arm64`
returns 0 with the arm applied (two-word strings take a different retain path).
The self-host failure is an x86-64 miscompile of `mmc` — the arm64 gate catches
it because `mmc`, an x86-64 binary, is the thing that miscompiles.

**Where a fix goes.** Not in the arm, and not in `lift_lambdas_view` (rebuilding
`lgfns` element-by-element instead of aliasing removes the symptom, which is the
symptom-shielding `if` CLAUDE.md warns about — the next aliased pair reopens
it). The invariant that is actually broken is `emitArrayPush`'s
transfer-vs-retain pairing, which assumes the self-append's old buffer always
dies. Two shapes fix it, both in `internal/ir`:

- a `__fern_arr_push_grow_move_ptr` / `_move_str` that retains the copied
  elements **iff the incoming rc != 1** — "the old buffer survives this grow" is
  exactly that test, and it also covers the `forceCopy` (#4827) and #4873
  bracket cases, which inc precisely to force a copy the old holder outlives; or
- keep one helper and pair the sites: when the appended-to local is
  `freeEligible`, route its self-append to the retaining `_ptr` / `_str` variant
  AND make the assign's reinit reclaim walk (`__fern_drop_arr_str` /
  `__drop_arr_struct_<E>`) instead of the buffer-only `__fern_arr_dec`, so the
  extra retain is compensated.

Either one is a three-backend change (x86-64 / arm64 / wasm runtime helper plus
the need-registration) and needs the gate list below.

**DONE — option 1 landed, and the arm is IN (2026-07-29).**
`__fern_arr_push_grow_move_ptr` / `_move_str` exist on all three backends
(x86-64 `_move_ptr` only — native single-word strings take the pointer form) as
a `moveForm` parameter on the existing `_ptr` / `_str` emitters, so the copy
path is shared and only the retain is gated:

    // the copy path leaves the OLD buffer's rc untouched
    if incoming_rc == 1 { skip the element-retain loop }

`emitArrayPush` routes `selfPushMoveCall` to them (previously: to the plain,
never-retaining `__fern_arr_push_grow`), and `rhsTainted` grew the
`__method_Array_push` receiver-only arm next to its `__method_Array_set`
sibling. `appendForcesCopy` already returns false for `selfPushMoveCall`, so
the forceCopy rc bump never reaches these helpers and there is no interaction to
reason about.

The causal check, since the arm's own probe had been passing for the wrong
reason before: with the arm applied and the routing reverted to the plain
helper, `alias_grow_uaf.fern` exits **1**; with both, **0**. The `_move_`
helpers are what make the arm sound, not a coincidence of the fixture.

**What the arm is worth today: one block.** `tokenize` on `parser.fern` goes
44565 → 44566 frees of 508638, exactly the "ONE BLOCK" predicted above — the
arm is only the *first* of the two conditions. `out` still escapes through the
six `return out;` sites, so move-on-return keeps it out of the sweep and the
walking drop the arm buys sits on a fall-through that never runs.

### THE 4x IS DONE — #5880 landed it, not the arm (2026-07-29)

**Do not go looking for the caller-side half. It is already banked.** #5884's
own body says "the 4x needs the CALLER's binding of the returned `Token[]`
becoming eligible … the only thing left in this strand"; that was written
against a stale baseline and is **wrong**. `perceus: interprocedural
counted-retain fixpoint credits method-receiver threading (#5880)` landed in
parallel and delivered it. Bisected by building each commit and running the
same driver (`var toks = lexer.tokenize(io.read_all_stdin()); return
toks.len() % 7;`) over `parser.fern` under `FERN_LEAKCHECK=1`:

| commit | frees of 509152 | live |
|---|---:|---|
| `e1f21613` (before both perceus PRs) | 44594 (8.8%) | 18.6 MB |
| `0edb78b7` #5878 move-on-construction-in-a-loop | 44594 — **no change** | 18.6 MB |
| **`2123ff75` #5880 interprocedural counted-retain fixpoint** | **184869 (36.3%)** | **14.2 MB** |
| `70de2db4` main, with the `Array_push` arm | 184870 (**+1**) | 14.2 MB |

That is the 4x this section spent so long pricing (it predicted 179100 / 35%;
the real figure is 184869 / 36.3%). The last row is the same one-block delta the
arm measures everywhere else, confirmed a second way: disabling the arm on
current main gives 184869, enabling it 184870.

**Two consequences for whoever reads this next.**

- Every "44566 / 8.7%" and "464073 of 508639 stranded (91.2%)" figure ABOVE this
  heading is pre-#5880 and must not be used as a current baseline. The
  measurement recipe is still good; the numbers are two PRs stale.
- A **zero-param short-circuit in `findReturnsNoParamEscape` is worth nothing**
  and should not be built. The reasoning is sound and the gap is real —
  `io__read_all_stdin` has 0 parameters yet `returnsNoParamEscape` is `false`,
  because the pass tests every return expression (`return chunks.join("")` is
  not a recognised fresh construction) without ever noticing there is no
  parameter to alias — but adding `if len(fn.Params) == 0 { continue }` changes
  the number by **0 blocks**: measured 184870 with and without. #5880 already
  reaches these bindings by another route. (It is also not obviously safe: the
  pass is consumed downstream as "safe to free this result", and a zero-param
  function returning `random_bytes(n)` — a header-less raw buffer `rhsTainted`
  special-cases as permanently tainted — would break that. Vacuous-on-params is
  not the same claim as freeable.)

The rcPlan dump is what settled where the remaining taint sits, and it is worth
repeating rather than reasoning about: with the arm in, `lexer__tokenize` reports
`freeEligible: l,out,rf,ri,rn,rs,text` — `out` IS reclaimable in the callee now.

### What is left after #5880: a diffuse tail, not another lever (2026-07-29)

**Current baselines, post-#5880. Use these, not anything above.**

| workload on `parser.fern` | allocs | frees | freed | live |
|---|---:|---:|---:|---|
| `tokenize` only | 509152 | 184870 | 36.3% | 14.2 MB |
| `tokenize` + `parse_module` | 902183 | 322623 | 35.8% | 22.8 MB |

So ~64% still leaks, and the obvious next question is which functions to aim at.
**Ranking the functions answered it, and the answer is that there is no next
single lever.** 297 stranded allocation sites spread over 161 functions, mostly
3–5 apiece; the two biggest (`inject_builtin_enums` 13/13, `parse_module` 11/13)
each run ONCE per compile, so their block count is negligible, while the
genuinely hot ones are already mostly fine (`parse_stmt` 4/20,
`parse_match_expr` 7/16). Nothing here is worth a 4x. Whatever comes next in this
strand should be a rule that fires across many sites, not a shape hunt — the
shape-hunting era of this section is over for this workload.

**The ranking method, and the two ways it lies.** Hook `RcPlanHook`, parse
`freeEligible` / `movedLocals` out of each dump, walk the AST for `*ast.Var`
declarations, and report those a function neither reclaims nor transfers. Both
naive versions of that metric are wrong, and both wrong versions look plausible:

| filter | count | why it's wrong |
|---|---:|---|
| every pointer-typed local not in `freeEligible` | 1258 / 381 funcs | counts locals MOVED into a returned construction — correct behaviour |
| ...also excluding `movedLocals` | 1151 / 371 funcs | still counts BORROWED ALIASES — `var name = p.peek_ident()` owns nothing, so having nothing to free is right, not a leak |
| ...and only inits that DEFINITELY allocate (`StructLit` / `ArrayLit` / `TupleLit` / `MakeClosure` / string concat) | **297 / 161 funcs** | this is the one to use |

The middle row is the trap worth naming: **"not `freeEligible`" is not the same
as "leaks".** A local that merely aliases someone else's value has nothing to
free, and freeing it would be a use-after-free — so the analysis is *right* to
exclude it. Ranking by ineligibility alone puts `parse_pattern` (7/7, every local
a `p.peek_ident()` alias into the token stream) near the top of a leak list it
does not belong on at all. Only a local whose initialiser is itself an
allocation can strand a block.

**Gates for that work, in order** (the first two are seconds, and the last two
are the ones that have historically disagreed):
`examples/probes/alias_grow_uaf.fern` (exit 0 compiled == interp, x86-64,
arm64 and wasm) → `internal/ir` `TestArrayPushProjectionSourceFreeEligible` →
`MapIntermediateReclaim` on all three backends →
`TestSelfHostStdTestE2EArm64` (312 s local, REQUIRED) →
`TestSelfHostLoadFixpointX86_64`.

**Found on the way, NOT fixed here: wasm two-word `string[]` self-append does
not reclaim.** `TestX86_64ArrayPushPtrElemReclaim` (`a = a.append("item")` 300
times, `build` called N times, working set measured with `__heap_bump_bytes`)
gained an arm64 leg here — green — and a wasm leg, which fails and always has:

| backend | `struct[]` | `string[]` (20 iters -> 400 iters) |
|---|---|---|
| x86-64 | O(1) | O(1) |
| arm64 | O(1) | O(1) |
| wasm | O(1) | **64480 -> 1231840 bytes** |

Identical numbers with and without this change's `_move_` routing, so it is not
caused by it — the two-word `string[]` grow's old buffer is simply never
reclaimed on wasm, where the single-word `struct[]` sibling is. The wasm leg was
removed rather than landed failing; adding it back is the acceptance test for
that fix. Two consequences to know before touching this area: the wasm side of
`_move_str` is CORRECT-BY-CONSTRUCTION but not yet exercised (disabling its
retain leaves `alias_grow_uaf.fern`'s wasm leg passing, because with no old
buffer freed there is no second walk-drop to over-release), and any measurement
of this shape on wasm is currently measuring the gap, not the contract.

`TestArrayPushProjectionSourceFreeEligible` was UPDATED rather than gated: its
projection half goes 2 → 4 deep-drop sites (`src` and `out`, reinit + sweep
each) and its direct-ident half 0 → 2 (`out` reclaims; `row` stays
escape-tainted, so the element it moved in is released exactly once). The
earlier reading that those extra drops might be an over-release is settled —
they reclaim, and the self-host gates agree now that the grow copy retains.

**Reusable method, since this class keeps coming back.** The two tools that
cracked it are cheap and general:

- **Bisect the analysis, not the program.** Gate the change on a per-function
  allow-list read from an env var, take the candidate set from an `RcPlanHook`
  diff, and halve. Each step is one driver build (~4 min) plus a 0.6 s compile —
  no need to guess which of 147 functions matters.
- **Don't run the 312 s test to iterate.** `TestSelfHostStdTestE2EArm64` builds
  `mmc` (an x86-64 binary) once and then compiles each case in well under a
  second; building `mmc` by hand and diffing `mmc <case> <stdlib> -target arm64`
  against a baseline build gives the same signal in 0.6 s per case, and the
  symbol diff names the corrupted function directly.
- **`FERN_RC_FREE_DEBUG=1` is the detector for this class** (quarantine + poison
  + `ud2` on any rc touch of a freed block). It needs `-cc gcc`: the in-process
  native assembler rejects `ud2`.

(The original question, kept for the record: **what taints `out`?**)
It is worth answering with the rcPlan dump rather than by guessing — print
`freeEligible` / the taint set for `lexer__tokenize` (the `RcPlanHook` in
`internal/ir/rc_dump.go` gives it in-process; the same hook the #4482 harness
uses) and read which rule fired. Note the `Array_push` receiver-only arm is NOT
it (one block, above), so a third taint source is in play and naming it is a
measurement, not a design question.

### Answered: what taints `out`, and why it is a COUPLED pair (2026-07-29)

The measurement the section above called for is done — a taint trace of
`computeFreeEligible` for `lexer__tokenize` (a temporary `why[name] = rule` map
threaded through the fixpoint), plus a minimal reproducer that finally
reproduces (`examples/probes/result_thread_leak.fern` — the FIRST to do so; the
three earlier mimics all reclaimed 100%). Result:

```
[taint tokenize] tainted=[after_dot i l mp out ptext rf ri rn rs …]
  l   <= rhsTainted *ast.FieldAccess@653  (l = rf.lex)
  out <= rhsTainted *ast.Call@654         (out = out.append(rf.tok))
  rf  <= rhsTainted *ast.Call@652         (var rf = scan_fstring(l, …))
  rn  <= rhsTainted *ast.Call@660         ri <= @668   rs <= @675
```

`out` is tainted by the append — but that is the SECONDARY mechanism. The
dominant one is the mutual `l`/`rf`/`rn`/`ri`/`rs` taint knot:

- `l = rf.lex` reads a pointer field OUT of the result struct. `rhsTainted`'s
  `FieldAccess` arm is unconditionally `true`, so `l` is tainted.
- `l` tainted makes `var rf = scan_fstring(l, …)` tainted — `scan_*`'s cursor
  param is a STRUCT, and `inferParamCountedRetain` handles only STRING params
  (`rc_analysis.go:489`), so the callee is never proven counted-retain and a
  tainted arg taints the result.
- `rf` tainted feeds back through the next `l = rf.lex`. The whole cluster is
  mutually tainted and **never freed** — every `Res`/`NumResult { lex, tok }`
  leaks per iteration, and with it the token its `tok` field holds.

That is why the `Array_push` receiver-only arm freed only one block: the tokens
are kept alive by the leaked RESULT STRUCTS, not by the accumulator buffer.
Decomposed with two controls (both in the reproducer's header):

| shape | allocs / frees | leaked |
|---|---|---:|
| `l = r.lex` + `out.append(r.tok)` (both mechanisms) | 2400 / 200 | 92% |
| `l = r.lex`, token consumed immediately, **no accumulator** | 2100 / 0 | **100%** |
| tokens accumulated, cursor threaded WITHOUT a result struct | 1900 / 800 | 58% |

The middle row is decisive: strip the accumulator entirely and the result-struct
threading STILL leaks everything. The append-ineligibility is real but subordinate.

**The fix is a COUPLED pair, and this is the new finding — neither half moves
`tokenize` alone, which is why every prior single-lever attempt measured
marginal.** To reclaim the cluster:

1. **Callee summary (part 1).** Generalise `inferParamCountedRetain` from
   string-params-only to a struct param whose every appearance is a counted
   projection — `scan(l)` stores only `l.src` (into a `Lx` field, inc'd) and
   `l.i` (scalar). Then `rhsTainted(scan(l))` is false regardless of `l`, so
   `rf`/`rn`/`ri`/`rs` stop being tainted. This is the projection widening the
   doc built and measured at "+3579 frees, ZERO on tokenize" — but that zero was
   measured in ISOLATION, which is exactly the trap:
2. **Caller projection inc (part 2).** With `rf` no longer tainted it becomes
   droppable, so `l = rf.lex` must INC the field (`l` owns its own reference)
   before `rf`'s deep-drop decs it — otherwise dropping `rf` frees the `Lx` that
   `l` still points to. Without part 2, `l = rf.lex`'s `FieldAccess` taint keeps
   `l` (and through it the whole cluster) tainted no matter what part 1 proves —
   which is precisely why part 1 alone showed ZERO on `tokenize`.

So they must land together, gated on the reproducer going 2400→~2400 freed AND
the map-intermediate negatives (the arm64/wasm siblings included — the wasm one
traps rather than leaks) AND whole-compiler byte-identity. Part 1 is
byte-identity-affecting (it un-taints call results across the compiler), so the
batch=4 emit-all fixpoint is the gate that a re-taint didn't silently change
codegen elsewhere.

The map-intermediate negatives remain the sharpest soundness probe in this area;
run them on every iteration of anything that touches the taint.

#### Part 2 landed, and the mechanism split is sharper than "coupled pair" (2026-07-29)

Part 2 is now in `rhsTainted`: a `FieldAccess` read whose source is a struct
LOCAL is a COUNTED alias (the bind inc's it, both the destination and the source
struct deep-drop), so it no longer taints its destination. Measured, and it
corrects the framing above:

- **Part 2 ALONE fully reclaims `result_thread_leak.fern`** (200→2400 of 2400
  freed, `live_bytes=0`, exit unchanged). Un-tainting `l = r.lex` un-taints `l`,
  and because `r = scan(l)`'s only tainted input was `l`, `r` un-taints with it —
  no part 1 needed for that shape. The earlier "neither half moves it" was
  measured on `tokenize`, whose result structs stay tainted for a SECOND reason
  the minimal reproducer lacked.
- **That second reason is scalar-arg poisoning, and it is what part 1 actually
  fixes.** In `tokenize`, `start_line`/`start_col` are tainted by `escapeOwned`
  (stored directly into `TokPunct { line: start_line, … }` literals in tokenize
  itself), and a tainted SCALAR argument to `scan_*(l, start_line, start_col)`
  re-taints the result via `rhsTainted`'s generic arg loop. Part 1's real job is
  the scalar-arg EXEMPTION (`inferParamCountedRetain`'s `ptrAllCounted` gate,
  which today needs `scan_*`'s struct cursor param counted — a struct-param
  generalisation of the string-only summary). A faithful reproducer that carries
  the inline scalar store — `rt3` in the session log: an inline `TPunct { line:
  start_line }` branch — shows part 2 alone reclaiming only 200→1000 of 3400,
  with `r` and `start_line` still tainted, exactly like the real lexer.

So the split is: **part 2 = the `FieldAccess`-on-struct-local un-taint (landed,
reclaims the threading cluster); part 1 = the scalar-arg exemption via a
struct-param counted-retain summary (still open, the map-negative-delicate half,
needed for tokenize's scalar-poisoned result structs).** `result_thread_leak.fern`
is the part-2 pin (`TestLeakCheckResultThreadReclaim{X86_64,Arm64}`);
`scalar_thread_leak.fern` is the part-1 reproducer (still leaking 3400/1000).

#### Part 1 built and measured — it reclaims the projection shape but STILL not the lexer (2026-07-29)

The struct-param generalisation was implemented: `inferParamCountedRetain` grows
a `structParamProjectionsSafe` classifier that credits a struct param whose
every appearance is a COUNTED store (bare `p` / `p.field` in a StructLit /
TupleLit / ArrayLit slot) or a NON-RETAINING read (a scalar field `p.i`, a
string-slice source `p.src[a:b]`, a string byte `p.src[i]`), disqualifying
anything else. With the cursor param credited, `scan`'s `ptrAllCounted` holds,
the scalar param `start_line` is exempted, and the tainted scalar argument no
longer re-taints the result struct.

Measured, and it splits cleanly along the doc's own prediction:

| workload | before | after part 1 |
|---|---|---|
| `scalar_thread_leak.fern` (projection-only cursor) | 3400 / **1000** | 3400 / **3400** — full reclaim |
| the real lexer bench (200× tokenize of a mixed source) | 60200 / 8000 | 60200 / **8000** — unchanged |

Green on the fast soundness gates (`internal/ir` + `internal/checker` units, the
map-intermediate reclaim negatives, the full leakcheck suite). But it does **not
touch the real lexer**, for exactly the reason the widening-list section above
gives: the real scanners use `l` as a METHOD RECEIVER (`l.at_end()`,
`l.peek_byte()`) and self-reassignment (`l = l.advance()`), which the projection
classifier disqualifies. The projection rule alone was held once before for this
same "does not touch the lexer" reason; the finding here is that a teeth-having
reproducer (`scalar_thread_leak.fern`) does not change that verdict — the shape
it fixes is a REDUCTION of the lexer, not the lexer.

**Part 1 was not landed in isolation** — but the motivated full fix now is.

#### Landed: the interprocedural counted-retain fixpoint (2026-07-29)

`inferParamCountedRetain` is now a least-fixpoint. `structParamProjectionsSafe`
gained the interprocedural **arg-position rule** — a `p` passed as argument i to
a call whose callee parameter i is counted-retain is inc'd (or read) there, not
aliased out, so it is safe exactly like a construction slot. Since a method call
`l.at_end()` lowers to `__method_Lex_at_end(l)` with the receiver as `Args[0]`,
this is the method-receiver-retention summary for free: `at_end` / `peek_byte`
read only `l.i` / `l.n` / `l.src[..]`, so they are projection-safe, so a caller's
`l.at_end()` is credited. Two more cases closed the real cursor threading:

- **Reassignment target** (`l = l.advance_to(..)`): the rebind is not a
  retention of the old value; the RHS is classified normally and the overwrite
  dec comes from `computeConsumedParams`.
- **Returned borrow** (`return l`): a borrowed value returned is inc'd on the
  way out, so the result holds a COUNTED reference to the param — creditable.
  This is the one that unblocked `advance` / `advance_to`, whose early path is
  `if (end <= l.i) { return l; }`. It is sound because returning a borrow inc's:
  measured with an adversarial `pick(m: Map, k): Map { return m; }` called with a
  tainted scalar and the result dropped every iteration — the caller's map stays
  live (no over-release), and the map-mutator negatives still exclude a builder
  whose `m.insert(..)` reaches a builtin argument first.

The fixpoint starts all-false and only adds credits (monotone, grounded), so a
mutual-recursion cycle with no grounding stays uncredited — the conservative
direction.

Measured: both reproducers reclaim fully (`result_thread_leak.fern` 2400/2400,
`scalar_thread_leak.fern` 3400/3400), and unlike part-1-in-isolation this DOES
touch the real lexer — the tokenize bench goes from **8000 to 24000 freed of
60200** (a 3× reduction in stranded blocks). Pinned by
`TestLeakCheckScalarThreadReclaim{X86_64,Arm64}`. Validated through the map
negatives, the full leakcheck suite, the broad RC/reuse/drop/container e2e
sweep, and the batch=4 whole-compiler byte-identity fixpoint (gen0==gen1, 36
units, no OOM — the change is byte-identity-preserving on the self-host
compiler, which is dense with `return p` and struct-threading).

**Residual, localised and mostly closed (2026-07-29).** The 24000/60200 was not
the FStringPart sub-arrays — it was a CALLER-side taint: `var toks =
tokenize(src)` left `toks` tainted, so its tokens stranded at the caller. Cause:
`tokenize`'s `src` param is `Lex { src: src, n: src.len() }`, and the `src.len()`
occurrence — a pure scalar read — disqualified the counted-retain summary
(string params only credited construction slots), so `tokenize(src)` re-tainted
its result whenever `src` was tainted (which it always is, being passed to a
user function). The fix: a pure-read builtin (`__method_string_len` /
`_Array_len` / `_slice_len` / `_Array_sum`) reads its receiver and returns a
scalar without retaining it, so a receiver occurrence no longer disqualifies —
credited in both the string-param summary and the struct classifier's Call arm.
The lexer bench goes **24000 → 49800 freed of 60200** (live 1318 KB → 333 KB), a
further 2× on top of the 3× above (8000 → 49800 cumulative, ~6×). Pinned
differentially by `TestLeakCheckPureReadLen{X86_64,Arm64}` (a builder using
`s.len()` must free as much as one without it). The remaining ~10 K blocks are a
smaller tail for a follow-up; the counted-retain summary now covers
constructions, scalar/slice/index reads, counted call arguments, method-receiver
retention, returned borrows, and pure-read builtins.

The rest of the widening list is unaffected by that refutation but is now
UNMOTIVATED until the leak is localised — none of it is known to touch the
number that matters. In rough order, if it turns out to: pure reads (`len`,
indexing, comparison) should not disqualify; `append` into an array the callee
returns IS a counted store (emitArrayPush incs) and needs only the same
treatment; locals that merely carry
the param into a construction want the taint-propagation fixpoint
`paramEscapesInFn` already has; and variant-constructor payloads are counted
under `EnumRcPayloads`. Each is additive — the summary is a per-(fn, param) bit —
but each also needs re-running the map negatives above, which are the sharpest
soundness probe this area has.

**Do not re-try the `rhsTainted` `Array_push` receiver-only arm as the fix for
this.** Re-measured on the parse probe with the entry-inc under-count fixed: it
is no longer obviously unsound, but it moves frees on a full `parser.fern` parse
by 174230 → 177213 out of 904802 allocs (+0.3%). It is not the load leak, which
is what the earlier attempt also concluded from RSS.

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
   not the emit-side side tables — is what is left to attack. That load cost is
   now attributed: see "The load leak, attributed: a callee that retains its
   parameter" — one missing release per retaining call, ~91% of `tokenize`'s
   blocks, with a 13-line probe and a named (not yet built) interprocedural
   summary as the fix. The gen1 emit-all
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
