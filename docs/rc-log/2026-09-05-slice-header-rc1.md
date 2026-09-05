# 2026-09-05 — a `[T]` slice header is an rc1 block the IR owns (#8406)

All four backends plus the IR's rc analysis. Part of the coreutils work
(#8278): `std/crypto`'s `update()` calls `__*_absorb(h, chunk.as_bytes())`,
and every call stranded a view header.

## The shape, measured

`FERN_RC_TRACE` on the digest fixture, before:

```
153 x 0x10  __fern_slice_make <- main     (bytes() inlined into main)
```

16 bytes per call, unbounded in a loop. `__slice_make` allocated its
`(data, len)` header from a bare `__fern_alloc` — `rcResultRaw`, no rc
header — so a slice was a value nothing could ever dec. `std/string.bytes`
pays one per copy (`__memcpy(out, s.as_bytes(), n)`), as does every
`as_bytes()` in a loop.

## What changed

The header now comes from `__fern_alloc_rc1` (rc=1 at data-8, payload size
at data-4) on x86-64, arm64, arm64ssa and wasm, and `ast.SliceType` joins
the rc-tracked value types: exit sweep, arg-temp reclaim, alias retain,
reinit, reassignment, closure captures, container-child drops.
`__slice_make` and `__method_string_as_bytes` move from `rcResultRaw` to
`rcResultOwned`.

No new runtime shape. `__fern_closure_drop` already means "free the rc1
block at the size stashed at data-4 when rc == 1, else `__fern_rc_dec`",
null / low-address / sentinel guarded — that IS the header's drop, so it is
reused rather than a `__fern_slice_drop` invented. It frees the 16-byte
header (8 on wasm32) and never the bytes viewed.

Four temp consumers had no binding to reclaim them: `slice as usize`,
`s.as_bytes()[i]`, the parent of `s.as_bytes()[a:b]`, and `[T][]` elements
via a new `__drop_arr_slice` walk — a flat `__fern_rc_dec` there takes a
header to rc 0 without freeing it.

## The trap: a slice param de-credited its siblings

The header change alone did NOT fix the digest fixture; `audit_std_crypto`
stayed at 6. `inferParamCountedRetain` had no `[T]` arm, so a slice
parameter went uncredited the moment slices became rc-tracked — and through
`ptrAllCounted` that de-credited *every other* parameter of the same
function, refusing the caller's temp release at `__md5_absorb(h, ...)` and
its four siblings. Worth recording because the null result was the
misleading part: the backends were right and the census still read 6.

## Witnessed, not contract-only

Sub-slice lifetime is the one that needed proving rather than asserting: a
child views the SOURCE's storage, never the parent header, so freeing the
parent first is safe. Pinned by `slice_sub_view_outlives_parent_header`,
which reassigns the parent while the child is live and returns a child from
a callee whose parent is swept at exit.

## Numbers

Census rows to 0: `slice_views` 6, `slice_at_length` 3,
`alloc_flat_bytes_roundtrip` 204, `bytes_writer` 1, `stream_reader` 2,
`generic_array_methods` 1, `base64_roundtrip` 2, `hex_roundtrip` 2, and
`audit_std_crypto` back to its pinned 0. `open_slice_evaluates_base_once`
6 to 4. Corpus totals 485 fixtures / 412 clean / 73 leaking / 7518 unpaired.

An `as_bytes` / `bytes()` 3M-iteration loop is 1.03-1.09x FASTER after the
change — the headers recycle through the freelist instead of extending the
arena, so system time drops to about zero. An rc1 header is one extra store.

## Still open

- An `as_bytes` on an INLINE-packed string promotes the bytes into an
  unowned copy. The header cannot own it: a sub-slice or a raw `as usize`
  pointer may outlive the header, since a view's lifetime is its source's.
  Documented in all four backend helpers.
- A slice stored as a Map VALUE still strands its header. `mapValKindTag`
  gives any non-string, non-array pointer kind 1, `mapSetValueCounted` only
  counts kind >= 2, and `escapeMapEntry` taints the source. Leak-preserving,
  not a regression, and the same unreclaimed-column gap tuples and generic
  enums sit in — #8354.
- A closure that CAPTURES a slice still leaks its env/pair, identically with
  and without this change: the pre-existing closure-capture gap the
  `closure_*_churn_free` rows pin, not a slice issue.
