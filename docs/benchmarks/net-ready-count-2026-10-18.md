# What a ready connection costs the serve loop at 4,096, 2026-10-18

`docs/benchmarks/net-connections-2026-10-17.md` left one throughput gap:
in the shared layout (server and load generator on the same four cores)
Fern's hello answers 353k req/s at 4,096 connections to H2O's 390k, while
at 64 connections and on dedicated cores Fern is ahead. This records a
search for work in the HTTP/1.1 loop that grows with the number of
connections ready at once or with the size of the connection table, so
that nobody looks there again without new evidence. There is none left
to find: the loop executes the same instructions and the same syscalls
per request with 4,096 connections busy as with 64.

## Method

- Server: `scripts/net-bench.d/hello.fern`'s handler under
  `serve.supervise` with one worker, built by `bin/fern -target
  x86-64-linux` from main at 5a196db51, with `max_connections: 16384`
  and `max_connections_per_ip: 0` as the benchmark server has.
- Load: oha holding N connections closed-loop for a fixed request count.
- Instructions: the worker under `valgrind --tool=callgrind`, at 10,000
  and 40,000 requests for each N; the slope between the two is the cost
  of a request, the intercept what the N connections cost to open and
  close. `--cache-sim=yes` on a `-g` build for the simulated 32 KB L1 and
  34 MB LL misses.
- Syscalls: `strace -c` on the worker over 200,000 requests.
- CPU time: the worker's user and system ticks from `/proc/PID/stat` over
  400,000 requests, with the worker and oha pinned to separate cores
  (worker on one, oha on three) and to the same four. Box: 4-core Xeon at
  2.8 GHz, 4 MB L2 per core, 33 MB L3, with other work running (load
  average 6), so these two numbers are indicative and the others are not
  affected by the load.

## Findings

1. **Instructions per request are flat.** 11,826 Ir per request at 64
   connections and 11,835 at 4,096: 9 instructions, 0.08%, apart. The
   intercepts differ by 27.8M Ir, which is the 4,032 extra connections at
   6,900 Ir each to accept and close. Every per-pass cost (the timeouts,
   the clock, the expiry sweep, the flight and HTTP/3 ticks) and every
   per-event cost (the slot lookup, the read, the parse, the handler, the
   write) is independent of the table size and the ready count.
2. **Syscalls per request are the same set.** One `read` and one `sendto`
   per request at both counts, plus `epoll_pwait` shared across the
   events it returns: one per 61 requests at 64 connections and one per 28
   at 4,096, under strace. Per connection: `accept`, four `getpeername`
   (#12611), two `fcntl`, `setsockopt`, two `epoll_ctl` and `close`, plus
   the `read` that sees the end of stream.
3. **The loop's own cache footprint does not grow with the table.** The
   simulated L1 data misses rise from 0.2 to 5.0 per request between 64
   and 4,096 connections, spread over the per-connection array reads
   (`__serve_burst` 1.3, `__body_deadline` 0.5, `__in_flight` 0.4,
   `__serve_read_plain` 0.4), and all of them hit the next level: LL read
   misses are 229 against 230 over 20,000 requests. At 14 cycles a miss
   that is under 0.1 us per request. Instruction misses are 477 per
   request at either count, the one cache figure worth attention: the
   request's 11.8k instructions span more code than a 32 KB L1 holds,
   with `__serve_burst` (86 a request) and
   `http_parse_request_framed_from` (64) the largest. That cost does not
   depend on N; it belongs with the per-request work.
4. **CPU time per request still grows by a third.** On this box one
   request costs the worker 3.9 us user and 10.7 us system at 64
   connections, 5.3 and 14.3 us at 4,096, with the cores dedicated; 3.7
   and 7.8 against 4.5 and 10.8 us with the cores shared. With the
   instruction and syscall counts unchanged, and the loop's own misses
   accounting for under 0.1 us of it, the growth is the kernel's
   per-socket state: 4,096 pairs of loopback sockets, their epoll items
   and their buffers no longer fit the cache the 64 did, and each pass
   through them evicts the loop's working set as well. System time is
   two to three times user time at either count, since on loopback the
   sender's `sendto` runs the receiver's TCP processing too.

## What this means for the gap

The shared-layout gap at 4,096 connections is not a per-pass or
per-event cost in the loop, and nothing in `std/serve` makes it cheaper
by scaling better. What differs between the layouts is the kernel's
scheduling of the worker against oha's threads: in the shared layout the
worker was preempted 64,000 times over 400,000 requests at 64
connections (one in six requests) and 25,000 times at 4,096, against
4,500 and 900 with the cores dedicated. Each preemption hands the core
to a thread that then evicts what the worker had cached. One reading of
H2O's p50 far below its mean at 4,096 connections, noted in the
2026-10-17 record, is that its threads hold unequal shares of the
connections, so the busier threads' working sets stay smaller than a
fair share would be; nothing here tests that, and it would not be a
shape to copy.

The per-request levers left are in what every request executes, which
PR #12630 (`http1-hot-path`) is cutting, and in the code footprint
finding 3 measures.

## Left

- The accept path's nine syscalls per connection, four of them
  `getpeername` for one address (#12611).
- The per-client cap's scan of the table on every accept (#12621).
