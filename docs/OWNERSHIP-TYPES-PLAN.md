# Ownership Types Plan (issue #4297)

Turning the accepted conclusion of issue #4297 into an executable, phased
implementation. **Goal:** lift the owned / borrowed / view / static distinction
out of scattered heuristics and runtime sentinels and into a *checked type
fact*, so the checker can enforce what today only convention and runtime rc
tags maintain.

## Why (the motivating bug)

PR #4294 fixed a corruption where the asm-IR backend built *some* string boxes
with an rc header and one producer (`str_slice`) without — incrementing a
header-less view clobbered the neighbouring heap block. The type checker never
had a chance: at the language level a slice-view, a heap string, and a static
literal are all one `string` type. "Does every producer of `string` agree on
the same ownership / rc discipline?" is a *codegen invariant*, not a *type
invariant*, so it lives below where the checker can see. Today that invariant
is upheld by a stack of syntactic heuristics plus a runtime rc tri-/quad-state
(inline SSO / static-immortal / heap / view). This plan makes ownership a thing
the type system carries and the checker enforces.

## What already exists (build on it, don't reinvent)

Ownership reaches the type layer today only in three narrow places; otherwise
it is inferred into side-tables or encoded as runtime sentinels.

Type-level precedents:
- `ArrayType` vs `SliceType` — `internal/ast/ast.go:108` / `:119-128`. `[T]` is
  already a documented **non-owning view** into an `Array<T>`, "distinct from
  owned `T[]` so the API surface signals 'this borrows' without a borrow
  checker." This is the template to generalise.
- `Param.Own bool` — `ast.go:2374`. The source-level `own x: T` affine /
  consuming annotation, checked (E050); default params are borrowed.
- `HandleType{ Borrowed bool }` — `ast.go:394`. WIT resource handles carry
  `own R` vs `borrow R` in the type (erased to i32 before backends).

Inferred, stored off the type:
- Native borrow inference `inferParamEscapes` (`internal/ir/ir.go:3173`) — a
  monotone interprocedural fixpoint; result is a side-table keyed by
  fn → per-param bool, consumed as `paramBorrowable`. Rides `OwnedByDefault`
  (`ast.go:1048`).
- Self-host `borrowable_params_interproc` / `consume_safe_params_interproc`
  (`examples/self_host/irlower.fern:18573` / `:18664`) — the same idea as
  `string[]` registries computed once per module.

Heuristic stand-ins for owned-vs-view (strings), the fragile surface:
- `expr_is_fresh_str`, `str_local_binding_is_fresh`, `slot_is_reclaimable_str`,
  the `is_str` slot flag, and the `i >= n_params` "params are borrowed" rule in
  `examples/self_host/irlower.fern`. These explicitly exclude literals, bare
  aliases, slices, and `.trim()` — exactly the cases a `view` type would
  classify by construction. Native strings copy on slice (`__str_slice`), so
  the *view* hazard is a self-host-only phenomenon today.

## The axis

Four values, orthogonal to a type's *shape*:

| Ownership | Meaning | Rust analogue | RC discipline |
|-----------|---------|---------------|---------------|
| `Owned`   | this binding owns a counted reference | `String` / `Vec<T>` | inc on alias; drop dec/free |
| `Borrowed`| a caller/owner elsewhere owns it | `&T` param | never inc, never drop |
| `View`    | aliases another value's backing store | `&str` / `&[T]` | never freed; source outlives it |
| `Static`  | interned / immortal | `&'static str` | never freed |

At runtime the string box already encodes this: rc `1` (owned), `>1` (shared),
`<0` (immortal — view or literal). Option C makes the compile-time type carry
the same fact so the backend can't disagree with itself.

## Sequencing

Per #4297: **Option C first** (internal ownership-tagged types, no surface
syntax), **then Option A** (a user-facing `str` view type), **park Option B**
(a general borrow modifier / lifetimes — against the "infer, don't annotate"
direction). Each slice below is independently shippable and validated.

### Tracked sub-issues (#4297 children)

The remaining phases are tracked as GitHub sub-issues under #4297 so the work is
visible outside this doc:

