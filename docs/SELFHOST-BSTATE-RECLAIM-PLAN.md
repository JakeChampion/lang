# Reclaiming the self-host's `BState` churn — design

The self-compile's >2 GiB working set is dominated by one allocation
pattern: the SSA builder threads a whole-state struct, `BState`, through
every lowering step by **functional replacement**, orphaning the old
state each time. This doc scopes the safe ways to reclaim that churn. It
is design-only; no slice here is implemented.

## The pattern

`BState` (`examples/self_host/ssa.fern:99`) is the SSA builder's working
state — blocks, current block, the SSA value-name/value tables, the
per-function type overlay, the shared signature seed, loop context:

```
struct BState { name: string, blocks: SBlock[], cur: SBlock,
                var_names: string[], var_vals: i32[],
                structs: parser.StructDecl[], vt_names: string[],
                vt_types: string[], seed: Seed, lc: LoopCtx, ... }
```

The builder is immutable-functional: every step returns a *new* `BState`
rather than mutating in place. Call sites thread it —

```
function build_expr(e: parser.Expr, s: BState): BState { ... }
...
s = build_expr(b.left, s);
s = build_expr(b.right, s);
```

— and there are **57** `s = build_{expr,stmt,stmts}(.., s)` sites. Each
produces a fresh `BState` and orphans the old one. Building one function
does this hundreds of times; the whole-compiler compile does it millions
of times. Every orphan is a fully-written object carrying live arrays, so
the orphan stream is the **dominant** allocation in the self-compile — the
thing that pushes it past a 1 GiB heap. (Distinct from the array-append
churn #2524 fixed; that was *inside* one accumulator, this is the entire
state object being replaced.)

## Why the obvious fix is a use-after-free

The natural move — at `s = build_expr(.., s)`, **deep-drop the old `s`** —
is exactly what the self-reassign overwrite (`typeSelfDropSafe` /
`selfReassignOwnedLocal`) does for simpler types. For `BState` it
**segfaults**, and the reason is precise:

`build_expr` does not build an independent `BState`. Its result **shares
fields** with the input — it carries most of the state forward unchanged
(typically `mk_bs(s.var_names, s.blocks, new_cur, s.seed, …)`), advancing
only `cur` / `blocks`. So the new and old `BState` **point at the same**
`var_names` / `structs` / `seed` buffers. Deep-dropping the old box frees
those buffers — but the **new** box still points at them → dangling read
→ crash.

