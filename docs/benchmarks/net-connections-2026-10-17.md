# What a connection costs the serve loop, 2026-10-17

#12504 and #12505 ask why Fern's HTTP/1.1 p99 and its throughput at 4,096
connections trail H2O on the nightly runner. This records what was measured
in the arm64 Linux devbox (12 cores, shared with other work), what was
found, and the two changes that came of it.

## Method

- Servers: `scripts/net-bench.d/hello.fern` under `serve.supervise`, built
  with `bin/fern-selfhost -target arm64-linux` against a clean `origin/main`
  stdlib and against the branch's; H2O 2.2 with the mruby hello the nightly
  uses (`scripts/net-lib`), `num-threads` equal to the server's cores.
- Load: oha, 5 s per run, six rounds with H2O, main and the branch
  interleaved; medians below.
- Layouts: "shared" puts the server and oha on the same four cores, as the
  runner does; "dedicated" gives the server four cores and oha seven others.
- Deterministic measures: server CPU time from `/proc/PID/stat` over a
  fixed request count, allocations from `__heap_alloc_count()`, and the
  kernel's `TcpExt: ListenOverflows`.

## Findings

1. **The listen backlog overflowed.** At 128 the accept queue overflowed
   under a 4,096-connection burst: 665 to 7,469 `ListenOverflows` per run,
   and each dropped SYN waited out a one-second retransmit, which is the
   1.04 s p99.9 in two of six runs. The loop accepts one connection per
   listener event, so a burst waits in the queue. H2O passes 65535 to
   listen(2). Fixed by #12610: zero overflows in every run since.
2. **A loop pass walked every open connection.** The wait's timeout scanned
   the backlog flags and every deadline, the backlog was found by scanning,
   and the expiry check scanned every deadline, on every pass. With 4,000
   idle keep-alive connections held, one active connection's request cost
   2.5 times the server CPU it cost alone (12.5 against 31.6 us).
3. **A close rebuilt the connection table.** `__conns_remove` copied all
   nineteen per-connection arrays without the closed entry. With 4,000 idle
   connections held, a connect, request and close cycle cost 30 times its
   CPU alone (24 against 739 to 789 us) and allocated 209 times instead of 29.

Findings 2 and 3 are fixed together: a pass reads a list of the backlogged
connections and a lower bound on the deadlines, sweeping the table only once
that bound has passed, and a close clears its slot into a free list that the
next accept takes. With the change the same measurements read 12.4 against
12.6 us per request and 24.5 against 26.0 us per cycle, at 10 allocations
per cycle either way. `TestSelfHostServePerConnectionCostFlat` holds both
properties.

Moving the last connection into the freed slot was tried first and copied
every array on each close: a `with` storing an element read from the same
array keeps that array live (only scalar reads are excused), so the swap
costs as much as the rebuild. Clearing a slot reads no element and writes
in place.

## The hello load against H2O

Medians of six rounds, requests per second and latency in microseconds.
Main includes #12610; the branch adds findings 2 and 3.

| layout | connections | server | req/s | p50 | p99 |
|---|--:|---|--:|--:|--:|
| shared | 64 | H2O | 415,188 | 82 | 1,293 |
| shared | 64 | Fern main | 489,496 | 50 | 1,651 |
| shared | 64 | Fern branch | 517,758 | 44 | 1,890 |
| shared | 4,096 | H2O | 389,668 | 2,156 | 7,298 |
| shared | 4,096 | Fern main | 353,158 | 10,591 | 29,702 |
| shared | 4,096 | Fern branch | 350,500 | 10,706 | 28,336 |
| dedicated | 64 | H2O | 480,070 | 116 | 454 |
| dedicated | 64 | Fern main | 560,791 | 82 | 660 |
| dedicated | 64 | Fern branch | 598,308 | 83 | 394 |
| dedicated | 4,096 | H2O | 542,065 | 1,627 | 5,610 |
| dedicated | 4,096 | Fern main | 584,134 | 6,280 | 17,186 |
| dedicated | 4,096 | Fern branch | 586,312 | 6,650 | 15,868 |

The hello load keeps every connection busy, so findings 2 and 3 barely show
in it: main and the branch differ by less than the spread between rounds,
which ran from 30% (shared, 4,096) to 12% (dedicated, 64) of the median.
Server CPU per request is 3.6 us for Fern and 4.8 us for H2O at 64
connections in the shared layout.

H2O's p50 at 4,096 connections sits far below its mean: 4,096 connections
over 390,000 req/s is a 10.5 ms mean, against a 2.2 ms p50. It answers some
connections much more often than others, which keeps its request-weighted
p99 low. Fern's p50 is close to its mean, the shape of fair queueing. So the
p99 gap at 4,096 connections reflects that difference in fairness rather
than a per-request cost.

At 64 connections in the shared layout Fern's p50 is half H2O's and its p99
higher; on dedicated cores Fern's p99 matches or beats H2O's. The tail there
follows the load generator and the server competing for the same cores
(#12509), not work in the loop.

## Left

- The per-client cap (`max_connections_per_ip`, 100 by default) still
  counts a new peer's connections by scanning the table on every accept
  (#12621).
