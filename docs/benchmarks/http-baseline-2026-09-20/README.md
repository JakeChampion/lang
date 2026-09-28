# HTTP server baseline, 2026-09-20

The measurement behind §1 of the networking tracking issue (#9851): today's
`tcp_serve` hello handler against a Go `net/http` hello on the same box.

Host: 4-core x86-64 Linux container, kernel 6.18, transparent hugepages
`madvise` (so RSS is readable at page granularity; per
`docs/LOCAL-DEV-LOOP.md` RSS is not comparable across hosts). Go 1.26.0.
Load generator and servers shared the four cores, so absolute numbers are
rough and latencies are closed-loop (subject to coordinated omission).

Files: `hello.fern` (the Fern handler), `go-hello.go` (the Go server),
`loadgen/` (the Go load generator: one connection per request by default,
`-k` for keep-alive, `-c` concurrency, `-d` duration).

```sh
go build -o fern ./cmd/fern
./fern -target x86-64-linux -o hello docs/benchmarks/http-baseline-2026-09-20/hello.fern
PORT=18095 ./hello &                       # 68 KB binary, 92 KB RSS idle
(cd docs/benchmarks/http-baseline-2026-09-20/loadgen && go build -o loadgen .)
loadgen -c 1 -d 5s;  grep VmRSS /proc/$(pgrep -f '^./hello')/status
loadgen -c 16 -d 5s; grep VmRSS ...
loadgen -c 64 -d 5s; grep VmRSS ...
# Go, same box, port 18096: go run docs/benchmarks/http-baseline-2026-09-20/go-hello.go
loadgen -addr 127.0.0.1:18096 -c 16 -d 5s
loadgen -addr 127.0.0.1:18096 -c 64 -d 5s
loadgen -addr 127.0.0.1:18096 -c 64 -d 5s -k
```

| server | connections | c | req/s | p50 | p99 | p99.9 | RSS after |
|---|---|---|---|---|---|---|---|
| Fern hello | close | 1 | 7,771 | 120 µs | 253 µs | 417 µs | 208 MB |
| Fern hello | close | 16 | 30,952 | 402 µs | 1.59 ms | 3.45 ms | 1.03 GB |
| Fern hello | close | 64 | 17,981 | 3.72 ms | 7.92 ms | 13.7 ms | 1.52 GB |
| Go net/http | close | 16 | 23,984 | 555 µs | 2.79 ms | 6.02 ms | 14 MB |
| Go net/http | close | 64 | 27,288 | 1.93 ms | 9.67 ms | 14.1 ms | 15 MB |
| Go net/http | keep-alive | 64 | 104,410 | 471 µs | 3.17 ms | 6.26 ms | 15 MB |

The Fern RSS column is #8003: about 5.3 KB leaked per request, linear, no
plateau (283k requests in total across the three runs). Go idles at 7.3 MB.

## Rerun, 2026-09-28

The same recipe on the same kind of box (4-core x86-64 Linux container,
Go 1.27.1), Fern hello only, built by the Go compiler with the socket
send credit of #10599. RSS is read after each run; the runs are back to
back on one server process.

| connections | c | requests | req/s | p50 | p99 | p99.9 | RSS after |
|---|---|---|---|---|---|---|---|
| close | 16 | 128,046 | 25,609 | 483 µs | 3.59 ms | 8.38 ms | 120 KB |
| close | 16 | 118,989 | 23,798 | 516 µs | 3.79 ms | 9.03 ms | 120 KB |
| close | 16 | 124,680 | 24,936 | 482 µs | 3.61 ms | 9.80 ms | 120 KB |
| close | 64 | 143,176 | 28,635 | 1.70 ms | 11.7 ms | 18.6 ms | 128 KB |
| close | 1 | 32,648 | 6,530 | 141 µs | 339 µs | 1.24 ms | 128 KB |

Idle RSS before the first run was 104 KB. Without the #10599 credit the
same binary grew by about 80 bytes per request, one serialised response
each, 9.9 MB after the first 16-connection run and 19.2 MB after the
second; the 2026-09-20 column above was 5.3 KB per request (#8003).
