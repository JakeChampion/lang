# 2026-11-02 — base64 is a vector kernel

`buf_push_base64`, IR kind 487. A coreutils lead in #8278.

## The shape

`base64` of 64 MiB retired 1,362,116,330 instructions on x86-64, against
906,123,841 for GNU 9.4. `coreutils/lib/base.fern` packed six input bytes
into a word, looked up eight alphabet characters one at a time, and
pushed them with one `buf_push_u64` call. That was 20 instructions an
input byte, and the loop tested three conditions on each round that did
not change in the loop.

## The rule

`buf_push_base64(h, src, lo, hi, alphabet)` appends four characters of the
64-byte alphabet for each whole group of three bytes in `src[lo..hi)`. The
alphabet is an argument, so base64 and base64url use the same kernel. A
partial group at the end is the caller's.

- **x86-64.** A round loads 32 bytes at `src - 4`, so the 24 input bytes sit
  in the two lanes as the shuffle needs them. `vpshufb` and two multiplies
  take the 6-bit fields apart, the method of Muła and Lemire. Four
  `vpshufb` lookups over the four 16-byte quarters of the alphabet map the
  fields, and one store writes 32 characters. A round runs while 28 bytes
  are left, so no load reads past the array.
- **arm64.** `ld3` splits 48 bytes into their three byte streams, shifts
  form the four fields, one `tbl` over the four alphabet registers maps
  each field, and `st4` writes 64 characters.
- **wasm and both interpreters.** A loop encodes one group at a time.

`base.fern` gives the kernel the whole groups of each block, a line at a
time when the output wraps. A group that crosses the end of a line goes
out one character at a time.

## Two fixes on the way

The x86-64 register path loaded the arguments of a runtime buffer call
into `rdx` and `rcx` in order. A later argument whose home was one of those
registers was overwritten before it was read. The call now resolves all
its arguments through `ssa_parallel_moves`, as the other runtime calls
do. On arm64 an argument after the fourth now loads before the moves into
`x0` to `x3`, because `x4` holds no value but those registers do.

The in-process arm64 assembler encoded `ld1` and `st1` over one register
only. It encodes `ld1` to `ld4` and `st1` to `st4` over a register list
now, with or without a post-increment. The in-process x86-64 assembler
encodes `vpmulhuw`, `vpmullw` and `vpbroadcastd`.

## Measured

x86-64, instructions from callgrind, 64 MiB of random bytes.

| | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `base64` | 1,362,116,330 | 321,886,106 | 906,123,841 |
| `base64 -w 0` | | 100,362,804 | 718,940,310 |

The output is the same as GNU's for wrap widths 0, 1, 2, 3, 5, 7, 13, 64
and 76, and for `basenc --base64` and `--base64url`. With the default wrap,
each line of 57 input bytes is one kernel call. Wrapping inside the kernel
is the next step for that row.

## Tests

`TestBufBuilder/push-base64` encodes every range of a 160-byte input with
start offsets 0 to 6 under two alphabets. It checks each against a loop in
Fern, on the Go interpreter, x86-64, arm64 and wasm.
`TestSelfHostArm64AsmEncodingMatchesGas` and `TestSelfHostX86FormsMatchGas`
compare the new encodings with GNU as.
