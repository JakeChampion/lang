# 2026-11-03 — base32 decodes with a vector kernel

`buf_decode_base32`, IR kind 489. A coreutils lead in #8278, after
`2026-11-03-a-base32-is-a-vector-kernel.md`.

## The shape

`base32 -d` of 64 MiB retired 2,864,683,935 instructions on x86-64, against
2,248,120,119 for GNU 9.4. `coreutils/lib/base.fern` decoded two characters
at a time through a 256 KB pair table, at about 15 instructions a
character.

## The rule

`buf_decode_base32(h, src, lo, hi, ranges)` appends five bytes for each
whole group of eight characters in `src[lo..hi)`. It stops at the first
group with a character outside both ranges, and returns the index after
the last group it decoded. `ranges` is four bytes: the start and length of
the run of characters for the values from 0, then of the run for the rest.
Both base32 alphabets are two runs, `A`-`Z` then `2`-`7`, and `0`-`9` then
`A`-`V`. So two unsigned range tests check a character, and no table is
necessary.

- **x86-64.** A round checks 16 characters with `vpsubb`, `vpminub` and
  `vpcmpeqb` for each range, and `vpmovmskb` tests them all. `vpmaddubsw`
  and `vpmaddwd` join the 5-bit values into 20-bit dwords, and two shifts
  join each pair of dwords. `vpshufb` puts the 10 bytes in order.
- **arm64.** A round checks 16 characters with `sub` and `cmhi`, and
  `uminv` tests them all. Shifts at halfword, word and doubleword width join
  the values, and `tbl` puts the 10 bytes in order.
- **wasm and both interpreters.** A loop decodes one group at a time.

A round stores 16 bytes for 10 decoded bytes, so the reserve keeps 16 bytes
past the decoded length. A group that a round does not take goes through a
loop of one character at a time, which stops at the bad character.

`base.fern` calls the kernel at the start of each input row. The newline,
the padding and a bad character stop it, and the existing paths take over
from there. `b32_rows` and its pair-table loop are gone.

| x86-64, 64 MiB | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `base32 -d` | 2,864,683,935 | 1,344,141,528 | 2,248,120,119 |
| `base32 -d`, input from `-w 0` | 1,606,924,845 | 207,956,602 | 1,476,307,072 |

The output, the diagnostics and the exit status are the same as GNU's, on
x86-64 and on arm64 under qemu. The inputs were 19 sizes from 0 to 4,099
bytes at five wrap widths, and 1 MB. The edge cases included padding, bad
padding, a bad character, lower case and spaces. Garbage with and without
`-i`, CRLF and an empty file were also inputs.
`basenc --base32 -d` and `--base32hex -d` also decode 1 MB without a
difference.

## Also in this change

- The x86-64 assembler encodes `vpmaddubsw`, `vpmaddwd`, and `vpsllq` and
  `vpsrlq` by an immediate. The shifts by an immediate are a table now, so
  `vpsrlw` is not a special case.
- The x86-64 assembler encodes `vpbroadcastb` and `vpbroadcastd` into an
  `xmm` register.

## Tests

`TestBufBuilder/decode-base32` decodes every range of 200 characters with
start offsets 0 to 8, in both alphabets, with and without bad characters
inside a round. It checks the bytes and the returned index against a loop
in Fern, on the Go interpreter, x86-64, arm64 and wasm.
`TestSelfHostArm64AsmEncodingMatchesGas` and `TestSelfHostX86FormsMatchGas`
compare the new encodings with GNU as.
