# HTTP/3 receives batched with recvmmsg and UDP_GRO, 2026-11-08

#12516, the receive half of `docs/NET-P8-HTTP3-PLAN.md` slice 10's "the
socket". Before this change `std/serve` read every HTTP/3 datagram with its
own `recvfrom`, and one more `recvfrom` per wake answered would-block. Now
one `recvmmsg` with `MSG_WAITFORONE` brings up to 16 messages into a
`net.Inbox`, the loop reads again only when a call filled every slot, and
`UDP_GRO` is on, so a run of datagrams of one size from one peer arrives as
one message that the inbox walks into its datagrams.

## Method

The HTTP/3 hello server and clients of `e2eharness.ServeAllocsH3ServerSource`,
`ServeAllocsH3ClientSource` and `ServeAllocsH3MuxClientSource`, as x86-64 builds
on a 4-core container. The server ran under `strace -f -c`. Three shapes:

- `seq`: one connection, 20,000 requests one at a time.
- `mux`: one connection, 20,000 requests with 64 in flight.
- `c64`: 64 connections at once, 500 requests each, one at a time.

Server CPU is `utime + stime` from `/proc/PID/stat` over the run, under
strace, which makes every system call dearer than it is. The system call
counts are strace's. Each shape ran twice per build. The rows are the second
runs, and the first runs agree within a few percent.

Two builds with one compiler: `base` is main at 5b43e70a, which sends with
`sendmmsg` and receives with `recvfrom`, and `inbox` receives through
`net.Inbox`.

## Result

| shape, build | receives | receives per request | server CPU, µs per request | wall s |
|---|--:|--:|--:|--:|
| seq, base | 44,011 `recvfrom`, 22,005 would-block | 2.200 | 123.5 | 5.89 |
| seq, inbox | 22,005 `recvmmsg` | 1.100 | 93.0 | 4.41 |
| mux, base | 1,156 `recvfrom`, 463 would-block | 0.058 | 6.5 | 0.36 |
| mux, inbox | 463 `recvmmsg` | 0.023 | 6.0 | 0.30 |
| c64, base | 160,941 `recvfrom`, 511 would-block | 5.029 | 182.8 | 12.28 |
| c64, inbox | 10,646 `recvmmsg` | 0.333 | 89.4 | 4.52 |

Every wake used to cost two reads, the datagram and the would-block answer
after it. The inbox reads once. It reads again only when a call filled all
16 slots. So the sequential connection halves its receives, and it spends 25
percent less server CPU per request.

With 64 connections the loop finds
about 15 datagrams in the queue per wake. The receives fall 15 times, and
the server's CPU per request falls by 51 percent. That is on top of the 41
percent the send half took.

`UDP_GRO` changed no count here: these clients send one datagram at a time,
so the kernel has no run to coalesce. The nightly's h2load load sends more
per connection. Its rows measure the same change on main after the merge.

## How to rerun

Build the two servers with `bin/fern -target x86-64-linux`, the base one with
`-stdlib` set to a checkout of main. Start the server on a fixed port under
`strace -f -c -p`. Run the client (`PORT ROUNDS [M]`), or 64 of the
sequential client at once. Read the server's `/proc/PID/stat` before you
stop it.
