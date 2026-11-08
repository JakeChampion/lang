# HTTP/3 sends batched with sendmmsg and UDP_SEGMENT, 2026-11-08

#12516, the send half of `docs/NET-P8-HTTP3-PLAN.md` slice 10's "the
socket". Before this change `std/serve` sent every HTTP/3 datagram with its
own `sendto`. Now a connection's flight becomes `UDP_SEGMENT` messages, and
every flight of one wake of the loop goes out in one `sendmmsg` before the
loop waits again (`net.Batch`, `__h3_drain`).

## Method

The HTTP/3 hello server and clients of `e2eharness.ServeAllocsH3ServerSource`,
`ServeAllocsH3ClientSource` and `ServeAllocsH3MuxClientSource`, x86-64 builds
on a 4-core container, the server under `strace -f -c`. Three shapes:

- `seq`: one connection, 20,000 requests one at a time.
- `mux`: one connection, 20,000 requests with 64 in flight.
- `c64`: 64 connections at once, 500 requests each, one at a time.

Server CPU is `utime + stime` from `/proc/PID/stat` over the run, under
strace, which makes every system call dearer than it is. The system call
counts are strace's. Each shape ran twice per build. The rows are the second
runs, and the first runs agree within a few percent.

Three builds: `base` is main at 282bfc9c, `flight` sends each flight through
`net.send_many` (one call per flush), `outbox` gathers every flight of a wake
and sends once.

## Result

| shape, build | sends | sends per request | server CPU, µs per request | wall s |
|---|--:|--:|--:|--:|
| seq, base | 22,005 `sendto` | 1.100 | 119.5 | 5.72 |
| seq, flight | 22,004 `sendmmsg` | 1.100 | 118.5 | 5.49 |
| seq, outbox | 22,004 `sendmmsg` | 1.100 | 118.0 | 5.72 |
| mux, base | 1,030 `sendto` | 0.051 | 6.0 | 0.31 |
| mux, flight | 463 `sendmmsg` | 0.023 | 5.0 | 0.31 |
| mux, outbox | 463 `sendmmsg` | 0.023 | 4.5 | 0.26 |
| c64, base | 160,368 `sendto` | 5.011 | 318.8 | 22.0 |
| c64, flight | 160,346 `sendmmsg` | 5.011 | 317.8 | 21.8 |
| c64, outbox | 2,788 `sendmmsg` | 0.087 | 187.2 | 12.4 |

The sequential connection sends one datagram per wake, so nothing is there
to batch. One connection with 64 requests in flight sends two or three
datagrams per wake: `UDP_SEGMENT` makes them one message. With 64
connections the loop sends about 60 datagrams per wake, one per connection,
and the outbox makes them one call: 57 times fewer sends, and the server's
CPU per request falls by 41 percent.

On this box the 64 clients send five
datagrams per request, because their acknowledgement timers fire while the
server is busy. The nightly's h2load load does not do that. The outbox
depends on the count per wake, not on the count per request.

The receive side is unchanged: one `recvfrom` per datagram, and about as
many `EAGAIN` reads as datagrams. GRO and `recvmmsg` are the other half of
the slice.

## How to rerun

Build the three fixtures with `bin/fern -stdlib internal/stdlib -target
x86-64-linux`. Start the server on a fixed port under `strace -f -c -p`. Run
the client (`PORT ROUNDS [M]`), or 64 of the sequential client at once. Read
the server's `/proc/PID/stat` before you stop it.
