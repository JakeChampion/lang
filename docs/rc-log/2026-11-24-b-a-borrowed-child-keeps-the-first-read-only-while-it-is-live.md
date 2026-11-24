# A borrowed child keeps the first read only while it is live

The reuse step of the sink pass counted a call that borrows a child as a
spend of that child. So a later read of the parent's slot took the first
read again. Below a join, the plan then did not take the element. It made a
new record for it and copied its array (#14440).

## The shape

```
@noinline
function written(st: Stream): i64 {
  return st.base + st.buf.len() as i64;
}

function sent_of(st: Stream, n: i64, fin: boolean): Stream {
  return Stream { ...st, sent: st.sent + n, fin_sent: st.fin_sent || fin };
}

@noinline
function new_data(s: Streams, i: i32, room: i32): Streams {
  let sent = s.list[i].sent;
  let w = written(s.list[i]);
  ...
  let out = append_range(s.filled, s.list[i].buf, at, at + n as i32);
  return Streams { ...s, filled: out, list: s.list.with(i, sent_of(s.list[i], n, sent + n == w)) };
}
```

This is `Streams.__new_data` of `std/quic/streams` with `Stream.__sent`
spliced. The entry block reads `s.list[i]` and lends it to `written`. The
update block reads `s.list` again. The spliced `||` puts a join between the
element read and the `with`.

## The cause

`semsink.reuse` can replace a later read with an earlier read of the same
slot. It does so when a child of the earlier read is spent above the later
read's block (`child_used_above`). The early sink pass ran with `lent` off,
so each call argument was a spend. The element that `written` only borrows
was thus a spent child.

So `reuse` replaced the update block's read of `s.list` with the entry
block's read. The update block then had no element read of its own. The
plan built a new `Stream` and copied `list` with `arr_slice`.

Without the call to `written`, the update block keeps its own read of `s.list`.
The plan then takes the element across the join and reuses its box. So the
plan can take across this join. The read that `reuse` merged away is what
stopped it.

## The fix

`reuse` computes its reads with `lent` on. A child that a call only borrows
keeps the first read only when `child_live_out` holds as well: a descendant
of the first read is live out of the later read's block. A real spend keeps
the first read as before. `sink_rereads` gets the same reads as before, with
each argument spent.

`TestSelfHostRereadPastAncestorSpend` (#14739) needs the second condition.
There a child of the element is lent to `Q.__marking`. The flight, a
descendant of the first read, is live past the update. With `lent` alone,
that test made 200 allocations, not 100.

## Measurements

`__heap_alloc_count()`, x86-64:

| Program | main `5c97bfd2` | fix | branch-free `fin_sent: fin` |
|---|--:|--:|--:|
| the shape above, 1,000 rounds (`TestSelfHostLentThenTakeAcrossJoin`) | 2,550 | 850 | 850 |
| the full `__new_data` reduction, 1,000 rounds of `data` | 6,000 | 4,000 | 4,000 |
| `TestSelfHostRereadPastAncestorSpend` | 100 | 100 | |
| the counter-case of #14440 | 1,024 | 1,024 | |

`TestSelfHostLentThenTakeAcrossJoin` exits 2 on main on x86-64 and arm64.

## A trap

A free function whose name starts with `__` is not spliced unless the module
reaches it directly (`semsource.outside_unnamed`). A reduction that renames
`__written` to `written` splices it, the call goes away, and the cost goes
with it. Keep the helper a call with `@noinline`.

## The next lead

`semsource.spliceable_body` still refuses a method of more than one block.
Its comment says that the plan takes an element only within the block that
reads it. In the shape above, the plan takes an element across a join when
the update block reads the array again. Measure the HTTP/3 probes with each
method spliced, on top of this fix. The result tells if that gate can open
(#13990).
