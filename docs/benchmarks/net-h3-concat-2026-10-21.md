# HTTP/3 per-request cost: the scalar concat grows in place, 2026-10-21

#12887 slice 1, continuing `docs/benchmarks/net-h3-encode-2026-10-21.md`.
With the header encoder's scans gone, the multiplexed profile's allocation
churn is spread over the QUIC and HTTP/3 layers' byte buffers, and the
single runtime entry allocating most often among them was `__fern_arr_concat`:
30,726 boxes over the 3,000 measured requests, one for every call, where the
appends around it had been growing their receivers in place since the
Perceus port.

## What changed

`xs.concat(ys)` over scalar elements was a fresh box of both on every call,
whatever the receiver's count. It is now a box update like `append`: the
plan treats a scalar concat's receiver as it treats an append's
(`ssaunits.box_update`), and the lowering takes `append_push`'s four forms
(`ssarc.concat_push`): onto a link, onto a field of a record the frame
owns, onto a receiver the frame holds no unit of, and onto one it does. The
runtime body (`asmcore.rt_src_arr_concat`, `wasm_ir.arr_concat_helper`)
appends onto the receiver's own block when it holds the block's only count
and the block has room, and otherwise copies into a fresh box — with room
to grow, twice the receiver's length, when the receiver was sole and outgrew
its block, so a chain `x = x.concat(chunk)` pays amortised O(1) boxes, and
exactly sized when the receiver is shared, so a one-shot join of a kept
array costs what it did. The runtime frees nothing; the lowering releases
the receiver only when the result moved, by comparing addresses, as
core/map's copy-on-write caller does.

The RC certifier (`rcsigs.fern`) reads the call as `effect_unsure` on the
receiver, as `__map_cow_inplace`'s row does, with the result marked counted
(`ir.counted`) where the frame holds the receiver's unit and left unmarked,
as the borrowed push's is, where it does not. The Fern-written runtime body
is itself certified, which is why it takes the receiver as a raw block
(`pa: usize`, as `__fern_bytes_into` does): a body may not hand a counted
parameter back as its result.

`internal/testing/e2ecompiler/self_host_array_concat_kernel_test.go` pins
the shapes on arm64, x86-64, the sanitizer and wasm: 200 concats of a
16-byte chunk onto a reassigned local take 9 boxes rather than 200, 200 onto
a record's field rebuilt around the join take 8, and a receiver another
binding still reads is left as it was.

## Measured

The hello server under `with_tls().with_h3()`, x86-64, `-g` build, under
callgrind, a client holding 64 requests in flight on one connection over the
warm-up plus 3,000 measured requests (the shape of
`docs/benchmarks/net-h3-encode-2026-10-21.md`). The server is stopped by
SIGTERM once the client exits, so the whole-run instruction count carries the
idle loop's noise (±2% between runs) and is not the figure; the per-function
counts and the ratchets' 100,000-request means are.

Boxes `__fern_arr_concat` allocated, and its cost by caller:

| caller | calls | before (Ir/call) | after (Ir/call) |
|---|--:|--:|--:|
| `quic/streams.Streams.write` | 10,006 | 132 | 148 |
| `http/h3.__frame` | 10,008 | 112 | 105 |
| `http/h3.Session.on_stream` (`__on_request`) | 5,046 | 109 | 125 |
| `http/qpack.Encoder.__put_str` | 5,003 | 123 | 89 |
| boxes from `__fern_arr_concat` | | 30,726 | 15,433 |

Two sites go in place now (`__put_str`'s output buffer, threaded through the
encoder with room to spare after its first doubling; `__frame`'s owned
prefix), and two cannot: `Streams.write` joins onto `st.send.buf` where
`st` is `s.list[i]`, a record the list still holds a count on, and
`__on_request` joins onto `reqs[i].frames.buf` the same way. Those pay the
uniqueness test and the exact copy, 16 instructions a call more than the
blind copy did. The net on the whole run is inside the noise (209.6 M before,
209.9 M after on one pair of runs, 213.8 M on another).

The allocation ratchets, 100,000 requests each on this box:

| row | before | after |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequestH3` (serial) | 231.19 | 226.06 |
| `TestSelfHostServeAllocsPerRequestH3Mux` (64 in flight) | 133.33 | 130.32 |

Both ceilings are lowered to the measurement (227 and 131). The plain, H2,
TLS and two-fetch rows are unchanged to the count.

## Next

The two shared-receiver sites are one shape: a buffer in a record held in a
list, read through `list[i]`, updated and put back with `.with(i, …)`. The
array keeps its count on the element across the update, so neither the
record nor its buffer is ever sole at the join. Taking the element's unit
out of the slot for the duration of the update — the array is sole, and the
slot is overwritten before anything reads it again — would let both the
record update and the concat run in place. The `h3q.Conn` session updates
in the same profile have the same shape, and would gain the same way.
