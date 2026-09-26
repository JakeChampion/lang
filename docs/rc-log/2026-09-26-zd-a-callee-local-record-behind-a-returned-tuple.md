# A callee-local record behind a returned tuple is swept

In the AST lowering (`FERN_SEM_IR=`), a record built in the callee and returned
through a tuple of its fields was never released (#10315):

```fern
function mk(k: i32): (i32, i32[]) {
    var r: Rec = Rec { n: k, ys: [k, 1] };
    return (r.n, r.ys);
}
```

The record's box, the field buffer and any element strings all leaked. The leak
was 88 bytes for an `i32[]` field, and 120 for a `string[]` field whose element
came from `to_string()`. `return (r.n, r.ys.len())` and `return r.ys` were
clean.

`r` held its credit, but the return sweep kept it. `returned_moved_arr_slots`
recursed into each tuple element, and its field arm (#4801) keeps a struct from
the sweep when a pointer-shaped field is returned. That was right while the
field was an uncounted alias of the struct's buffer.

The tuple literal now retains an array field read
(`2026-09-26-zb-a-tuple-literal-counts-the-field-it-holds.md`). The returned
tuple therefore holds its own count, and the element no longer needs its struct
kept alive. The tuple arm skips such an element, so `r`'s sweep decrements the
buffer to the tuple's single count.

The keep still stands for a field returned bare (`return r.node`), which takes
no retain.

## Measured

`TestSelfHostTupleFieldShare` gained two rows, `callee_local_ints` and
`callee_local_strarr`. Each is bound once and discarded once, with a same-size
allocation between the call and the read.

They are census-balanced under every lowering, including the callee-skipped
mix, on x86-64 (the sanitizer too), arm64, wasm and native. Main leaks 192 and
240 bytes on them.

The gate reaches every array field kind, not just the two above. An
array-of-structs and an array-of-enums field in the same shape now answer
correctly with no sanitizer finding, and they leak less than main. They still
leak, though. The record's gated element walk declines while the tuple holds
the buffer, and the tuple's `a` release is one buffer dec, so the elements are
left behind. With a semantic callee, the `ARRF:` flag for such a position is
`0`, and even the buffer stays. That is #10326.

`callee_local_structarr` and `callee_local_enumarr` pin the counts (allocs /
frees) on every leg that leaks:

| lowering | `Pt[]` | `Flag[]` |
|---|---|---|
| semantic | balanced | balanced |
| AST, and AST callee | 30 / 22 | 28 / 22 |
| AST main, semantic callee | 30 / 20 | 28 / 20 |

A nested `i32[][]` field does not lower on the AST path at all (#10318).

The native leg surfaced a native bug in the same rows (#10325). `t.1[0].x`, a field
read through an index into a tuple element, failed with `ir: field access on
unresolved struct ""`, because `exprStaticType` had no arm for a numeric
selector. It now resolves one through `targetTupleType`, with two
`TestTupleElementFieldAccessLowers` rows.
