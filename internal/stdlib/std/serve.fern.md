# HTTP server execution

This module configures and runs HTTP servers.

## Model

Server configuration selects protocol support, transport options, and operational limits. A handler receives a request and host capabilities.

## Contracts

Server execution owns network and scheduler effects. TLS configuration requires an identity. Protocol upgrades and optional kernel features remain explicit configuration choices.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __recv_deadline_default(): Duration>>

<<4 pub struct Config>>

<<5 pub function config(): Config>>

<<6 function __ktls_asked(opts: Config): boolean>>

<<7 pub function (c: Config) with_io_uring(): Config>>

<<8 function __loop_driver(opts: Config): async.RealDriver>>

<<9 pub function run(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>

<<10 function __serve_listener(port: i32, opts: Config): __Listen>>

<<11 struct __Listen>>

<<12 function __listen_failed(l: __Listen): i32>>

<<13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)>>

<<14 function __every_v4(): u8[]>>

<<15 struct __Conns>>

<<16 function __pulling(c: __Conns, at: i32): Option[__Body]>>

<<17 enum __Tail>>

<<18 function __after_range(r: Reader, rest: __Spans): Option[__Tail]>>

<<19 struct __Clock>>

<<20 function __clock_tick(clk: __Clock): __Clock>>

<<21 function __file_measured(resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse>>

<<22 struct __FileBody>>

<<23 function __file_chunk(f: __FileBody, i: i32): Option[u8[]]>>

<<24 function __tail_left(c: __Conns, at: i32): i32>>

<<25 function __chunk_step(producer: ChunkProducer, index: i32, end: string, chunk: Option[u8[]]): (__Tail, u8[]...>>

<<26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, pool: fetch.Idl...>>

<<27 const SLOT_LIMIT: i32>>

<<28 function __slot_of(slots: u8[], fd: i32): i32>>

<<29 function __serve_accept[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, host_for: (Option...>>

<<30 function __serve_read_proxy[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], hea...>>

<<31 function __serve_handshook(c: __Conns, at: i32, was: boolean, done: boolean, opts: Config): (__Conns, boole...>>

<<32 enum __Behind>>

<<33 function __serve_behind(fr: http.HttpFraming, none: http.HttpFramed): (__Behind, http.HttpFramed)>>

<<34 function __serve_continue[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): __Conns>>

<<35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...>>

<<36 const H2_NOT: i32>>

<<37 function __h2_mode(c: __Conns, at: i32, opts: Config): i32>>

<<38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, opts: Config): (__...>>

<<39 function __h2_framed(c: __Conns, fd: i32): boolean>>

<<40 struct __H2Answered>>

<<41 function __h2_pending_max(): i32>>

<<42 function __h2_stream_reset(c: __Conns, fd: i32, sid: i32): __Conns>>

<<43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, t: async.Ta...>>

<<44 function __holds_request(buf: u8[]): boolean>>

<<45 enum __Sent>>

<<46 function __serve_send[D: async.Driver](drv: D, c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns,...>>

<<47 function __serve_pend[D: async.Driver](drv: D, c: __Conns, at: i32, rest: u8[], opts: Config): __Conns>>

<<48 function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Co...>>

<<49 const LISTENER_INTEREST: i32>>

<<50 function __stop_begin(stop: __Stop, sig: i32): __Stop>>

<<51 struct __Stop>>

<<52 function __stop_none(): __Stop>>

<<53 struct __Body>>

<<54 function __body_source(fd: i32, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource>>

<<55 function __pull_read(p: __Pull, raw: u8[]): u8[]>>

<<56 function __body_live(fl: __Flight): boolean>>

<<57 struct __Flight>>

<<58 function __started_again(s: __Started): __Started>>

<<59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, fd: i32, readine...>>

<<60 function __upgrading(resp: HttpResponse): boolean>>

<<61 function __flight_pair(fl: __Flight, fd: i32): i32>>

<<62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flight[], fd: i32, re...>>

<<63 function __serve_loop(listener: i32, handler: (HttpRequest, platform.Host)>>

<<64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flight[], at: i32, eof: boolean,...>>

<<65 function __serve_loop_with[S](listener: i32, init: S, handler: (S, HttpRequest, platform.Host)>>

<<66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>

<<67 function __supervise(port: i32, opts: Config, worker: (i32, Config, Option[Writer])>>

<<68 function __supervisor_fork(drv: async.RealDriver, port: i32, listener: i32, worker: (i32, Config, Option[Wr...>>

<<69 function __exporter_up(ex: Option[metrics.Exporter]): boolean>>

<<70 struct __Supervisor>>

<<71 function (s: __Supervisor) gave_up(): boolean>>

<<72 function __port_from_env(name: string, fallback: i32): i32>>

<<73 struct __Quic>>

<<74 function __h3_pending_max(): i64>>

<<75 function __h3_sent(sock: i32, out: u8[][], to: net.SocketAddr): void>>

<<76 struct __H3Req>>

<<77 function __h3_touched(b: __H3Burst, serial: i32): __H3Burst>>

<<78 function __h3_start_all[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], burst: __H3Burst, handler: (H...>>

<<79 function __h3_chunk(c: __Conns, pi: i32, chunk: Option[u8[]]): (__Conns, boolean)>>

<<80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, stop: __Stop): (__Co...>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module configures and runs HTTP servers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "std/async";
import "std/platform";
import "std/fetch";
import "std/http/trace";
import "std/http/cookie";
import "std/http/forwarded";
import "std/http";
import "std/stream";
import "std/net";
import "std/time";
import "std/metrics";
import "std/tls";
import "std/tls/server";
import "std/tls/ktls";
import "std/i64";
import "std/envflag";
import "core/int";
import "std/http/grpc";
import "std/http/h2";
import "std/http/h3";
import "std/http/h3q";
import "std/quic/conn" as quic;
import "std/quic/packet";
import "std/http/h2/frame";
```

### `3 function __recv_deadline_default(): Duration`

```fern
<<3 function __recv_deadline_default(): Duration>>=
function __recv_deadline_default(): Duration {
  return time.duration_seconds(10 as i64);
}
```

### `4 pub struct Config`

```fern
<<4 pub struct Config>>=
pub struct Config {
  backlog: i32,
  reuse_port: boolean,
  recv_deadline: Duration,
  min_data_rate: i32,
  data_rate_grace: Duration,
  response_min_data_rate: i32,
  response_data_rate_grace: Duration,
  keep_alive_idle: Duration,
  keep_alive_requests: i32,
  max_connections: i32,
  max_connections_per_ip: i32,
  max_in_flight: i32,
  workers: i32,
  shutdown_grace: Duration,
  readiness_path: string,
  drain_deadline: Duration,
  limits: http.HttpLimits,
  stop_with_parent: boolean,
  stream_bodies: boolean,
  tls: Option[server.Config],
  cookie_jar: boolean,
  http_allow: Option[fetch.Allow],
  handler_deadline: Duration,
  background_deadline: Duration,
  trace: boolean,
  proxy_protocol: boolean,
  metrics: metrics.Registry,
  metrics_port: i32,
  h2: boolean,
  io_uring: boolean,
  io_uring_zc_bytes: i32,
  ktls: boolean,
  h3: boolean,
  h3_sock: i32,
}
```

### `5 pub function config(): Config`

```fern
<<5 pub function config(): Config>>=
pub function config(): Config {
  return Config { backlog: 65535, reuse_port: false, recv_deadline: __recv_deadline_default(), min_data_rate: 240, data_rate_grace: time.duration_seconds(5 as i64), response_min_data_rate: 240, response_data_rate_grace: time.duration_seconds(5 as i64), keep_alive_idle: time.duration_seconds(130 as i64), keep_alive_requests: 1000, max_connections: 1024, max_connections_per_ip: 100, max_in_flight: 1024, workers: 0, shutdown_grace: time.duration_seconds(2 as i64), readiness_path: "", drain_deadline: time.duration_seconds(30 as i64), limits: http.http_limits(), stop_with_parent: false, stream_bodies: false, tls: None, cookie_jar: false, http_allow: None, handler_deadline: time.duration_seconds(0 as i64), background_deadline: time.duration_seconds(30 as i64), trace: false, proxy_protocol: false, metrics: metrics.registry(), metrics_port: 0, h2: false, io_uring: false, io_uring_zc_bytes: 3072, ktls: false, h3: false, h3_sock: 0 - 1 };
}

pub function (c: Config) with_tls(cfg: server.Config): Config {
  return Config { ...c, tls: Some(cfg.with_alpn(["http/1.1"])) };
}

pub function (c: Config) with_h2(): Config {
  return Config { ...c, h2: true };
}

pub function (c: Config) with_h3(): Config {
  return Config { ...c, h3: true };
}

pub function (c: Config) with_ktls(): Config {
  return Config { ...c, ktls: true };
}
```

### `6 function __ktls_asked(opts: Config): boolean`

```fern
<<6 function __ktls_asked(opts: Config): boolean>>=
function __ktls_asked(opts: Config): boolean {
  return opts.ktls || envflag.on("FERN_KTLS");
}
```

### `7 pub function (c: Config) with_io_uring(): Config`

```fern
<<7 pub function (c: Config) with_io_uring(): Config>>=
pub function (c: Config) with_io_uring(): Config {
  return Config { ...c, io_uring: true };
}
```

### `8 function __loop_driver(opts: Config): async.RealDriver`

```fern
<<8 function __loop_driver(opts: Config): async.RealDriver>>=
function __loop_driver(opts: Config): async.RealDriver {
  if (opts.io_uring || envflag.on("FERN_IO_URING")) {
    return async.real_driver_io_uring();
  }
  return async.real_driver();
}
```

### `9 pub function run(port: i32, opts: Config, handler: (HttpRequest, platform.Host)`

```fern
<<9 pub function run(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>=
pub function run(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse): i32 {
  return run_shutdown(port, opts, handler, (reason: string): void => {});
}

pub function run_shutdown(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse, shutdown: (string) => void): i32 {
  let l: __Listen = __serve_listener(port, opts);
  if (l.fd < 0) {
    return __listen_failed(l);
  }
  return __serve_loop(l.fd, handler, opts, shutdown, None);
}
```

### `10 function __serve_listener(port: i32, opts: Config): __Listen`

```fern
<<10 function __serve_listener(port: i32, opts: Config): __Listen>>=
function __serve_listener(port: i32, opts: Config): __Listen {
  let l: __Listen = __listener_of(port, opts);
  if (l.fd >= 0) {
    tcp_socket_ctl(l.fd, 3, 1);
  }
  return l;
}
```

### `11 struct __Listen`

```fern
<<11 struct __Listen>>=
struct __Listen { fd: i32, addr: string }
```

### `12 function __listen_failed(l: __Listen): i32`

```fern
<<12 function __listen_failed(l: __Listen): i32>>=
function __listen_failed(l: __Listen): i32 {
  eprint("serve: cannot listen on " + l.addr + ": " + net.error_from_host(l.fd).message());
  return 98;
}

function __listener_of(port: i32, opts: Config): __Listen {
  match (env("LISTEN_FDS")) {
    Some(n) => {
      match (i32.parse_i32_radix(n, 10)) {
        Ok(count) => {
          if (count >= 1) {
            return __Listen { fd: 3, addr: "" };
          }
        },
        Err(_) => {}
      }
    },
    None => {}
  }
  if (target_os() != "wasi") {
    let both: i32 = tcp_listen_with(__every_v6(), port, opts.backlog, opts.reuse_port);
    if (both >= 0 || !net.error_from_host(both).ipv6_unavailable()) {
      return __Listen { fd: both, addr: net.socket_addr(net.ipv6_unspecified(), port).to_string() };
    }
  }
  return __Listen { fd: tcp_listen_with(__every_v4(), port, opts.backlog, opts.reuse_port), addr: net.socket_addr(net.ipv4_unspecified(), port).to_string() };
}
```

### `13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)`

```fern
<<13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)>>=
pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse)): i32 {
  return run_with_shutdown(port, opts, init, handler, (reason: string, state: S): void => {});
}

pub function run_with_shutdown[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), shutdown: (string, S) => void): i32 {
  let l: __Listen = __serve_listener(port, opts);
  if (l.fd < 0) {
    return __listen_failed(l);
  }
  return __serve_loop_with(l.fd, init, handler, opts, shutdown, None);
}
```

### `14 function __every_v4(): u8[]`

```fern
<<14 function __every_v4(): u8[]>>=
function __every_v4(): u8[] {
  return [0u8, 0u8, 0u8, 0u8];
}

function __every_v6(): u8[] {
  return [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
}

function __eagain(): i32 {
  if (target_os() == "darwin") {
    return 35;
  }
  if (target_os() == "wasi") {
    return 6;
  }
  return 11;
}

function __enotsup(): i32 {
  if (target_os() == "darwin") {
    return 45;
  }
  if (target_os() == "wasi") {
    return 58;
  }
  return 95;
}

function __recv_room(): i32 {
  return 4096;
}

function __recv_scratch(): u8[] {
  return __alloc_u8(__recv_room());
}
```

### `15 struct __Conns`

```fern
<<15 struct __Conns>>=
struct __Conns { open: i32, free: i32, fds: i32[], slots: u8[], bufs: u8[][], offs: i32[], pending: u8[][], deadlines: i64[], due: i64, keep: boolean[], served: i32[], backlog: boolean[], queued: i32[], continued: boolean[], rate_since: i64[], rate_base: i32[], send_since: i64[], send_base: i32[], peers: i32[], hosts: platform.Host[], tails: __Tail[], tls: __Tls[], framing: __Framing[], last: http.HttpFramed, out: usize, lent: i32, parked: __Flight[], later: (() => void)[], metrics: metrics.ServerMetrics, pulls: __H2Pull[], producers: __H2Producer[], tls_reads: i32, zc_bytes: i32, zc_held: u8[][], zc_slots: i32[], zc_free: boolean[], quic: __Quic, date: string }

enum __Tls { Plain, Secured(server.Session), Lent, Awaiting(Option[server.Session], u8[]), InKernel(server.Session) }

enum __Framing { Http1, Pulling(__Body), Http2(h2.Conn) }
```

### `16 function __pulling(c: __Conns, at: i32): Option[__Body]`

```fern
<<16 function __pulling(c: __Conns, at: i32): Option[__Body]>>=
function __pulling(c: __Conns, at: i32): Option[__Body] {
  match (c.framing[at]) {
    Pulling(body) => {
      return Some(body);
    },
    _ => {
      return None;
    }
  }
  return None;
}
```

### `17 enum __Tail`

```fern
<<17 enum __Tail>>=
enum __Tail { NoTail, FileTail(Reader, i64, __Spans), SeamTail(Reader, u8[], __Spans), ChunkTail(ChunkProducer, i32, string), ParkedTail(ChunkProducer, i32, string), ZcTail(u8[], i32, i32) }

struct __Spans { ranges: i64[], seams: u8[][] }
```

### `18 function __after_range(r: Reader, rest: __Spans): Option[__Tail]`

```fern
<<18 function __after_range(r: Reader, rest: __Spans): Option[__Tail]>>=
function __after_range(r: Reader, rest: __Spans): Option[__Tail] {
  let next: __Spans = __Spans { ranges: rest.ranges, seams: __from(rest.seams, 1) };
  if (rest.seams.len() > 0 && rest.seams[0].len() > 0) {
    return Some(SeamTail(r, rest.seams[0], next));
  }
  return __range_tail(r, next);
}

function __range_tail(r: Reader, sp: __Spans): Option[__Tail] {
  if (sp.ranges.len() < 2) {
    r.close();
    return Some(NoTail);
  }
  let rest: __Spans = __Spans { ranges: __from(sp.ranges, 2), seams: sp.seams };
  if (sp.ranges[1] == 0 as i64) {
    return __after_range(r, rest);
  }
  match (r.seek(sp.ranges[0], 0)) {
    Ok(at) => {
      return Some(FileTail(r, sp.ranges[1], rest));
    },
    Err(e) => {
      r.close();
      return None;
    }
  }
  return None;
}

function __from[T](xs: T[], from: i32): T[] {
  let out: T[] = [];
  let i: i32 = from;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

function __tail_keeps(t: __Tail): boolean {
  match (t) {
    ChunkTail(_, _, end) => {
      return end.len() > 0;
    },
    ParkedTail(_, _, end) => {
      return end.len() > 0;
    },
    _ => {}
  }
  return true;
}
```

### `19 struct __Clock`

```fern
<<19 struct __Clock>>=
struct __Clock { sec: i64, date: string, line: string }
```

### `20 function __clock_tick(clk: __Clock): __Clock`

```fern
<<20 function __clock_tick(clk: __Clock): __Clock>>=
function __clock_tick(clk: __Clock): __Clock {
  let sec: i64 = now_unix_ms() / 1000;
  if (sec == clk.sec && clk.line.len() > 0) {
    return clk;
  }
  let date: string = time.instant_from_unix(sec).format_http_date();
  return __Clock { sec: sec, date: date, line: "Date: " + date + "\r\n" };
}

function __serve_wire(out: usize, method: string, version: string, answer: HttpResponse, keep_alive: boolean, clk: __Clock): __Tail {
  if (!http.http_fields_ok(answer.headers) || !http.http_fields_ok(answer.trailers)) {
    return __serve_wire(out, method, version, http.internal_error(""), keep_alive, clk);
  }
  let date: string = clk.line;
  if (answer.headers.has("date")) {
    date = "";
  }
  let headless: boolean = http.http_bodiless(answer.status) || method == "HEAD";
  match (answer.body) {
    BodyFile(path) => {
      let whole: i64[] = [];
      let none: u8[] = [];
      return __wire_file(out, method, version, answer, path, __Spans { ranges: whole, seams: [none, none] }, headless, keep_alive, clk, date);
    },
    BodyFileRanges(path, ranges, seams) => {
      if (ranges.len() == 0 || !http.file_ranges_ok(ranges, seams)) {
        return __serve_wire(out, method, version, http.internal_error(""), keep_alive, clk);
      }
      return __wire_file(out, method, version, answer, path, __Spans { ranges: ranges, seams: seams }, headless, keep_alive, clk, date);
    },
    BodyChunks(producer) => {
      if (answer.status == 101) {
        http.http_serialize_response_head_into(out, version, answer, false, date);
        return ChunkTail(producer, 0, "");
      }
      return __wire_chunks(out, version, answer, producer, headless, keep_alive, date);
    },
    _ => {
      http.http_serialize_response_into(out, method, version, answer, keep_alive, date);
      return NoTail;
    }
  }
  return NoTail;
}

function __wire_file(out: usize, method: string, version: string, resp: HttpResponse, path: string, body: __Spans, headless: boolean, keep_alive: boolean, clk: __Clock, date: string): __Tail {
  match (open_reader(path)) {
    Ok(r) => {
      let ranges: i64[] = [];
      match (r.stat()) {
        Ok(st) => {
          ranges = __ranges_held(st.size, body.ranges);
        },
        Err(e) => {}
      }
      if (ranges.len() == 0) {
        r.close();
        return __serve_wire(out, method, version, http.not_found(), keep_alive, clk);
      }
      let framing: string = date + "Content-Length: " + http.file_ranges_len(ranges, body.seams).to_string() + "\r\n";
      if (headless) {
        r.close();
        http.http_serialize_response_head_into(out, version, resp, keep_alive, framing);
        return NoTail;
      }
      match (__range_tail(r, __Spans { ranges: ranges, seams: __from(body.seams, 1) })) {
        Some(t) => {
          http.http_serialize_response_head_into(out, version, resp, keep_alive, framing);
          buf_push_bytes_range(out, body.seams[0], 0, body.seams[0].len());
          return t;
        },
        None => {
          return __serve_wire(out, method, version, http.not_found(), keep_alive, clk);
        }
      }
    },
    Err(e) => {
      return __serve_wire(out, method, version, http.not_found(), keep_alive, clk);
    }
  }
  return NoTail;
}

function __ranges_held(size: i64, ranges: i64[]): i64[] {
  let none: i64[] = [];
  if (ranges.len() == 0) {
    return [0 as i64, size];
  }
  let i: i32 = 0;
  while (i + 1 < ranges.len()) {
    if (ranges[i] + ranges[i + 1] > size) {
      return none;
    }
    i = i + 2;
  }
  return ranges;
}

function __file_chunks(resp: HttpResponse): HttpResponse {
  let none: u8[] = [];
  let whole: i64[] = [];
  match (resp.body) {
    BodyFile(path) => {
      return __file_measured(resp, path, whole, [none, none]);
    },
    BodyFileRanges(path, ranges, seams) => {
      if (ranges.len() == 0 || !http.file_ranges_ok(ranges, seams)) {
        return http.internal_error("");
      }
      return __file_measured(resp, path, ranges, seams);
    },
    _ => {
      return resp;
    }
  }
  return resp;
}
```

### `21 function __file_measured(resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse`

```fern
<<21 function __file_measured(resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse>>=
function __file_measured(resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse {
  match (__file_stat(path)) {
    Some(st) => {
      let held: i64[] = __ranges_held(st.size, ranges);
      if (held.len() == 0) {
        return http.not_found();
      }
      let f: __FileBody = __FileBody { path: path, id: st, ranges: held, seams: seams };
      let fields: HeaderMap = resp.headers;
      if (!http.http_bodiless(resp.status)) {
        fields = fields.set("content-length", http.file_ranges_len(held, seams).to_string());
      }
      let none: u8[] = [];
      let producer: ChunkProducer = ChunkProducer { next: (i: i32): Option[u8[]] => __file_chunk(f, i), idle_ms: 0, filler: none, trailers: (): HeaderMap => HeaderMap { names: [], values: [] } };
      return HttpResponse { ...resp, body: BodyChunks(producer), headers: fields };
    },
    None => {
      return http.not_found();
    }
  }
  return http.not_found();
}

function __file_stat(path: string): Option[FileStat] {
  match (open_reader(path)) {
    Ok(r) => {
      let measured: Result[FileStat, IoError] = r.stat();
      r.close();
      match (measured) {
        Ok(st) => {
          return Some(st);
        },
        Err(e) => {
          return None;
        }
      }
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}
```

### `22 struct __FileBody`

```fern
<<22 struct __FileBody>>=
struct __FileBody { path: string, id: FileStat, ranges: i64[], seams: u8[][] }
```

### `23 function __file_chunk(f: __FileBody, i: i32): Option[u8[]]`

```fern
<<23 function __file_chunk(f: __FileBody, i: i32): Option[u8[]]>>=
function __file_chunk(f: __FileBody, i: i32): Option[u8[]] {
  let piece: i64 = 65536 as i64;
  let at: i64 = i as i64;
  let k: i32 = 0;
  while (true) {
    if (f.seams[k].len() > 0) {
      if (at == 0 as i64) {
        return Some(f.seams[k]);
      }
      at = at - 1 as i64;
    }
    if (2 * k >= f.ranges.len()) {
      return None;
    }
    let len: i64 = f.ranges[2 * k + 1];
    let pieces: i64 = (len + piece - 1 as i64) / piece;
    if (at < pieces) {
      let from: i64 = at * piece;
      let want: i64 = len - from;
      if (want > piece) {
        want = piece;
      }
      return __file_read(f, f.ranges[2 * k] + from, want as i32);
    }
    at = at - pieces;
    k = k + 1;
  }
  return None;
}

function __file_read(f: __FileBody, offset: i64, n: i32): Option[u8[]] {
  match (open_reader(f.path)) {
    Ok(r) => {
      let got: Option[u8[]] = __read_from(r, f.id, offset, n);
      r.close();
      return got;
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}

function __read_from(r: Reader, id: FileStat, offset: i64, n: i32): Option[u8[]] {
  if (!__same_file(r, id)) {
    return None;
  }
  match (r.seek(offset, 0)) {
    Ok(at) => {
      return __read_n(r, n);
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}

function __same_file(r: Reader, id: FileStat): boolean {
  match (r.stat()) {
    Ok(st) => {
      return st.dev == id.dev && st.ino == id.ino && st.size == id.size && st.mtime == id.mtime && st.mtime_nsec == id.mtime_nsec;
    },
    Err(e) => {
      return false;
    }
  }
  return false;
}

function __read_n(r: Reader, n: i32): Option[u8[]] {
  let out: u8[] = [];
  while (out.len() < n) {
    match (r.read_chunk_bytes(n - out.len())) {
      Ok(bytes) => {
        if (bytes.len() == 0) {
          return None;
        }
        out = __joined(out, bytes);
      },
      Err(e) => {
        return None;
      }
    }
  }
  return Some(out);
}

function __wire_chunks(out: usize, version: string, resp: HttpResponse, producer: ChunkProducer, headless: boolean, keep_alive: boolean, date: string): __Tail {
  if (headless) {
    http.http_serialize_response_head_into(out, version, resp, keep_alive, date);
    return NoTail;
  }
  if (version == "HTTP/1.0") {
    http.http_serialize_response_head_into(out, version, resp, false, date);
    return ChunkTail(producer, 0, "");
  }
  let framing: string = date + "Transfer-Encoding: chunked\r\n";
  if (resp.trailers.names.len() > 0) {
    framing = framing + "Trailer: " + resp.trailers.names.join(", ") + "\r\n";
  }
  http.http_serialize_response_head_into(out, version, resp, keep_alive, framing);
  return ChunkTail(producer, 0, http.http_serialize_fields(resp.trailers) + "\r\n");
}

function __in_flight(c: __Conns, at: i32): boolean {
  if (c.pending[at].len() > 0) {
    return true;
  }
  match (c.tails[at]) {
    NoTail => {
      return false;
    },
    _ => {
      return true;
    }
  }
  return true;
}
```

### `24 function __tail_left(c: __Conns, at: i32): i32`

```fern
<<24 function __tail_left(c: __Conns, at: i32): i32>>=
function __tail_left(c: __Conns, at: i32): i32 {
  let left: i64 = 0 as i64;
  match (c.tails[at]) {
    FileTail(r, n, rest) => {
      left = n + http.file_ranges_len(rest.ranges, rest.seams);
    },
    SeamTail(r, seam, rest) => {
      left = seam.len() as i64 + http.file_ranges_len(rest.ranges, rest.seams);
    },
    ZcTail(body, from, _) => {
      return body.len() - from;
    },
    _ => {
      return 0;
    }
  }
  if (left > 2147483647 as i64) {
    return 2147483647;
  }
  return left as i32;
}

function __tail_drop(c: __Conns, at: i32): __Conns {
  match (c.tails[at]) {
    FileTail(r, left, rest) => {
      r.close();
    },
    SeamTail(r, seam, rest) => {
      r.close();
    },
    ZcTail(_, _, slot) => {
      c = __zc_settled(c, slot);
    },
    _ => {}
  }
  return __Conns { ...c, tails: c.tails.with(at, NoTail) };
}

function __tail_piece[D: async.Driver](drv: D, c: __Conns, at: i32): (__Conns, u8[], boolean) {
  let none: u8[] = [];
  match (c.tails[at]) {
    NoTail => {
      return (c, none, true);
    },
    ParkedTail(_, _, _) => {
      return (c, none, true);
    },
    FileTail(r, left, after) => {
      return __file_piece(c, at, r, left, after);
    },
    SeamTail(r, seam, after) => {
      return __tail_then(c, at, __range_tail(r, after), seam);
    },
    ZcTail(body, from, _) => {
      return (__Conns { ...c, tails: c.tails.with(at, NoTail) }, __bytes_from(body, from), true);
    },
    ChunkTail(producer, index, end) => {
      return __chunk_piece(drv, c, at, producer, index, end);
    }
  }
  return (c, none, true);
}

function __file_piece(c: __Conns, at: i32, r: Reader, left: i64, after: __Spans): (__Conns, u8[], boolean) {
  let none: u8[] = [];
  let want: i32 = 65536;
  if (left < want as i64) {
    want = left as i32;
  }
  match (r.read_chunk_bytes(want)) {
    Ok(bytes) => {
      if (bytes.len() == 0) {
        return (__tail_drop(c, at), none, false);
      }
      let rest: i64 = left - bytes.len() as i64;
      if (rest <= 0 as i64) {
        return __tail_then(c, at, __after_range(r, after), bytes);
      }
      return (__Conns { ...c, tails: c.tails.with(at, FileTail(r, rest, after)) }, bytes, true);
    },
    Err(e) => {
      return (__tail_drop(c, at), none, false);
    }
  }
  return (c, none, false);
}

function __tail_then(c: __Conns, at: i32, next: Option[__Tail], piece: u8[]): (__Conns, u8[], boolean) {
  match (next) {
    Some(t) => {
      return (__Conns { ...c, tails: c.tails.with(at, t) }, piece, true);
    },
    None => {
      return (__Conns { ...c, tails: c.tails.with(at, NoTail) }, piece, false);
    }
  }
  return (c, piece, false);
}

function __chunk_piece[D: async.Driver](drv: D, c: __Conns, at: i32, producer: ChunkProducer, index: i32, end: string): (__Conns, u8[], boolean) {
  let none: u8[] = [];
  let next: (i32) => Option[u8[]] = producer.next;
  let i: i32 = index;
  while (true) {
    let t: async.Task[Option[u8[]]] = async.task_new((): Option[u8[]] => next(i));
    match (async.task_start(t)) {
      Done(chunk) => {
        async.task_free(t);
        let step: (__Tail, u8[], boolean) = __chunk_step(producer, i, end, chunk);
        if (step.2) {
          return (__Conns { ...c, tails: c.tails.with(at, step.0) }, step.1, true);
        }
      },
      Suspended(w) => {
        return (__tail_park(drv, c, at, producer, i, end, t, w), none, true);
      },
      Cancelled => {
        async.task_free(t);
        return (__tail_drop(c, at), none, false);
      }
    }
    i = i + 1;
  }
  return (c, none, true);
}
```

### `25 function __chunk_step(producer: ChunkProducer, index: i32, end: string, chunk: Option[u8[]]): (__Tail, u8[]...`

```fern
<<25 function __chunk_step(producer: ChunkProducer, index: i32, end: string, chunk: Option[u8[]]): (__Tail, u8[]...>>=
function __chunk_step(producer: ChunkProducer, index: i32, end: string, chunk: Option[u8[]]): (__Tail, u8[], boolean) {
  match (chunk) {
    Some(bytes) => {
      let tail: __Tail = ChunkTail(producer, index + 1, end);
      if (bytes.len() == 0) {
        return (tail, bytes, false);
      }
      return (tail, __chunk_framed(bytes, end), true);
    },
    None => {
      return (NoTail, __chunk_end(producer, end), true);
    }
  }
  return (NoTail, __chunk_end(producer, end), true);
}

function __chunk_end(producer: ChunkProducer, end: string): u8[] {
  if (end.len() == 0) {
    return [];
  }
  return ("0\r\n" + http.http_serialize_fields(producer.trailers()) + end).bytes();
}

function __chunk_framed(bytes: u8[], end: string): u8[] {
  if (end.len() == 0) {
    return bytes;
  }
  return (int.int_to_string_radix(bytes.len(), 16) + "\r\n").bytes().concat(bytes).concat("\r\n".bytes());
}

function __idle_due(producer: ChunkProducer, now: i64): i64 {
  if (producer.idle_ms <= 0 || producer.filler.len() == 0) {
    return 0 - 1 as i64;
  }
  return now + producer.idle_ms as i64 * 1000000;
}

function __flight_filler[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, opts: Config): (__Conns, __Flight[]) {
  let fl: __Flight = fls[fi];
  let at: i32 = __conns_index(c, fl.fd);
  if (at < 0) {
    return (c, fls.with(fi, __Flight { ...fl, idle: 0 - 1 as i64 }));
  }
  let piece: u8[] = [];
  match (c.tails[at]) {
    ParkedTail(producer, _, end) => {
      piece = __chunk_framed(producer.filler, end);
      fls = fls.with(fi, __Flight { ...fl, idle: __idle_due(producer, monotonic_ns()) });
    },
    _ => {
      return (c, fls.with(fi, __Flight { ...fl, idle: 0 - 1 as i64 }));
    }
  }
  if (c.pending[at].len() > 0) {
    return (c, fls);
  }
  let sent: (__Conns, __Sent) = __serve_send(drv, c, at, piece, opts);
  match (sent.1) {
    Failed => {
      __flight_cancel(drv, fls[fi]);
      return (__serve_close(drv, sent.0, at), __flights_remove(fls, fi));
    },
    _ => {
      return (sent.0, fls);
    }
  }
  return (sent.0, fls);
}

function __tail_park[D: async.Driver](drv: D, c: __Conns, at: i32, producer: ChunkProducer, index: i32, end: string, t: async.Task[Option[u8[]]], w: async.Wait): __Conns {
  let none: i32[] = [];
  let now: i64 = monotonic_ns();
  let fl: __Flight = __flight_watch(drv, __Flight { job: Producer(t), wait: w, fd: c.fds[at], set: none, due: 0 - 1 as i64, method: "", version: "", kept: false, behind: Idle, body: __pulling(c, at), idle: __idle_due(producer, now), ends: 0 - 1 as i64, stream: 0, since: 0 - 1 as i64, grpc: false, quic: 0 }, now);
  match (c.framing[at]) {
    Pulling(b) => {},
    _ => {
      drv.watch(c.fds[at], __read_interest(c, c.tls[at]));
    }
  }
  return __Conns { ...c, tails: c.tails.with(at, ParkedTail(producer, index, end)), deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()), parked: c.parked.append(fl) };
}

function __producer_done[D: async.Driver](drv: D, c: __Conns, fd: i32, chunk: Option[u8[]], opts: Config, clk: __Clock): __Conns {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  let piece: u8[] = [];
  match (c.tails[at]) {
    ParkedTail(producer, index, end) => {
      let step: (__Tail, u8[], boolean) = __chunk_step(producer, index, end, chunk);
      c = __Conns { ...c, tails: c.tails.with(at, step.0) };
      piece = step.1;
    },
    _ => {
      return c;
    }
  }
  if (piece.len() > 0) {
    let sent: (__Conns, __Sent) = __serve_send(drv, c, at, piece, opts);
    c = sent.0;
    match (sent.1) {
      Partial => {
        return c;
      },
      Failed => {
        return __serve_close(drv, c, at);
      },
      Whole => {}
    }
  }
  let drained: (__Conns, i32) = __serve_drain(drv, c, at, opts, clk);
  c = drained.0;
  if (drained.1 >= 0) {
    return __backlog_on(c, drained.1);
  }
  return c;
}
```

### `26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, pool: fetch.Idl...`

```fern
<<26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, pool: fetch.Idl...>>=
function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, pool: fetch.Idle): (__Conns, __Flight[]) {
  let taken: (__Conns, __Flight[]) = __parked_taken(c, fls);
  c = taken.0;
  fls = taken.1;
  if (__upstream_busy(pool)) {
    let upstreams: (__Conns, __Flight[]) = __shared_turn(drv, pool, c, fls, opts);
    c = upstreams.0;
    fls = upstreams.1;
  }
  if (c.later.len() == 0) {
    return (c, fls);
  }
  let work: (() => void)[] = c.later;
  let none: (() => void)[] = [];
  c = __Conns { ...c, later: none };
  for w in work {
    fls = __background_start(drv, fls, w, opts);
  }
  return (c, fls);
}

function __background_start[D: async.Driver](drv: D, fls: __Flight[], work: () => void, opts: Config): __Flight[] {
  let t: async.Task[i32] = async.task_new((): i32 => __background_run(work));
  match (async.task_start(t)) {
    Suspended(w) => {
      let none: i32[] = [];
      let now: i64 = monotonic_ns();
      return fls.append(__flight_watch(drv, __Flight { job: Background(t), wait: w, fd: 0 - 1, set: none, due: 0 - 1 as i64, method: "", version: "", kept: false, behind: Idle, body: None, idle: 0 - 1 as i64, ends: __bound_ends(opts.background_deadline, now), stream: 0, since: 0 - 1 as i64, grpc: false, quic: 0 }, now));
    },
    _ => {
      async.task_free(t);
    }
  }
  return fls;
}

function __background_run(work: () => void): i32 {
  work();
  return 0;
}

function __serve_count(c: __Conns, at: i32, method: string, status: i32): __Conns {
  return __serve_count_from(c, method, status, c.rate_since[at]);
}

function __serve_count_from(c: __Conns, method: string, status: i32, since: i64): __Conns {
  let m: metrics.ServerMetrics = c.metrics;
  let grown: metrics.ServerMetrics = m.request(method, status, monotonic_ns() - since);
  if (grown.methods.len() == m.methods.len()) {
    return c;
  }
  return __Conns { ...c, metrics: grown };
}

function __stage_background(c: __Conns, resp: HttpResponse): __Conns {
  match (resp.background) {
    Some(work) => {
      return __Conns { ...c, later: c.later.append(work) };
    },
    None => {
      return c;
    }
  }
  return c;
}

function __parked_taken(c: __Conns, fls: __Flight[]): (__Conns, __Flight[]) {
  if (c.parked.len() == 0) {
    return (c, fls);
  }
  let none: __Flight[] = [];
  return (__Conns { ...c, parked: none }, fls.concat(c.parked));
}

function __conns_index(c: __Conns, fd: i32): i32 {
  if (fd >= 0 && fd < SLOT_LIMIT) {
    let v: i32 = __slot_of(c.slots, fd);
    if (v != SLOT_FULL) {
      return v - 1;
    }
  }
  let i: i32 = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] == fd) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}
```

### `27 const SLOT_LIMIT: i32`

```fern
<<27 const SLOT_LIMIT: i32>>=
const SLOT_LIMIT: i32 = 65536;

const SLOT_FULL: i32 = 65535;
```

### `28 function __slot_of(slots: u8[], fd: i32): i32`

```fern
<<28 function __slot_of(slots: u8[], fd: i32): i32>>=
function __slot_of(slots: u8[], fd: i32): i32 {
  let at: i32 = fd * 2;
  if (at + 1 >= slots.len()) {
    return 0;
  }
  return slots[at] as i32 | slots[at + 1] as i32 << 8;
}

function __slot_set(slots: u8[], fd: i32, v: i32): u8[] {
  if (fd < 0 || fd >= SLOT_LIMIT) {
    return slots;
  }
  let at: i32 = fd * 2;
  while (slots.len() <= at + 1) {
    slots = slots.append(0);
  }
  let w: i32 = v;
  if (w > SLOT_FULL) {
    w = SLOT_FULL;
  }
  return slots.with(at, (w & 255) as u8).with(at + 1, (w >> 8) as u8);
}

function __conns_add(c: __Conns, fd: i32, deadline: i64, peer: i32, host: platform.Host, tls: __Tls): __Conns {
  let empty: u8[] = [];
  let keep: boolean = false;
  match (tls) {
    Secured(_) => {
      keep = true;
    },
    Awaiting(next, _) => {
      match (next) {
        Some(_) => {
          keep = true;
        },
        None => {}
      }
    },
    _ => {}
  }
  let k: i32 = c.free;
  if (k >= 0) {
    let next: i32 = c.offs[k];
    return __Conns { ...c, open: c.open + 1, free: next, fds: c.fds.with(k, fd), slots: __slot_set(c.slots, fd, k + 1), bufs: c.bufs.with(k, empty), offs: c.offs.with(k, 0), pending: c.pending.with(k, empty), deadlines: c.deadlines.with(k, deadline), due: __sooner(c.due, deadline), keep: c.keep.with(k, keep), served: c.served.with(k, 0), backlog: c.backlog.with(k, false), continued: c.continued.with(k, false), rate_since: c.rate_since.with(k, 0 - 1 as i64), rate_base: c.rate_base.with(k, 0), send_since: c.send_since.with(k, 0 - 1 as i64), send_base: c.send_base.with(k, 0), peers: c.peers.with(k, peer), hosts: c.hosts.with(k, host), tails: c.tails.with(k, NoTail), tls: c.tls.with(k, tls), framing: c.framing.with(k, Http1) };
  }
  return __Conns { open: c.open + 1, free: c.free, fds: c.fds.append(fd), slots: __slot_set(c.slots, fd, c.fds.len() + 1), bufs: c.bufs.append(empty), offs: c.offs.append(0), pending: c.pending.append(empty), deadlines: c.deadlines.append(deadline), due: __sooner(c.due, deadline), keep: c.keep.append(keep), served: c.served.append(0), backlog: c.backlog.append(false), queued: c.queued, continued: c.continued.append(false), rate_since: c.rate_since.append(0 - 1 as i64), rate_base: c.rate_base.append(0), send_since: c.send_since.append(0 - 1 as i64), send_base: c.send_base.append(0), peers: c.peers.append(peer), hosts: c.hosts.append(host), tails: c.tails.append(NoTail), tls: c.tls.append(tls), framing: c.framing.append(Http1), last: c.last, out: c.out, lent: c.lent, parked: c.parked, later: c.later, metrics: c.metrics, pulls: c.pulls, producers: c.producers, tls_reads: c.tls_reads, zc_bytes: c.zc_bytes, zc_held: c.zc_held, zc_slots: c.zc_slots, zc_free: c.zc_free, quic: c.quic, date: c.date };
}

function __conns_from_peer(c: __Conns, peer: i32): i32 {
  let n: i32 = 0;
  let i: i32 = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] >= 0 && c.peers[i] == peer) {
      n = n + 1;
    }
    i = i + 1;
  }
  return n;
}

function __conns_remove(c: __Conns, at: i32): __Conns {
  let fd: i32 = c.fds[at];
  let was: boolean = c.backlog[at];
  let next: i32 = c.free;
  let lent: i32 = c.lent;
  if (lent == at) {
    lent = 0 - 1;
  }
  let empty: u8[] = [];
  let r: __Conns = __Conns { ...c, open: c.open - 1, free: at, fds: c.fds.with(at, 0 - 1), slots: __slot_set(c.slots, fd, 0), bufs: c.bufs.with(at, empty), offs: c.offs.with(at, next), pending: c.pending.with(at, empty), backlog: c.backlog.with(at, false), tails: c.tails.with(at, NoTail), tls: c.tls.with(at, Plain), framing: c.framing.with(at, Http1), lent: lent };
  return __Conns { ...r, queued: __unqueued(r.queued, fd, was), pulls: __pulls_without(r.pulls, fd), producers: __producers_without(r.producers, fd) };
}

function __conns_timeout(c: __Conns, now: i64): i32 {
  if (c.queued.len() > 0) {
    return 0;
  }
  let best: i64 = c.due;
  if (best < 0 as i64) {
    return -1;
  }
  if (best <= now) {
    return 0;
  }
  let ms: i64 = (best - now + 999999 as i64) / 1000000;
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  return ms as i32;
}

function __backlog_on(c: __Conns, at: i32): __Conns {
  if (c.backlog[at]) {
    return c;
  }
  let fd: i32 = c.fds[at];
  return __Conns { ...c, backlog: c.backlog.with(at, true), queued: c.queued.append(fd) };
}

function __unqueued(queued: i32[], fd: i32, was: boolean): i32[] {
  if (!was) {
    return queued;
  }
  let out: i32[] = [];
  for q in queued {
    if (q != fd) {
      out = out.append(q);
    }
  }
  return out;
}

function __backlog_due(c: __Conns, at: i32): i64 {
  if (!c.backlog[at]) {
    return c.due;
  }
  return __sooner(c.due, c.deadlines[at]);
}
```

### `29 function __serve_accept[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, host_for: (Option...`

```fern
<<29 function __serve_accept[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, host_for: (Option...>>=
function __serve_accept[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, host_for: (Option[net.SocketAddr]) => platform.Host): __Conns {
  let fd: i32 = tcp_accept(listener);
  if (fd < 0) {
    return c;
  }
  let peer: i32 = 0;
  if (opts.max_connections_per_ip > 0) {
    peer = tcp_socket_ctl(fd, 7, 0);
    if (peer != 0 && __conns_from_peer(c, peer) >= opts.max_connections_per_ip) {
      tcp_close(fd);
      return c;
    }
  }
  tcp_socket_ctl(fd, 3, 1);
  tcp_socket_ctl(fd, 1, 1);
  let tls: __Tls = Plain;
  match (__tls_session(opts)) {
    Ok(t) => {
      tls = t;
    },
    Err(_) => {
      tcp_close(fd);
      return c;
    }
  }
  if (opts.proxy_protocol) {
    tls = __awaiting(tls);
  }
  if (drv.watch(fd, __read_interest(c, tls)) < 0) {
    tcp_close(fd);
    return c;
  }
  c.metrics.accepted.inc();
  c.metrics.open.inc();
  return __conns_add(c, fd, monotonic_ns() + opts.recv_deadline.as_nanos(), peer, host_for(__peer_of(fd)), tls);
}

function __peer_of(fd: i32): Option[net.SocketAddr] {
  match (net.peer_addr(fd)) {
    Ok(a) => {
      return Some(a);
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}

function __tls_session(opts: Config): Result[__Tls, tls.TlsError] {
  match (opts.tls) {
    Some(cfg) => {
      let offered: server.Config = cfg;
      if (opts.h2) {
        offered = cfg.with_alpn(["h2", "http/1.1"]);
      }
      let sess: server.Session = server.start(offered, random_bytes(server.entropy(offered)))?;
      return Ok(Secured(sess));
    },
    None => {
      return Ok(Plain);
    }
  }
  return Ok(Plain);
}

function __serve_close[D: async.Driver](drv: D, c: __Conns, at: i32): __Conns {
  drv.unwatch(c.fds[at]);
  c.metrics.open.dec();
  match (c.tls[at]) {
    Secured(sess) => {
      if let Ok(step) = sess.close() {
        tcp_send_bytes(c.fds[at], step.output);
      }
    },
    InKernel(sess) => {
      if let Ok(step) = sess.close() {
        ktls.send_records(c.fds[at], step.output);
      }
    },
    Lent => {
      __lent_closed(c, at);
    },
    _ => {}
  }
  tcp_close(c.fds[at]);
  return __conns_remove(__tail_drop(c, at), at);
}

function __serve_read[D: async.Driver](drv: D, c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
  match (c.tls[at]) {
    Secured(_) => {
      return __serve_read_tls(drv, c, at, scratch, opts);
    },
    Awaiting(next, head) => {
      return __serve_read_proxy(drv, c, at, next, head, scratch, opts);
    },
    _ => {
      return __serve_read_plain(drv, c, at, scratch, opts);
    }
  }
  return __serve_read_plain(drv, c, at, scratch, opts);
}

function __awaiting(t: __Tls): __Tls {
  let none: u8[] = [];
  match (t) {
    Secured(s) => {
      return Awaiting(Some(s), none);
    },
    _ => {
      return Awaiting(None, none);
    }
  }
  return Awaiting(None, none);
}
```

### `30 function __serve_read_proxy[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], hea...`

```fern
<<30 function __serve_read_proxy[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], hea...>>=
function __serve_read_proxy[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], head: u8[], scratch: u8[], opts: Config): (__Conns, boolean) {
  let deadline: i64 = c.deadlines[at];
  let read: (__Conns, boolean) = __serve_read_plain(drv, c, at, scratch, opts);
  let bytes: u8[] = head.concat(read.0.bufs[at]);
  let empty: u8[] = [];
  let lent: i32 = read.0.lent;
  if (lent == at) {
    lent = 0 - 1;
  }
  c = __Conns { ...read.0, bufs: read.0.bufs.with(at, empty), lent: lent, deadlines: read.0.deadlines.with(at, deadline), due: __sooner(read.0.due, deadline) };
  match (forwarded.proxy_header(bytes, 0)) {
    HeaderRead(src, n) => {
      return __proxied(drv, c, at, next, src, __bytes_from(bytes, n), opts, read.1);
    },
    HeaderShort => {
      return (__Conns { ...c, tls: c.tls.with(at, Awaiting(next, bytes)) }, read.1 || bytes.len() > 65551);
    },
    HeaderBad => {
      return (c, true);
    }
  }
  return (c, true);
}

function __proxied[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], src: Option[net.SocketAddr], rest: u8[], opts: Config, done: boolean): (__Conns, boolean) {
  match (src) {
    Some(a) => {
      c = __Conns { ...c, hosts: c.hosts.with(at, c.hosts[at].with_peer(src)) };
    },
    None => {}
  }
  match (next) {
    Some(sess) => {
      let took: (__Conns, __Tls, u8[], boolean) = __tls_taken(drv, c, at, Secured(sess), rest[0:rest.len()], opts);
      return (__Conns { ...took.0, bufs: took.0.bufs.with(at, took.2), tls: took.0.tls.with(at, took.1) }, done || took.3);
    },
    None => {
      return (__Conns { ...c, bufs: c.bufs.with(at, rest), tls: c.tls.with(at, Plain) }, done);
    }
  }
  return (c, done);
}

function __serve_read_plain[D: async.Driver](drv: D, c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
  let buf: u8[] = c.bufs[at];
  let was_idle: boolean = buf.len() == 0;
  let lent: i32 = c.lent;
  let done: boolean = false;
  let more: boolean = true;
  while (more) {
    if (lent == at) {
      buf = __bytes_from(buf, 0);
      lent = 0 - 1;
    }
    __arr_set_len(scratch, __recv_room());
    let n: i32 = drv.recv_into(c.fds[at], scratch);
    if (n == 0 - __eagain()) {
      more = false;
    } else if (n <= 0) {
      let stopped: (__Conns, boolean) = __plain_stopped(c, at, n);
      c = stopped.0;
      done = stopped.1;
      more = !done;
    } else {
      __arr_set_len(scratch, n);
      if (buf.len() == 0) {
        buf = scratch;
        lent = at;
      } else {
        buf = buf.concat(scratch);
      }
      if (buf.len() > http.http_request_bytes_cap(opts.limits)) {
        more = false;
        done = true;
      } else {
        more = n == __recv_room();
      }
    }
  }
  if (done || !was_idle || buf.len() == 0) {
    return (__Conns { ...c, bufs: c.bufs.with(at, buf), lent: lent }, done);
  }
  let deadline: i64 = monotonic_ns() + opts.recv_deadline.as_nanos();
  return (__Conns { ...c, bufs: c.bufs.with(at, buf), lent: lent, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline) }, false);
}

function __read_onto(buf: u8[], plain: u8[]): u8[] {
  if (buf.len() == 0) {
    return plain;
  }
  return buf.concat(plain);
}

function __serve_read_tls[D: async.Driver](drv: D, c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
  let buf: u8[] = c.bufs[at];
  let was_idle: boolean = buf.len() == 0;
  let t: __Tls = c.tls[at];
  c = __Conns { ...c, tls: c.tls.with(at, Plain) };
  let was: boolean = false;
  if let Secured(sess) = t {
    was = sess.connected();
  }
  let done: boolean = false;
  let more: boolean = true;
  while (more) {
    __arr_set_len(scratch, __recv_room());
    let n: i32 = drv.recv_into(c.fds[at], scratch);
    if (n == 0 - __eagain()) {
      more = false;
    } else if (n <= 0) {
      more = false;
      done = true;
    } else {
      let took: (__Conns, __Tls, u8[], boolean) = __tls_taken(drv, c, at, t, scratch[0:n], opts);
      c = took.0;
      t = took.1;
      buf = __read_onto(buf, took.2);
      done = took.3;
      more = !done && n == __recv_room() && buf.len() <= http.http_request_bytes_cap(opts.limits);
      if (buf.len() > http.http_request_bytes_cap(opts.limits)) {
        done = true;
      }
    }
  }
  c = __Conns { ...c, bufs: c.bufs.with(at, buf), tls: c.tls.with(at, t) };
  let off: (__Conns, boolean) = __serve_handshook(c, at, was, done, opts);
  c = off.0;
  done = off.1;
  if (done || !was_idle || buf.len() == 0) {
    return (c, done);
  }
  let deadline: i64 = monotonic_ns() + opts.recv_deadline.as_nanos();
  return (__Conns { ...c, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline) }, false);
}
```

### `31 function __serve_handshook(c: __Conns, at: i32, was: boolean, done: boolean, opts: Config): (__Conns, boole...`

```fern
<<31 function __serve_handshook(c: __Conns, at: i32, was: boolean, done: boolean, opts: Config): (__Conns, boole...>>=
function __serve_handshook(c: __Conns, at: i32, was: boolean, done: boolean, opts: Config): (__Conns, boolean) {
  if (done || was || !__ktls_asked(opts)) {
    return (c, done);
  }
  if let Secured(sess) = c.tls[at] {
    if (sess.connected()) {
      return __serve_offload(c, at, sess);
    }
  }
  return (c, done);
}

function __serve_offload(c: __Conns, at: i32, sess: server.Session): (__Conns, boolean) {
  if (c.pending[at].len() != 0) {
    return (c, false);
  }
  if let Some(o) = sess.offload() {
    match (ktls.attach(c.fds[at], o.keys)) {
      ktls.Kernel => {
        return (__Conns { ...c, tls: c.tls.with(at, InKernel(o.session)) }, false);
      },
      ktls.Unchanged => {},
      ktls.Broken(_) => {
        return (c, true);
      }
    }
  }
  return (c, false);
}

function __plain_stopped(c: __Conns, at: i32, n: i32): (__Conns, boolean) {
  if (n == 0 - 5) {
    if let InKernel(_) = c.tls[at] {
      return __kernel_record(c, at);
    }
  }
  return (c, true);
}

function __kernel_record(c: __Conns, at: i32): (__Conns, boolean) {
  let fd: i32 = c.fds[at];
  if let InKernel(sess) = c.tls[at] {
    match (ktls.read_now(fd)) {
      ktls.Opened(t, content) => {
        match (sess.read(ktls.framed(t, content), now_unix_ms())) {
          Ok(step) => {
            let c2: __Conns = __Conns { ...c, tls: c.tls.with(at, InKernel(step.session)) };
            if (ktls.send_records(fd, step.output) < 0 || ktls.install(fd, step.rekeyed) < 0) {
              return (c2, true);
            }
            return (c2, step.closed);
          },
          Err(f) => {
            ktls.send_records(fd, f.alert);
            return (c, true);
          }
        }
      },
      ktls.NothingYet => {
        return (c, false);
      },
      ktls.ReadFailed(e) => {
        if (e == 74) {
          ktls.alert(fd, 20);
        }
        return (c, true);
      },
      _ => {
        return (c, true);
      }
    }
  }
  return (c, true);
}

function __tls_taken[D: async.Driver](drv: D, c: __Conns, at: i32, t: __Tls, raw: [u8], opts: Config): (__Conns, __Tls, u8[], boolean) {
  let none: u8[] = [];
  if let Secured(sess) = t {
    match (sess.read(raw, now_unix_ms())) {
      Ok(step) => {
        let done: boolean = step.closed;
        if (step.output.len() > 0) {
          let sent: (__Conns, __Sent) = __serve_send_raw(drv, c, at, step.output, opts);
          c = sent.0;
          match (sent.1) {
            Failed => {
              done = true;
            },
            _ => {}
          }
        }
        return (c, Secured(step.session), step.data, done);
      },
      Err(f) => {
        tcp_send_bytes(c.fds[at], f.alert);
        return (c, Plain, none, true);
      }
    }
  }
  return (c, t, none, true);
}

function __bytes_from(buf: u8[], from: i32): u8[] {
  return __bytes_range(buf, from, buf.len());
}
```

### `32 enum __Behind`

```fern
<<32 enum __Behind>>=
enum __Behind { Pipelined, Idle, Expecting, Illformed(i32) }
```

### `33 function __serve_behind(fr: http.HttpFraming, none: http.HttpFramed): (__Behind, http.HttpFramed)`

```fern
<<33 function __serve_behind(fr: http.HttpFraming, none: http.HttpFramed): (__Behind, http.HttpFramed)>>=
function __serve_behind(fr: http.HttpFraming, none: http.HttpFramed): (__Behind, http.HttpFramed) {
  match (fr) {
    http.Framed(f) => {
      return (Pipelined, f);
    },
    http.Incomplete => {
      return (Idle, none);
    },
    http.Continue => {
      return (Expecting, none);
    },
    http.Malformed(status) => {
      return (Illformed(status), none);
    }
  }
  return (Idle, none);
}

function __tail_persists(behind: __Behind, eof: boolean): boolean {
  match (behind) {
    Pipelined => {
      return true;
    },
    Idle => {
      return !eof;
    },
    Expecting => {
      return !eof;
    },
    Illformed(_) => {
      return false;
    }
  }
  return false;
}

function __serve_took(c: __Conns, at: i32, f: http.HttpFramed, opts: Config, eof: boolean, stop: __Stop): (__Conns, boolean, __Behind, http.HttpFramed) {
  let served: i32 = c.served[at] + 1;
  let keep: boolean = f.keep_alive && served < opts.keep_alive_requests && !__serve_stopping(stop);
  let due: i64 = __backlog_due(c, at);
  let queued: i32[] = __unqueued(c.queued, c.fds[at], c.backlog[at]);
  let off: i32 = c.offs[at] + f.len;
  let last: http.HttpFramed = http.http_framed_kept(f);
  let parsed: (__Behind, http.HttpFramed) = (Idle, f);
  if (off < c.bufs[at].len()) {
    parsed = __serve_behind(http.http_parse_request_framed_from(c.bufs[at], off, opts.limits, last), f);
  }
  if (keep) {
    keep = __tail_persists(parsed.0, eof);
  }
  return (__Conns { ...c, offs: c.offs.with(at, off), served: c.served.with(at, served), due: due, queued: queued, backlog: c.backlog.with(at, false), continued: c.continued.with(at, false), last: last, keep: c.keep.with(at, keep) }, keep, parsed.0, parsed.1);
}

function __serve_compact(c: __Conns, at: i32): __Conns {
  if (c.lent == at) {
    return __Conns { ...c, bufs: c.bufs.with(at, __bytes_from(c.bufs[at], c.offs[at])), offs: c.offs.with(at, 0), lent: 0 - 1 };
  }
  if (c.offs[at] == 0) {
    return c;
  }
  return __Conns { ...c, bufs: c.bufs.with(at, __bytes_from(c.bufs[at], c.offs[at])), offs: c.offs.with(at, 0) };
}

function __pipeline_burst(): i32 {
  return 32;
}

function __serve_next(c: __Conns, at: i32, opts: Config): __Conns {
  let now: i64 = monotonic_ns();
  let span: Duration = opts.keep_alive_idle;
  if (c.bufs[at].len() > c.offs[at]) {
    span = opts.recv_deadline;
  }
  c = __Conns { ...c, deadlines: c.deadlines.with(at, now + span.as_nanos()), due: __sooner(c.due, now + span.as_nanos()), rate_since: c.rate_since.with(at, 0 - 1 as i64) };
  return __body_deadline(c, at, opts, now);
}

function __body_deadline(c: __Conns, at: i32, opts: Config, now: i64): __Conns {
  let since: i64 = c.rate_since[at];
  let base: i32 = c.rate_base[at];
  let buf: u8[] = c.bufs[at];
  if (since < 0 as i64) {
    if (buf.len() <= c.offs[at] || !http.http_header_block_complete(buf, c.offs[at], opts.limits)) {
      return c;
    }
    since = now;
    base = buf.len();
  }
  let earned: i64 = since + opts.data_rate_grace.as_nanos();
  if (opts.min_data_rate > 0) {
    earned = earned + (buf.len() - base) as i64 * (1000000000 as i64) / (opts.min_data_rate as i64);
  }
  let deadline: i64 = c.deadlines[at];
  if (earned > deadline) {
    deadline = earned;
  }
  return __Conns { ...c, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline), rate_since: c.rate_since.with(at, since), rate_base: c.rate_base.with(at, base) };
}

function __serve_ready[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, behind: __Behind, clk: __Clock): (__Conns, boolean) {
  match (behind) {
    Pipelined => {
      return (c, true);
    },
    Idle => {
      return (__serve_next(c, at, opts), false);
    },
    Expecting => {
      return (__serve_continue(drv, c, at, opts), false);
    },
    Illformed(status) => {
      return (__serve_refuse(drv, c, at, status, opts, clk), false);
    }
  }
  return (c, false);
}
```

### `34 function __serve_continue[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): __Conns`

```fern
<<34 function __serve_continue[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): __Conns>>=
function __serve_continue[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): __Conns {
  if (c.continued[at]) {
    return __serve_next(c, at, opts);
  }
  c = __Conns { ...c, continued: c.continued.with(at, true), keep: c.keep.with(at, true) };
  buf_push(c.out, "HTTP/1.1 100 Continue\r\n\r\n");
  let sent: (__Conns, __Sent) = __serve_send_out(drv, c, at, opts);
  match (sent.1) {
    Partial => {
      return sent.0;
    },
    Failed => {
      return __serve_close(drv, sent.0, at);
    },
    Whole => {
      return __serve_next(sent.0, at, opts);
    }
  }
  return sent.0;
}

function __serve_refuse[D: async.Driver](drv: D, c: __Conns, at: i32, status: i32, opts: Config, clk: __Clock): __Conns {
  c = __Conns { ...c, metrics: c.metrics.request("", status, 0 as i64) };
  __serve_wire(c.out, "GET", "HTTP/1.1", http.text(status, ""), false, clk);
  let sent: (__Conns, __Sent) = __serve_send_out(drv, __Conns { ...c, keep: c.keep.with(at, false) }, at, opts);
  match (sent.1) {
    Partial => {
      return sent.0;
    },
    Failed => {
      return __serve_close(drv, sent.0, at);
    },
    Whole => {
      return __serve_close(drv, sent.0, at);
    }
  }
  return sent.0;
}

function __serve_respond[D: async.Driver](drv: D, c: __Conns, at: i32, method: string, version: string, resp: HttpResponse, keep_alive: boolean, opts: Config, answered: i32, behind: __Behind, clk: __Clock): (__Conns, boolean) {
  resp = __with_alt_svc(c, resp);
  c = __serve_count(c, at, method, resp.status);
  c = __stage_background(c, resp);
  let rest: __Tail = __serve_tail_wire(c, at, method, version, resp, keep_alive, clk);
  c = __Conns { ...c, tails: c.tails.with(at, rest), keep: c.keep.with(at, c.keep[at] && keep_alive && __tail_keeps(rest)) };
  if (__corks(rest, c.keep[at], answered, behind)) {
    return (c, true);
  }
  let sent: (__Conns, __Sent) = __serve_send_out(drv, c, at, opts);
  c = sent.0;
  match (sent.1) {
    Partial => {
      return (c, false);
    },
    Failed => {
      return (__serve_close(drv, c, at), false);
    },
    Whole => {}
  }
  let produced: (__Conns, __Sent) = __serve_produce(drv, c, at, opts);
  c = produced.0;
  match (produced.1) {
    Partial => {
      return (c, false);
    },
    Failed => {
      return (__serve_close(drv, c, at), false);
    },
    Whole => {}
  }
  if (!c.keep[at]) {
    return (__serve_close(drv, c, at), false);
  }
  let ready: (__Conns, boolean) = __serve_ready(drv, c, at, opts, behind, clk);
  c = ready.0;
  if (!ready.1) {
    return (c, false);
  }
  if (answered >= __pipeline_burst()) {
    return (__backlog_on(c, at), false);
  }
  return (c, true);
}

function __corks(rest: __Tail, persists: boolean, answered: i32, behind: __Behind): boolean {
  if (!persists || answered >= __pipeline_burst()) {
    return false;
  }
  match (rest) {
    NoTail => {},
    _ => {
      return false;
    }
  }
  match (behind) {
    Pipelined => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __drain_pending[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  if (c.pending[at].len() == 0) {
    return (c, Whole);
  }
  return __serve_send_raw(drv, c, at, c.pending[at], opts);
}
```

### `35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...`

```fern
<<35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...>>=
function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns, i32) {
  let sent: (__Conns, __Sent) = __drain_pending(drv, c, at, opts);
  c = sent.0;
  match (sent.1) {
    Partial => {
      return (c, -1);
    },
    Failed => {
      return (__serve_close(drv, c, at), -1);
    },
    Whole => {}
  }
  let produced: (__Conns, __Sent) = __serve_produce(drv, __Conns { ...c, pending: c.pending.with(at, []) }, at, opts);
  c = produced.0;
  match (produced.1) {
    Partial => {
      return (c, -1);
    },
    Failed => {
      return (__serve_close(drv, c, at), -1);
    },
    Whole => {}
  }
  if (!c.keep[at]) {
    return (__serve_close(drv, c, at), -1);
  }
  if (drv.watch(c.fds[at], __read_interest(c, c.tls[at])) < 0) {
    return (__serve_close(drv, c, at), -1);
  }
  let parsed: (__Behind, http.HttpFramed) = __serve_behind(http.http_parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last);
  let ready: (__Conns, boolean) = __serve_ready(drv, __Conns { ...c, pending: c.pending.with(at, []) }, at, opts, parsed.0, clk);
  if (!ready.1) {
    return (ready.0, -1);
  }
  return (ready.0, at);
}
```

### `36 const H2_NOT: i32`

```fern
<<36 const H2_NOT: i32>>=
const H2_NOT: i32 = 0;

const H2_SERVED: i32 = 1;

const H2_AWAITED: i32 = 2;
```

### `37 function __h2_mode(c: __Conns, at: i32, opts: Config): i32`

```fern
<<37 function __h2_mode(c: __Conns, at: i32, opts: Config): i32>>=
function __h2_mode(c: __Conns, at: i32, opts: Config): i32 {
  match (c.framing[at]) {
    Http2(conn) => {
      return H2_SERVED;
    },
    _ => {}
  }
  if (!opts.h2 || c.served[at] > 0) {
    return H2_NOT;
  }
  if (__alpn_h2(c, at)) {
    return H2_SERVED;
  }
  let buf: u8[] = c.bufs[at];
  let from: i32 = c.offs[at];
  let want: u8[] = h2.preface();
  let i: i32 = 0;
  while (i < want.len() && from + i < buf.len()) {
    if (buf[from + i] != want[i]) {
      return H2_NOT;
    }
    i = i + 1;
  }
  if (buf.len() - from < want.len()) {
    return H2_AWAITED;
  }
  return H2_SERVED;
}

function __alpn_h2(c: __Conns, at: i32): boolean {
  match (c.tls[at]) {
    Secured(sess) => {
      return sess.alpn() == "h2";
    },
    InKernel(sess) => {
      return sess.alpn() == "h2";
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __h2_config(opts: Config, streaming: boolean): h2.Config {
  return h2.Config { ...h2.config(), max_header_list: opts.limits.header_bytes, max_body: opts.limits.body, max_concurrent_streams: opts.max_in_flight, stream_bodies: streaming };
}

function __h2_streaming(opts: Config): boolean {
  return opts.stream_bodies && async.parks();
}

function __serve_h2[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, eof: boolean): (__Conns, __Flight[]) {
  c = __h2_opened(drv, c, at, opts, true);
  let step: h2.Step = __h2_fed(c, at);
  c = __h2_taken(c, at);
  let answered: __H2Answered = __h2_more(drv, __h2_answered(drv, __H2Answered { conn: step.conn, conns: c, fls: fls, out: step.output }, step.events, at, handler, opts, stop), at);
  let conn: h2.Conn = answered.conn;
  let out: u8[] = answered.out;
  let flights: __Flight[] = answered.fls;
  let closing: boolean = conn.closed() || eof;
  c = answered.conns;
  c = __Conns { ...c, framing: c.framing.with(at, Http2(conn)), keep: c.keep.with(at, !closing) };
  return __h2_flushed(drv, c, flights, at, out, opts, closing);
}

function __h2_settled[D: async.Driver](drv: D, a: __H2Answered, at: i32, opts: Config): (__Conns, __Flight[]) {
  let c: __Conns = __Conns { ...a.conns, framing: a.conns.framing.with(at, Http2(a.conn)) };
  return __h2_flushed(drv, c, a.fls, at, a.out, opts, a.conn.closed());
}

function __h2_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, scratch: u8[], handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, clk: __Clock): (__Conns, __Flight[]) {
  let ev: (__Conns, i32, boolean) = __serve_event(drv, c, fd, scratch, opts, clk);
  c = ev.0;
  if (ev.1 < 0) {
    return (c, fls);
  }
  let served: (__Conns, __Flight[]) = __serve_h2(drv, c, fls, ev.1, handler, opts, stop, ev.2);
  if (__flight_at(served.1, fd) < 0) {
    return served;
  }
  let woken: (__Conns, __Flight[]) = __h2_pulls_woken(drv, served.0, served.1, fd, opts, clk);
  return __h2_after_pulls(drv, woken.0, woken.1, fd, opts);
}

function __h2_pulls_woken[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  let streams: i32[] = [];
  let producers: boolean[] = [];
  for fl in fls {
    if (fl.stream > 0 && __flight_pair(fl, fd) >= 0) {
      streams = streams.append(fl.stream);
      producers = producers.append(__is_producer(fl));
    }
  }
  let i: i32 = 0;
  while (i < streams.len()) {
    let fi: i32 = __flight_on_stream(fls, fd, streams[i], producers[i]);
    if (fi >= 0) {
      let woke: (__Conns, __Flight[]) = __flight_wake(drv, c, fls, fi, __flight_pair(fls[fi], fd), opts, clk);
      c = woke.0;
      fls = woke.1;
    }
    i = i + 1;
  }
  return (c, fls);
}

function __is_producer(fl: __Flight): boolean {
  match (fl.job) {
    Producer(t) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __flight_on_stream(fls: __Flight[], fd: i32, stream: i32, producer: boolean): i32 {
  let i: i32 = 0;
  while (i < fls.len()) {
    if (fls[i].fd == fd && fls[i].stream == stream && __is_producer(fls[i]) == producer) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}
```

### `38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, opts: Config): (__...`

```fern
<<38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, opts: Config): (__...>>=
function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, opts: Config): (__Conns, __Flight[]) {
  let at: i32 = __conns_index(c, fd);
  if (at < 0 || c.pulls.len() == 0 && c.producers.len() == 0) {
    return (c, fls);
  }
  match (c.framing[at]) {
    Http2(conn) => {
      let a: __H2Answered = __h2_more(drv, __h2_consumed(__H2Answered { conn: conn, conns: c, fls: fls, out: [] }, at), at);
      if (a.out.len() == 0) {
        return (__Conns { ...a.conns, framing: a.conns.framing.with(at, Http2(a.conn)) }, a.fls);
      }
      return __h2_settled(drv, a, at, opts);
    },
    _ => {
      return (c, fls);
    }
  }
  return (c, fls);
}

function __h2_fed(c: __Conns, at: i32): h2.Step {
  let bytes: u8[] = c.bufs[at];
  let from: i32 = c.offs[at];
  if (from > 0) {
    bytes = __bytes_from(bytes, from);
  }
  match (c.framing[at]) {
    Http2(conn) => {
      return conn.read(bytes);
    },
    _ => {}
  }
  return h2.Step { conn: h2.server(h2.config()).conn, output: [], events: [] };
}

function __h2_taken(c: __Conns, at: i32): __Conns {
  let lent: i32 = c.lent;
  if (lent == at) {
    lent = 0 - 1;
  }
  return __Conns { ...c, bufs: c.bufs.with(at, []), offs: c.offs.with(at, 0), lent: lent };
}

function __h2_turn_with[D: async.Driver, S](drv: D, c: __Conns, at: i32, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, stop: __Stop, eof: boolean, mode: i32): (__Conns, S) {
  if (mode != H2_SERVED) {
    if (eof) {
      return (__serve_close(drv, c, at), st);
    }
    return (c, st);
  }
  c = __h2_opened(drv, c, at, opts, false);
  let step: h2.Step = __h2_fed(c, at);
  c = __h2_taken(c, at);
  let conn: h2.Conn = step.conn;
  let out: u8[] = step.output;
  for e in step.events {
    match (e) {
      h2.Request(stream, req) => {
        let stepped: (__Conns, h2.Conn, S, u8[]) = __h2_step_with(c, at, conn, stream, req, st, handler, opts, stop);
        c = stepped.0;
        conn = stepped.1;
        st = stepped.2;
        out = out.concat(stepped.3);
      },
      _ => {}
    }
  }
  c = __Conns { ...c, framing: c.framing.with(at, Http2(conn)), keep: c.keep.with(at, !conn.closed() && !eof) };
  let flushed: (__Conns, __Flight[]) = __h2_flushed(drv, c, [], at, out, opts, conn.closed() || eof);
  return (flushed.0, st);
}

function __h2_step_with[S](c: __Conns, at: i32, conn: h2.Conn, stream: i32, req: HttpRequest, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, stop: __Stop): (__Conns, h2.Conn, S, u8[]) {
  c = __serve_started(c, at);
  let stepped: (S, HttpResponse) = (st, http.text(503, ""));
  if (!__serve_not_ready(req, opts, stop)) {
    stepped = __serve_step(handler, st, req, c.hosts[at], opts);
  }
  c = __serve_count(c, at, req.method, stepped.1.status);
  let r: h2.Step = conn.with_date(c.date).respond(stream, __with_alt_svc(c, stepped.1));
  return (c, r.conn, stepped.0, r.output);
}

function __h2_flushed[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, out: u8[], opts: Config, closing: boolean): (__Conns, __Flight[]) {
  if (out.len() > 0) {
    let sent: (__Conns, __Sent) = __serve_send(drv, c, at, out, opts);
    c = sent.0;
    match (sent.1) {
      Partial => {
        return (c, fls);
      },
      Failed => {
        return __h2_close(drv, c, fls, at);
      },
      Whole => {}
    }
  }
  if (closing) {
    return __h2_close(drv, c, fls, at);
  }
  c = __serve_next(c, at, opts);
  if (__flight_at(fls, c.fds[at]) >= 0) {
    c = __Conns { ...c, deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()) };
  }
  return (c, fls);
}

function __h2_close[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32): (__Conns, __Flight[]) {
  let fd: i32 = c.fds[at];
  let kept: __Flight[] = [];
  for fl in fls {
    if (fl.fd == fd) {
      __flight_cancel(drv, fl);
    } else {
      kept = kept.append(fl);
    }
  }
  return (__serve_close(drv, c, at), kept);
}

function __h2_turn[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, eof: boolean, mode: i32): (__Conns, __Flight[]) {
  if (mode == H2_SERVED) {
    return __serve_h2(drv, c, fls, at, handler, opts, stop, eof);
  }
  if (eof) {
    return __h2_close(drv, c, fls, at);
  }
  return (c, fls);
}
```

### `39 function __h2_framed(c: __Conns, fd: i32): boolean`

```fern
<<39 function __h2_framed(c: __Conns, fd: i32): boolean>>=
function __h2_framed(c: __Conns, fd: i32): boolean {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return false;
  }
  match (c.framing[at]) {
    Http2(conn) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __flight_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, readiness: i32, scratch: u8[], opts: Config, clk: __Clock, pool: fetch.Idle): (__Conns, __Flight[], boolean) {
  if (__upstream_owned(pool, fd)) {
    let upstream: (__Conns, __Flight[]) = __shared_event(drv, pool, c, fls, fd, readiness, opts);
    return (upstream.0, upstream.1, true);
  }
  let fi: i32 = __flight_index(fls, fd);
  if (fi >= 0) {
    let woke: (__Conns, __Flight[]) = __flight_wake(drv, c, fls, fi, __flight_pair(fls[fi], fd), opts, clk);
    let taken: (__Conns, __Flight[]) = __staged_taken(drv, woke.0, woke.1, opts, pool);
    return (taken.0, taken.1, true);
  }
  let busy: i32 = __flight_at(fls, fd);
  if (busy >= 0) {
    let held: (__Conns, __Flight[]) = __serve_held_event(drv, c, fls, busy, fd, readiness, scratch, opts);
    let taken: (__Conns, __Flight[]) = __staged_taken(drv, held.0, held.1, opts, pool);
    return (taken.0, taken.1, true);
  }
  return (c, fls, false);
}

function __h2_opened[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, streamable: boolean): __Conns {
  match (c.framing[at]) {
    Http2(open) => {
      return c;
    },
    _ => {}
  }
  let start: h2.Step = h2.server(__h2_config(opts, streamable && __h2_streaming(opts)));
  c = __Conns { ...c, framing: c.framing.with(at, Http2(start.conn)) };
  return __serve_send(drv, c, at, start.output, opts).0;
}
```

### `40 struct __H2Answered`

```fern
<<40 struct __H2Answered>>=
struct __H2Answered { conn: h2.Conn, conns: __Conns, fls: __Flight[], out: u8[] }

struct __H2Body { data: Cell[u8[]], ended: Cell[i32], consumed: Cell[i32], fault: Cell[i32], done: Cell[boolean] }

struct __H2Pull { fd: i32, stream: i32, body: __H2Body }

struct __H2Producer { fd: i32, stream: i32, producer: ChunkProducer, index: i32, trailers: HeaderMap, running: boolean }
```

### `41 function __h2_pending_max(): i32`

```fern
<<41 function __h2_pending_max(): i32>>=
function __h2_pending_max(): i32 {
  return 65536;
}

function __pull_at(c: __Conns, fd: i32, stream: i32): i32 {
  let i: i32 = 0;
  while (i < c.pulls.len()) {
    if (c.pulls[i].fd == fd && c.pulls[i].stream == stream) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __producer_at(c: __Conns, fd: i32, stream: i32): i32 {
  let i: i32 = 0;
  while (i < c.producers.len()) {
    if (c.producers[i].fd == fd && c.producers[i].stream == stream) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __pulls_without(pulls: __H2Pull[], fd: i32): __H2Pull[] {
  let out: __H2Pull[] = [];
  for p in pulls {
    if (p.fd != fd) {
      out = out.append(p);
    }
  }
  return out;
}

function __producers_without(ps: __H2Producer[], fd: i32): __H2Producer[] {
  let out: __H2Producer[] = [];
  for p in ps {
    if (p.fd != fd) {
      out = out.append(p);
    }
  }
  return out;
}

function __pull_removed(pulls: __H2Pull[], at: i32): __H2Pull[] {
  let out: __H2Pull[] = [];
  let i: i32 = 0;
  while (i < pulls.len()) {
    if (i != at) {
      out = out.append(pulls[i]);
    }
    i = i + 1;
  }
  return out;
}

function __producer_removed(ps: __H2Producer[], at: i32): __H2Producer[] {
  let out: __H2Producer[] = [];
  let i: i32 = 0;
  while (i < ps.len()) {
    if (i != at) {
      out = out.append(ps[i]);
    }
    i = i + 1;
  }
  return out;
}

function __h2_body_source(body: __H2Body, fd: i32): BodySource {
  return BodySource { next: (): Option[u8[]] => __h2_body_next(body, fd), fault: body.fault, send: (bytes: u8[]): i32 => 0 - 1 };
}

function __h2_body_next(body: __H2Body, fd: i32): Option[u8[]] {
  while (true) {
    let d: u8[] = body.data.get();
    if (d.len() > 0) {
      let empty: u8[] = [];
      body.data.set(empty);
      body.consumed.set(body.consumed.get() + d.len());
      return Some(d);
    }
    if (body.ended.get() != 0) {
      return None;
    }
    let set: i32[] = [fd, 1];
    async.wait_any(set, 0 - 1);
  }
  return None;
}

function __h2_arrived(c: __Conns, fd: i32, sid: i32, bytes: u8[]): __Conns {
  let pi: i32 = __pull_at(c, fd, sid);
  if (pi < 0) {
    return c;
  }
  let body: __H2Body = c.pulls[pi].body;
  body.data.set(body.data.get().concat(bytes));
  return c;
}

function __h2_body_ended(c: __Conns, fd: i32, sid: i32): __Conns {
  let pi: i32 = __pull_at(c, fd, sid);
  if (pi < 0) {
    return c;
  }
  c.pulls[pi].body.ended.set(1);
  return __pull_tidied(c, pi);
}

function __pull_tidied(c: __Conns, pi: i32): __Conns {
  let body: __H2Body = c.pulls[pi].body;
  if (body.ended.get() != 0 && body.done.get()) {
    return __Conns { ...c, pulls: __pull_removed(c.pulls, pi) };
  }
  return c;
}

function __h2_responded(a: __H2Answered, at: i32, sid: i32): __H2Answered {
  let pi: i32 = __pull_at(a.conns, a.conns.fds[at], sid);
  if (pi < 0) {
    return a;
  }
  a.conns.pulls[pi].body.done.set(true);
  return __H2Answered { ...a, conns: __pull_tidied(a.conns, pi) };
}
```

### `42 function __h2_stream_reset(c: __Conns, fd: i32, sid: i32): __Conns`

```fern
<<42 function __h2_stream_reset(c: __Conns, fd: i32, sid: i32): __Conns>>=
function __h2_stream_reset(c: __Conns, fd: i32, sid: i32): __Conns {
  let pi: i32 = __pull_at(c, fd, sid);
  if (pi >= 0) {
    let body: __H2Body = c.pulls[pi].body;
    body.ended.set(2);
    body.fault.set(400);
    c = __Conns { ...c, pulls: __pull_removed(c.pulls, pi) };
  }
  let pr: i32 = __producer_at(c, fd, sid);
  if (pr >= 0) {
    c = __Conns { ...c, producers: __producer_removed(c.producers, pr) };
  }
  return c;
}

function __h2_streamed[D: async.Driver](drv: D, a: __H2Answered, at: i32, sid: i32, req: HttpRequest, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let empty: u8[] = [];
  let body: __H2Body = __H2Body { data: cell_new(empty), ended: cell_new(0), consumed: cell_new(0), fault: cell_new(0), done: cell_new(false) };
  let fd: i32 = a.conns.fds[at];
  let conns: __Conns = __Conns { ...a.conns, pulls: a.conns.pulls.append(__H2Pull { fd: fd, stream: sid, body: body }) };
  let streamed: HttpRequest = HttpRequest { ...req, body: stream.stream_from_source(__h2_body_source(body, fd)) };
  return __h2_request(drv, __H2Answered { ...a, conns: conns }, at, sid, streamed, handler, opts, stop);
}

function __joined(a: u8[], b: u8[]): u8[] {
  if (a.len() == 0) {
    return b;
  }
  if (b.len() == 0) {
    return a;
  }
  return a.concat(b);
}

function __h2_respond[D: async.Driver](drv: D, a: __H2Answered, at: i32, sid: i32, resp: HttpResponse): __H2Answered {
  resp = __with_alt_svc(a.conns, resp);
  let conn: h2.Conn = a.conn;
  let conns: __Conns = a.conns;
  let fls: __Flight[] = a.fls;
  conn = conn.with_date(conns.date);
  let out: u8[] = a.out;
  let answer: HttpResponse = __file_chunks(resp);
  match (answer.body) {
    BodyChunks(producer) => {
      let step: h2.Step = conn.respond_head(sid, answer);
      let headed: __H2Answered = __H2Answered { conn: step.conn, conns: conns, fls: fls, out: __joined(out, step.output) };
      if (!step.conn.producing(sid)) {
        return __h2_responded(headed, at, sid);
      }
      let fd: i32 = conns.fds[at];
      conns = __Conns { ...conns, producers: conns.producers.append(__H2Producer { fd: fd, stream: sid, producer: producer, index: 0, trailers: answer.trailers, running: false }) };
      return __h2_produce(drv, __H2Answered { ...headed, conns: conns }, at, sid);
    },
    _ => {
      let step: h2.Step = conn.respond(sid, answer);
      return __h2_responded(__H2Answered { conn: step.conn, conns: conns, fls: fls, out: __joined(out, step.output) }, at, sid);
    }
  }
  return a;
}

function __h2_produce[D: async.Driver](drv: D, a: __H2Answered, at: i32, sid: i32): __H2Answered {
  while (true) {
    let fd: i32 = a.conns.fds[at];
    let pi: i32 = __producer_at(a.conns, fd, sid);
    if (pi < 0) {
      return a;
    }
    let p: __H2Producer = a.conns.producers[pi];
    if (p.running || a.conn.pending(sid) >= __h2_pending_max()) {
      return a;
    }
    let next: (i32) => Option[u8[]] = p.producer.next;
    let i: i32 = p.index;
    let t: async.Task[Option[u8[]]] = async.task_new((): Option[u8[]] => next(i));
    match (async.task_start(t)) {
      Done(chunk) => {
        async.task_free(t);
        let stepped: (__H2Answered, boolean) = __h2_chunk(a, at, pi, chunk);
        a = stepped.0;
        if (stepped.1) {
          return a;
        }
      },
      Suspended(w) => {
        let running: __H2Producer = __H2Producer { ...p, running: true };
        let conns: __Conns = __Conns { ...a.conns, producers: a.conns.producers.with(pi, running) };
        let none: i32[] = [];
        let fl: __Flight = __Flight { job: Producer(t), wait: w, fd: fd, set: none, due: 0 - 1 as i64, method: "", version: "", kept: false, behind: Idle, body: None, idle: 0 - 1 as i64, ends: 0 - 1 as i64, stream: sid, since: 0 - 1 as i64, grpc: false, quic: 0 };
        return __H2Answered { ...a, conns: conns, fls: a.fls.append(__flight_watch(drv, fl, monotonic_ns())) };
      },
      Cancelled => {
        async.task_free(t);
        return __h2_producer_failed(a, at, pi);
      }
    }
  }
  return a;
}

function __h2_chunk(a: __H2Answered, at: i32, pi: i32, chunk: Option[u8[]]): (__H2Answered, boolean) {
  let p: __H2Producer = a.conns.producers[pi];
  match (chunk) {
    Some(bytes) => {
      let step: h2.Step = a.conn.write(p.stream, bytes);
      let advanced: __H2Producer = __H2Producer { ...p, index: p.index + 1, running: false };
      return (__H2Answered { ...a, conn: step.conn, conns: __Conns { ...a.conns, producers: a.conns.producers.with(pi, advanced) }, out: a.out.concat(step.output) }, false);
    },
    None => {
      let step: h2.Step = a.conn.end(p.stream, __trailers_joined(p.trailers, p.producer.trailers()));
      let conns: __Conns = __Conns { ...a.conns, producers: __producer_removed(a.conns.producers, pi) };
      return (__h2_responded(__H2Answered { ...a, conn: step.conn, conns: conns, out: a.out.concat(step.output) }, at, p.stream), true);
    }
  }
  return (a, true);
}

function __trailers_joined(a: HeaderMap, b: HeaderMap): HeaderMap {
  let i: i32 = 0;
  while (i < b.names.len()) {
    a = a.append(b.names[i], b.values[i]);
    i = i + 1;
  }
  return a;
}

function __h2_producer_failed(a: __H2Answered, at: i32, pi: i32): __H2Answered {
  let p: __H2Producer = a.conns.producers[pi];
  let step: h2.Step = a.conn.reset(p.stream, frame.CANCEL);
  let conns: __Conns = __Conns { ...a.conns, producers: __producer_removed(a.conns.producers, pi) };
  return __h2_responded(__H2Answered { ...a, conn: step.conn, conns: conns, out: a.out.concat(step.output) }, at, p.stream);
}
```

### `43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, t: async.Ta...`

```fern
<<43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, t: async.Ta...>>=
function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, t: async.Task[Option[u8[]]], ready: i32, opts: Config): (__Conns, __Flight[]) {
  let at: i32 = __conns_index(c, fl.fd);
  let pi: i32 = __producer_at(c, fl.fd, fl.stream);
  if (at < 0 || pi < 0) {
    let gone: async.TaskStatus[Option[u8[]]] = async.task_cancel(t);
    async.task_free(t);
    return (c, fls);
  }
  match (c.framing[at]) {
    Http2(conn) => {
      let a: __H2Answered = __H2Answered { conn: conn, conns: c, fls: fls, out: [] };
      match (async.task_resume(t, ready)) {
        Done(chunk) => {
          async.task_free(t);
          let stepped: (__H2Answered, boolean) = __h2_chunk(a, at, pi, chunk);
          a = stepped.0;
          if (!stepped.1) {
            a = __h2_produce(drv, a, at, fl.stream);
          }
          return __h2_settled(drv, a, at, opts);
        },
        Suspended(w) => {
          return (c, fls.append(__flight_watch(drv, __Flight { ...fl, wait: w }, monotonic_ns())));
        },
        Cancelled => {
          async.task_free(t);
          return __h2_settled(drv, __h2_producer_failed(a, at, pi), at, opts);
        }
      }
    },
    _ => {}
  }
  let dropped: async.TaskStatus[Option[u8[]]] = async.task_cancel(t);
  async.task_free(t);
  return (c, fls);
}

function __h2_consumed(a: __H2Answered, at: i32): __H2Answered {
  let fd: i32 = a.conns.fds[at];
  for p in a.conns.pulls {
    if (p.fd == fd) {
      let n: i32 = p.body.consumed.get();
      if (n > 0) {
        p.body.consumed.set(0);
        let step: h2.Step = a.conn.consumed(p.stream, n);
        a = __H2Answered { ...a, conn: step.conn, out: a.out.concat(step.output) };
      }
    }
  }
  return a;
}

function __h2_more[D: async.Driver](drv: D, a: __H2Answered, at: i32): __H2Answered {
  if (a.conns.producers.len() == 0) {
    return a;
  }
  let fd: i32 = a.conns.fds[at];
  let streams: i32[] = [];
  for p in a.conns.producers {
    if (p.fd == fd && !p.running) {
      streams = streams.append(p.stream);
    }
  }
  for sid in streams {
    a = __h2_produce(drv, a, at, sid);
  }
  return a;
}

function __h2_answered[D: async.Driver](drv: D, a: __H2Answered, events: h2.Event[], at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let fd: i32 = a.conns.fds[at];
  for e in events {
    match (e) {
      h2.Request(sid, req) => {
        a = __h2_request(drv, a, at, sid, req, handler, opts, stop);
      },
      h2.Streamed(sid, req) => {
        a = __h2_streamed(drv, a, at, sid, req, handler, opts, stop);
      },
      h2.StreamData(sid, bytes) => {
        a = __H2Answered { ...a, conns: __h2_arrived(a.conns, fd, sid, bytes) };
      },
      h2.StreamEnd(sid, trailers) => {
        a = __H2Answered { ...a, conns: __h2_body_ended(a.conns, fd, sid) };
      },
      h2.Reset(sid, code) => {
        a = __H2Answered { ...a, conns: __h2_stream_reset(a.conns, fd, sid) };
      },
      _ => {}
    }
  }
  return a;
}

function __h2_request[D: async.Driver](drv: D, a: __H2Answered, at: i32, stream: i32, req: HttpRequest, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let conn: h2.Conn = a.conn;
  let fls: __Flight[] = a.fls;
  let out: u8[] = a.out;
  let conns: __Conns = a.conns;
  conns = __serve_started(conns, at);
  match (__serve_start(handler, conns.hosts[at], req, "HTTP/2", true, Idle, opts, stop)) {
    HandlerDone(resp) => {
      conns = __serve_count(conns, at, req.method, resp.status);
      return __h2_respond(drv, __H2Answered { conn: conn, conns: conns, fls: fls, out: out }, at, stream, resp);
    },
    HandlerParked(fl) => {
      let parked: __Flight = __Flight { ...fl, fd: conns.fds[at], stream: stream, since: conns.rate_since[at] };
      return __H2Answered { conn: conn, conns: conns, fls: fls.append(__flight_watch(drv, parked, monotonic_ns())), out: out };
    }
  }
  return __H2Answered { conn: conn, conns: conns, fls: fls, out: out };
}

function __h2_answer[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, fl: __Flight, resp: HttpResponse, opts: Config): (__Conns, __Flight[]) {
  match (c.framing[at]) {
    Http2(conn) => {
      c = __serve_count_from(c, fl.method, resp.status, fl.since);
      return __h2_settled(drv, __h2_respond(drv, __H2Answered { conn: conn, conns: c, fls: fls, out: [] }, at, fl.stream, resp), at, opts);
    },
    _ => {
      return (c, fls);
    }
  }
  return (c, fls);
}

function __serve_eof[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): __Conns {
  if (__in_flight(c, at)) {
    return __Conns { ...c, keep: c.keep.with(at, false) };
  }
  if (__holds_request(__bytes_from(c.bufs[at], c.offs[at]))) {
    return __serve_refuse(drv, c, at, 400, opts, clk);
  }
  return __serve_close(drv, c, at);
}
```

### `44 function __holds_request(buf: u8[]): boolean`

```fern
<<44 function __holds_request(buf: u8[]): boolean>>=
function __holds_request(buf: u8[]): boolean {
  for b in buf {
    if (b != 13 as u8 && b != 10 as u8) {
      return true;
    }
  }
  return false;
}

function __serve_event[D: async.Driver](drv: D, c: __Conns, fd: i32, scratch: u8[], opts: Config, clk: __Clock): (__Conns, i32, boolean) {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return (c, -1, false);
  }
  if (__in_flight(c, at)) {
    let drained: (__Conns, i32) = __serve_drain(drv, c, at, opts, clk);
    if (!__h2_framed(drained.0, fd)) {
      return (drained.0, drained.1, false);
    }
    c = drained.0;
    at = __conns_index(c, fd);
  }
  let had: i32 = c.bufs[at].len();
  let backlog: boolean = c.backlog[at];
  let read: (__Conns, boolean) = __serve_read(drv, c, at, scratch, opts);
  if (!read.1 && !backlog && read.0.bufs[at].len() == had) {
    return (read.0, -1, false);
  }
  return (read.0, at, read.1);
}

function __serve_finish[D: async.Driver](drv: D, c: __Conns, fd: i32, eof: boolean, opts: Config, clk: __Clock): __Conns {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  c = __serve_compact(c, at);
  if (!eof || c.backlog[at]) {
    return c;
  }
  return __serve_eof(drv, c, at, opts, clk);
}

function __with_backlog(c: __Conns, own events: i32[]): i32[] {
  let n: i32 = async.ready_pairs(events);
  let reported: i32 = n * 2;
  for fd in c.queued {
    if (!__reported(events, reported, fd)) {
      if (n * 2 + 1 < events.len()) {
        events = events.with(n * 2, fd).with(n * 2 + 1, 0);
      } else {
        events = events.append(fd).append(0);
      }
      n = n + 1;
    }
  }
  return async.end_pairs(events, n);
}

function __reported(events: i32[], n: i32, fd: i32): boolean {
  let k: i32 = 0;
  while (k + 1 < n) {
    if (events[k] == fd) {
      return true;
    }
    k = k + 2;
  }
  return false;
}
```

### `45 enum __Sent`

```fern
<<45 enum __Sent>>=
enum __Sent { Whole, Partial, Failed }
```

### `46 function __serve_send[D: async.Driver](drv: D, c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns,...`

```fern
<<46 function __serve_send[D: async.Driver](drv: D, c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns,...>>=
function __serve_send[D: async.Driver](drv: D, c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns, __Sent) {
  match (c.tls[at]) {
    Secured(sess) => {
      c = __Conns { ...c, tls: c.tls.with(at, Plain) };
      match (sess.write(payload)) {
        Ok(step) => {
          return __serve_send_raw(drv, __Conns { ...c, tls: c.tls.with(at, Secured(step.session)) }, at, step.output, opts);
        },
        Err(_) => {
          return (c, Failed);
        }
      }
    },
    Lent => {
      match (__lent_sealed(c, at, payload)) {
        Some(sealed) => {
          return __serve_send_raw(drv, c, at, sealed, opts);
        },
        None => {
          return (c, Failed);
        }
      }
    },
    _ => {}
  }
  return __serve_send_raw(drv, c, at, payload, opts);
}

function __lent_closed(c: __Conns, at: i32): void {
  match (c.framing[at]) {
    Pulling(body) => {
      match (body.tls.get()) {
        Some(sess) => {
          body.tls.set(None);
          if let Ok(step) = sess.close() {
            tcp_send_bytes(c.fds[at], step.output);
          }
        },
        None => {}
      }
    },
    _ => {}
  }
}

function __lent_sealed(c: __Conns, at: i32, payload: u8[]): Option[u8[]] {
  match (c.framing[at]) {
    Pulling(body) => {
      match (body.tls.get()) {
        Some(sess) => {
          match (sess.write(payload)) {
            Ok(step) => {
              body.tls.set(Some(step.session));
              return Some(step.output);
            },
            Err(_) => {
              return None;
            }
          }
        },
        None => {
          return Some(payload);
        }
      }
    },
    _ => {
      return Some(payload);
    }
  }
  return None;
}

function __serve_send_raw[D: async.Driver](drv: D, c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns, __Sent) {
  let n: i32 = tcp_send_bytes(c.fds[at], payload);
  if (n >= payload.len()) {
    return (c, Whole);
  }
  if (n < 0 && n != 0 - __eagain()) {
    return (c, Failed);
  }
  let rest: u8[] = payload;
  if (n > 0) {
    rest = __bytes_from(payload, n);
  }
  return (__serve_pend(drv, c, at, rest, opts), Partial);
}

function __serve_send_out[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  match (c.tls[at]) {
    Secured(sess) => {
      return __serve_send(drv, c, at, buf_take_bytes(c.out), opts);
    },
    _ => {}
  }
  let n: i32 = tcp_send_buf(c.fds[at], c.out, 0);
  if (n >= buf_len(c.out)) {
    buf_clear(c.out);
    return (c, Whole);
  }
  if (n < 0 && n != 0 - __eagain()) {
    buf_clear(c.out);
    return (c, Failed);
  }
  let rest: u8[] = buf_take_bytes(c.out);
  if (n > 0) {
    rest = __bytes_from(rest, n);
  }
  return (__serve_pend(drv, c, at, rest, opts), Partial);
}

function __pending_interest(c: __Conns, at: i32): i32 {
  match (c.framing[at]) {
    Http2(conn) => {
      return __read_interest(c, c.tls[at]) | 2;
    },
    _ => {
      return 2;
    }
  }
  return 2;
}

function __read_interest(c: __Conns, tls: __Tls): i32 {
  if let Plain = tls {
    return 9;
  }
  return c.tls_reads;
}

function __tls_reads(opts: Config): i32 {
  if (__ktls_asked(opts)) {
    return 1;
  }
  return 9;
}
```

### `47 function __serve_pend[D: async.Driver](drv: D, c: __Conns, at: i32, rest: u8[], opts: Config): __Conns`

```fern
<<47 function __serve_pend[D: async.Driver](drv: D, c: __Conns, at: i32, rest: u8[], opts: Config): __Conns>>=
function __serve_pend[D: async.Driver](drv: D, c: __Conns, at: i32, rest: u8[], opts: Config): __Conns {
  drv.watch(c.fds[at], __pending_interest(c, at));
  let first: boolean = c.pending[at].len() == 0;
  return __drain_deadline(__Conns { ...c, pending: c.pending.with(at, rest) }, at, opts, first);
}

function __serve_produce[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  if (!__in_flight(c, at)) {
    return (c, Whole);
  }
  if (__tail_parked(c, at)) {
    return (c, Partial);
  }
  let zc: (__Conns, __Sent, boolean) = __zc_tail_send(drv, c, at, opts);
  if (zc.2) {
    return (zc.0, zc.1);
  }
  let moved: (__Conns, __Sent, boolean) = __file_tail_send(drv, zc.0, at, opts);
  c = moved.0;
  if (moved.2) {
    return (c, moved.1);
  }
  let produced: (__Conns, u8[], boolean) = __tail_piece(drv, c, at);
  c = produced.0;
  if (!produced.2) {
    return (c, Failed);
  }
  if (__tail_parked(c, at)) {
    return (c, Partial);
  }
  let sent: (__Conns, __Sent) = __serve_send(drv, c, at, produced.1, opts);
  c = sent.0;
  match (sent.1) {
    Whole => {},
    _ => {
      return (c, sent.1);
    }
  }
  if (!__in_flight(c, at)) {
    return (c, Whole);
  }
  drv.watch(c.fds[at], __pending_interest(c, at));
  return (c, Partial);
}

function __serve_tail_wire(c: __Conns, at: i32, method: string, version: string, resp: HttpResponse, keep_alive: boolean, clk: __Clock): __Tail {
  match (__zc_wire(c, at, method, version, resp, keep_alive, clk)) {
    Some(t) => {
      return t;
    },
    None => {}
  }
  return __serve_wire(c.out, method, version, resp, keep_alive, clk);
}

function __zc_wire(c: __Conns, at: i32, method: string, version: string, resp: HttpResponse, keep_alive: boolean, clk: __Clock): Option[__Tail] {
  if (c.zc_bytes <= 0 || method == "HEAD" || http.http_bodiless(resp.status) || !__zc_conn(c, at)) {
    return None;
  }
  if let BodyBytes(body) = resp.body {
    if (body.len() < c.zc_bytes || !http.http_fields_ok(resp.headers) || !http.http_fields_ok(resp.trailers)) {
      return None;
    }
    let date: string = clk.line;
    if (resp.headers.has("date")) {
      date = "";
    }
    http.http_serialize_response_head_sized_into(c.out, version, resp, keep_alive, date, body.len());
    return Some(ZcTail(body, 0, 0 - 1));
  }
  return None;
}

function __zc_conn(c: __Conns, at: i32): boolean {
  if let Plain = c.tls[at] {
    if let Http1 = c.framing[at] {
      return true;
    }
  }
  return false;
}

function __zc_tail_send[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): (__Conns, __Sent, boolean) {
  if let ZcTail(body, from, slot) = c.tails[at] {
    let landed: (__Conns, i32, boolean) = __zc_landed(drv, c, at, from, slot);
    let off: i32 = landed.1;
    if (off == 0 - 115) {
      return (landed.0, Partial, true);
    }
    if (off >= body.len()) {
      return (__Conns { ...landed.0, tails: landed.0.tails.with(at, NoTail) }, Whole, true);
    }
    if (landed.2) {
      return __zc_copy(drv, landed.0, at, body, off, opts);
    }
    return __zc_write(drv, landed.0, at, body, off, opts);
  }
  return (c, Whole, false);
}

function __zc_landed[D: async.Driver](drv: D, c: __Conns, at: i32, from: i32, slot: i32): (__Conns, i32, boolean) {
  if (slot < 0) {
    return (c, from, false);
  }
  let r: i32 = drv.zc_result(slot);
  if (r == 0 - 115) {
    return (c, r, false);
  }
  c = __zc_settled(c, slot);
  if (r == 0 - 95 || r == 0 - 22) {
    return (__Conns { ...c, zc_bytes: 0 }, from, true);
  }
  if (r < 0) {
    return (c, from, true);
  }
  return (c, from + r, false);
}

function __zc_write[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Conns, __Sent, boolean) {
  if (c.zc_bytes > 0) {
    let s: i32 = drv.send_zc(c.fds[at], body, off);
    if (s >= 0) {
      drv.watch(c.fds[at], 8);
      c = __drain_deadline(c, at, opts, true);
      return (__Conns { ...c, tails: c.tails.with(at, ZcTail(body, off, s)), zc_held: c.zc_held.append(body), zc_slots: c.zc_slots.append(s), zc_free: c.zc_free.append(false) }, Partial, true);
    }
    if (s == 0 - 95) {
      c = __Conns { ...c, zc_bytes: 0 };
    }
  }
  return __zc_copy(drv, c, at, body, off, opts);
}
```

### `48 function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Co...`

```fern
<<48 function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Co...>>=
function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Conns, __Sent, boolean) {
  let sent: (__Conns, __Sent) = __serve_send(drv, __Conns { ...c, tails: c.tails.with(at, NoTail) }, at, __bytes_from(body, off), opts);
  return (sent.0, sent.1, true);
}

function __zc_settled(c: __Conns, slot: i32): __Conns {
  let i: i32 = 0;
  while (i < c.zc_slots.len()) {
    if (c.zc_slots[i] == slot && !c.zc_free[i]) {
      return __Conns { ...c, zc_free: c.zc_free.with(i, true) };
    }
    i = i + 1;
  }
  return c;
}

function __zc_reclaim[D: async.Driver](drv: D, c: __Conns): __Conns {
  if (c.zc_held.len() == 0) {
    return c;
  }
  let held: u8[][] = [];
  let slots: i32[] = [];
  let free: boolean[] = [];
  let i: i32 = 0;
  while (i < c.zc_held.len()) {
    if (c.zc_free[i] && drv.zc_notified(c.zc_slots[i])) {
      drv.zc_release(c.zc_slots[i]);
    } else {
      held = held.append(c.zc_held[i]);
      slots = slots.append(c.zc_slots[i]);
      free = free.append(c.zc_free[i]);
    }
    i = i + 1;
  }
  return __Conns { ...c, zc_held: held, zc_slots: slots, zc_free: free };
}

function __tail_parked(c: __Conns, at: i32): boolean {
  match (c.tails[at]) {
    ParkedTail(_, _, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __file_tail_send[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): (__Conns, __Sent, boolean) {
  match (c.tls[at]) {
    Secured(sess) => {
      return (c, Whole, false);
    },
    _ => {}
  }
  match (c.tails[at]) {
    FileTail(r, left, after) => {
      let want: i32 = 65536;
      if (left < want as i64) {
        want = left as i32;
      }
      let n: i32 = tcp_sendfile(c.fds[at], r.fd, want);
      if (n == 0 - __enotsup()) {
        return (c, Whole, false);
      }
      if (n == 0 - __eagain()) {
        drv.watch(c.fds[at], __pending_interest(c, at));
        return (__drain_deadline(c, at, opts, true), Partial, true);
      }
      if (n <= 0) {
        return (__tail_drop(c, at), Failed, true);
      }
      let rest: i64 = left - n as i64;
      if (rest <= 0 as i64) {
        return __sent_range(drv, c, at, __after_range(r, after));
      }
      drv.watch(c.fds[at], __pending_interest(c, at));
      return (__Conns { ...c, tails: c.tails.with(at, FileTail(r, rest, after)) }, Partial, true);
    },
    _ => {
      return (c, Whole, false);
    }
  }
  return (c, Whole, false);
}

function __sent_range[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[__Tail]): (__Conns, __Sent, boolean) {
  match (next) {
    Some(t) => {
      let moved: __Conns = __Conns { ...c, tails: c.tails.with(at, t) };
      match (t) {
        NoTail => {
          return (moved, Whole, true);
        },
        _ => {
          drv.watch(c.fds[at], __pending_interest(c, at));
          return (moved, Partial, true);
        }
      }
    },
    None => {
      return (__Conns { ...c, tails: c.tails.with(at, NoTail) }, Failed, true);
    }
  }
  return (c, Failed, true);
}

function __drain_deadline(c: __Conns, at: i32, opts: Config, first: boolean): __Conns {
  let now: i64 = monotonic_ns();
  let since: i64 = c.send_since[at];
  let base: i32 = c.send_base[at];
  let queued: i32 = tcp_socket_ctl(c.fds[at], 6, 0);
  if (queued < 0) {
    queued = 0;
  }
  let outstanding: i32 = c.pending[at].len() + queued + __tail_left(c, at);
  if (first) {
    since = now;
    base = outstanding;
  }
  let deadline: i64 = now + opts.recv_deadline.as_nanos();
  if (opts.response_min_data_rate > 0) {
    deadline = since + opts.response_data_rate_grace.as_nanos() + (base - outstanding) as i64 * (1000000000 as i64) / (opts.response_min_data_rate as i64);
  }
  return __Conns { ...c, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline), send_since: c.send_since.with(at, since), send_base: c.send_base.with(at, base) };
}
```

### `49 const LISTENER_INTEREST: i32`

```fern
<<49 const LISTENER_INTEREST: i32>>=
const LISTENER_INTEREST: i32 = 5;
```

### `50 function __stop_begin(stop: __Stop, sig: i32): __Stop`

```fern
<<50 function __stop_begin(stop: __Stop, sig: i32): __Stop>>=
function __stop_begin(stop: __Stop, sig: i32): __Stop {
  if (__serve_stopping(stop)) {
    return stop;
  }
  return __Stop { ...stop, at: monotonic_ns(), sig: sig };
}

function __serve_stop_sources(drv: async.RealDriver, opts: Config): __Stop {
  drv.watch_signal(15);
  drv.watch_signal(2);
  if (opts.stop_with_parent && drv.watch_parent() == 0 - 3) {
    return __stop_begin(__stop_none(), 15);
  }
  return __stop_none();
}

function __stop_signal(fd: i32): i32 {
  if (fd == 0 - 15 || fd == 0 - 2) {
    return 0 - fd;
  }
  return 0;
}

function __serve_pass_end[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, stop: __Stop, accepting: boolean, parked: i32): (__Conns, __Stop, boolean, boolean) {
  let now: i64 = monotonic_ns();
  c = __serve_expire(drv, c, opts, now);
  if (!__serve_stopping(stop)) {
    accepting = __serve_gate(drv, listener, c, opts, accepting, parked);
  }
  return __serve_stop_step(drv, listener, c, opts, stop, accepting, now, parked);
}
```

### `51 struct __Stop`

```fern
<<51 struct __Stop>>=
struct __Stop { at: i64, sig: i32, closed: boolean, aborted: boolean }
```

### `52 function __stop_none(): __Stop`

```fern
<<52 function __stop_none(): __Stop>>=
function __stop_none(): __Stop {
  return __Stop { at: 0 - 1 as i64, sig: 0, closed: false, aborted: false };
}

function __serve_stopping(stop: __Stop): boolean {
  return stop.at >= 0 as i64;
}

function __stop_timeout(stop: __Stop, opts: Config, now: i64, timeout: i32): i32 {
  if (!__serve_stopping(stop)) {
    return timeout;
  }
  let due: i64 = stop.at + opts.shutdown_grace.as_nanos();
  if (stop.closed) {
    due = stop.at + opts.drain_deadline.as_nanos();
  }
  let ms: i64 = (due - now + 999999 as i64) / 1000000;
  if (ms < 0 as i64) {
    ms = 0 as i64;
  }
  if (timeout >= 0 && timeout as i64 < ms) {
    return timeout;
  }
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  return ms as i32;
}

function __serve_stop_step[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, stop: __Stop, accepting: boolean, now: i64, parked: i32): (__Conns, __Stop, boolean, boolean) {
  if (!__serve_stopping(stop)) {
    return (c, stop, accepting, false);
  }
  if (!stop.closed) {
    if (now < stop.at + opts.shutdown_grace.as_nanos()) {
      return (c, stop, accepting, false);
    }
    if (accepting) {
      drv.unwatch(listener);
    }
    tcp_close(listener);
    stop = __Stop { ...stop, closed: true };
    accepting = false;
  }
  let past_drain: boolean = now >= stop.at + opts.drain_deadline.as_nanos();
  let swept: (__Conns, __Stop) = __stop_sweep(drv, c, stop, past_drain);
  return (swept.0, swept.1, accepting, __stop_done(swept.0, parked, past_drain));
}

function __stop_done(c: __Conns, parked: i32, past_drain: boolean): boolean {
  return c.open == 0 && (parked == 0 || past_drain);
}

function __stop_sweep[D: async.Driver](drv: D, c: __Conns, stop: __Stop, past_drain: boolean): (__Conns, __Stop) {
  let i: i32 = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] >= 0) {
      let idle: boolean = c.bufs[i].len() == 0 && !__in_flight(c, i) && !c.backlog[i];
      if (idle || past_drain) {
        if (!idle) {
          stop = __Stop { ...stop, aborted: true };
        }
        c = __serve_close(drv, c, i);
      }
    }
    i = i + 1;
  }
  return (c, stop);
}

function __stop_code(stop: __Stop): i32 {
  if (stop.aborted) {
    return 1;
  }
  return 0;
}

function __stop_reason(stop: __Stop): string {
  if (stop.aborted) {
    return "drain-deadline";
  }
  if (stop.sig == 2) {
    return "sigint";
  }
  return "sigterm";
}

function __serve_not_ready(req: HttpRequest, opts: Config, stop: __Stop): boolean {
  return __serve_stopping(stop) && opts.readiness_path.len() > 0 && req.path == opts.readiness_path;
}

function __serve_gate[D: async.Driver](drv: D, listener: i32, c: __Conns, opts: Config, accepting: boolean, parked: i32): boolean {
  let cap: i32 = opts.max_connections;
  if (cap < 1) {
    cap = 1;
  }
  let full: boolean = c.open >= cap || parked >= opts.max_in_flight;
  if (accepting && full) {
    drv.unwatch(listener);
    return false;
  }
  if (!accepting && !full) {
    return drv.watch(listener, LISTENER_INTEREST) >= 0;
  }
  return accepting;
}

function __serve_expire[D: async.Driver](drv: D, c: __Conns, opts: Config, now: i64): __Conns {
  if (c.due < 0 as i64 || c.due > now) {
    return c;
  }
  let due: i64 = 0 - 1 as i64;
  let i: i32 = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] >= 0 && !c.backlog[i]) {
      if (c.deadlines[i] <= now && __in_flight(c, i)) {
        c = __drain_deadline(c, i, opts, false);
      }
      if (c.deadlines[i] <= now) {
        c = __serve_close(drv, c, i);
      } else {
        due = __sooner(due, c.deadlines[i]);
      }
    }
    i = i + 1;
  }
  return __Conns { ...c, due: due };
}
```

### `53 struct __Body`

```fern
<<53 struct __Body>>=
struct __Body { state: Cell[i32], rest: Cell[u8[]], tls: Cell[Option[server.Session]] }

struct __Pull { fd: i32, left: Cell[i32], dstate: Cell[i32], dheld: Cell[u8[]], dleft: Cell[i32], dbudget: Cell[i32], droom: Cell[i32], pending: Cell[u8[]], since: Cell[i64], got: Cell[i32], continue_owed: Cell[i32], opts: Config, body: __Body, fault: Cell[i32] }
```

### `54 function __body_source(fd: i32, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource`

```fern
<<54 function __body_source(fd: i32, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource>>=
function __body_source(fd: i32, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource {
  let fault: Cell[i32] = cell_new(0);
  let owed: i32 = 0;
  if (head.expects_continue && initial.len() == 0) {
    owed = 1;
  }
  let empty: u8[] = [];
  let p: __Pull = __Pull { fd: fd, left: cell_new(head.length), dstate: cell_new(0), dheld: cell_new(empty), dleft: cell_new(0), dbudget: cell_new(opts.limits.header_bytes), droom: cell_new(opts.limits.body), pending: cell_new(initial), since: cell_new(0 as i64), got: cell_new(0), continue_owed: cell_new(owed), opts: opts, body: body, fault: fault };
  return BodySource { next: (): Option[u8[]] => __body_next(p), fault: fault, send: (bytes: u8[]): i32 => __pull_write(p, bytes) };
}

function __pull_write(p: __Pull, bytes: u8[]): i32 {
  let wire: u8[] = bytes;
  match (p.body.tls.get()) {
    Some(sess) => {
      match (sess.write(bytes)) {
        Ok(step) => {
          p.body.tls.set(Some(step.session));
          wire = step.output;
        },
        Err(_) => {
          return 0 - 71;
        }
      }
    },
    None => {}
  }
  let at: i32 = 0;
  while (at < wire.len()) {
    let n: i32 = tcp_send_bytes(p.fd, __bytes_from(wire, at));
    if (n > 0) {
      at = at + n;
      continue;
    }
    if (n != 0 - __eagain()) {
      return n;
    }
    let set: i32[] = [p.fd, 2];
    let woke: i32 = async.wait_any(set, __idle_ms(p.opts));
    if (woke == async.cancelled()) {
      return 0 - 125;
    }
    if (woke < 0) {
      return 0 - 110;
    }
  }
  return bytes.len();
}

function __idle_ms(opts: Config): i32 {
  let ms: i64 = opts.keep_alive_idle.as_millis();
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  if (ms <= 0 as i64) {
    return 1;
  }
  return ms as i32;
}

function __body_next(p: __Pull): Option[u8[]] {
  if (p.body.state.get() != 0) {
    return None;
  }
  if (p.continue_owed.get() == 1) {
    p.continue_owed.set(0);
    __pull_send(p, "HTTP/1.1 100 Continue\r\n\r\n".bytes());
  }
  while (p.body.state.get() == 0) {
    let bytes: u8[] = __body_take(p);
    if (bytes.len() > 0) {
      let out: u8[] = __body_decode(p, bytes);
      if (out.len() > 0) {
        return Some(out);
      }
    }
  }
  return None;
}

function __body_take(p: __Pull): u8[] {
  let none: u8[] = [];
  let pending: u8[] = p.pending.get();
  if (pending.len() > 0) {
    p.pending.set(none);
    return pending;
  }
  while (true) {
    let wait_ms: i32 = __body_wait_ms(p);
    if (wait_ms <= 0) {
      return __body_fault(p, 408);
    }
    let set: i32[] = [p.fd, 1];
    let woke: i32 = async.wait_any(set, wait_ms);
    if (woke == async.cancelled()) {
      return __body_fault(p, async.cancelled());
    }
    if (woke < 0) {
      return __body_fault(p, 408);
    }
    match (__pull_chunk(p)) {
      Some(bytes) => {
        return bytes;
      },
      None => {}
    }
  }
  return none;
}

function __pull_chunk(p: __Pull): Option[u8[]] {
  let scratch: u8[] = __alloc_u8(__body_recv_size(p));
  let n: i32 = tcp_recv_into(p.fd, scratch);
  if (n > 0) {
    p.got.set(p.got.get() + n);
    let data: u8[] = __pull_read(p, scratch.take(n));
    if (data.len() > 0 || p.body.state.get() != 0) {
      return Some(data);
    }
    return None;
  }
  if (n != 0 - __eagain()) {
    return Some(__body_fault(p, 400));
  }
  return None;
}
```

### `55 function __pull_read(p: __Pull, raw: u8[]): u8[]`

```fern
<<55 function __pull_read(p: __Pull, raw: u8[]): u8[]>>=
function __pull_read(p: __Pull, raw: u8[]): u8[] {
  match (p.body.tls.get()) {
    Some(sess) => {
      match (__pull_opened(p, sess, raw)) {
        Some(data) => {
          return data;
        },
        None => {
          return __body_fault(p, 400);
        }
      }
    },
    None => {
      return raw;
    }
  }
  return raw;
}

function __pull_opened(p: __Pull, sess: server.Session, raw: u8[]): Option[u8[]] {
  match (sess.read(raw, now_unix_ms())) {
    Ok(step) => {
      p.body.tls.set(Some(step.session));
      if (step.output.len() > 0) {
        tcp_send_bytes(p.fd, step.output);
      }
      if (step.closed) {
        return None;
      }
      return Some(step.data);
    },
    Err(f) => {
      tcp_send_bytes(p.fd, f.alert);
      return None;
    }
  }
  return None;
}

function __pull_send(p: __Pull, bytes: u8[]): void {
  match (p.body.tls.get()) {
    Some(sess) => {
      if let Ok(step) = sess.write(bytes) {
        p.body.tls.set(Some(step.session));
        tcp_send_bytes(p.fd, step.output);
      }
    },
    None => {
      tcp_send_bytes(p.fd, bytes);
    }
  }
}

function __body_wait_ms(p: __Pull): i32 {
  if (p.left.get() == 0 - 2) {
    return __idle_ms(p.opts);
  }
  let now: i64 = monotonic_ns();
  if (p.since.get() == 0 as i64) {
    p.since.set(now);
  }
  let earned: i64 = p.since.get() + p.opts.data_rate_grace.as_nanos();
  if (p.opts.min_data_rate > 0) {
    earned = earned + p.got.get() as i64 * (1000000000 as i64) / (p.opts.min_data_rate as i64);
  }
  if (earned <= now) {
    return 0;
  }
  let ms: i64 = (earned - now + 999999 as i64) / (1000000 as i64);
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  return ms as i32;
}

function __body_recv_size(p: __Pull): i32 {
  let left: i32 = p.left.get();
  if (left >= 0 && left < 4096) {
    return left;
  }
  return 4096;
}

function __body_decode(p: __Pull, bytes: u8[]): u8[] {
  let left: i32 = p.left.get();
  if (left == 0 - 2) {
    return bytes;
  }
  if (left < 0) {
    return __body_decode_chunked(p, bytes);
  }
  if (bytes.len() < left) {
    p.left.set(left - bytes.len());
    return bytes;
  }
  p.left.set(0);
  p.body.state.set(1);
  if (bytes.len() == left) {
    return bytes;
  }
  p.body.rest.set(__bytes_from(bytes, left));
  return bytes.take(left);
}

function __body_decode_chunked(p: __Pull, bytes: u8[]): u8[] {
  let d: http.ChunkDecoder = http.ChunkDecoder { state: p.dstate.get(), held: p.dheld.get(), left: p.dleft.get(), budget: p.dbudget.get(), room: p.droom.get(), fields: p.opts.limits.header_fields, trailers: HeaderMap { names: [], values: [] } };
  match (d.feed(bytes)) {
    http.ChunkData(next, data) => {
      p.dstate.set(next.state);
      p.dheld.set(next.held);
      p.dleft.set(next.left);
      p.dbudget.set(next.budget);
      p.droom.set(next.room);
      return data;
    },
    http.ChunkEnd(data, trailers, rest) => {
      p.body.rest.set(rest);
      p.body.state.set(1);
      return data;
    },
    http.ChunkRefused(status) => {
      return __body_fault(p, status);
    }
  }
  return __body_fault(p, 400);
}

function __body_fault(p: __Pull, status: i32): u8[] {
  let none: u8[] = [];
  p.fault.set(status);
  p.body.state.set(2);
  return none;
}
```

### `56 function __body_live(fl: __Flight): boolean`

```fern
<<56 function __body_live(fl: __Flight): boolean>>=
function __body_live(fl: __Flight): boolean {
  match (fl.body) {
    Some(b) => {
      return b.state.get() == 0;
    },
    None => {
      return false;
    }
  }
  return false;
}

function __body_settled[D: async.Driver](drv: D, c: __Conns, at: i32, body: __Body): __Conns {
  c = __tls_back(__Conns { ...c, framing: c.framing.with(at, Http1) }, at, body);
  let rest: u8[] = body.rest.get();
  if (rest.len() > 0) {
    c = __Conns { ...c, bufs: c.bufs.with(at, rest.concat(__bytes_from(c.bufs[at], c.offs[at]))), offs: c.offs.with(at, 0) };
  }
  drv.watch(c.fds[at], __read_interest(c, c.tls[at]));
  if (body.state.get() != 1) {
    return __Conns { ...c, keep: c.keep.with(at, false) };
  }
  return c;
}

function __tls_back(c: __Conns, at: i32, body: __Body): __Conns {
  match (body.tls.get()) {
    Some(sess) => {
      body.tls.set(None);
      return __Conns { ...c, tls: c.tls.with(at, Secured(sess)) };
    },
    None => {
      return c;
    }
  }
  return c;
}

function __tls_regained(c: __Conns, at: i32, fl: __Flight): __Conns {
  match (fl.body) {
    Some(b) => {
      return __tls_back(c, at, b);
    },
    None => {
      return c;
    }
  }
  return c;
}

function __expecting(behind: __Behind): boolean {
  match (behind) {
    Expecting => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

function __serve_unframed[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, expecting: boolean, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, count: i32, clk: __Clock, eof: boolean): (__Conns, __Flight[], boolean, __Behind, http.HttpFramed) {
  if (!opts.stream_bodies || eof) {
    if (expecting) {
      c = __serve_continue(drv, c, at, opts);
    } else if (!eof) {
      c = __body_deadline(c, at, opts, monotonic_ns());
    }
    return (c, fls, false, Idle, c.last);
  }
  match (http.http_parse_request_head_from(c.bufs[at], c.offs[at], opts.limits, c.last.request)) {
    http.Headed(h) => {
      return __serve_stream(drv, c, fls, at, h, start, opts, stop, count, clk);
    },
    http.HeadIncomplete => {
      return (__body_deadline(c, at, opts, monotonic_ns()), fls, false, Idle, c.last);
    },
    http.HeadMalformed(status) => {
      return (__serve_refuse(drv, c, at, status, opts, clk), fls, false, Idle, c.last);
    }
  }
  return (c, fls, false, Idle, c.last);
}

function __upgrade_head(f: http.HttpFramed): http.HttpHead {
  return http.HttpHead { request: f.request, len: f.len, keep_alive: f.keep_alive, version: f.version, length: 0 - 2, expects_continue: false };
}

function __serve_stream[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, h: http.HttpHead, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, count: i32, clk: __Clock): (__Conns, __Flight[], boolean, __Behind, http.HttpFramed) {
  let fd: i32 = c.fds[at];
  let buf: u8[] = c.bufs[at];
  let empty: u8[] = [];
  let body: __Body = __Body { state: cell_new(0), rest: cell_new(empty), tls: cell_new(None) };
  match (c.tls[at]) {
    Secured(sess) => {
      body.tls.set(Some(sess));
      c = __Conns { ...c, tls: c.tls.with(at, Lent) };
    },
    _ => {}
  }
  let src: BodySource = __body_source(fd, h, __bytes_from(buf, c.offs[at] + h.len), opts, body);
  let req: HttpRequest = HttpRequest { ...h.request, body: stream.stream_from_source(src) };
  let served: i32 = c.served[at] + 1;
  let keep: boolean = h.keep_alive && served < opts.keep_alive_requests && !__serve_stopping(stop);
  let due: i64 = __backlog_due(c, at);
  let queued: i32[] = __unqueued(c.queued, c.fds[at], c.backlog[at]);
  c = __Conns { ...c, offs: c.offs.with(at, buf.len()), served: c.served.with(at, served), due: due, queued: queued, backlog: c.backlog.with(at, false), continued: c.continued.with(at, false), keep: c.keep.with(at, keep), framing: c.framing.with(at, Pulling(body)) };
  let f: http.HttpFramed = http.HttpFramed { request: req, len: 0, keep_alive: keep, version: h.version };
  c = __serve_started(c, at);
  match (start(f, keep, stop, c.hosts[at])) {
    HandlerDone(resp) => {
      if (__upgrading(resp)) {
        let upgraded: (__Conns, boolean) = __serve_respond(drv, c, at, h.request.method, h.version, resp, false, opts, count + 1, Idle, clk);
        return (upgraded.0, fls, false, Idle, c.last);
      }
      c = __body_settled(drv, c, at, body);
      if (h.length == 0 - 2) {
        c = __Conns { ...c, keep: c.keep.with(at, keep) };
      }
      let parsed: (__Behind, http.HttpFramed) = __serve_behind(http.http_parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last);
      let answered: (__Conns, boolean) = __serve_respond(drv, c, at, h.request.method, h.version, resp, c.keep[at], opts, count + 1, parsed.0, clk);
      return (answered.0, fls, answered.1, parsed.0, parsed.1);
    },
    HandlerParked(fl) => {
      let parked: (__Conns, __Flight[]) = __serve_park(drv, c, fls, at, __flight_watch(drv, __Flight { ...fl, body: Some(body) }, monotonic_ns()), opts);
      return (parked.0, parked.1, false, Idle, c.last);
    }
  }
  return (c, fls, false, Idle, c.last);
}
```

### `57 struct __Flight`

```fern
<<57 struct __Flight>>=
struct __Flight { job: __Job, wait: async.Wait, fd: i32, set: i32[], due: i64, method: string, version: string, kept: boolean, behind: __Behind, body: Option[__Body], idle: i64, ends: i64, stream: i32, since: i64, grpc: boolean, quic: i32 }

enum __Job { Handler(async.Call[HttpRequest, platform.Host, HttpResponse]), Producer(async.Task[Option[u8[]]]), Background(async.Task[i32]) }

enum __Started { HandlerDone(HttpResponse), HandlerParked(__Flight) }
```

### `58 function __started_again(s: __Started): __Started`

```fern
<<58 function __started_again(s: __Started): __Started>>=
function __started_again(s: __Started): __Started {
  match (s) {
    HandlerDone(resp) => {
      return HandlerDone(resp);
    },
    HandlerParked(fl) => {
      return HandlerParked(fl);
    }
  }
  return s;
}

function __serve_start(handler: (HttpRequest, platform.Host) => HttpResponse, plat: platform.Host, req: HttpRequest, version: string, kept: boolean, behind: __Behind, opts: Config, stop: __Stop): __Started {
  if (__serve_not_ready(req, opts, stop)) {
    return HandlerDone(http.text(503, ""));
  }
  let held: (platform.Host, i64) = __request_host(plat, req, opts);
  plat = held.0;
  let ends: i64 = held.1;
  match (async.call_start(handler, req, plat)) {
    Done(resp) => {
      return HandlerDone(__in_time(resp, ends, plat.elapsed_ns(), grpc.is_grpc(req)));
    },
    Suspended(w) => {
      let none: i32[] = [];
      return HandlerParked(__Flight { job: Handler(async.call_of(w, handler, req, plat)), wait: w, fd: 0, set: none, due: 0 - 1 as i64, method: req.method, version: version, kept: kept, behind: behind, body: None, idle: 0 - 1 as i64, ends: ends, stream: 0, since: 0 - 1 as i64, grpc: grpc.is_grpc(req), quic: 0 });
    },
    Cancelled => {}
  }
  return HandlerDone(http.text(503, ""));
}

function __bound_ends(bound: Duration, now: i64): i64 {
  let span: i64 = bound.as_nanos();
  if (span <= 0 as i64) {
    return 0 - 1 as i64;
  }
  return now + span;
}

function __in_time(resp: HttpResponse, ends: i64, now: i64, is_grpc: boolean): HttpResponse {
  if (ends >= 0 as i64 && now >= ends) {
    return __late(is_grpc);
  }
  return resp;
}

function __late(is_grpc: boolean): HttpResponse {
  if (is_grpc) {
    return grpc.error(grpc.DEADLINE_EXCEEDED, "deadline exceeded");
  }
  return http.text(503, "");
}

function __serve_step[S](handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), st: S, req: HttpRequest, plat: platform.Host, opts: Config): (S, HttpResponse) {
  let held: (platform.Host, i64) = __request_host(plat, req, opts);
  let stepped: (S, HttpResponse) = handler(st, req, held.0);
  return (stepped.0, __in_time(stepped.1, held.1, held.0.elapsed_ns(), grpc.is_grpc(req)));
}

function __serve_started(c: __Conns, at: i32): __Conns {
  if (c.rate_since[at] >= 0 as i64) {
    return c;
  }
  return __Conns { ...c, rate_since: c.rate_since.with(at, monotonic_ns()), rate_base: c.rate_base.with(at, c.bufs[at].len()) };
}

function __request_ends(req: HttpRequest, opts: Config, now: i64): i64 {
  let ends: i64 = __bound_ends(opts.handler_deadline, now);
  match (grpc.timeout(req.headers)) {
    Some(d) => {
      let own: i64 = now + d.as_nanos();
      if (ends < 0 as i64 || own < ends) {
        return own;
      }
      return ends;
    },
    None => {
      return ends;
    }
  }
  return ends;
}

function __request_host(plat: platform.Host, req: HttpRequest, opts: Config): (platform.Host, i64) {
  let ends: i64 = __request_ends(req, opts, plat.elapsed_ns());
  if (ends >= 0 as i64) {
    plat = plat.with_deadline(ends);
  }
  if (opts.trace) {
    let host: platform.Host = plat;
    plat = plat.with_trace(trace.continue_from(req.headers, (): i32 => host.random_i32()));
  }
  return (plat, ends);
}

function __serve_park[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], at: i32, fl: __Flight, opts: Config): (__Conns, __Flight[]) {
  let fd: i32 = c.fds[at];
  if (buf_len(c.out) > 0) {
    let sent: (__Conns, __Sent) = __serve_send_out(drv, c, at, opts);
    c = sent.0;
    match (sent.1) {
      Failed => {
        __flight_cancel(drv, fl);
        return (__serve_close(drv, c, at), fls);
      },
      _ => {}
    }
  }
  return (__Conns { ...c, deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()) }, fls.append(__Flight { ...fl, fd: fd, since: c.rate_since[at] }));
}

function __held_deadline(): i64 {
  let g: i64 = 1000000000 as i64;
  return g * g * 9;
}
```

### `59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, fd: i32, readine...`

```fern
<<59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, fd: i32, readine...>>=
function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, fd: i32, readiness: i32, scratch: u8[], opts: Config): (__Conns, __Flight[]) {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return (c, fls);
  }
  if ((readiness & 4) != 0) {
    __flight_cancel(drv, fls[fi]);
    return (__serve_close(drv, c, at), __flights_remove(fls, fi));
  }
  if (__body_live(fls[fi])) {
    drv.unwatch(fd);
    return (c, fls);
  }
  let read: (__Conns, boolean) = __serve_read(drv, __tls_regained(c, at, fls[fi]), at, scratch, opts);
  c = __serve_compact(read.0, at);
  if (read.1) {
    c = __serve_parked_eof(c, fd);
  }
  return (__Conns { ...c, deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()) }, fls);
}

function __serve_settle[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fd: i32, eof: boolean, opts: Config, clk: __Clock): __Conns {
  if (eof && __flight_at(fls, fd) >= 0) {
    return __serve_parked_eof(__serve_finish(drv, c, fd, false, opts, clk), fd);
  }
  return __serve_finish(drv, c, fd, eof, opts, clk);
}

function __serve_parked_eof(c: __Conns, fd: i32): __Conns {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  return __Conns { ...c, keep: c.keep.with(at, false) };
}

function __flight_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fi: i32, ready: i32, opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  let fl: __Flight = fls[fi];
  __flight_unwatch(drv, fl, ready);
  fls = __flights_remove(fls, fi);
  match (fl.job) {
    Handler(call) => {
      return __handler_wake(drv, c, fls, fl, call, ready, opts, clk);
    },
    Producer(t) => {
      if (fl.quic > 0) {
        return __h3_producer_wake(drv, c, fls, fl, t, ready);
      }
      if (fl.stream > 0) {
        return __h2_producer_wake(drv, c, fls, fl, t, ready, opts);
      }
      if (__conns_index(c, fl.fd) < 0) {
        let gone: async.TaskStatus[Option[u8[]]] = async.task_cancel(t);
        async.task_free(t);
        return (c, fls);
      }
      match (async.task_resume(t, ready)) {
        Done(chunk) => {
          async.task_free(t);
          return (__producer_done(drv, c, fl.fd, chunk, opts, clk), fls);
        },
        Suspended(w) => {
          return (c, fls.append(__flight_watch(drv, __Flight { ...fl, wait: w }, monotonic_ns())));
        },
        Cancelled => {
          async.task_free(t);
          return (__serve_abandon(drv, c, fl.fd), fls);
        }
      }
    },
    Background(t) => {
      return (c, __background_wake(drv, fls, fl, t, ready));
    }
  }
  return (c, fls);
}

function __handler_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, call: async.Call[HttpRequest, platform.Host, HttpResponse], ready: i32, opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  match (async.call_resume(call, ready)) {
    Done(resp) => {
      async.call_free(call);
      return __serve_answer(drv, c, fls, fl, __in_time(resp, fl.ends, monotonic_ns(), fl.grpc), opts, clk);
    },
    Suspended(w) => {
      return (c, fls.append(__flight_watch(drv, __Flight { ...fl, wait: w }, monotonic_ns())));
    },
    Cancelled => {
      async.call_free(call);
      if (fl.quic > 0) {
        return (__h3_abandon(c, fl), fls);
      }
      return (__serve_abandon(drv, c, fl.fd), fls);
    }
  }
  return (c, fls);
}

function __background_wake[D: async.Driver](drv: D, fls: __Flight[], fl: __Flight, t: async.Task[i32], ready: i32): __Flight[] {
  match (async.task_resume(t, ready)) {
    Suspended(w) => {
      return fls.append(__flight_watch(drv, __Flight { ...fl, wait: w }, monotonic_ns()));
    },
    _ => {
      async.task_free(t);
    }
  }
  return fls;
}

function __serve_answer[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, resp: HttpResponse, opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  if (fl.quic > 0) {
    return __h3_answer(drv, c, fls, fl, resp);
  }
  let at: i32 = __conns_index(c, fl.fd);
  if (at < 0) {
    return (c, fls);
  }
  if (fl.stream > 0) {
    return __h2_answer(drv, c, fls, at, fl, resp, opts);
  }
  let behind: __Behind = fl.behind;
  match (fl.body) {
    Some(b) => {
      if (__upgrading(resp)) {
        return (__serve_respond(drv, c, at, fl.method, fl.version, resp, false, opts, __pipeline_burst(), Idle, clk).0, fls);
      }
      c = __body_settled(drv, c, at, b);
      behind = __serve_behind(http.http_parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last).0;
    },
    None => {}
  }
  let answered: (__Conns, boolean) = __serve_respond(drv, c, at, fl.method, fl.version, resp, fl.kept && c.keep[at], opts, __pipeline_burst(), behind, clk);
  return (__serve_finish(drv, answered.0, fl.fd, false, opts, clk), fls);
}
```

### `60 function __upgrading(resp: HttpResponse): boolean`

```fern
<<60 function __upgrading(resp: HttpResponse): boolean>>=
function __upgrading(resp: HttpResponse): boolean {
  if (resp.status != 101) {
    return false;
  }
  match (resp.body) {
    BodyChunks(p) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __upgrade_request(req: HttpRequest): boolean {
  if (!req.headers.has("sec-websocket-key") || !req.headers.has("upgrade")) {
    return false;
  }
  for v in req.headers.get_all("connection") {
    for tok in v.split(",") {
      if (tok.trim().eq_ignore_ascii_case("upgrade")) {
        return true;
      }
    }
  }
  return false;
}

function __serve_abandon[D: async.Driver](drv: D, c: __Conns, fd: i32): __Conns {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  return __serve_close(drv, c, at);
}

function __flight_watch[D: async.Driver](drv: D, fl: __Flight, now: i64): __Flight {
  let w: async.Wait = fl.wait;
  let once: i32 = 0;
  if (drv.oneshot()) {
    once = async.ONESHOT;
  }
  let keeps: boolean = drv.keeps();
  let i: i32 = 0;
  while (i + 1 < w.set.len()) {
    let interest: i32 = w.set[i + 1];
    if (!__own_connection(fl, w.set[i]) && interest != fetch.MAIL_INTEREST) {
      if (!__kept_pair(keeps, interest)) {
        let watched: i32 = drv.watch(w.set[i], async.interest_bits(interest) | once);
      } else if ((interest & async.WATCHED) == 0) {
        let kept: i32 = drv.watch(w.set[i], async.interest_bits(interest));
      }
    }
    i = i + 2;
  }
  let due: i64 = 0 - 1 as i64;
  if (w.timeout_ms >= 0) {
    due = now + w.timeout_ms as i64 * 1000000;
  }
  return __Flight { ...fl, set: w.set, due: due };
}

function __flight_unwatch[D: async.Driver](drv: D, fl: __Flight, fired: i32): void {
  let spent: i32 = 0 - 1;
  if (drv.oneshot()) {
    spent = fired;
  }
  let keeps: boolean = drv.keeps();
  let i: i32 = 0;
  while (i + 1 < fl.set.len()) {
    if (!__own_connection(fl, fl.set[i]) && fl.set[i + 1] != fetch.MAIL_INTEREST && i / 2 != spent && !__kept_pair(keeps, fl.set[i + 1])) {
      let unwatched: i32 = drv.unwatch(fl.set[i]);
    }
    i = i + 2;
  }
}

function __kept_pair(keeps: boolean, interest: i32): boolean {
  return keeps && (interest & (async.KEEP | async.WATCHED)) != 0;
}

function __orphan_event[D: async.Driver](drv: D, pool: fetch.Idle, fd: i32): void {
  if (pool.armed(fd)) {
    let unwatched: i32 = drv.unwatch(fd);
    pool.disarm(fd);
  }
}

function __own_connection(fl: __Flight, fd: i32): boolean {
  return fl.stream > 0 && fd == fl.fd;
}

function __flight_cancel[D: async.Driver](drv: D, fl: __Flight): void {
  __flight_unwatch(drv, fl, 0 - 1);
  match (fl.job) {
    Handler(call) => {
      let st: async.TaskStatus[HttpResponse] = async.call_cancel(call);
      async.call_free(call);
    },
    Producer(t) => {
      let st: async.TaskStatus[Option[u8[]]] = async.task_cancel(t);
      async.task_free(t);
    },
    Background(t) => {
      let st: async.TaskStatus[i32] = async.task_cancel(t);
      async.task_free(t);
    }
  }
}

function __flights_cancel[D: async.Driver](drv: D, fls: __Flight[]): void {
  for fl in fls {
    __flight_cancel(drv, fl);
  }
}

function __flight_index(fls: __Flight[], fd: i32): i32 {
  let i: i32 = 0;
  while (i < fls.len()) {
    if (__flight_pair(fls[i], fd) >= 0) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}
```

### `61 function __flight_pair(fl: __Flight, fd: i32): i32`

```fern
<<61 function __flight_pair(fl: __Flight, fd: i32): i32>>=
function __flight_pair(fl: __Flight, fd: i32): i32 {
  let i: i32 = 0;
  while (i + 1 < fl.set.len()) {
    if (fl.set[i] == fd) {
      return i / 2;
    }
    i = i + 2;
  }
  return -1;
}

function __flight_at(fls: __Flight[], fd: i32): i32 {
  let i: i32 = 0;
  while (i < fls.len()) {
    if (fls[i].fd == fd) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __flights_remove(fls: __Flight[], at: i32): __Flight[] {
  let out: __Flight[] = [];
  let i: i32 = 0;
  while (i < fls.len()) {
    if (i != at) {
      out = out.append(fls[i]);
    }
    i = i + 1;
  }
  return out;
}

function __flights_timeout(fls: __Flight[], now: i64): i32 {
  let best: i64 = 0 - 1 as i64;
  for fl in fls {
    best = __sooner(best, fl.due);
    best = __sooner(best, fl.idle);
    best = __sooner(best, fl.ends);
  }
  if (best < 0 as i64) {
    return -1;
  }
  if (best <= now) {
    return 0;
  }
  let ms: i64 = (best - now + 999999 as i64) / 1000000;
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  return ms as i32;
}

function __sooner(a: i64, b: i64): i64 {
  if (b < 0 as i64) {
    return a;
  }
  if (a < 0 as i64 || b < a) {
    return b;
  }
  return a;
}

function __least_timeout(a: i32, b: i32): i32 {
  if (a < 0) {
    return b;
  }
  if (b < 0 || a < b) {
    return a;
  }
  return b;
}

function __flights_expire[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  let again: boolean = fls.len() > 0;
  while (again) {
    again = false;
    let now: i64 = monotonic_ns();
    let i: i32 = 0;
    while (i < fls.len() && !again) {
      if (fls[i].ends >= 0 as i64 && fls[i].ends <= now) {
        let late: (__Conns, __Flight[]) = __flight_overdue(drv, c, fls, i, opts, clk);
        c = late.0;
        fls = late.1;
        again = true;
      } else if (fls[i].due >= 0 as i64 && fls[i].due <= now) {
        let woke: (__Conns, __Flight[]) = __flight_wake(drv, c, fls, i, -1, opts, clk);
        c = woke.0;
        fls = woke.1;
        again = true;
      } else if (fls[i].idle >= 0 as i64 && fls[i].idle <= now) {
        let filled: (__Conns, __Flight[]) = __flight_filler(drv, c, fls, i, opts);
        c = filled.0;
        fls = filled.1;
        again = true;
      }
      i = i + 1;
    }
  }
  return (c, fls);
}

function __flight_overdue[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], i: i32, opts: Config, clk: __Clock): (__Conns, __Flight[]) {
  let late: __Flight = fls[i];
  __flight_cancel(drv, late);
  fls = __flights_remove(fls, i);
  match (late.job) {
    Handler(call) => {
      return __serve_answer(drv, c, fls, late, __late(late.grpc), opts, clk);
    },
    _ => {}
  }
  return (c, fls);
}

function __host_platform(reactor: i32, opts: Config, shared: boolean): platform.Host {
  let host: platform.Host = platform.host();
  if (reactor >= 0) {
    host = platform.host_on(reactor);
  }
  if (shared && async.parks()) {
    host = host.with_pool(fetch.idle_shared());
  }
  match (opts.http_allow) {
    Some(a) => {
      host = host.with_allow(a);
    },
    None => {}
  }
  if (opts.cookie_jar) {
    return host.with_jar(cookie.jar());
  }
  return host;
}
```

### `62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flight[], fd: i32, re...`

```fern
<<62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flight[], fd: i32, re...>>=
function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flight[], fd: i32, readiness: i32, opts: Config): (__Conns, __Flight[]) {
  let s: fetch.Shared = fetch.shared_of(pool);
  match (s.event(fd, (readiness & 1) != 0)) {
    Some(dead) => {
      let unwatched: i32 = drv.unwatch(dead);
      tcp_close(dead);
    },
    None => {}
  }
  return __shared_woken(drv, s, c, fls, opts);
}

function __clock_none(): __Clock {
  return __Clock { sec: 0 as i64, date: "", line: "" };
}

function __shared_woken[D: async.Driver](drv: D, s: fetch.Shared, c: __Conns, fls: __Flight[], opts: Config): (__Conns, __Flight[]) {
  for id in s.answered() {
    let fi: i32 = __flight_index(fls, id);
    if (fi >= 0) {
      let woke: (__Conns, __Flight[]) = __flight_wake(drv, c, fls, fi, __flight_pair(fls[fi], id), opts, __clock_none());
      c = woke.0;
      fls = woke.1;
    }
  }
  return (c, fls);
}

function __upstream_busy(pool: fetch.Idle): boolean {
  match (pool.shared) {
    Some(sh) => {
      return !sh.quiet();
    },
    None => {
      return false;
    }
  }
  return false;
}

function __upstream_owned(pool: fetch.Idle, fd: i32): boolean {
  match (pool.shared) {
    Some(sh) => {
      return !sh.quiet() && sh.owns(fd);
    },
    None => {
      return false;
    }
  }
  return false;
}

function __shared_turn[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flight[], opts: Config): (__Conns, __Flight[]) {
  let s: fetch.Shared = fetch.shared_of(pool);
  let idle: i64 = pool.idle.as_nanos();
  let woken: (__Conns, __Flight[]) = __shared_woken(drv, s, c, fls, opts);
  c = woken.0;
  fls = woken.1;
  for fd in s.unwatched() {
    let watched: i32 = drv.watch(fd, 1);
  }
  let (writing, drained) = s.flush();
  for fd in writing {
    let watched: i32 = drv.watch(fd, 3);
  }
  for fd in drained {
    let watched: i32 = drv.watch(fd, 1);
  }
  for fd in s.expired(monotonic_ns(), idle) {
    let unwatched: i32 = drv.unwatch(fd);
    tcp_close(fd);
  }
  return (c, fls);
}

function __shared_close[D: async.Driver](drv: D, pool: fetch.Idle): void {
  match (pool.shared) {
    Some(s) => {
      for fd in s.sockets() {
        let unwatched: i32 = drv.unwatch(fd);
        tcp_close(fd);
      }
    },
    None => {}
  }
}

function __init_platform(): platform.Host {
  return platform.host();
}
```

### `63 function __serve_loop(listener: i32, handler: (HttpRequest, platform.Host)`

```fern
<<63 function __serve_loop(listener: i32, handler: (HttpRequest, platform.Host)>>=
function __serve_loop(listener: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, on_stop: (string) => void, snap: Option[Writer]): i32 {
  let drv: async.RealDriver = __loop_driver(opts);
  let watched: i32 = drv.watch(listener, LISTENER_INTEREST);
  if (watched < 0) {
    return watched;
  }
  let accepting: boolean = true;
  let stop: __Stop = __serve_stop_sources(drv, opts);
  let plat: platform.Host = __host_platform(drv.handle(), opts, true);
  let host_for: (Option[net.SocketAddr]) => platform.Host = (peer: Option[net.SocketAddr]): platform.Host => plat.with_peer(peer);
  let conns: __Conns = __Conns { open: 0, free: 0 - 1, fds: [], slots: [], bufs: [], offs: [], pending: [], deadlines: [], due: 0 - 1 as i64, keep: [], served: [], backlog: [], queued: [], continued: [], rate_since: [], rate_base: [], send_since: [], send_base: [], peers: [], hosts: [], tails: [], tls: [], framing: [], last: http.http_framed_none(), out: buf_new(4096), lent: 0 - 1, parked: [], later: [], metrics: metrics.server_metrics(), pulls: [], producers: [], tls_reads: __tls_reads(opts), zc_bytes: opts.io_uring_zc_bytes, zc_held: [], zc_slots: [], zc_free: [], quic: __quic_none(), date: "" };
  let flights: __Flight[] = [];
  let scratch: u8[] = __recv_scratch();
  let clk: __Clock = __Clock { sec: 0 as i64, date: "", line: "" };
  let events: i32[] = async.event_room(64);
  let ex: Option[metrics.Exporter] = __exporter(drv, opts, snap);
  let start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started = (f: http.HttpFramed, kept: boolean, st: __Stop, host: platform.Host): __Started => __started_again(__serve_start(handler, host, f.request, f.version, kept, Idle, opts, st));
  conns = __Conns { ...conns, quic: __quic_open(drv, listener, opts) };
  while (true) {
    let now: i64 = monotonic_ns();
    events = __with_backlog(conns, drv.wait_into(events, __snapshot_timeout(snap, __stop_timeout(stop, opts, now, __least_timeout(__least_timeout(__conns_timeout(conns, now), __flights_timeout(flights, now)), __quic_timeout(conns.quic, now))))));
    let ends: i32 = async.ready_pairs(events) * 2;
    conns = __zc_reclaim(drv, conns);
    let second: i64 = clk.sec;
    clk = __clock_tick(clk);
    if (clk.sec != second) {
      conns = __Conns { ...conns, date: clk.date };
      __snapshot_send(snap, conns.metrics, opts);
    }
    let k: i32 = 0;
    while (k < ends) {
      let fd: i32 = events[k];
      let readiness: i32 = events[k + 1];
      k = k + 2;
      let sig: i32 = __stop_signal(fd);
      if (sig != 0) {
        stop = __stop_begin(stop, sig);
        continue;
      }
      if (__exporter_owns(ex, fd)) {
        ex = __scrape_wake(drv, ex, fd, __loop_exposition(conns.metrics, opts), !__serve_stopping(stop));
        continue;
      }
      if (fd == listener) {
        conns = __serve_accept(drv, listener, conns, opts, host_for);
        continue;
      }
      if (__quic_owns(conns, fd)) {
        let datagrams: (__Conns, __Flight[], __H3Burst) = __h3_event(drv, conns, flights, opts, stop);
        let started: (__Conns, __Flight[]) = __h3_start_all(drv, datagrams.0, datagrams.1, datagrams.2, handler, host_for, opts, stop);
        conns = started.0;
        flights = started.1;
        continue;
      }
      if (__h2_framed(conns, fd)) {
        let turned: (__Conns, __Flight[]) = __h2_event(drv, conns, flights, fd, scratch, handler, opts, stop, clk);
        conns = turned.0;
        flights = turned.1;
        continue;
      }
      let flown: (__Conns, __Flight[], boolean) = __flight_event(drv, conns, flights, fd, readiness, scratch, opts, clk, plat.pool);
      conns = flown.0;
      flights = flown.1;
      if (flown.2) {
        continue;
      }
      let ev: (__Conns, i32, boolean) = __serve_event(drv, conns, fd, scratch, opts, clk);
      conns = ev.0;
      let at: i32 = ev.1;
      if (at < 0) {
        __orphan_event(drv, plat.pool, fd);
        continue;
      }
      let mode: i32 = __h2_mode(conns, at, opts);
      if (mode != H2_NOT) {
        let served: (__Conns, __Flight[]) = __h2_turn(drv, conns, flights, at, handler, opts, stop, ev.2, mode);
        conns = served.0;
        flights = served.1;
        continue;
      }
      let burst: (__Conns, __Flight[]) = __serve_burst(drv, conns, flights, at, ev.2, handler, start, opts, stop, clk);
      conns = burst.0;
      flights = burst.1;
      conns = __serve_settle(drv, conns, flights, fd, ev.2, opts, clk);
      let taken: (__Conns, __Flight[]) = __staged_taken(drv, conns, flights, opts, plat.pool);
      conns = taken.0;
      flights = taken.1;
    }
    let due: (__Conns, __Flight[]) = __flights_expire(drv, conns, flights, opts, clk);
    conns = due.0;
    flights = due.1;
    let woken: (__Conns, __Flight[]) = __staged_taken(drv, conns, flights, opts, plat.pool);
    conns = woken.0;
    flights = woken.1;
    if (__quic_on(conns)) {
      let ticked: (__Conns, __Flight[], __H3Burst) = __h3_pass(drv, conns, flights, opts, stop);
      let started_late: (__Conns, __Flight[]) = __h3_start_all(drv, ticked.0, ticked.1, ticked.2, handler, host_for, opts, stop);
      conns = started_late.0;
      flights = started_late.1;
    }
    let ended: (__Conns, __Stop, boolean, boolean) = __serve_pass_end(drv, listener, conns, opts, stop, accepting, flights.len());
    conns = ended.0;
    stop = ended.1;
    accepting = ended.2;
    if (ended.3) {
      __flights_cancel(drv, flights);
      __quic_close(drv, conns.quic);
      __shared_close(drv, plat.pool);
      __exporter_close(drv, ex);
      drv.close();
      buf_free(conns.out);
      on_stop(__stop_reason(stop));
      return __stop_code(stop);
    }
  }
  if (accepting) {
    drv.unwatch(listener);
  }
  __quic_close(drv, conns.quic);
  __shared_close(drv, plat.pool);
  __exporter_close(drv, ex);
  drv.close();
  buf_free(conns.out);
  return 0;
}
```

### `64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flight[], at: i32, eof: boolean,...`

```fern
<<64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flight[], at: i32, eof: boolean,...>>=
function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flight[], at: i32, eof: boolean, handler: (HttpRequest, platform.Host) => HttpResponse, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, clk: __Clock): (__Conns, __Flight[]) {
  let conns: __Conns = c;
  let flights: __Flight[] = fls;
  let more: boolean = true;
  let count: i32 = 0;
  let parsed: (__Behind, http.HttpFramed) = __serve_behind(http.http_parse_request_framed_from(conns.bufs[at], conns.offs[at], opts.limits, conns.last), conns.last);
  let behind: __Behind = parsed.0;
  let framed: http.HttpFramed = parsed.1;
  while (more) {
    let next: __Behind = Idle;
    let next_framed: http.HttpFramed = framed;
    match (behind) {
      Pipelined => {
        let f: http.HttpFramed = framed;
        if (__upgrade_request(f.request)) {
          let upgraded: (__Conns, __Flight[], boolean, __Behind, http.HttpFramed) = __serve_stream(drv, conns, flights, at, __upgrade_head(f), start, opts, stop, count, clk);
          conns = upgraded.0;
          flights = upgraded.1;
          more = false;
          continue;
        }
        let took: (__Conns, boolean, __Behind, http.HttpFramed) = __serve_took(conns, at, f, opts, eof, stop);
        conns = __serve_started(took.0, at);
        count = count + 1;
        match (__serve_start(handler, conns.hosts[at], f.request, f.version, took.1, took.2, opts, stop)) {
          HandlerDone(resp) => {
            let answered: (__Conns, boolean) = __serve_respond(drv, conns, at, f.request.method, f.version, resp, took.1, opts, count, took.2, clk);
            conns = answered.0;
            more = answered.1;
            next = took.2;
            next_framed = took.3;
          },
          HandlerParked(fl) => {
            let parked: (__Conns, __Flight[]) = __serve_park(drv, conns, flights, at, __flight_watch(drv, fl, monotonic_ns()), opts);
            conns = parked.0;
            flights = parked.1;
            more = false;
          }
        }
      },
      Idle | Expecting => {
        let unframed: (__Conns, __Flight[], boolean, __Behind, http.HttpFramed) = __serve_unframed(drv, conns, flights, at, __expecting(behind), start, opts, stop, count, clk, eof);
        conns = unframed.0;
        flights = unframed.1;
        more = unframed.2;
        next = unframed.3;
        next_framed = unframed.4;
        if (more) {
          count = count + 1;
        }
      },
      Illformed(status) => {
        conns = __serve_refuse(drv, conns, at, status, opts, clk);
        more = false;
      }
    }
    behind = next;
    framed = next_framed;
  }
  return (conns, flights);
}
```

### `65 function __serve_loop_with[S](listener: i32, init: S, handler: (S, HttpRequest, platform.Host)`

```fern
<<65 function __serve_loop_with[S](listener: i32, init: S, handler: (S, HttpRequest, platform.Host)>>=
function __serve_loop_with[S](listener: i32, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, on_stop: (string, S) => void, snap: Option[Writer]): i32 {
  let st: S = init;
  let drv: async.RealDriver = __loop_driver(opts);
  let watched: i32 = drv.watch(listener, LISTENER_INTEREST);
  if (watched < 0) {
    return watched;
  }
  let accepting: boolean = true;
  let stop: __Stop = __serve_stop_sources(drv, opts);
  let plat: platform.Host = __host_platform(drv.handle(), opts, false);
  let host_for: (Option[net.SocketAddr]) => platform.Host = (peer: Option[net.SocketAddr]): platform.Host => plat.with_peer(peer);
  let conns: __Conns = __Conns { open: 0, free: 0 - 1, fds: [], slots: [], bufs: [], offs: [], pending: [], deadlines: [], due: 0 - 1 as i64, keep: [], served: [], backlog: [], queued: [], continued: [], rate_since: [], rate_base: [], send_since: [], send_base: [], peers: [], hosts: [], tails: [], tls: [], framing: [], last: http.http_framed_none(), out: buf_new(4096), lent: 0 - 1, parked: [], later: [], metrics: metrics.server_metrics(), pulls: [], producers: [], tls_reads: __tls_reads(opts), zc_bytes: opts.io_uring_zc_bytes, zc_held: [], zc_slots: [], zc_free: [], quic: __quic_none(), date: "" };
  let flights: __Flight[] = [];
  let scratch: u8[] = __recv_scratch();
  let clk: __Clock = __Clock { sec: 0 as i64, date: "", line: "" };
  let events: i32[] = async.event_room(64);
  let ex: Option[metrics.Exporter] = __exporter(drv, opts, snap);
  conns = __Conns { ...conns, quic: __quic_open(drv, listener, opts) };
  while (true) {
    let now: i64 = monotonic_ns();
    events = __with_backlog(conns, drv.wait_into(events, __snapshot_timeout(snap, __stop_timeout(stop, opts, now, __least_timeout(__least_timeout(__conns_timeout(conns, now), __flights_timeout(flights, now)), __quic_timeout(conns.quic, now))))));
    let ends: i32 = async.ready_pairs(events) * 2;
    conns = __zc_reclaim(drv, conns);
    let second: i64 = clk.sec;
    clk = __clock_tick(clk);
    if (clk.sec != second) {
      conns = __Conns { ...conns, date: clk.date };
      __snapshot_send(snap, conns.metrics, opts);
    }
    let k: i32 = 0;
    while (k < ends) {
      let fd: i32 = events[k];
      let readiness: i32 = events[k + 1];
      k = k + 2;
      let sig: i32 = __stop_signal(fd);
      if (sig != 0) {
        stop = __stop_begin(stop, sig);
        continue;
      }
      if (__exporter_owns(ex, fd)) {
        ex = __scrape_wake(drv, ex, fd, __loop_exposition(conns.metrics, opts), !__serve_stopping(stop));
        continue;
      }
      if (fd == listener) {
        conns = __serve_accept(drv, listener, conns, opts, host_for);
        continue;
      }
      if (__quic_owns(conns, fd)) {
        let datagrams: (__Conns, __Flight[], __H3Burst) = __h3_event(drv, conns, flights, opts, stop);
        let answered: (__Conns, __Flight[], S) = __h3_start_all_with(drv, datagrams.0, datagrams.1, datagrams.2, st, handler, host_for);
        conns = answered.0;
        flights = answered.1;
        st = answered.2;
        continue;
      }
      let flown: (__Conns, __Flight[], boolean) = __flight_event(drv, conns, flights, fd, readiness, scratch, opts, clk, plat.pool);
      conns = flown.0;
      flights = flown.1;
      if (flown.2) {
        continue;
      }
      let ev: (__Conns, i32, boolean) = __serve_event(drv, conns, fd, scratch, opts, clk);
      conns = ev.0;
      let at: i32 = ev.1;
      if (at < 0) {
        __orphan_event(drv, plat.pool, fd);
        continue;
      }
      let mode: i32 = __h2_mode(conns, at, opts);
      if (mode != H2_NOT) {
        let served: (__Conns, S) = __h2_turn_with(drv, conns, at, st, handler, opts, stop, ev.2, mode);
        conns = served.0;
        st = served.1;
        continue;
      }
      let more: boolean = true;
      let count: i32 = 0;
      let parsed: (__Behind, http.HttpFramed) = __serve_behind(http.http_parse_request_framed_from(conns.bufs[at], conns.offs[at], opts.limits, conns.last), conns.last);
      let behind: __Behind = parsed.0;
      let framed: http.HttpFramed = parsed.1;
      while (more) {
        let next: __Behind = Idle;
        let next_framed: http.HttpFramed = framed;
        match (behind) {
          Pipelined => {
            let f: http.HttpFramed = framed;
            let took: (__Conns, boolean, __Behind, http.HttpFramed) = __serve_took(conns, at, f, opts, ev.2, stop);
            conns = took.0;
            count = count + 1;
            conns = __serve_started(conns, at);
            let stepped: (S, HttpResponse) = (st, http.text(503, ""));
            if (!__serve_not_ready(f.request, opts, stop)) {
              stepped = __serve_step(handler, st, f.request, conns.hosts[at], opts);
            }
            st = stepped.0;
            let answered: (__Conns, boolean) = __serve_respond(drv, conns, at, f.request.method, f.version, stepped.1, took.1, opts, count, took.2, clk);
            conns = answered.0;
            more = answered.1;
            next = took.2;
            next_framed = took.3;
          },
          Idle => {
            more = false;
          },
          Expecting => {
            conns = __serve_continue(drv, conns, at, opts);
            more = false;
          },
          Illformed(status) => {
            conns = __serve_refuse(drv, conns, at, status, opts, clk);
            more = false;
          }
        }
        behind = next;
        framed = next_framed;
      }
      conns = __serve_finish(drv, conns, fd, ev.2, opts, clk);
      let taken: (__Conns, __Flight[]) = __staged_taken(drv, conns, flights, opts, plat.pool);
      conns = taken.0;
      flights = taken.1;
    }
    let due: (__Conns, __Flight[]) = __flights_expire(drv, conns, flights, opts, clk);
    conns = due.0;
    flights = due.1;
    let woken: (__Conns, __Flight[]) = __staged_taken(drv, conns, flights, opts, plat.pool);
    conns = woken.0;
    flights = woken.1;
    if (__quic_on(conns)) {
      let ticked: (__Conns, __Flight[], __H3Burst) = __h3_pass(drv, conns, flights, opts, stop);
      let answered: (__Conns, __Flight[], S) = __h3_start_all_with(drv, ticked.0, ticked.1, ticked.2, st, handler, host_for);
      conns = answered.0;
      flights = answered.1;
      st = answered.2;
    }
    let ended: (__Conns, __Stop, boolean, boolean) = __serve_pass_end(drv, listener, conns, opts, stop, accepting, flights.len());
    conns = ended.0;
    stop = ended.1;
    accepting = ended.2;
    if (ended.3) {
      __flights_cancel(drv, flights);
      __quic_close(drv, conns.quic);
      __exporter_close(drv, ex);
      drv.close();
      buf_free(conns.out);
      on_stop(__stop_reason(stop), st);
      return __stop_code(stop);
    }
  }
  if (accepting) {
    drv.unwatch(listener);
  }
  __quic_close(drv, conns.quic);
  __exporter_close(drv, ex);
  drv.close();
  buf_free(conns.out);
  return 0;
}
```

### `66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)`

```fern
<<66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>=
pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse): i32 {
  return supervise_shutdown(port, opts, handler, (reason: string): void => {});
}

pub function supervise_shutdown(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse, shutdown: (string) => void): i32 {
  return __supervise(port, opts, (fd: i32, worker_opts: Config, snap: Option[Writer]): i32 => __serve_loop(fd, handler, worker_opts, shutdown, snap));
}

pub function supervise_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse)): i32 {
  return supervise_with_shutdown(port, opts, init, handler, (reason: string, state: S): void => {});
}

pub function supervise_with_shutdown[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), shutdown: (string, S) => void): i32 {
  return __supervise(port, opts, (fd: i32, worker_opts: Config, snap: Option[Writer]): i32 => __serve_loop_with(fd, init, handler, worker_opts, shutdown, snap));
}
```

### `67 function __supervise(port: i32, opts: Config, worker: (i32, Config, Option[Writer])`

```fern
<<67 function __supervise(port: i32, opts: Config, worker: (i32, Config, Option[Writer])>>=
function __supervise(port: i32, opts: Config, worker: (i32, Config, Option[Writer]) => i32): i32 {
  let listener: i32 = 0 - 1;
  if (!__per_worker_listeners(opts)) {
    let l: __Listen = __serve_listener(port, opts);
    if (l.fd < 0) {
      return __listen_failed(l);
    }
    listener = l.fd;
  }
  let workers: i32 = opts.workers;
  if (workers < 1) {
    workers = cpu_count();
  }
  if (workers < 1) {
    workers = 1;
  }
  let resolved: Config = Config { ...opts, workers: workers };
  let sup: __Supervisor = __Supervisor { pids: [], spawned: [], pipes: [], partial: [], snaps: [], quic: [], h3_free: __h3_pool(listener, resolved), backoff_ms: 100 as i64, fork_after: 0 as i64, fast_deaths: 0, stopping: false, worst: 0, crashed: 0 };
  let drv: async.RealDriver = async.real_driver();
  let chld: i32 = drv.watch_signal(17);
  if (chld < 0) {
    return chld;
  }
  drv.watch_signal(15);
  drv.watch_signal(2);
  let ex: Option[metrics.Exporter] = None;
  while (true) {
    let refork_ms: i32 = -1;
    if (!sup.stopping && sup.pids.len() < workers) {
      let wait_ms: i64 = sup.fork_after - now_unix_ms();
      if (wait_ms <= 0 as i64) {
        let forked: (__Supervisor, i32) = __supervisor_fork(drv, port, listener, worker, resolved, sup);
        sup = forked.0;
        if (forked.1 != 0 - 1) {
          return forked.1;
        }
        if (opts.metrics_port > 0 && !__exporter_up(ex) && sup.pids.len() == 1) {
          ex = __exporter(drv, opts, None);
          if (!__exporter_up(ex)) {
            __supervisor_abandon(drv, sup);
            drv.close();
            return 98;
          }
        }
        continue;
      }
      refork_ms = wait_ms as i32;
    }
    if (sup.stopping && sup.pids.len() == 0) {
      __exporter_close(drv, ex);
      drv.close();
      return sup.worst;
    }
    let events: i32[] = drv.wait(8, refork_ms);
    sup = __supervisor_events(sup, events);
    ex = __supervisor_scrapes(drv, ex, events, sup);
    if (sup.gave_up()) {
      __supervisor_abandon(drv, sup);
      __exporter_close(drv, ex);
      drv.close();
      return sup.crashed;
    }
  }
  return 0;
}

function __supervisor_events(sup: __Supervisor, events: i32[]): __Supervisor {
  let k: i32 = 0;
  while (k + 1 < events.len()) {
    let fd: i32 = events[k];
    k = k + 2;
    let sig: i32 = __stop_signal(fd);
    if (sig != 0) {
      sup = __supervisor_stop(sup, sig);
    }
    if (fd == 0 - 17) {
      sup = __supervisor_reap(sup);
    }
    let pipe: i32 = __index_of_i32(sup.pipes, fd);
    if (fd >= 0 && pipe >= 0) {
      sup = __supervisor_snapshot(sup, pipe);
    }
  }
  return sup;
}

function __supervisor_snapshot(sup: __Supervisor, i: i32): __Supervisor {
  let got: u8[] = [];
  match (net.read_up_to(sup.pipes[i], 65536)) {
    Ok(bytes) => {
      got = bytes;
    },
    Err(_) => {}
  }
  if (got.len() == 0) {
    return sup;
  }
  let framed: (string[], string) = metrics.frames(sup.partial[i] + string_from_bytes_unchecked(got));
  sup = __Supervisor { ...sup, partial: sup.partial.with(i, framed.1) };
  if (framed.0.len() > 0) {
    sup = __Supervisor { ...sup, snaps: sup.snaps.with(i, framed.0[framed.0.len() - 1]) };
  }
  return sup;
}

function __supervisor_scrapes(drv: async.RealDriver, ex: Option[metrics.Exporter], events: i32[], sup: __Supervisor): Option[metrics.Exporter] {
  let k: i32 = 0;
  while (k + 1 < events.len()) {
    let fd: i32 = events[k];
    k = k + 2;
    if (__exporter_owns(ex, fd)) {
      ex = __scrape_wake(drv, ex, fd, metrics.merge(sup.snaps), !sup.stopping && sup.pids.len() > 0);
    }
  }
  return ex;
}

function __index_of_i32(xs: i32[], x: i32): i32 {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == x) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}
```

### `68 function __supervisor_fork(drv: async.RealDriver, port: i32, listener: i32, worker: (i32, Config, Option[Wr...`

```fern
<<68 function __supervisor_fork(drv: async.RealDriver, port: i32, listener: i32, worker: (i32, Config, Option[Wr...>>=
function __supervisor_fork(drv: async.RealDriver, port: i32, listener: i32, worker: (i32, Config, Option[Writer]) => i32, opts: Config, sup: __Supervisor): (__Supervisor, i32) {
  let snap: Option[Pipe] = __snapshot_pipe(opts);
  let h3_sock: i32 = 0 - 1;
  if (sup.h3_free.len() > 0) {
    h3_sock = sup.h3_free[0];
  }
  let pid: i32 = proc_fork();
  if (pid == -38) {
    eprint("serve: supervision unavailable; serving single-process");
    __snapshot_pipe_close(snap);
    drv.unwatch_signal(17);
    drv.unwatch_signal(15);
    drv.unwatch_signal(2);
    drv.close();
    let single: __Listen = __worker_listener(port, listener, opts);
    if (single.fd < 0) {
      return (sup, __listen_failed(single));
    }
    __h3_pool_close(sup.h3_free, h3_sock);
    return (sup, worker(single.fd, Config { ...opts, workers: 1, h3_sock: h3_sock }, None));
  }
  if (pid < 0) {
    __snapshot_pipe_close(snap);
    return (sup, pid);
  }
  if (pid == 0) {
    drv.close();
    let own: __Listen = __worker_listener(port, listener, opts);
    if (own.fd < 0) {
      exit(__listen_failed(own));
    }
    let loop_opts: Config = Config { ...opts, stop_with_parent: true, h3_sock: h3_sock };
    if (listener < 0) {
      loop_opts = Config { ...loop_opts, workers: 1 };
    }
    exit(worker(own.fd, loop_opts, __snapshot_writer(snap)));
    return (sup, 0);
  }
  let read_end: i32 = 0 - 1;
  match (snap) {
    Some(q) => {
      q.w.close();
      drv.watch(q.r.fd, 1);
      read_end = q.r.fd;
    },
    None => {}
  }
  return (__Supervisor { ...sup, pids: sup.pids.append(pid), spawned: sup.spawned.append(now_unix_ms()), pipes: sup.pipes.append(read_end), partial: sup.partial.append(""), snaps: sup.snaps.append(""), quic: sup.quic.append(h3_sock), h3_free: __without_index_i32(sup.h3_free, 0) }, 0 - 1);
}

function __snapshot_pipe(opts: Config): Option[Pipe] {
  if (opts.metrics_port <= 0) {
    return None;
  }
  match (pipe()) {
    Ok(q) => {
      tcp_socket_ctl(q.w.fd, 3, 1);
      return Some(q);
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}

function __snapshot_writer(snap: Option[Pipe]): Option[Writer] {
  match (snap) {
    Some(q) => {
      q.r.close();
      return Some(q.w);
    },
    None => {
      return None;
    }
  }
  return None;
}

function __snapshot_pipe_close(snap: Option[Pipe]): void {
  match (snap) {
    Some(q) => {
      q.r.close();
      q.w.close();
    },
    None => {}
  }
}

function __snapshot_timeout(snap: Option[Writer], timeout: i32): i32 {
  match (snap) {
    Some(w) => {
      return __least_timeout(timeout, 1000);
    },
    None => {
      return timeout;
    }
  }
  return timeout;
}

function __snapshot_send(snap: Option[Writer], m: metrics.ServerMetrics, opts: Config): void {
  match (snap) {
    Some(w) => {
      w.write(__loop_exposition(m, opts) + metrics.EOF);
    },
    None => {}
  }
}

function __loop_exposition(m: metrics.ServerMetrics, opts: Config): string {
  return m.render() + opts.metrics.render();
}

function __exporter(drv: async.RealDriver, opts: Config, snap: Option[Writer]): Option[metrics.Exporter] {
  if (opts.metrics_port <= 0 || __snapshot_timeout(snap, 0 - 1) >= 0) {
    return None;
  }
  match (metrics.listen(drv, opts.metrics_port)) {
    Ok(e) => {
      return Some(e);
    },
    Err(e) => {
      eprint("serve: cannot listen for metrics on port " + int.int_to_string(opts.metrics_port) + ": " + e.message());
      return None;
    }
  }
  return None;
}
```

### `69 function __exporter_up(ex: Option[metrics.Exporter]): boolean`

```fern
<<69 function __exporter_up(ex: Option[metrics.Exporter]): boolean>>=
function __exporter_up(ex: Option[metrics.Exporter]): boolean {
  match (ex) {
    Some(e) => {
      return true;
    },
    None => {
      return false;
    }
  }
  return false;
}

function __exporter_owns(ex: Option[metrics.Exporter], fd: i32): boolean {
  match (ex) {
    Some(e) => {
      return e.owns(fd);
    },
    None => {
      return false;
    }
  }
  return false;
}

function __scrape_wake(drv: async.RealDriver, ex: Option[metrics.Exporter], fd: i32, text: string, ready: boolean): Option[metrics.Exporter] {
  match (ex) {
    Some(e) => {
      return Some(e.wake(drv, fd, (path: string): HttpResponse => metrics.scrape(path, (): string => text, ready)));
    },
    None => {
      return None;
    }
  }
  return None;
}

function __exporter_close(drv: async.RealDriver, ex: Option[metrics.Exporter]): void {
  match (ex) {
    Some(e) => {
      e.close(drv);
    },
    None => {}
  }
}

function __per_worker_listeners(opts: Config): boolean {
  if (!opts.reuse_port) {
    return false;
  }
  match (env("LISTEN_FDS")) {
    Some(n) => {
      return false;
    },
    None => {
      return true;
    }
  }
  return true;
}

function __worker_listener(port: i32, listener: i32, opts: Config): __Listen {
  if (listener >= 0) {
    return __Listen { fd: listener, addr: "" };
  }
  let own: __Listen = __serve_listener(port, opts);
  if (own.fd >= 0) {
    tcp_socket_ctl(own.fd, 8, 0);
  }
  return own;
}
```

### `70 struct __Supervisor`

```fern
<<70 struct __Supervisor>>=
struct __Supervisor { pids: i32[], spawned: i64[], pipes: i32[], partial: string[], snaps: string[], quic: i32[], h3_free: i32[], backoff_ms: i64, fork_after: i64, fast_deaths: i32, stopping: boolean, worst: i32, crashed: i32 }
```

### `71 function (s: __Supervisor) gave_up(): boolean`

```fern
<<71 function (s: __Supervisor) gave_up(): boolean>>=
function (s: __Supervisor) gave_up(): boolean {
  return s.fast_deaths >= 8;
}

function __supervisor_abandon(drv: async.RealDriver, sup: __Supervisor): void {
  let s: __Supervisor = __supervisor_stop(sup, 15);
  let until: i64 = now_unix_ms() + 5000 as i64;
  while (s.pids.len() > 0) {
    let left: i64 = until - now_unix_ms();
    if (left <= 0 as i64) {
      break;
    }
    drv.wait(8, left as i32);
    s = __supervisor_reap(s);
  }
  for pid in s.pids {
    match (signal_send(pid, 9)) {
      Ok(_) => {},
      Err(_) => {}
    }
  }
  while (s.pids.len() > 0) {
    drv.wait(8, 50);
    s = __supervisor_reap(s);
  }
}

function __supervisor_stop(sup: __Supervisor, sig: i32): __Supervisor {
  if (sup.stopping) {
    return sup;
  }
  for pid in sup.pids {
    match (signal_send(pid, sig)) {
      Ok(_) => {},
      Err(_) => {}
    }
  }
  return __Supervisor { ...sup, stopping: true };
}

function __supervisor_reap(sup: __Supervisor): __Supervisor {
  let i: i32 = 0;
  while (i < sup.pids.len()) {
    let code: i32 = proc_waitpid_nohang(sup.pids[i]);
    if (code == 0 - 1) {
      i = i + 1;
      continue;
    }
    if (code < 0) {
      code = 0 - code;
    }
    let lived_ms: i64 = now_unix_ms() - sup.spawned[i];
    if (sup.pipes[i] >= 0) {
      tcp_close(sup.pipes[i]);
    }
    let freed: i32[] = sup.h3_free;
    if (sup.quic[i] >= 0) {
      freed = freed.append(sup.quic[i]);
    }
    sup = __Supervisor { ...sup, pids: __without_index_i32(sup.pids, i), spawned: __without_index_i64(sup.spawned, i), pipes: __without_index_i32(sup.pipes, i), partial: __without_index_str(sup.partial, i), snaps: __without_index_str(sup.snaps, i), quic: __without_index_i32(sup.quic, i), h3_free: freed };
    if (sup.stopping) {
      if (code > sup.worst) {
        sup = __Supervisor { ...sup, worst: code };
      }
      continue;
    }
    eprint("serve: worker died with exit code " + int.int_to_string(code) + "; restarting");
    sup = __Supervisor { ...sup, crashed: code };
    if (lived_ms < 100 as i64) {
      sup = __Supervisor { ...sup, fast_deaths: sup.fast_deaths + 1 };
      if (sup.gave_up()) {
        eprint("serve: giving up after 8 consecutive fast worker deaths");
        return sup;
      }
    } else {
      sup = __Supervisor { ...sup, fast_deaths: 0, backoff_ms: 100 as i64 };
    }
    let next: i64 = sup.backoff_ms * (2 as i64);
    if (next > 5000 as i64) {
      next = 5000 as i64;
    }
    sup = __Supervisor { ...sup, fork_after: now_unix_ms() + sup.backoff_ms, backoff_ms: next };
  }
  return sup;
}

function __without_index_i32(xs: i32[], at: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (i != at) {
      out = out.append(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

function __without_index_str(xs: string[], at: i32): string[] {
  let out: string[] = [];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (i != at) {
      out = out.append(xs[i]);
    }
    i = i + 1;
  }
  return out;
}

function __without_index_i64(xs: i64[], at: i32): i64[] {
  let out: i64[] = [];
  let i: i32 = 0;
  while (i < xs.len()) {
    if (i != at) {
      out = out.append(xs[i]);
    }
    i = i + 1;
  }
  return out;
}
```

### `72 function __port_from_env(name: string, fallback: i32): i32`

```fern
<<72 function __port_from_env(name: string, fallback: i32): i32>>=
function __port_from_env(name: string, fallback: i32): i32 {
  match (env(name)) {
    Some(s) => {
      let n: i32 = 0;
      let i: i32 = 0;
      while (i < s.len()) {
        let b: i32 = s[i] as i32;
        if (b < 48 || b > 57) {
          return fallback;
        }
        n = n * 10 + (b - 48);
        if (n > 65535) {
          return fallback;
        }
        i = i + 1;
      }
      if (i == 0) {
        return fallback;
      }
      return n;
    },
    None => {
      return fallback;
    }
  }
}
```

### `73 struct __Quic`

```fern
<<73 struct __Quic>>=
struct __Quic { sock: i32, port: i32, cfg: quic.Config, conns: h3q.Conn[], keys: u8[][], origs: u8[][], peers: net.SocketAddr[], serials: i32[], next: i32, producers: __H3Producer[], stopping: boolean, buf: u8[] }

struct __H3Producer { serial: i32, stream: i64, producer: ChunkProducer, index: i32, trailers: HeaderMap, running: boolean }
```

### `74 function __h3_pending_max(): i64`

```fern
<<74 function __h3_pending_max(): i64>>=
function __h3_pending_max(): i64 {
  return 262144 as i64;
}

function __h3_burst(): i32 {
  return 64;
}

function __quic_none(): __Quic {
  return __Quic { sock: 0 - 1, port: 0, cfg: quic.config([]), conns: [], keys: [], origs: [], peers: [], serials: [], next: 1, producers: [], stopping: false, buf: [] };
}

function __quic_socket(port: i32): i32 {
  if (target_os() != "wasi") {
    let both: i32 = udp_bind_with(__every_v6(), port, true);
    if (both >= 0 || !net.error_from_host(both).ipv6_unavailable()) {
      return both;
    }
  }
  return udp_bind_with(__every_v4(), port, true);
}

function __h3_pool(listener: i32, opts: Config): i32[] {
  match (opts.tls) {
    None => {
      return [];
    },
    Some(_) => {}
  }
  if (!opts.h3 || listener < 0) {
    return [];
  }
  let port: i32 = tcp_local_port(listener);
  let pool: i32[] = [];
  let i: i32 = 0;
  while (i < opts.workers) {
    let sock: i32 = __quic_socket(port);
    if (sock < 0) {
      __h3_pool_close(pool, 0 - 1);
      return [];
    }
    pool = pool.append(sock);
    i = i + 1;
  }
  return pool;
}

function __h3_pool_close(pool: i32[], keep: i32): void {
  let i: i32 = 0;
  while (i < pool.len()) {
    if (pool[i] != keep) {
      let closed: Result[void, net.NetError] = net.close(pool[i]);
    }
    i = i + 1;
  }
}

function __quic_open[D: async.Driver](drv: D, listener: i32, opts: Config): __Quic {
  let Some(t) = opts.tls else {
    return __quic_none();
  };
  if (!opts.h3) {
    return __quic_none();
  }
  let port: i32 = tcp_local_port(listener);
  let sock: i32 = opts.h3_sock;
  if (sock < 0) {
    sock = __quic_socket(port);
  }
  if (sock < 0) {
    return __quic_none();
  }
  let nb: Result[void, net.NetError] = net.set_nonblocking(sock, true);
  if (drv.watch(sock, LISTENER_INTEREST) < 0) {
    let closed: Result[void, net.NetError] = net.close(sock);
    return __quic_none();
  }
  return __Quic { ...__quic_none(), sock: sock, port: port, cfg: h3q.transport(quic.config([]).server_side(t.identity)), buf: __alloc_u8(65536) };
}

function __quic_on(c: __Conns): boolean {
  return c.quic.sock >= 0;
}

function __quic_owns(c: __Conns, fd: i32): boolean {
  return __quic_on(c) && fd == c.quic.sock;
}

function __quic_close[D: async.Driver](drv: D, q: __Quic): void {
  if (q.sock < 0) {
    return;
  }
  let now: i64 = monotonic_ns();
  let i: i32 = 0;
  while (i < q.conns.len()) {
    let closing: h3q.Conn = q.conns[i].close(now).transmit(now);
    __h3_sent(q.sock, closing.datagrams(), q.peers[i]);
    i = i + 1;
  }
  drv.unwatch(q.sock);
  let closed: Result[void, net.NetError] = net.close(q.sock);
}

function __quic_timeout(q: __Quic, now: i64): i32 {
  let best: i64 = 0 - 1 as i64;
  for c in q.conns {
    let d: i64 = c.deadline();
    if (d > 0) {
      best = __sooner(best, d);
    }
  }
  if (best < 0 as i64) {
    return 0 - 1;
  }
  if (best <= now) {
    return 0;
  }
  return ((best - now) / 1000000 + 1) as i32;
}

function __with_alt_svc(c: __Conns, resp: HttpResponse): HttpResponse {
  if (!__quic_on(c) || resp.headers.has("alt-svc")) {
    return resp;
  }
  return HttpResponse { ...resp, headers: resp.headers.append("alt-svc", "h3=\":" + int.int_to_string(c.quic.port) + "\"; ma=86400") };
}

function __dated(c: __Conns, resp: HttpResponse): HttpResponse {
  if (c.date.len() == 0 || resp.headers.has("date")) {
    return resp;
  }
  return HttpResponse { ...resp, headers: resp.headers.append("date", c.date) };
}
```

### `75 function __h3_sent(sock: i32, out: u8[][], to: net.SocketAddr): void`

```fern
<<75 function __h3_sent(sock: i32, out: u8[][], to: net.SocketAddr): void>>=
function __h3_sent(sock: i32, out: u8[][], to: net.SocketAddr): void {
  for d in out {
    let r: Result[i32, net.NetError] = net.send_to(sock, d, to);
  }
}
```

### `76 struct __H3Req`

```fern
<<76 struct __H3Req>>=
struct __H3Req { serial: i32, id: i64, req: HttpRequest, peer: net.SocketAddr }

struct __H3Burst { reqs: __H3Req[], touched: i32[] }
```

### `77 function __h3_touched(b: __H3Burst, serial: i32): __H3Burst`

```fern
<<77 function __h3_touched(b: __H3Burst, serial: i32): __H3Burst>>=
function __h3_touched(b: __H3Burst, serial: i32): __H3Burst {
  if (b.touched.contains(serial)) {
    return b;
  }
  return __H3Burst { ...b, touched: b.touched.append(serial) };
}

function __h3_event[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, stop: __Stop): (__Conns, __Flight[], __H3Burst) {
  let cur: __Conns = c;
  let fs: __Flight[] = fls;
  let burst: __H3Burst = __H3Burst { reqs: [], touched: [] };
  let n: i32 = 0;
  while (n < __h3_burst()) {
    let Ok(got) = net.recv_from(cur.quic.sock, cur.quic.buf) else {
      return (cur, fs, burst);
    };
    let r: (__Conns, __Flight[], __H3Burst) = __h3_datagram(drv, cur, fs, cur.quic.buf[0:got.0], got.1, burst, opts, stop);
    cur = r.0;
    fs = r.1;
    burst = r.2;
    n = n + 1;
  }
  return (cur, fs, burst);
}

function __h3_datagram[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], d: [u8], from: net.SocketAddr, burst: __H3Burst, opts: Config, stop: __Stop): (__Conns, __Flight[], __H3Burst) {
  let Ok(h) = packet.parse(d, 0, c.quic.cfg.cid_len) else {
    return (c, fls, burst);
  };
  let i: i32 = __h3_find(c.quic, h.dcid);
  let cur: __Conns = c;
  if (i < 0) {
    let made: (__Conns, i32) = __h3_accept(c, h, d, from, opts, stop);
    cur = made.0;
    i = made.1;
    if (i < 0) {
      return (cur, fls, burst);
    }
  }
  let q: __Quic = cur.quic;
  let serial: i32 = q.serials[i];
  cur = __Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].receive(d, monotonic_ns())) } };
  let taken: (__Conns, __H3Req[]) = __h3_events(cur, i);
  let more: (__Conns, __Flight[]) = __h3_more(drv, taken.0, fls, serial);
  return (more.0, more.1, __h3_touched(__H3Burst { ...burst, reqs: burst.reqs.concat(taken.1) }, serial));
}

function __h3_find(q: __Quic, dcid: u8[]): i32 {
  let i: i32 = 0;
  while (i < q.conns.len()) {
    if (__bytes_same(q.keys[i], dcid) || __bytes_same(q.origs[i], dcid)) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}

function __bytes_same(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __h3_from_ip(q: __Quic, ip: net.IpAddr): i32 {
  let n: i32 = 0;
  for peer in q.peers {
    if (peer.ip.eq(ip)) {
      n = n + 1;
    }
  }
  return n;
}

function __h3_accept(c: __Conns, h: packet.Header, d: [u8], from: net.SocketAddr, opts: Config, stop: __Stop): (__Conns, i32) {
  let q: __Quic = c.quic;
  let initial: boolean = false;
  match (h.kind) {
    packet.Initial => {
      initial = true;
    },
    _ => {}
  }
  if (!initial || d.len() < packet.MIN_INITIAL_DATAGRAM || q.stopping || __serve_stopping(stop) || q.conns.len() >= opts.max_connections) {
    return (c, 0 - 1);
  }
  if (opts.max_connections_per_ip > 0 && __h3_from_ip(q, from.ip) >= opts.max_connections_per_ip) {
    return (c, 0 - 1);
  }
  let Ok(made) = quic.accept(q.cfg, random_bytes(quic.server_entropy(q.cfg)), h, monotonic_ns()) else {
    return (c, 0 - 1);
  };
  let conn: h3q.Conn = h3q.server(made, h3.config()).with_max_body(opts.limits.body as i64);
  let grown: __Quic = __Quic { ...q, conns: q.conns.append(conn), keys: q.keys.append(made.scid()), origs: q.origs.append(h.dcid), peers: q.peers.append(from), serials: q.serials.append(q.next), next: q.next + 1 };
  return (__Conns { ...c, quic: grown }, grown.conns.len() - 1);
}

function __h3_events(c: __Conns, i: i32): (__Conns, __H3Req[]) {
  let q: __Quic = c.quic;
  let evs: h3q.Event[] = q.conns[i].events;
  let producers: __H3Producer[] = q.producers;
  let reqs: __H3Req[] = [];
  let serial: i32 = q.serials[i];
  for e in evs {
    match (e) {
      h3q.Requested(id, req) => {
        reqs = reqs.append(__H3Req { serial: serial, id: id, req: req, peer: q.peers[i] });
      },
      h3q.StreamDropped(id, _) => {
        producers = __h3_producer_dropped(producers, serial, id);
      },
      _ => {}
    }
  }
  return (__Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].events_taken()), producers: producers } }, reqs);
}
```

### `78 function __h3_start_all[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], burst: __H3Burst, handler: (H...`

```fern
<<78 function __h3_start_all[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], burst: __H3Burst, handler: (H...>>=
function __h3_start_all[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], burst: __H3Burst, handler: (HttpRequest, platform.Host) => HttpResponse, host_for: (Option[net.SocketAddr]) => platform.Host, opts: Config, stop: __Stop): (__Conns, __Flight[]) {
  if (burst.reqs.len() == 0 && burst.touched.len() == 0) {
    return (c, fls);
  }
  let cur: __Conns = c;
  let fs: __Flight[] = fls;
  for r in burst.reqs {
    let started: __Started = __serve_start(handler, host_for(Some(r.peer)), r.req, "HTTP/3", true, Idle, opts, stop);
    let done: (__Conns, __Flight[]) = __h3_request(drv, cur, fs, r.serial, r.id, r.req, started);
    cur = done.0;
    fs = done.1;
  }
  return (__h3_flush_all(cur, burst.touched), fs);
}

function __h3_start_all_with[D: async.Driver, S](drv: D, c: __Conns, fls: __Flight[], burst: __H3Burst, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, __Flight[], S) {
  if (burst.reqs.len() == 0 && burst.touched.len() == 0) {
    return (c, fls, st);
  }
  let cur: __Conns = c;
  let fs: __Flight[] = fls;
  let state: S = st;
  for r in burst.reqs {
    let (next, resp) = handler(state, r.req, host_for(Some(r.peer)));
    state = next;
    let done: (__Conns, __Flight[]) = __h3_request(drv, cur, fs, r.serial, r.id, r.req, HandlerDone(resp));
    cur = done.0;
    fs = done.1;
  }
  return (__h3_flush_all(cur, burst.touched), fs, state);
}

function __h3_flush_all(c: __Conns, serials: i32[]): __Conns {
  let cur: __Conns = c;
  for s in serials {
    cur = __h3_flush(cur, s);
  }
  return cur;
}

function __h3_request[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], serial: i32, id: i64, req: HttpRequest, started: __Started): (__Conns, __Flight[]) {
  let now: i64 = monotonic_ns();
  match (started) {
    HandlerDone(resp) => {
      let counted: __Conns = __serve_count_from(c, req.method, resp.status, now);
      return __h3_respond(drv, counted, fls, serial, id, req.method, resp);
    },
    HandlerParked(fl) => {
      let parked: __Flight = __Flight { ...fl, fd: 0 - 1, stream: id as i32, quic: serial, since: now };
      return (c, fls.append(__flight_watch(drv, parked, now)));
    }
  }
  return (c, fls);
}

function __h3_at(q: __Quic, serial: i32): i32 {
  let i: i32 = 0;
  while (i < q.serials.len()) {
    if (q.serials[i] == serial) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}

function __h3_respond[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], serial: i32, id: i64, method: string, resp: HttpResponse): (__Conns, __Flight[]) {
  let i: i32 = __h3_at(c.quic, serial);
  if (i < 0) {
    return (c, fls);
  }
  let cur: __Conns = __stage_background(c, resp);
  let q: __Quic = cur.quic;
  resp = __file_chunks(__dated(cur, resp));
  match (resp.body) {
    BodyChunks(producer) => {
      if (method == "HEAD" || http.http_bodiless(resp.status)) {
        return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].respond_head(id, resp, 0 - 1 as i64).end(id, HeaderMap { names: [], values: [] })) } }, fls);
      }
      let p: __H3Producer = __H3Producer { serial: serial, stream: id, producer: producer, index: 0, trailers: resp.trailers, running: false };
      cur = __Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].respond_head(id, resp, 0 - 1 as i64)), producers: q.producers.append(p) } };
      return __h3_produce(drv, cur, fls, cur.quic.producers.len() - 1);
    },
    _ => {
      if (method == "HEAD") {
        return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].respond_head(id, resp, resp.body_bytes().len() as i64).end(id, HeaderMap { names: [], values: [] })) } }, fls);
      }
      return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].respond(id, resp)) } }, fls);
    }
  }
  return (cur, fls);
}

function __h3_produce[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], pi: i32): (__Conns, __Flight[]) {
  let cur: __Conns = c;
  let k: i32 = pi;
  while (k >= 0) {
    let p: __H3Producer = cur.quic.producers[k];
    let i: i32 = __h3_at(cur.quic, p.serial);
    if (i < 0 || p.running || cur.quic.conns[i].quic().buffered(p.stream) >= __h3_pending_max()) {
      return (cur, fls);
    }
    let next: (i32) => Option[u8[]] = p.producer.next;
    let index: i32 = p.index;
    let t: async.Task[Option[u8[]]] = async.task_new((): Option[u8[]] => next(index));
    match (async.task_start(t)) {
      Done(chunk) => {
        async.task_free(t);
        let r: (__Conns, boolean) = __h3_chunk(cur, k, chunk);
        cur = r.0;
        if (r.1) {
          return (cur, fls);
        }
      },
      Suspended(w) => {
        let q: __Quic = cur.quic;
        let none: i32[] = [];
        let fl: __Flight = __Flight { job: Producer(t), wait: w, fd: 0 - 1, set: none, due: 0 - 1 as i64, method: "", version: "", kept: false, behind: Idle, body: None, idle: 0 - 1 as i64, ends: 0 - 1 as i64, stream: p.stream as i32, since: 0 - 1 as i64, grpc: false, quic: p.serial };
        let running: __Conns = __Conns { ...cur, quic: __Quic { ...q, producers: q.producers.with(k, __H3Producer { ...p, running: true }) } };
        return (running, fls.append(__flight_watch(drv, fl, monotonic_ns())));
      },
      Cancelled => {
        async.task_free(t);
        return (__h3_producer_failed(cur, k), fls);
      }
    }
  }
  return (cur, fls);
}
```

### `79 function __h3_chunk(c: __Conns, pi: i32, chunk: Option[u8[]]): (__Conns, boolean)`

```fern
<<79 function __h3_chunk(c: __Conns, pi: i32, chunk: Option[u8[]]): (__Conns, boolean)>>=
function __h3_chunk(c: __Conns, pi: i32, chunk: Option[u8[]]): (__Conns, boolean) {
  let q: __Quic = c.quic;
  let p: __H3Producer = q.producers[pi];
  let i: i32 = __h3_at(q, p.serial);
  if (i < 0) {
    return (__Conns { ...c, quic: __Quic { ...q, producers: __h3_producers_without(q.producers, pi) } }, true);
  }
  match (chunk) {
    Some(bytes) => {
      return (__Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].write(p.stream, bytes)), producers: q.producers.with(pi, __H3Producer { ...p, index: p.index + 1, running: false }) } }, false);
    },
    None => {
      return (__Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].end(p.stream, __trailers_joined(p.trailers, p.producer.trailers()))), producers: __h3_producers_without(q.producers, pi) } }, true);
    }
  }
  return (c, true);
}

