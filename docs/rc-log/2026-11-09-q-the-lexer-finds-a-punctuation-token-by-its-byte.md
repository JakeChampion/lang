# The lexer finds a punctuation token by its byte

`lexer.single_punct` gives the text of a one-byte punctuation token.
It searched the 25 punctuation bytes in turn, for each token that is not an identifier, a number or a string.
In one compile of the subject, the search loop ran 6.4 M times.

`PUNCT_SLOT` now maps each byte below 128 to its slot in `PUNCT_TEXTS`, so one load finds the token.
`PUNCT_BYTES` is deleted.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,255,501,566 |
| this change | 29,196,408,443 (−0.20%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
