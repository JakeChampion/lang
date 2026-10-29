# HTTP two-fetch throughput, 2026-10-29

The two-fetch profile of #12887 (slice 4): a handler that makes two
requests in turn to an upstream before it answers
(`scripts/net-bench.d/twofetch.fern` against the `twofetch` binary in
`scripts/net-bench.d/hyperhello`). The previous report is
`net-twofetch-2026-10-17.md`.

## The nightly before this work

`scripts/net-bench` on the GitHub x86-64 runner, run 40673650834 on
2026-10-28, main at b0241b2f3:

| server | c64 req/s | c64 user + sys µs per request | c4096 req/s | c4096 user + sys µs per request |
|---|--:|--:|--:|--:|
| fern-twofetch | 17,912 | 57.2 + 48.2 | 12,047 | 104.5 + 79.8 |
| hyper-twofetch | 22,861 | 29.4 + 41.8 | 18,167 | 43.8 + 57.5 |

Fern was 22% behind at 64 connections and 34% behind at 4,096.

## What changed

Instructions for each request, from callgrind on the server under oha at
64 connections. The figure is the difference between a run of 9,000
requests and a run of 3,000, divided by 6,000. Every response must be a
200: when the upstream is gone, each request is a 502, and oha still
counts it as a success.

| change | instructions per request |
|---|--:|
| main before #13807 | 77,900 |
| #13807: suspend fold, paired zero stores, task mode load, literal origin check | 55,100 |
| #13817: a response reuses the connection's last header block | 54,000 |
| #13818: a range loop proves its index | 53,800 |

#13758, #13771, #13799 and #13802 removed the allocations and the
scans that grew with the connections. #13802 also brought the
allocations for each request from 103 to 58, which
`TestSelfHostServeAllocsPerRequestTwoFetch` holds.

## Measured on this box

A 4-core container, main at 07f1f4458 with #13818. Fern and hyper each
run against the Go hello upstream that `scripts/net-bench` uses. oha
runs 8 s at each connection count. The CPU figures are the server's
session over the load, divided by the requests it answered. Two runs,
in each order:

| run | server | c64 req/s | c64 user + sys µs | c4096 req/s | c4096 user + sys µs | c4096 p99 |
|---|---|--:|--:|--:|--:|--:|
| 1 | fern | 35,100 | 16.5 + 22.3 | 29,141 | 23.1 + 30.5 | 294 ms |
| 1 | hyper | 31,275 | 22.5 + 23.9 | 25,570 | 35.7 + 34.2 | 426 ms |
| 2 | hyper | 31,904 | 23.4 + 22.3 | 25,611 | 36.8 + 33.1 | 451 ms |
| 2 | fern | 34,031 | 16.4 + 22.5 | 28,422 | 23.7 + 31.8 | 280 ms |

On this box Fern is 7% to 12% ahead of hyper at 64 connections and 11%
to 14% ahead at 4,096. Its user time for each request is 30% to 36%
lower. The nightly is the record. This box is not the runner, so read
the ratio, not the rate.

## What is left

- A call that suspends saves about 120 words and restores them on resume.
  The copies cost about 1,400 instructions for each request, one load
  and one store for each word.
- The phis at the suspend prologue take the lift's entry zero on the
  path that does not resume. Their stores cost about 450 instructions
  for each request, although no read sees the zero.
- Each header field costs about 380 instructions. The scan kernels pick
  their 32-byte path from the length of the whole read buffer, so a
  short token pays the full vector setup.
