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

The tag VOCABULARY is a separate axis from the carrier set: it names scalars,
strings, structs, nominal enums, maps, tuples and the builtin Option/Result
generics, and `""` for everything else. A node type can carry a stamp and still
be told nothing, which is how the enum gap below survived a complete carrier set.

| node | landed | consumers reading it |
|---|---|---|
| `ExprCall.ty` | #5531 | `expr_struct_type`, `expr_map_type_tag`, `expr_tuple_elem_tag`, `try_opt_type`, `expr_is_str` / `_f64` / `_u32` / `_u64`, `infer_expr_width` |
| `ExprFieldAccess.ty` | #5986 | `fa_type_tag` — the single leaf behind `expr_struct_type`, `expr_map_type_tag`, `infer_expr_width` and `expr_is_f64` / `_f32` / `_u32` / `_u64`; plus `cap_type_expr` at lift time |
| `ExprIndex.ty` | #6165 | `ix_type_tag` — the leaf behind the `ExprIndex` arms of `expr_is_f64` and `infer_expr_width` AND the two load sites (`lower_expr`'s `arr_get` width, `lower_i64`'s `arr_get_i64`), so the element width and the value's downstream type answer from one place |
| `ExprSlice.ty` | #5986 | the `ExprSlice` arm of `lower_expr` — both the `expr_is_arr_src` **gate** (a non-empty tag proves array-ness the walk cannot reach) and `slice_elem_is_wide` (the `arr_slice` element width). Names the SOURCE array's type, via the checker's `type_to_arrtag` |
| `ExprIdent.ty` | #5986 | `id_type_tag` — the leaf behind the `ExprIdent` arms of `expr_is_str` / `_bool` / `_f64` / `_u32` / `_u64` and `infer_expr_width`, plus `lower_i64`'s ExprIdent LOAD site |
| `ExprBinary.ty` / `ExprUnary.ty` | this slice | the composite operator-overload paths: `lower_expr`'s overload desugar (which now also stamps the ExprCall it builds), `lower_i64`'s binary and unary LOAD sites, and the binary / unary arms of `expr_is_str` / `_bool` / `_f64` / `_u32` / `_u64` and `infer_expr_width` |

**Phase A's carrier set is complete** with these two — every node type #5986's
table proposed now carries the checker's answer.

### The consumer sweep (#5986): every stamped carrier now has its readers

An audit of every predicate, binding mark, and lift-time resolver in irlower
found 28 consumers re-deriving a type structurally while the stamp on the same
node went unread — each one demonstrated as a live defect against the interp
oracle before fixing (13 silent wrong answers, 11 bails) and pinned by
`internal/e2eselfhost/self_host_annotate_consumers_ir_test.go` (oracle-compared,
x86-64 + wasm, each gap with a walk-path control). The wirings follow the
ordering rules above; the sweep's own findings, for whoever wires the next one:

- **The checker's tag vocabulary is wider than the walks' spelling set, and
  guards written for one do not hold for the other.** `is_enum_like_name`'s
  scalar exclusion list holds only the spellings declarations use — feed it a
  checker tag and `"u64"` reads as a nominal enum, which typed every
  `x as u64` as a struct value and bailed every stdlib `checked_mul`.
  `struct_tag_from_ty` is the admission helper that rejects the scalar tag
  vocabulary first; route struct/enum tag admissions through it.
- **A walk that answers "" on purpose is not a hole.** `some_opt_type` returns
  "" for a `Some(x)` whose payload kind the tuple-element machinery cannot
  carry; a tag fallback that "fixed" that rejection lowered the payload down a
  path that cannot hold it. `expr_opt_elem_tag`'s tag admission holds the tag
  to the same payload set the walk admits, and `opt_recv_base_type`'s fallback
  skips Some/Ok/Err constructions outright. Before filling a "", read why it
  is empty.
- **A lift-time "" can be a checker resolver hole, not a consumer gap.**
  `match (s.o)` on an `Option[i32]` FIELD kept declining the lift after its
  consumer was wired, because `type_from_name_with_names_and_unions` (behind
  `collect_struct_sigs`) had no builtin-generic-enum arm and the field typed
  unknown — the stamp was never made. The resolver has the arm now, mirroring
  `type_from_ref_su`; pinned in the `TestSelfHostTypeResolveSimple` goldens.
- `method_recv_tyname` resolves an associated bare-type receiver
  (`Counter.start()`) to its registry-key spelling now — a walk fix, so the
  unannotated build gets it too.

### The fn-typed param/field boundary — CLOSED by the fn_ret widening

A call through a fn-typed param or struct field used to type unknown — the
parser coarsens every fn-type spelling to the flat `"fn"` tag and the checker
read the `fn_ret` sidecar nowhere — so those calls stamped `""` and no
consumer wiring could see them. Three changes closed it:

