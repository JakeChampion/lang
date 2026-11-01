# 2026-11-01 — the first read stays when its element returns through a join

`semsink.read_above`, `child_grown_in`, `read_in`, `parent_spent_elsewhere`.
Refs #13990.

## The shape

```fern
function respond(c: Conn, id: i64, resp: Resp): Conn {
  match (resp.body) {
    Text(s) => { return c.__respond_with(id, s.as_bytes()); },
    _ => { return c.__respond_with(id, resp.body_bytes()); }
  }
}

return Conns { ...c, quic: Q { ...q, conns: q.conns.with(i, respond(q.conns[i].with_date(c.date), id, resp)) } };
```

`respond` is a private function with one caller, so the once set splices
it. Its `match` puts the two calls on two arms and the `with` after their
join. The element read, `q.conns[i]`, stays in the block before the branch.

## The rule

The inliner leaves one read of `q.conns` for the element read and the
`with`. `semsink.read_above` then reads `c.quic` and `q.conns` again in the
join block, because the parent `c` is spent there, at the update. The plan
sees two reads of each slot. `needed_past_slot` refuses the element take,
because the later read finds the slot empty after a take. The chain `with_date`,
`__respond_with`, `with` then copies the connection on each round.

The first read now stays when three things hold. A descendant of it was
spent in a dominator of the re-read's block, which here is the element
read that `with_date` consumed. A `with` on that array sits in the re-read's
block, and no element read of the array sits there. No block the first read
dominates spends the parent, except the re-read's own block. Keeping the
first read alive to that block therefore holds nothing.

The exception uses a separate index of `with` destinations. The ordinary
growth index also includes append, concat and truncate. Those operations
keep their existing re-read behavior. Using the growth index for this
exception increased compiler copies on the fixed checker workload, even
though the emitted checker assembly did not change.

Two wider forms were measured and rejected. The first kept the first read
for any descendant spent in a dominator. It cost 3 allocations a request on
the HTTP/3 server, in `h3q.Conn.__readable` and others. There the
descendant is a record field that a call consumed, and no `with` follows.

The second kept the first read when the array is read again in the re-read's
block. It cost 1 a request in `quic.Conn.__new_data` and `__on_ack`. There
the element the `with` stores is read in that block, and the re-read serves
that take.

## Measured

- `element-take-returns-through-a-spliced-join` in
  `TestSelfHostPayloadTakeIR`: 1,000 rounds allocate 1,027 boxes, against
  3,007 before, the same as with `respond` kept as a call.
- `TestSelfHostServeAllocsPerRequestH3Mux` stays at 28.8 a request, and the
  five coreutils rows of the entry 2026-11-01-a are flat.
- With every method admitted to the once set, the probe reads 33.3 a
  request against 42.3 before. The rest is spread over the connection and
  stream code, as the comment on #13990 records, and the one-block rule of
  2026-11-01-a stays.
