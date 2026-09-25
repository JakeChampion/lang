# A view result with no one source is a copy

A function whose result holds a view is anchored to the one parameter those
views read (`ssasem.result_anchor`), and its caller keeps that argument alive
while the result lives. A result reading either of two parameters has no one
argument to keep alive, so `anchor_module` refused it, and its callers with it
("view result escapes its source"):

```fern
function either(a: string, b: string, first: boolean): str {
    if (first) { return a; }
    return b;
}
```

A plain `str` result with no one source now returns a copy of each view
(`semsource.copy_returned_views`): `return v` becomes
`return (v + "") as str`. That is a counted string retagged as a view,
which anchors nothing, so the function's anchor is "none". The view release
already handles a counted string: `__fern_str_view_free` releases it rather
than freeing a view's box. The copy reads the view before the function
releases anything, so it is sound whatever the view reads, a local
included.

`anchor_module` runs its fixpoint, copies every remaining plain-`str`
escape, and runs the fixpoint again. A caller that returns such a result,
like `through` below, settles on the second round instead of being refused
with the callee.

A result that holds views without being a plain `str` (an `Option[str]`, a
tuple or array of views) is still refused. Copying it would mean rebuilding
the container.

The copy is one allocation per call, and only in this shape.

## Tests

- `TestSelfHostSemanticProduction`, on x86-64 (with the sanitizer too), arm64
  and wasm:
  - `a-view-result-of-either-parameter-is-a-copy`, the row that pinned the
    refusal: 104 of 104 produced, 5 allocations and 5 frees.
  - `a-copied-view-result-passes-through-a-caller`, new: `through` returns
    `either`'s result, and `either` is handed that copy back, 40 rounds.
    59 of 59 produced, 354 and 354, answering 232.
- `TestSelfHostSemanticSourcePrint`: `copied_view_of_a_local` and
  `copied_view_of_either` are produced, and the golden prints the copy on
  each return. `refused_option_of_either` keeps the refusal visible.
