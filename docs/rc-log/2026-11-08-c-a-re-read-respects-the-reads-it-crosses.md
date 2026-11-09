# 2026-11-08 — a re-read respects the reads it crosses

`semsink.read_above`, `semsink.read_above_for`, `semsink.ancestor_spent_past`
and `semsink.child_kept_across`. Fixes #14739, found when ECN was added to
the HTTP/3 server (#12516).

## The shape

`std/serve.__h3_flush` with ECN, reduced:

```fern
let built = Holder { ...c, quic: Quic { ...qq, conns: qq.conns.with(i, qq.conns[i].transmit()) } };
let b = built.quic;
let flight = b.conns[i].datagrams();
let mark = b.conns[i].codepoint();
return Holder { ...built, quic: Quic { ...b, conns: b.conns.with(i, b.conns[i].sent()), out: added(b.out, b.paths[i], flight, mark) } };
```

`codepoint` calls a function that borrows the connection's QUIC state and is
not inlined. The call splits the block. Below it, the update stores the
connection back into `b.conns`, and the update copied the array on each
flush.

## Two causes

- **A re-read past an ancestor's spend.** `sink_past_lent` read `flight`
  again below the call, as a field of the QUIC state, and in a loop below
  the store-back. The QUIC state then had to live past the update of its
  grandparent, `b.conns`, so the plan held it, and the update copied the
  connection. `read_above_for` checked the spends of the parent's ancestors
  only when the parent died in the first read's block. Here the parent
  lived on into the borrowed call. Now `ancestor_spent_past` refuses the
  re-read when an ancestor is spent while the leaf lives and the parent does
  not.

- **A parent read again while its old child lives.** Below the call,
  `sink` (before inference, where every call spends) read `b` again for the
  update. It did not read `b.conns` again, because the call seemed to spend
  an element of it. So the array read from the old `b` lived into the block
  beside the new `b`. `twin_holds` then held the array across the new read
  of its slot, and the `with` on it copied. Now `child_kept_across` refuses
  to read a value again while a read off its first read lives into the
  block and is not read again there.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostRereadPastAncestorSpend`, 100 flushes | 300 | 200 |
| `TestSelfHostRereadKeepsTheChildRead`, 100 flushes | 400 | 100 |
| `TestSelfHostServeAllocsPerRequestH3` and `H3Mux` | 50.69, 28.82 | 50.69, 28.82 |
| ECN branch, hello server, 300 sequential requests | 16,101 | 15,201 |

The ECN branch measures the same as main without ECN, 15,201, so ECN costs
no allocation once this lands.

## Traps

The first cause alone fixed the reduction in #14739 and not the server. The
server's graph had no re-read from `sink_past_lent`, so the dumps before and
after it were the same. `FERN_SEM_DUMP` with a pass switched off at a time
found the second cause: the split read came from `sink`, before inference.

`hold_at` did not make the hold. `twin_holds` did, and it runs after
`held_elements`, so a debug print in `hold_at` alone shows nothing.

`ancestor_spent_past` alone took `TestSelfHostServeAllocsPerRequestH3Mux`
to 28.70. With `child_kept_across` it is 28.82 again: the second rule keeps
out a re-read that saved that box.
