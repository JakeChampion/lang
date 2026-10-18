# The hello request's instruction-cache footprint, 2026-10-18

`docs/benchmarks/net-ready-count-2026-10-18.md` left one figure worth
attention: 477 simulated L1 instruction misses per hello request at any
connection count. This records what that number is made of, so the next
reader can decide whether to spend a layout change on it.

## Method

- Server: `serve.run` with the hello handler of
  `internal/testing/e2eharness.ServeAllocsServerSource`, built by `bin/fern
  -g -target x86-64-linux` at the head of the PR that lands this note (the
  `-g` build retires the same instruction count as the default build on
  this program).
- Load: oha on one keep-alive connection, 4,000 and 8,000 requests; the
  slope between the two is the per-request figure.
- Cache: `valgrind --tool=callgrind --cache-sim=yes --I1=32768,8,64`, a
  32 KB, 8-way L1i with 64-byte lines (64 sets).

## Findings

- 611 I1 misses per request on this build (10,548 Ir per request at the
  time), against 477 at 11,826 Ir in the ready-count report; the client's
  longer header values and the build differ, the shape does not.
- At 64 bytes a line, 611 misses is about 39 KB of distinct code touched
  per request, more than the cache holds: about 9.5 lines per set for
  8 ways, and the access pattern is a cycle, so every set evicts what the
  next request needs and essentially every touched line misses once per
  request. The miss count is the size of the footprint, not a hot spot.
- By function: `http_parse_request_framed_from` 109, `__serve_burst` 90,
  `__serve_loop` 69, `__serve_respond` 34, `__sem_drop_serve____Conns`
  29 (gone since), `__serve_read_plain` 21, `__serve_next` 15,
  `__serve_wire` 15, `__serve_start` 14; the remaining 215 are spread over
  some fifty functions at under 12 each.
- How much of each touched line is cold code interleaved with the hot
  path is not measured here: instructions retired per touched line
  average 17, but a loop re-executes its lines, so that average does not
  separate density from repetition. Per-line counts from callgrind would.

## What it means

The footprint has to shrink by about a quarter (below roughly 8 lines a
set, about 30 KB) before any of it stays resident between requests; a
smaller cut buys nothing under a cyclic pattern. Moving cold arms out of
line counts toward that only by the cold bytes that sit in touched lines,
which this run did not quantify, and the rest is a request-path size
budget. It is left as a measurement.
