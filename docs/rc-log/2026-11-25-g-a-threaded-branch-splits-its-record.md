# A threaded branch splits its record

2026-11-25. Each target. The change is in `semsource.prune_dead_constructions`
and `seminline`. Refs #14440.

## The shape

`server.__read` reads each TLS record in a loop:

```fern
let next = __record(session, buf[at:end], now)?;
session = next.session;
output = __then(output, next.output);
```

`__record` and `__signed` are spliced into `__read`. `__signed` builds a
`Step`, and the loop reads its fields at once. So the `Step` needs no box.
But each request boxed it.

## The cause

The `?` merges the `Ok` record and the filler of each `Err` arm in one phi.
A second phi holds the flag, and a branch on the flag goes to the loop body
or to the `Err` return. The `Err` return gives the phi of the record to
`__pair_set` whole. So `split_constructions` marks the phi as used whole, and
it does not read the fields of the record off its construction.

`semthread.thread` then threads the branch on the flag. The loop body reads
the fields off the `record_new` itself, and the `Err` return reads a phi of
fillers. But no split ran after the thread, so the box stayed.

## The change

`prune_dead_constructions` runs `seminline.split_reads` after a thread that
changed the graph. The optbisect name of the step is `split-threaded`.

## Measured

Allocations a request, x86-64:

| Probe | main `cf6906ff` | the change |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequestTLS` | 7.00 | 6.00 |
| `TestSelfHostServeAllocsPerRequestH3` | 46.69 | 46.69 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 26.64 | 26.64 |

In the TLS census, `server.__read` goes from 2.00 to 1.00. Its last box is
the `Step` that `__read` returns. `ServeAllocsTLSCeiling` goes from 7 to 6.

`TestSelfHostThreadedBranchSplitsItsRecord` holds the change. Over 1,000
calls it makes 6,000 allocations on main and 5,000 with the change.

## The traps

- The two boxes of `server.__read` are 56 bytes. With an 8-byte header,
  that is the six fields of `record.Protection`. But the header is 16
  bytes, so the box has five fields: the `Step`.

- `FERN_DEBUG_SEM_DUMP` prints the graph twice. The `FUNC` form is the end
  of the inliner, before the thread. The `FERN_SEM_DUMP:` form is the input
  of the plan, after the thread. Here the reads of the `Step` read a phi in
  the first form and the `record_new` in the second.

## Next lead

The TLS census at 6.00 has six sites at 1.00 each. Two of them are on the
read path from `serve.__tls_taken`: the `Step` that `server.__read` returns,
and a box of three fields in `handshake.__server_run`.
