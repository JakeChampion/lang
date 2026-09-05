# 2026-09-05 — an element appended by index is a counted store

`out = out.append(pre[p])` over a `struct` array stored pre's element box into
out with no retain. `pre`'s own release then dropped the box, out kept the
pointer, and the next string allocation recycled the memory. The same for an
element of an array of arrays. String and enum elements were already balanced
by their own rules (`str_param_elem_escapes` and the local deep-free
withdrawal; the enum read) and are unaffected.

Found by the LICM mirror (#8245): its prologue list is built with appends and
spliced into the output with `out = out.append(pre[p])`. The native-built
`bin/fern-selfhost` ran the pass correctly, since native's lowering counts the
store; the stage-2 compiler — the same source compiled BY the self-host —
refused its own modules with garbage op kinds (`kind 269466528`; `kind
538976288` is `0x20202020`, four spaces from the recycled string). It failed
with and without hoisted code, which is what separated the pass's own
miscompilation from the transform: a stage-2 built by the commit-1 self-host,
which does not hoist, failed the same way.

## Measured — x86-64, base self-host vs the retain, `bin/fern -interp` as oracle

`pre` built by three appends inside a callee, `out` filled from `pre[p]`, the
callee returns `out`, main recycles the freed boxes through 64 string concats
and then sums the elements.

| element kind | base self-host | with the retain | interp |
|---|---|---|---|
| `struct Op { k, s }` | **26** | 79 | 79 |
| `i32[][]` | **26** | 79 | 79 |
| `string[]` | 16 | 16 | 16 |
| `enum E { A(i32), B }` | 79 | 79 | 79 |
| all three of string / enum / `i32[][]` in one program | **113** | 166 | 166 |

The fix is `indexed_box_elem_escapes` — an `ExprIndex` whose element is a
struct (`expr_struct_type` resolves to a struct decl) or an array
(`expr_is_arr_src` / `expr_is_strarr` / `expr_is_f64arr` / `expr_is_i64arr`)
— asked at the three sites that lower an appended element: the `a = a.append`
statement, the expression-position push (`lower_call_append`) and the clone
form (`lower_append_elem_value`). Whoever owns the source array, its release
drops the element, so the store is a second owner. The stage-2 compiler built
from the fixed self-host compiles the LICM fixture byte-identically to
`bin/fern-selfhost` again.

Pinned by `TestSelfHostAppendIndexedElem{X86_64,WasmIR}`: the digits read back
after the source array died, one row per append form plus a borrowed-parameter
source, the nested-array kind, and the string and enum controls.

## The residual — the destination's element walk

With the retain the element has two owners and only one of them releases it:
`out`'s element walk is credited only to a FRESH struct-array local with no
element alias (`arrstruct_credit_rows`: `collect_fresh_arrstruct_names`,
`arrarr_row_escapes`), and an index read from another array is exactly an
element alias. So `out` is released shallow and its three elements stay live.
Measured under `FERN_LEAKCHECK=1` on the pinned rows, `pre` dying at the end of
a one-trip loop body in main and `out` declared outside it:

| row | allocs / frees | live |
|---|---|---|
| struct, statement form | 71 / 68 | 144 (3 × 48) |
| struct, expression position | 73 / 70 | 144 |
| struct, clone form | 80 / 77 | 144 |
| struct, borrowed-parameter source | 72 / 69 | 144 |
| `i32[][]` | 71 / 68 | 120 |
| enum (control, unchanged) | 71 / 68 | 120 |
| string (control, unchanged) | 71 / 68 | 96 |

The two controls leak the same three boxes without any change here, so the
gap is the credit, not the retain: before the fix every box was freed, and the
struct and array ones under a live pointer. The test reads
the counters only to prove the path allocates; balancing them means admitting
an index-sourced element into the destination's walk, which is the reclaim
side of goal 2 and not this change.
