# 2026-09-28 — a conditional arm retains the string, struct or dyn local it yields (#10570)

AST lowering (`FERN_SEM_IR=`). The semantic lowering balanced every program
below already.

```fern
t = t + slen(if (j > 1) { s } else { s + "x" });
t = t + px(if (j > 1) { p } else { P { x: 3, y: j } });
var b = if (j > 1) { s } else { s + "x" };
```

An if, match or block arm retained an array local it yielded and nothing
else. A conditional mixing a string, struct or dyn local with a fresh arm was
then not provably owned, so no consumer or binding released it and the fresh
arm leaked at every evaluation. The source local read the yield as an escape
and lost its own release too.

## The arm retains

Native's `emitCountedYield` rule, ported for the kinds that were missing: an
arm whose value is a bare string, struct or dyn value declared outside the
conditional retains it (`cond_alias_leaf_retains`). "Outside" is the slot test
`slot < tslot`: the value temp is allocated before any arm lowers, so a match
payload binding or a local the arm declares always sits above it. The retain
lands at all three stores — `lower_value_tail`'s if-arm store, and the
`tmp = E` assignment a match arm and a block tail lower to (both paths of
`lower_stmt_assign`, the dyn one retaining before any `op_dyn_box`). A name
with no slot (a typed const) is retained when its type is a string or struct,
which keeps the retain a superset of what the binding credits below accept.

## Everything that reads the conditional agrees with it

- **Consumers.** `cond_owned_str` / `cond_owned_struct` accept a leaf naming
  such a local (`cond_outer_leaf_slot`); a dyn local contributes the concrete
  its `DYN:` credit names. They now walk each arm body
  (`cond_yield_arms`), so #10438's "a block's tail local moves out" rule
  applies to an arm's tail local as well
  (`else { var z: dyn Shape = Square { … }; z }`). An alias initializer
  (`{ var q = s; q }`) is refused: that block hands `s`'s box on uncounted.
- **The yielded local keeps its credit.** The string (`STR:`, both families),
  dyn (`DYN:`) and plan-off struct escape gates read
  `cond_leaf_credit_view`: the body with every retained yield of that name
  replaced by an unknown value. The plan route (`free_eligible_sites_of`)
  never tainted a value-block return, so it needed nothing.
- **Bindings.** `vb_leaves_fresh_str` admits a retained leaf beside a fresh
  (or literal) one. `collect_cond_struct_names` credits a struct binding
  whose leaves are fresh boxes of one leak-safe type or retained locals.
  A dyn binding is completed at the coercion site like `DYNCAND:` primitives,
  from `cond_owned_struct`, for a scalar-only concrete only: the dyn sweep's
  `__struct_drop_<T>` is not rc-gated.
- **Shared boxes.** A struct bound from a conditional, and a struct local any
  conditional yields, take `SINKSHARE:` so each owner walks the fields only
  on finding rc 1.
- **Returns.** A returned conditional is a counted return when each leaf is
  a local of the function or would be a counted return on its own:
  `str_fresh_ret_fns` for a string, `CNTRET:` for a struct.

## A block of a bare name is not a conditional (found on the way)

`{ a }` is a one-statement block, which `lower_iife` lowers to the plain read
of `a` with no value temp, so nothing retains it. #10438's rule nonetheless
counted an array local it yielded as retained, and `sum({ a })` released `a`'s
buffer under the local: 3 / 3 on the census and a use-after-free under
`FERN_SANITIZE=1`. `cond_value_body` no longer reads that shape as a
conditional value, so no rule claims it (`block_of_bare_name`). Pinned as
`condBareNameBlockSrc`.

## Measured (x86-64 `FERN_LEAKCHECK=1`, AST lowering; wasm matches)

| program | before | after |
|---|---|---|
| #10570 repro | 6 / 0 | 6 / 6 |
| `condAliasStrSrc` | 23 / 0 | 23 / 23 |
| `condAliasStructSrc` | 22 / 9 | 22 / 22 |
| `condAliasDynSrc` | 16 / 0 | 16 / 16 |
| `condAliasOutlivesSrc` | 6 / 0 | 6 / 6 |
| `condBareNameBlockSrc` | 3 / 3, sanitizer use-after-free | 3 / 3, sanitizer clean |
| a `string`-fielded struct local, bound, argument and field read | 30 / 8 | 30 / 10 |

`FERN_SANITIZE=1` is clean on every balanced row. `TestSelfHostConditionalValueRelease{X86_64,Wasm,Arm64}`
holds the five new programs to the interpreter's answer on both lowerings.

## Not covered

- A parameter yielded by a conditional still reads as escaping to the borrow
  inference (#10571): `via_str(s, j)` keeps its caller's `s`, now pinned in
  `condNotOwnedSrc` beside the array form.
- A dyn producer's result is not a counted arm, and a dyn local re-declared in
  a loop body frees only its last value (#10572).
- A string-fielded struct passed as an argument is still not stashed, so the
  last row above leaks its fresh arms (the #10438 stash gate).
- A string alias declared in a loop body over a string declared outside it
  (`var keep = s;`) loses both credits: 1 / 0 on the base as well. Unrelated
  to conditionals.
