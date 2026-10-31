# 2026-10-30 — the verifier checks record schemas once per function

`ssasem.Func.schema_checked`, `ssasem.schema_error` and the places that
change records or enums. Refs #8171.

## The shape

`ssasem.analyze` verifies the record and enum schemas of a function with
`semrecords.verify`. The compiler analyses each function about 2.5
times, and the schemas do not change between most of these analyses.
The schema check was about 1.15% of one compile of the compiler.

## What changed

`ssasem.Func` has a `schema_checked` mark. When an analysis passes, the
lowering sets the mark with `ssasem.checked_schema`. `schema_error`
does not verify the schemas again while the mark is set.

Each change to records or enums clears the mark:

- The inliner keeps the mark only when the callee adds no record and no
  enum.
- The pair pass clears it when it adds a callee enum or record.
- The layout pass clears it when it lays out the records again.
- A new function starts with the mark clear.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is main at 39d1bc8d0.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 100.941 G | 100.592 G (−0.35%) |

The whole-compiler binary is byte-identical to the base compiler's.
