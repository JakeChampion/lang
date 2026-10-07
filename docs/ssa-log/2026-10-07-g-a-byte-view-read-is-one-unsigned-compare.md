# 2026-10-07 — a byte view's read is one unsigned compare

`ssabytes.decoded_read`. Refs #10615.

## What changed

A checked read of a `[u8]` view compared its index twice, `index < 0` and
`index >= len`, each with its own branch to `__fern_oob_abort`. A length is
never negative, so `index >=u len` is the same test: a negative index reads
as larger than any length. The read now makes that one compare.

Where an index is proved, `ssabounds` still marks the read unchecked and it
makes no compare at all.

## Measured

`cksum -a ALG` over 4 MB of random input, x86-64, callgrind Ir, both
compilers built by themselves from source. The baseline is main at 546920c8.
Every digest matches GNU `cksum`, and the stage-2 compiler rebuilds itself
byte for byte.

| digest | main | this change |
|---|--:|--:|
| md5 | 101,567,018 | 93,178,282 (−8.3%) |
| blake2b | 118,648,944 | 110,243,440 (−7.1%) |
| sha1 | 155,963,732 | 147,574,996 (−5.4%) |
| sha512 | 184,483,583 | 176,094,719 (−4.5%) |
| sm3 | 291,956,310 | 283,829,722 (−2.8%) |
| sha256 | 296,477,810 | 288,089,074 (−2.8%) |
| crc | 2,408,518 | 2,408,518 |

That is two instructions per input byte, the second compare and its branch.
Compiling `checker.fern` moves by +0.02%.

## Traps

- md5 still makes one compare per input byte. Its 64 reads per block are
  `bs[off + k]` for constant `k`, and nothing relates `off` to the view's
  length, so `ssabounds` proves none of them.
