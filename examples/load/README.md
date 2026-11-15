# load

An HTTP load generator with the command line of
[oha](https://github.com/hatoo/oha) 1.16. It sends requests at a fixed
concurrency or at a fixed rate. It gives the latency distribution as text,
JSON, CSV or a SQLite database. It speaks HTTP/1.0, HTTP/1.1, HTTP/2 and
HTTP/3. It connects over TCP, TLS, QUIC, a Unix socket or VSOCK, directly or
through a proxy.

It can use all CPUs, with a reactor for each CPU. On a terminal, it shows the
live display of oha. It has every option of oha 1.16 (#15592).

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
$ load -z 10s -c 10 -p 8 --http2 --insecure https://127.0.0.1:8443/
$ load -n 1000 --rand-regex-url 'http://127.0.0.1:8080/[a-z]{2}/[0-9]+'
$ load -n 100 -x http://127.0.0.1:3128 --proxy-http2 https://example.test/
```

## Options

| Option | Effect |
| --- | --- |
| `-n COUNT` | Send COUNT requests. The default is 200. The suffixes `k` (1,000) and `m` (1,000,000) are permitted, for example `10k`. |
| `-z DURATION` | Send requests for DURATION instead of a count, for example `10s`, `500ms`, `1m` or `1m 30s`. |
| `-w`, `--wait-ongoing-requests-after-deadline` | At the end of `-z`, wait for the requests in flight. It needs `-z`. |
| `-c CONNECTIONS` | Use CONNECTIONS connections at the same time. The default is 50. |
| `-p COUNT` | Keep COUNT requests in flight on each connection of HTTP/2 or HTTP/3. The default is 1. |
| `-q RATE` | Send RATE requests each second, across all connections and all reactors, for example `1000` or `0.5`. `0` is no rate. See [The rate of -q](#the-rate-of--q). |
| `--latency-correction` | With `-q` or a burst, start the latency of a request at the time that it is due. |
| `--burst-delay DURATION`, `--burst-rate COUNT` | Send COUNT requests at the start of each DURATION. Give the two options together, and not with `-q`. |
| `--worker-threads COUNT` | Use COUNT reactors. The default is one for each 64 connections, and not more than one for each CPU. See [Reactors](#reactors). |
| `--rand-regex-url` | The URL is a regular expression. Each request goes to a URL that the expression generates. See [Generated URLs](#generated-urls). |
| `--max-repeat COUNT` | The largest count of extra repeats of `*`, `+` and `{n,}` in `--rand-regex-url`. The default is 4. |
| `--urls-from-file` | The operand is a file with one URL on each line. Each request goes to a line that the tool selects at random. |
| `--dump-urls COUNT` | Write COUNT URLs of `--rand-regex-url` or `--urls-from-file`, one on each line, and stop. |
| `-m METHOD`, `--method METHOD` | The method. The default is `GET`. |
| `-H "Name: value"` | Add a header field. Give `-H` again for each field. A field replaces an earlier field of the same name. A `Content-Length` or `Transfer-Encoding: chunked` field frames the body, as in oha. |
| `-d BODY` | Send BODY as the request body. |
| `-D FILE` | Send the bytes of FILE as the request body. |
| `-Z FILE` | Send one line of FILE as the body of each request. The tool selects the line at random. |
| `-F NAME=VALUE`, `--form NAME=VALUE` | Send a `multipart/form-data` body, as curl does. `NAME=@PATH` sends the file PATH. Give `-F` again for each part. |
| `-A ACCEPT` | The value of the `Accept` field. The default is `*/*`. |
| `-T TYPE` | The value of the `Content-Type` field. |
| `-a USER:PASSWORD` | Send an `Authorization` field for Basic authentication. |
| `--aws-sigv4 aws:amz:REGION:SERVICE` | Sign each request with AWS Signature Version 4. The key comes from `-a ACCESS_KEY:SECRET_KEY`. |
| `--aws-session TOKEN` | Send TOKEN as the `X-Amz-Security-Token` of the signature. |
| `--host HOST` | The value of the `Host` field. |
| `--disable-compression` | Do not send the `Accept-Encoding: gzip, compress, deflate, br` field of oha. |
| `-t TIMEOUT` | Stop a request that takes longer than TIMEOUT, and count the error `timeout`. The default is no timeout. |
| `--connect-timeout TIMEOUT` | Stop a connect, and the handshakes after it, that takes longer than TIMEOUT. The default is 5s. |
| `--disable-keepalive` | Open a new connection for each request, and send `Connection: close`. |
| `-r COUNT`, `--redirect COUNT` | Follow up to COUNT redirects of a request. The default is 0. See [Redirects](#redirects). |
| `--http-version VERSION` | `1.0`, `1.1` (the default), `2` or `3`. See [HTTP/2 and HTTP/3](#http2-and-http3). |
| `--http2` | The same as `--http-version 2`. |
| `-x PROXY` | Send the requests through the `http` or `https` proxy PROXY. See [Proxies](#proxies). |
| `--proxy-header "Name: value"` | Add a header field to the requests to the proxy. |
| `--proxy-http-version VERSION`, `--proxy-http2` | Speak `1.0`, `1.1` or `2` to the proxy. |
| `--insecure` | Accept the certificate of an `https` server without a check of its chain or its name. |
| `--cacert FILE` | Also trust the certificates in the PEM file FILE. |
| `--cert FILE`, `--key FILE` | Send the client certificate chain of the PEM file FILE, with its private key. See [HTTPS](#https). |
| `--connect-to HOST:PORT:TARGET_HOST:TARGET_PORT` | Connect to TARGET_HOST:TARGET_PORT when the URL is HOST:PORT. Give it again for more targets, and the tool selects one at random. The `Host` field and the TLS server name stay those of the URL. |
| `--ipv4`, `--ipv6` | Use only an address of that family. |
| `--unix-socket PATH` | Connect to the Unix socket PATH for an `http` URL. |
| `--vsock-addr CID:PORT` | Connect to PORT of the VSOCK context CID for an `http` URL. Linux only. |
| `--output-format FORMAT` | `text` (the default), `json`, `csv` or `quiet`. See [Outputs](#outputs). |
| `-o FILE`, `--output FILE` | Write the output to FILE instead of to the standard output. |
| `-u UNIT`, `--time-unit UNIT` | Give the times of the text summary in `ns`, `us`, `ms`, `s`, `m` or `h`. |
| `--stats-success-breakdown` | Also give the latencies of the responses with a status of 2xx and of the other responses. |
| `--db-url FILE` | Write each response to the SQLite database FILE. See [Outputs](#outputs). |
| `--debug` | Send one request, write the request and the response, and stop. |
| `--no-tui`, `--fps COUNT`, `--no-color` | Do not show the live display, show it COUNT times each second (the default is 16), or show it with no colors. See [The live display](#the-live-display). |
| `--no-pre-lookup` | Accepted, as in oha. The tool always looks up the names once, before the run. |
| `-V`, `--version` | Write the version and stop. |

The URL is the only operand. An option value can follow its option, as in
`-n 200`. A short option can have its value in the same argument, as in
`-n200`, and a long option after `=`, as in `--redirect=2`.

The exit codes are those of oha. These conditions give a message, the usage,
and exit code 2:

- an option that is not valid
- an option that is given twice
- two options that conflict

These conditions give a message and exit code 1:

- a URL that is not valid, or a host with no address
- a file that the tool cannot read, or a `--cacert` file with no certificate
- a header value that is not valid

A run gives exit code 0, also when requests fail, as oha does.

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
sends the host of the URL as the server name (SNI), except for an IP address.
It offers the ALPN protocol `h2` for HTTP/2, and no protocol for HTTP/1, as
oha does.

The client verifies the certificate chain and the host name against the
system roots, and the roots of `--cacert`. A `SSL_CERT_FILE` variable names a
different root file. With `--insecure`, the
client accepts any certificate chain for any name. It still checks that the
server signed the handshake with the key of its certificate.

With `--cert` and `--key`, the client sends its certificate chain when the
server asks for one, over TLS 1.3 and over TLS 1.2. The key is an ECDSA P-256 or
P-384 key, an Ed25519 key or an RSA key, in a PEM file of `std/tls/identity`.

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

## HTTP/2 and HTTP/3

With `--http2`, each connection of `-c` is one connection of HTTP/2 with up
to `-p` requests in flight, as in oha. An `https` URL negotiates `h2` with
ALPN. An `http` URL sends the preface of HTTP/2 with no upgrade (prior
knowledge). The windows of a connection and of a stream are 2^30 - 1 bytes,
as in oha.

With `--http-version 3`, each connection of `-c` is one QUIC connection on a
UDP socket of its own, with up to `-p` requests in flight. The URL must be
`https`, and a proxy is refused. oha 1.16 has HTTP/3 only when it is built
with its `http3` feature. The oha of `mise.toml` does not have it, so the
tests compare HTTP/3 only with a `std/serve` server.

## Proxies

With `-x PROXY`, an `http` URL goes to the proxy in absolute form, as in
`GET http://host/path HTTP/1.1`. An `https` URL goes through a tunnel: the tool
sends `CONNECT host:port` to the proxy, and then speaks TLS to the host in the
tunnel. The fields of `--proxy-header` go to the proxy only.

With `--proxy-http2`, the tool speaks HTTP/2 to the proxy. For an `http` URL,
the requests are streams of HTTP/2 to the proxy, with the host of the URL in
`:authority`. For an `https` URL, the tunnel is a CONNECT stream of HTTP/2
(RFC 9113 section 8.5), and the requests in the tunnel use the version of
`--http-version`. An `https` proxy gets TLS first.

## Generated URLs

With `--rand-regex-url`, the URL is a regular expression of `std/regex`, and
the dots of the URL match only a dot, as in oha. Each request goes to a URL
that the expression generates. The distribution is that of the `rand_regex`
crate of oha. Each repeat count of a repetition has the same chance. Each
branch of an alternation, and each character of a class, also has the same
chance.

`*`, `+` and `{n,}` have up to `--max-repeat` extra repeats.
`--dump-urls COUNT` writes COUNT URLs and stops.

A URL with a different host opens a new connection. Like oha, the tool keeps
a connection open when the next URL has the same host and port.

## Outputs

`--output-format csv` writes one line for each request, as oha does, under
the header `request-start,DNS,DNS+dialup,Response-delay,request-duration,bytes,status`.
The times are in seconds. The tool keeps the record of
each request only for `csv` and `--db-url`.

`--db-url FILE` writes one row for each response to the table
`oha` of the SQLite database FILE, with the schema of oha. The tool writes the
file format of SQLite itself. When FILE is a database of oha or of the tool,
the tool adds its rows to the rows that are there.

The tool refuses a
database with a `-wal` or a `-journal` file, and a database that is not of
this table.

`--debug` sends one request. It writes the request and the response in the
form of the Rust debug output of oha, and stops.

`--stats-success-breakdown` adds two histograms and two distributions to the
text and JSON summaries. They are for the responses with a status of 2xx,
and for the other responses.

## The live display

On a terminal, without `--no-tui`, the tool shows the live display of oha on
the alternative screen. It has the progress and the requests in each time
scale. It also has the status codes, the errors, the requests of the past,
and a histogram of the latencies. The keys `+` and `-` change the time scale, `a` selects it from the
time of the run, and `q` or Ctrl-C stops the run. When the run ends, the
tool restores the terminal and writes the summary. The summary on a terminal
has the colors of oha, except with `--no-color`.

The live display has the cells and the colors that oha 1.16.0 draws for the
same size of terminal, except the count of open files, which is a count of
the process. A unit test compares two frames at 24 rows and 80 columns with
frames that oha drew.

The live display needs the responses as they come, so a run with the live
display uses one reactor. A `std/worker` worker gives its result only when
it ends, and it has no channel to send responses while it runs.

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

The tool and oha give the same exit codes and the same keys in each output.
Their requests have the same bytes, except for the user agent and the
boundary of a form. These differences remain.

The histograms and the samples are approximate:

- A percentile is the highest value of its histogram bucket, with three
  significant digits. oha gives the exact value. The rank of a percentile is
  the rank of oha: floor(p / 100 × N) + 1 of N responses.

- A histogram bin counts each latency at the lowest value of its histogram
  bucket. A latency less than 0.1% above a bin edge can go in the next bin.

- oha measures the first byte at the first data of the body. The tool
  measures it at the first byte of the response, so a response with no body
  also has a first byte. For HTTP/2 and HTTP/3, the first byte is the time of
  the read that completes the head of the response.

- `rps` has a sample for each full 10 ms of the run. oha makes a sample for
  each group of responses that end within 10 ms of the first of the group.

These differences are in what the tool does:

- A request that is due waits only for the connections of its reactor. oha
  has one queue for all connections.

- `--worker-threads` has a default of one reactor for each 64 connections,
  and not more than one for each CPU. oha uses a thread for each core.

- DNS-lookup is zero: the tool looks up the names once, before the run, and
  `--no-pre-lookup` does not change that. With a proxy, the tool looks up only
  the proxy.

- The live display forces one reactor. oha draws it from all its worker
  threads.

- oha 1.16.0 accepts `--http-version 0.9` and `--proxy-http-version 0.9`,
  but it cannot send a request of HTTP/0.9. When a connection is open,
  hyper 1.11 panics before it writes the head of the request, and oha
  aborts with exit code 134. The tool does the same, with the text of the
  panic. The number in the text is the process id of the tool, and oha
  gives the id of its thread. With `RUST_BACKTRACE` set, oha writes the
  frames of a backtrace, and the tool writes none.

- An error that `std/net` has no name for, such as ENODEV, has the text
  `Network error (errno 19) (os error 19)`. oha gives the text of the system,
  `No such device (os error 19)`.

- The user agent is `fern-load/0.4`. The boundary of a form is
  `----formdata-load-` and 32 hex digits. oha uses `oha/1.16.0` and
  `----formdata-oha-`.

As oha does, the tool counts a request in flight at the end of `-z` as
`aborted due to deadline`. This error is not in the success rate. With `-w`,
the tool waits for the response. A run with no response gives the `NaN` rows
of oha.

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

The table compares the tool with oha 1.16.0 on the same host, with the
servers on that host too. The runs of the tools alternated, three times, for
5 seconds each (`-z 5s`). Each row gives the mean of three runs, and the
lowest and the highest run.

- http: the `std/serve` hello server of
  `internal/testing/e2e/load_example_test.go`, `-c 50`.
- https, h2c and h2: a Go `net/http` server with a P-256 certificate, HTTP/2
  in clear text for h2c, and `--insecure` for both tools. HTTP/2 runs used
  `-c 10 -p 10`.

| Target | Tool | Requests/sec | CPU time for each request |
| --- | --- | --: | --: |
| http | oha 1.16.0 | 121,188 (117,762 to 124,672) | 9.8 µs |
| http | this tool | 110,951 (105,898 to 118,908) | 5.5 µs |
| https | oha 1.16.0 | 46,786 (42,956 to 53,751) | 13.4 µs |
| https | this tool | 33,441 (32,043 to 35,172) | 10.8 µs |
| h2c | oha 1.16.0 | 36,628 (33,216 to 41,504) | 13.3 µs |
| h2c | this tool | 34,007 (31,284 to 36,721) | 7.3 µs |
| h2 | oha 1.16.0 | 38,679 (36,741 to 42,392) | 13.3 µs |
| h2 | this tool | 32,821 (32,018 to 33,235) | 8.7 µs |

The CPU time is the user time and the system time of the tool and its
reactors, divided by its responses. The tool uses 20% to 45% less CPU time
for each request than oha on each target.

The requests/sec of a run depend on the load of the host more than on the
tool. In a later set of three alternating runs against the http server, the
tool gave 128,746 requests/sec and oha 95,518. For https at `-c 50`, the
default of one reactor is a limit: in four alternating runs, oha gave 60,497
requests/sec, the tool 53,002 with one reactor and 64,818 with
`--worker-threads 3`. For h2, one reactor gave 37,219 and oha 34,889.

The host was a 4-core x86-64 container (Intel Xeon at 2.1 GHz, Linux 6.18) on
2026-11-14. Other jobs ran on the host, with a load average of 5 to 17, and
the runs vary by up to 40%.

## Files

| File | Contents |
| --- | --- |
| `load.fern` | The command: the reactors, the connections, TLS, proxies, HTTP/2, HTTP/3 and the event loop |
| `options.fern` | The command line, its checks, the URL of a redirect, and the request bytes |
| `fields.fern` | The header map, with the order and the replace rules of oha |
| `body.fern` | The bodies of `-Z` and `-F` |
| `urls.fern` | The URLs of `--rand-regex-url` and `--urls-from-file` |
| `sigv4.fern` | AWS Signature Version 4 |
| `schedule.fern` | The timetable of `-q` and of bursts, and the part of each reactor |
| `report.fern` | The framing of a response, the tally, and the text, JSON and CSV outputs |
| `histogram.fern` | The latency histogram, its merge and its bins |
| `sqlite.fern` | The SQLite file of `--db-url` |
| `debug.fern` | The output of `--debug` |
| `tui.fern` | The live display |
| `load_test.fern` | The unit tests |

## Tests

- `TestSelfHostExampleLoadUnit` runs `load_test.fern` on x86-64, arm64 and
  wasm32-wasi, with a balanced leak census. It tests the options, the URL of a
  redirect, the request, the AWS signature, the bodies and the timetable of
  `-q` and of bursts. It also tests the histogram, the response ends, the
  merge of tallies, the `rps` samples and the outputs.
- `TestSelfHostExampleLoadHTTP` runs the command on x86-64 and arm64 against a
  Go `net/http` server that counts its requests and connections. It checks
  that the count of the server is the count of the tool with `-n`, `-z` and
  `-z -w`.

`TestSelfHostExampleLoadHTTP` also checks these cases:

- keep-alive, chunked bodies, a body of many reads, and a body that ends at
  the close
- an incomplete response, a refused connection, and the usage errors
- `-m`, `-H`, `-d`, `-D`, `-Z`, `-F`, `-A`, `-T`, `-a`, `--host` and
  `--disable-keepalive`
- exact `-n` and `-c` with several `--worker-threads`, and the rate of `-q`
  over four reactors
- HTTPS with `--insecure`, with `--cacert` and `-r`, and with no trusted root
- client certificates over TLS 1.3 and TLS 1.2
- HTTP/2 over TLS and in clear text, HTTP/1.0, and HTTP/3 against a
  `std/serve` server of HTTP/3
- proxies for `http` and `https`, over HTTP/1.1 and HTTP/2, with
  `--proxy-header`
- Unix sockets, VSOCK, `--rand-regex-url`, `--urls-from-file`, AWS
  signatures, CSV, `--db-url` (read back with Python `sqlite3`),
  `--stats-success-breakdown` and `--debug`
- the live display in a pseudo-terminal
- each JSON key, `-t`, `-r`, `--connect-to`, bursts, `-o`, `-u`,
  `--output-format quiet`, `-V` and `--ipv6`

On x86-64, the `oha` cases run oha 1.16.0 next to the tool. They compare
these results:

- the exit code of each usage error
- the bytes of each request, and the distribution of `--dump-urls`
- the shape of the CSV and JSON outputs, and a run with no response
- the `--debug` output, and the error of VSOCK

The cases are in
`internal/testing/e2ecompiler/self_host_example_load_test.go` and
`internal/testing/e2ecompiler/self_host_example_load_features_test.go`.

In the `rate json` case of `TestSelfHostExampleLoadHTTP`, the server stops for
50 ms on each 20th request. The tool sends at `-q 200 -c 1` with
`--latency-correction`. The test reads the JSON summary with
`encoding/json`. It checks the count of requests, and that the corrected p75
and p99 show the stops.

`TestLoadExampleX86_64` and `TestLoadExampleInterp` in
`internal/testing/e2e/load_example_test.go` run the tool with `-q` against
a `std/serve` hello, natively and under `-interp`.
