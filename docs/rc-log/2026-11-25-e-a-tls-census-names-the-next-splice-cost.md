# A TLS census names the next splice cost

2026-11-25. x86-64. The change is a test: `TestSelfHostServeAllocsCensusTLS`.

## The change

`TestSelfHostServeAllocsCensusTLS` writes the allocation census of the TLS
keep-alive server of `TestSelfHostServeAllocsPerRequestTLS`. It writes to the
file that `FERN_SERVE_ALLOC_CENSUS` names, with `.tls` after the name. Without
the variable, the test skips, as the H3 and two-fetch censuses do.

## Measured

The local patch admits each method with more than one block to the once set.
It removes the `spliceable_body` term in `semsource.inline_leaves` and in
`semsource.unnamed`. It is not committed.

Allocations a request, main `9632fa79`:

| Probe | main | each method spliced |
| --- | --: | --: |
| HTTP/1.1 | 0.00 | 0.00 |
| HTTP/2 | 20.00 | 20.00 |
| TLS | 8.00 | 11.00 |
| HTTP/3, serial | 49.69 | 47.69 |
| HTTP/3, multiplexed | 28.67 | 27.49 |
| two fetches | 57.00 | 57.00 |

The H3 rows agree with the last entry. The TLS row is the new cost. The TLS
server has 554 multi-block method names. A search over their hashes found one
name with a cost when spliced alone: `record.Protection.seal_framed` (8.00 to
10.00). The last 1.00 comes only with other splices.

The census of the TLS server with only `seal_framed` spliced:

| Site | Caller | main | spliced |
| --- | --- | --: | --: |
| `handshake__Server__write_framed` | `server__Session__write_framed` | 1.00 | 2.00 |
| `record____advanced` | `handshake__Server__write_framed` | 0.00 | 1.00 |

## The cause

As a call, `handshake.__write_framed` reads `c.write` and gives it to
`seal_framed` in one block. The plan takes the field there with a uniqueness
test on `c`. Thus `__advanced` gets a sole `Protection`, and its
`Protection { ...p, seq: ... }` uses the same box again.

With `seal_framed` spliced, `__seal` is spliced too. The read of `c.write`
(`record_get 6` of `c`) is in one block. The arms of the match on the suite
lend it to `seal_in_place`. `__advanced` spends it in the block after the join
of those arms.

The plan takes an element only in the block that reads it. Thus the read
stays a borrow, and the spend in the join block retains it. `__advanced` then
makes a new box, and the rebuild of `Conn` copies each field.

## The traps

- A re-read in the join block is not enough. A local patch to
  `semsink.read_above_for` let `sink_rereads` read `c.write` again in the
  block of `__advanced`. The patch accepts a parent that is live out of the
  block. The blocks after it must read only the other fields of the parent.
- The plan gave that re-read a retain and not the tested take. The count
  stayed at 10.00.

- `FERN_DEBUG_SEM_DUMP` matches the graph name of this stage, which has no
  module prefix: `write_framed`, not `handshake__Server.write_framed`. Four
  functions have that name in the TLS server. A dump that prints nothing is a
  wrong name, not a missing function.

## Next lead

The plan must take a field with its uniqueness test in the block that spends
it. A join that only lends the field must not stop the take. That is the
take across a join that #14440 names. The one-block rule in `semsource.spliceable_body` stays until then.
