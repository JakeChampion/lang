# Ownership inference & Perceus completeness plan

Status: design done; Slice 0 (inference foundation) landed (2026-06-05).

This document captures (1) where Fern's reference-counting / Perceus
implementation actually stands, (2) how it compares to the three
reference implementations (Koka, Lean 4, Roc), and (3) the plan to
make `own` *optional* by inferring it — which, it turns out, is the
same work as finishing Perceus. It is the successor planning doc to
`RC-PERCEUS-PLAN.md` (which is the historical phase-by-phase record);
read that for the shipped mechanism, this for where we go next.

## 1. Where we are

Reclamation (`ast.RcFreeEnabled`) and constructor reuse
(`ast.RcReuseEnabled`) are both **default-on**, differentially gated
(`Test{X86_64,Arm64,WASM}FixturesFreeMatchesNoFree` and
`…ReuseMatchesNoReuse` pin free-on==free-off and reuse-on==reuse-off
byte-identical across all three backends). The guiding invariant is
**"safe leak"**: every conservative bail-out degrades to
decrement-without-free or skip-reuse — it leaks memory but **never**
over-releases. The runtime `__fern_rc_is_unique` gate is the universal
second net that keeps every in-place reuse and aggressive temp-dec
sound even when the static analysis is imprecise.

### What's shipped

- Precise-*ish* RC: freelist allocator, drop specialization
  (`__drop_enum_/struct_/tuple_`), move-on-return pair-cancellation.
- **Reuse / FBIP**: self-overwrite reuse
  (`tryStruct/EnumReuseOverwrite`), field-store elision (Koka-style
  reuse specialization at the *field* level), general cross-local FBIP
  via reuse tokens (`computeReuseSources`, widened to cross-type by
  box-class), **C1** consuming-match shallow-free, **C2** true in-place
  cons-cell reuse, and **TRMC** (`internal/ir/trmc.go`).
- Consuming functions and methods (`own`), inherent and trait-based,
  including enum-returning; `own`-aware trait conformance; sound
  use-after-move (E050) tracking incl. `dyn` dispatch.
- Cross-function temp reclaim: `returnsNoParamEscape` (+ fresh-locals),
  `ownedCallResultType`, the stage-(b) call-arg reclaim.
- Cycle-freedom **by enforcement** (E048 immutability + bottom-up
  construction) — no collector, none needed.

### The leak surface (what is intentionally NOT reclaimed)

Ordered roughly by cost. All are *safe* leaks.

1. **Borrowed / borrowed-derived buffers** — all non-`own` params and
   bindings derived from them. The borrow model passes args with **no
   caller-side inc**, so the rc undercounts them; freeing would UAF the
   live borrow. *This is the dominant structural leak:* a recursive
   traversal over a borrowed structure cannot reclaim its cells at all.
   `own` is the only escape hatch.
2. **Drops are mostly scope-exit, not last-use.** `computePreciseDrops`
   places a deep-drop right after the last top-level use for a
   straight-line subset (single-decl, unmoved, non-reassigning init),
   but a pointer-element value whose last use sits inside an
   `if/while/for/match` falls back to the function-exit sweep
   (`emitRcDecLocalsAtExit`). Peak memory is therefore higher than
   garbage-free Perceus; there is no per-arm drop placement.
