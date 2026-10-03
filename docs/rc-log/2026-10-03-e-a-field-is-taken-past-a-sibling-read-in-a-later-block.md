# 2026-10-03 — a field is taken past a sibling read in a later block

`ssaunits.block_reads`. Refs #11204, #9854.

```fern
c = __Conns { ...c, tails: c.tails.with(at, wire.tail), keep: c.keep.with(at, c.keep[at] && wire.keep) };
```

std/tcp's `__serve_respond` copied `c.tails` on every request. `c` was
counted and unique at entry, but the planner took `c.keep` and RETAINED
`c.tails`, and the retained array failed the `with`'s uniqueness test by
its own count. `&&` splits the block, so `c.keep[at]` is read in a later
block than the read of `c.tails`. `needed_past` asks `named_after` whether a
later block names `c`. `block_reads` excused a read of another slot only
for a tuple take (#11203), so for a record field the read of `c.keep`
counted as naming `c`, and the take was refused.

## The rule

A later block's read of a DIFFERENT slot of the box no longer refuses a
take, for every kind of take. That is what `read_later_in_block` and
`reached_past_take` already allowed. A read of the taken slot itself still
counts, so a take a loop would run again on the same box is refused.

## Measured

`TestSelfHostSemanticAllocationCounts/a-field-is-taken-past-a-sibling-read-in-a-later-block`,
ten rounds of the shape above on a three-field record: 14 allocations
before, 4 after (the record's three arrays and its box).

`TestSelfHostHeldConnectionsHeapBoundX86_64`, bump bytes per held
connection over the second batch of 64:

| shape | before (#11220) | after |
| --- | --: | --: |
| accepted, never used | 568 | 560 |
| kept alive after one request | 2,483 | 584 |

The kept figure is #9854's exit criterion (under 1 KiB), so its leg
lands with this entry. The per-request copy was the whole of it. Each
`.with` copy left an array whose capacity equalled its length, so the next
accept's append reallocated that array at a size class it had never used,
and the bump grew by a table's worth per connection.

## Found with

`FERN_RC_TRACE=1` on the hello server under 40 kept connections, with
each allocation that raised the bump high-water mark attributed to its
site, plus gdb at `__serve_respond`'s entry. The gdb check showed the
table's box and all fifteen arrays at count 1, which ruled out sharing and
pointed at the plan.
