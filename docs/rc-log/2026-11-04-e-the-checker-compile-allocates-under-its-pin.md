# 2026-11-04 — the checker compile allocates under its pin

The allocation lane on main (#14175), the `x86_64/checker.allocs` row.

## The shape

The lane compiles `compiler/checker.fern` to assembly with a leak-check
build of the compiler and counts the allocations. The pin was 36,037,176.
Main at `49a78a6c` measured 38,923,162. The baseline note blamed two
checker merges. A compiler from before those merges measured 37.9M on
the old subject. The growth was spread over many sites, so the sites
below are the largest, not the newest.

## The measurement

A trace build (`FERN_LEAKCHECK=1 FERN_RC_TRACE=1`) writes one line per
allocation with its site and caller. `nm -n` maps each site to a
function. A `-cover` run of the same compile gives the hit count of each
line. The two together name the site and the line that allocates.

| repair | allocations after | fewer |
|---|--:|--:|
| main at `49a78a6c` | 38,923,162 | |
| three name tests read bytes in place | 38,415,666 | 507,496 |
| the last-use questions fold a boolean | 37,748,377 | 667,289 |
| the reach walk keeps one edge array; a map pass names its node kind | 37,089,966 | 658,411 |
| the small integers come from a constant table | 35,915,045 | 1,174,921 |

The x86-64 emitter writes 448,466 integers for the subject. 440,637 of
them lie between -256 and 256. The emitted assembly of four programs is
byte for byte the same with the table.

## Traps

- A buffer-built `ssa_mem` string costs the same three allocations as
  the concatenation it replaced, and leaks without a `buf_free`.
- A whole-tree map that calls its visitor on every node allocates one
  boxed triple per node to say that the node stays. A predicate on the
  node kind is cheaper than a visitor that answers "no change".
- The feature census pins the count of generic functions at 16. A
  wrapper per map shape moves the count. The `_where` family keeps it.

## Tests

The fixture corpus on the check, interp, x86-64 and wasm legs, the leak
census and the rctrace gates. Also the checker codes, the certifier, the
feature census and the complexity ratchet.
