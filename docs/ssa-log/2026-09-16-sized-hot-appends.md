# Sizing the compiler's two hottest append targets

**Date:** 2026-09-16

## Why

After the emitter, fixpoint and dense-table work took the x86-64 SSA driver
compile from 41 s to 28 s, the profile's largest remaining item is the garbage
collector at 23%, and what feeds it is allocation volume: `growslice` alone is
4.24 s cumulative. Two of its callers grow a slice from nothing on every unit
of work.

## Measurements

Both estimates come from the repository rather than a guess.

| Quantity | Measured over | Value |
|---|---|---|
| Source bytes per token | 120 self-host sources, 12.5 MB, 1.68 M tokens | 7.46 |
| Machine instructions per SSA op | the driver, 97,985 blocks, 697,696 ops | 1.38 |

A block therefore holds about ten instructions, which an empty slice reaches
in five allocations and four copies.

## Change

`Tokenize` reserves one token per 7 source bytes, just past the corpus average,
plus a floor of 16 for files too short for the division to reserve anything.

The statistic that matters here is the byte-weighted mean, because what the
reserve buys is measured over the whole corpus rather than per file: at 7.46
bytes per token by volume, the reserve covers 106% of the corpus's tokens, so
nearly all the growth goes away in aggregate. It is not a per-file bound. The
distribution over the 120 sources is wider than the mean suggests:

| Statistic | Bytes per token |
|---|---|
| mean (byte-weighted) | 7.46 |
| median | 6.63 |
| p25 / p75 | 5.61 / 8.99 |
| min / max | 3.51 / 27.20 |

So 68 of the 120 files, 57%, sit under the divisor and still grow once. Sizing
for them instead would over-reserve on every long file, which is the trade
taken deliberately: the aggregate is what the collector sees.

`emitBlock` reserves `len(b.Ops)*3/2 + 4` instructions, the measured 1.38 per op
rounded up with room for the terminator and any edge moves.

## Result

Driver compile, alternating runs on the same machine, with GC cycles counted
under `GODEBUG=gctrace=1`:

| Build | Compile | GC cycles |
|---|---|---|
| main | 31.6 s, 30.7 s | 100 |
| this branch | 27.4 s, 27.5 s | 72 |

A first cut reserved one token per 8 bytes and moved the wall time not at all,
because 8 sits on the wrong side of the 7.46 average: every file still paid a
final growth. Seven covers it, and the same two lines are then worth 3.3 s.
The emitted assembly is byte-identical.

## Tests

- `TestTokenSliceIsSizedForTheCorpusDensity` pins the population the estimate
  came from: if the repository's own sources drift denser than one token per
  7 bytes, the divisor wants revisiting, and the failure says what the density
  now is.
- The lexer, parser and both SSA backend packages cover the behaviour, which
  a capacity change cannot alter; the byte-identical driver assembly is the
  end-to-end check.