- `fn_ret_ty` (parser) keeps tuple (`(a, b)`) and array (`T[]`) return
  spellings alongside the struct/nominal/string/scalar set it already kept.
- `build_func_scope` binds a `"fn"`-tagged PARAM to a real `TypeFunc` built
  from `fn_param_types` + `fn_ret` (`fn_param_decl_type`), so the existing
  closure-call arm types calls through it.
- `collect_struct_sigs` types a fn FIELD as `TypeFunc` from its `fn_ret`
  (empty param list — a `StructFieldDecl` carries no param spellings), a new
  closure-field-call arm in `check_call_expr` types `h.f(args)` from it ahead
  of the undefined-method failure, and `type_assignable` gained a deliberately
  COARSE fn-to-fn arm: comparing signatures would reject constructions the
  unknown-typed field accepted, and the empty field param list cannot be
  compared. Tightening it needs field param spellings first. This also killed
  a live false E038 ("calling non-function value of type unknown") on every
  closure-field call under `-check`.

The call-site half is live too now: `FuncSig.param_types` and
`MethodSig.param_types` resolve `"fn"` params through the same sidecars, and a
bare non-const function name used as a value types as its signature's function
type (without which a known fn param would read a named-function argument as a
mismatch and untype the call). A non-function argument to a fn-typed param
draws native's E038; a bare fn name assigned where a scalar is declared draws
native's E003 — both pinned in the checker-codes corpus, with `-check` over
the compiler's own sources byte-identical.

The sidecar audit found the widened spellings mostly inert (every irlower
registry consumer filters by prefix or `is_struct_ret_name`), with one real
behaviour change that turned out to be a live fix: `fn_sig_of` now sees a
tuple/array return as `'w'`, so a fn param mixing i64/f64 params with such a
return emits the width-typed wasm funcref signature where it silently fell
back to the arity-keyed `$fn<N>` type — the pre-widening module ran and
answered wrong (exit 1 vs oracle 42). An annotated tuple-returning fn var was
likewise a silent x86-64 miscompile via `stamp_lambda_ret`. Both are pinned
(`fnwiden_*` cases), as are the four previously-unreachable param/field
shapes (`fnparam_*`) and `-check` byte-identity over the compiler's own
sources.

### What `ExprIdent.ty` is for: the half no slot carries

