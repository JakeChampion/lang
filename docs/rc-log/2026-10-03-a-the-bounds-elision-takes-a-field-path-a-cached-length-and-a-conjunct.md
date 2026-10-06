# 2026-10-03 — the bounds elision takes a field path, a cached length and a conjunct

`parser.elide_len_bounded_body`. Refs #8171.

## What the profile named

Every `cmp; jae __fern_oob_abort` left in the stage-2 compiler costs
699 M Ir on the 25.44 G compile of `checker.fern` (2.7%), over 11,404
sites. The parser's elision took one guard: `while (i < xs.len())`
with `xs` a bare ident, `i` starting at a non-negative literal in the
statement immediately before the loop. Counting the `while` guards in
`compiler`:

| guard | loops |
|---|--:|
| `i < xs.len()` | 2,420 |
| `i < x.f.len()` | 1,160 |
| `i < n` | 502 |
| `i < xs.len() && …` | 150 |

Only the first row was elided.

## What changed

The pass now also marks reads under:

- a field path: `while (i < r.items.len())` over `r.items[i]`. Fields and
  elements are immutable after construction (E048, E056), so `r.items`
  stays the same while nothing assigns or rebinds `r`.
- a cached length: `while (i < n)`, when the nearest earlier statement at the
  same level binding `n` is `let n = P.len()` and nothing between that and the
  loop assigns `n` or assigns or rebinds `P`'s root. The loop body may not
  assign or rebind `n` either.
- a guard that is one top-level `&&` conjunct. Reads in the condition to
  the right of the bounding conjunct are marked too, since they are
  evaluated only after it holds: `while (i < s.len() && s[i] == 32)`.

`i`'s start value can now come from any earlier statement at the same
level, provided nothing between assigns or rebinds `i`. That is what lets
`let i = 0; let n = xs.len(); while (i < n)` qualify.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at be489e35 and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.379 G (−0.26%) |
| bounds-check sites in the stage-2 binary | 11,404 | 9,413 |
| `elide_len_bounded_body` inclusive | 21 M | 29 M |

The pass costs 8 M more to run, and that is inside the net figure.
Emitted bytes change on 650 of the 1,965 `selfhost-emit-hashes` rows (218
x86-64, 218 arm64, 214 wasm32), none refused.

The 1,991 sites removed are 17% of the total, but the gain is smaller than
17% of 699 M. A path guard still reloads `r.items` and its length every
round. That reload costs about what the check did (the native measurement
in `docs/ARRAY-PIPELINE-BASELINE-2026-09.md` §2 found the two equal), so only
the cached-length shape saves both.

## Witnessed

`TestSelfHostBoundsElideIR{X86_64,Wasm,Arm64}` carry a correctness case
for each new shape against the interpreter. On x86-64 a differential
confirms each shape emits fewer `__fern_oob_abort` than a twin bounded by
a literal, and six cases must still trap at an out-of-range read: the
array reassigned in the body or before the loop, `n` reassigned, the
path's root reassigned before the read, the index reset between its start
and the loop, and a condition read to the left of the guard.
