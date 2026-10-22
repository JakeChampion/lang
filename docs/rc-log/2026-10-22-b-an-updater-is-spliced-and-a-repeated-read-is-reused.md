# 2026-10-22 — an updater is spliced, and a repeated read is reused

`seminline.updater`, `semsink.reuse`, `semsink.child_used_above`,
`semcse.replaced`, `semdead.dead_cycles`. Refs #12887.

## The shape

`std/http/h3.__on_request` reads a request out of the session's list,
joins bytes onto its buffer, and puts it back:

```
let r: Req = cur.reqs[i];
r = Req { ...r, frames: Frames { ...r.frames, buf: r.frames.buf.concat(__copy(bytes)) }, fin: r.fin || fin };
return cur.__put_req(i, r).__request_frames(id);
```

`__put_req` is `Session { ...s, reqs: s.reqs.with(i, r) }`. The previous
entry let an element's take through an update that keeps its array. Three
things still stopped it here.

- **The store-back was a call.** `__put_req` has ten callers. It is not a
  leaf, since it builds a record, and it is not called once. The session
  went whole into the call, so the caller did not take the list out of it.
  The callee reads the list.

- **The callee read the list again.** Spliced, `__put_req` reads
  `s.reqs` in the block after `r.fin || fin`. That is a second read of the
  slot the first read must empty.

- **`semsink` read the element again** in that block, where the session
  dies. The join grows the buffer of the first read, so the plan must take
  the first read.

## What changed

- **`seminline.updater`.** An updater rebuilds a record parameter in one
  block. It reads the fields and updates arrays among them with `with` or
  `append`. The leaf inliner splices an updater into every caller. The
  caller then plans the element read, the element store and the update that
  keeps the array together.

- **`semsink.reuse`.** A counted projection can repeat a read of the same
  parent and slot in a block that dominates it. The projection is then that
  read when a projection off the first read is spent in a dominator. The
  plan must take the first read out of its box, so a second read finds the
  slot empty. The reuse runs before the re-reads. It runs only where a value
  has two reads of the same slot in different blocks.

- **`semsink.child_used_above`.** semsink refuses a re-read where a
  projection off the first read is updated in place (`append`, `with`,
  `concat`) in a dominator. The reuse occurs for any use. That predicate
  for the re-read cost ten boxes a request elsewhere in the serve loop.

- **`semcse.replaced`.** The rewrite that drops a merged read now renames a
  record update's donor too, which `imm` holds. Before, it renamed only the
  operands. An update whose donor was the dropped read then named a value
  that nothing defines. No program showed it, but the reuse merges reads
  that are donors.

- **`semdead.dead_cycles`.** The splice puts an updater's record update in
  the caller's loop. The leaf inliner splits the record into one phi a
  field, but the update stays, read only by the record's own phi. Each
  round then built a record that nothing read. A cycle of phis and
  constructions that read only one another is now unread, and semdead drops
  it. A value whose type has a finalizer stays, so each drop still runs.

## Measured

| Row | Before | After |
|---|---|---|
| `TestSelfHostServeAllocsPerRequestH3` | 224.92 | 200.92 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 130.54 | 112.91 |

The splice alone gives 210.92 and 116.20. The reuse gives the rest, mostly
in the other callers of `__put_req`. A fresh stream's frames and buffer are
static constants, and a constant cannot grow in place. So the hello row does
not show the take in `__on_request`. The handler takes the whole chain on a
stream whose buffer is its own.

`TestSelfHostPayloadTakeIR` gains two cases, under `FERN_LEAKCHECK`:

| Case | Main | Now |
|---|---|---|
| `element-take-across-a-branch`, 200 updates | 403 | 12 |
| `element-take-through-a-reused-donor`, 100 updates | 203 | 11 |

`bench/record_update` threads a record through `Blk { ...b, buf:
b.buf.with(i, v) }` in a method. Its `.ir` row on x86-64 reads 37,759,081,
from 109,849,111, the same count as `array_with`: the record costs nothing
a byte. Without `dead_cycles` it read 195,075,845.

`TestSelfHostDeadConstruction` gains `loop-carried-unread`, a record two
loops carry and read field by field (28 boxes while the cycle stood, 2 now),
and `loop-carried-finalizer-kept`, its twin with a finalizer.

The splice has a size cost. It copies an updater's field reads into each
caller, and the compiler has many updaters with many callers. The
self-built compiler (`fern.fern/stage2`) links at 13,913,088 bytes, 5.4%
over the stage0-built one, against 0.3% at the previous pin
(`.github/selfhost-driver-sizes.txt`).

`TestSelfHostInlineUpdater` pins the splice: an updater called from three
places is spliced into each, and the same body marked `@noinline` stays a
call.

## Next

The hello row's remaining boxes have no census in this entry. A census of
the serve loop by allocation site names the next slice.
