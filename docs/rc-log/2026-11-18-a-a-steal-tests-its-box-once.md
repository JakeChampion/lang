# A steal tests its box one time

2026-11-18. Each target. The change is in `ssarc`.

## The gap

A steal reads a counted field out of a box that dies. The lowering tested
the uniqueness of the box, and on the unique arm it nulled the slot. Then it
tested the same flag again, and on the shared arm it retained the field
(`ssarc.steal`, then `unless_unique`).

The two tests cost a compare and a branch at run time. The certifier walks
each test apart. At the join after the first test, the field holds the unit
that it took on one edge, and no unit on the other. So it is unproved. Over
`fern_main`, 2,123 unproved values were field loads and 903 were element
loads with a borrowed origin. Most of them were steals.

## The change

`ssarc.steal` tests the box one time. The unique arm nulls the slot, and
the other arm retains the field. The scratch slot for the flag is gone.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 7,634 | 4,381 |
| `wasm32/checker.unproved` | 2,275 | 1,284 |
| `x86_64/checker.refuted` | 4,993 | 5,033 |
| `x86_64/checker.weakened` | 170 | 235 |
| `x86_64/checker.missed` | 477 | 372 |

The certifier finds nothing over `fern_main`. Of the 105 mutants that the
certifier no longer misses, it refutes 40 and finds 65 weaker. The assembly
of `fern_main` is 202,382,433 bytes, from 202,691,122.
