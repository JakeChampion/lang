# A view bound under a second name is a fresh view

#9802 made the planner refuse any plan that retains a view. A view's box
carries the immortal sentinel rather than a count, so a retain is a no-op
while the release that balances it frees the box. The shape that reached it
from source was four lines:

```fern
var s: str = slice_unchecked(t, 0, 5);
var v: str = s;
while (i < 3) { v = slice_unchecked(t, 5, 10); i = i + 1; }
return v.len() + s.len() + u.len();
```

`var v: str = s` bound `v` to `s`'s own value. The loop's phi for `v`
merges that value with a fresh view and is owned, and `s` stays live past
the loop, so the entry edge could supply the phi only by retaining `s`. The
typed path refused the function ("a view is lent, never retained"), and the
AST lowering answered 14 but leaked the boxes.

`semsource.unaliased_view` makes a view bound under a second name, by
declaration or assignment, a fresh view of the same bytes: a full-length
slice of it, anchored to the bytes the original reads. The two names share
no box, the phi's entry value is the slice and is taken by move, and each
box is released once. A string literal and a string retagged as a view are
counted boxes that are sound to retain, so they are bound as before.

The same holds when the loop starts from a borrowed parameter. A phi
merging the parameter with a fresh view is owned, so its entry edge
retained the parameter's view: `walk` in the new row below was refused the
same way, and `main` with it, 0 of 2 produced.

The cost is one view box per such binding. The compiler has none; the
stdlib has three, in `std/fetch` and `std/cli`, none in a loop.

`view_retain_error` stays: a construction or container write could still
ask to retain a view, and the planner refuses that.

## Tests

`TestSelfHostSemanticProduction`, on x86-64 (with the sanitizer too), arm64
and wasm, against the AST lowering:

- `view-loop-rebinds-a-live-view` produced the refusal before. It now
  produces 1 of 1 with no leak: 5 allocs, 5 frees.
- `view-loop-rebinds-a-view-parameter`, new: the loop over a borrowed
  parameter, 20 calls, 2 of 2 produced, 59 allocs and 59 frees.
