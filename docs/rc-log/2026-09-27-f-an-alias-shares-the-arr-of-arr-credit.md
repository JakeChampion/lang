# An alias shares the arr-of-arr credit

2026-09-27 — `irlower`: `credit_arrarr_group`, `emit_arrarr_reclaim_store`,
`aliased_push_store_ok`, `arrarr_row_effective`. Fixes #10416.

```fern
let g: i32[][] = [[3, 1], [2, 3]];
let h: i32[][] = g;
return g.len() + h.len();
```

On the AST lowering (`FERN_SEM_IR=`) this leaked both rows (allocs 3, frees
1). The semantic lowering balanced.

## Cause

Only a fresh, unaliased `T[][]` local earned the `ARRARR:` credit, which is
what routes its release through `__fern_arrarr_free`'s row walk. `let h = g`
read as an escape in `arrarr_unsafe_for`, so `g` lost the credit, and `h`
never had one. Both slots took the shallow buffer dec: the second one freed
the outer buffer and stranded the rows. A `let p = r.grid` field bind had no
credit either, so once the holder struct released its own reference first,
`p`'s shallow dec stranded the rows the same way.

## Change

The bind retains the outer buffer, so every sharer holds a counted
reference, and `__fern_arrarr_free` walks the rows only at rc 1. Any number
of owners may therefore take it: whichever releases last frees the rows.

- `credit_arrarr_group` credits a candidate together with every local
  sharing its buffer: the `let v = g` chain (`alias_chain_closure`, now the
  one raw closure every alias-chain credit uses) and each other candidate
  reassigned only from the group (`h = g`, `alias_reassign_target_sites`,
  the string family's walk, renamed now it is shared). All or nothing, each
  member vetted by `arrarr_unsafe_for` and `arrarr_row_escapes_iter`, which
  forgive a share with another member.
- A `let p = r.f` bind of a scalar-row nested-array field
  (`collect_arrarr_field_bind_sites`) is a candidate of its own. The field's
  rows are counted: `__struct_drop_<T>` already walks them (#10397).
- `emit_arrarr_reclaim_store` takes `alias_inc`, for a loop-local alias
  re-declaration, and the assign path uses it for `h = g`. With the retain
  it releases the old buffer unconditionally.
- `aliased_push_store_ok` admits a credited arr-of-arr: a copying
  `h.append(row)` on a shared buffer now retains the rows the copy shares and
  gives the old reference back, in the self-append store and in
  `return h.append(row)` (`settle_returned_append`). Before, both kept the
  old reference, which stayed unobserved while a credited slot could never
  be shared.
- `arrarr_row_effective` resolves `return h` through `let h = g` when each
  bind is its source's last use, so a producer returning an alias registers
  `AAC:` like one returning `g`.

## Measured (x86-64, AST lowering, allocs / frees)

| shape | main | this change |
|---|---|---|
| `let h = g` (the issue) | 3 / 1 | 3 / 3 |
| `let k = h` chained on it | 3 / 1 | 3 / 3 |
| `let p = r.grid`, then `r` rebound | 10 / 8 | 10 / 10 |
| the same as a loop local, four rounds | 40 / 32 | 40 / 40 |
| `g` last read before `h`'s reads | 7 / 5 | 7 / 7 |
| `let h = g` at g's last use (a move) | 6 / 4 | 6 / 6 |
| producer returning `h`, three calls | 18 / 12 | 18 / 18 |
| loop-local `let h = g`, five rounds | 30 / 20 | 30 / 30 |
| outer `h = g` each round, five rounds | 32 / 21 | 32 / 32 |
| swap `t = a; a = b; b = t`, four rounds | 17 / 14 | 17 / 17 |
| `h = h.append(..)` and `g = g.append(..)`, three calls | 30 / 18 | 30 / 30 |

wasm gives the same numbers. `TestSelfHostArrArrAlias{X86_64,Wasm}` run
twelve rows under both lowerings; the x86-64 leg also runs each under
`FERN_SANITIZE=1`. The eleven balanced rows fail on main on the AST leg.

## Still leaking

- A producer returning `g.append(row)` is not registered `AAC:`, so the
  caller's binding releases shallow and leaks the rows (3 per call). No alias
  is involved. The `alias_escapes` row holds this shape to the sanitizer
  only.
- `p = r.grid` as an ASSIGNMENT into an outer local costs the holder `r` its
  own struct credit, so the box, buffer and rows all leak (18 / 5 over four
  rounds). The `let` form is covered above.
- A `string[][]` field bind stays uncredited: its string elements need the
  strict credit, which a field read cannot prove.
