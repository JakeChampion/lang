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
- **CS2.** Consume the richer classification where the boolean was lossy — e.g.
  a `View` (slice / `.trim()`) field is retained as immortal (its inc/free are
  rc-sentinel no-ops) *by classification* rather than by the case-by-case
  `.trim()` / slice exclusions scattered through the reclaim gates. This is the
  path toward a sound struct string-field reclaim (the A2 goal) driven by the
  ownership fact instead of ad-hoc syntax checks.
- **CS3.** Migrate the remaining scattered predicates
  (`str_local_binding_is_fresh`, `str_free_producer_ident`,
  `needs_rc_inc_on_alias`'s string exclusion) to derive from the classifier;
  retire the duplicated logic once parity holds under the per-module gate.
- **CS4.** Carry `ownership` on the self-host `Ty` itself (`asmcore.fern:956`) so
  a bound local / field remembers its classification, not just the producing
  expression — the self-host analogue of C1's structural axis.

### Phase A — the `str` view type (user-facing), layered on C

- **A1.** `str` as the borrowed-string sibling of `SliceType`: `string` owned,
  `str` a `{data,len}` view. `s[a:b]: str`, `.trim(): str`, zero-copy;
  `.to_owned()` to materialise.
- **A2.** Dangling rule: a `str` may not escape unless its source is a
  parameter / `'static` (extend the existing `sliceBorrowsLocal` analysis to
  strings).

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