This is the long-known **"broad `x = f(x)` shares fields"** hazard (the
codebase's *"the broad form segfaulted the self-compile"*). It is **not**
a strings problem: PR #2538 lifted `typeSelfDropSafe`'s string exclusion
believing strings were the blocker (they are now fully rc-tracked —
`docs/RC-STRINGS-PLAN.md`), and CI caught **74 segfaults** in
`TestSelfHostStdTestE2E`. The string exclusion was *incidentally*
shielding the field-sharing hazard. Strings being rc-tracked is necessary
but not sufficient: for the deep-drop to be balanced, every **carried**
field must be **inc'd** when the result is constructed. A struct-literal
functional update (`s = S{ ...s, x }`) does inc its carried fields (safe —
verified). But `build_expr` builds its result through a **constructor
call** (`mk_bs(...)`) whose field arguments are *borrows* under the
precise-affine model — not retained — so the carried buffers are
uncounted aliases.

> **Soundness-gate lesson from #2538:** the self-host **fixpoint** + SSA
> emit tests passed while this was broken. `TestSelfHostStdTestE2E`
> (compiling diverse programs through the self-host) is the test that
> catches it. Any BState-reclaim change MUST gate on it locally, not just
> the fixpoint.

## Three framings

### A. Retain carried fields (rc-balanced deep-drop)

Make the builder's result construction **inc the fields it carries
forward** from the input `s`. Then a full deep-drop of the old box is
balanced for every field (dec → rc ≥ 1, no free), so the existing
self-reassign overwrite reclaims the orphan box **and** its genuinely-dead
(non-carried) buffers, with no special analysis.

- **How:** `mk_bs` (and any other result-constructor) retains each
  pointer-shaped field it stores that came from a borrowed param —
  effectively the `needsRcIncOnAlias` treatment, applied at the
  constructor. Then lift `typeSelfDropSafe` for the now-balanced shapes.
- **Cost:** one inc per carried pointer field per build step (cheap — an
  rc bump, not a copy). The shared read-only `seed` is carried every step;
  its inc/dec must net to zero (it already lives behind one shared
  reference by design — confirm the bumps are balanced and it is never
  freed).
- **Risk:** must catch **every** result-constructor and **every** carried
  field; a missed inc → the exact UAF #2538 hit. Conservative fallback:
  if any field's provenance is unclear, don't reclaim (leak, never free).
- **Verdict:** most localized, reuses the whole rc arc, but the
  "every field, every constructor" burden is the soundness-critical part.

### B. Callee field-share / escape analysis (partial drop)

Instead of retaining, **analyze** each builder's returns to learn, per
result field, whether it is *carried* from the input (alias `s.field`) or
*freshly built*. At the overwrite, **partial-drop** only the non-carried
(orphaned) fields of the old `s`.

- **How:** the checker-side analogue of the IR's `returnsNoParamEscape`,
  refined from "does the result alias the param" to "*which fields* of the
  result alias the param." Drives a field-masked drop at the self-reassign
  site.
- **Cost:** new whole-program analysis + a partial-drop emitter (drop a
  subset of a struct's fields). More machinery than A.
- **Risk:** analysis precision; recursion (`build_expr` calls itself, so
  the carried-set is a fixed point). Conservative fallback = treat all
  fields carried = no reclaim.
- **Verdict:** the most general (works without touching constructors) but
  the heaviest; A is B's special case where the "carried set" is made
  empty-of-orphans by retaining.

### C. Mutable / in-place `BState` (no orphan at all)

Stop threading `BState` by value. Make the builder mutate one `BState` in
place (or thread it as an `own` parameter mutated through `env_put`-style
in-place ops), so there is no replaced box to reclaim.

- **How:** larger self-host rewrite of the builder's calling convention;
  or an **arena** that resets `BState` scratch per compiled function
  (sidesteps per-object reclaim, fits the compiler's phase structure).
- **Cost:** the biggest change to the self-host source; touches all 57
  sites + the build functions' bodies.
- **Risk:** behavioral (the builder must stay correct — `TestSelfHost*`
  + StdTest gate); but **not** an rc-UAF risk (no deep-drop involved).
- **Verdict:** highest effort, lowest *soundness* risk; the arena variant
  is attractive because a compiler naturally has per-function lifetimes.

## Recommended sequencing

1. **Measure first.** Confirm `BState` orphans are the dominant resident
   churn (not just allocation) with a peak-RSS probe compiling a moderate
   input both ways — so the effort is justified before building analysis.
2. **Prototype A on one constructor** (`mk_bs`) behind the existing
   self-reassign path, gated on `TestSelfHostStdTestE2E` + the full
   fixpoint matrix locally. If the per-field retain is tractable and
   green, expand; if the "every field" burden proves fragile, fall back to
   **C/arena** (no deep-drop, no UAF class).
3. Treat B as the fallback general mechanism only if A can't cover the
   constructors cleanly.

## What is NOT the blocker

- **Strings** — fully rc-tracked already (`docs/RC-STRINGS-PLAN.md`); not
  the issue (#2538 proved lifting the string guard alone is unsound).
- **The allocator** — #2501/#2510/#2519 (two-tier freelist + mantissa
  classes) already reclaim freed large blocks; freeing is not wasteful.
  (See the CORRECTION below: the field-sharing orphans are in fact freed
  by the normal overwrite path — the OOM is a *separate* missing-drop leak
  for dead intermediate locals.)
- **`own`-param threading** — works (#2524 + the precise-affine model
  #2533) for *array* accumulators; `BState` is a *struct that shares
  fields through a returning constructor*, which is the harder case this
  doc addresses.

## Current measurement + diagnosis (post-#2519..#2575)

Re-measured on today's main (the numbers above predate the merged
allocator/RC reclamation arc). Method: build the SSA→asm driver
(`examples/self_host/ssa_emit_run.fern`) with `cmd/fern -target x86-64-linux`, feed it
a real module on stdin, sample peak RSS (`/proc/<pid>/VmHWM`):

| input | lines | peak RSS | exit |
|-------|-------|----------|------|
| `asm.fern` | 8263 | **44 MB** | ok |
| `wasm.fern` (6000-line prefix) | 6000 | **136 MB** | ok |
| `wasm.fern` (full) | 10187 | **935 MB** | ok |
| `parser`+`ssa`+`checker`+`wasm` (~23.6k) | — | **>1 GiB** | **137 (OOM at the 1 GiB heap)** |

The blowup is real and current. It is **not** proportional to line count
(all 8263 lines of `asm.fern` cost 44 MB; a 6000-line *prefix* of
`wasm.fern` already costs 136 MB and the full file 935 MB) — it tracks
**per-function complexity**: `wasm.fern`'s instruction-selection functions
have huge bodies, and the cost concentrates in building them.

### CORRECTION (verified by microbenchmark): the dominant orphans ARE reclaimed

Earlier revisions of this doc — and the #2538/#2568 framing — assumed the
`mk_bs`-reconstruction orphans are *never freed* and concluded we need
inter-procedural Perceus reuse-token threading. **That diagnosis is wrong.**
A microbenchmark of the exact `BState` shape (a struct with a `string`
field plus `i32[]` / `string[]` fields, updated through a returning
constructor) disproves it:

```fern
struct St { name: string, a: i32, xs: i32[], ys: string[] }
function mk(name: string, a: i32, xs: i32[], ys: string[]): St { return St { name: name, a: a, xs: xs, ys: ys }; }
function (s: St) bump(): St { return mk(s.name, s.a + 1, s.xs, s.ys); }   // shares carried fields
function main(): i32 {
    var s: St = St { name: "hello", a: 0, xs: [1, 2, 3], ys: ["p", "q"] };
    var i: i32 = 0;
    while (i < 200000000) { s = s.bump(); i = i + 1; }   // 200M field-sharing replacements
    return s.a;
}
```

`s = s.bump()` is exactly `s = s.emit(inst)` / `s = build_expr(.., s)`: a
**field-sharing functional replacement**. At **200,000,000 iterations** this
runs in ~13 s with **flat memory** (no growth, no OOM). If each replacement
leaked even the ~5-word box, 200M of them would be gigabytes and OOM long
before the 1 GiB ceiling. They don't — the **normal rc overwrite-drop**
already reclaims the orphan box: the carried fields were inc'd when the new
box was constructed, so dec'ing the old box nets to rc ≥ 1 on the shared
buffers (no free) and frees only the box. This is "framing A" (rc-balanced
deep-drop) and it is *already in the runtime* for the `x = f(x)` overwrite
form. `#2538`'s segfault came from a *separate, more aggressive*
`typeSelfDropSafe` lift that over-released shared buffers — the ordinary
overwrite path was never the problem.

### The real leak: a missing drop for dead intermediate locals

The OOM is a different, narrower bug. A `var` local that holds a heap value,
then **dies after being consumed by a borrowing call** (its value flowing
into a *different* result), is **never freed**:

```fern
function f(s: St): St {
    var t: St = s.bump();    // box1
    var u: St = t.bump();    // box2 ; t (box1) is now dead — last use was a borrow
    return u;                // box2 moves out ; box1 should be freed here, but is NOT
}
// loop: s = f(s)  — 20,000,000 iterations
```

This leaks ~one box per call: **915 MB over 20M iterations** (vs. the flat
200M-iteration overwrite loop above). The difference is the *form of death*:
an **overwrite** (`s = …`) fires the reclaim, but an intermediate `var` that
simply **goes out of scope after a borrow** does not get its drop inserted.
The reclaim here is conservative — when last-use can't be proved unique past
a call, it leaks rather than risk a double-free (sound, but unbounded).

This is the shape that dominates the self-compile. `build_expr` / `build_stmt`
create many intermediate locals (`var t = walk(...)`, `var r: BResult = …`,
nested `s = build_expr(child, s)` whose recursion holds transient state) — and
in `wasm.fern`'s large functions there are thousands of them per build. The
leak is proportional to **total intermediate locals across the build**, which
is exactly the per-function-complexity scaling observed (asm 44 MB vs wasm
935 MB).

### The fix: insert the missing drop (NOT reuse-token threading)

Because the orphan boxes are *already* reclaimable by the normal overwrite
path, the residual leak only needs the **drop for a dead intermediate local**
to be emitted at its last-use / scope-exit. That is far lighter than the
inter-procedural reuse-token machinery the previous revision proposed — and
reuse tokens would not even apply to the `f` case above (`t` and `u` are
distinct locals; there is no in-place reuse opportunity, just a drop that is
missing). Concretely, the work is in the IR's drop insertion
(`computePreciseDrops` / `computeFreeEligible` / `computeMovedLocals`): a
local consumed by a borrowing call and then dead must still receive its
freeing drop, rather than being excluded as a possibly-aliased value.

**Next step:** pin the exact exclusion in the IR that drops `box1` on the
floor (likely a "passed to a call → treat as possibly-escaped → don't free"
conservatism), add a leak-regression e2e test (peak-RSS-bounded or
alloc/free-balance counted) for the `var t = f(x); g(t)` shape, fix the drop,
and re-measure `wasm.fern` SSA-emit (baseline **935 MB → expect tens of MB**,
matching `asm.fern`). Gate on `TestSelfHostStdTestE2E` + the fixpoint matrix
from the first commit (the #2538/#2568 lesson: leaks pass the fixpoint; only
StdTest + a memory assertion catch them). Framings A/B/C above remain on
record but are now superseded for this workload — the dominant orphans need
no new analysis, and the leak is a drop-insertion fix, not a reuse feature.

**2026-07-03 — the NATIVE half landed (#4357).** The exclusion was pinned
exactly where predicted: `rhsTainted`'s `*ast.Call` case
(`internal/ir/rc_analysis.go`) propagated receiver/arg taint into the call
RESULT, so `var t = f(x)` over any param-derived `x` was permanently
free-INeligible. The fix consults `findReturnsNoParamEscape` — the existing
interprocedural, transitively slot-sensitive "every return is built from
scalars and fresh constructions" oracle (exactly the return-field-freshness
fixpoint the RC-PERCEUS-SELF-HOST-PORT Increment-A history specifies, already
trusted by the nested-call temp reclaim) — and untaints the result of a
qualifying free-function call. Regression:
`internal/e2e/rc_heap_bump_intermediate_local_test.go` (bounded high-water on
the `var t = f(x); var u = g(t)` shape + the `id(s)`-returns-param soundness
negative, x86-64 / arm64 / wasm). Residuals: METHOD callees keep the taint
(the oracle map is keyed by free-fn name), and the SELF-HOST mirror still
needs its `fresh_array_ret_fns` fixpoint — the self-host already reclaims the
annotated strict-fresh STRUCT shape (probe-verified flat), so its remaining
gap is array/map/tuple-returning producers and un-annotated bindings.

**2026-07-11 — residual re-survey (post-#4781/#4800).** Re-probed the two
native residuals above; only one is still live:

- **METHOD-callee taint — NOT a live leak (phantom).** `rhsTainted`'s method
  arm (`rc_analysis.go`, the `FieldAccess` callee) genuinely never consults
  `findReturnsNoParamEscape`, but 18 heap-bump probes across every shape
  (bound-local, straight-line, discarded, escaping-receiver, method-result-
  as-arg; scalar / rc-field struct / array returns) show every `recv.m(i)`
  form bounding identically to its `m(recv, i)` free-fn twin. The reason is
  that `computeFreeEligible`'s taint is *escape*-taint, not blanket
  borrow-taint: a receiver is only tainted when it independently escapes, and
  in that case the loop-rebind reclaim (#4733/#4734) and precise-drops already
  reclaim the fresh result. Consulting the oracle for methods would be a
  correct parity tidy-up but is not observable — struck from the live-gap
  list pending a concrete repro.
- **Map-returning intermediates — real leak, precise root cause.** `var m =
  mk(i)` and a discarded `mk(i)` both leak on native x86-64 (exit 98 on the
  `__heap_bump_bytes` fixpoint), while an inline `var m = map_new(8); m =
  m.insert(..)` loop-local bounds. Root cause: `mk` cow-threads its map
  (`m = m.insert(1, k); return m`), and `computeFreshLocals` (`ir.go`) admits
  only single-assignment locals used *only in return position* — the
  `m.insert(..)` receiver reads taint `m`, so `mk` fails `exprNoParamEscape`
  and `returnsNoParamEscape[mk]` is false. The sound fix is a cow-aware
  freshness extension (a local staying fresh through `m = m.<cowmethod>(..)`
  self-mutations), gated so no mutation stored a param-derived VALUE into the
  map — the arg-escape tracking that makes it non-trivial, and a wrong map
  reclaim is a cow-handle use-after-free, so it wants its own test-first
  change, not a drive-by. Tracked in #4357. (Landed 2026-07-18: native #5096;
  self-host #5126/#5128/#5131/#5134 — bound, loop-declared, builder-call and
  discarded forms all bounded on both compilers.)

**2026-07-18 — the deferred `wasm.fern` SSA-emit re-measure.** Same method
(`ssa_emit_run.fern` built by `cmd/fern -target x86-64-linux`, input on stdin,
peak RSS via `getrusage`), on a main with the dead-intermediate drop
(#4533), the map reclaim arc (#5096 + self-host ports), and everything
between merged:

| input | lines | peak RSS (was) | peak RSS (now) |
|-------|-------|----------------|----------------|
| `asm.fern` | 7558 (was 8263, pre-asmcore split) | 44 MB | **23.5 MB** |
| `wasm.fern` 6000-line prefix | 6000 | 136 MB | **119 MB** |
| `wasm.fern` ~baseline-size prefix | 9879 (vs 10187) | 935 MB | **2356 MB** |
| `wasm.fern` (full) | 12881 (was 10187) | — | **2404 MB** |

The "expect tens of MB" prediction did NOT materialize, and the residual is
NOT the dead-intermediate shape: the small inputs improved (44→23.5,
136→119 — the merged reclaim work is visible), while the like-for-like
~10k-line prefix got 2.5× WORSE. Localization by prefix bisection: the cost
concentrates in `wasm.fern` lines ~7000–9900 — the WAT-template
string-builder functions (`map_helpers` 389 lines, `strcat_helpers` 208,
the env/random/args/fs `*_func`/`*_helpers` families), each a huge
string-concat chain over long literals — at ~0.5 MB RSS per input LINE.
Wall time is linear (1.9 s full file) and the emitted output is only
2.5 MB, so the 2.4 GB is unreclaimed allocation churn in the driver's own
SSA build/emit of those functions, not live data and not quadratic time.
Two adjacent native micro-leaks probed while localizing (heap-bump
fixpoint): a discarded concat-chain fresh-ret result (`chunk(i).len()`,
chunk returning `"a" + p + "b" + …`) leaks ~128 B/call, and a
scope-per-iteration `s = s + lit` accumulation leaks ~96 B/iteration —
real, but 3–4 orders of magnitude too small to explain the blowup. The
dominant churn is therefore inside the SSA data-structure build for
huge-bodied functions (per-op / per-literal allocations the current drops
never reclaim) and needs its own instrumented investigation before any
fix is proposed.
