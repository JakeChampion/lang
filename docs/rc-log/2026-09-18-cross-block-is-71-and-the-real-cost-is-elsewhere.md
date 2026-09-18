# 2026-09-18 — cross-block is 71, and the real cost is elsewhere

No code change. Two measurements: the cross-block reuse lead re-priced on the
current pairing, and the thing that turned up while pricing it, which is
larger than every reuse lead put together.

## Cross-block, re-priced

`the-in-block-pairing-is-close-to-exhausted.md` put the cheap cross-block form
(donor's block is the construction's ONLY predecessor) at +31 and the
dominating-block ceiling at +144. Both predate the same-instruction pairing,
which consumed donors those probes counted. Re-measured with a scratch probe
over the whole compiler, against today's 1,167 pairings:

| | sites |
|---|---|
| donor in the construction's sole predecessor block | **12** (was 31) |
| donor in ANY block that dominates the construction | **71** (was 144) |
| skipped: functions over 120 blocks | 294 |
| no matching-slot donor in any dominating block | 3,371 |

71 is a ceiling: it ignores that the token must be threaded in a frame slot
across arbitrary control flow, proved unclobbered on every path from drop to
construction, and proved unspent on every back edge that reaches the
construction. That is a real mechanism on a memory-safety-critical path, for
at most 6% more pairings, and the cheap form that needs none of it is worth 1%.

**The lead is closed.** Not "next", not "expensive but worth it" — the number
halved as the in-block rules improved, which is what you would expect and is
the reason to re-measure a stale ceiling before building against it.

## What turned up instead

Comparing the two lowerings' emitted x86-64 for the compiler's own sources:

| | AST | typed | delta |
|---|---|---|---|
| instructions | 2,325,032 | 2,897,957 | **+24.6%** |
| `call` | 211,283 | 326,378 | **+54.5%** |
| `__fern_rc_is_unique` | 8,870 | 41,545 | **4.7x** |
| deep per-type drop helper calls | ~7,950 | 29,504 | **3.7x** |
| per-type drop helper definitions | 131 | 467 | 3.6x |
| `__fern_rc_inc` | 20,904 | 36,344 | 1.7x |
| `__fern_str_free` | 38,560 | 59,518 | 1.5x |
| `__fern_arr_dec` | 40,075 | 54,902 | 1.4x |
| `__fern_alloc_reuse` | 449 | 1,167 | 2.6x |

The extra calls and the pushes and `addq $N, %rsp` that go with them account
for roughly 361,000 of the 573,000 extra instructions. Reuse is 718 of the
115,095 extra calls — 0.6%. **The typed path's cost is its reclaim traffic,
not its reuse machinery**, and the reuse work of 2026-09-18 bought 718 avoided
allocations against a standing 115,095-call overhead that nobody had counted.

Emitted-line counts mislead here twice over, and both traps cost time:

- `.quad` data sits between function symbols, so a naive per-symbol line count
  attributed 65,328 lines to `__sem_drop_Bundle`, whose body is about 150
  instructions. Count instructions, not lines.
- The typed path emits 440,824 block labels against 238,292, of which 110,770
  are labels immediately followed by another label. Those are text, not code —
  they cost assembly time and nothing else, and no `jmp` targets the next
  label on either path, so the obvious peephole is already done.

## The lead this opens

Why the typed path reclaims 3.7x as often is not yet known and is the next
thing to measure. The candidates, in the order they should be probed:

1. **Borrow inference.** The AST path proves more values borrowed, so they
   need no drop at all. If the unit planner is counting values the AST path
   borrows, that is one change with a large multiplier.
2. **Helper granularity.** 467 drop helpers against 131 suggests the typed
   path emits a distinct deep drop per type where the AST path shares or
   inlines a shallower one.
3. **The uniqueness test per field.** `__sem_drop_T` tests `rc_is_unique` on
   each child before recursing; at 41,545 calls that test is itself a
   measurable fraction of the delta.

None of it is a correctness gap — every reclaim gap list (`selfhost-leak-
matrix`, `-arm64`, `construction-retain`, `container-sink`) is clean on both
compilers, 150, 150, 35 and 21 cells — so this is a performance question
about the path that is becoming the default, which is exactly the trade
`CLAUDE.md` says to weigh explicitly rather than assume.

## Trap

Both prices in the first table were stale by the time they were quoted, and
the entry that quoted them did not say when they had been taken. A ceiling
measured against an older rule set is not a ceiling. **Re-measure before
building, and put the date of the measurement next to the number.**