3. **Map keys and non-array map values** are never reclaimed ("a later
   slice") — a permanent leak class for string-keyed maps.
4. **Non-uniform / generic / boxed-generic enum** boxes + payloads
   flat-dec (the box is only freed when variant layouts are uniform).
5. **One-level / non-uniform nested struct/enum/tuple fields** flat-dec
   their inner heap.
6. **Closure capture envs** in fallback paths; **fresh temps** in
   unhandled shapes (method results that may alias the receiver,
   pair-form returns, retain-sink/indirect-call args); **chained
   string-concat intermediates**.

### Conservative analysis gates

`computeFreeEligible` (owned-vs-borrowed taint, fixpoint),
`returnsNoParamEscape`/`computeFreshLocals` (cross-function escape),
`ownedCallResultType`+`reclaimArgTemps` (temp reclaim),
`computeMovedLocals` (move-on-return) — all sound by
over-approximation: any unknown shape ⇒ tainted/disqualified, costing
reclamation but never safety.

## 2. How we compare

Every reference implementation sits at the **inferred** end of an
*infer ↔ annotate* axis. Fern is the outlier.

| | ownership | precise drop | reuse / FBIP | concurrency | annotations |
|---|---|---|---|---|---|
| **Koka** | **inferred** (Lorenzen borrow inference) | **yes** — garbage-free | yes (the headline) | atomic option | `fip`/`fbip` are *optional static checks* that **verify** in-place, they don't enable it |
| **Lean 4** | inferred (heuristic) | reset/reuse, not garbage-free | yes (origin of reset/reuse) | **persistent/atomic split** (most mature) | borrow inferred |
| **Roc** | **inferred**, *deliberately never surfaced* | not precise; RC + rc==1 mutation | yes | single-thread focus | none — "won't move behind-the-scenes optimizations into the type system" |
| **Fern** | **explicit, required `own`** | hybrid (last-use subset + sweep) | yes (C1/C2/TRMC/field-elision) | none (non-atomic) | `own` is **required** and drives a **checked affine constraint (E050)** |

The thing none of the other three have is Fern's **checked affine
constraint**: in Koka/Roc you may use a value any number of times and
the compiler invisibly consumes it at the *last* use, so a
use-after-consume is impossible by construction. Fern instead makes the
*programmer* responsible (and errors via E050).

**Bottom line:** Koka is the most complete *precise* Perceus and sets
the bar; Lean 4 owns the multi-threaded story; **Roc is our closest
sibling** (RC + rc==1 mutation, cycles forbidden by construction) but
leans on inference where we chose explicit `own`. Fern has the
*structure* of Perceus (reuse tokens, FBIP, TRMC, drop specialization,
cycle-freedom) but trails all three on automation, drop precision,
reuse specialization, and the map/closure reclamation frontier.

## 3. The key realization

`own` is doing two unrelated jobs that pull in opposite directions:

- **(a) an optimization enabler** — the *only* way to make a recursive
  traversal reclaim/reuse (leak #1). This job *should* be inferred;
  that's pure upside (less annotation, more reclaim).
- **(b) a user-visible affine guarantee** — E050, the trait contract,
  self-documenting consuming APIs. This is a *language-identity* choice
  (Rust-like explicit vs Roc-like invisible), not an optimization.

And job (a) only *needs* `own` because we lack precise drop-at-last-use
(leak #2): if the compiler dropped each value at its true last use, a
recursive traversal would reclaim cells automatically — no annotation,
and no use-after-free for E050 to guard against.

**Therefore "get rid of `own`" and "finish Perceus" are the same
project: precise drops + ownership inference.** The destination is the
Koka/Perceus standard:

> **owned-by-default + inferred borrow**, with `own` demoted to an
> *optional* `fip`-style annotation (self-documenting APIs + an opt-in
> *checked* in-place guarantee).

Note the subtlety that forces the model flip: in today's
borrowed-by-default model the *same* syntax `match (xs)` means *borrow*
or *consume* depending on `xs`'s declared ownership — so ownership
cannot be inferred from syntax alone, because the annotation is the
input that disambiguates. Under owned-by-default, `match (xs)` always
consumes (reclaims) and borrow inference finds the cases where it can
*skip* reclaim (xs read-only). That is the clean, inferable model and
the one all three references use.

We will **not** rip `own` out entirely (full Roc): the affine
*guarantee* has real value for Fern's niche (small fast CLI tools,
short-lived edge servers live on predictable bounded memory, and an
explicit annotation gives a guarantee an invisible optimization
cannot), and our influences are Rust/Zig as much as Roc.

## 4. The plan

Sequenced so each slice leaves the tree shippable and is gated behind a
flag + a differential gate (the pattern that landed `RcFreeEnabled` /
`RcReuseEnabled` / `TrmcEnabled`). Runtime-ordering puts precise drops
first (it lowers peak memory *and* is the prerequisite that makes
inference safe), but the first *landable* slice is the inference
analysis, which is independent, pure, and de-risks the design.

- **Slice 0 — ownership-inference analysis (DONE).** A pure,
  per-function analysis classifying each pointer parameter as
  *borrow-only* (every use is a read / projection / borrowed call
  position — never returned, stored into a construction, or moved) vs
  *escaping*. Reuses the existing taint/escape primitives
  (`computeFreeEligible`, `recordExprUses`). Shipped as `inferParamEscapes` (a greatest-fixpoint
  call-graph escape analysis) + `TestInferParamEscapes` (both directions);
  no behaviour change yet. This is the core of borrow inference and the
  foundation every later slice builds on.

- **Slice 1 — per-arm precise drops (PARTIAL).** Extend
  `computePreciseDrops`' control-flow placement (slice 5) past
  primitive-element arrays. Landed: string/array-free **struct / tuple**
  locals whose last use is inside an `if/while/for/match` now drop right
  after that statement instead of at the function-exit sweep
  (`safeForControlFlowDrop` + `typeIsStringArrayFree` — being
  string/array-free keeps the deferred arm64 two-word heap-string
  reclamation path out of the early-drop window; struct/tuple fields are
  rc-counted so the early deep-drop is rc-protected). Differentially
  gated (free-on == free-off, full corpus, all backends).

  **Blocked (the real prize): ENUM precise drops — Slice 1b.** Investigated
  in depth (and reverted — see below). The FBIP list/tree case cannot take
  precise drops, and the block is the SAME root cause at *three* layers,
  all stemming from enum construction MOVING its pointer payloads without
  an rc inc (unlike `StructLit`/`TupleLit`, which inc):

    1. `preciseDroppableType` excludes `EnumType`.
    2. `computeFreeEligible` TAINTS a pointer-payload enum construction
       (`var xs = Cons(1, tail)`) — the escape analysis keeps the local
       ineligible so the moved-in payload's borrow isn't freed out from
       under. So enum-of-pointers locals are never even `freeEligible`,
       the gate *before* `preciseDroppableType`.
    3. Scalar-only enums (`enum Opt { Some(i32), None }`) sidestep (2)
       but are **pair-form** (returned/held in registers, not heap-boxed),
       so there is no box to precise-drop — moot.

  A first attempt (admit `EnumType` to `preciseDroppableType` + an
  `exprIsFresh` init guard) was **non-functional**: layer (2) rejects the
  pointer-payload enums before the new code runs, and layer (3) makes the
  scalar enums boxless. Reverted rather than land dead code.

  **Slice 1b — DONE (`EnumRcPayloads`, default on).** Enum construction now
  rc-counts its pointer payloads exactly like `StructLit`: an aliased
  payload is inc'd (`emitEnumNew`), a moved last-use owned-local payload is
  move-marked (`markConstructionMoves`' enum case), and an own-param payload
  is inc'd + balanced by the exit-sweep dec — so enum boxes carry counted
  payload references. That dissolves all three blockers in tandem: the
  escape analysis treats a variant constructor like a fresh `StructLit`
  (`escapeOwned` + `rhsTainted`→fresh), so enum locals become `freeEligible`,
  and `preciseDroppableType` admits them. The consuming match (C1/C2) is
  unchanged — it transfers the box's payload reference to the binding the
  same way regardless of how the box acquired it — and the #2026
  iterative-build reassignment skip is disabled under the flag (the inc
  needs the overwrite dec to balance it). Enums transitively containing a
  **Map** are excluded (their deep drop calls the not-everywhere-wired
  `__map_drop_values`) and keep the move model — a documented safe leak,
  losing nothing for the FBIP list/tree case.

  Soundness: a byte-identical differential gate
  (`Test{X86_64,Arm64,WASM}EnumRcPayloadsMatchesMove`) pins rc-on ==
  move-model on the full corpus, the whole e2e suite is green with the flag
  on (every backend + the self-host), and the FBIP shapes are
  underflow-zero (`TestX86_64EnumRcPayloadsSound`). The win: enum locals
  (lists/trees) now take precise drops and self-overwrite reuse.

- **Slice 2 — owned-by-default (`OwnedByDefault`).** Flip parameter
  ownership toward the Koka model: a parameter is OWNED by the callee (the
  caller retains it with an inc at the call site, the callee reclaims it
  with a dec at exit) so an ordinary reader reclaims its argument when it
  holds the last reference — no `own` needed. rc is invisible, so the
  differential gate (`Test{X86_64,Arm64,WASM}OwnedByDefaultMatchesBorrow`)
  pins owned == borrow byte-identical on the whole corpus; the reclaim is
  the only effect. Shipped per parameter-type category, exactly like
  Phase 1.

  **Sub-slice 2a — DONE (default on).** Enum parameters that are
  rc-eligible (1b), transitively string/array/Map-free, UNIFORM-boxed, and
  in a NON-TRMC function. That is the FBIP list/tree reader case (`sum`,
  `len`, tree folds reclaim their argument). Key balance points found en
  route: the caller-side inc is **alias-only** (a fresh temp is moved, its
  rc=1 transferred to the callee — inc'ing it orphans the original ref);
  the stage-(b) caller temp-reclaim is suppressed for owned args (the
  callee frees them). The whole e2e suite (every backend + self-host) is
  green with the flag on.

  **Sub-slice 2b — TRMC-as-consuming — DONE (default on).** A TRMC
  function's hole-passing loop bypasses the param exit-sweep, so 2a kept
  TRMC params on the borrow model. 2b teaches the *consume-safe* subset
  (`trmcShapeConsumeSafe`: an owned-by-default scrutinee whose recursive
  arm steals a same-enum tail and whose every other binding is scalar —
  the FBIP list-map `Cons(i32, List)` walk) to FREE each input cell as the
  loop advances. The walk is a Perceus consuming traversal: a `stillFreeing`
  flag, set until the first SHARED cell; while set, an `is_unique`-gated
  `__fern_box_free` (recycled by the freelist into the next node's alloc —
  in-place reuse), else a single `__fern_rc_dec` that balances the caller's
  retain inc and stops. The free is emitted at the END of the recursive arm
  (after every read of the box — bindings, head payloads, self-call arg
  temps — and before the param store rebinds the scrutinee to the tail), so
  it is correct regardless of whether those expressions reference the
  scrutinee. Pointer-headed cells and trees are excluded (a shallow free
  would lose a non-tail reference). Consume-safe TRMC callees become
  owned-by-default at the CALL site (`calleeParamOwnedByDefault`) while the
  definition side still skips the exit-sweep (the loop frees, not the
  sweep). Verified: the differential gate stays byte-identical; IR tests pin
  the consume sequence fires for the scalar-head list and is withheld for
  the borrow model and pointer-head lists; and a peak-memory e2e shows the
  consume high-water at ~half the borrow model (62 vs 125 cells for N=2000,
  identical on all three backends) — the in-place-reuse dividend.

  **Sub-slice 2c — structs + tuples — DONE (default on).**
  `isOwnedByDefaultType` now also admits `StructType` (backed by a real
  `StructDecl`, so runtime handles — Map/Reader/Writer/MapIter — are
  excluded) and `TupleType` when `typeIsStringArrayFree` (which
  transitively rejects string/array/slice/Map, the not-fully-wired
  deep-drop fields). Fern struct fields are **immutable** after
  construction, so — like enums — there is no in-place mutation for the
  caller-side retain inc to disturb; no copy-on-write concern. Boxes are
  uniform by construction (no variants), and per-field/element rc counting
  (Phase 1e) balances the deep drop; the reclamation machinery (`emitDec`
  struct/tuple branches, `rcTracked`, `computeFreeEligible`, call-site
  `emitAliasInc`) was already type-generic, so this is purely a gate
  widening. `TestStructReuseSkipsBorrowedParam` became flag-aware (an owned
  param's box IS reused in place under owned-by-default — is_unique-gated);
  new e2e soundness + bounded-heap guards on all three backends; the
  differential gate stays byte-identical.

  **Sub-slice 2d — borrow inference — DONE (default on,
  `BorrowInferEnabled`).** The headline optimization that makes `own`
  redundant: a parameter the escape analysis (`inferParamEscapes`, Slice 0)
  proves cannot escape the callee is kept BORROWED rather than owned — the
  caller skips the retain inc and the callee skips the exit dec, since a
  balanced inc/dec pair on a non-escaping value can only change rc traffic,
  never observable behaviour. A fresh-temp arg passed to a borrowed reader
  is reclaimed by the caller's existing arg-temp path (`freshOwnedRcTempType`
  / `ownedCallResultType`). `paramBorrowable(fn, i)` (escapes[fn][i] ==
  false) is consulted on BOTH the definition side (`paramOwnedByDefault`)
  and the call site (`calleeParamOwnedByDefault`) so they agree — except a
  **consume-safe TRMC callee**, which always frees its scrutinee in the
  loop and so stays owned at the call site regardless of escape facts (the
  precedence fix that avoids a double free: the caller must not also reclaim
  a cell the loop already freed). Verified: the differential gate
  (`Test{X86_64,Arm64,WASM}BorrowInferMatchesOwned`) is byte-identical on
  the whole corpus; IR tests pin that a pure reader loses both its
  caller-side incs and its callee-side reclamation while an escaping
  (returned) param stays owned.

  **Remaining sub-slices (optional, later):** admit array/string/
  non-uniform-payload enums and string-containing structs/tuples into
  owned-by-default (more reclaim coverage; borrow inference already skips
  the inc/dec for the non-escaping readers among them via the borrow
  fallback).

- **Slice 3 — `own` optional + the `fip` checked guarantee — DONE
  (first cut).** With borrow inference driving reclaim, `own` is no longer
  *required* for ownership — it stays as an optional annotation that still
  carries the affine E050 use-after-move discipline (decision: keep `own` +
  E050 as-is, an opt-in checked assertion; borrow inference is what made it
  optional, so no semantics change was needed). The new `fip function`
  modifier is the **checked guarantee**: a contextual parser modifier
  (`fip function …` / `pub fip function …`, `fip` still usable as an
  identifier) that the checker verifies (E053) performs **no heap
  allocation** — a sound, conservative subset of Koka's fully-in-place.
  Verify-don't-enable: the in-place lowering (reuse, the COW
  unique-in-place branch, TRMC) already happens; `fip` only asserts and
  checks it. Allowed: scalars/ops, field+index reads, control flow,
  rebinding locals, **in-place index writes to an `own` array param** (the
  COW unique branch — no copy), and calls to other `fip` functions +
  whitelisted non-allocating builtins (`len`). Rejected: array/tuple/struct
  /payload-enum literals, string concat/interpolation, writes through a
  non-`own` heap value, and any call not proven allocation-free. Dogfooded:
  `std/sort`'s `sort_i32_inplace_asc/_desc` are now `fip` (they sort the
  owned buffer in place, allocating nothing). Parser + checker tests both
  directions; `internal/diag/explanations/E053.md`.

  **Future relaxation:** Koka's `fip` also permits allocation fully matched
  by reuse (an FBIP `map` that reuses every cell). The current cut is
  strictly allocation-free (the stricter, simpler guarantee); the
  reuse-paired relaxation (`fip(n)` / `fbip`) is a follow-up.

- **Later (independent of the above):** map key/value reclamation
  (leak #3), non-uniform/generic enum deep-drop (#4), nested-field deep
  drop (#5), reuse *specialization* (static function cloning for the
  token-available caller).

## RESOLVED: the O(N²) self-reassign accumulator leak

**Resolution (instrumentation-led, two genuine uncounted-alias bugs).** The
`RcFreeDebug` + widened-deep-drop driver under gdb pinpointed the failure as
predicted — not a field-type axis but two specific uncounted aliases, both
**borrow-inference gaps** exposed only once the deep-drop actually frees the old
value:

1. **Threaded borrowed param over-released on overwrite.** The self-host
   accumulators (`BState`, `EmitState`, `CheckCtx`, the SSA `SFunc`) are not
   *locals* but *parameters* THREADED through their owning function (`s =
   s.emit(..)`, `ctx = check_stmt(.., ctx)`, `f = SFunc{..}`). Because their
   string field excludes them from owned-by-default, the borrow model kept them
   borrowed — yet the reassignment-overwrite *dec'd* the old value with **no
   caller-side inc**, undercounting it so a later `is_unique`-gated drop freed it
   while the caller still held it. Fix: `computeConsumedParams` promotes such a
   reassigned, drop-wired, string/array-bearing struct/tuple param to
   **callee-owned** — not borrow-tainted (so it becomes `freeEligible` and the
   overwrite / exit-sweep deep-drop it) plus a single **entry-inc** in
   `lowerFunc` so the first overwrite-dec balances. Purely callee-internal: the
   call ABI is unchanged (`calleeParamOwnedByDefault` stays false), so the
   owned-by-default differential gate is untouched. This is the O(N²)→O(N) fix
   (verified bounded on x86-64; the `-ssa` emit no longer OOMs).

2. **`push` of a borrowed pointer element stored uncounted.** The SSA optimiser
   rebuilds blocks by `ninsts.push(ins)` where `ins` reads from the *old*
   block's `insts`. `emitArrayPush` stored the element pointer without an inc, so
   the new buffer held an uncounted alias; deep-dropping the old `SFunc` then
   over-released the shared `SInst`. Fix: `emitArrayPush` inc's an **aliased**
   pointer element (`needsRcIncOnAlias && !moveSites`), mirroring the
   array-literal / struct-field element inc — a fresh element is still moved in.

A third, enabling fix: **`genStructDropFn` now dec's native single-word
(x86-64) string fields** (it previously handled only the two-word wasm/arm64
ABI), matching the inline exit-sweep and `appendChildDrop`. Without it a
string-bearing struct routed through the *generated* drop (nested field /
overwrite / consumed param) leaked its string buffers forever.

Gated on the full self-host suite + the full e2e + the differential gates (all
green); regression tests in `internal/e2e/rc_self_reassign_field_test.go`
(`TestX86_64SSAAccumThreadedParam` pins O(N) + underflow-zero for the
threaded-param shape, plus arm64 / wasm soundness counterparts). The historical
diagnosis and the two failed type-shape widenings are kept below.

### Known limitation: wasm doesn't *reuse* the threaded-param struct accumulator

The fix restores O(N) on the **native** backends (x86-64 verified; arm64 shares
the target-agnostic IR). On **wasm** the threaded-PARAM *struct* accumulator
(`build(s: Bld, n)` with a growing array field) stays O(N²) — `TestWASMSelf...`
pins only soundness there, not O(N). Characterised (bump high-water, free-on):

| shape | local | param |
|---|---|---|
| bare `i32[]` (no struct) | O(N) | **O(N)** |
| struct, box only (no growing field) | O(N) | **O(N)** |
| struct, **fixed**-size array field, rebuilt each step | **O(1)** (reused) | **O(N)** (freed, not reused) |
| struct, **growing** array field | O(N) | **O(N²)** |

It is **pre-existing and independent of this fix** (reproduces with the
consumed-param entry-inc disabled) and is **not** an over-release — the
deep-drop *frees* the old box+buffer on wasm (the fixed-array param is linear,
not quadratic). The gap is purely **reuse**: a struct accumulator threaded
through a *local* has its freed blocks reused by the wasm freelist (O(1) for the
fixed shape), but the same accumulator threaded through a *parameter* does not —
even for identical-size allocations every step. So it is neither a size-class
mismatch nor an in-place-push issue; it lives in the wasm freelist /
reclamation path's local-vs-param asymmetry (cf. the documented "an inline
box_free in a complex function body doesn't return the box to the freelist on
wasm, but a generated function does"). A real fix needs that wasm-allocator
investigation; it is out of scope for the `-ssa` OOM, whose target is native.

## Original problem: the O(N²) self-reassign accumulator leak

**Symptom.** `s = s.emit(x)` — self-reassigning an owned struct *local* through
a method/call — flat-dec's the old value (frees the box, **orphans its nested
array/struct heap**). In the self-host SSA builder
(`s.cur.insts.push(inst)` threaded through method calls rebuilding the builder
each step) this leaks the old block's instruction buffer on every `emit` →
**O(N²) peak memory**, which is what makes the self-host `-ssa` full-program
emit OOM.

**Shipped (sound, partial).** `selfReassignOwnedLocal` + `typeSelfDropSafe` now
deep-drop the old value on self-reassign **only when the type is transitively
string- and Map-free** — turning O(N²)→O(N) for string-free accumulators
(`St{cur: Blk{insts: i32[]}}`: wasm bump 382→1388→5275 ⇒ 26→50→98). The
self-host's hot accumulators (`BState`, `EmitState`) carry a `string` field, so
they're **excluded — the `-ssa` OOM is NOT yet fixed.**

**Two FAILED widening attempts (both unsound, both caught by the self-host
suite — heed this).**
1. Bypass `freeEligible` and deep-drop any self-reassigned local → over-released
   (PR #2159, reverted #2160). The local can be aliased by another live binding.
2. A string-skipping ("nostr") deep-drop family that reclaims structural heap
   but never dec's a string → **still** segfaulted the self-host (caught
   pre-commit). This disproved "strings are the axis": excluding string-bearing
   structs only *dodges* the failing class; those structs' **non-string** fields
   (some borrow-derived / uncounted **array or nested-struct** alias) are what's
   actually over-released.

**The lesson + the next step.** This cannot be fixed by reasoning about which
field *types* are safe — every synthetic reproduction is correctly counted, yet
the self-host over-releases at scale. The failing **uncounted alias must be
pinpointed at runtime**: build the self-host driver with `ast.RcFreeDebug`
(poison freed blocks, trap on use-after-free) + the widened deep-drop, run a
self-host test, and read the backtrace to the exact over-released allocation —
then fix *that* aliasing site (likely a borrowed array/struct flowing into a
struct field without an inc, i.e. a string-rc / borrow-inference gap), and
re-gate on the **full self-host suite + e2e** (the gate that caught both
attempts). Do NOT ship another type-shape-reasoned widening.

## References

- Reinking, Xie, de Moura, Leijen. *Perceus: Garbage Free Reference
  Counting with Reuse* (PLDI 2021).
- Lorenzen, Leijen, Swierstra. *FP²: Fully in-Place Functional
  Programming* (ICFP 2023) — the `fip`/`fbip` checked-guarantee model.
- Lorenzen. *Optimizing Reference Counting with Borrowing* (MSc thesis)
  — borrow inference, no annotations.
- Ullrich, de Moura. *Counting Immutable Beans* (IFL 2019) — Lean's
  reset/reuse + inferred borrow.
- Roc — *Functional* / *FAQ* (the deliberate no-uniqueness-types
  stance) + *Reference Counting with Reuse in Roc* (thesis).