- **CS4** → **#4812** — carry `ownership` on the self-host `Ty` (not just the
  producing expr).
- **A1** → **#4813** — introduce the user-facing `str` view type.
- **A2** → **#4814** — the `str` escape/dangling rule.

Landed slices (C1–C3, CS1, CS2) have no open issue. C4–C6 (native RC rewire /
enforcement) are deprioritized and intentionally not filed — see the Phase C
notes below. CS3 (`string[]`-element reclaim) is the ownership-axis framing of
work already tracked in **#4355** (string RC on the IR path) and is cross-
referenced there rather than duplicated. Adjacent (non-axis) RC leaks live in
**#2704** and **#4357**.

### Phase C — internal ownership axis (native compiler first, the reference)

- **C1 (foundation, additive, no behaviour change).** Introduce `ast.Ownership`
  (`Owned | Borrowed | View | Static`) and `ast.StructuralOwnership(Type)
  Ownership` — the *default* ownership implied by a type's shape (`SliceType` →
  `View`, scalars → `Owned`/N/A, everything pointer-shaped → `Owned`). Pure
  addition + unit tests; nothing reads it yet. **Landed (#4302).**
- **C2 (producer classifier, additive).** `ast.ExprResultOwnership(Expr, Type)`
  — the ownership an expression *produces*, resolved from syntax + type (string
  literal → `Static`; string slice → `Owned` copy; array slice → `View`; fresh
  construction / call → `Owned`; else the type's structural default). This is
  the typed counterpart of the self-host `expr_is_fresh_str` heuristics — the
  input the checker-enforcement (C5) and the self-host port (CS2) consume.
  Binding-precise refinement of a bare identifier (borrowed param vs owned
  local) needs the symbol table and is folded into C3. **This PR.**
  - *Deferred (was C2): folding `Param.Own` into the enum.* On inspection this
    is low-value churn — `Param` is overloaded for params AND struct fields with
    dozens of literals that omit `Own`, so a field swap flips the zero-value
    default (borrowed → owned) across un-audited sites, and `Param.Own` reads
    cleanly as a bool. Ownership on params is surfaced instead via C3 (the
    inference result), where it carries real information.
- **C3 (binding-precise resolution, additive).** `ast.ExprResultOwnershipWith`
  — the classifier with a `resolve(name) (Ownership, bool)` hook, so a bare
  identifier read reflects its binding's ownership (a borrowed param, an owned
  local, a `View` local) instead of the type's structural default. The producer
  cases are unchanged; only the identifier fallback consults the resolver. This
  is the mechanism the next slice's inference-fed resolver plugs into. **This
  PR.**
- **C4–C6 (native RC rewire + native enforcement) — DEPRIORITIZED.** On
  inspection the native compiler's ownership facts are *already cleanly
  factored*: `Param.Own` (the affine annotation), `paramBorrowable` (the
  `inferParamEscapes` result, `ir.go:9143`), and the per-shape RC gating are
  distinct, non-redundant queries. Routing them through the axis would be a
  byte-identical **rename** with nothing to consolidate, on mature,
  already-memory-safe code — high regression surface, ~zero payoff, and native
  copies string slices so there's no view hazard for a checker rule (C6) to
  catch. The value of the axis is realised where the representation is *messy* —
  the self-host (below), not here. Revisit native enforcement only if a concrete
  native ownership gap surfaces.

### Phase C-selfhost — port the axis to the self-host (where the value is)

The self-host is where ownership is reconstructed from the fragile
`expr_is_fresh_str`-style heuristics + runtime rc sentinels that caused #4294 and
blocked the A2 struct-field reclaim three times. This is the phase that earns the
axis its keep. Each slice **must** be validated with
`TestSelfHostModloadPerModuleWholeCompilerX86_64` (runs the built compiler —
catches the corrupting UAFs that byte-identity fixpoints miss), not just the
fixpoints.

- **CS1 (classifier, behaviour-identical).** `irlower.str_producer_ownership(e)`
  — the self-host counterpart of `ast.ExprResultOwnership`, classifying a
  string-producing expression as Owned / Borrowed / View / Static (codes
  mirroring native `ast.Ownership`). `expr_is_fresh_str` is redefined as exactly
  `== Owned`, so every caller is byte-for-byte unchanged; the View / Static /
  Borrowed distinctions (lost by the old boolean) are what CS2 consumes.
  Validated: the per-module run gate + the Load/Modload x86 fixpoints, both
  green. **Landed (this PR).**
- **CS2 (struct string-field reclaim — the A2 goal, LANDED).** A `string` field
  of a reclaimable, non-escaping struct local is now reclaimed on the struct's
  drop, on all three backends. Construction retains (rc_inc) a non-fresh field
  and hands over a fresh one with no inc; the per-type `__struct_drop`'s k_str
  arm frees it via the rc-aware `__fern_str_free` (free rc==1, dec rc>1, skip an
  immortal view/literal rc<0). The classification comes from `str_producer_owner
  ship` (CS1), and — crucially — via the **precompute unblock** the root-cause
  section prescribes: `field_ownerships(slit) -> i32[]` runs once per struct-lit
  (cold), so `lower_expr` consults only scalars and never re-reads a field-value
  expression, keeping its hot locals taint-free (no emit runaway). Validated: the
  per-module run gate, the Load/Modload x86 fixpoints, a dedicated x86 reclaim
  test (fresh field freed + aliased field balanced, 2M cycles, underflow 0), and
  a wasm struct-drop churn case (emission + 400k cycles bounded under a 16 MiB
  cap). Gated NARROW — only structs already reclaimable via an rc-array / nested
  field get a `__struct_drop`, so a string-only struct stays leak-only (avoiding
  the `slot_is_reclaimable_struct` broadening that OOM'd an earlier attempt).
  Extended to **nested** string fields: `nddo_reach` now treats a `string` field
  as deep-drop-worthy, so a container's drop deep-drops a nested string-only
  struct (rc_is_unique-gated) and its `__struct_drop`'s k_str arm reclaims the
  string — closing the leak for a string-only struct nested in a reclaimable one.
  And a base-copied string field (`R { ...base, … }`) is retained so the drop
  only decs the alias (no over-release).
- **CS3 (`string[]`-element reclaim) — NEXT, needs element-string RC first.** A
  `string[]` struct field (IR-eligible via `decl_is_leaksafe_d`) still leaks BOTH
  its buffer AND its element strings — it is no reclaim kind in
  `emit_ir_struct_drop_one`. A `k_strarr` drop arm mirroring `k_box` (walk the
  buffer, but `__fern_str_free` each element instead of `__fern_arr_dec`, then
  free the buffer) is the shape. **Blocker:** freeing element strings on the
  drop is only sound if each element is sole-owned, but the `rc_is_unique` gate
  guards the *buffer*, not element aliasing — a shared string sitting in a
  uniquely-owned array would be over-released. So this needs **element-string
  RC** first: retain (`rc_inc`) an aliased string when it is pushed / built into
  a `string[]` (the array-element analogue of the struct-field construction
  inc), so the per-element `__fern_str_free` only decs the dup. That is a
  genuine, separate slice (touches array construction / `__fern_arr_push`), not
  a session-tail add-on. Keep it NARROW (only `string[]` fields of
  already-reclaimable structs; no `struct_has_reclaim_array_field` broadening)
  to avoid the `slot_is_reclaimable_struct` OOM.
- **CS4 (#4812, LANDED).** Carry `ownership` on the self-host type layer so a
  bound local remembers its classification, not just the producing expression —
  the self-host analogue of C1's structural axis + C3's binding resolver.
  Implementation note: the axis lives on **`LocalInfo.str_own`** (irlower's
  per-slot record), NOT on asmcore's `Ty` — irlower does not import asmcore
  (`Ty` serves the legacy AST emitters slated for retirement, `LocalInfo` is
  the IR path's type carrier), and the issue's additive-preference points the
  same way. Codes mirror `str_producer_ownership` / native `ast.Ownership`
  (0 Owned / 1 Borrowed / 2 View / 3 Static); every slot defaults **Borrowed**
  (the conservative never-free class — params, container-element binders, and
  IIFE temps stay there). Seeded in `lower_var` from the new
  `str_binding_ownership` (producer classification; an ident init propagates a
  View/Static source verbatim and demotes an Owned source to Borrowed — an
  alias never acquires ownership); read back via `str_own_slot` and the
  read-side `str_expr_ownership` (ident reflects the slot fact VERBATIM, the
  native `ExprResultOwnershipWith` contract). Observable via the driver's
  `-str-own` dump (`irlower_run.fern`), pinned by
  `TestSelfHostStrOwnDump` (`internal/e2eselfhost/self_host_strown_dump_test.go`)
  — the self-host analogue of native `ownership_test.go`. No codegen decision
  consults the facts yet (behaviour-identical; fixpoints + the per-module run
  gate pin that); CS3 (#4355) and the reuse-analysis slice are the consumers.
- **Not doing:** migrating `str_local_binding_is_fresh` / `str_free_producer_
  ident` to derive from `str_producer_ownership` — inspection shows genuinely
  different admit-sets (syntactic-vs-typed, box-freshness-vs-ownership, even
  `i32_to_string` differs), so it is not a clean behaviour-preserving refactor;
  forcing it would risk the fragile reclaim path for ~no gain.

## Root cause of the A2 struct-field-reclaim runaway (resolved)

The A2 attempt (reclaim struct string fields) failed three times; the third,
undiagnosed wall was a **~2–3 GB/s allocation runaway** that OOM-killed the
per-module emit of the `irlower` module. It is now root-caused (gdb-sampled +
bisected), and it is **not** a flaw in the reclaim inc/dec logic.

**Mechanism.** A2's construction gate adds, in the hot `lower_expr`
`ExprStructLit` path, an *extra read* of the field-value expression
`slit.field_values[found]` (to classify its freshness — whether via
`expr_is_fresh_str`, a `match`, or a bound local). An array-element / match /
field access is an **aliasing** read: the native Perceus taint analysis
(`computeFreeEligible`, `ir.go:4520`) marks its result and — through backward
propagation — surrounding locals as *tainted* (borrowed-derived → "retained
without an inc, so the owner must not free"), i.e. **not free-eligible**. Those
locals then leak on *every* `lower_expr` call. `lower_expr` runs millions of
times emitting `irlower` (the largest module), so the per-call leak accumulates
to gigabytes → OOM. Bisection is decisive: the one variant that does **not**
read `slit.field_values[found]` in the added gate (a plain always-inc) passes
cleanly; every variant that reads it — match, bound local, or borrow-arg to a
predicate — OOMs, regardless of the inc/dec decision.

This is precisely **"Leak #1: borrowed / borrowed-derived buffers"** from
`OWNERSHIP-INFERENCE-PLAN.md` — the conservative "safe leak" the ownership /
Perceus work exists to shrink. So A2 is blocked *by the compiler's own
incomplete reclamation of borrowed-derived values*, not by anything about
string fields — which is why the type-driven ownership approach (this plan), not
another ad-hoc reclaim attempt, is the right unblock.

**Concrete unblock for a future A2 (CS-phase).** Do not read the field-value
expression a second time inside `lower_expr`. Instead compute the per-field
ownership in a **single separate pass** — a helper
`field_ownerships(slit, s) -> i32[]` called *once per struct-lit* — and have
`lower_expr` consult the precomputed `i32[]` (scalars, which don't taint) while
the existing single `lower_expr(slit.field_values[found], …)` read *moves* the
value (no taint). That confines the aliasing reads to a cold helper instead of
tainting the hot `lower_expr` locals, so the driver stops leaking. CS2/CS3
should build the reclaim on top of `str_producer_ownership` (CS1) via this
precompute shape.

### Phase A — the `str` view type (user-facing), layered on C

- **A1 (#4813) — slice 1 LANDED (native surface).** `str` as the borrowed
  view of `string`. Representation decision (recorded on #4813): a **distinct
  `ast.StrType`**, NOT `SliceType{Elem: u8}` — a string view's runtime shape
  is the #4294 immortal rc=-1 *string box*, not the slice `{data,len}` fat
  pointer, so `str` lowers exactly like `string` and is **erased to
  StringType at the LowerWith choke point** (`ir/erase_str.go`, mirroring
  HandleType erasure; no backend sees it). Spelled as a **contextual type
  name**, not a lexer keyword (std/log's `.str` method + `str` identifiers
  keep working). Semantics: `string` freely borrows INTO `str` (var init,
  argument, element); `str` flows into `string` *parameters* (borrowed-by-
  default positions, `argAssignable`); `str` never silently promotes into an
  owning sink (var/field/return — strict `assignable`); `.to_owned()`
  (std/string, `s + ""` — fresh-owned on every path) materialises the copy;
  `str` shares the whole `string` method surface (`methodTypeName` maps it).
  `StructuralOwnership(StrType) = View` — the C1 axis's first surface
  citizen. Coverage: parser + ownership + checker tests, `TestInterpStrView`
  + `TestX86_64StrView` e2e. **Deferred to later A1 slices:** producers
  (`s[a:b]: str`, `.trim(): str` — the zero-copy flip, gated behind A2), the
  `own`-param tightening, and self-host compiler acceptance of `str`
  (native-only surface for now — a #4451 debt entry).
- **A2 (#4814, LANDED).** Dangling rule — **E065**, the `str` sibling of
  E063: a `str` view may not escape via `return` unless its source is a
  parameter (caller-owned) or a string literal (`'static` / immortal); a view
  of a function-LOCAL owned `string` is rejected (`checkStrEscape` /
  `strViewsLocal` in the checker — the cycle-guarded binding-chase mirroring
  `sliceBorrowsLocal`). Call results are not chased (today every callee
  returns an owned `string`, a move — the same documented intraprocedural
  hole as the slice rule; both tighten together with the producer flip).
  `return` is the only checked escape position, matching the E063 precedent.
  This answers #4297's open question for the current model: immutability +
  bump-arena + "views don't escape their source" suffices without lifetime
  annotations, at the cost of the conservative local-source rejection.
  **Landed since:** self-host `str` acceptance (#4856 — parse-boundary
  erasure in `parse_type_name`, pinned byte-identical by
  `TestSelfHostStrViewErasure`) and the `own`-param tightening
  (`argAssignable` takes the callee's per-param `own` flag from
  `Info.OwnFuncs` / the trait `Param.Own`; a view never flows to a
  consumer). **Landed since (the producer flip):** P1 flipped the
  `.trim()` family (`std/string` trim/trim_start/trim_end return `str`);
  P2 flipped `s[a:b]` itself — slicing an owned `string` returns a `str`
  sub-view (checker `SliceExpr` case), after two migration batches made
  every consumer in the tree flip-clean (batch 1: stdlib + examples,
  #4891; batch 2: the self-host compiler sources, #4900 — 231 sites
  enumerated as the pristine-vs-flipped checker error delta, read-only
  bindings annotated `str`, owning sinks materialised via `+ ""`). E065's
  chase gained the `SliceExpr` case with the flip (returning a slice of a
  local is a dangling view; slicing a param stays fine). **Remaining
  A-phase work:** P3 backend zero-copy convergence (native `__str_slice` +
  wasm produce the #4294 view box instead of copying), gated by the
  byte-identity differentials + the per-module run gate; call-result
  chasing in E065/E063 (views laundered through a `str`-returning callee)
  tightens with it.

## Validation discipline (learned from #4294 / the A2 attempts)

- Native slices: the existing `internal/ir` + `internal/checker` + `rc_*`
  differential suites, byte-identity where behaviour must not change.
- Self-host slices: **always** the per-module run gate, not just fixpoints —
  deterministic corruption passes byte-identity fixpoints.
- Every slice ships with tests at the layer it touches (checker rule → checker
  test; type addition → unit test; runtime behaviour → e2e).

## Non-goals

- No lifetimes / general borrow checker (Option B), unless the language
  identity shifts toward Rust-like explicitness.
- No removal of `own` — it stays as an optional affine guarantee (the
  edge/CLI niche wants it), demoted from required to optional, per
  `OWNERSHIP-INFERENCE-PLAN.md`.
