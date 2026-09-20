# 2026-09-20 — an array slice is a fresh array

`unsupported slice source: u8[]` / `i32[]` held eleven programs, eight of
them the digest tests: `h.update_bytes(msg[i:i + 7])` in a loop is the
shape every one of them tests. The refusal's comment said an array's slice
"hands back a second reference to the source's buffer, which is a kind this
vocabulary does not have". It does not: the stack IR's `arr_slice` copies
the window into a fresh array, 4- or 8-byte elements by the element width
(`irlower.slice_elem_is_wide`), and the checker types the result `[T]`,
which `typeinfo` spells as the same array type as `T[]`.

## What changed

A semantic kind of its own, `ssasem.arr_slice` (-40): three operands (the
source, `lo`, `hi`), a result of the source's type, a fresh unit
(`ssaunits.hands_out`) and not a projection, verified by
`arr_slice_error`, lowered by `ssarc.container` to `op_arr_slice(width)`.
`semsource.array_slice` produces it for a source whose elements own nothing
— a `u8`, `i32`, `i64`, `f64` or `bool` array — with an open end reading the
source's length and the bounds left to the runtime, which traps exactly as
the AST lowering's op does. An array whose elements own something is still
refused under the old message: `__fern_arr_slice` copies the words without a
retain, so the copy would alias them, and admitting it needs a retaining
copy first.

## Measured

Corpus census, x86-64, `FERN_SEM_IR_REPORT=1`, 864 programs:

| | before | after |
|---|---|---|
| declarations produced whole | 78,746 of 86,882 (90%) | 80,685 of 86,902 (92%) |
| programs produced whole | 792 of 864 | 804 of 864 |
| `unsupported slice source` sites | 16 | 0 |

No program's compile status changed, and the compiler emits itself
byte-identically with the change. `digest_md5_test`, `digest_sha256_test`
and `hash_checksums_test` produce whole and print the same TAP through
both lowerings.

`TestSelfHostSemanticProduction` gains `array-slice-of-scalars`: `u8`
slices handed straight to a call in a loop and an open-ended one, an `i32`
slice bound to a local, `i64` and `f64` slices for the 8-byte copy, pinned
`noLeak`. The sanitizer found the AST lowering never releases a slice that
is a call's argument (frees 1 of 4 on the probe; the same slice bound to a
local first is released), filed as #9843; the typed lowering frees all of
them.

## What the census says next

The roots left are all small and all deeper than a missing surface: the
mixed-module rule (`calls a function value of N arguments, a type the AST
lowering builds a value of`, 24 sites, every one of them the test runner's
`it` in a program whose `main` is held by another root), `map unit is not
shared` (13), the 13 methods no contract can key (`Empty.to_json`,
`string.tail`, hoisted bodies named through a box, `Reader.termios_get`),
`replacement of a capture` (8), `a view is lent, never retained` (8), and
the checker's `not yet checked` bindings in the Option and Result
combinator tests (8).
