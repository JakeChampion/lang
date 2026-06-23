# Iterator-bounded generics on the self-host IR path — fix plan

Status: **investigated + fix-path validated; not yet landed.** This note
captures the validated approach and the remaining work so a focused effort can
complete it without re-deriving the analysis.

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
2. Build the driver: `/tmp/fern -target x86-64 scratch/asm_load_run.fern > d.s`
   then `gcc -nostdlib -static -o alr d.s` (~3 min).
3. Decide/probe a repro:
   `echo 'import "core/iter"; function main(): i32 { var xs: i32[] = [1,2,3,4]; return iter.sum(iter.of(xs)); }' > t.fern`
   then `./alr t.fern internal/stdlib -ir-probe` (post-mono patched) to see the
   per-function `BAIL` frontier; oracle the result against `/tmp/fern -interp`.

Once `iter__ArrayIter__i32.next` and `iter__sum__...` both report `ir` and the
program matches the interpreter (10 for `sum([1,2,3,4])`), add e2e coverage
(`TestSelfHostIterBoundedIR` modelled on `self_host_u64_methods_ir_test.go`) and
run the full self-host suite before opening the PR.
