# A spliced chain takes its element

2026-11-25. Each target. The change is in `semsink`. Refs #14440.

## The shape

Since #17098 and #17188, a method of more than one block is spliced. The TLS
keep-alive server then splices the whole write chain into
`serve.__serve_send_out`:

```fern
match (c.tls[at]) {
  Secured(sess) => {
    c = __Conns { ...c, tls: c.tls.with(at, Plain) };
    match (sess.write_framed(framed)) {
      Ok(step) => {
        c = __Conns { ...c, tls: c.tls.with(at, Secured(step.session)) };
```

`write_framed` updates `sess.server.conn.write` and each record above it.
The probe went from 8.00 to 11.00 allocations a request.

## The cause

Two re-read rules of the early `sink` stopped the takes of the chain.

First, `c` dies in the block that stores the element back
(`with(at, Plain)`). As a call, `sess` died in that block too. Spliced,
`sess` lives on into the chain, so `child_live_out` refused to read the
element again there.

The payload thus came from the first read of the element, which the plan
cannot take. The plan retained `sess`. The old `Secured` box went to the
`with`, and the new `Secured` was a new box.

Second, the error arms of `__advanceable` and of the suite match read
`p.key.len()` and `p.iv.len()`. `sink_block` read the whole chain again
from `sess` in each of those blocks, only for the `length`. Those re-reads
of `sess.server` stopped a take of `server` out of `sess` on the main path.

## The change

- `child_held_out` replaces `child_live_out` in `read_above`. A child that
  lives out of the block does not stop the re-read when a block that the
  block dominates spends it. The plan takes that child.
- `used_through`: `sink_block` and `rereads_any` read the parent of a
  counted projection again only when the block can use the projection.
  The block must spend it or a read off it, or it must live out of the
  block. A read that the block only measures gives the plan nothing to
  take.

`reuse` keeps `child_live_out`.

## Measured

Allocations a request, x86-64, main `c9a32177`:

| Probe | main | `child_held_out` only | the change |
|---|--:|--:|--:|
| `TestSelfHostServeAllocsPerRequestTLS` | 11.00 | 10.00 | 7.00 |
| `TestSelfHostServeAllocsPerRequestH3` | 46.69 | 46.69 | 46.69 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 26.64 | 26.64 | 26.64 |

TLS was 8.00 before the multi-block splice. `ServeAllocsTLSCeiling` goes
from 11 to 7.

In the TLS census (`TestSelfHostServeAllocsCensusTLS`), `record.__advanced`
goes from 1.00 to 0. `serve.__serve_send_out` goes from 4.00 to 1.00. Its
last box is the `handshake.Out` that `__out()` gives as a literal.

`TestSelfHostSplicedChainTakesTheElement` holds the first rule. Over 1,000
calls it makes 2,006 allocations on main and 1,007 with the change. 1,007 is
also the count with the chain as a call. The TLS probe holds the second
rule: without it, the probe reads 10.00.

## The traps

- The extra box of the reduction is 24 bytes, the size of a `Session`. It is
  the `Secured` box, which also has two slots.

- `FERN_DEBUG_RC_CERTIFY_DUMP` with `FERN_RC_CERTIFY=report` gives the shape
  of each `arr_box` and `__fern_alloc_reuse`: the `shape_addr` after it.

- A reduction with a `length` read on an error path did not show the second
  rule. The splice folded the read into a block that already held the
  chain. Only the suite arms of the real `seal_framed` show it.

- The census names `serve.__serve_send_out` for the records of the spliced
  chain. The graph name for `FERN_DEBUG_SEM_DUMP` is
  `serve____serve_send_out__async__RealDriver`.

## Next lead

The TLS census at 7.00 has `server.__read` from `server.Session.read` at
2.00 as its largest site.