function __h3_producer_failed(c: __Conns, pi: i32): __Conns {
  let q: __Quic = c.quic;
  let p: __H3Producer = q.producers[pi];
  let i: i32 = __h3_at(q, p.serial);
  let conns: h3q.Conn[] = q.conns;
  if (i >= 0) {
    conns = conns.with(i, conns[i].abort(p.stream, h3.H3_INTERNAL_ERROR));
  }
  return __Conns { ...c, quic: __Quic { ...q, conns: conns, producers: __h3_producers_without(q.producers, pi) } };
}

function __h3_producers_without(ps: __H3Producer[], pi: i32): __H3Producer[] {
  let out: __H3Producer[] = [];
  let k: i32 = 0;
  while (k < ps.len()) {
    if (k != pi) {
      out = out.append(ps[k]);
    }
    k = k + 1;
  }
  return out;
}

function __h3_producer_dropped(ps: __H3Producer[], serial: i32, id: i64): __H3Producer[] {
  let out: __H3Producer[] = [];
  for p in ps {
    if (p.serial != serial || p.stream != id) {
      out = out.append(p);
    }
  }
  return out;
}

function __h3_producer_at(q: __Quic, serial: i32, id: i64): i32 {
  let k: i32 = 0;
  while (k < q.producers.len()) {
    if (q.producers[k].serial == serial && q.producers[k].stream == id) {
      return k;
    }
    k = k + 1;
  }
  return 0 - 1;
}

