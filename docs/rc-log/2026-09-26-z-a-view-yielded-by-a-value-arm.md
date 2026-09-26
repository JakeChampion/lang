# A view yielded by a value block's arm is a fresh view

`semsource.unaliased_view` made a view bound under a second name, by
declaration or assignment, a fresh view of the same bytes
(`2026-09-25-v-a-view-under-a-second-name.md`). An if-expression or
match-expression arm that yields a live view binds it under a second name
too, the result's, but the arm's value reached the join's phi as the
source's own value:

```fern
var b: str = if (k > 1) { a } else { slice_unchecked(t, 1, 2) };
while (n < k) { a = slice_unchecked(t, n, n + 1); n = n + 1; }
```

The loop's phi for `a` then merged a view `b` still held, and supplying it
retained a view. The typed path refused `pick` ("a view is lent, never
retained"), and `main` with it: 0 of 2 produced. The AST lowering that
stood leaked 1776 bytes on the if-expression and 1608 on the match.

`arm_value` now passes an arm's value through `unaliased_view`, which covers
both expression forms and a block's trailing `return`.

A `for` over a `str[]` whose loop variable rebinds an outer view was
already produced whole; a row now pins it.

## Tests

`TestSelfHostSemanticProduction`, with no leak on x86-64 (with the sanitizer
too), arm64 and wasm:

- `a-view-yielded-by-an-if-arm-outlives-its-rebound-source`: 2 of 2 produced.
- `a-view-yielded-by-a-match-arm-outlives-its-rebound-source`: 2 of 2.
- `a-for-over-views-rebinds-an-outer-view`: 2 of 2, the pin.
