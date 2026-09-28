# A compare with a literal checks the length first

Part of #8920's work on what the self-host compiler emits.

## What changed

`__fern_str_eq` was the most expensive function in a self-host compile: 8.9%
of the instructions in compiling `coreutils/tsort.fern`, over 22M calls. Most
calls compare a string with a literal, in chains such as `lexer.is_keyword`,
`parser.precedence` and the x86 register-name tables. Most of those strings
differ from the literal in length.

`ssaunits.Plan` gains `text_lens`: each value's byte length when it is a
string literal, and -1 otherwise. `ssarc` lowers a `==` or `!=` with a
literal operand as a length test first. It compares the other operand's
length with the literal's and calls `str_eq` only when they match. This is
in the semantic lowering, so every backend takes it.

## Measured

The self-host compiler compiling `coreutils/tsort.fern` (callgrind, x86-64),
each compiler built by itself:

| | before | length first |
|---|---|---|
| instructions | 4,226,463,334 | 4,136,606,565 (−2.1%) |
| `__fern_str_eq` calls | 22,001,452 | 12,652,427 |
| stage 3 bytes (`-g`) | 13,360,072 | 13,548,504 (+1.4%) |

The size is each literal compare's inline length test. The built `tsort`
gives identical output, and stage 3 = stage 4.

`TestSelfHostTextEqLiteral` checks, on arm64, x86-64 and wasm, leak-checked
and under `FERN_SANITIZE`:

- equal and unequal strings of the same length, and of different lengths;
- the empty literal and a literal with an escape;
- a literal on the left and `!=`;
- a concatenated temporary and a slice view compared with a literal.

## Still calling

The remaining calls compare two non-literal strings: `ssasem.find_contract`,
`util.index_of_str`, `checker.Scope.lookup` and `ownership.names_has` scan
arrays of names. The x86 register tables still call on a length match.
