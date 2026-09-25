# 2026-09-21 — a type variable only a constraint mentions

`nth[T, I: Iterator[T]](it: I, n: i32): Option[T]` refused with `unbound type
variable`, and that single refusal was the root of the whole cascade in
`examples/tests/iter_test` (175 declarations) and `iter_combinators_test` (151).
Neither file produced a single declaration; every other refusal in both was a
consequence — `is reached by a direct call from test__assert_eq__i32, which the
AST lowering calls through a box`, and so on down through `to_string`,
`int__int_to_string` and the bigint helpers.

## Where the argument went

`T` appears in exactly two places in that signature: inside the bound on `I`,
and in the return. It is an UNBOUNDED parameter, so `parse_func_decl` erases it
— it is not in `fd.type_params` at all — and the only thing that could say what
it is, is the impl the bound resolved to.

Except the bound did not survive the parse. `parse_type_param_list` consumed a
bound's bracketed type arguments and then threw them away:

```fern
if (p.verbatim) { tname = tname + bargs; }
```

Outside `-fmt`, `I: Iterator[T]` became `I: Iterator`. So by the time the
monomorphiser built `iter__nth__iter__Range`, the instance key held `I = Range`
and nothing held `T`; `clone_bg` substituted `I` and left `T` standing. The
clone's return resolved to `Option[unknown]`, `ssasem.contract_variables` found
a `T` no argument could bind, and `invoke_rest` refused.

## The fix is where the instance is made

`clone_bg` is what creates the instance, so it is what should create it fully
instantiated. For each bounded parameter already pinned by the key, the impl of
the bound's trait for that concrete type gives the trait's own arguments, and
those line up positionally with what the bound wrote:

```
bound        Iterator[T]
impl         impl Iterator[i32] for Range
             --------  ---
so           T = i32
```

A PARAMETRIC impl writes them in its own parameters rather than concretely
(`impl[T] Iterator[T] for ArrayIter[T]`), so the impl's `for` type is matched
against the concrete one first and the substitution carried through — the same
two-step native's `implTraitArgsFor` does.

Three things had to exist for that to be expressible:

- the parser keeps the bound as written. The two readers that want the erased
  monomorphisation key — the checker's `tp_bound_traits` and
  `e021_bound_call_diag` — take the base name of each `+` entry, through a
  shared `split_plus_bounds`. Under `-fmt` the spelling is unchanged, because
  verbatim already rebuilt it.
- `ImplInfo` gains `trait_ref`: the trait reference with its arguments intact.
  `trait_name` has them stripped (deliberately — `impl Greet[i32] for P` has to
  match the `Greet` trait for default-method synthesis, #4340), so the two
  cannot be folded together.
- `ref_bind` matches a type-variable pattern against a concrete spelling at the
  string level, which is the level the monomorphiser works at.

One pass suffices, and not by luck: a bound is always written over a BOUNDED
parameter, and every bounded parameter is concrete from the key before this
runs. The variables it pins are the erased ones, which carry no bounds of their
own — that is why they were erased.

## What it costs the flatten boundary

`bound_traits` is not namespace-rewritten — "those are TRAIT names, and the
trait namespace is not rewritten". A bound's type ARGUMENTS now ride along in
the same spelling, so a bound written over a module-local type
(`I: Iterator[Foo]`) does not match the impl's mangled `mod__Foo` and pins
nothing. That is the status quo, not a regression: the variable stays erased
exactly as it did before any of this existed. The shape that matters —
a variable argument, `Iterator[T]` — is namespace-free.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`.

| program | before | after |
|---|---|---|
| `examples/tests/iter_test` | 0 of 175 | 175 of 175 |
| `examples/tests/iter_combinators_test` | 0 of 151 | 151 of 151 |

Both suites pass (15 of 15 and 8 of 8) and both legs answer identically.

On the test-row program the typed leg reclaims the shape whole —
`allocs=3025 frees=3025 live_bytes=0` — where the AST leg strands 118,400 bytes
in 2775 blocks. That gap is the eager iterators: `to_array` builds an array per
call and the AST lowering never releases it.

## What this does not reach

`ndarray_test` and `array_combinators_test`, the next two largest, do not come
with it. Their cascades root elsewhere.

## The half held back

The self-host checker's half landed with #9925. Every scope carries the impl
table, a bounded parameter's impl binds the type variables its bound names, and
a destination that the arguments leave open is compared against the impl. So
`iter.nth(iter.range(0, 9), 4)` reads `Option[i32]` on both compilers, and
`var xs: string[] = iter.to_array(iter.range(0, 5))` is E021 on both.
