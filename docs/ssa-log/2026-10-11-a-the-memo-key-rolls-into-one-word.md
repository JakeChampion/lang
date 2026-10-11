# 2026-10-11 — the memo key rolls into one word

`util.roll` and its family, `rcmemo.key`, `ircore.lowered_digest`. Refs
#8171. No emitted byte changes.

## The shape

Every compile certifies its reference counting through a memo
(`rcmemo.fern`), so a warm compile pays only the key: a digest of each
function's name, contract, lifted shape, every op field the lift reads, and
the contracts of the callees it names. On the stage-2 compile of
`checker.fern` with a warm memo (1,616 hits, no misses), `certify_or_refuse`
was 898 M Ir of 16.61 G, 5.4% of the compile, and nearly all of it was the key.

`util.Digest` was a record of two 30-bit polynomial lanes. Each `roll`
built a new one: 8.19 M `__fern_arr_box` calls on this compile, plus the
releases of the old ones (4.57 M in `certify_or_refuse`, 3.3 M in
`roll_wide` and `roll_text`). A word also rolled in two steps per lane, its
low 30 bits and then its top two, so `roll` was about 40 instructions, past
`seminline`'s leaf limit, and was never spliced. An op rolled about thirteen
words.

## What changed

A digest is now an `i64`. Each word is xored into the state. The state is
then multiplied by an odd constant, and its high half is folded into its
low half. Each step is a bijection of the state, so two sequences of one
length that differ in one word never roll the same. Every bit of a word
reaches every bit of the state.
The old lanes did not have that property: the low k bits of a polynomial
roll modulo 2^30 depend only on the low k bits of what was rolled. There is
no record left to build, and `roll` is a leaf. `lowered_digest`, the
per-module cache key, uses the same functions. `rcmemo.version` is now 3,
because a key from the old digest means nothing to the new one.

`TestSelfHostRcCertifyMemoKey` gains a variant whose i64 immediate differs
only in bit 62. With the high half dropped from `roll_wide`, it fails, and
so does the existing float-immediate variant.

## Measured

`checker.fern` (this tree) built for x86-64-linux under callgrind. The
compilers are stage 2s built with `-g` by the same stage 1, the base from
main at b3766ab. Each compiler had its own memo, warmed by one compile
before the measured one. The base and this change compile `checker.fern` for
x86-64, arm64 and wasm to the same bytes. The changed stage 2 rebuilds
`fern.fern` byte for byte.

| | before | this change |
|---|--:|--:|
| total Ir | 16.612 G | 15.835 G (−4.68%) |
| `certify_or_refuse`, inclusive | 898 M | 121 M |

Of the 121 M that is left, 29 M is `rccertify.Abis.lookup` and 25 M is
`roll_text`.

The whole compiler self-compiling (`fern.fern` to an x86-64 binary, warm
memo, median of three interleaved runs on the 4-core container) took 23.50 s
before and 22.83 s after. User time went from 20.93 s to 20.18 s.

## Tried first

Letting a `seminline` leaf build a record of scalars, so that a spliced
`roll` would be read apart rather than boxed, moved this compile by
0.0005%. `roll` was over the leaf limit regardless, and nothing else in the
compiler took the new shape. That change was not kept.
