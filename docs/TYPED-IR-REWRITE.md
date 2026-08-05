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
| `ExprIndex.ty` | #6165 | `ix_type_tag` — the leaf behind the `ExprIndex` arms of `expr_is_f64` and `infer_expr_width` AND the two load sites (`lower_expr`'s `arr_get` width, `lower_i64`'s `arr_get_i64`), so the element width and the value's downstream type answer from one place |
| `ExprSlice.ty` | this slice | the `ExprSlice` arm of `lower_expr` — both the `expr_is_arr_src` **gate** (a non-empty tag proves array-ness the walk cannot reach) and `slice_elem_is_wide` (the `arr_slice` element width). Names the SOURCE array's type, via the checker's `type_to_arrtag` |

**Some holes need a consumer, not a carrier.** `mk()[i].N` — a tuple element
behind an index of a call result — lowered the element as a 4-byte i32, failing
the wasm validator on an f64. The cause was not a missing annotation:
`expr_tuple_elem_tag`'s `ExprIndex` arm resolved the element tuple type from the
slot's `arrarr_elem`, which only exists for a NAMED `(tuple)[]` local, and
returned `""` for a call result. `ExprIndex.ty` had held the checker's answer for
that node since #6165; the consumer simply never read it. Wiring it was three
lines and needed no new field.

Worth checking for before adding a carrier: the cheapest remaining wins in this
migration are consumers that never learned to read an annotation already in
place.

**Not every carrier belongs in the shared tag vocabulary.** `ExprSlice.ty` needs
an ARRAY spelling (`"f64[]"`), and `type_to_irtag` has no `TypeArray` arm — it
returns `""` for every array-valued expression. Adding one there is the obvious
move and the wrong one: four consumers (`expr_is_str` / `_f64` / `_u32` /
`_u64`) read `c.ty` **tag-first**, short-circuiting their structural walk on any
non-empty value, so teaching the shared namer to name arrays silently changes
what they see on every array-valued call. The carrier instead gets its own
`type_to_arrtag`, used at exactly one stamp site and read by one walk-first
leaf. Widening the shared vocabulary is a separate decision from adding a
carrier, and should be made on its own evidence.

A third ordering fact, learned wiring `ExprIndex.ty`: **a carrier must reach the
LOAD site, not just the value predicates.** Wiring `expr_is_f64` alone made
`var v: f64 = (if (c) { [1.5] } else { [2.5] })[0]` type as f64 downstream while
still emitting a 4-byte `arr_get` — the two halves disagreed and the wasm
validator rejected the module outright ("expected f64, found i32"). The width
decision and the type decision have to share the leaf, which is why
`lower_expr`'s `gw` and `lower_i64`'s ExprIndex arm route through `ix_type_tag`
too. Pinned by `TestSelfHostAnnotateIndexIR_X86_64`.

**A carrier must survive every rebuild, and monomorphisation is a rebuild.**
`mono_expr` / `ms_expr` / `me_expr` (parser.fern) rebuild every expression when
cloning a generic function, and they originally rebuilt an `ExprIndex` with
`ty: ""`. #6165 recorded that as a benign residual on the theory that a clone
falling back to the structural walk merely loses precision. That was wrong: it
is a **miscompile**, and one the path probe cannot see — the module still routes
`ir`, the compiler still exits 0, and the clone reads an f64 element as a 4-byte
i32. `pick[T]`'s `pick__i32` clone emitted wasm the validator rejects.

These same three sites also drop `unchecked`, and that stays dropped. The
asymmetry is the point: losing the bounds-elide mark is **conservative** (the
clone keeps its bounds check), losing the type tag is **not**. "This rebuild
already discards a field, so discarding one more is consistent" is exactly the
reasoning that produced the bug.

The tag stays valid across cloning because annotation runs on the ERASED form —
a bare type parameter stamps `""`, so only concrete tags survive to be copied,
which is the same reason `subst_expr` already carried it. Pinned by
`TestSelfHostAnnotateIndexMonoIR_X86_64`.

### A carrier is only as good as the checker behind it

#6165 shipped with a known sibling gap: the f64 shape
`var v: f64 = (if (c) { [1.5, 2.5] } else { … })[1]` lowers, but the **i64**
shape `var v: i64 = (if (c) { [7000000000, 9000000000] } else { … })[1]` still
bails the IR path — while the interpreter, the semantic oracle, evaluates it
fine. Chasing that down produced the finding worth recording here, because it
bounds what the whole annotate-and-consume migration can deliver.

Instrumenting `ix_type_tag` shows the tag is not *missing* for the i64 shape.
It is **wrong**: the checker stamps `i32`. The cause is one line —
`check_expr`'s `ExprNumber` arm returns `t_i32()` for every non-float literal,
with no magnitude test and no context sensitivity:

```
parser.ExprNumber(n) => {
    if (n.is_float) { return t_float(); }
    return t_i32();
},
```

The native compiler does something categorically different: an unsuffixed
integer literal parses **polymorphic** (`NumberLit.Width == 0`, see
`internal/parser/parser.go`'s suffix switch) and a later settling pass fixes its
width from context, so `var v: i64 = <literal>` settles the literal to 64. Only
a typed suffix (`42i64`) pins the width at parse time. The self-host checker has
no settling pass at all.

Three consequences:

1. **This is not an irlower gap and no carrier can paper over it.** A tag
   carries whatever the checker concluded; when the conclusion is wrong, the tag
   propagates the wrong answer faster. The migration's ceiling is the checker's
   precision.
2. **The structural walk is still load-bearing as a guard, not just a
   fallback.** `ix_type_tag` consulting the walk FIRST is what makes the wrong
   `i32` tag harmless here — it matches neither the f64 nor the i64 branch, so
   the shape keeps its old (bailing) behaviour instead of silently lowering a
   truncating 4-byte read. A tag-first leaf would have turned a bail into a
   miscompile. This is a second, independent reason for the ordering rule above,
   which was originally justified only by f32/f64 precision.
3. **Literal settling in the self-host checker is its own project**, not a slice
   of this one — it is an inference mechanism the checker lacks, and it would
   move the inferred type of every unsuffixed literal in every program, against
   a byte-identical self-compile gate. Scoped separately in
   `docs/SELFHOST-LITERAL-SETTLING.md`, with the native settling pass
   (`settleNumeric` / `settleInt` / `checkLiteralFits`, driven from 66 call
   sites) as the reference.

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
