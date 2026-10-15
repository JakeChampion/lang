# The networking epic's targets, 2026-10-14

Where each row of #9851's §7 ("what best in the world will mean") stands
once all eight phases have landed, measured on main at 957181fc6. Unless a
row names another source, the figures are from the 4-core development
container, with the load generator sharing the cores. The epic asks for a
fixed, dedicated host for the wall-time rows. None exists, so the two
rows that need one take these indicative figures as their answer.

| axis | target | measured | state |
|---|---|---|---|
| binary with HTTP/1.1 + TLS + DNS | under 500 KB static | 2,132,112 bytes (`scripts/net-h2spec.d/server.fern`, HTTP/1.1, h2 and TLS); `scripts/net-bench.d/hello.fern` is 2,230,552 | missed, 4.3x; the work is under #9851 |
| idle heap per worker | `__heap_bump_bytes()` under 64 KiB after first accept | 15,840 bytes at the first request | met |
| per idle keep-alive connection | under 1 KiB of bump growth per held connection | 992 bytes (`net/fern/held.bytes_per_connection`, nightly) | met |
| allocations per request, hello and static | 0 | 0, gated by the zero-allocation tests | met |
| per-request CPU, keep-alive | a ns budget from Experiment 3 on the fixed host | no budget set; no fixed host to set one on | indicative: settled without a host |
| p99 at 4,096 connections | within 1.2x of h2o on the fixed host | about 1.3x at 40,000 req/s, h2o starving some connections for 10 s (below) | indicative: settled without a host |
| cold start, native | under 1 ms from exec to first accept | p50 0.69 ms, p90 0.90 ms from exec to the first *response* (50 starts) | met |
| cold start, wasm component | under 100 µs instantiate under `wasmtime serve`, pooling allocator | p50 0.45 ms per request, which bounds instantiation from above but does not isolate it | open: not isolated |
| a handler against a flaky upstream, deterministically | under 20 lines, seed-replayable | P5 slice 9 (#9859) | met |
| routed JSON API with middleware and graceful shutdown | under 40 lines | P5 slice 9 (#9859) | met |

## Binary size

A program that only prints is 2,912 bytes, so tree-shaking works; the
size is what `serve.run` reaches. By the symbol table of a `-g` build of
the hello server (1,766,982 bytes of functions):

| module | bytes | functions |
|---|--:|--:|
| std/serve | 509,025 | 118 |
| std/tls handshake | 256,902 | 62 |
| main (inlined and monomorphised pieces) | 244,156 | 932 |
| std/http/h2 | 132,822 | 36 |
| x509 | 84,083 | 27 |
| crypto | 53,229 | 45 |
| std/http | 48,311 | 40 |

Two things put it over. A plain HTTP/1.1 server carries TLS and HTTP/2,
since `serve.run` chooses them from its `Config` at run time, so both are
reachable whatever the program asks for. And the functions are large:
`__serve_loop` is 105 KB and `main` 93 KB, the inliner's splices and the
reference-counting code compounded. The serve module alone is over the
whole target.

`-size` builds call the reference-count helpers instead of testing the
count inline, and splice only what is called once
(docs/SELFHOST-SSA-BACKEND.md). At 3ae61060f that takes the TLS server
from 2,105,368 bytes to 1,374,696 and the hello server from 2,206,008 to
1,438,928; the reference-count half alone is 1,583,480 and 1,664,832. What is left is
code density: about 300,000 instructions, 37% of them moves.

## p99 at 4,096 connections

oha 1.16.0 at a fixed rate with `--latency-correction`, 10 s per load,
three rounds each, against h2o 2.2.5 (one thread per core, the mruby
hello net-bench configures) and the Fern hello server (a worker per
core).

| rate | server | p50 ms | p99 ms | p99.9 ms | responses |
|---|---|--:|--:|--:|--:|
| 20,000 | h2o | 0.40 | 9,861 to 9,880 | 9,907 to 9,928 | 199,543 to 199,908 |
| 20,000 | fern | 0.39 to 0.44 | 16 to 39 | 61 to 67 | 199,972 to 199,991 |
| 40,000 | h2o | 0.60 to 0.70 | 18 to 28 | 9,882 to 9,924 | 399,659 to 399,763 |
| 40,000 | fern | 0.69 to 0.70 | 20 to 42 | 35 to 90 | 399,930 to 399,985 |

h2o leaves some of the 4,096 connections waiting about ten seconds, so at
20,000 its p99 is that wait, and at 40,000 its p99.9 is. Fern answers
every connection inside 90 ms. At 40,000 Fern's p99 is about 1.3 times
h2o's by the medians, with the rounds' own spread wider than the gap. On
a box where the generator shares the four cores this is indicative,
and it is the row's recorded answer. The closed-loop nightly figures (`net/fern/c4096.p99_us` 53,951
against `net/h2o/c4096.p99_us` 19,284) do not either: closed loop ties
latency to throughput (4,096 connections over 87,755 req/s is 47 ms), and
h2o's p99 below its own mean says it was not serving all 4,096 at once.

## How to rerun

- Binary size: `bin/fern -target x86-64-linux -o S
  scripts/net-h2spec.d/server.fern`, then `-g` and `nm -S --size-sort`
  for the split.
- Idle heap: a hello handler answering `__heap_bump_bytes()`.
- Cold start: exec with an inherited listener (`LISTEN_FDS=1`), then time
  the first response, over 50 starts.
- p99: `oha -c 4096 -q RATE --latency-correction -z 10s` against each
  server.
