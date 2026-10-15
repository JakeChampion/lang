# 2026-10-15 — the certifier replays a block whose inputs held

`rccertify.settle`, `rccertify.Memo`, `ssalive.compute_kept`. Refs #12181.
No emitted byte changes, no change to any verdict.

## The shape

A cold compile of the compiler spends about 47% of its instructions in
`irverifygate.certify_or_refuse`: 62.6 G of 132.5 G. Most of it is
`settle`, which runs every reached block of a function in every round until
no row changes. The work in each round is blocks times tracked slots. On
that compile, 28% of it falls in the first round, 27% in the second, 26% in
the third and 20% after that. A round after the first reruns every block,
but only a loop's blocks can see a changed row.

A block's run reads three things from other blocks: whether each
predecessor has run, the row on each of its in-edges, and the inputs of its
tracked phis. Everything else it reads is either the walk's tables or its
own scratch row. With those three unchanged, the run repeats its last run
exactly.

Liveness had a second cost. `live_of` solved liveness for every named value
of the function, but `live_rows` reads a bit only for values whose root the
walk tracks.

## What changed

- In a cyclic settle, each block keeps a `Memo` holding the inputs its last
  run read and what it added to the round:
  - findings;
  - opaque calls;
  - the slots it left unproved;
  - the slots it met at maybe.
- A block whose inputs match its memo replays the memo instead of running.
  The match compares in place against the live rows.
- `maybe_at` keeps its rule that the first block wins. Replay sets only
  slots that are still unset, and it visits blocks in the same order.
- An acyclic settle is a single round, so it keeps no memos.
- `ssalive.compute_kept` gives a liveness bit only to the values a mask
  marks. A value's bit never depends on another value's, so a kept value's
  answer is the same as `compute`'s. The certifier passes the values it
  tracks.

## Measured

One cold compile of `compiler/fern.fern` (`FERN_RC_CERTIFY_MEMO=0`) under
callgrind, by `-g` compilers built from the same tree.

| | Ir |
|---|--:|
| before | 132.516 G |
| masked liveness alone | 131.164 G (−1.0%) |
| memo, comparing a built input array | 128.718 G (−2.9%) |
| memo with an in-place compare, plus masked liveness | 126.012 G (−4.9%) |

`certify_or_refuse` falls from 62.56 G to 56.09 G inclusive (−10.3%).
`settle` falls from 18.40 G to 13.24 G inclusive (−28%), of which
`block_round` is 9.71 G. The first memo built an input array per block,
which cost 2.16 G. The in-place compare costs about 1.4 G, inlined into
`settle`.
`ssalive.compute` falls from 4.78 G to 3.75 G.

Under both compilers, `FERN_RC_CERTIFY=report` prints the same findings
and the same census, line for line, on the whole compiler. The emitted
binaries are identical.

## Pinned by

- `leak-in-a-loop-body` in `TestSelfHostRcCertifyCases`. The loop body's
  inputs are the same in the second round as in the first, so its finding
  in the final round comes only from replay. Dropping findings from
  `replayed` fails this case and no other.
- `reuse-of-a-loop-box-through-its-token` already covers the unproved slots.
  Dropping them from `replayed` fails that case.
- `TestSelfHostSSALifetimeDependencies` now checks `compute_kept` against
  `compute` on every lifetime graph under two masks. Ignoring the mask fails
  all five graphs.

## Next

`walk_of` is now the largest piece, at 24.5 G inclusive, and
`walk_assuming` is 48.5 G of the certifier's 56.1 G. A walk under a token
rebuilds the walk's tables over the region it extracts. Every token walk
pays for that again, even where the region and the pruning are the same as
another token's.
