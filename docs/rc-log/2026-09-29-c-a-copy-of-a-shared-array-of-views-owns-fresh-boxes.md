# A copy of a shared array of views owns fresh boxes

On the register backends a string view is a 24-byte heap box with the immortal
rc -1, so no retain can count a second holder. `ssarc` un-shares a shared
`str[]` by copying its element pointers and retaining them over the receiver
(`__fern_arr_inc_elems`). For a view element that retain did nothing, so the two
arrays held one box between them, and each freed it on release
(`__fern_str_view_free`). This was #10726.

A minimal reproduction: `grow(xs, s)` appends to a lent `xs`, the caller then
releases `xs`, and the next 24-byte allocation reuses the box `ys[0]` still
held. Before the fix `ys[0]` printed `zz`, and the sanitizer reported a
use-after-free on x86-64. arm64 gave the same wrong answer. wasm was correct,
because a wasm slice copies its bytes into a counted block.

## Fix

Each copy site now calls `own_copied`: `ssarc.arr_slice`, `sole_owned_base`,
`append_push`, `borrowed_push`, `append_field` and the `with` copy. For an
element type that is not a view it is the old retain over the receiver. For
`str` it is `__fern_arr_own_elems(copy, source)`:

- a counted element is retained;
- an arena view box is replaced in the copy by a fresh box over the same bytes;
- a view box outside the arena, such as a literal, is never freed, so it is
  shared as is.

The copy gets the fresh boxes rather than the source, because a borrowed
`xs[i]` still points at the source's box. On wasm the helper is the element
retain over the source.

## Cost

- One 24-byte box per arena view element, each time a shared `str[]` is
  copied.
- A loop that appends to a lent array through a callee copies every element on
  each call, so it allocates O(n²) boxes. With #10724's verification it measures
  25 allocations for 5 elements, against 15 when the boxes were shared.

## Tests

`TestSelfHostSemanticProduction` on x86-64 (with the sanitizer too), arm64 and
wasm:

- `a-copied-array-of-views-owns-its-boxes`
- `every-copy-of-a-shared-array-of-views-owns-its-boxes`, which covers an
  append, a `with`, a window and a record field append, each with the source
  still live.

## Still open

A view map value read back would share its column's box the same way, which is
why it stays refused (#10701).
