# Iterator-bounded generics on the self-host IR path — LANDED

Status: **DONE.** `core/iter.sum` / `count` / `to_array` / `product` over
`iter.of(xs)` now route the self-host **IR path** (`decide = ir`) and match the
interpreter. Gated by `TestSelfHostIterBoundedReducersIR`; see the 2026-06-23
FEATURE-AUDIT entry for the shipped summary. The historical analysis below is
retained for context.

## What actually shipped (the final blocker, resolved)

The deep dive below correctly isolated the `[T]`→`[i32]` cascade but mis-attributed
the final `ArrayIter__i32.next: BAIL`. Empirical instrumentation (dumping the bail
point in `lower_func` + the failing struct-literal in `lower_expr`) pinned **two**
residual issues the plan had not: (1) the cloned `next`'s return type carried a
**bare, un-module-prefixed** `ArrayIter[i32]` (finalize bakes the source name;
`mg_ty` matches the prefixed `iter__ArrayIter`), and the **body** struct literal
stayed at the generic `iter__ArrayIter` (no key inference for `self.xs`); and
(2) once `sum` cloned, its GAS label `iter__sum__iter__ArrayIter[i32]` had raw
brackets that the assembler rejects (so the program never even ran — the "1" was
gcc's exit code). The shipped fix is the four `parser.fern` changes in the
FEATURE-AUDIT entry: targeted promotion (`feeds_user_parametric` +
`token_at_paren_depth0`), clone-time `Self` resolution
(`replace_struct_ident` + `retarget_self_lit_stmts`), tuple-aware
`subst_ty`/`mg_ty`, and `sanitize_key` on bounded-generic clone names. The
promotion is **targeted** (not promote-all) precisely to avoid the 512-budget
regression the plan flagged — and it had to be narrowed twice (exclude
return-only / function-param `T`, then exclude built-in `Option`/`Result`/`Map`
bases) to keep `find` / `reduce` / `nth` / the array-method surface erased.

---

(Historical) Status: **deeply investigated; 5 monomorphiser layers fixed + validated; one
final lowering blocker remains (`ArrayIter__i32.next` body).** Not yet landed —
a multi-layer change that resisted rapid iteration. This note captures every
validated layer and the precise remaining blocker so a focused effort can finish
without re-deriving the analysis.

## Progress (post-monomorphisation `-ir-probe` on `iter.sum(iter.of(xs))`)

Baseline (no fixes): `iter__ArrayIter__T.next: BAIL` (bogus `[T]` clone),
`module: AST`. After the layers below, the probe reads:

```
iter__sum__iter__ArrayIter[i32]: ir        <- FIXED (was BAIL / bogus [T])
iter__ArrayIter__i32.next: BAIL lower       <- the one remaining blocker
module: AST
```

i.e. the whole `[T]`→`[i32]` cascade is gone and `sum`'s clone lowers; only the
cloned `next` body still won't lower.

## The five validated monomorphiser layers (all in `parser.fern`)

1. **Promote unbounded type params** — the func-header loop drops unbounded
   `[T]` (erasure); append them to `bounded_tps` so `of[T]` monomorphises. (For
   production this must be TARGETED — only when `T` feeds a parametric struct —
   to avoid the 512-budget regression on the bootstrap self-compiles.)
2. **`call_ret_type`** in `mono_infer`'s `ExprCall` arms — substitute a generic
   callee's inferred type args into its return type, so `iter.of(xs:i32[])`
   infers `ArrayIter[i32]` and `sum` then infers `I = ArrayIter[i32]`.
3. **Tuple-aware `subst_ty`** — add a `(...)` branch recursing into each element;
   without it `T`/structs nested in a tuple (`Option[(T, Self)]`) are never
   substituted.
4. **Tuple-aware `mg_ty`** — same `(...)` branch, so a generic struct nested in a
   tuple gets its clone-name mangling (`(i32, ArrayIter[i32])` →
   `(i32, ArrayIter__i32)`).
5. **Clone-time `Self` resolution** — in `clone_struct_method`, `subst_self(.., mang)`
   on the return + param types (resolve nested `Self` → the concrete clone name).
   IMPORTANT: do it at clone time, NOT in `finalize_impl_method` — rewriting
   `Self` there perturbs the return-type *registry* the caller (`sum`) reads, and
   regresses `sum`'s clone to BAIL. Clone-time keeps `finalize` (and the registry)
   untouched, so `sum` stays `ir`.

Also attempted: seeding the receiver (`self → mang`) into the `ms_stmts` env that
processes the clone body (line ~5300, the `ms_stmts(clm.body, …)` call) so
`ms_expr` can type `self.xs` and mangle the body's struct literal. This did NOT
clear the `next` bail on its own.

## The remaining blocker: `ArrayIter__i32.next` body won't lower

A hand-written CONCRETE equivalent lowers fine (proves the body *shape* is
supported):

```fern
struct AI { xs: i32[], i: i32 }
function (self: AI) nxt(): Option[(i32, AI)] {
    if (self.i < self.xs.len()) { return Some((self.xs[self.i], AI { xs: self.xs, i: self.i + 1 })); }
    return None;
}   // decide=ir, runs correctly
```

So the blocker is residual non-concreteness in (or an unsupported shape of) the
CLONED body. Two hypotheses were TESTED and did NOT clear the bail:

- Seeding the receiver (`self → ArrayIter__i32`) into the clone body's `ms_stmts`
  env (so `ms_expr` can type `self.xs`) — no change.
- Substituting type params in the body's struct-literal `type_name`
  (`subst_ty(sl.type_name, …)` in `subst_expr`, so `ArrayIter[T]{}` →
  `ArrayIter[i32]{}` for `ms_expr` to mangle) — no change.

So the residual issue is NOT (only) the struct-literal mangling. The blocker
must be pinned empirically: instrument `irlower.fern`'s `lower_func` / `lower_stmts`
to print the kind of the FIRST statement/expr whose lowering returns `ok=false`
when lowering `iter__ArrayIter__i32.next`, and/or dump the cloned `next` FuncDecl
(receiver_type, ret_type, body) right after `clone_struct_method` +
`ms_stmts` to see exactly what the lowering is fed. The concrete `AI.nxt` above
lowers, so a direct text diff of the cloned `next` against `AI.nxt` will reveal
the residual non-concrete token. Only then is the final fix knowable; guessing
from the body shape has been exhausted (5 layers fixed, `sum` lowering, `next`
still BAIL).

## Reproduce

Build a scratch driver with the five layers (patch `parser.fern`), patch
`asm_load_run.fern`'s `-ir-probe` to run on
`parser.module_with_builtins(merged)` (post-mono), then:
`./alr t.fern internal/stdlib -ir-probe` on
`import "core/iter"; function main(): i32 { var xs: i32[] = [1,2,3,4]; return iter.sum(iter.of(xs)); }`.
Oracle against `/tmp/fern -interp` (10). When `ArrayIter__i32.next` reports `ir`,
add `TestSelfHostIterBoundedIR` (model on `self_host_u64_methods_ir_test.go`),
make promotion targeted, and run the full self-host suite before the PR.

## The gap

`core/iter.sum` / `count` / `to_array` and `std/num.sum_iter` / `product_iter`
route the **legacy AST fallback** (`decide = ast`) instead of the IR path. These
are the bounded-generic Iterator reducers, e.g.:

```fern
pub function sum[I: Iterator[i32]](it: I): i32 {
    var total = 0; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { total = total + t.0; cur = t.1; }, None => { go = false; } } }
    return total;
}
```

driven over `iter.of(xs)` where `of[T](xs: T[]): ArrayIter[T]` and
`impl[T] Iterator[T] for ArrayIter[T]`.

## Root cause (merged: see FEATURE-AUDIT 2026-06-22 root-cause entry)

The self-host parser **type-erases UNBOUNDED generic params** (`of[T]` →
`type_params = []`; uniform 8-byte ABI, one body fits every instantiation —
`parser.fern` func-header parse, the `if (has_bound)` gate). Only **bounded**
params (`[I: Iterator[i32]]`) are kept for the monomorphiser. So `of` reaches
monomorphisation with no type params but a return type that still literally
spells `ArrayIter[T]`. That dangling `T` cascades:

1. `mono_infer(iter.of(xs))` returns the unsubstituted `ArrayIter[T]`, so the
   bounded generic `sum` is cloned at a bogus `ArrayIter[T]`
   (observed `II iter__sum key=iter__ArrayIter[T]`).
2. `mg_ty` (struct-instantiation collector) sees `ArrayIter[T]` and mints an
   `ArrayIter__T` struct+method clone whose `next` body carries a generic `T`
   and never lowers (`iter__ArrayIter__T.next: BAIL lower` → module AST).

## Validated fix path (parser side) — eliminates the `[T]` cascade

Three coordinated changes in `examples/self_host/parser.fern`. Built and probed
with a post-monomorphisation `-ir-probe` (patch `asm_load_run.fern`'s `-ir-probe`
to run on `parser.module_with_builtins(merged)` instead of bare `merged`).

1. **Promote unbounded type params to the monomorphiser.** In the func-header
   type-param loop, append the param name to `bounded_tps` even when it has no
   trait bound. (Diagnostic used "promote all"; the PRODUCTION version must be
   **targeted** — see Risks.)

2. **`call_ret_type`** (new helper, used in `mono_infer`'s `ExprCall` arms for
   both `ExprIdent` and the `mod.fn` `ExprFieldAccess` form): look up the callee
   FuncDecl; if generic, `infer_inst` its type args from the call args and
   `subst_ty` them into its return type. So `iter.of(xs:i32[])` infers
   `ArrayIter[i32]`, and `sum` then infers `I = ArrayIter[i32]`.

3. **`subst_self`** (new helper) used in `finalize_impl_method`: a deep,
   token-aware `Self → impl_type` rewrite for the return type AND param types.
   The existing rewrite only handled `rt == "Self"` (whole type), not nested
   `Option[(T, Self)]`. Note: Fern strings don't support `>=`/`<=`; classify
   bytes via `string[i]` (yields an i32 byte, per the lexer's `is_alnum`).

**Verified effect of 1+2:** the post-mono probe goes from
`iter__ArrayIter__T.next: BAIL` to correctly-keyed `iter__ArrayIter__i32`, and
`II iter__sum key=iter__ArrayIter[i32]` (concrete). `sum`'s clone lowered with
just 1+2.

## Remaining work (not yet solved)

- **`iter__ArrayIter__i32.next` still `BAIL lower`.** The cloned method body
  returns `Option[(i32, Self)]` (an Option of a tuple of (scalar, struct)). The
  IR lowering of that *return* shape from a cloned receiver method does not yet
  succeed. Constituent shapes lower individually (Option-of-tuple `match`;
  returning `Some((i32, struct-literal))` from a free fn; trait-bound generic
  over a NON-generic struct) — but this exact cloned-method-return combination
  does not. This needs **dedicated `irlower.fern` work**, not just parser
  changes.

- **`subst_self` interaction.** With `subst_self` added on top of 1+2,
  `iter__sum__...ArrayIter[i32]` *also* started to `BAIL lower` (it was `ir`
  with just 1+2). The Self rewrite changes a type spelling that `sum`'s clone
  lowering depends on; the three changes must be reconciled (likely
  `subst_self` should target only the genuinely-dangling nested `Self`, or the
  lowering must accept the rewritten spelling). Re-derive with the post-mono
  probe after each change.

## Risks / production considerations

- **512-function IR budget (#3425).** "Promote all unbounded generics" will
  monomorphise every generic function, inflating function count — the big
  bootstrap self-compiles (`TestSelfHostBootstrapsItself`, Stage2) could exceed
  the budget and regress to AST. The production promotion must be **targeted**:
  promote an unbounded `T` only when it appears as a type-argument to a
  *parametric struct/enum* in the signature (so the struct monomorphiser needs a
  concrete `T`), and keep erasing purely-opaque `T` / `T[]` params. Deciding
  "parametric struct" needs the struct table, so do the promotion in a
  post-parse pass (or thread the struct names in), not at parse time where the
  names alone are available.

- **Gate on the full self-host suite** (`internal/e2e` self-host x86_64 + arm64
  + wasm), not just the iter cases — these changes touch the monomorphiser that
  every generic program flows through.

## How to iterate (reproduce the probe)

1. `go build -o /tmp/fern ./cmd/fern`; copy `examples/self_host/*.fern` to a
   scratch dir; patch as above.
2. Build the driver: `/tmp/fern -target x86-64-linux scratch/asm_load_run.fern > d.s`
   then `gcc -nostdlib -static -o alr d.s` (~3 min).
3. Decide/probe a repro:
   `echo 'import "core/iter"; function main(): i32 { var xs: i32[] = [1,2,3,4]; return iter.sum(iter.of(xs)); }' > t.fern`
   then `./alr t.fern internal/stdlib -ir-probe` (post-mono patched) to see the
   per-function `BAIL` frontier; oracle the result against `/tmp/fern -interp`.

Once `iter__ArrayIter__i32.next` and `iter__sum__...` both report `ir` and the
program matches the interpreter (10 for `sum([1,2,3,4])`), add e2e coverage
(`TestSelfHostIterBoundedIR` modelled on `self_host_u64_methods_ir_test.go`) and
run the full self-host suite before opening the PR.
