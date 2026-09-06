# The RECLAIM deficit is uniform, measured by free RATE

*2026-09-06* — the correction that matters most in this thread, and the second
time the same mistake produced it. Predecessor:
`2026-09-05-emit-retention-lead-was-an-artifact.md`.

## The measurement

Every matched free charged back to the phase that ALLOCATED the block. The
aggregator matches by pointer, so this is not the drop-helper bucketing that
spoiled the first attribution: a free is credited to whoever asked for the
memory, not to the helper that ran the release.

| phase | native allocs | native freed | self-host allocs | self-host freed | Δ points | alloc ratio |
|---|--:|--:|--:|--:|--:|--:|
| irlower | 1,029,287 | 89.6% | 1,464,973 | 39.9% | **−49.7** | 1.42x |
| parser | 691,679 | 78.1% | 609,847 | 34.7% | **−43.5** | 0.88x |
| asm_ir | 372,889 | 74.6% | 1,050,636 | 45.1% | **−29.6** | 2.82x |
| lexer | 197,983 | 58.1% | 183,287 | 2.6% | **−55.4** | 0.93x |
| ir | 73,582 | 81.2% | 113,992 | 47.8% | **−33.4** | 1.55x |
| astwalk | 81,232 | 97.3% | 94,361 | 12.7% | **−84.6** | 1.16x |
| asmcore | 23,693 | 100.0% | 35,259 | 31.4% | **−68.6** | 1.49x |
| ircore | 27,643 | 100.0% | 31,803 | 37.6% | **−62.4** | 1.15x |

Every phase is 30 to 85 points worse. Allocation counts are broadly comparable,
0.88x to 2.82x. **The self-host does not allocate dramatically more; it frees
dramatically less, everywhere.**

## The mistake this corrects, made twice

The predecessor ranked phases by RETAINED BLOCKS and concluded the gap was
concentrated — `irlower` at 14.6x, `ir` at 1.4x, "same reclaim model, near
parity, so whatever irlower does differently is that module's code". That
reading does not survive the rate table. `irlower` tops the survivor ranking
because it allocates 1.46 M blocks, the most of any phase; its free rate (39.9%)
is close to `ir`'s (47.8%), and `ir` is itself 33 points behind native. The
control was not a control.

The same document had already caught itself ranking by BYTES and mistaking a
0.14%-of-blocks cluster for the headline. Ranking by an aggregate and reading
concentration into what is really volume is one error made twice, in one
investigation, by the same author. It is worth naming as a class rather than
fixing case by case: **rank by the rate, or by the excess over the oracle, never
by the total.**

## Where it points

The uniform-deficit reading is back, now with a per-phase measurement rather
than an aggregate behind it: 38% against native's 83% is not one phase's
problem, and no per-construct fix addresses it.

`irlower` still holds the most bytes, so a fix there recovers the most — but
whatever it fixes is not special to `irlower`. The sharpest small cases are
`asmcore` and `ircore`: native reclaims those **completely**, 100% on both,
against the self-host's 31.4% and 37.6%. Small, unambiguous, and a total on one
side is the easiest kind of target to reason about.

## Caveat

Native's `(other)` bucket is 503,360 allocations — it inlines enough that a
large share is unattributable, so its per-phase VOLUMES are understated. The
per-phase RATES should be robust to that, since a block and its free are
attributed identically, but that is an argument rather than a measurement and
native's absolute counts should not be leaned on.

Both sides remain EXIT-LIVE sets, so a phase whose output outlives it (the
lexer's tokens, the IR) legitimately retains. That is exactly why the rate
comparison against native is the number to read: it holds that confound fixed on
both sides.
