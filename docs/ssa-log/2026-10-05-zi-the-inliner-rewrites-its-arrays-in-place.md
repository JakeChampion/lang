# 2026-10-05 — the inliner rewrites its arrays in place

Self-host typed lowering, every target. Refs #8171.

## The shape

#11576 brought `seminline` back: it splices small callees into their callers
and splits constructions that are only read apart. Compiling `checker.fern`,
it added about 4 G instructions to the stage-2 compile, which went from
17.87 G to 21.89 G on the same input. Most of that was array copying,
`__fern_arr_slice` alone taking 1.34 G. Three of its loops updated an array
with `.with` while something else still held it, so each update copied the
whole array:

- `counted` took the per-value read counts by value, so each counted operand
  copied an array the size of the function's value table. `all_kept` did the
  same for the kept flags.
- `split_all` and `splice_round` kept the function array in a struct they
  rebuilt every iteration, so each rewritten function copied all 1,889 bodies
  and retained and released each of them: 3.5 M releases in `split_all` alone.

`counted` and `all_kept` take their arrays `own`. `kept_values` keeps its
flags in a local where it used to thread them through a `Spread` struct and
`copied_back`. `split_all` and `splice_round` keep the arrays they rewrite in
locals and build the result once at the end. After the first `.with` copies
a borrowed array, each later one writes in place.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at fac64a9a against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on every row:

| | main | rewritten in place |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 21.947 G | 20.881 G (−4.86%) |
| `seminline.inline_leaves`, inclusive | 1,518 M | 452 M |
| `seminline.split_all`, inclusive | 1,197 M | 167 M |
| `seminline.drop_unread`, inclusive | 597 M | 21 M |
| `__fern_arr_slice`, inclusive | 1,356 M | 428 M |

Most of what is left in `inline_leaves` is `splice` itself, 190 M over 738
splices.
