# A slice read in place keeps its box in the frame

Part of #8920.

## What changed

`slice_unchecked(s, a, b)` builds a 24-byte view box over the source's bytes.
The semantic lowering (`ssarc`) always put that box on the heap, and the frame
released it again through `__fern_str_view_free`. The AST lowering has had a
frame form since #6713: `op_str_slice_frame` builds the box in three reserved
frame words, and both native SSA emitters already honour it.

`ssaunits.plan` now marks a slice result `frame_views` when every use reads the
box within one instruction:

- a binary operator (`==`, `<`, `+`, …);
- a length or a byte read;
- a further window of it;
- a `str_as` retag handed to a callee that borrows and does not keep it. Only
  `semsource.lend` makes that retag.

A phi, a store, a return, a variant payload or any other use disqualifies it.
The phi rule is also what makes one set of words per site sound in a loop: an
earlier iteration's box can only be read again through a phi.

`ssarc.instruction` emits the frame form for those. `place_views` then gives
each one three slots above every other local, since the scratch height is only
known once the body is lowered. The frame still releases the view. On the
register backends `__fern_str_view_free` skips a box outside the heap, and on
wasm, whose slice copies into a fresh block, the release is what frees it.

## Measured

`TestSelfHostFrameViews`, 100 rounds per probe, heap allocations:

| probe | before | after (arm64, x86-64) | wasm |
|---|---|---|---|
| `slice(row, 0, n) == head` over 3 rows | 300 | 0 | 300 |
| `slice(s, 2, n) + ""` | 200 | 100 | 200 |
| length, byte, window of a window, lent to a borrowing callee | 400 | 0 | 400 |
| `s[0:2]` matched as `Some(v)` (control) | 200 | 200 | 200 |

The self-host compiler built by itself, compiling `coreutils/tsort.fern`
(callgrind, x86-64, same source for both builds):

| | before | after |
|---|---|---|
| instructions | 4,131,985,923 | 4,090,982,616 (−1.0%) |
| stage 2 linked bytes (`-g`) | 13,124,056 | 13,111,352 |

Stage 2 → stage 3 is byte-identical with the change.

## Still on the heap

A view that leaves its instruction: bound to a name read across a loop back
edge, returned, wrapped in an option, or passed to a callee that may keep it.
The `+ ""` copy idiom the compiler sources use everywhere still allocates the
copy itself; only the view box under it is gone.
