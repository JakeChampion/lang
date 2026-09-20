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