function __h3_more[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], serial: i32): (__Conns, __Flight[]) {
  let cur: __Conns = c;
  let fs: __Flight[] = fls;
  let k: i32 = 0;
  while (k < cur.quic.producers.len()) {
    let p: __H3Producer = cur.quic.producers[k];
    let before: i32 = cur.quic.producers.len();
    if (p.serial == serial && !p.running) {
      let r: (__Conns, __Flight[]) = __h3_produce(drv, cur, fs, k);
      cur = r.0;
      fs = r.1;
    }
    if (cur.quic.producers.len() == before) {
      k = k + 1;
    }
  }
  return (cur, fs);
}

function __h3_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, t: async.Task[Option[u8[]]], ready: i32): (__Conns, __Flight[]) {
  let k: i32 = __h3_producer_at(c.quic, fl.quic, fl.stream as i64);
  if (k < 0) {
    let gone: async.TaskStatus[Option[u8[]]] = async.task_cancel(t);
    async.task_free(t);
    return (c, fls);
  }
  match (async.task_resume(t, ready)) {
    Done(chunk) => {
      async.task_free(t);
      let r: (__Conns, boolean) = __h3_chunk(c, k, chunk);
      if (r.1) {
        return (__h3_flush(r.0, fl.quic), fls);
      }
      let more: (__Conns, __Flight[]) = __h3_produce(drv, r.0, fls, k);
      return (__h3_flush(more.0, fl.quic), more.1);
    },
    Suspended(w) => {
      return (c, fls.append(__flight_watch(drv, __Flight { ...fl, wait: w }, monotonic_ns())));
    },
    Cancelled => {
      async.task_free(t);
      return (__h3_flush(__h3_producer_failed(c, k), fl.quic), fls);
    }
  }
  return (c, fls);
}

