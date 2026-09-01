# The array overwrite releases at the wrong depth (and `.with` gains its element credit)

Two defects the concat-credit probe (`docs/rc-log/2026-09-01-concat-operand-param-credit.md`)
left behind, both in the `.with` path, both fixed here.

## 1. The overwrite dec was buffer-only everywhere

`a = <rhs>` on an array local released the old buffer with
`__fern_arr_dec`, which frees the buffer without walking its elements.
The comment justifying that says the push copy-grow "transferred the old
buffer's pointer elements to the new buffer (no inc)" — true of
`__fern_arr_push_grow_move_ptr`, and of nothing else. Every other RHS
leaves the old buffer OWNING its elements:

- `a = mk()` — an unrelated producer; the old value simply dies.
- `a = f(a)` through a callee whose `.with` cow'd — and
  `__fern_arr_cow_inplace_ptr` INCS each element into the copy, because
  otherwise both buffers would share elements under one count.

Measured over 8 rounds, x86-64 and arm64 alike:

| shape | before | after |
| --- | --- | --- |
| `a = mk()` | 256 | 0 |
| `a = f(a)` through a cowing callee | 512 | 0 |
| `a = a.with(i, v)` spelled inline | 0 | 0 |
| `a = a.append(v)` | 0 | 0 |

The two that were already clean are the two the lowering special-cases
(`isSelfArraySetReassign` emits no dec at all; the self-append keeps the
buffer-only one). Note `var a = mk()` re-executed in a LOOP already
reclaimed correctly — `emitVarReinitDropOld` routes to the deep
`emitOwnedSlotDrop` — so the two spellings of one thing disagreed.

## The identity guard, and the trap in it

A callee can hand the local's own buffer back: the cow's rc==1 in-place
path, a grow with spare capacity, an argument flowed through unchanged.
`selfReassignOwnedLocal` already names this as the reason `string[]` is
excluded from the general `a = f(a)` form — "the array branch's overwrite
`__fern_arr_dec` has no identity guard". So the deep drop goes behind a
pointer-changed test.

**The else arm is load-bearing and its absence is worse than the bug.**
The first draft simply skipped the release when the pointers matched.
That looks right — the buffer is live, do not release it — and it
regressed a clean case from 0 to 576 B, because the reference being
released there is not the old buffer's: it is the one the CALL added.
Skipping it left the buffer permanently one count above zero, so its
scope-exit deep drop saw rc 2, took the shared path, and never walked
its elements. The same-buffer arm keeps the shallow `__fern_arr_dec`.

## 2. No tier credited `__method_Array_set`'s element

`xs.with(i, p)` is `xs.append(p)`'s sibling — `emitArraySet` incs an
aliased pointer element and the buffer's deep drop gives it back — and
`computeFreeEligible` has read it that way since #4399 sink 2. None of
`stringParamCounted` / `arrayParamCounted` / `paramProjectionsSafe` had
the position, so a helper as small as `put(xs, v) -> xs.with(0, v)`
stranded its caller's fresh argument: 32 B a round. The receiver stays
uncredited on purpose — `.with` hands the receiver's own buffer back at
rc 1, which is not a retention the caller can discount.

## Measured together

Every probe from the concat-credit arc now reads 0 on x86-64, including
the two that arc left pinned: the `.with` registry corpus case 384 → 0
and the 10-round fixpoint probe 2,240 → 0. arm64 moved too (registry
3,072 → 2,688; fixpoint 5,120 → 2,880) without a single regression.

**Driver: 371,776 → 369,648 B (−2,128, +52 frees)**, output byte-identical,
traced and untraced builds agreeing to the byte on both sides.

> A driver measurement is only as good as the `bin/fern` that built it.
> After a rebase, `go build ./...` does NOT refresh `bin/fern`, so the
> next driver build pairs an OLD compiler with NEW self-host sources —
> which read 1,221,824 B here and looked like a 3x regression until the
> four-way A/B (both commits x traced/untraced) put every output at the
> same md5. Rebuild `bin/fern` explicitly before every driver run.
The Array_set credit alone moves the driver 0 bytes — the self-host's
`.with` element is always a fresh concat, never a parameter — so it is a
correctness and tier-parity fix, not a frontier one.

Banked: `forin_elem_escape_return_keeps_retain` (96 x86-64 / 112 arm64 →
0), and five regex conformance fixtures whose capture tables are built
with `.with`, taking the census total from 44,497 unpaired allocations to
42,248.
