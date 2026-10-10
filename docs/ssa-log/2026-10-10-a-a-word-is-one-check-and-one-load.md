# 2026-10-10 — a word is one check and one load

`ssabounds.grouped_reads`, `ssabounds.word_loads`, the check-only `arr_get`.
Refs #10615.

## What changed

- **The check is split from the read.** `grouped_reads` used to keep a run's
  first read checked and place a probe read of the highest offset before it,
  so a fused word still made two checked byte reads whose bytes nothing used.
  It now places one check before the run's first read: an `array_get` with
  `imm` 2 (`ssasem.check_only()`), lowered to `arr_get` with `i32_imm` 2
  (`ir.check_only`), which aborts as the read would and loads nothing. On
  arm64 and x86-64 it is the compare and the trap; on wasm, the compare and
  `unreachable`; a view's check is `__sem_byte_view_check`, the compare and
  the abort without the byte load. Every read in the run is then unchecked,
  so `word_loads` drops all of them.
- **One index for both ends.** The check reads index `(lo + span) | (lo >> 31)`,
  with `lo` the run's lowest index and `span` the distance to its highest. It
  is in range exactly when every read is: a negative `lo` makes it -1, and an
  `lo + span` past INT32_MAX wraps negative.
- **Any order, and constant indexes.** The run no longer has to start at its
  lowest offset, so a word written with its bytes out of order is grouped,
  and reads at constant indexes (`b[4] .. b[7]`) group as offsets from no
  base, checked at the highest.
- **`word_loads` fuses words whose reads are all unchecked.** It returned
  early when a function had no checked read, which now is every function
  `grouped_reads` has finished with, and was already every function whose
  reads `proven_indices` had proven.

A word from an owned array is now, on arm64:

```
add x10, x9, #3 ; sxtw x10, w10 ; asr x11, x9, #31 ; orr x10, x10, x11
ldr x6, [x0] ; cmp x10, x6 ; b.lo 1f ; b __fern_oob_abort
1: sxtw x9, w9 ; add x0, x0, #8 ; add x0, x0, x9 ; ldrsw x0, [x0]
```

where main made a checked `ldrb` at `o + 3` and another at `o` first.

## Measured

arm64-darwin (Apple M-series, native), instructions retired from
`/usr/bin/time -l`, best of five. Both compilers are built from source by
the same compiler (main's stage 1 at a2084565). Every digest matches main's.

`cksum -a ALG` over 64 MiB of random input:

| digest | main | this change | change |
|---|--:|--:|--:|
| md5 | 788,759,687 | 786,881,475 | −0.24% |
| sha1 | 1,628,779,574 | 1,628,459,083 | −0.02% |

The digest kernels read their blocks through `[u8]` views, whose dead byte
load the register path already pruned; for them the change trades the
second compare and branch for the `asr` and `orr`, and md5's 64-byte block
is the same length either way. sha256, sha512, sm3 and blake2b (three runs
each) moved by less than the run-to-run spread.

An owned `u8[]` read as four words per call, one of them with its bytes out
of order, 64 passes over 1 MiB (`block` below): 264,300,942 → 243,110,122
instructions (−8.0%), cycles 40.6 M → 35.6 M.

```
@noinline function block(bs: u8[], o: i32): u32 {
    let a: u32 = bs[o] as u32 | bs[o + 1] as u32 << 8 | bs[o + 2] as u32 << 16 | bs[o + 3] as u32 << 24;
    let b: u32 = bs[o + 4] as u32 << 24 | bs[o + 5] as u32 << 16 | bs[o + 6] as u32 << 8 | bs[o + 7] as u32;
    let c: u32 = bs[o + 10] as u32 << 16 | bs[o + 8] as u32 | bs[o + 11] as u32 << 24 | bs[o + 9] as u32 << 8;
    let d: u32 = bs[o + 12] as u32 | bs[o + 13] as u32 << 8 | bs[o + 14] as u32 << 16 | bs[o + 15] as u32 << 24;
    return a ^ b ^ c ^ d;
}
```

## Traps

- The check's result is a placeholder nothing reads, but wasm still types
  the local it lands in: the check pushes a zero of the element's own type
  (`i64.const 0` for an `i64[]`), not an `i32`.
- `(lo + span) | (lo >> 31)` relies on the register backends holding an i32
  sign-extended, as every other i32 `>>` does.
