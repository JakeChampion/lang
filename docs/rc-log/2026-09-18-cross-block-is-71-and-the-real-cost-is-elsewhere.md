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

## The lead this opens — and why it is not "the typed path is 24.6% wasteful"

**The baseline aborts.** A compiler built through the AST lowering exits 134 —
`__fern_oob_abort` — compiling `literate.fern`, `printer.fern`, `lexer.fern`,
`parser.fern` and `checker.fern`, on all three targets, while the same sources
built through the semantic path compile every one of them. It reproduces on
`main` with no local changes and nothing gates it, because no test builds the
whole compiler through the AST lowering and RUNS the result. #9763.

So the table above compares against a lowering with a known miscompile, and the
obvious reading of it is not available. The AST path emits 3.7x fewer deep drop
helper calls and 4.7x fewer uniqueness tests **and its output faults on a
bounds check** — and a missing retain is exactly what turns a live value into a
freed one and an index into an out-of-bounds index. At least some of that
"extra" reclaim traffic on the typed path is correctness the AST path is
missing, which is also why every reclaim gap list is clean on both compilers
(`selfhost-leak-matrix`, `-arm64`, `construction-retain`, `container-sink`:
150, 150, 35 and 21 cells) — those grids are small programs the AST path still
gets right.

What survives as a lead is the question, not the number: is any of the typed
path's reclaim traffic avoidable? The candidates, in the order they should be
probed, each now needing its own correctness argument rather than a diff
against a broken baseline:

1. **Borrow inference.** A value proved borrowed needs no drop at all.
2. **Helper granularity.** 467 drop helpers against 131 suggests a distinct
   deep drop per type where one shared shallower helper would do.
3. **The uniqueness test per field.** `__sem_drop_T` tests `rc_is_unique` on
   each child before recursing; 41,545 calls is a measurable fraction by
   itself.

A worked non-example, because it looked like the whole answer for an hour:
`asmcore.EmitState.write` returns its own borrowed receiver unchanged, and
`emit_function_via_ir_named` carries 546 `rc_is_unique` + `__sem_drop_EmitState`
pairs that the AST path does not emit at all. Reduced to a nine-line fixture
the direction REVERSES — the typed path emits 2 deep drops where the AST path
emits 3 — so "the typed path fails to elide a returned borrow" is not what
those 546 are. Whatever they are, the fixture does not have it, and the
hypothesis was refuted in ten minutes by writing one.

## Trap

Both prices in the first table were stale by the time they were quoted, and the
entry that quoted them did not say when they had been taken. A ceiling measured
against an older rule set is not a ceiling. **Re-measure before building, and
put the date of the measurement next to the number.**

The second trap is larger and is the reason this section was rewritten: the
first draft read the call-count table as the typed path's waste, having never
run the baseline. **A differential against another implementation is worth
nothing until that implementation is known to be correct** — and here it took
one `echo $?` to find out that it was not.
