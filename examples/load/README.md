# load

An HTTP load generator with the command line of
[oha](https://github.com/hatoo/oha). It sends requests to one URL at a fixed
concurrency or at a fixed rate. It gives the latency distribution as text or
as JSON. It speaks HTTP/1.1 with keep-alive, over TCP or over TLS, and it can
use all CPUs, with a reactor for each CPU. This version is slices 1, 2 and 3
of #15592.

```
$ load -n 2000 -c 10 http://127.0.0.1:8080/
Summary:
  Success rate:	100.00%
  Total:	27.0560 ms
  Slowest:	4.1776 ms
  Fastest:	0.0101 ms
  Average:	0.1014 ms
  Requests/sec:	73920.7433

  Total data:	9.77 KiB
  Size/request:	5 B
  Size/sec:	360.94 KiB

Response time histogram:
  0.010 ms [1]    |
  0.427 ms [1958] |■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  0.844 ms [9]    |
  1.260 ms [1]    |
  1.677 ms [8]    |
  2.094 ms [2]    |
  2.511 ms [0]    |
  2.927 ms [0]    |
  3.344 ms [11]   |
  3.761 ms [3]    |
  4.178 ms [7]    |

Response time distribution:
  10.00% in 0.0187 ms
  25.00% in 0.0219 ms
  50.00% in 0.0461 ms
  75.00% in 0.0824 ms
  90.00% in 0.1148 ms
  95.00% in 0.1586 ms
  99.00% in 2.9614 ms
  99.90% in 4.0612 ms
  99.99% in 4.1776 ms


Details (average, fastest, slowest):
  DNS+dialup:	0.0617 ms, 0.0192 ms, 0.2404 ms
  DNS-lookup:	0.0000 ms, 0.0000 ms, 0.0000 ms

Status code distribution:
  [200] 2000 responses
```

## Build and run

```
$ fern -O -target x86-64-linux -o load examples/load/load.fern   # or arm64-linux
$ load -z 10s -c 50 http://127.0.0.1:8080/
$ load -q 1000 -z 10s -c 50 --latency-correction --output-format json http://127.0.0.1:8080/
$ load -n 10k -c 20 --insecure https://127.0.0.1:8443/
```

## Options

| Option | Effect |
| --- | --- |
| `-n COUNT` | Send COUNT requests. The default is 200. The suffixes `k` (1,000) and `m` (1,000,000) are permitted, for example `10k`. |
| `-z DURATION` | Send requests for DURATION instead of a count, for example `10s`, `500ms`, `1m` or `1m 30s`. |
| `-w`, `--wait-ongoing-requests-after-deadline` | At the end of `-z`, wait for the requests in flight. It needs `-z`. |
| `-c CONNECTIONS` | Use CONNECTIONS connections at the same time. The default is 50. |
| `-q RATE` | Send RATE requests each second, across all connections and all reactors, for example `1000` or `0.5`. The maximum is 1,000,000,000. See [The rate of -q](#the-rate-of--q). |
| `--latency-correction` | With `-q` or a burst, start the latency of a request at the time that it is due. See [The rate of -q](#the-rate-of--q). |
| `--burst-delay DURATION`, `--burst-rate COUNT` | Send COUNT requests at the start of each DURATION. Give the two options together, and not with `-q`. |
| `--worker-threads COUNT` | Use COUNT reactors. The default is one for each 64 connections, and not more than one for each CPU. See [Reactors](#reactors). |
| `-m METHOD`, `--method METHOD` | The method. The default is `GET`. |
| `-H "Name: value"` | Add a header field. Give `-H` again for each field. A field replaces an earlier field of the same name. |
| `-d BODY` | Send BODY as the request body, with a `Content-Length` field. |
| `-D FILE` | Send the bytes of FILE as the request body. |
| `-A ACCEPT` | The value of the `Accept` field. The default is `*/*`. |
| `-T TYPE` | The value of the `Content-Type` field. |
| `-a USER:PASSWORD` | Send an `Authorization` field for Basic authentication. |
| `--host HOST` | The value of the `Host` field. |
| `--disable-compression` | Do not send the `Accept-Encoding: gzip, compress, deflate, br` field of oha. |
| `-t TIMEOUT` | Stop a request that takes longer than TIMEOUT, and count the error `timeout`. The default is no timeout. |
| `--connect-timeout TIMEOUT` | Stop a connect, and the TLS handshake after it, that takes longer than TIMEOUT. The error is `timeout`. The default is 5s. |
| `--disable-keepalive` | Open a new connection for each request, and send `Connection: close`. |
| `-r COUNT`, `--redirect COUNT` | Follow up to COUNT redirects of a request. The default is 0, which counts the redirect response. See [Redirects](#redirects). |
| `--insecure` | Accept the certificate of an `https` server without a check of its chain or its name. |
| `--cacert FILE` | Also trust the certificates in the PEM file FILE. |
| `--connect-to HOST:PORT:TARGET_HOST:TARGET_PORT` | Connect to TARGET_HOST:TARGET_PORT when the URL is HOST:PORT. The `Host` field and the TLS server name stay those of the URL. Put an IPv6 address in brackets. |
| `--ipv4`, `--ipv6` | Use only an address of that family. |
| `--output-format FORMAT` | `text` (the default), `json` or `quiet`. See [The JSON summary](#the-json-summary). `quiet` writes no summary. |
| `-o FILE`, `--output FILE` | Write the summary to FILE instead of to the standard output. |
| `-u UNIT`, `--time-unit UNIT` | Give the times of the text summary in `ns`, `us`, `ms`, `s`, `m` or `h`. |
| `-V`, `--version` | Write the version and stop. |
| `--no-tui`, `--no-color`, `--fps COUNT` | Accepted for compatibility with oha. The tool has no TUI and no colors. |
| `--no-pre-lookup` | Accepted for compatibility with oha. The tool always looks up the name once, before the run. |
| `-p COUNT`, `--http-version 1.1` | Accepted for compatibility with oha. `-p` applies only to HTTP/2. |

The URL is the only operand. Its scheme is `http` or `https`, and its host is
an IP address or a name. The tool uses the first address of the name. An
option value can follow its option, as in `-n 200`. A short option can have
its value in the same argument, as in `-n200`, and a long option after `=`, as
in `--redirect=2`.

An option that is not valid gives a message, the usage, and exit code 2.
These conditions give a message and exit code 1: a host with no address, a
`-D` file that the tool cannot read, and a `--cacert` file with no
certificate. A run
gives exit code 0, also when requests fail, as oha does.

### Options that the tool refuses

These oha options give a message and exit code 2:

- `--http2`, and `--http-version` with a value that is not `1.1`: the tool uses HTTP/1.1.
- `-x`, `--proxy-header`, `--proxy-http-version` and `--proxy-http2`: there are no proxies.
- `--rand-regex-url`, `--urls-from-file`, `--max-repeat` and `--dump-urls`: the tool sends requests to one URL.
- `-Z` and `-F`, `--form`: use `-d` or `-D`, with `-T` for the type.

These oha options also give a message and exit code 2:

- `--aws-session` and `--aws-sigv4`: there are no AWS signatures.
- `--cert` and `--key`: there are no client certificates.
- `--unix-socket` and `--vsock-addr`: the tool connects only over TCP.
- `--db-url`, `--debug` and `--stats-success-breakdown`.
- `--output-format csv`: the tool does not keep a record of each request.

## The summary

The summary has the layout of the text summary of oha 1.16. All times in the
summary use the unit of `-u`, or else the largest unit that the slowest
response is more than: `ns`, `us`, `ms`, `sec`, `min` or `hr`.

- **Success rate** is the responses divided by the requests that ended. A
  response with any status is a success. A request that a connection error
  or a timeout stopped is a failure.
- **Total** is the time of the run.
- **Requests/sec** is the requests that ended, divided by Total.
- **Total data** is the sum of the body lengths of the responses. The tool
  does not count the chunk sizes of a chunked body.
- **Size/request** is Total data divided by the responses, and **Size/sec**
  is Total data divided by Total.

The next blocks give the distribution of the latencies and the details:

- **Response time histogram** divides the range from the fastest to the
  slowest response into 11 bins, as oha does. Each bin gives its highest
  value and its count.
- **Response time distribution** gives the latency at nine percentiles.

The last blocks give the connections and the results:

- **DNS+dialup** gives the time from the start of a request to an open
  connection, for each request that opened a connection. For `https`, it
  includes the TLS handshake. **DNS-lookup** is always zero: the tool looks
  up the name once, before the run, and no connection waits for a lookup.
- **Status code distribution** gives the count of each status code.
- **Error distribution** gives the count of each error. It is only there when
  a request failed.

The latency of a request starts when the request starts. With
`--latency-correction`, it starts when the request is due. For the first
request on a connection, it includes the time to connect.

## The rate of -q

With `-q RATE`, the tool sends requests on a timetable. Request k (k = 0, 1,
2, and so on) is due at T0 + k / RATE seconds. T0 is the start of the run.
With `--burst-delay D --burst-rate B`, request k is due at
T0 + floor(k / B) × D.

A free connection sends the next request at the time that it is due. When all
connections of a reactor are busy, a request that is due waits for the first
of them that becomes free. The requests of a reactor go out in the order of
the timetable. Thus `-c` is the maximum number of requests in flight.

With `--latency-correction`, the latency of a request starts when it is due,
not when it goes out. Thus when the server stops, each request that is due
during the stop gets the wait in its latency. This is the correction for
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

## Reactors

The tool runs `--worker-threads` reactors. Without the option, it runs one
reactor for each 64 connections, and not more than one for each CPU. Each
reactor is a `std/worker` worker, which is a process on Linux and macOS. The
tool runs the last reactor itself. A reactor has its own connections and its
own event loop, and it shares nothing with the other reactors while it runs.
At the end, the tool merges the tallies of the reactors.

- The reactors divide the `-c` connections. There are not more reactors than
  connections.
- With `-n`, the reactors divide the requests. The run sends exactly `-n`
  requests.

The requests of the reactors follow the options of the run:

- With `-z`, each reactor sends requests until the same end time.
- With `-q` or a burst, reactor j of R sends requests j, j + R, j + 2R and so
  on of the one timetable of the run. Thus the rate is the rate of the run,
  and `-n` stays exact.

A request that is due waits only for the connections of its reactor. An
engine that runs a worker at `spawn`, such as the interpreter, gets one
reactor (`worker.parallel()`).

oha divides the work of all connections between its threads while it runs.
The reactors cannot do that, because each reactor has its own connections. A
reactor with few connections wakes for each few responses. On a host that
also runs the server, these wakes take CPU time from the server.
Thus the default gives each reactor at least 64 connections. See
[Measurement](#measurement).

## HTTPS

An `https` URL connects over TLS 1.3 or TLS 1.2 with `std/tls`. The client
sends the host of the URL as the server name (SNI), except for an IP address,
and offers the ALPN protocol `http/1.1`. It verifies the certificate chain and
the host name against the system roots, and the roots of `--cacert`. A
`SSL_CERT_FILE` variable names a different root file. With `--insecure`, the
client accepts any certificate chain for any name. It still checks that the
server signed the handshake with the key of its certificate.

A TLS connection stays open between requests, as a TCP connection does. A
redirect to another host opens a TLS connection to that host. The TLS records
are made in the process: the tool does not use kernel TLS.

## Redirects

With `-r COUNT`, a response with a `Location` field sends the request again
to that location, as oha does. The location can be an absolute URL, an
absolute path, or a path relative to the request. The summary counts only the
last response, with the latency of all of them.

A location on the host of the
open connection uses that connection when the response keeps it open. A
location on another host opens a connection to that host. When the request
gets more than COUNT redirects, it fails with this error of oha:
`Redirection limit has reached`.

## The JSON summary

`--output-format json` writes one JSON object with the keys of
`oha --output-format json` (oha 1.16), in the same order. Times are in
seconds, except in `metrics.latency_ms`. A value is `null` when there is no
response to measure it.

| Key | Contents |
| --- | --- |
| `summary` | `successRate` (a fraction from 0 to 1), `total`, `slowest`, `fastest`, `average`, `requestsPerSec`, `totalData` (bytes), `sizePerRequest` (bytes, an integer) and `sizePerSec` |
| `metrics` | `success_rate`, `requests_per_sec`, and `latency_ms` with `min`, `mean`, `p50`, `p95`, `p99` and `max` in milliseconds to three decimals |
| `responseTimeHistogram` | The 11 bins of the text summary: the highest value of each bin in seconds, and its count |
| `latencyPercentiles` | `p10`, `p25`, `p50`, `p75`, `p90`, `p95`, `p99`, `p99.9` and `p99.99` |
| `firstByteHistogram`, `firstBytePercentiles` | The same, for the time from the start of a request to the first byte of its response |
| `rps` | `mean`, `stddev`, `max`, `min` and `percentiles` of the requests for each second in each 10 ms of the run |
| `details` | `DNSDialup`, `DNSLookup` and `firstByte`, each with `average`, `fastest` and `slowest` |
| `statusCodeDistribution` | The count of each status code, for example `{"200": 1000}` |
| `errorDistribution` | The count of each error message |

The keys of a histogram are numbers as Rust writes an `f64`: the shortest
digits that read back as the value, with no exponent. Like oha, the tool sorts
these keys as text.

## Differences from oha

- A percentile is the highest value of its histogram bucket, with three
  significant digits. oha gives the exact value. The rank of a percentile is
  the rank of oha: floor(p / 100 × N) + 1 of N responses.

The histograms and the samples are approximate:

- A histogram bin counts each latency at the lowest value of its histogram
  bucket. A latency less than 0.1% above a bin edge can go in the next bin.
- oha measures the first byte at the first data of the body. The tool
  measures it at the first byte of the response, so a response with no body
  also has a first byte.
- `rps` has a sample for each full 10 ms of the run. oha makes a sample for
  each group of responses that end within 10 ms of the first of the group.

These differences are in what the tool does:

- A request that is due waits only for the connections of its reactor. oha
  has one queue for all connections.
- `--worker-threads` has a default of one reactor for each 64 connections,
  and not more than one for each CPU. oha uses a thread for each core.

Some options and fields are not the same:

- DNS-lookup is zero: the tool looks up the name once, before the run.
- The tool refuses a `Content-Length` or `Transfer-Encoding` field from `-H`,
  because the tool frames the body itself.
- A run with no responses gives no latency rows and no histogram rows. oha
  gives `NaN` rows.
- The options in [Options that the tool refuses](#options-that-the-tool-refuses)
  are not there.

As oha does, the tool counts a request in flight at the end of `-z` as
`aborted due to deadline`. This error is not in the success rate. With `-w`,
the tool waits for the response.

## How it works

Each reactor drives its connections with the reactor of `std/async`. Each
connection sends its request and reads the response. When the response is
complete, the connection sends the next request immediately. Thus each request
uses one `sendto` and one `read`, and each `epoll_pwait` gives the events of
many connections.

A timeout of `-t` or `--connect-timeout` does not add a timer for each
request. The reactor wakes at the earliest deadline and looks at the
connections that are in flight.

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

Two histograms merge when the tool adds their counts, so the merged tally of
the reactors is exact.

## Measurement

The table compares the tool with oha 1.16.0 for 5 seconds (`-z 5s`). The
server was the `std/serve` hello server of
`internal/testing/e2e/load_example_test.go`, on the same host, with
`max_connections` and `max_connections_per_ip` set to 4096. With the default
of 100 connections for each address, the server closes connections at
`-c 200`, for both tools. The https server was a Go `net/http` server with a
P-256 certificate, and both tools used `--insecure`.

The runs of the tools alternated, three times. Each row gives the mean of
three runs, and the lowest and the highest run.

| Target | Tool | Requests/sec | CPU time for each request |
| --- | --- | --: | --: |
| http, `-c 50` | oha 1.16.0, 4 threads | 133,101 (130,256 to 137,285) | 9.3 µs |
| http, `-c 50` | this tool, 1 reactor (the default) | 155,493 (118,108 to 184,371) | 5.7 µs |
| http, `-c 50` | this tool, `--worker-threads 4` | 139,812 (128,156 to 146,348) | 6.4 µs |
| http, `-c 200` | oha 1.16.0, 4 threads | 149,674 (134,826 to 168,682) | 9.8 µs |
| http, `-c 200` | this tool, 4 reactors (the default) | 151,118 (130,511 to 166,545) | 6.6 µs |
| https, `-c 50` | oha 1.16.0, 4 threads | 99,672 (89,234 to 114,127) | 13.7 µs |
| https, `-c 50` | this tool, 1 reactor (the default) | 115,230 (103,419 to 123,630) | 7.4 µs |

The CPU time is the user time and the system time of the tool and its
reactors, divided by its responses. The server was the limit of each run: it
used one CPU for http. Thus a lower CPU time for each request is the clearer
result.

Most of the CPU time of the tool is in the kernel: one `sendto` and
one `read` for each request, and one `epoll_pwait` for about seven requests.

With one reactor at `-c 200`, the tool gave 164,194 to 192,796 requests/sec
in five runs. Thus on this host, fewer reactors give more requests/sec,
because the reactors and the server share the 4 CPUs.

The host was a 4-core x86-64 container (Intel Xeon at 2.1 GHz, Linux 6.18) on
2026-11-14. Two other jobs used about two of the CPUs during the runs. The
load average was 3.5 to 5, and the runs vary by up to 30%.

## Files

| File | Contents |
| --- | --- |
| `load.fern` | The command: the reactors, the connections, TLS and the event loop |
| `options.fern` | The command line, the URL of a redirect, and the request bytes |
| `schedule.fern` | The timetable of `-q` and of bursts, and the part of each reactor |
| `report.fern` | The framing of a response, the tally, and the text and JSON summaries |
| `histogram.fern` | The latency histogram, its merge and its bins |
| `load_test.fern` | The unit tests |

## Tests

- `TestSelfHostExampleLoadUnit` runs `load_test.fern` on x86-64, arm64 and
  wasm32-wasi, with a balanced leak census. It tests the options, the URL of a
  redirect, the request, and the timetable of `-q` and of bursts. It also
  tests the histogram, the response ends, the merge of tallies, the `rps`
  samples and the summaries.
- `TestSelfHostExampleLoadHTTP` runs the command on x86-64 and arm64 against a
  Go `net/http` server that counts its requests and connections. It checks
  that the count of the server is the count of the tool with `-n`, `-z` and
  `-z -w`.

`TestSelfHostExampleLoadHTTP` also checks these cases:

- keep-alive, chunked bodies, a body of many reads, and a body that ends at
  the close
- an incomplete response, a refused connection, and the usage errors
- `-m`, `-H`, `-d`, `-D`, `-A`, `-T`, `-a`, `--host` and `--disable-keepalive`
- exact `-n` and `-c` with several `--worker-threads`, and the rate of `-q`
  over four reactors
- HTTPS with `--insecure`, with `--cacert` and `-r`, and with no trusted root
- each JSON key, `-t`, `-r`, `--connect-to`, bursts, `-o`, `-u`,
  `--output-format quiet`, `-V` and `--ipv6`

Both are in `internal/testing/e2ecompiler/self_host_example_load_test.go`.

In the `rate json` case of `TestSelfHostExampleLoadHTTP`, the server stops for
50 ms on each 20th request. The tool sends at `-q 200 -c 1` with
`--latency-correction`. The test reads the JSON summary with
`encoding/json`. It checks the count of requests, and that the corrected p75
and p99 show the stops.

`TestLoadExampleX86_64` and `TestLoadExampleInterp` in
`internal/testing/e2e/load_example_test.go` run the tool with `-q` against
a `std/serve` hello, natively and under `-interp`.
