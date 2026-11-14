# 2026-11-14 — an append loop is one range concat

All targets. `sembytecopy` (`concat_array`, `copy_kind`, `replace`),
`ssasem.arr_concat_error`, `ssaunits.sole_boxes`, `ssarc.concat_call`,
`concat_owned`, `concat_borrowed`, `concat_field`, `ircore.register_helper`, and
the concat kernels (`compiler/runtime/common/__fern_arr_concat.fern`,
`__fern_arr_concat_slow.fern`, `wasm_ir.arr_concat_helper`). Issue #15829.

## The shape

```fern
let i = lo;
while (i < hi) { a = a.append(b[i]); i += 1; }
```

`sembytecopy` took this loop only when `a` and `b` were owned `u8[]` values. A
view source, such as `b: [u8]`, and any other element type kept the loop. Each
pass of the loop did a bounds check, a capacity check and a store.

## What changed

The pass takes the append shape for any element type but a string view, and
for a view source. It writes `arr_concat` with four operands: the receiver,
the source, the start and the count. The count is `max(hi - lo, 0)`, as for
the byte helpers. An owned `u8[]` source keeps `__fern_bytes_append_range`.
The source must not be the receiver that the loop starts from.

The concat is in the body block of the old loop, and a phi in the exit joins
it with the receiver. The header keeps its test, so an empty range makes no
call. The pass now does this for the byte helpers too.

`ssarc.concat_call` checks a range with a count above zero against the
source, and calls `__fern_oob_abort` when the range is not in it. The kernel
gets the address of element `from` and the count. The native backends enter
it through its register entry, as they enter the byte helpers.

## Short ranges

A merge sort ends each merge with two such loops, and most of them copy one
or two elements. The first form of the change made `bench/sort_ints` 20%
slower in Ir and 7% slower in wall time. Three causes, and their fixes:

- The concat ran in the header, also for an empty range. It is now in the
  body.
- The kernel saved six registers and spilled its locals for each call. Its
  in-place copy into a unique receiver with room is now a small entry,
  `__fern_arr_concat`, which calls `__fern_arr_concat_slow` for a new box.
  It copies eight words or fewer with a loop. The two share
  `__fern_arr_copy_words` for longer copies and `__fern_arr_inc_words` for
  the counts.
- `ssaunits.sole_boxes` took the source operand of a concat as an escape.
  An array that a concat read was then not sole at its next update, so each
  later append and `with` tested its count. A concat now only reads its
  source (`TestSelfHostConcatSourceStaysSole`).

In the sort, the source is the input array, and the `return arr` path joins
it with the copy. Before the last fix, the copy thus lost its sole state
too, and the insertion sort copied it.

A range of two `i32` elements still costs about 148 Ir against 71 for the
loop. A range of five or more is cheaper.

A copy onto an empty array first made a box of exactly the copied length.
`std/pmap.node_insert_at` copies a prefix onto `[]`, appends one node, and
copies the suffix, so each append after the copy grew the box again. The
slow kernel now gives a new box from an empty array the room that appends
of the same elements give it: 4, then doubled. `bench/pmap_insert` makes
10,490 allocations, against 11,990 on main.

## Counted elements

The plan has no unit for an element in a box. The counts of the elements are
the business of the lowering and of the kernel:

- The kernel takes an odd width for counted elements. It increments the
  count of each element that it copies from the source.
- `release` of a box update's receiver frees only the box. When a unique
  receiver grows, its elements move into the new box, and nothing increments
  them.
- A shared receiver, a borrowed receiver whose copy is the result, and a
  record field whose copy is the result share their elements with the copy.
  The lowering then calls `__fern_arr_inc_elems` on the receiver
  (`own_copied`), as `append_push`, `borrowed_push` and `append_field` do.

The first form of the kernel incremented every element of a new box. That
leaked each element of a unique receiver that grew, because the shallow
release of the receiver did not decrement them. The leak census showed 5
blocks in a loop that grew a `string[]` three times.

The concat method of std/array now takes the kernel for counted elements
too.

## A trap on wasm

`memory.copy` checks its source address even when it copies no byte. An
empty range can start anywhere, so the address of element `from` can be out
of the memory. The wasm kernel sets its source to the receiver when the
count is zero or less.

## Measured

x86-64 container, the best of five runs of 200,000 joins (180 bytes) and
2,000 joins (64 KiB) of a `[u8]` view onto an empty `u8[]`.

| Join | Loop before | Loop after | `a.concat(b)` |
| --- | ---: | ---: | ---: |
| 180 bytes | 480 ns | 17 ns | 16 ns |
| 64 KiB | 72,702 ns | 2,317 ns | 2,314 ns |

Callgrind Ir and the best wall time of 15 interleaved runs of the bench
programs, main against this change:

| Program | Ir before | Ir after | Wall before | Wall after |
| --- | ---: | ---: | ---: | ---: |
| `sort_ints` | 23,053,987 | 22,272,594 (-3.4%) | 5,231 us | 5,094 us |
| `sort_strings` | 95,020,121 | 96,251,312 (+1.3%) | 11,858 us | 11,127 us |
| `pmap_insert` | 48,289,453 | 48,903,377 (+1.3%) | 6,513 us | 6,398 us |

`sort_strings` and `pmap_insert` run more instructions, because the kernel
increments the count of each element that it copies in a call. Both take
less time.

Stdlib helpers that now reach a kernel with no loop in their x86-64 code:
`client.__copy` (`__cat` is spliced into it), `ecdsa.__cat3`,
`ecdsa.__copy` and `qpack.__from` (a `string[]` loop). `ecdsa.__der_integer`
reaches the kernel for its copy, and keeps the loop that skips the leading
zero bytes.

## Not taken

A write loop from a view, `out = out.with(at + i, b[from + i])`, keeps its
loop. `std/tls/keyshare.__cat` and `std/tls/message.__copy` have that shape
(#15833).
