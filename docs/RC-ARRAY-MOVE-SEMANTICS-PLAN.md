# Move semantics for threaded array params — the SSA self-compile memory unblock

## Status

Planning doc. The allocator + owned-array reclamation work that motivated it
has landed (#2501 two-tier large-block freelist + `i32[]` self-reassign free;
#2506 `string[]`/`struct[]` self-append; #2510 arm64 parity; #2519 mantissa-bit
classes). This doc specifies the **one remaining piece** that lets the
whole-compiler **SSA self-compile** fit in memory.

## The problem, in one paragraph

A `cmd/fern`-built self-host compiler compiling **itself through the SSA IR**
needs a **>2 GiB working set** and traps (OOM, exit 137) at the 1 GiB heap.
The AST path fits at ~726 MB. The gap is **not** allocator waste and **not**
the owned-array churn that #2501–#2519 fixed — it is the self-host compiler's
**borrowed-array threading**: functions that take an array parameter, grow it
(`p = p.append(x)` / `p = f(.., p, ..)`), and **return** it
(`ops = build_stmt(ops, ..)`, the instruction/block/value lists in
`ssa.fern`). Every such reassignment orphans the old buffer, and the RC model
**cannot free it** because the parameter is *borrowed* (the caller, by the
borrow ABI, still holds a reference the callee can't see counted).

## How we got here (the three layers already fixed)

| Layer | Symptom | Fix |
|------|---------|-----|
| Allocator | freelist was exact-fit, capped at 2048 B — any block >2 KiB leaked | two-tier freelist, large blocks reclaimed by rounded-capacity class (#2501 x86, #2510 arm64; #2519 mantissa-bit) |
| IR — owned locals | `a = a.append(x)` on an owned local used a non-freeing `__fern_rc_dec` | route the overwrite through buffer-only `__fern_arr_dec` for self-reassigned / self-append locals (#2501, #2506) |
| IR — **borrowed params** | `p = p.append(x)` where `p` is a borrowed param leaks every grow | **this doc** |

A probe isolates the layers: a build-1000-element-array-and-drop loop went
691 MB→flat after #2501; a `string[]` append loop flat after #2506. But a
loop threading an array **through a returning function** still leaks ~one
buffer per call, and that is what the self-host does pervasively.

## Why borrowed array params can't just be freed

`p = p.append(x)` on a borrowed `p`:

- **In-place append needs `rc == 1`** (unique ownership) to mutate without
  corrupting another holder. A borrowed param is **not** uniquely owned — the
  caller holds a reference the callee's rc doesn't count — so freeing the old
  buffer at the overwrite is a **use-after-free** of the caller's value.

Two attempts that **don't** work (both verified):

1. **Free it anyway** (the broad `selfReassignOwnedLocal`-without-restriction
   experiment): **segfaults** the self-compile — exactly the borrowed-buffer
   UAF above.
2. **Promote to callee-owned via `computeConsumedParams`** (the struct/tuple
   mechanism, extended to arrays): adds a callee entry-`__fern_rc_inc`, which
   makes `rc ≥ 2`, so the first `append` **copies** (preserving the caller's
   buffer) and subsequent grows free. Correct, **but**:
   - the entry-inc defeats in-place append → an append **loop** copies every
     iteration → **O(N²)**; and
   - the self-host's params are threaded **and returned**, so escape analysis
     re-taints them (`freeEligible == false`) → the promotion is a **no-op**
     for the dominant pattern.

   Measured: a threaded-**not-returned** param bound to 7 MB (promotion
   worked); the threaded-**and-returned** probe still leaked (392 MB at 100k).

The irreducible tension: **in-place mutation and safe freeing of a *borrowed*
buffer are incompatible.** The only clean resolution is to stop borrowing —
**move** the array into the callee so it is uniquely owned.

## The fix: move-and-rebind for `own` array params

The language already has `own` parameters (`p.Own`, parsed as `own p: T[]`)
and a call-site ownership guard (`E051`). An `own` array param is uniquely
owned by the callee, so `p = p.append(x)` mutates **in place** *and* the
overwrite frees grow intermediates — **no entry-inc, no forced copy, no
O(N²)**. The blocker is purely at the **checker**:

> `E051` (`internal/checker/checker.go:4476`, predicate `isOwnedExpr` at
> `:4425`) rejects passing a **local var** to an `own` param — only a fresh
> construction or another `own` param qualifies. So `x = f(.., x, ..)` (move
> `x` in, get it back) can't be written.

The self-host's threading is exactly the **move-and-rebind** shape
`x = f(.., x, ..)`: `x` is passed to `f` *and* is the assignment target, so
the old `x` is consumed and immediately replaced — a textbook move with no
use-after.

### Four changes (each small, ordered, independently testable)

1. **Checker — accept a move-and-rebind local as an `own` arg.**
   Extend `isOwnedExpr`/`guardCallArgs` so an `*ast.Ident` naming a **local**
   (not a borrowed param, not an alias of one) passed to an `own` param is
   accepted **iff** the enclosing statement is `local = call(.., local, ..)`
   (the ident is both the assignment target and the moved arg) **and** the
   local has **no later read on any path** after this statement (reuse the
   existing flow-sensitive `moved` walk that already powers `E050`
   use-after-move for `own` params, extended to track local moves).
   Conservative: anything not provably a clean move-and-rebind stays `E051`.

2. **IR — suppress the moved-out local's drops.**
   A local moved into an `own` param transfers ownership to the callee; its
   scope-exit sweep drop **and** the reassignment-overwrite drop of the *old*
   value must be **elided** (the new value returned by the callee is what the
   slot now owns). Without this it's a double-free. Mirror the existing
   `movedLocals` handling that already suppresses drops for moved values.

3. **Callee side — `own` array param is uniquely owned.**
   Confirm (and test) that an `own` array param takes **no** entry-inc and is
   `freeEligible`, so `p = p.append(x)` is in-place when `rc == 1` and the
   overwrite frees grow intermediates via `__fern_arr_dec`. This is the payoff
   — O(N) build, O(1) retained per call.

4. **Self-host annotation pass.**
   Mark the threaded array params `own` in `ssa.fern` (and `asm.fern` /
   `asm_arm64.fern` where they thread arrays): `build_stmts`, `open_block`,
   `set_env`, the instruction/block/value list threads. **Invariant:** every
   call site of an `own`-param function must move (be a move-and-rebind), or
   `E051` fires — which is the desired compile-time check, not a silent
   miscompile.

### Soundness test matrix (gate every step)

- **Use-after-move rejected**: `var a = [...]; var b = f(a); g(a)` → `E050`.
- **Aliasing safe**: `a = pick(a, a)` (returns the receiver) stays
  value-correct with `__rc_underflow_count() == 0` (the move-checker must see
  the alias and *not* treat it as a clean move, or borrowed-return-retain must
  keep rc≥2).
- **No double-free**: moved-out local not dropped at scope exit
  (`__rc_underflow_count() == 0`).
- **Bounded heap**: the threaded-and-returned probe (`build(300)` via an
  `own`-param `thread`, ×100k) flat across iteration counts via
  `__heap_bump_bytes()`.
- **Not O(N²)**: an `own`-param append **loop** stays in-place (one buffer,
  not N copies) — assert bump bytes ≈ one array, and wall-time linear.
- **The milestone**: whole-compiler SSA self-compile completes under ~1 GiB.

## Why this is the right shape

- It reuses existing machinery (`own` params, `E050`/`E051`, `movedLocals`)
  rather than inventing a new ownership mode.
- It is **opt-in per call site** via the source annotation, so it can land
  incrementally (annotate one self-host hotspot, measure, expand) and never
  silently changes a program that doesn't use `own`.
- The risk is **soundness of the move-checker** (a hole = UAF). Keeping the
  accepted shape narrow (exactly `local = call(.., local, ..)` with no later
  use) and gating on the test matrix above contains it. This is why it is a
  deliberate, reviewable feature rather than a quick allocator-style patch.

## Out of scope / alternatives considered

- **Arena-per-compile-unit** (reset scratch after each function): fits a
  compiler's phase structure but is a larger architectural change to the
  runtime + self-host driver; revisit if move semantics proves insufficient.
- **General `x = f(x)` move inference without `own`** (whole-program, infer
  the move + agree across all call sites): more powerful but needs
  whole-program agreement; the explicit `own` annotation is the tractable
  first cut.
