# 2026-09-20 — a function value may live in a tuple

`2026-09-19-a-function-value-may-live-in-an-array.md` left the tuple, Map
and Cell arms of `ssasem.nests_func` and said: "the tuple arm may be the same
one-line lift plus whatever `semsource` needs for `t.0(...)`. Measure it; do
not assume it is one line because this one was."

It is two: the arm, and the call.

## The shape

```fern
while (i < 200) {
    var xs: i32[] = [i, i + 1, i + 2];
    var p: ((i32) => i32, i32) = (((x: i32) => x + xs[0] + xs[2]), i);
    t = t + (p.0)(1) % 3 + p.1 % 2;
    i = i + 1;
}
```

| leg | before | after |
|---|---|---|
| semantic | refused: `unsupported call target: (((i32) => i32), i32).0` | produced 2 of 2, allocs=600 frees=600 live_bytes=0 |
| AST | answers, **leaks 16000 bytes in 400 blocks** | unchanged |

The refusal came before `nests_func` was ever asked: `p.0` is parsed as a
field access, `method_call` looked the receiver up as a record, and a tuple
has no schema to find a field in. A tuple ELEMENT of function type is the
record-field case one container over, so `method_call` gets that arm: read
the element (`tuple_get`, the way a record field is `record_get`) and call
through the value. Then the verifier's arm: `nests_func` admits a function
value as a tuple element, since `ssarc.drop_tuple_fields` already walks each
element through `drop_value` and that dispatches a function type to
`drop_captures`.

Which is also the reclaim result: the AST lowering leaks a closure held in
a tuple, every round, and the produced body does not. #9576's table has a
struct-field row for this leak; the tuple is the same family.

## The Cell arm was dead

`nests_func` also refused a function value in a Cell. The checker refuses a
Cell of any composite type (E057: "a cell's element type must be a scalar or
string"), so no Cell ever held one and the arm answered nothing. Deleted
rather than lifted.

## What is left

The Map arms, and a function value nested in an array or tuple of arrays or
tuples. The fuzz census is unchanged by this entry — 451 of 508 whole, 499
agree, 0 diverge — because every fernsmith program holding a lambda in a
tuple also holds one somewhere still refused. The compiler's own sources
produce 8615 of 8615.
