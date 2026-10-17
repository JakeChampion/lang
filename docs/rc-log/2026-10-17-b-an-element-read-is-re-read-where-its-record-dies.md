# 2026-10-17 — an element read is re-read where its record dies

`semsink.projection`, `semsink.read_above_for`, `semcse.merge`,
`ssasem.twin_arity`.

## The shape

```fern
let c: Conn = out.conn;
let s: Stream = c.streams[at];
if (!s.owes_end) {
  return out;
}
...
let sent: Stream = Stream { ...s, pending: __rest(s.pending, n) };
out = Run { ...out, conn: Conn { ...c, streams: c.streams.with(at, sent) } };
```

`std/http/h2` keeps a connection's streams in an array inside the
connection record, and every step that touches a stream reads it like this:
the element in one block, the update in a later one. The update's block
reads `c.streams` a second time, so the array the `with` consumes is a
different value from the one the element was read from. The element's chain
runs back through the first reads, to the record's slot the update replaces,
so the plan could take none of them: the record, the array and the element
were each copied, and the old ones released field by field: in the
allocation profile, two copies of the 24-field connection record a request,
two of the stream array and two of the stream record.

## The rule

- `semsink` re-reads an array element as it re-reads a field: where the
  record holding the array dies, from the record's own re-read, at the same
  index. The index is defined above the read it repeats, so it is in hand
  there.
- A re-read through a chain of parents is refused when a parent is spent
  (handed to a call, say) while the value the block reads through the chain
  is still to be used. The check had looked only at the parent's direct
  child, which an element re-read showed to be wrong: `iterate_array` read
  `s.values[it]` after handing `s` to `map_column`, and re-read through `s`
  the certifier found the frame releasing a box it did not hold.
- `semcse` merges a block's repeated reads of one slot of one value, the
  re-reads at a block's head and the block's own reads below them, so the
  array the update consumes is the one the element came from and the plan
  holds the element where the array goes.
- `ssasem`'s guard check counts two reads of one array at one index as one
  value, as it does a field, so a variant test of the first settles a
  payload read of the re-read.

## Measured

`TestSelfHostElementRereadReuse` (the h2 shape, 1,000 rounds): 12,000
allocations before, 0 after, on x86-64, arm64 and wasm.

In-memory driver of a server `h2.Conn`, one HEADERS frame read and one hello
response per request: 55 allocations a request before, 49 after; the
connection record, the stream array and the stream record are each built in
the box they replace.

## Witnessed

`TestSelfHostElementRereadReuse` runs the shape plain (allocation count) and
under the sanitizer (balanced leak census) on all three targets. The compiler
built with the change compiles itself, and that build compiles itself to the
same bytes.
