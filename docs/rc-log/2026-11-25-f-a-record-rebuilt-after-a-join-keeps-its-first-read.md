# A record rebuilt after a join keeps its first read

2026-11-25. Each target. The change is in `semsink`. Refs #14440.

## The shape

```fern
function (p: Prot) seal_framed(own buf: u8[]): Result[Sealed, i32] {
  if (buf.len() < 3) { return Err(5); }
  return Ok(Sealed { record: seal(p, buf)?, next: advanced(p) });
}

@noinline
function write_framed(own c: Conn, own buf: u8[]): Result[Wrote, i32] {
  if (c.quic) { return Err(9); }
  match (c.write.seal_framed(buf)) {
    Ok(s) => { return Ok(Wrote { conn: Conn { ...c, write: s.next }, bytes: s.record }); },
    Err(e) => { return Err(e); }
  }
  return Err(8);
}
```

This is `handshake.__write_framed` with `record.Protection.seal_framed`
spliced. `seal` is `record.__seal`: a match on the suite lends `p` to
`seal_in_place` in each arm, and an error arm reads `p.key`. `advanced` is
`record.__advanced`: `Prot { ...p, seq: p.seq + 1 }`.
`TestSelfHostRebuiltAfterJoinTakesTheRead` holds the program.

## The cause

The read of `c.write` (`v5`) is in a block before the match. The rebuild of
`Prot` from `v5` is in the block after the join of the arms.

`sink_rereads` read `c.write` again in the error arm, for `p.key.len()`.
That is a second read of the `write` slot of `c`. `ssaunits.named_after`
then saw a read of the slot past the block of `v5`, so the plan did not take
`v5` out of `c`. `v5` stayed a borrow. The rebuild retained each field and
made a new `Prot` box on each call.

## The change

`semsink.read_above` does not read a slot again in a block when another
block updates the record that the slot first gives
(`updated_elsewhere`). The update block can then take the first read. An
array keeps its re-reads, because each `with` stores back into the array
that its own block reads. Without that limit, the HTTP/3 probe went from
46.53 to 49.41 allocations a request: `streams.Streams.on_stream` updates
`list` in two arms.

## Measured

The program of the test, x86-64, 1000 rounds: 2000 allocations before,
1000 after. As a call, `seal_framed` gives 1000.

The censuses, x86-64, allocations a request:

| Census | main `90224f69` | after |
| --- | --: | --: |
| TLS, keep-alive | 11.00 | 9.00 |
| HTTP/3, serial | 46.53 | 46.53 |
| HTTP/3, multiplexed | 25.97 | 25.97 |
| two fetches | 61.00 | 61.00 |

`ServeAllocsTLSCeiling` goes from 11 to 9.

On main, `h3q.Conn.respond` is not spliced, because its call lends a view.
A local patch spliced it. The HTTP/3 census stayed at 46.53. Thus the cost
of 10.75 that #14440 measured for that splice is gone.

## Next lead

The TLS census at 9.00 against 8.00 before the splice of each method. The
last 1.00 comes only with other splices
(`2026-11-25-e-a-tls-census-names-the-next-splice-cost.md`).