function __h3_answer[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], fl: __Flight, resp: HttpResponse): (__Conns, __Flight[]) {
  let counted: __Conns = __serve_count_from(c, fl.method, resp.status, fl.since);
  let r: (__Conns, __Flight[]) = __h3_respond(drv, counted, fls, fl.quic, fl.stream as i64, fl.method, resp);
  return (__h3_flush(r.0, fl.quic), r.1);
}

function __h3_abandon(c: __Conns, fl: __Flight): __Conns {
  let q: __Quic = c.quic;
  let i: i32 = __h3_at(q, fl.quic);
  if (i < 0) {
    return c;
  }
  return __h3_flush(__Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].abort(fl.stream as i64, h3.H3_REQUEST_CANCELLED)) } }, fl.quic);
}

function __h3_flush(c: __Conns, serial: i32): __Conns {
  let q: __Quic = c.quic;
  let i: i32 = __h3_at(q, serial);
  if (i < 0) {
    return c;
  }
  let built: __Conns = __Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].transmit(monotonic_ns())) } };
  __h3_sent(built.quic.sock, built.quic.conns[i].datagrams(), built.quic.peers[i]);
  return __Conns { ...built, quic: __Quic { ...built.quic, conns: built.quic.conns.with(i, built.quic.conns[i].sent()) } };
}
```

### `80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, stop: __Stop): (__Co...`

```fern
<<80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, stop: __Stop): (__Co...>>=
function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flight[], opts: Config, stop: __Stop): (__Conns, __Flight[], __H3Burst) {
  let burst: __H3Burst = __H3Burst { reqs: [], touched: [] };
  let cur: __Conns = __h3_goaway(c, stop);
  let now: i64 = monotonic_ns();
  let i: i32 = 0;
  while (i < cur.quic.conns.len()) {
    let q: __Quic = cur.quic;
    let d: i64 = q.conns[i].deadline();
    if (d > 0 && d <= now) {
      cur = __Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].tick(now)) } };
      let taken: (__Conns, __H3Req[]) = __h3_events(cur, i);
      cur = __h3_flush(taken.0, q.serials[i]);
      if (taken.1.len() > 0) {
        burst = __h3_touched(__H3Burst { ...burst, reqs: burst.reqs.concat(taken.1) }, q.serials[i]);
      }
    }
    i = i + 1;
  }
  return (__h3_forget_done(cur), fls, burst);
}

