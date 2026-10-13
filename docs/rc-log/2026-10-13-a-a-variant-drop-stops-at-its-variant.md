# 2026-10-13 — a variant drop stops at its variant

`ssarc`: `drop_variant_fields`, `drop_variant_chain`, `drop_captures`; new
`else_if_open` and `close_ifs`. Refs #8171.

## The shape

The drop helper of an enum releases the fields of whichever variant the box
holds. It tested each variant that has children in its own `if`:

```
if (variant_is A) { drop A's fields }
if (variant_is B) { drop B's fields }
...
```

So after the matching arm had run, every later test still ran. Each one
reloaded the shape word, because the arm before it had called out to release
a field. `ast.Expr` has eighteen variants, so every node the compiler freed
paid up to eighteen shape loads and compares, wherever its variant sat in the
list.

`drop_captures`, which matches a closure's body address against each
environment its type may carry, had the same shape.

## What changed

The variants exclude one another, so each test after the first is the
previous one's `else`. The first test that holds ends the chain. No arm runs
between two tests, so the shape load is shared down the chain and the helper
loads it once.

`drop_dyn` keeps its separate tests. An enum among a dyn type's concretes is
dropped by its own helper with no guard of its own, and that call has to run
whatever the earlier tests found.

## Measured

The whole compiler under callgrind, with both compilers built from the same
tree and compiling the same frozen copy of main, each on a warm memo.

| | Ir |
|---|--:|
| main at fef3701 | 69.956 G |
| chained variant drops | 69.616 G (−0.49%) |

The generated drop and release helpers fall by 312 M. 210 M of that is
`__sem_drop_ast__Expr`, 40% of its cost. The checker's inline drops fall by
another 30 M.

The bench corpus on x86-64:

| | before | after | |
|---|--:|--:|--:|
| `perceus_deriv.ir` | 95.83 M | 93.53 M | −2.40% |
| `utf8_ingest_unchecked.ir` | 3.331 M | 3.317 M | −0.41% |
| `perceus_cfold.ir` | 67.14 M | 66.96 M | −0.26% |
| `http_hello.ir` | 29.34 M | 29.26 M | −0.26% |
| `map_string.ir` | 65.73 M | 66.03 M | +0.46% |

`map_string.ir` carries its own 2% tolerance, because its hash seed spreads
it. `TestSelfHostOptimisationShapes/drop_variants_chained` pins the single
shape load on x86-64 and arm64.
