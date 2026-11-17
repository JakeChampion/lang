# The shape test of a token costs nothing on arm64

2026-11-17. x86-64 and arm64. A measurement, with no change to the compiler.
Issue #15687. Main at `f3a6dd3b`.

## The question

`ssatokenkeep.keeps` gives a reuse allocation a branch on its token. Sometimes no
block above proved the shape of the token. The box side then first compares
the tag in the header of the token with the shape that the allocation builds
(`shape_test`). The box side then writes neither the shape nor a word that
the box already has.

When that pass landed, `aarch64/pvec_with.ir` rose 1.17%, and the x86-64 row
did not move. The issue named a possible cause: on arm64 the compare needed
`adrp` and `add` for the address of the shape. It asked for a count of arm64
instructions with the form and without it. If arm64 alone paid, the compiler
was to select the form for each target.

Two things changed after the issue:

- `proven` gives the form only to a site that keeps one word or more
  (`keeps_word`). The issue tried this limit and did not keep it.
- The shape of a box is a tag in its header (`fbbb1c6b`), so the compare
  needs no address.

## Measured

Two compilers from the same sources. "With" is main. "Without" has one
change: `proven` refuses each site with an untested shape. The listings of
four programs of `bench/` differ, and no other program has the form.

Instructions executed. x86-64 is Callgrind. arm64 is qemu with one
instruction for each translation block (`docs/LOCAL-DEV-LOOP.md`).

| Program | Target | With | Without | Without against with |
|---|---|---:|---:|---:|
| `pvec_with` | x86-64 | 46,375,485 | 46,375,848 | +0.00% |
| `pvec_with` | arm64 | 50,562,777 | 50,563,140 | +0.00% |
| `ordmap_insert` | x86-64 | 68,420,002 | 69,248,661 | +1.21% |
| `ordmap_insert` | arm64 | 84,848,635 | 85,511,563 | +0.78% |
| `perceus_deriv` | x86-64 | 51,439,934 | 51,526,334 | +0.17% |
| `perceus_deriv` | arm64 | 61,206,092 | 61,854,092 | +1.06% |
| `perceus_rbtree_ck` | x86-64 | 30,224,997 | 31,253,637 | +3.40% |
| `perceus_rbtree_ck` | arm64 | 34,374,614 | 35,470,774 | +3.19% |

Instructions in the listing:

| Program | Target | With | Without |
|---|---|---:|---:|
| `pvec_with` | x86-64 | 3,475 | 3,469 |
| `pvec_with` | arm64 | 3,689 | 3,681 |
| `ordmap_insert` | x86-64 | 6,026 | 6,010 |
| `ordmap_insert` | arm64 | 6,363 | 6,341 |
| `perceus_deriv` | x86-64 | 3,127 | 3,119 |
| `perceus_deriv` | arm64 | 3,377 | 3,373 |
| `perceus_rbtree_ck` | x86-64 | 3,301 | 3,254 |
| `perceus_rbtree_ck` | arm64 | 3,660 | 3,618 |

## The answer

The form does not cost instructions on arm64. In `pvec_with` it changes 363
instructions of 50.6 million on each target. In the other three programs the
form is a gain on the two targets, of 0.17% to 3.40%. The form adds 4 to 47
instructions to a listing.

Thus the form stays, and the compiler does not select it for each target.
The row `aarch64/pvec_with.ir` is now 50,562,775, below the 54,238,264 of
the pin before the rise.
