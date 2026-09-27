# An AST caller releases a record a semantic producer built

An AST-lowered caller never released a struct returned by a
semantic-lowered producer when the struct had a reference field (#10415).
`FERN_SEM_IR_SKIP=main` on

```
struct Ints { n: i32, ys: i32[] }
function build(k: i32): Ints { ...loop-built g...; return Ints { n: k, ys: g }; }
function main(): i32 { var r: Ints = build(3); return r.ys[1] + r.n; }
```

left allocs=2 frees=0, where every other lowering balanced.

## Cause

The issue guessed that the semantic lowering never registered `build` in the
strict-fresh producer registry (`return_fresh_struct_ret_fns_of`). It is
registered. The registry is built from syntax over the whole module, and it
admits `build`. `ssarc.caller_rows` then rewrites the rows of every produced
callee for its AST callers. It erases the bare-name row, because the row was
inferred from a body the AST no longer lowers, and it wrote the row back only
for a schema with no reference field (`box_only_result`). `Ints` has one, so
`main`'s `r` lost its reclaim credit.

That limit was deliberate. The bare-name row makes the caller's exit sweep run
`__struct_drop_<T>` with no uniqueness check on the box. A produced result is
one counted reference, and when that reference is a retain of a box the caller
still owns (a returned parameter, say), the unguarded walk frees fields the
other owner reads.

## Change

`bare_row_result` also grants the row when `built_result` proves the box is
the caller's only reference:

- each return value, through any copies and phis, is defined by a
  `record_new`;
- nothing touches those values except a field read (`record_get`) or a copy
  or phi inside the same set;
- the plan retains none of them.

The field counts need no proof. Every per-field arm of `__struct_drop_<T>` is
rc-guarded (`__fern_arr_dec`, `__fern_arrarr_free`, `__fern_str_free`, the
`is_unique`-gated nested drops), so a field another owner shares survives the
walk.

Methods stay at the box-only floor. `caller_rows` writes rows under the bare
declaration name, but the AST looks a method up as `<Base>.<name>`, so a
method's built verdict would credit a free function with the same name.
`caller_rows` / `caller_sigs` now take a `method` flag, as `grow_mask`
already did. The `method_name_collision` row fails without the flag: it hits
a use-after-free under `FERN_SEM_IR_SKIP=main`.

## Measured (allocs / frees, x86-64 and wasm alike, `FERN_SEM_IR_SKIP=main`)

| shape | before | after |
|---|---|---|
| the issue's program | 2 / 0 | 2 / 2 |
| `TestSelfHostNestedArrFieldDrop` `loop_built_producer` | 57 / 50 | 57 / 57 |
| `TestSelfHostNestedArrFieldDrop` `strarr_producer_rebind` | 44 / 41 | 44 / 44 |

Both `TestSelfHostNestedArrFieldDrop` pins are removed, which leaves no
pinned rows, so its pin machinery is removed too. `TestSelfHostMixedStructRelease`
runs five rows on x86-64 (leakcheck and the sanitizer), arm64 and wasm under
four lowerings. Three rows are built producers: loop-built, rebound in a
loop, and merged through a phi. They balance everywhere. The other two are
producers that must not be credited: a returned parameter, a choice between
two parameters, a record also stored elsewhere, and the method collision.
Those rows check the answer and a sanitizer report of leaks only.

## Still leaking

- A produced METHOD returning a record with reference fields, called from an
  AST caller. Its rows are keyed under the wrong name (above). The same
  mismatch affects its parameter rows (`borrowable_params`, `param_counted`),
  whose flags would also need the receiver position dropped to line up with
  the AST's per-parameter flags.
- A produced result that is not a built box (a returned parameter, a forwarded
  call) keeps the AST caller's leak floor. Releasing it needs an rc-guarded
  box drop on the AST side, not a row.
