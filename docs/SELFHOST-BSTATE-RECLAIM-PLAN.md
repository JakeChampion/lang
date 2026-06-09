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
  classes) already reclaim freed large blocks; the problem is the orphans
  are never *freed*, not that freeing is wasteful.
- **`own`-param threading** — works (#2524 + the precise-affine model
  #2533) for *array* accumulators; `BState` is a *struct that shares
  fields through a returning constructor*, which is the harder case this
  doc addresses.

## Current measurement + diagnosis (post-#2519..#2575)

Re-measured on today's main (the numbers above predate the merged
allocator/RC reclamation arc). Method: build the SSA→asm driver
(`examples/self_host/ssa_emit_run.fern`) with `cmd/fern -target x86-64`, feed it
a real module on stdin, sample peak RSS (`/proc/<pid>/VmHWM`):

| input | lines | peak RSS | exit |
|-------|-------|----------|------|
| `asm.fern` | 8263 | **44 MB** | ok |
| `wasm.fern` | 8413 | **784 MB** | ok |
| `parser`+`ssa`+`checker`+`wasm` (~23.6k) | — | **>1 GiB** | **137 (OOM at the 1 GiB heap)** |

The blowup is real and current; recent RC work didn't tame it. The 18×
spread between two similarly-sized modules (wasm 784 MB vs asm 44 MB) is a
per-function-complexity churn, not flat overhead.

### The churn is `mk_bs` reconstruction

Every `BState` update routes through ONE constructor, `mk_bs(name, nparams, …,
seed)` (14 fields). The 15 update methods each call it with **13 carried
`s.field` args + 1-2 changed**, e.g.

```
function (s: BState) emit(inst: SInst): BState {
    var nc = …;
    return mk_bs(s.name, s.nparams, …, nc /*changed*/, s.var_names, …, s.seed);
}
```

So `s = s.emit(inst)` (and `s = build_expr(.., s)`, recursively per expr node)
allocates a fresh `BState` box and orphans the old one — millions of small
(~14-word) boxes over a large module's SSA build. The carried array/string
fields are shared with the new box (inc'd at `mk_bs`'s construction), so the
orphan is dominated by the *boxes*, not their payloads.

### Why the obvious reclaims don't apply

- `tryStructReuseOverwrite` (Fern's FBIP struct-reuse) fires only for a
  **literal at the assignment site** (`p = T{ f: v, … }`). The `BState` churn is
  a **call** (`s = s.emit(inst)` / `s = build_expr(.., s)`) whose `BState{}`
  literal lives inside `mk_bs`, behind the call — out of reach.
- Lifting `typeSelfDropSafe` to deep-drop the orphan at the overwrite
  **segfaulted** (#2538): `mk_bs`'s result shares fields the deep-drop
  over-releases.
- Affine local-tracking to move the threaded value into an `own` callee was
  **fuzzer-fragile** (#2568): rc locals are non-linear; the affine checker
  false-positives unboundedly across `fernsmith`/fuzz/differential.

### The Perceus-aligned fix: reuse-token threading

Koka/Lean reclaim exactly this shape with a **reuse token**: when a unique value
is dropped and a same-size value constructed, the allocation is reused in place.
For `BState`, the caller's unique old `s` flows as a reuse token into `mk_bs`,
whose `BState{}` reuses it — an in-place field update of the box, no orphan.
This is **inter-procedural** reuse-token passing (the token crosses the
`s.emit(…) → mk_bs` call); Fern's reuse primitive (`__fern_alloc_reuse`)
supports the leaf but the analysis does not yet thread tokens through calls.

Baseline to beat: **wasm.fern SSA-emit 784 MB → flat box reuse**; the
self-compile must clear the 1 GiB heap. Gate any attempt on
`TestSelfHostStdTestE2E` + the fixpoint from the first commit (the #2538/#2568
lesson). Larger alternatives — inlining `mk_bs` at all update sites to expose
the literal to `tryStructReuseOverwrite`, or making `BState` mutable — are
bigger self-host rewrites; the reuse-token route is the smallest IR-level change
and matches how Koka/Lean do it.
