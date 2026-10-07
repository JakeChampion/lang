# 2026-10-06 — a punctuation token carries a literal

`lexer.match_multipunct` and the single-character punctuation arm of
`lexer.tokenize_impl`. Refs #8171. No emitted byte changes: the compiler
before and after builds `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, byte for byte.

## What changed

Every punctuation token's text was a slice of the source materialised
with `+ ""`: a string allocation and a concat per `(`, `,`, `.`, `==`
and so on, 412 k of them on a `checker.fern` compile, each freed when the
token went. The punctuators are a closed set, so each is now a literal
string the lexer returns from a byte test: a literal is immortal, so the
token carries no allocation, no copy and no release. `is_single_punct`
is gone with the slice it guarded; `single_punct` is the same set as a
lookup, and the multi-character matcher returns its literal from the
bytes it already read, so it takes no slice either and the note about
split code points is moot.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin, and
both compile main's sources: `checker.fern` imports `lexer.fern`, so a
compile of the branch's tree would also be compiling the changed module,
which cost 60 M more work and hid the win the first time it was measured.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.789 G (−0.43%) |
| `lexer.tokenize_impl`, self | 190.8 M | 179.8 M |
| `__fern_str_concat`, self | 158.2 M | 146.0 M |
| `__fern_str_eq`, self | 197.8 M | 185.4 M |
| `__fern_alloc`, self | 343.4 M | 337.3 M |
| `__fern_str_free`, self | 77.0 M | 70.1 M |

The string-equality drop is the parser's operator comparisons: two
literals of the same spelling share one address, which the kernel
answers before it reads a byte.

## What is left in the lexer

The lexer's state is a record rebuilt per token (`Lex`, released 1.8 M
times a compile now, 6.8 M before), the trivia skip returns a record per
token, and an identifier's text is still a copy of the source. Each is
an allocation per token.
