# HTTP two-fetch throughput, 2026-10-04

The first report of `scripts/net-bench` with the two-fetch servers P3's
exit criterion names (`docs/NET-P3-SUSPENSION-PLAN.md` §1, #9857): a
handler that fetches an upstream twice per request, loaded by oha 1.16.0
closed-loop at 64 and 4,096 connections. The Fern one
(`scripts/net-bench.d/twofetch.fern`) is a single loop built by the
self-host compiler, every handler parked on the upstream between its
request and the answer, reaching the Go hello through it as a forward
proxy; the hyper one (`scripts/net-bench.d/hyperhello`'s `twofetch`) is
hyper 1.11.1's http1 server and hyper-util's pooled client on a tokio
runtime with a worker per core, reaching the same Go hello directly. Loads
of 3 s each on the 4-core development container, generator, upstream and
servers on the same cores, so the figures are a ratio between the two
servers on one box and not a capacity; the nightly lane records 10 s loads
on a GitHub runner, and `scripts/perf-history show
net/fern-twofetch/c4096.p99_us` reads that trend back. The hello rows are
the same run's, for the ratio between a hello and a two-fetch request.

```
# scripts/net-bench report — arch x86_64, target x86-64-linux, 4 cpus
# 3s per load, connections: 64 4096, oha 1.16.0, go1.26.0, hyper 1.11.1 (1.97.0), h2o 2.2.5
net/fern/c64.rps	101475
net/fern/c64.p50_us	405
net/fern/c64.p99_us	3503
net/fern/c64.failed	0
net/fern/c4096.rps	67356
net/fern/c4096.p50_us	43319
net/fern/c4096.p99_us	187213
net/fern/c4096.failed	0
net/go/c64.rps	74843
net/go/c64.p50_us	374
net/go/c64.p99_us	6379
net/go/c64.failed	0
net/go/c4096.rps	67630
net/go/c4096.p50_us	58009
net/go/c4096.p99_us	162817
net/go/c4096.failed	0
net/hyper/c64.rps	119347
net/hyper/c64.p50_us	436
net/hyper/c64.p99_us	2038
net/hyper/c64.failed	0
net/hyper/c4096.rps	96263
net/hyper/c4096.p50_us	33364
net/hyper/c4096.p99_us	133481
net/hyper/c4096.failed	0
net/h2o/c64.rps	110078
net/h2o/c64.p50_us	411
net/h2o/c64.p99_us	3665
net/h2o/c64.failed	0
net/h2o/c4096.rps	102935
net/h2o/c4096.p50_us	8934
net/h2o/c4096.p99_us	26804
net/h2o/c4096.failed	0
net/fern-twofetch/c64.rps	3316
net/fern-twofetch/c64.p50_us	17805
net/fern-twofetch/c64.p99_us	35416
net/fern-twofetch/c64.failed	0
net/fern-twofetch/c4096.rps	2418
net/fern-twofetch/c4096.p50_us	1437824
net/fern-twofetch/c4096.p99_us	1661017
net/fern-twofetch/c4096.failed	0
net/hyper-twofetch/c64.rps	20838
net/hyper-twofetch/c64.p50_us	2737
net/hyper-twofetch/c64.p99_us	8680
net/hyper-twofetch/c64.failed	0
net/hyper-twofetch/c4096.rps	16487
net/hyper-twofetch/c4096.p50_us	135368
net/hyper-twofetch/c4096.p99_us	2241844
net/hyper-twofetch/c4096.failed	0
net/fern/held.bytes_per_connection	560
net/fern/held.bytes_per_suspended_handler	12034
```

Read `.rps` as requests per second, `.p50_us` and `.p99_us` as the median
and 99th-percentile latency in microseconds, `.failed` as responses that
were not 200 plus errors other than the requests the end of the run cut
off, `held.bytes_per_connection` as the bump-allocator growth one idle
kept-alive connection costs the Fern loop and
`held.bytes_per_suspended_handler` as the growth one connection whose
handler is parked on its upstream costs it, both read off
`TestSelfHostHeldConnectionsHeapBoundX86_64`.

What the first reading says: one Fern loop holds 4,096 connections each
with a handler parked on the upstream and answers every request, at a p99
under hyper's four-thread p99 at the same count, so the multiplexing
holds at the exit criterion's scale. Its throughput is a sixth of
hyper's, on one core against four and with each request costing two
fetch-client round trips and two park-and-resume cycles; the hello rows
put one two-fetch request at about seven hello requests of that core's
time. The ratio is the number to move, and `perf.yml`'s instruction
counts are where a change to the fetch client or the suspension pass
shows per request.
