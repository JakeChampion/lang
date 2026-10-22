# 2026-10-22 — str_hash takes a short string whole

The `__str_hash` kernel on x86-64, arm64 and wasm. Refs #8171.

## The shape

`util.NameIndex.find` was 877 M Ir of self time on main at 0b88710, and
723 M Ir of that was the `__str_hash` kernel inlined through
`name_bucket`. The kernel hashes a string of 8 bytes or more a word at a
time. A shorter string built its zero-padded word a byte at a time, in a
loop of six instructions a byte on x86-64. Most names the compiler hashes
are that short.

## What changed

The short word is now loaded whole. From 4 bytes up it is two
overlapping 4-byte loads, the second shifted into place. Below that it
is the first, middle and last bytes, which cover every position of a 1,
2 or 3 byte string. Each load stays inside the string. The hash of every
string is unchanged, and `TestSelfHostStrHashIR*` checks lengths 0 to 40
against the reference on all three targets.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | main at 0b88710 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 73.462 G | 73.384 G (−0.11%) |

The whole-compiler binary differs, because each program carries the new
kernel. The compiler built with the new kernel and the one built with
the old produce byte-identical output from the frozen subject.

## What is left

The kernel's fixed cost is still about 30 instructions a call: the
string's length and data loads, the seed and basis mix, and the final
fold. `NameIndex.find` is called about 10 M times per self-compile from
many sites, with no single caller above 0.4%. A caller that hashes the
same name more than once can keep the bucket.