A bare name is typed in irlower from the SLOT it reads, and the slot walk is
strictly better than the annotation wherever it resolves — it records widths the
checker's `Type` erases, and every slot-marking rule maintains it. So the
carrier is read only for the OTHER half: a name with no slot at all, which today
means a module `const`. A `const` desugars to a zero-argument accessor, so its
bare read is really a call, and each ident predicate had to learn that
separately — `expr_is_str` did (#2954), `expr_is_f64` and `infer_expr_width` did
(#4801), and `expr_is_u32` / `expr_is_u64` / `expr_is_bool` never did.

Both omissions were live miscompiles, and both are the same shape as every
defect in this issue's table:

```fern
const M: u32 = 2147484527u32;
function main(): i32 { return ((M >> 1u32) % 100u32) as i32; }   // wasm: 11, oracle 63
```
```fern
import "std/json";
const B: boolean = true;
function main(): i32 { return B.to_json().len() as i32; }        // 1 ("1"), oracle 4 ("true")
```

The u32 one is wasm-only — x86-64 and arm64 keep a u32 zero-extended in a
64-bit register, so a signed `shr` already matched there — which is why it
survived a green x86-64 fixture leg. The boolean one diverges on both.

`id_type_tag` replaces all six clauses, so the next scalar kind is added in one
place rather than missed in three. It keeps the ret-flag registries as the
UNANNOTATED build's answer (`asm_ir_run`, the native compiler), which is what
keeps the field byte-identity-safe — but reads `id.ty` ahead of them, because
for a const the stamp names the DECLARED return type while the registries
re-derive it and collapse i64 with u64. That is the reverse of `fa_type_tag`'s
ordering and for a reason that does not apply here: there the walk returns the
field's declared SPELLING and is more precise than the tag; here the walk IS the
registry and is less precise than it.

The load site rule from `ExprIndex.ty` applies unchanged: `lower_i64`'s
`ExprIdent` arm decides whether an i64 const emits its 8-byte call, and it
reads the same leaf `infer_expr_width` does. Split across two derivations, a
disagreement is a module the wasm validator rejects.

**Open, and it is NOT a lambda problem: calling an f64-returning fn-VALUE
through a param loses its type.** Probing the lambda positions the var-binding
stamp cannot reach (call argument, struct-literal field, return position) showed
all four failing — but the minimal reproducer has no lambda in it at all:

```fern
function mkval(): f64 { return 4.5; }
function apply(f: () => f64): f64 { return f(); }
function main(): i32 { return (apply(mkval) * 10.0) as i32; }   // exits 1, oracle 45
```

The wasm validator rejects **`apply`**, not the callee and not any lifted
lambda. So the defect is in how a function types `f()` where `f` is an
f64-returning fn-typed param — the lambda cases were downstream symptoms of it,
and a pass that stamps lambda return types (which I built and reverted) cannot
fix any of them.

This is worth stating because the shape is misleading: four probes all involving
lambdas, one root cause involving none. Reach for the smallest reproducer before
building the fix — dropping the lambda was what identified the real mechanism.

**The fix is NOT in the type predicates** — measured, not assumed. The obvious
sketch is: `ParamDecl.fn_ret` carries `"f64"` for such a param (since the
fn-return widening), `closure_opt_rets` is already a `name|type` registry
populated from that field in `lower_func`'s param loop but records only
`Option[…]` / `Result[…]` (and `try_opt_type` returns its lookup verbatim, so
scalars cannot be folded in) — therefore add a `clo_scalar_rets` sibling and
read it from `expr_is_f64`'s ExprCall arm.

I built exactly that. The registry populates correctly (`apply.f -> f64`,
verified by instrumentation) and `expr_is_f64` then answers true — **and the
program still fails identically.** Reverted.

The emitted wasm says why:

```wat
(func $apply (param i32) (result f64)   ;; signature already correct
```

`apply`'s own signature is right; the `call_indirect` **inside** it yields i32,
so the return mismatches. The value's type was never the problem — the indirect
call's SIGNATURE is. So the fix belongs wherever a fn-param call's result type
is emitted (the `fn_param_sigs` machinery), and `expr_is_f64` is at best a
necessary companion to it, not the fix.

Recorded this way deliberately: the plausible one-field sketch is wrong, and
would cost whoever picks this up a build-and-instrument cycle to discover. The
signature, not the predicate.

**Root cause, third level: function values used to be structurally all-i32,
and the IR now carries their signature.** `ir.op_call_indirect(argc)` names
only the arity, so the wasm backend keyed one funcref type per arity and
hardcoded `(type $fn<N> (func (param i32)xN (result i32)))` — on that keying an
`f64`-returning fn value cannot be represented at all, and `expr_is_f64` being
right only means the enclosing function is declared `(result f64)` while the
`call_indirect` inside it is still typed `(result i32)`, which is the validator
error the three-line reproducer above hit.

`op_call_indirect_sig(argc, sig)` closed that (#6282). The op carries a width
string — one char per parameter ('w' i32/pointer, 'l' i64/u64, 'd' f64/f32),
then `_`, then the result char — and `wasm_ir.fn_type_name` keys the `$fn…` type
set on it, rendering per-position value types. An EMPTY sig is byte-for-byte
the old op and keeps the arity-keyed `$fn<N>`, which is what lets the tag be
added one construction site at a time. The register backends read the arity
alone and ignore the tag; they need no signature because their result register
is chosen by the call's own type, which is why a wide fn value is a wasm-only
defect once the frontend types it.

The general lesson stands and has flipped direction: the carriers push type
information down toward codegen, and where codegen has nowhere to put it,
widening the IR — not the frontend — is the work. That widening is done here.
What follows it is the frontend work of actually filling the field.

### Calls through a fn value that no slot carries

`fn_value_sig` answers for a fn-typed local or parameter by reading the sidecars
`lower_func` seeded onto its SLOT, and those were the only two tagged sites. A
fn value reached any other way — a struct field, an array element, a tuple
element, another call's result — has no slot, so all seven of those sites
emitted the untagged op. Four shapes with a wide result were live defects,
measured against the interp oracle (45 in each case):

| call | x86-64 | wasm |
|---|---|---|
| `var h: H = H { f: mkval }; h.f()` | 45 | module refused |
| `var fs: (() => f64)[] = [mkval]; fs[0]()` | 255 | module refused |
| `var t: (() => f64, i32) = (mkval, 1); t.0()` | 255 | module refused |
| `var f: (f64) => f64 = scale; f(4.5)` | wrong | module refused |

Two boundaries in that table are worth reading off it. Binding the result to a
declared local first (`var v: f64 = fs[0]();`) fixed the REGISTER backends —
the declaration supplies the width — and did nothing for wasm, whose funcref
type is a separate decision; so the `_local` rows in the pinning suite are a
control for the register half only. And the zero-argument `var f: () => f64 =
mkval; f()` was already correct on both, even though the checker could not type
it: irlower's own slot walk carried the width. That gap is real all the same,
and closing it is what makes the with-argument form above work.

The x86-64 column is the tell: this is not a codegen gap. **The self-host
checker could not type three of the four**, so `-check` fell through to
`ill_typed_hint`, the `ExprCall.ty` stamp was empty, and every width predicate
downstream read the f64 as an i32. Probing the checker first — the rule the
`ExprBinary` slice learned — was again what separated a mechanical field
addition from the real work. Three layers were responsible:

- **`check_call_expr` had no arm for a callee that is a VALUE.** Its callee
  `match` handled `ExprIdent` and `ExprFieldAccess` and bailed on everything
  else with "call target is not an identifier", so an `ExprIndex` or a nested
  `ExprCall` callee never got as far as being typed. `t.0()` failed one level
  in: the `ExprFieldAccess` arm assumes a field-access callee is a method call,
  so a numeric field on a tuple returned "receiver isn't a typed value" —
  even though `check_expr(t.0)` on its own already answered `TypeFunc{ret:
  f64}`. `call_through_fn_value` types the callee and returns that function
  type's result, and is now the single rule both bail sites ask.
- **A fn-typed LOCAL's declared return never reached its binding.**
  `var_declared_type` resolved `v.type_name` and ignored the `fn_ret` sidecar
  beside it, so `var f: () => f64` bound the opaque `fn` tag with an unknown
  result — the local sibling of the `fn_param_decl_type` arm params already had.
- **The parenthesised fn spelling dropped its result entirely.**
  `parse_type_paren`'s grouping branch returned `consume_array_suffix(p, "fn")`,
  whose `fn_ret` channel is hardcoded `""`. That branch is the ONLY way an array
  of functions is spelled, so `(() => f64)[]` recorded nothing about its element
  and no consumer could have read one. It now recovers the result from the
  reconstructed inner spelling through `parse_type_ref`, and both branches admit
  it through one `fn_ret_admitted` rule rather than two copies of the list.

With those, `callee_fn_sig` supplies the funcref tag at all seven sites from the
checker's stamp on the call — the carrier answering a question that was
otherwise a re-derivation. All four shapes now match the interp oracle on
x86-64 and wasm, pinned per shape with its bound-local and its all-i32 control
in `internal/e2eselfhost/self_host_fn_value_call_ir_test.go`.

### The with-arguments half: the parameters have to be CARRIED

A funcref tag must name the whole signature, so a call carrying arguments needs
the callee's parameter widths as well as its result. Deriving them from the
ARGUMENT expressions at the call site would work and is the wrong fix — it
re-derives what the declaration already stated, which is the drift this whole
file documents. `StructFieldDecl` now carries a `fn_param_types` sidecar beside
its `fn_ret`, filled by the same non-consuming `peek_fn_param_types` lookahead
`parse_param` uses, substituted by the monomorphiser and renamed by flatten
alongside the spellings it sits with.

`decl_field_fn_sig` builds the tag from those two sidecars through `fn_sig_of` —
the same rules a fn-typed PARAM goes through, so a field call and a parameter
call cannot disagree about the signature they dispatch on. And the tag drives
BOTH halves of the call: `sig_arg_width_char` reads each argument's width out of
it, so the width an argument is lowered at and the funcref type the call names
come from one string. That pairing is the point — split across two derivations
they disagree, and a disagreement is a module the validator rejects.

**A TUPLE element carries the same tag now, from the declaration rather than a
second derivation.** `t.0(4.5)` was the same defect: the element's tag is the
coarse `"clo"` dispatch marker, and no signature can be recovered from it.

The fix is not a parallel list of element signatures. `tuple_elem_tags` is
*supposed* to be lossy — every element consumer wants the dispatch tag, and
widening the tag in place would break the fourteen `== "clo"` gates that read
it, including the two call sites themselves. What was missing is the
declaration: `LocalInfo.tuple_type` keeps the slot's declared tuple SPELLING
beside the tags derived from it, so `tuple_elem_fn_sig` can parse the element's
own `TypeRef` and hand it to `fn_sig_of_ref`. Fifteen of the sites that record
element tags already had that spelling in hand and now keep it; the rest record
nothing and the call declines exactly as before.

`tuple_type` joins the family of declared-spelling sidecars a slot already
carries — `struct_type`, `opt_type`, `map_type`, `arrarr_elem`, `cell_elem` —
which is the shape to reach for when a derived tag cannot answer a question the
declaration could.

### Un-coarsening `"fn[]"` was tried and reverted — the couplings are behavioural

The array element looked like the cheap one. Rather than a
`StmtVar.fn_param_types` sidecar (58 construction literals plus the
monomorphiser, lift and flatten rebuild sites), just stop coarsening: let
`((f64) => f64)[]` keep its spelling and give the fifteen sites that match the
flat `"fn[]"` tag one `parser.is_fn_array_type_name` predicate. Fifteen sites
against fifty-eight, and it DELETES machinery. It was tried, it worked for the
calls, and it was reverted.

**Counting the textual matches measured the wrong thing.** The dependencies on
the coarse spelling are behavioural, and neither of the two found is a match on
`"fn[]"`:

- **The whole-array alias bind reached its arm by accident.** `var xs = r.hs`
  is claimed by the ENUM-array-field arm, because the coarse element `"fn"`
  reads as enum-like to `is_enum_like_name`. With the spelling kept,
  `(() => i32)` is not an enum name, the arm declines, the slot is never marked,
  and `xs[0]()` bails the IR path. Nameable and fixable — a `fnarr_field_read_type`
  sibling — but nothing in the source says the alias depends on that
  misclassification.
- **Something in the rc / struct-drop path also keys off it.** With the alias
  fixed and every call answering correctly, the `rc-soundness` churn probe in
  `self_host_fnptr_array_field_ir_test.go` reports a LEAK (exit 98, the heap
  growing across two identical churn runs) where the base returns 0. Not
  root-caused before the revert.

Two lessons worth more than the slice. **A tag with a long history accretes
readers that depend on its shape rather than its text**, so `grep` bounds the
edit and not the risk. And **the suites named for the thing you are changing are
the gate**: the fixture legs, the formatter corpus, the checker differentials and
the annotate suites were all green on the reverted work; the two suites that
caught it are the two with `FnptrArrayField` and `CloArrayFieldBind` in their
names, and running them was not part of the plan until CI failed.

The sidecar route was then taken, and it is what landed — see below.

### The ARRAY element: the sidecar route, taken

`var fs: ((f64) => f64)[] = [scale]; fs[0](4.5)` is the one shape whose declared
spelling does not survive to a consumer. A tuple keeps its spelling, a field
records its two sidecars; an array is coarsened whole to `"fn[]"`, and `fn_ret`
alone names half a signature.

So `StmtVar` gained `fn_param_types` beside its `fn_ret` — the additive route,
57 construction literals plus the monomorphiser / lift / flatten rebuild sites.
`peek_fn_array_param_types` is `peek_fn_param_types` asked one grouping paren
in, and `fn_param_types_for` keeps whichever of the two peeks the resulting tag
makes meaningful, so a binding, a parameter and a struct field all record the
spellings through the same lookahead.

irlower rebuilds the ELEMENT spelling from the pair and keeps it on the slot as
`LocalInfo.fnarr_elem` — the array member of the declared-spelling family
`tuple_type` joined. `array_elem_fn_sig` reads it (or a struct field's own two
sidecars) and hands it to `fn_sig_of_ref`, and `sig_arg_width_char` reads the
argument widths back out of the same tag, so the closure-element arm and the
fn-pointer-element arm each lower arguments and name their funcref from ONE
string. A whole-array alias (`var xs = fs`, `var xs = r.hs`) inherits the
spelling at the rebind, because otherwise `xs[0]` reaches the call with nothing
to name.

**The checker had the matching hole one level down**, and the field shape is
what exposed it: `collect_struct_sigs` resolved a `"fn"` field to a `TypeFunc`
but had no arm for `"fn[]"`, so `r.hs[0](4.5)` typed unknown, `expr_is_f64`
answered false, and the `as i32` emitted an integer mask instead of a truncate —
a silent wrong answer (0 against an oracle of 45) on the register backends,
present before this work and independent of any signature. The array arm is the
field sibling of `var_declared_type`'s, and both stay `t_func_opaque` on
purpose: the parameter spellings exist to name a funcref, and checking arguments
against them would start rejecting calls that type-check today.

### `mk()()`: the callee is a CALL, and its declaration is what names it

The last of these shapes. `mk()(4.5)` has a callee with no slot, no field and no
spelling anywhere at the call site — what describes it is the *callee's own
declaration*, and `parse_func_decl` computed the returned fn type's `fn_ret`
from `parse_type_name` and threw it away.

It was worse than an arity-keyed funcref. Both register backends were **silently
wrong**: `mk()()` returning f64 exited 255 and `mk()(4.5)` exited 0, against an
oracle of 45, because the result was read out of an integer register.

Both routes were measured before building, the lesson from the `"fn[]"` revert
applied rather than restated. `FuncDecl.ret_type == "fn"` has only **3** direct
consumers, which makes un-coarsening look cheap — but `.ret_type` is read **372**
times, and those readers are exactly the kind that depend on a tag's shape rather
than its text. The additive route touches 112 construction literals of which 45
already spread, so 67 needed editing. Sixty-seven mechanical edits beat 372
behavioural unknowns.

So `FuncDecl` gained `ret_fn_ret` and `ret_fn_param_types`, filled by the same
lookahead trio, substituted by the monomorphiser and renamed by flatten.
`func_ret_type` resolves a `"fn"` return to `t_func_opaque` — the function-level
member of the family, and opaque for the family's reason. irlower registers
`ret_fn_sigs_of` once per module, keyed by callee name, so both arms of the
`mk()()` lowering — the plain fn-pointer one and the env-first closure one —
read the same tag for their argument widths and their funcref type.

**One arm is not the fix.** Tagging the emission on both arms but lowering
arguments at declared widths on only one passed every row except the
two-parameter closure case, which wasm rejected with `expected i64, found i32`.
The rule this file states twice already is worth stating once more: *one declared
signature drives both halves, at every site that dispatches.*

The larger alternative remains what this file argues for: **stop coarsening.**
#7961 made a fn-typed tuple ELEMENT keep its arrow spelling; doing the same for
the parenthesised grouping and the top-level `(T,…) => R` form would let
`parse_type_ref` answer everywhere and would DELETE the sidecars rather than add
to them. The cost is the 117 sites matching the coarse `"fn"` / `"fn[]"`
spelling today, each of which has to ask `parser.ref_is_fn_value` instead. That
is its own project, and a big-bang one against a byte-identity gate.

### A generic struct's fn field has no stamp to inherit

`Box[T] { f: (T) => T }` at `Box[i64]` compiled clean and emitted a module the
wasm validator refused: `b.f(45i64) as i32` lowered with no `i32.wrap_i64`.

The cause is this file's own rule read from the other side. Annotation runs on
the ERASED form, so a field spelled `(T) => T` stamps nothing and the clone
inherits nothing — the checker cannot answer here however well it is wired. The
declared sidecars, substituted by the monomorphiser, are the only source. And
the predicates had each learned that separately: `expr_is_str` grew the fn-field
arm for the string case (#5306 gap 2) and the width predicates never grew
theirs.

`call_fn_field_ret` is the one leaf now, read by `expr_is_str`,
`infer_expr_width` and `lower_i64`'s load site together, so the next scalar kind
is added in one place rather than missed in three.

Note how little of this the fixpoint reaches: the self-host's own sources carry
one fn-typed parameter (`astwalk.fold_expr` / `fold_stmt`, #6993) at one arity
and one signature, so `internal/e2eselfhost` and the fixture legs are still the
gates that matter.

**CLOSED (the section below is the record of why it was blocked).** `fn_ret` now
carries scalar returns, and `parse_stmt`'s var binding stamps them onto an
unannotated lambda init, so the declared type answers before `irt_guess` is ever
consulted. The audit the note called for found exactly ONE consumer that treated
a non-empty, non-string, unbracketed `fn_ret` as a struct name
(`struct_ret_fns_aug`, irlower). It now asks `parser.is_struct_ret_name` — the
same predicate that decides what `fn_ret` keeps — which rejects brackets,
`"string"` and every scalar, so producer and consumer cannot drift. That
substitution also deleted two ad-hoc exclusions at the call site.

**Open: a lambda's declared return type never reaches the lambda.** A lifted
lambda's return type is *inferred* from its body by `irt_guess`, which #6216 and
#6222 extended to nine expression forms. But inference is the wrong source when
the binding already states the answer:

```fern
var f: () => f64 = () => m.get_or("k", 0.0);   // still miscompiles
```

`irt_guess` cannot type a **builtin** method call without absorbing the whole
builtin surface (map / array / string), and it should not have to — the binding
says `f64`.

The annotation route is blocked one level down, and precisely: `parse_type_name`
*does* recover a fn type's return spelling, but deliberately keeps only
**struct**, **nominal-enum** and **string** returns (`fn_ret_ty`, parser.fern) —
scalars are dropped as "primitives need no field lookup", which was true of every
consumer that existed when it was written. So `var f: () => f64` yields an empty
`fn_ret`, and a stamp-the-lambda fix built on it is inert. Verified by
instrumenting it: `STAMP fn_ret=[] lam_ret=[]`.

Closing this means widening `fn_ret` to carry scalar returns, which changes what
every existing `fn_ret` consumer sees (`ParamDecl.fn_ret`,
`StructFieldDecl.fn_ret`, `struct_ret_fns`, …). Those consumers appear to filter
by struct-ness already — the note beside `fn_ret_ty` says the `struct_ret_fns`
consumer "filters 'string' out (a string is not a struct)" — so the widening is
plausibly safe, but it is a sidecar audit plus full gates, i.e. its own slice.
Worth doing: it replaces a growing inference table with the declared type, which
is the same "stop re-deriving what is already known" move as the carriers.

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

**And a consumer is rarely alone.** The tuple DESTRUCTURE has its own copy of
that `ExprIndex` arm — `var (a, b) = arr[i]`, typed from the same
`arrarr_elem` — and it did not get the fallback when `expr_tuple_elem_tag` did.
So one token apart, `ps[0].1` was right and `var (i, v) = ps[0]` returned
garbage for an f64 element (255 on x86-64, 0 on wasm, compiler exit 0). The two
arms are written as siblings and are commented as siblings; only one of them was
fixed. When wiring a carrier into a consumer, grep for the walk it replaces —
here `arrarr_elem_of_slot` — and fix every reader of it in the same diff.

**Not every carrier belongs in the shared tag vocabulary.** `ExprSlice.ty` needs
an ARRAY spelling (`"f64[]"`), and `type_to_irtag` has no `TypeArray` arm — it
returns `""` for every array-valued expression. Adding one there is the obvious
move and the wrong one: four consumers (`expr_is_str` / `_f64` / `_u32` /
`_u64`) read `c.ty` **tag-first**, short-circuiting their structural walk on any
non-empty value, so teaching the shared namer to name arrays silently changes
what they see on every array-valued call. The carrier instead gets its own
`type_to_arrtag`, used at exactly one stamp site and read by one walk-first
leaf. Widening the shared vocabulary is a separate decision from adding a
carrier, and should be made on its own evidence. It has been made once since,
for nominal ENUM names — see "The shared vocabulary had no name for an enum"
below for the evidence, and for why a nominal name is safe where an array
spelling is not.

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

### The last carrier was blocked one level down, and the block was the point

`ExprBinary` is the one carrier in #5986's table with no defect behind it, and
the reason is worth stating: irlower's binary arms are **compositional**. They
recurse into the operands (`expr_is_f64(b.left) || expr_is_f64(b.right)`), so
they carry no name-keyed registry and have no missing-arm class. Ordinary
arithmetic needs no tag and the tag is inert on it.

The exception is a **composite operator overload**, where both operands are
structs and the result is whatever the method returns:

```fern
struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
var d: f64 = p * q;               // f64 — and no walk over p / q can say so
```

Every scalar-returning overload — `f64`, `i64`, `string`, `boolean`, and the
unary `neg` sibling — **bailed the IR path on both backends**, while native
compiled them and the interpreter ran them. Two layers were responsible, and
finding the lower one before building is what kept this slice honest:

1. **irlower** asked `struct_ret_fns` whether the method existed. That registry
   records only STRUCT returns, so a scalar-returning overload read `""` there
   and the guard took it for "no such method" — refusing a valid program. The
   `""` meant "a return I do not record", not "nothing is there". This is the
   issue's thesis in its purest form: a registry that answers one kind of
   question being read as though it answered all of them.
2. **The self-host checker had no operator-overload arm at all.** `p * q` on
   struct operands typed unknown and was rejected `E009`. So the obvious move —
   add the carrier, read `b.ty` — would have stamped `""` on exactly the
   expression whose type was missing and changed nothing.

Point 2 is the one that matters for anyone extending this migration: **probe
the checker before adding a carrier.** The check costs one `-check` run against
the self-host binary and it is decisive. Here it turned a mechanical 36-site
field addition into a checker fix plus a carrier, which is a different piece of
work with a different risk profile.

The checker fix mirrors what comparison operators already did: `==` and `<` on
a composite have resolved through `Eq` / `Ord` via `cmp_method_receiver` for a
long time, and arithmetic was simply the missing sibling. `binop_overload_ret` /
`unop_overload_ret` reuse that same receiver resolution, and — deliberately —
are the SINGLE place both `check_expr` and the `E009` diagnostic walk ask the
question. That walk's own comment promises it "fires only where check_expr
already rejects the operands", which holds only while the two read one rule;
two copies of it is the drift this file documents everywhere else.

Two further notes on the wiring:

- **The registry stays.** A struct-returning overload still resolves through
  `struct_ret_fns`, and the guard is `registry == "" && tag == ""`. That keeps
  the unannotated build (`asm_ir_run`, the native compiler) byte-identical, and
  the struct case is pinned by its own control so the tag cannot quietly
  displace the walk.
- **The predicate wiring is additive** (`walk || tag`), never tag-first. An
  unsuffixed literal types `i32` in the self-host checker, so a tag-first width
  leaf would NARROW a binary the operand walk had already widened to 64 — the
  same hazard §"A carrier is only as good as the checker behind it" records for
  `ExprIndex`, reached from a different direction.

And the load-site rule from `ExprIndex.ty` bit once more, exactly as predicted:
wiring `infer_expr_width` alone left `(p + q) % 100i64` reporting width 64 while
`lower_i64`'s binary arm still recursed into the struct operands and bailed at
the enclosing `as i32`. Both halves read the same tag now.

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

### The shared vocabulary had no name for an enum, and three layers agreed

Widening `type_to_irtag` is a decision this file had already argued should be
made "on its own evidence" (see the `ExprSlice.ty` note above). Here is that
evidence: the namer had a `TypeStruct(st) => st.name` arm and **no union
sibling**, so every enum-valued expression stamped `""` — while irlower's
admission helper `struct_tag_from_ty` had admitted an enum name all along. The
carrier and its consumer were built for each other and never met.

Eight shapes were live BAILS on both backends, each one measured against the
interp oracle, each `main` refused on the unknown symbol `i32.rank`:

| shape | layer |
|---|---|
| `mk()[0].m()` where `mk(): Color[]` | carrier + walk |
| `cs[1:2][0].m()` (sliced base) | carrier |
| `r.c.m()` — a struct FIELD typed at an enum | carrier |
| `t.0.m()` — the tuple-element sibling of it | carrier |
| `mkf()().m()` — the callee is a call | carrier |
| `[Color.Red, Color.Green][1].m()` | checker |
| `(if (b) { [Color.Green] } else { … })[0].m()` | checker + carrier |
| `h.sh.m()` — a `dyn Trait` struct FIELD | carrier |

The widening is safe for the reason the `TypeStruct` arm already states, and it
is worth repeating because it is what separates this from the array case that
was rejected: the four tag-FIRST consumers (`expr_is_str` / `_f64` / `_u32` /
`_u64`) compare the tag to their own keyword, so a nominal NAME reads false
there — which is what the structural walk answered for an enum call too. An
array spelling would have changed those answers; a nominal name cannot.

Three layers were responsible, and the last one is the one worth reading:

- **The carrier.** `type_to_irtag` names a bare nominal union now. A union
  carrying ARGS is a generic instantiation with no irlower spelling, so only the
  bare form names itself.
- **The consumer.** `expr_struct_type`'s `ExprCall` and `ExprFieldAccess` arms
  guarded their tag reads with `decl_is_struct` while the `ExprIndex` arm beside
  them already used `struct_tag_from_ty`. `decl_is_struct` is not the question
  those arms' callers ask — `method_recv_tyname` wants the receiver's NOMINAL
  type, and the same function's other arms return enum names from the slot walk.
  The `dyn Trait` field row in the table above fell out of that one substitution:
  the ExprCall arm has returned the coarse `"dyn Trait"` spelling from
  `struct_ret_fns_of` for a long time and the FIELD arm discarded the identical
  spelling.
- **The walk.** `struct_ret_fns_of`'s two ARRAY arms had no enum element
  sibling, so `function mk(): Color[]` recorded nothing at all. That is the
  answer the UNANNOTATED drivers (`asm_ir_run`, the native compiler) still
  depend on, so it is fixed as a walk fix rather than left to the tag.
  `enum_ret_recordable` is the one rule the scalar return and both array
  returns ask, which is also where the `is_enum_like_name` type-VARIABLE
  exclusion (#6441) now lives instead of being restated. Note its ORDERING
  constraint: a one-letter enum name satisfies `irl_looks_type_var`, so the
  enum arm has to run BEFORE the erased-generic branch that would otherwise
  claim `E[]`, find no parameter spelled `E[]`, and record nothing.

**And the checker had the hole under all of it.** A qualified UNIT variant
`Color.Red` in VALUE position typed unknown: `check_call_expr` learned the
qualified CONSTRUCTOR form in #6657 and `check_expr`'s field-access arm never
learned the value sibling, so it read the enum NAME as an object and answered
"can only field-access a struct or tuple". Two rows in the table are that bug,
not a carrier gap — and a carrier added without it would have stamped `""` on
exactly the expressions that needed one. `qual_variant_union` is the single rule
both spellings ask now.

This is the third time this file records the same instruction, so treat it as
the rule rather than an anecdote: **probe the checker before adding a carrier.**
The measurement that separated the two halves here was one debug print of
`type_to_irtag(check_expr(e, s))` at the stamp site, comparing an enum program
against its struct twin — the struct one printed `P`, the enum one printed
nothing, and that single line said which of the three layers to open first.

**And the admission helper had to get PRECISE before it could be widened.**
`struct_tag_from_ty` admitted through `is_enum_like_name`, which answers "not a
primitive, not an array, not bracketed" — and an ERASED generic struct name
satisfies all three. Annotation runs before monomorphisation, so `OrdSet[K]`
stamps the bare `OrdSet`, a name **no decl survives to carry** (only the
concrete clones do). Routing two more arms through the helper therefore keyed
every method on `ordset__OrdSet.<m>`, a symbol nothing defines, and took five
persistent-collection stdlib modules off the IR path.

`decl_is_enum` is the precise predicate — its own comment already said so —
and with it plus `decl_is_struct` the helper's scalar exclusion list is
redundant: nothing declares a struct or an enum named `i32`. The third
admission, the coarse `dyn Trait` box, is what `is_dyn_value_name` now names
once instead of three inline prefix tests.

Two things to take from it. **A loose predicate is safe only as far as its
current callers reach**; widening what asks it is what turns "occasionally
over-matches" into a defect, so tighten before you widen. And the gate that
caught it is the one `docs/TEST-GATES.md` names for exactly this: a self-host
change that false-positives on real library code passes the checker-codes
differential green, because every row there is a stdlib-free single-file
program. `TestSelfHostStdTestE2E` compiles the stdlib, and it is what holds
this — no synthetic case in the suite below reproduces it, because the shape
needs a generic module whose clones replace the erased declaration.

Pinned per shape, with its struct/scalar/bare-variant control, on x86-64 and
wasm in `internal/e2eselfhost/self_host_annotate_enum_ir_test.go`.

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
