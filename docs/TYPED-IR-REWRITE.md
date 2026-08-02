# Typed-IR rewrite: feed lowering the types the checker already computes

Status: in progress, Phase A. Tracking issues: #5531 (the mechanism), #5986
(finish the migration). See "Carriers landed" below for what is annotated today.

## TL;DR

`irlower.fern` re-infers the type of nearly every expression at lowering time, by
walking the AST with ~28 structural predicates and ~20 per-module string-keyed
registries. The compiler already has a real type system in `checker.fern` that
computes exactly these types (`check_expr(e: parser.Expr, s: Scope): Type`,
checker.fern:1451) - and then throws them away, because the `Expr` AST nodes carry
no type field. The fundamental fix is to **annotate resolved types onto the IR and
have lowering read them instead of re-deriving them.** This is a plumbing change,
not a from-scratch type system: the hard part (the type system) already exists.

## The problem, with evidence

State-of-the-art compilers type-check once into a typed IR (Rust HIR->MIR, Swift
SIL, MoonBit, Roc, Cranelift's frontend), where every value carries its resolved
type, then lower by *reading* those types. Lowering is mechanical and *total*.

This compiler does the opposite:

1. **The checker computes types and discards them.** `check_expr` returns a `Type`
   (`TypeI32 | TypeArray | TypeStruct | TypeTuple | TypeFunc | TypeMap | ...`) for
   every expression, uses it to validate, and returns only diagnostics. The
   `parser.Expr` variants (`ExprBinary { op, left, right, line, col }`, etc.) have
   **no type field**, so nothing downstream can see the result.

2. **Lowering re-derives all of it.** `irlower.fern` (39.8k lines) carries ~28
   re-inference predicates that reconstruct types by structural AST walks:

   ```
   expr_is_str expr_is_f64 expr_is_f32 expr_is_u32 expr_is_u64 expr_is_bool
   expr_is_result expr_is_tuple expr_is_nonscalar expr_is_cell expr_is_arr_src
   expr_is_strarr expr_is_f64arr expr_is_i64arr expr_is_u32arr expr_is_u64arr
   expr_is_closure_elem expr_is_fresh_str expr_is_bare_row_read
   infer_expr_width expr_scalar_type expr_recv_prim_type expr_struct_type
   expr_enum_type expr_map_type_tag expr_tuple_elem_tag method_recv_tyname
   try_opt_type
   ```

3. **Plus ~20 per-module string-keyed registries**, re-derived every compile via
   ~105 `*_ret_fns_of` passes, threaded through a 75-field `LowerState`:
   `opt_ret_fns str_ret_fns arr_ret_fns strarr_ret_fns f64_ret_fns i64_ret_fns
   tuple_ret_fns struct_ret_fns map_ret_fns arr_method_fns const_fns closure_fns
   closurearr_ret_fns fn_param_sigs closure_opt_rets ...`. Each maps a name or
   `"Type.method"` string to a re-derived type fact.

## Why this is the root cause, not a symptom

Nearly every fragility and bug worked recently traces to it:

- **Duplicate resolvers.** `match (f(x))`'s Option-type recovery exists in TWO
  places - the inline resolver in `lower_stmt_match` AND `try_opt_type` - so the
  same gap had to be fixed twice (#5529, #5530). With a typed IR there is one
  typed value to read.
- **Coarsening + sidecars.** The parser coarsens `(i32) => Option[i32]` to the
  string `"fn"`, then sidecar fields (`ParamDecl.fn_ret`, `fn_param_dyn`,
  `LowerState.closure_opt_rets`) laboriously recover what was discarded. A
  structured type never loses it.
- **The bail-to-AST model (fragility #1).** Because lowering is a partial
  re-derivation, a predicate that couldn't resolve a type bailed the whole
  function to a separate AST emitter (`asm.fern`/`asm_arm64.fern`/`wasm.fern`).
  Those three are now **deleted** and every backend routes IR-or-error (#3457),
  so an unresolved type is a hard error naming the bail site rather than a
  silent fall-through to a second answer. That converts the failure mode from
  "wrong output" to "no output" — better, but still one defect per missing arm,
  which is what the annotation removes.
- **The type-reinference "soup" (fragility #3).** Documented directly: the
  predicates independently re-derive overlapping facts and drift apart.
- **Cost.** ~105 per-module derivation passes + O(depth) re-walks per expression
  is a real runtime and memory tax (related to the #3425 live-set wall).

## The rewrite

### Phase A - typed HIR (the high-value core)

1. **Annotate.** Add a resolved `Type` to expression nodes (a `type` field on each
   `Expr` variant, or a parallel typed-HIR produced by the checker). The checker
   already computes it in `check_expr`; this just stops discarding it.
2. **Consume.** Rewrite lowering to read `expr.type` instead of calling the
   predicates. `expr_is_f64(e)` becomes `e.type is TypeFloat{width:64}`;
   `try_opt_type(e)` becomes `e.type is TypeOption`. Each predicate, once every
   caller reads the annotation, deletes. Most of the 20 registries and the 105
   derivation passes delete with them (the checker's scope already resolved names
   to typed symbols).

Result: the predicate soup, the duplicate resolvers, the coarsening sidecars, and
most of `LowerState`'s width go away. Lowering becomes a mechanical typed->IR
translation.

### Phase B - value/SSA IR (separable, second)

The IR today is a linear **stack-op** sequence (`op_load_local` / `op_store_local`
/ `op_bin`), which is why "values remaining on stack" is a reachable *malformed
output* bug class (fragility #4, hit by the Cell[i64] bug). Move to an SSA/value
graph (Cranelift/MoonBit/LLVM-shaped). This eliminates the stack-imbalance class
and is what makes register allocation and the Perceus RC pass clean (inc/dec/drop
and reuse analysis are liveness questions SSA answers for free - directly on goal
2's critical path).

## Migration: incremental, byte-identity-guarded, not big-bang

This is an ~900-function shared lowering pinned by byte-identical self-compile
fixpoints. It must migrate one seam at a time:

1. Add the `Type` annotation; leave every predicate in place (no behaviour change).
2. Migrate ONE predicate to read the annotation (e.g. `expr_is_f64`), keeping the
   old walk as a fallback/assert. Run the fixpoints - output must stay
   byte-identical. Repeat per predicate.
3. When a predicate has no non-annotation caller, delete it and its registries.
4. Phase B (SSA) is a distinct project after Phase A stabilises.

A seam already exists: #5519 extracted `expr_scalar_type` as the single scalar
classification chokepoint precisely so it can later read a real type instead of
re-walking. The numeric-predicate unifications (#5523/#5524) narrowed the surface
further. Continue widening these seams.

### Carriers landed

`checker.annotate_module` walks the checked tree and stamps a `ty` tag —
`type_to_irtag(check_expr(e, s))`, the canonical string spelling irlower already
keys on — onto the node types below. It runs after `check_module` and before
every emit path, so `-decide`, the eligibility judgement and the emit all see the
same annotated tree. A driver that skips it (`asm_ir_run`, the native compiler)
leaves every `ty` empty and gets the structural walk unchanged.

| node | landed | consumers reading it |
|---|---|---|
| `ExprCall.ty` | #5531 | `expr_struct_type`, `expr_map_type_tag`, `expr_tuple_elem_tag`, `try_opt_type`, `expr_is_str` / `_f64` / `_u32` / `_u64`, `infer_expr_width` |
| `ExprFieldAccess.ty` | #5986 | `fa_type_tag` — the single leaf behind `expr_struct_type`, `expr_map_type_tag`, `infer_expr_width` and `expr_is_f64` / `_f32` / `_u32` / `_u64`; plus `cap_type_expr` at lift time |

Two ordering facts constrain how a carrier is read, and both cost a debugging
session to rediscover:

- **The declaration walk wins where it resolves; the tag fills its holes.** The
  checker's `Type` has no `f32` (`TypeFloat` carries no width), so a stamped tag
  reports a genuine `f32` field as `"f64"` and would take `expr_is_f32`'s
  dispatch off the f32 path. The declared spelling is strictly more precise, so
  `fa_type_tag` consults it first and falls back to `fa.ty` only when it yields
  `""`. `ExprCall.ty` has no such twin (there is no declaration to read), which
  is why #5531 could trust its tag first.
- **Annotation runs before monomorphisation** (`module_with_builtins` →
  `monomorphize_module`), so a generic function's body is annotated in its erased
  form: a field whose type is a bare type parameter stamps `""` and the clone
  inherits that. Measured across the fixture corpus, that is essentially the only
  place a field read goes unstamped.

## Sequencing and cost

- **After goal 1 (retire the AST emitters, #3457).** Do the typed-IR rewrite
  against ONE lowering, not while reconciling a new typed IR against three legacy
  AST backends. The small IR-gap fixes shrinking the AST-fallback surface now
  (#5529, #5530, ...) are the enabling work.
- **Multi-week, high regression risk.** The byte-identical fixpoints are the
  safety net; the incremental per-predicate migration keeps each step verifiable.
- **Not a moonshot.** The type system is written and running (`checker.fern`,
  8.97k lines). This is annotate-and-consume, not build-from-scratch. That is the
  single fact that makes it worth doing.

## Explicitly NOT this

- Sea of nodes. Its payoff is aggressive optimisation (GVN, code motion), which is
  not the pain here (path divergence, re-inference, stack imbalance, RC insertion
  are). Its graph-soup debugging cost is real against a byte-identity constraint.
  A conventional typed HIR + linear-then-SSA MIR fits the actual problems.
