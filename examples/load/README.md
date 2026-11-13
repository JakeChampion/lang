# load

An HTTP load generator with the command line of
[oha](https://github.com/hatoo/oha). It sends requests to one URL at a fixed
concurrency or at a fixed rate. It gives the latency distribution as text or
as JSON. This version is slices 1 and 2 of #15592: HTTP/1.1 with keep-alive on
one CPU.

```
$ load -n 2000 -c 10 http://127.0.0.1:8080/
Summary:
  Success rate:	100.00%
  Total:	18.4833 ms
  Slowest:	2.6793 ms
  Fastest:	0.0148 ms
  Average:	0.0906 ms
  Requests/sec:	108205.7448

Response time distribution:
  10.00% in 0.0320 ms
  25.00% in 0.0444 ms
  50.00% in 0.0687 ms
  75.00% in 0.1094 ms
  90.00% in 0.1729 ms
  95.00% in 0.2171 ms
  99.00% in 0.3451 ms
  99.90% in 0.8965 ms
  99.99% in 2.6793 ms

Status code distribution:
  [200] 2000 responses
```

## Build and run

```
$ fern -O -target x86-64-linux -o load examples/load/load.fern   # or arm64-linux
$ load -z 10s -c 50 http://127.0.0.1:8080/
$ load -q 1000 -z 10s -c 50 --output-format json http://127.0.0.1:8080/
```

## Options

| Option | Effect |
| --- | --- |
| `-n COUNT` | Send COUNT requests. The default is 200. The suffixes `k` (1,000) and `m` (1,000,000) are permitted, for example `10k`. |
| `-z DURATION` | Send requests for DURATION instead of a count, for example `10s`, `500ms`, `1m` or `1m 30s`. |
| `-w`, `--wait-ongoing-requests-after-deadline` | At the end of `-z`, wait for the requests in flight. |
| `-c CONNECTIONS` | Use CONNECTIONS connections at the same time. The default is 50. |
| `-q RATE` | Send RATE requests each second, across all connections, for example `1000` or `0.5`. The maximum is 1,000,000,000. See [The rate of -q](#the-rate-of--q). |
| `--latency-correction` | Accepted for compatibility with oha. The tool always corrects the latency of `-q`. |
| `--output-format FORMAT` | `text` (the default) or `json`. See [The JSON summary](#the-json-summary). |
| `-m METHOD`, `--method METHOD` | The method. The default is `GET`. |
| `-H "Name: value"` | Add a header field. Give `-H` again for each field. |
| `-d BODY` | Send BODY as the request body, with a `Content-Length` field. |
| `--no-tui` | Accepted for compatibility with oha. The tool has no TUI. |

The URL is the only operand. Its host is an IP address or a name, and the
tool uses the first address of the name. An option value can follow its
option, as in `-n 200`, or be in the same argument, as in `-n200`.

An option that is not valid gives a message, the usage line, and exit code 2.
A run gives exit code 0, also when requests fail, as oha does.

## The summary

The summary has the layout of the text summary of oha. It does not have the
response time histogram or the details of the connect times. All
times in the summary use the unit of the slowest response: `ns`, `us`, `ms` or
`secs`.

- **Success rate** is the responses divided by the requests that ended. A
  response with any status is a success. A request that a connection error
  stopped is a failure.
- **Total** is the time of the run.
- **Requests/sec** is the requests that ended, divided by Total.
- **Response time distribution** gives the latency at nine percentiles.
- **Status code distribution** gives the count of each status code.
- **Error distribution** gives the count of each error. It is only there when
  a request failed.

The latency of a request starts when the request starts. With `-q`, it starts
when the request is due. For the first request on a connection, it includes
the time to connect.

## The rate of -q

With `-q RATE`, the tool sends requests on a timetable. Request k (k = 0, 1,
2, and so on) is due at T0 + k / RATE seconds. T0 is the start of the run.

A free connection sends the next request at the time that it is due. When all
connections are busy, a request that is due waits for the first connection
that becomes free. The requests go out in the order of the timetable. Thus
`-c` is the maximum number of requests in flight.

The latency of a request starts when it is due, not when it goes out. Thus
when the server stops, each request that is due during the stop gets the wait
in its latency. This is the correction for
coordinated omission of wrk2.

With `-n COUNT`, the tool sends COUNT requests. The run takes at least
(COUNT - 1) / RATE seconds.

With `-z DURATION`, the tool sends each request that is due before the end of
DURATION. That is not more than DURATION × RATE requests, rounded up. Some
requests can be due at the end with no free connection. The tool does not
send them, and the summary does not count them.

The tool waits in the reactor to 1 ms before the next request is due. Then it
polls. Thus a request goes out not more than one loop of the reactor after it
is due.

Without `-q`, each connection sends its next request when the response to
the previous request is complete.

## The JSON summary

`--output-format json` writes one JSON object with the keys of
`oha --output-format json` (oha 1.16) for the values that the tool measures.
Times are in seconds, except in `metrics.latency_ms`. A latency is `null`
when there is no response.

| Key | Contents |
| --- | --- |
| `summary` | `successRate` (a fraction from 0 to 1), `total`, `slowest`, `fastest`, `average` and `requestsPerSec` |
| `metrics` | `success_rate`, `requests_per_sec`, and `latency_ms` with `min`, `mean`, `p50`, `p95`, `p99` and `max` in milliseconds to three decimals |
| `latencyPercentiles` | `p10`, `p25`, `p50`, `p75`, `p90`, `p95`, `p99`, `p99.9` and `p99.99` |
| `statusCodeDistribution` | The count of each status code, for example `{"200": 1000}` |
| `errorDistribution` | The count of each error message |

These oha keys are not in the JSON summary, because the tool does not
measure their values:

- `summary.totalData`, `summary.sizePerRequest` and `summary.sizePerSec`
- `responseTimeHistogram`
- `firstByteHistogram` and `firstBytePercentiles`
- `rps`
- `details`

## Differences from oha

- The tool uses one CPU. oha uses a thread for each core. Slice 3 adds a
  reactor for each CPU.
- With `-q`, the tool always corrects the latency for coordinated omission.
  oha does this only with `--latency-correction`.
- A percentile is the highest value of its histogram bucket, with three
  significant digits. oha gives the exact value.
- `--output-format` has no `csv` or `quiet`.
- There is no HTTPS. An `https` URL gives exit code 2. Slice 3 adds HTTPS.
- The tool refuses a `Content-Length` or `Transfer-Encoding` field from `-H`,
  because the tool frames the body of `-d` itself.
- A run with no responses gives no latency rows. oha gives `NaN` rows.

As oha does, the tool counts a request in flight at the end of `-z` as
`aborted due to deadline`. This error is not in the success rate. With `-w`,
the tool waits for the response.

## How it works

One thread drives all connections with the reactor of `std/async`. Each
connection sends its request and reads the response. When the response is
complete, the connection sends the next request immediately. Thus each request
uses one `sendto` and one `read`, and each `epoll_pwait` gives the events of
many connections.

`std/http` frames each response. A response with a `Content-Length` field is
complete after that number of bytes. A chunked body is complete after its last
chunk. A response with no length is complete when the server closes the
connection. When a response does not keep
the connection open, the next request opens a new connection.

The latency histogram has the log-linear buckets of HdrHistogram. Each value
below 2,048 ns has its bucket. Above that value, each power of two has 1,024
buckets, so a percentile has three significant digits. The histogram uses
311 KiB for latencies to 39 hours. A percentile is the highest value of its
bucket. It is not more than the slowest response or less than the fastest.

## A first measurement

The table compares the tool with oha 1.16.0 at `-c 50` for 10 seconds. The
server was the `std/serve` hello server of
`internal/testing/e2e/load_example_test.go`, on the same host.
The runs of the two tools alternated. Slice 3 makes the full benchmark.

| Tool | Requests/sec (mean of 3) | CPU time for each request |
| --- | --: | --: |
| oha 1.16.0, 4 threads | 125,158 | 11.3 µs |
| this tool, 1 thread | 172,245 | 5.4 µs |

The CPU time is the user time and the system time of the tool, divided by its
responses. The tool used 93% of one CPU, and 84% of its CPU time was in the
kernel.

The host was a 4-core x86-64 container (Intel Xeon at 2.1 GHz, Linux 6.18) on
2026-11-13. The server and the load generator used the same 4 cores.

## Files

| File | Contents |
| --- | --- |
| `load.fern` | The command: the connections and the reactor loop |
| `options.fern` | The command line and the request bytes |
| `schedule.fern` | The timetable of `-q` |
| `report.fern` | The framing of a response, the tally, and the text and JSON summaries |
| `histogram.fern` | The latency histogram |
| `load_test.fern` | The unit tests |

## Tests

- `TestSelfHostExampleLoadUnit` runs `load_test.fern` on x86-64, arm64 and
  wasm32-wasi, with a balanced leak census. It tests the options, the request, the
  timetable of `-q`, the histogram, the response ends and the summaries.
- `TestSelfHostExampleLoadHTTP` runs the command on x86-64 and arm64 against a
  Go `net/http` server that counts its requests and connections. It checks
  that the count of the server is the count of the tool with `-n`, `-z` and
  `-z -w`. It also checks keep-alive, chunked bodies, a body of many reads, and
  a body that ends at the close. Other cases are an incomplete response, a refused connection,
  `-m`, `-H`, `-d`, and the usage errors.

Both are in `internal/testing/e2ecompiler/self_host_example_load_test.go`.

In the `rate json` case of `TestSelfHostExampleLoadHTTP`, the server stops for
50 ms on each 20th request. The tool sends at `-q 200 -c 1`. The test reads
the JSON summary with `encoding/json`. It checks the count of requests, and
that the corrected p75 and p99 show the stops.

`TestLoadExampleX86_64` and `TestLoadExampleInterp` in
`internal/testing/e2e/load_example_test.go` run the tool with `-q` against
a `std/serve` hello, natively and under `-interp`.