function __h3_goaway(c: __Conns, stop: __Stop): __Conns {
  let q: __Quic = c.quic;
  if (q.stopping || !__serve_stopping(stop)) {
    return c;
  }
  let cur: __Conns = __Conns { ...c, quic: __Quic { ...q, stopping: true, conns: __each_shutdown(q.conns) } };
  for s in q.serials {
    cur = __h3_flush(cur, s);
  }
  return cur;
}

function __each_shutdown(cs: h3q.Conn[]): h3q.Conn[] {
  let out: h3q.Conn[] = [];
  for c in cs {
    out = out.append(c.shutdown());
  }
  return out;
}

function __h3_forget_done(c: __Conns): __Conns {
  let q: __Quic = c.quic;
  let kept: __Quic = __Quic { ...q, conns: [], keys: [], origs: [], peers: [], serials: [] };
  let producers: __H3Producer[] = q.producers;
  let i: i32 = 0;
  while (i < q.conns.len()) {
    if (q.conns[i].done()) {
      producers = __h3_producers_of_gone(producers, q.serials[i]);
    } else {
      kept = __Quic { ...kept, conns: kept.conns.append(q.conns[i]), keys: kept.keys.append(q.keys[i]), origs: kept.origs.append(q.origs[i]), peers: kept.peers.append(q.peers[i]), serials: kept.serials.append(q.serials[i]) };
    }
    i = i + 1;
  }
  return __Conns { ...c, quic: __Quic { ...kept, producers: producers } };
}

function __h3_producers_of_gone(ps: __H3Producer[], serial: i32): __H3Producer[] {
  let out: __H3Producer[] = [];
  for p in ps {
    if (p.serial != serial) {
      out = out.append(p);
    }
  }
  return out;
}
```
