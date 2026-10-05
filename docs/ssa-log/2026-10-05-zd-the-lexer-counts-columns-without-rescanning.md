# 2026-10-05 — the lexer counts columns without rescanning

Self-host lexer, every target. Refs #8171.

## The shape

`lexer.Lex.advance_to(end)` moves past `[l.i, end)` and walks every byte of
it to count newlines, so the line and column stay exact. Every scan that
called it had already walked the same run to find `end`. So each
identifier, digit run, string piece and comment body was read twice. In
`skip_trivia`, each whitespace run and each comment also built a fresh `Lex`.

Most of those runs stop before a newline by construction: an identifier, a
digit run, a string piece that stops at `\n`, a comment body.

## The change

- `Lex.advance_in_line(end)` moves past a run that holds no newline. It adds
  the run's length to the column and reads nothing.
- Every scan whose run stops before a newline calls it instead of
  `advance_to`.
- `skip_trivia` counts lines and columns in locals as it scans, and builds one
  `Lex` at the end, none when there was no trivia.
- `advance_to` is left for the character literal, whose one decoded scalar
  can be a raw newline.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 3a1ce872 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, with and without `-g`, and main's
`fern.fern`, to byte-identical binaries:

| | main | runs counted once |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.741 G | 18.672 G (−0.37%) |
| `lexer.tokenize_impl`, inclusive | 478 M | 408 M |
| `lexer.skip_trivia`, inclusive | 113 M | 67 M |
