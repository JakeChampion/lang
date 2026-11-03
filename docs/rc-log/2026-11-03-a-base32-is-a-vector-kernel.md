# 2026-11-03 — base32 is a vector kernel

`buf_push_base32`, IR kind 488. A coreutils lead in #8278, after
`2026-11-02-f-base64-is-a-vector-kernel.md`.

## The shape

`base32` of 64 MiB retired 1,686,435,808 instructions on x86-64, against
1,167,468,675 for GNU 9.4. `coreutils/lib/base.fern` packed five input
bytes into a word through a call to `pack_value`. Then it looked up eight
alphabet characters one at a time, and pushed them with `buf_push_u64`. The lookups
were close to the minimum for a scalar loop, at about six instructions a
character. So the loss was the scalar loop itself, not the code the
compiler made for it.

## The rule

`buf_push_base32(h, src, lo, hi, alphabet)` appends eight characters of the
32-byte alphabet for each whole group of five bytes in `src[lo..hi)`. The
alphabet is an argument, so base32 and base32hex use the same kernel. A
partial group at the end is the caller's.

- **x86-64.** A round reads four groups as two 32-byte loads, at `src - 6`
  and `src - 1`, so each 128-bit lane holds one group. `vpshufb` puts the
  two bytes under each 5-bit field in a word, and `vpmulhuw` shifts each
  word right. After `vpand` and `vpackuswb`, two `vpshufb` lookups map the
  fields, one for each 16-byte half of the alphabet.
- **arm64.** Four `tbl` lookups over 32 loaded bytes put the field bytes of
  each group in halfwords, and `ushl` by negative counts shifts them right.
  `uzp1` keeps the low bytes, and one `tbl` over the alphabet maps them.
- **wasm and both interpreters.** A loop encodes one group at a time, and
  the two interpreters share one path with base64.

A round reads past `hi` when the array goes on. So a round runs while 20
bytes of groups are left and the load ends inside the array, not inside
`hi`. Without that, a call per output line of 45 bytes ran one round and
five scalar groups.

The kernel needs 32 alphabet bytes when it encodes a whole group. A shorter
alphabet gets the array bounds abort.

`base.fern` gives a codec to a kernel when its alphabet has `1 << bits`
characters: base64 and base64url, base32 and base32hex. With a wrap, the
kernel encodes the whole groups that fit on the line. Fern code encodes the
one group across the line end.

| x86-64, 64 MiB | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `base32` | 1,686,435,808 | 577,364,394 | 1,167,468,675 |
| `base32 -w 0` | 1,451,516,489 | 106,575,592 | 942,574,274 |

The output is the same as GNU's on x86-64, and on arm64 under qemu. The
wrap widths were 0, 1, 2, 3, 5, 7, 8, 13, 40, 64, 76, 77 and 2^63-1. The
same is true for `basenc --base32` and `--base32hex`. With the default wrap,
the group across the line end is now the larger part of each line.

## Also in this change

- The stack paths of both native backends took the need of a buffer helper
  from a chain of kinds. They take it from `buf_needs` now, the same lookup
  the register path uses, by the helper's name.
- `buf_arity` reads a table beside `BUF_KINDS`, and the interpreter's
  buffer builtins are a list. That keeps the compiler under its complexity
  ratchet.
- The x86-64 assembler encodes `vpackuswb`.

## Tests

`TestBufBuilder/push-base32` encodes every range of a 160-byte input with
start offsets 0 to 6 under two alphabets, and the RFC 4648 vector
`foobar`. It checks each against a loop in Fern, on the Go interpreter,
x86-64, arm64 and wasm. `TestSelfHostArm64AsmEncodingMatchesGas` and
`TestSelfHostX86FormsMatchGas` compare the new encodings with GNU as.
