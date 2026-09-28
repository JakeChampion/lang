# The lift writes only the scope rows it reads

`ssa_lift.lift_impl` keeps three per-scope rows, each `nslots` wide:
- the slots before an `if`;
- the then-arm's exit slots;
- a loop's header phis.

Every `if`, `block` and `loop` wrote all three, zeros included, so each scope
opened cost three writes per slot. Each kind reads at most one row:
- an `if` reads its snapshot, and its then-arm row once `else` has written it;
- a `loop` reads its header phis;
- a `block` reads none.

Each scope now writes only its own row. `row_room` grows a row array with
zeros when the row is new.

A loop's `end` also rebuilt the whole block list to replace its header block,
which is quadratic in the number of blocks. The header is appended after the
loop opens, so the scan now starts at the block count recorded then
(`sc_blk_from`), and the header is replaced in place.

Stage-2 (self-host-built) compilers from main 8d308f006, compiling to an
x86-64 binary, under callgrind:

| input | main | this change |
|---|--:|--:|
| `checker.fern` | 57,866,222,472 | 57,452,914,832 (−0.71%) |
| `ssa.fern` | 4,064,667,995 | 4,064,667,956 |

On `checker.fern`, `lift_impl`'s own cost falls from 2.62 G to 1.97 G. The
zero-fill it no longer does inline reappears as 0.27 G in `row_room`.

The two compilers emit a byte-identical `checker.fern` binary, and
`scripts/selfhost-emit-hashes` matches on all 1,926 rows.

The nine per-scope stacks beside the rows (`sc_else`, `sc_parent`,
`sc_blk_from`, …) are still written on every scope open, whatever the kind.
`put_at` appends when the depth equals the length, so a depth that some kinds
skipped would put a later scope's entry at the wrong index. Skipping those
writes needs the stacks padded the way `row_room` pads the rows.
