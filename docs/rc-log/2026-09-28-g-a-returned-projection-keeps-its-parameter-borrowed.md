# 2026-09-28 — a returned projection keeps its parameter borrowed (#8920)

Typed path (`ownership.fern`, the borrow inference over the produced graphs).

## Found by profiling

A `-g` self-built compiler (main 8c77182bc, built by itself) compiling
`coreutils/tsort.fern` under callgrind ran 3,726,160,909 instructions. The
parser's `(p: Par) peek(): Token { ... return p.toks[p.pos]; }` called
`__sem_release_parser__Par` 737,126 times. `peek` only reads its receiver, but
`carried_values` carried a return through `carry`, which marks the returned
value AND every value it is anchored to. `p.toks[p.pos]` is anchored to `p`, so
`p` escaped, and `inferred_mode` counts an escaping struct or union. Every call
then retained the whole `Par` at the call site and released it in `peek`.

## Change

A return lets out the returned value only. The unit planner already retains a
projection of a borrowed parameter to hand it back; that is the path an array
parameter always took, since the counted upgrade never applies to arrays. An
identity return (`return p;`) still counts `p`, because the returned value is
`p` itself. The returned-view special case folds into the same rule: a view
takes no unit, and it was already the only thing carried.

Constructions, stores and counted call slots still carry anchors. A projection
built into a container still counts its root. Whether that one can retain
instead is a separate question, not asked here.

## Witness

`TestSelfHostOwnershipInference`:
- `returned-projection-keeps-the-parameter-borrowed`: a cursor whose `peek`
  returns an element of its token array, called in a loop while the cursor
  stays live. Balanced at 4 allocations and 4 frees with no underflow on
  x86-64, arm64 and wasm.
- `modes-in-the-emitted-code`: `Cursor.advance` calls `__sem_release_Cursor`,
  proving the marker, and `Cursor.peek` must not. With the old
  `carried_values` the second assertion fails.

## Measured (x86-64)

Both compilers were built by themselves with `-g` (`make selfhost-cli`, then two
self-rebuilds). The new one reproduces itself byte for byte (stage3 == stage4).

| | main 8c77182bc | this change |
|---|---|---|
| instructions, `tsort` compile | 3,726,160,909 | 3,649,254,534 (−76,906,375, −2.06%) |
| self-built compiler binary | 13,744,552 B | 13,657,592 B (−86,960) |
| `tsort` binary | 109,704 B | 109,272 B, same output and exit code |

The largest self-cost drops:

| function | main | this change |
|---|---|---|
| `parser.Par.peek` | 48,650,156 | 19,902,402 |
| `__sem_release_parser__Par` | 31,533,883 | 16,541,525 |
| `__sem_release_checker__Scope` | 18,036,377 | 12,038,966 |
| `checker.Scope.lookup` | 21,754,255 | 15,906,431 |

`Scope.lookup` is the same shape: a reader that returns what it finds in its
receiver. The remaining `__sem_release_parser__Par` calls come from
`parse_primary`, `parse_binary`, `with_depth` and `parse_stmt_at`. Those were
not examined here and are the next lead.
