# 2026-10-07 — a run of reads is checked at its ends

`ssabounds.grouped_reads`. Refs #10615.

## What changed

Every digest kernel loads a block as `bs[off]`, `bs[off + 1]` ...
`bs[off + 63]` (127 for sha512). Nothing relates `off` to the view's
length, so `proven_indices` proves none of those reads, and each one made
its own bounds check: 65 abort sites in md5's kernel.

`grouped_reads` runs after `proven_indices`. It takes a run of checked reads
of one array at one index plus constants, `x + c`. Nothing between the
run's first read and its last may abort another way or be observed: only
constants, phis, integer and boolean arithmetic other than `/` and `%`,
integer casts, lengths and array reads may sit between them. The pass then
checks the run at its two ends:

- the first read keeps its check, and must have the smallest offset, at
  least zero;
- a probe read of the largest offset is placed before it;
- the reads between are unchecked.

With the smallest offset at least zero, the two checks cover every offset
between them, and an index that wraps past `i32` fails the probe. The
abort names no index, so aborting at the probe rather than at the read that
would have failed cannot be told apart. Only reads of an integer or boolean
are grouped, because the probe's result is never used and must own nothing.

Every digest kernel now has two abort sites.

## Measured

`cksum -a ALG` over 4 MB of random input, x86-64, callgrind Ir, both
compilers built by themselves from source. The baseline is main at b22f8ed6.
Every digest matches GNU `cksum`, and the stage-2 compiler rebuilds itself
byte for byte.

| digest | main | this change |
|---|--:|--:|
| md5 | 93,178,228 | 84,985,908 (−8.8%) |
| blake2b | 110,243,386 | 105,327,799 (−4.5%) |
| sha1 | 147,574,942 | 141,086,584 (−4.4%) |
| sha512 | 176,094,665 | 169,114,673 (−4.0%) |
| sha256 | 288,089,020 | 282,387,106 (−2.0%) |
| sm3 | 283,829,668 | 278,783,124 (−1.8%) |
| crc | 2,408,464 | 2,408,464 |

Compiling `checker.fern` for x86-64: 14,146,467,320 Ir on main,
14,160,211,963 with the pass (+0.10%).

## Traps

- A division between two reads keeps both checks: `10 / d` with `d` zero
  must fault before a later read's out-of-range abort.
- A run whose first read is not its smallest offset is left alone, as is
  one with a negative offset: the first read's check is what bounds the low
  end.
