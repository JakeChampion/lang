# The read lends the loop's scratch

2026-10-06: `std/serve.__serve_read`, `__serve_compact`, `__Conns.lent`.
Slice 9 of `docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853), the read's
copy.

## The shape

```fern
let n: i32 = tcp_recv_into(c.fds[at], scratch);
...
if (buf.len() == 0) {
  buf = __bytes_range(scratch, 0, n);
}
```

The loop read every connection through one 4 KiB scratch and copied the
bytes out into the connection's buffer, a byte at a time: one allocation
and the copy per request, for a request that was parsed and answered
within the same event and whose buffer was the shared empty array again by
the event's end.

## The rule

A read into an empty buffer lends the scratch to the connection for the
event. The scratch is `__alloc_u8(4096)`; before each read it is set back
to its room and after the read shortened to the bytes read, both with the
bytes floor's `__arr_set_len`, which writes the length slot of the box, so
the parser reading `c.bufs[at]` sees the bytes and no more. `__Conns.lent`
names the connection holding the scratch, and `__serve_compact`, which
every event runs once it is done with the connection, copies out what the
connection still holds unanswered and hands the scratch back: the shared
empty array when the event answered everything it read, a copy of a
partial or pipelined request otherwise, the copy the loop made on every
read before. A readiness event on a connection whose handler is parked
compacts at once, since its read is the whole event.

The loop stops reading at a short read rather than reading again for
`-EAGAIN`: the wait is level-triggered, so bytes a short read leaves are
reported by the next wait, and the probe's syscall per request goes with
the copy. A second read in one event, which only a request of the
scratch's size or more makes, takes the scratch back first, copying its
bytes out, so no read writes into bytes a connection holds.

The interpreter had no `__arr_set_len`; it gains one that sets the length
of the array the binding names, within the room of its allocation, which
is what the loop asks of it. The Go checker's note on the builtin said
"shorten"; it sets the length within the capacity.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 4 |
| The read lends the scratch | 3 |

## What is left on the path

The parse's three: the `Framed` box the loop keeps whole, `__request_head`'s
head record, and the `Length(n)` framing it holds.
