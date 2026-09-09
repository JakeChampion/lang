# Self-hosted function type structure

This integrates the TypeRef correction from the preserved self-hosted typed-match
work. Exact function/result types are a prerequisite for typed captures, calls
and ownership analysis in the Fern-written compiler.

## Reproduced defect

`parser.parse_type_ref` stripped trailing array suffixes before finding the outer
arrow. It represented `(T) => U[][]` as an array of functions returning `U`, not
a function returning a nested array. Rendering that wrong tree produced the
original text, so the previous round-trip test passed. Callable accessors also
unwrapped only one grouping and lost deeply grouped function types.

The structural regression failed on the published parent: `fn=0 depth=2
retdepth=0` rather than `fn=1 depth=0 retdepth=2`, and the grouped function was
not recognized. Its text still round-tripped in every case.

The parser now recognizes an outer arrow before peeling suffixes, recursively
decoding the complete result. The shared `ref_ungroup` strips scalar grouping
only: it stops at an array or a real tuple. Callable classification, result
selection and parameter spelling all use that same operation.

## Verification

The expanded TypeRef driver tests function/array distinctions, nested arrows,
zero-parameter functions, grouped arrays, real tuples and opaque `fn`. Its old
round-trip expectations remain intact. The resolver driver additionally inspects
the actual semantic `TypeFunc.ret_type` and `TypeArray.elem` recursively; checking
only the existing debug string `fn` would miss the error again.

Local results, Linux ARM64 container be368d4b7a7c:

- Parent structural regression: FAIL, reproduced in 0.287 s.
- Initial TypeRef and both resolver tests after correction: PASS, 1.226 s.
- Expanded structural and semantic-result contract suite: PASS, 1.213 s.
- Production function values/calls, cross-module CLI, Wasm and ARM64 checker:
  PASS, 94.098 s, no skips.
- `make lint-all` and full source-lint suite: PASS (source lint 12.307 s).

Local x86 execution uses QEMU for correctness, not native timing claims. Full
integration, self-host fixpoints and driver-size gates remain required in CI.
This corrects the type boundary; it does not itself retire AST ownership analysis
or make the production callable parameter signatures fully precise.

## Measured cost

Comparable ARM64 `checker.fern` builds use the same #8959 bootstrap compiler
(Go 1.26.0, darwin/arm64), against parent #8960 497e89b76 and the corrected source.
Both are linked with `gcc -static -nostdlib` in the same Linux ARM64 image.

| Bytes | Parent | Corrected |
| --- | ---: | ---: |
| Linked ELF | 3,322,176 | 3,322,032 |
| `.text` | 3,141,144 | 3,141,016 |
| `.rodata` | 41,201 | 41,201 |
| `.eh_frame` | 40,460 | 40,460 |
| `.bss` | 4,160 | 4,160 |

The 128-byte instruction reduction is completely accounted for by symbol sizes:
`ref_fn_result` -104, `ref_is_fn_value` -80, new `ref_ungroup` +256, and the now-unused
flat `TypeRef` drop helper -200. `parse_type_ref` itself remains 7,500 bytes.
No size baseline changed and no general performance improvement is claimed.

Three alternating parent/corrected compile processes, after local test suites
finished, using `/usr/bin/time -l fern -target arm64-linux PATH/checker.fern`
with assembly output discarded. Host Apple M3 Pro, macOS 15.7.9:

| Source | Trial | Real seconds | User seconds | Maximum RSS bytes |
| --- | ---: | ---: | ---: | ---: |
| Parent | 1 | 1.00 | 1.96 | 288079872 |
| Corrected | 1 | 0.97 | 1.94 | 297664512 |
| Parent | 2 | 0.87 | 1.84 | 306331648 |
| Corrected | 2 | 0.94 | 1.86 | 268189696 |
| Parent | 3 | 0.88 | 1.86 | 270073856 |
| Corrected | 3 | 1.02 | 1.90 | 312836096 |

These short process observations are noisy and do not establish a timing or
memory improvement. Allocations and self-built compiler runtime were not measured.
