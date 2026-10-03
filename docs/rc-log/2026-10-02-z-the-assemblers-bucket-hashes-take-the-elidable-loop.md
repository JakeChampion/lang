# 2026-10-02 — the assembler's bucket hashes take the elidable loop

`x86_native.x86_name_bucket` and `x86_line_bucket`. Refs #8171. Emitted
bytes unchanged: the `selfhost-emit-hashes` sweep is 1,965 rows, 0
differing from the compiler of `2026-10-02-w`, and the stage-2 checker
binary is byte-identical to it.

## What the profile named

The two bucket hashes of the x86 assembler, over every label name and
(since `2026-10-02-w`) every memoised line, bound the length to a local
and looped over `i < n`, which is not the shape the parser's
bounds-check elision takes, so every byte read carried its check.

## What changed

Both loop as `while (i < s.len())` over `s[i]` from `i = 0`, the shape
`2026-10-02-y` gives the name hashes of `util` and `checker`.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container,
stage 2 on top of `2026-10-02-w` (the line memo) at 3aab13aa.

| | line memo | this change |
|---|--:|--:|
| stage 2, total Ir | 30.230 G | 30.168 G (−62 M) |

## Witnessed

`TestSelfHostFeatureCensus`, `TestSelfHostFixtureSourcesCheck`,
`TestSelfHostSemanticSource*`, `TestSelfHostCheckerCodes*`, the lint
ratchet, and the emit-hash sweep. The rest is CI's.
