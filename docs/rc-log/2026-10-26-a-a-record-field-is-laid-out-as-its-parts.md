# 2026-10-26 — a record field is laid out as its parts

`sempair.laid_fields`, `split_calls` (now driven by `Sites`), `read_laid`;
`semscalarreturn.split_operands` and `read_of_parts`; `seminline.fields_all`;
`semsource.laid_records` and `Built.laid`; `semlower.laid_decls`;
`irtables.replaced_decls`. The rest of #12723's allocation regression, after
`2026-10-25-g`.

## The shape

```fern
pub struct Wait { task: i32, set: i32[], timeout: Option[Duration] }
pub struct Sent { pn: i64, at: Instant, size: i32, ack_eliciting: boolean, in_flight: boolean, resend: Resend[] }
pub struct Kept { place: __Place, fd: i32, since: Instant, tls: Option[client.Session], h2: Option[h2.Conn] }
```

A record's field was a reference whatever its type, so a field of type
`Instant` or `Option[Duration]` cost a box of its own, and an Option of a
record cost two. #13369 gave these three fields those types, where they had
been `i64` or `i32`. QUIC records a `Sent` for every packet it sends,
`async.run_task_call` builds a `Wait` at every park, and `fetch.__park` builds
a `Kept` for every connection it pools.

The cause was found by reversing #13369's stdlib diff onto the current stdlib
and tracing the H3 server on the same compiler under two client run lengths.
The difference per request is `quic.conn.Conn.__recorded` building
`Sent.at`, one allocation per packet. Before this change, the two-fetch trace
put 8 of its 12 extra allocations in `async.run_task_call` and 2 in
`fetch.__park`.

## What changed

Such a field is laid out inside its record's box as its parts:
`Wait`'s fields become `task, set, timeout$0, timeout$1, timeout$2`. A
construction takes each operand apart the way a call takes an argument
apart, on the same driver. A read of the field becomes one read per part, and
the reads that take it apart become copies of those parts. The pass runs on
the semantic form between the parameter split and the return pairing, so a
call result stored into such a field is read apart, and its callee can pair.

A field is laid out only when every read of it, across the program, takes it
apart or hands it to a field laid out the same way. A record is left alone
when code outside these bodies could see its layout: a callee with no body,
a map, a dynamic value, a construction hole; or it is generic, an
environment, holds a view, or has a finalizer.

The record's new schema replaces the old in every function and in the
module, and its struct declaration replaces the module's own for the
backends. Wasm reads each field's width from that declaration when it builds
the box.

The split driver had a hole that a record with two Option fields would have
hit. When one site had two Option operands that both needed a branch, it
branched for the first and read the second's payload unguarded. The typed
lowering refuses that ("unguarded variant projection"), so a call such as
`both(xs[i], ys[j])` with two `Option[Duration]` parameters did not compile.
Each such operand now gets its own branch before the site is rewritten.
`TestSelfHostScalarParams` has the call.

## Measured

Serve allocations a request, `internal/testing/e2ecompiler`, against
`2026-10-25-g` (the parameter split alone).

| Test | Pin | Before | After |
|---|---:|---:|---:|
| `TestSelfHostServeAllocsPerRequest` | 0 | 0 | 0 |
| `…H2` | 21 | 20 | 20.0 |
| `…TLS` | 11 | 10 | 10.0 |
| `…H3` | 67 | 67.93 | 66.93 |
| `…H3Mux` | 36 | 35.7 | 35.72 |
| `…TwoFetch` | 91 | 97 | 91.00 |

`TestSelfHostLaidFields` builds 1000 rounds of four such records across a
module boundary on the three targets. It allocates 4000 times, one box per
record, where the parameter split alone allocates 8766. The compiler compiles
itself to the same bytes with the pass.

## What is left

`Duration.sub` still boxes its result under `Instant.since` on the two-fetch
path, because a caller elsewhere keeps the result whole and pairing is decided
per callee. Two-fetch sits exactly on its pin.
