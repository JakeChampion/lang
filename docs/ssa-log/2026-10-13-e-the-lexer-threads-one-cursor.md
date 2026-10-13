# 2026-10-13 — the lexer threads one cursor

`lexer.fern`: `Lex`, `skip_trivia`, `advance`, `advance_in_line`, `skip` and
the scanners. Refs #8171. No emitted byte changes for the programs the
compiler compiles; the compiler's own lexer allocates less.

## The shape

`tokenize_impl` carried its position in a `Lex` record and threaded it by
value: every `skip_trivia` returned a fresh `TrivResult` holding a fresh
`Lex`, every `advance` built a new `Lex`, and the scanners took the cursor
borrowed, so none of those boxes could be reused. On the stage-2 compile of
`checker.fern` that was two boxes per token and one per byte advanced
through a literal, each an allocation, a header write and a release.

## What changed

The comments ride on the cursor (`Lex.comments`), so `skip_trivia` returns
the cursor alone, and the cursor is `own` everywhere it moves: through
`skip_trivia`, `advance`, `advance_in_line`, the new `skip` (an advance by
`k` bytes within a line, so the moved receiver is not read again in its own
argument) and the scanners, which return it in their result. A record update
of an owned, unique base writes its box in place, so the one cursor box
lives for the whole scan.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the first-append entry
before this one.

| | Ir |
|---|--:|
| base | 14.607 G |
| one cursor | 14.579 G (−0.19%) |

`tokenize_impl` −13.2 M, `__fern_arr_box` −7.3 M and `__fern_arr_dec` −6.9 M:
about 600 K boxes fewer. The compiled `checker.fern` is byte-identical, and
the compiler still rebuilds itself byte for byte. `TestSelfHostLexer*` and
the comment-carrying CLI suites are the gates.
