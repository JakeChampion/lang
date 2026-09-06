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

## Calibrating the two instruments against each other

The trace and `FERN_LEAKCHECK` disagree by exactly one thing:

| | bytes |
|---|--:|
| trace `live_bytes` | 272,733,408 |
| leakcheck `live_bytes` | 264,410,336 |
| difference | **8,323,072** |

The five `__fern_strbuf_grow` survivor rows sum to 4,194,304 + 2,490,368 +
1,048,576 + 524,288 + 65,536 = **8,323,072**. Exact.

The emitted `__fern_strbuf_grow` un-counts itself:

```
call __fern_alloc
movq __fern_lc_alloc_count(%rip), %rcx
subq $1, %rcx                     # un-count this allocation
movq __fern_lc_alloc_bytes(%rip), %rcx
subq %r12, %rcx                   # un-count these bytes
```

so leakcheck excludes the string builder deliberately and the rctrace hook does
not. Both are right; they agree once that is known. Anything quoting figures
from both — as this investigation has throughout — should say which.

The strbuf buffers ARE leaked: `strbuf_grow` allocates the new buffer, copies,
and never frees the old, which is why the survivor set holds the doubling
sequence 64 KB / 512 KB / 1 MB / 2.5 MB / 4 MB. It is bounded — log-many
generations, about 2x the final buffer — which is presumably why it is excluded
rather than fixed. Worth stating explicitly because at 8.3 MB in SEVEN blocks it
is the largest per-block retention in the trace and reads as a prize until the
un-counting is seen.

### What that corrects

The `asmcore` byte total quoted from this trace, 17.09 MB across 82 sites,
includes that 8.3 MB. By leakcheck's accounting `asmcore` retains about 8.8 MB —
mostly `add_string_lit` (3.67 MB in 448 blocks via `arr_slice`, 3.30 MB in 906
via the owned-append path) and a long tail.

The per-phase RATE table above is unaffected: seven blocks out of `asmcore`'s
35,259 allocations does not move 31.4%. Only the byte totals were inflated, and
only for `asmcore`. It stays the sharpest small case — native reclaims 100% of
the same source — but the target inside it is `add_string_lit`, not the builder.
