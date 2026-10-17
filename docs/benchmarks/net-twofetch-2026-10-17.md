# HTTP two-fetch throughput, 2026-10-17

Why the two-fetch profile (#12506) went from six times behind hyper to
within the 1.2x criterion, and what is left. The profile is a handler
that makes two requests to an upstream before it answers
(`scripts/net-bench.d/twofetch.fern` against hyper-util's pooled client
in `scripts/net-bench.d/hyperhello`). Both servers make the two requests
in turn rather than at once, so the comparison is like for like. The
first report is `net-twofetch-2026-10-04.md`.

## The nightly

`scripts/net-bench` on the GitHub x86-64 runner (4 cores, load generator
and upstream on the same cores), 10 s per load:

| run | main | fern c64 | hyper c64 | ratio | fern c4096 | hyper c4096 | ratio |
|---|---|--:|--:|--:|--:|--:|--:|
| 38711081067, 10-15 | e572e830b | 5,102 | 29,768 | 5.83x | 3,483 | 24,220 | 6.95x |
| 38877915571, 10-16 | 50629c44f | 3,097 | 22,163 | 7.16x | 2,266 | 18,319 | 8.08x |
| 39018037675, 10-17 | d7cbaef08 | 39,761 | 44,414 | 1.12x | 23,827 | 32,219 | 1.35x |

The runner's own speed moves between nights (hyper doubled from 10-16 to
10-17 with no change on its side), so read the ratio, not the rate. One
night under 1.2x meets the criterion once; `scripts/perf-history show
net/fern-twofetch/c64.rps` is the trend.

## What the gap was

Each cause below was measured on its own. Its commit carries the
before and after figures.

1. **One loop against a thread per core.** The Fern server ran one loop
   while hyper ran a worker per core, so the nightly compared one core
   with four. The server now runs under `serve.supervise` (6c564f80d).
2. **The origin check read the resolver's files on every request.** A
   handler's fetch goes through the proxy under the public-address policy,
   which resolves and checks the origin per request. std/dns read
   `/etc/resolv.conf`, the hostname and `/etc/hosts`, and probed a source
   address for NAT64, even for an address literal: nearly half the
   handler's time. `dns.Resolver` now keeps what it read (4f087800f).
3. **The proxy variables were read from the environment per request**,
   and the wall clock was read for a cookie jar the handler did not have
   (#12529).
4. **Runtime costs in the code the compiler emits**:
   - every task-state access walked the task table (#12527);
   - the clock was read by syscall rather than through the vDSO, about 20
     `clock_gettime` calls a request (#12539);
   - a suspending frame saved its words one push at a time, 525 pushes
     and 525 pops a request (#12540).
5. **The loop's per-event costs grew with the connections it held.**
   Lookups scanned every connection (c4be8cec8, 329b6029b). A parked
   fetch's socket was added to the reactor and removed again on every
   fetch (#12556).

## Measured on this change

Measured in the arm64 Linux devbox, main at 7929401, with the
`serve.run` variant of the two-fetch server and a Go upstream.

Syscalls per request under `strace -c -f`, 40,000 requests at 64
connections:

| | read | write | epoll_ctl | epoll_wait | total |
|---|--:|--:|--:|--:|--:|
| Fern | 3.00 | 3.00 | 2.01 | 0.11 | 8.16 |
| hyper | 3.00 | 3.00 | 0.01 | 0.08 | 6.18 |

Upstream connections are reused at 64 connections: 0.002 `connect` per
request on both. The extra `epoll_ctl` are the `EPOLL_CTL_MOD` that
re-arms an upstream socket with ONESHOT for each fetch.

User-space instructions per request under callgrind (one keep-alive
connection, the difference between 300 and 1,300 requests over 1,000):

| | instructions per request |
|---|--:|
| hyper | 66,227 |
| Fern, main | 125,127 |
| Fern, this change | 117,458 |

Allocations per request, from `__heap_alloc_count()` on one keep-alive
connection (`TestSelfHostServeAllocsPerRequestTwoFetch` on x86-64; the
same harness by hand on arm64):

| | x86-64 | arm64 |
|---|--:|--:|
| main | 220 | 226 |
| this change | 170 | 176 |

The change has three parts:

- `net.ipv4_parse` reads the four octets in one pass. It used to split
  the string into owned pieces and grow an array per octet, and a fetch
  through a proxy parses an address three times.
- The proxy rule takes the URL `__target_of` has already parsed, rather
  than parsing it again, and skips `no_proxy` when it is empty.
- The connection pool returns its rows as they are unless a prefix has
  expired, rather than rebuilding them for every take and every park.

## What is left

- **User-space work per fetch.** At c64 the kernel side (loopback sends
  waking the peer) is about the same for both servers. The difference is
  51,000 more user-space instructions per request in Fern, with its 170
  allocations. They are spread across the fetch client:
  - URL parsing, twice per fetch (the request's URL and the proxy's);
  - the proxy's `Proxy-Authorization`, base64 per fetch;
  - the request head built by string concatenation;
  - the response's `HeaderMap`;
  - reference counting and allocator calls under all of these.
  
  No single site is above 4%.
- **Per-event scans at 4,096 connections** (#12613). A single loop's CPU per
  request doubles from c64 to c4096: 25.5 µs to 55.1 µs in the devbox,
  where hyper's rises by about half. The CPU profile puts about 21% of
  the loop's time in the flight table:
  - `__flight_index` scans every parked handler for the descriptor that
    woke;
  - `__flights_remove` copies the table on every wake;
  - `__flights_timeout` scans it on every turn.
  
  Another 6% is upstream connections closed and dialled again, because
  the pool keeps at most 64 idle and a loop with thousands of handlers
  parked on one upstream needs more. Fern made 0.044 `connect` per
  request against hyper's 0.016.
- **One `epoll_ctl` per fetch.** Keeping a pooled upstream socket
  registered across fetches, rather than re-arming it with ONESHOT,
  would save the two calls a request.
- **Ten `getpeername` calls per accepted connection** (#12611).
