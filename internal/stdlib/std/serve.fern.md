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

<<12 pub struct ListenError>>

<<13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)>>

<<14 function __every_v4(): u8[]>>

<<15 struct __Conns>>

<<16 function __pulling(c: __Conns, at: i32): Option[__Body]>>

<<17 enum __Tail>>

<<18 function __after_range(own r: Reader, rest: __Spans): Option[__Tail]>>

<<19 struct __Clock>>

<<20 function __clock_tick(clk: __Clock): __Clock>>

<<21 function __file_measured(resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse>>

<<22 struct __FileBody>>

<<23 function __file_chunk(f: __FileBody, i: i32): Option[u8[]]>>

<<24 function __tail_left(c: __Conns, at: i32): i32>>

<<25 function __chunk_step(producer: ChunkProducer, index: i32, end: string, chunk: Option[u8[]]): (__Tail, u8[]...>>

<<26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, pool: fetch.Idl...>>

<<27 const SLOT_LIMIT: i32>>

<<28 function __slot_of(slots: u8[], fd: i32): i32>>

<<29 function __serve_accept[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config)>>

<<30 function __serve_read_proxy[D: async.Driver](drv: D, c: __Conns, at: i32, next: Option[server.Session], hea...>>

<<31 function __serve_handshook(c: __Conns, at: i32, was: boolean, done: boolean, opts: Config): (__Conns, boole...>>

<<32 enum __Behind>>

<<33 function __serve_behind(fr: http.HttpFraming, none: http.HttpFramed): (__Behind, http.HttpFramed)>>

<<34 function __serve_continue[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config): __Conns>>

<<35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...>>

<<36 const H2_NOT: i32>>

<<37 function __h2_mode(c: __Conns, at: i32, opts: Config): i32>>

<<38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fd: i32, opts: Config): (__...>>

<<39 function __h2_framed(c: __Conns, fd: i32): boolean>>

<<40 struct __H2Answered>>

<<41 function __h2_pending_max(): i32>>

<<42 function __h2_stream_reset(c: __Conns, fd: i32, sid: i32): __Conns>>

<<43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fl: __Flight, t: async.Ta...>>

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

<<54 function __body_source(sock: Socket, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource>>

<<55 function __pull_read(p: __Pull, raw: u8[]): u8[]>>

<<56 function __body_live(fl: __Flight): boolean>>

<<57 struct __Flight>>

<<58 function __started_again(s: __Started): __Started>>

<<59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fi: i32, fd: i32, readine...>>

<<60 function __upgrading(resp: HttpResponse): boolean>>

<<61 function __flight_pair(fl: __Flight, fd: i32): i32>>

<<62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flights, fd: i32, re...>>

<<63 function __serve_loop(l: __Listen, handler: (HttpRequest, platform.Host)>>

<<64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, eof: boolean,...>>

<<65 function __serve_loop_with[S](l: __Listen, init: S, handler: (S, HttpRequest, platform.Host)>>

<<66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>

<<67 function __supervise(port: i32, opts: Config, worker: (__Listen, Config, Option[Writer])>>

<<68 function __supervisor_fork(drv: async.RealDriver, own: __Listen, listener: i32, worker: (i32, Config, Opt...>>

<<69 function __exporter_up(ex: Option[metrics.Exporter]): boolean>>

<<70 struct __Supervisor>>

<<71 function (s: __Supervisor) gave_up(): boolean>>

<<72 function __port_from_env(name: string, fallback: i32): i32>>

<<73 struct __Quic>>

<<74 function __h3_pending_max(): i64>>

<<75 function __h3_sent(sock: Socket, out: u8[][], path: [u8]): void>>

<<76 struct __H3Req>>

<<77 function __h3_touched(b: __H3Burst, serial: i32): __H3Burst>>

<<78 function __h3_start_all[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, burst: __H3Burst, handler...>>

<<79 function __h3_chunk(c: __Conns, pi: i32, chunk: Option[u8[]]): (__Conns, boolean)>>

<<80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, stop: __Stop): (__Co...>>
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
import "core/error";
import "core/cmp";
import "std/platform";
import "std/fetch";
import "std/http/trace";
import "std/http/cookie";
import "std/http/forwarded";
import "std/http";
import "std/stream";
import "std/net";
import "std/time";
import "std/clock";
import "std/metrics";
import "std/tls";
import "std/tls/server";
import "std/tls/ktls";
import "std/i64";
import "std/envflag";
import "std/http/grpc";
import "std/http/h2";
import "std/http/h3";
import "std/http/h3q";
import "std/array";
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
  h3_retry_above: i32,
}
```

### `5 pub function config(): Config`

```fern
<<5 pub function config(): Config>>=
pub function config(): Config {
  return Config { backlog: 65535, reuse_port: false, recv_deadline: __recv_deadline_default(), min_data_rate: 240, data_rate_grace: time.duration_seconds(5 as i64), response_min_data_rate: 240, response_data_rate_grace: time.duration_seconds(5 as i64), keep_alive_idle: time.duration_seconds(130 as i64), keep_alive_requests: 1000, max_connections: 1024, max_connections_per_ip: 100, max_in_flight: 1024, workers: 0, shutdown_grace: time.duration_seconds(2 as i64), readiness_path: "", drain_deadline: time.duration_seconds(30 as i64), limits: http.limits(), stop_with_parent: false, stream_bodies: false, tls: None, cookie_jar: false, http_allow: None, handler_deadline: time.duration_seconds(0 as i64), background_deadline: time.duration_seconds(30 as i64), trace: false, proxy_protocol: false, metrics: metrics.registry(), metrics_port: 0, h2: false, io_uring: false, io_uring_zc_bytes: 3072, ktls: false, h3: false, h3_sock: 0 - 1, h3_retry_above: 32 };
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
pub function run(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse): Result[i32, ListenError] {
  return run_shutdown(port, opts, handler, (reason: string): void => {});
}

pub function run_shutdown(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse, shutdown: (string) => void): Result[i32, ListenError] {
  let l = __serve_listener(port, opts);
  if (l.sock.fd < 0) {
    return Err(__listen_error(port, l.sock.fd));
  }
  return __serve_loop(l, handler, opts, shutdown, None);
}
```

### `10 function __serve_listener(port: i32, opts: Config): __Listen`

```fern
<<10 function __serve_listener(port: i32, opts: Config): __Listen>>=
function __serve_listener(port: i32, opts: Config): __Listen {
  let l = __listener_of(port, opts);
  if (l.sock.fd >= 0) {
    tcp_socket_ctl(l.sock, 3, 1);
  }
  return l;
}
```

### `11 struct __Listen`

```fern
<<11 struct __Listen>>=
// h3_key seals the address-validation tokens the HTTP/3 listener's Retry
// packets carry, and the session tickets it issues. One serve.run picks
// it, so every worker it forks accepts the tokens and tickets any of them
// issued.
struct __Listen { sock: Socket, inherited: boolean, h3_key: u8[] }

function __listen(sock: Socket, inherited: boolean): __Listen {
  return __Listen { sock: sock, inherited: inherited, h3_key: random_bytes(32) };
}

// __listen_release closes a listener serve bound itself. An inherited
// LISTEN_FDS listener stays open, so a caller can retry on it.
function __listen_release(l: __Listen): void {
  if (l.sock.fd >= 0 && !l.inherited) {
    __shut(l.sock);
  }
}

// A descriptor this process inherited, from LISTEN_FDS or across a fork.
function __adopt(fd: i32): Socket {
  unsafe {
    return __socket_from_fd(fd);
  }
}

// The loop copies a __Listen and a __Conns, so a socket closes through a borrow, once, when its owner lets it go.
function __shut(sock: Socket): i32 {
  unsafe {
    return __tcp_close_unchecked(sock);
  }
}
```

### `12 pub struct ListenError`

A port the server could not bind: the server's own, or `metrics_port`.

```fern
<<12 pub struct ListenError>>=
pub struct ListenError { port: i32, error: net.NetError }

impl cmp.Display for ListenError {}

impl error.Error for ListenError {}

pub function (e: ListenError) message(): string {
  return "cannot listen on port " + e.port.to_string() + ": " + e.error.message();
}

pub function (e: ListenError) to_string(): string {
  return e.message();
}

// exit_code is the status for `main` to return: the server's own, or 98
// after printing a bind failure to stderr.
pub function exit_code(r: Result[i32, ListenError]): i32 {
  match (r) {
    Ok(code) => {
      return code;
    },
    Err(e) => {
      eprint("serve: " + e.message());
      return 98;
    }
  }
  return 98;
}

function __listen_error(port: i32, code: i32): ListenError {
  return ListenError { port: port, error: net.error_from_host(code) };
}

function __listener_of(port: i32, opts: Config): __Listen {
  match (env("LISTEN_FDS")) {
    Some(n) => {
      match (i32.parse_i32_radix(n, 10)) {
        Ok(count) => {
          if (count >= 1) {
            return __listen(__adopt(3), true);
          }
        },
        Err(_) => {}
      }
    },
    None => {}
  }
  if (target_os() != "wasi") {
    let both = tcp_listen_with(__every_v6(), port, opts.backlog, opts.reuse_port);
    if (both.fd >= 0 || !net.error_from_host(both.fd).ipv6_unavailable()) {
      return __listen(both, false);
    }
  }
  return __listen(tcp_listen_with(__every_v4(), port, opts.backlog, opts.reuse_port), false);
}
```

### `13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)`

```fern
<<13 pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host)>>=
pub function run_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse)): Result[i32, ListenError] {
  return run_with_shutdown(port, opts, init, handler, (reason: string, state: S): void => {});
}

pub function run_with_shutdown[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), shutdown: (string, S) => void): Result[i32, ListenError] {
  let l = __serve_listener(port, opts);
  if (l.sock.fd < 0) {
    return Err(__listen_error(port, l.sock.fd));
  }
  return __serve_loop_with(l, init, handler, opts, shutdown, None);
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
struct __Conns { open: i32, free: i32, fds: i32[], socks: Socket[], slots: u8[], bufs: u8[][], offs: i32[], pending: u8[][], deadlines: i64[], due: i64, keep: boolean[], served: i32[], backlog: boolean[], queued: i32[], continued: boolean[], rate_since: i64[], rate_base: i32[], send_since: i64[], send_base: i32[], peers: i32[], request_host: platform.Host, addresses: Option[net.SocketAddr][], tails: __Tail[], tls: __Tls[], framing: __Framing[], last: http.HttpFramed, out: usize, lent: i32, parked: __Flight[], later: (() => void)[], metrics: metrics.ServerMetrics, pulls: __H2Pull[], producers: __H2Producer[], tls_reads: i32, zc_bytes: i32, zc_held: u8[][], zc_slots: i32[], zc_free: boolean[], quic: __Quic, date: string, peer_counts: __PeerCounts }

// The open connections of each peer key, in an open-addressing table whose
// size is a power of two. A zero key marks an empty slot, and no peer key is 0.
struct __PeerCounts { keys: i32[], counts: i32[], used: i32 }

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

### `18 function __after_range(own r: Reader, rest: __Spans): Option[__Tail]`

```fern
<<18 function __after_range(own r: Reader, rest: __Spans): Option[__Tail]>>=
function __after_range(own r: Reader, rest: __Spans): Option[__Tail] {
  let next = __Spans { ranges: rest.ranges, seams: __from(rest.seams, 1) };
  if (rest.seams.len() > 0 && rest.seams[0].len() > 0) {
    return Some(SeamTail(r, rest.seams[0], next));
  }
  return __range_tail(r, next);
}

function __range_tail(own r: Reader, sp: __Spans): Option[__Tail] {
  if (sp.ranges.len() < 2) {
    r.close();
    return Some(NoTail);
  }
  let rest = __Spans { ranges: __from(sp.ranges, 2), seams: sp.seams };
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
  let i = from;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i += 1;
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
  let date: string = time.timestamp_from_unix(sec).format_http_date();
  return __Clock { sec: sec, date: date, line: "Date: " + date + "\r\n" };
}

function __serve_wire(out: usize, method: string, version: string, answer: HttpResponse, keep_alive: boolean, clk: __Clock): __Tail {
  if (!http.fields_ok(answer.headers) || !http.fields_ok(answer.trailers)) {
    return __serve_wire(out, method, version, http.internal_error(""), keep_alive, clk);
  }
  let date = clk.line;
  if (answer.headers.has("date")) {
    date = "";
  }
  let headless = http.bodiless(answer.status) || method == "HEAD";
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
        http.serialize_response_head_into(out, version, answer, false, date);
        return ChunkTail(producer, 0, "");
      }
      return __wire_chunks(out, version, answer, producer, headless, keep_alive, date);
    },
    _ => {
      http.serialize_response_into(out, method, version, answer, keep_alive, date);
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
      let framing = date + "Content-Length: " + http.file_ranges_len(ranges, body.seams).to_string() + "\r\n";
      if (headless) {
        r.close();
        http.serialize_response_head_into(out, version, resp, keep_alive, framing);
        return NoTail;
      }
      match (__range_tail(r, __Spans { ranges: ranges, seams: __from(body.seams, 1) })) {
        Some(t) => {
          http.serialize_response_head_into(out, version, resp, keep_alive, framing);
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
  let i = 0;
  while (i + 1 < ranges.len()) {
    if (ranges[i] + ranges[i + 1] > size) {
      return none;
    }
    i += 2;
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
      if (!http.bodiless(resp.status)) {
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
    http.serialize_response_head_into(out, version, resp, keep_alive, date);
    return NoTail;
  }
  if (version == "HTTP/1.0") {
    http.serialize_response_head_into(out, version, resp, false, date);
    return ChunkTail(producer, 0, "");
  }
  let framing: string = date + "Transfer-Encoding: chunked\r\n";
  if (resp.trailers.names.len() > 0) {
    framing = framing + "Trailer: " + resp.trailers.names.join(", ") + "\r\n";
  }
  http.serialize_response_head_into(out, version, resp, keep_alive, framing);
  return ChunkTail(producer, 0, http.serialize_fields(resp.trailers) + "\r\n");
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

// Takes the tail out of its slot, so a reader it holds is closed once.
function __tail_drop(own c: __Conns, at: i32): __Conns {
  let (held, tails) = c.tails.swap_at(at, NoTail);
  let d = __Conns { ...c, tails: tails };
  match (held) {
    FileTail(r, left, rest) => {
      r.close();
    },
    SeamTail(r, seam, rest) => {
      r.close();
    },
    ZcTail(_, _, slot) => {
      d = __zc_settled(d, slot);
    },
    _ => {}
  }
  return d;
}

// Moves the reader of the tail at `at` past its current range or seam.
function __tail_advance(own c: __Conns, at: i32, piece: u8[]): (__Conns, u8[], boolean) {
  let (held, tails) = c.tails.swap_at(at, NoTail);
  let d = __Conns { ...c, tails: tails };
  match (held) {
    FileTail(r, left, after) => {
      return __tail_then(d, at, __after_range(r, after), piece);
    },
    SeamTail(r, seam, after) => {
      return __tail_then(d, at, __range_tail(r, after), piece);
    },
    _ => {}
  }
  return (d, piece, false);
}

function __tail_piece[D: async.Driver](drv: D, own c: __Conns, at: i32): (__Conns, u8[], boolean) {
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
      return __tail_advance(c, at, seam);
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

function __file_piece(own c: __Conns, at: i32, r: Reader, left: i64, after: __Spans): (__Conns, u8[], boolean) {
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
        return __tail_advance(c, at, bytes);
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

function __chunk_piece[D: async.Driver](drv: D, own c: __Conns, at: i32, producer: ChunkProducer, index: i32, end: string): (__Conns, u8[], boolean) {
  let none: u8[] = [];
  let next = producer.next;
  let i = index;
  while (true) {
    let t = async.task_new((): Option[u8[]] => next(i));
    match (async.task_start(t)) {
      Done(chunk) => {
        let step = __chunk_step(producer, i, end, chunk);
        if (step.2) {
          return (__Conns { ...c, tails: c.tails.with(at, step.0) }, step.1, true);
        }
      },
      Suspended(w) => {
        return (__tail_park(drv, c, at, producer, i, end, t, w), none, true);
      }
    }
    i += 1;
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
      let tail = ChunkTail(producer, index + 1, end);
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
  return ("0\r\n" + http.serialize_fields(producer.trailers()) + end).bytes();
}

function __chunk_framed(bytes: u8[], end: string): u8[] {
  if (end.len() == 0) {
    return bytes;
  }
  return (bytes.len().to_string_radix(16) + "\r\n").bytes().concat(bytes).concat("\r\n".bytes());
}

function __idle_due(producer: ChunkProducer, now: i64): i64 {
  if (producer.idle_ms <= 0 || producer.filler.len() == 0) {
    return 0 - 1 as i64;
  }
  return now + producer.idle_ms as i64 * 1000000;
}

function __flight_filler[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, fi: i32, opts: Config): (__Conns, __Flights) {
  let fl = fls.all[fi];
  let at = __conns_index(c, fl.fd);
  if (at < 0) {
    return (c, __flights_idle(fls, fi, -1i64));
  }
  let piece: u8[] = [];
  match (c.tails[at]) {
    ParkedTail(producer, _, end) => {
      piece = __chunk_framed(producer.filler, end);
      fls = __flights_idle(fls, fi, __idle_due(producer, monotonic_ns()));
    },
    _ => {
      return (c, __flights_idle(fls, fi, -1i64));
    }
  }
  if (c.pending[at].len() > 0) {
    return (c, fls);
  }
  let (sent_c, sent_how) = __serve_send(drv, c, at, piece, opts);
  match (sent_how) {
    Failed => {
      __flight_cancel(drv, fls.all[fi]);
      return (__serve_close(drv, sent_c, at), __flights_remove(fls, fi));
    },
    _ => {
      return (sent_c, fls);
    }
  }
  return (sent_c, fls);
}

function __tail_park[D: async.Driver](drv: D, own c: __Conns, at: i32, producer: ChunkProducer, index: i32, end: string, t: async.Task[Option[u8[]]], w: async.Wait): __Conns {
  let none: i32[] = [];
  let now: i64 = monotonic_ns();
  let fl: __Flight = __flight_watch(drv, __Flight { job: Producer(t), wait: w, fd: c.fds[at], set: none, due: 0 - 1 as i64, method: "", version: "", kept: false, behind: Idle, body: __pulling(c, at), idle: __idle_due(producer, now), ends: 0 - 1 as i64, stream: 0, since: 0 - 1 as i64, grpc: false, quic: 0 });
  match (c.framing[at]) {
    Pulling(b) => {},
    _ => {
      drv.watch(c.fds[at], __read_interest(c, c.tls[at]));
    }
  }
  return __Conns { ...c, tails: c.tails.with(at, ParkedTail(producer, index, end)), deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()), parked: c.parked.append(fl) };
}

function __producer_done[D: async.Driver](drv: D, own c: __Conns, fd: i32, chunk: Option[u8[]], opts: Config, clk: __Clock): __Conns {
  let at = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  let piece: u8[] = [];
  match (c.tails[at]) {
    ParkedTail(producer, index, end) => {
      let step = __chunk_step(producer, index, end, chunk);
      c = __Conns { ...c, tails: c.tails.with(at, step.0) };
      piece = step.1;
    },
    _ => {
      return c;
    }
  }
  if (piece.len() > 0) {
    let sent = __serve_send(drv, c, at, piece, opts);
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
  let drained = __serve_drain(drv, c, at, opts, clk);
  c = drained.0;
  if (drained.1 >= 0) {
    return __backlog_on(c, drained.1);
  }
  return c;
}
```

### `26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, pool: fetch.Idl...`

```fern
<<26 function __staged_taken[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, pool: fetch.Idl...>>=
function __staged_taken[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, opts: Config, pool: fetch.Idle): (__Conns, __Flights) {
  let taken = __parked_taken(c, fls);
  c = taken.0;
  fls = taken.1;
  if (__upstream_busy(pool)) {
    let upstreams = __shared_turn(drv, pool, c, fls, opts);
    c = upstreams.0;
    fls = upstreams.1;
  }
  if (c.later.len() == 0) {
    return (c, fls);
  }
  let work = c.later;
  let none: (() => void)[] = [];
  c = __Conns { ...c, later: none };
  for w in work {
    fls = __background_start(drv, fls, w, opts);
  }
  return (c, fls);
}

function __background_start[D: async.Driver](drv: D, fls: __Flights, work: () => void, opts: Config): __Flights {
  let t = async.task_new((): i32 => __background_run(work));
  match (async.task_start(t)) {
    Suspended(w) => {
      let none: i32[] = [];
      let now = monotonic_ns();
      return __flights_add(fls, __flight_watch(drv, __Flight { job: Background(t), wait: w, fd: -1, set: none, due: -1i64, method: "", version: "", kept: false, behind: Idle, body: None, idle: -1i64, ends: __bound_ends(opts.background_deadline, now), stream: 0, since: -1i64, grpc: false, quic: 0 }));
    },
    Done(v) => {}
  }
  return fls;
}

function __background_run(work: () => void): i32 {
  work();
  return 0;
}

function __serve_count(own c: __Conns, at: i32, method: string, status: i32): own __Conns {
  let since = c.rate_since[at];
  return __serve_count_from(c, method, status, since);
}

function __serve_count_from(own c: __Conns, method: string, status: i32, since: i64): own __Conns {
  let m = c.metrics;
  let grown = m.request(method, status, time.duration_nanos(monotonic_ns() - since));
  if (grown.methods.len() == m.methods.len()) {
    return c;
  }
  return __Conns { ...c, metrics: grown };
}

function __stage_background(own c: __Conns, resp: HttpResponse): own __Conns {
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

function __parked_taken(c: __Conns, fls: __Flights): (__Conns, __Flights) {
  if (c.parked.len() == 0) {
    return (c, fls);
  }
  let none: __Flight[] = [];
  let fs: __Flights = fls;
  for fl in c.parked {
    fs = __flights_add(fs, fl);
  }
  return (__Conns { ...c, parked: none }, fs);
}

function __conns_index(c: __Conns, fd: i32): i32 {
  if (fd >= 0 && fd < SLOT_LIMIT) {
    let v = __slot_of(c.slots, fd);
    if (v != SLOT_FULL) {
      return v - 1;
    }
  }
  let i = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] == fd) {
      return i;
    }
    i += 1;
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

function __conns_add(own c: __Conns, own sock: Socket, deadline: i64, peer: i32, addr: Option[net.SocketAddr], tls: __Tls): __Conns {
  let fd: i32 = sock.fd;
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
    return __Conns { ...c, open: c.open + 1, free: next, fds: c.fds.with(k, fd), socks: c.socks.with(k, sock), slots: __slot_set(c.slots, fd, k + 1), bufs: c.bufs.with(k, empty), offs: c.offs.with(k, 0), pending: c.pending.with(k, empty), deadlines: c.deadlines.with(k, deadline), due: __sooner(c.due, deadline), keep: c.keep.with(k, keep), served: c.served.with(k, 0), backlog: c.backlog.with(k, false), continued: c.continued.with(k, false), rate_since: c.rate_since.with(k, 0 - 1 as i64), rate_base: c.rate_base.with(k, 0), send_since: c.send_since.with(k, 0 - 1 as i64), send_base: c.send_base.with(k, 0), peers: c.peers.with(k, peer), addresses: c.addresses.with(k, addr), tails: c.tails.with(k, NoTail), tls: c.tls.with(k, tls), framing: c.framing.with(k, Http1), peer_counts: __peer_raise(c.peer_counts, peer) };
  }
  return __Conns { open: c.open + 1, free: c.free, fds: c.fds.append(fd), socks: c.socks.append(sock), slots: __slot_set(c.slots, fd, c.fds.len() + 1), bufs: c.bufs.append(empty), offs: c.offs.append(0), pending: c.pending.append(empty), deadlines: c.deadlines.append(deadline), due: __sooner(c.due, deadline), keep: c.keep.append(keep), served: c.served.append(0), backlog: c.backlog.append(false), queued: c.queued, continued: c.continued.append(false), rate_since: c.rate_since.append(0 - 1 as i64), rate_base: c.rate_base.append(0), send_since: c.send_since.append(0 - 1 as i64), send_base: c.send_base.append(0), peers: c.peers.append(peer), request_host: c.request_host, addresses: c.addresses.append(addr), tails: c.tails.append(NoTail), tls: c.tls.append(tls), framing: c.framing.append(Http1), last: c.last, out: c.out, lent: c.lent, parked: c.parked, later: c.later, metrics: c.metrics, pulls: c.pulls, producers: c.producers, tls_reads: c.tls_reads, zc_bytes: c.zc_bytes, zc_held: c.zc_held, zc_slots: c.zc_slots, zc_free: c.zc_free, quic: c.quic, date: c.date, peer_counts: __peer_raise(c.peer_counts, peer) };
}

function __with_connection_host(own c: __Conns, at: i32): own __Conns {
  c = __Conns { ...c, request_host: c.request_host.with_peer(c.addresses[at]) };
  return c;
}

function __peer_home(peer: i32, size: i32): i32 {
  let h: i32 = peer * -1640531535;
  return (h ^ h >> 16) & size - 1;
}

function __peer_slot(keys: i32[], peer: i32): i32 {
  let i: i32 = __peer_home(peer, keys.len());
  while (keys[i] != 0 && keys[i] != peer) {
    i = i + 1 & keys.len() - 1;
  }
  return i;
}

function __peer_count(t: __PeerCounts, peer: i32): i32 {
  if (t.keys.len() == 0) {
    return 0;
  }
  let i: i32 = __peer_slot(t.keys, peer);
  if (t.keys[i] != peer) {
    return 0;
  }
  return t.counts[i];
}

function __peer_raise(t: __PeerCounts, peer: i32): __PeerCounts {
  if (peer == 0) {
    return t;
  }
  if ((t.used + 1) * 2 > t.keys.len()) {
    t = __peer_grow(t);
  }
  let i = __peer_slot(t.keys, peer);
  if (t.keys[i] == peer) {
    let n = t.counts[i];
    return __PeerCounts { ...t, counts: t.counts.with(i, n + 1) };
  }
  return __PeerCounts { keys: t.keys.with(i, peer), counts: t.counts.with(i, 1), used: t.used + 1 };
}

function __peer_grow(t: __PeerCounts): __PeerCounts {
  let size: i32 = t.keys.len() * 2;
  if (size < 16) {
    size = 16;
  }
  let keys: i32[] = [];
  let counts: i32[] = [];
  while (keys.len() < size) {
    keys = keys.append(0);
    counts = counts.append(0);
  }
  let i: i32 = 0;
  while (i < t.keys.len()) {
    let peer: i32 = t.keys[i];
    if (peer != 0) {
      let at: i32 = __peer_slot(keys, peer);
      keys = keys.with(at, peer);
      counts = counts.with(at, t.counts[i]);
    }
    i = i + 1;
  }
  return __PeerCounts { keys: keys, counts: counts, used: t.used };
}

// The last connection of a peer empties its slot, and later keys of the same
// probe run shift back so that no lookup stops early.
function __peer_lower(t: __PeerCounts, peer: i32): __PeerCounts {
  if (peer == 0 || t.keys.len() == 0) {
    return t;
  }
  let i: i32 = __peer_slot(t.keys, peer);
  if (t.keys[i] != peer) {
    return t;
  }
  let n: i32 = t.counts[i];
  if (n > 1) {
    return __PeerCounts { ...t, counts: t.counts.with(i, n - 1) };
  }
  let mask: i32 = t.keys.len() - 1;
  let hole: i32 = i;
  let k: i32 = i + 1 & mask;
  while (t.keys[k] != 0) {
    let key: i32 = t.keys[k];
    let home: i32 = __peer_home(key, t.keys.len());
    if ((k - home & mask) >= (k - hole & mask)) {
      let count: i32 = t.counts[k];
      t = __PeerCounts { ...t, keys: t.keys.with(hole, key), counts: t.counts.with(hole, count) };
      hole = k;
    }
    k = k + 1 & mask;
  }
  return __PeerCounts { keys: t.keys.with(hole, 0), counts: t.counts.with(hole, 0), used: t.used - 1 };
}

function __conns_remove(own c: __Conns, at: i32): __Conns {
  let fd = c.fds[at];
  let peer = c.peers[at];
  let was = c.backlog[at];
  let next = c.free;
  let lent = c.lent;
  if (lent == at) {
    lent = -1;
  }
  let empty: u8[] = [];
  let r = __Conns { ...c, open: c.open - 1, free: at, fds: c.fds.with(at, -1), slots: __slot_set(c.slots, fd, 0), addresses: c.addresses.with(at, None), bufs: c.bufs.with(at, empty), offs: c.offs.with(at, next), pending: c.pending.with(at, empty), backlog: c.backlog.with(at, false), tails: c.tails.with(at, NoTail), tls: c.tls.with(at, Plain), framing: c.framing.with(at, Http1), lent: lent, peer_counts: __peer_lower(c.peer_counts, peer) };
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

function __backlog_on(own c: __Conns, at: i32): own __Conns {
  if (c.backlog[at]) {
    return c;
  }
  let fd = c.fds[at];
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

### `29 function __serve_accept[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config)`

```fern
<<29 function __serve_accept[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config)>>=
function __serve_accept[D: async.Driver](drv: D, listener: Socket, own c: __Conns, opts: Config): __Conns {
  let sock = tcp_accept_with(listener, true);
  if (sock.fd < 0) {
    return c;
  }
  let addr = __peer_of(sock);
  let peer = 0;
  if (opts.max_connections_per_ip > 0) {
    peer = __peer_key_of(addr);
    if (peer != 0 && __peer_count(c.peer_counts, peer) >= opts.max_connections_per_ip) {
      tcp_close(sock);
      return c;
    }
  }
  tcp_socket_ctl(sock, 1, 1);
  let tls = Plain;
  match (__tls_session(opts)) {
    Ok(t) => {
      tls = t;
    },
    Err(_) => {
      tcp_close(sock);
      return c;
    }
  }
  if (opts.proxy_protocol) {
    tls = __awaiting(tls);
  }
  if (drv.watch(sock.fd, __read_interest(c, tls)) < 0) {
    tcp_close(sock);
    return c;
  }
  c.metrics.accepted.inc();
  c.metrics.open.inc();
  return __conns_add(c, sock, monotonic_ns() + opts.recv_deadline.as_nanos(), peer, addr, tls);
}

// __peer_key_of keys a peer for the per-address connection count as
// net.peer_key does: an IPv4 address as one word, an IPv6 address by its
// first eight bytes, never 0 for an address that is known.
function __peer_key_of(addr: Option[net.SocketAddr]): i32 {
  let Some(a) = addr else {
    return 0;
  };
  match (a.ip) {
    V4(b) => {
      return b[0] as i32 | b[1] as i32 << 8 | b[2] as i32 << 16 | b[3] as i32 << 24;
    },
    V6(b) => {
      let key: i32 = (b[0] as i32 | b[1] as i32 << 8 | b[2] as i32 << 16 | b[3] as i32 << 24) ^ (b[4] as i32 | b[5] as i32 << 8 | b[6] as i32 << 16 | b[7] as i32 << 24);
      if (key == 0) {
        return 1;
      }
      return key;
    }
  }
  return 0;
}

function __peer_of(sock: Socket): Option[net.SocketAddr] {
  match (net.peer_addr(sock)) {
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
      let sess: server.Session = server.start(offered, random_bytes(server.entropy(offered)))?.session;
      return Ok(Secured(sess));
    },
    None => {
      return Ok(Plain);
    }
  }
  return Ok(Plain);
}

function __serve_close[D: async.Driver](drv: D, own c: __Conns, at: i32): own __Conns {
  drv.unwatch(c.fds[at]);
  c.metrics.open.dec();
  match (c.tls[at]) {
    Secured(sess) => {
      if let Ok(step) = sess.close() {
        tcp_send_bytes(c.socks[at], step.output);
      }
    },
    InKernel(sess) => {
      if let Ok(step) = sess.close() {
        ktls.send_records(c.socks[at], step.output);
      }
    },
    Lent => {
      __lent_closed(c, at);
    },
    _ => {}
  }
  __shut(c.socks[at]);
  return __conns_remove(__tail_drop(c, at), at);
}

function __serve_read[D: async.Driver](drv: D, own c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
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

function __awaiting(own t: __Tls): own __Tls {
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
function __serve_read_proxy[D: async.Driver](drv: D, own c: __Conns, at: i32, next: Option[server.Session], head: u8[], scratch: u8[], opts: Config): (__Conns, boolean) {
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

function __proxied[D: async.Driver](drv: D, own c: __Conns, at: i32, next: Option[server.Session], src: Option[net.SocketAddr], rest: u8[], opts: Config, done: boolean): (__Conns, boolean) {
  match (src) {
    Some(a) => {
      c = __Conns { ...c, addresses: c.addresses.with(at, src) };
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
  unsafe {
    let buf = c.bufs[at];
    let was_idle = buf.len() == 0;
    let lent = c.lent;
    let done = false;
    let more = true;
    while (more) {
      if (lent == at) {
        buf = __bytes_from(buf, 0);
        lent = -1;
      }
      __arr_set_len(scratch, __recv_room());
      let n = drv.recv_into(c.socks[at], scratch);
      if (n == 0 - __eagain()) {
        more = false;
      } else if (n <= 0) {
        let stopped = __plain_stopped(c, at, n);
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
        if (buf.len() > http.request_bytes_cap(opts.limits)) {
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
    let deadline = monotonic_ns() + opts.recv_deadline.as_nanos();
    return (__Conns { ...c, bufs: c.bufs.with(at, buf), lent: lent, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline) }, false);
  }
}

function __read_onto(buf: u8[], plain: u8[]): u8[] {
  if (buf.len() == 0) {
    return plain;
  }
  return buf.concat(plain);
}

function __serve_read_tls[D: async.Driver](drv: D, own c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
  unsafe {
    let buf = c.bufs[at];
    let was_idle = buf.len() == 0;
    let t = c.tls[at];
    c = __Conns { ...c, tls: c.tls.with(at, Plain) };
    let was = false;
    if let Secured(sess) = t {
      was = sess.connected();
    }
    let done = false;
    let more = true;
    while (more) {
      __arr_set_len(scratch, __recv_room());
      let n = drv.recv_into(c.socks[at], scratch);
      if (n == 0 - __eagain()) {
        more = false;
      } else if (n <= 0) {
        more = false;
        done = true;
      } else {
        let took = __tls_taken(drv, c, at, t, scratch[0:n], opts);
        c = took.0;
        t = took.1;
        buf = __read_onto(buf, took.2);
        done = took.3;
        more = !done && n == __recv_room() && buf.len() <= http.request_bytes_cap(opts.limits);
        if (buf.len() > http.request_bytes_cap(opts.limits)) {
          done = true;
        }
      }
    }
    c = __Conns { ...c, bufs: c.bufs.with(at, buf), tls: c.tls.with(at, t) };
    let off = __serve_handshook(c, at, was, done, opts);
    c = off.0;
    done = off.1;
    if (done || !was_idle || buf.len() == 0) {
      return (c, done);
    }
    let deadline = monotonic_ns() + opts.recv_deadline.as_nanos();
    return (__Conns { ...c, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline) }, false);
  }
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
    match (ktls.attach(c.socks[at], o.keys)) {
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
  if (n == -5) {
    if let InKernel(_) = c.tls[at] {
      return __kernel_record(c, at);
    }
  }
  return (c, true);
}

function __kernel_record(c: __Conns, at: i32): (__Conns, boolean) {
  let sock = c.socks[at];
  if let InKernel(sess) = c.tls[at] {
    match (ktls.read_now(sock)) {
      ktls.Opened(t, content) => {
        match (sess.read(ktls.framed(t, content), clock.now())) {
          Ok(step) => {
            let c2 = __Conns { ...c, tls: c.tls.with(at, InKernel(step.session)) };
            if (ktls.send_records(sock, step.output) < 0 || ktls.install(sock, step.rekeyed) < 0) {
              return (c2, true);
            }
            return (c2, step.closed);
          },
          Err(f) => {
            ktls.send_records(sock, f.alert);
            return (c, true);
          }
        }
      },
      ktls.NothingYet => {
        return (c, false);
      },
      ktls.ReadFailed(e) => {
        if (e == 74) {
          ktls.alert(sock, 20);
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

function __tls_taken[D: async.Driver](drv: D, own c: __Conns, at: i32, t: __Tls, raw: [u8], opts: Config): (__Conns, __Tls, u8[], boolean) {
  let none: u8[] = [];
  if let Secured(sess) = t {
    match (sess.read(raw, clock.now())) {
      Ok(step) => {
        let done = step.closed;
        if (step.output.len() > 0) {
          let sent = __serve_send_raw(drv, c, at, step.output, opts);
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
        tcp_send_bytes(c.socks[at], f.alert);
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
    http.Complete(f) => {
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
  let last: http.HttpFramed = http.framed_kept(f);
  let parsed: (__Behind, http.HttpFramed) = (Idle, f);
  if (off < c.bufs[at].len()) {
    parsed = __serve_behind(http.parse_request_framed_from(c.bufs[at], off, opts.limits, last), f);
  }
  if (keep) {
    keep = __tail_persists(parsed.0, eof);
  }
  return (__Conns { ...c, offs: c.offs.with(at, off), served: c.served.with(at, served), due: due, queued: queued, backlog: c.backlog.with(at, false), continued: c.continued.with(at, false), last: last, keep: c.keep.with(at, keep) }, keep, parsed.0, parsed.1);
}

function __serve_compact(own c: __Conns, at: i32): own __Conns {
  if (c.lent == at) {
    return __Conns { ...c, bufs: c.bufs.with(at, __bytes_from(c.bufs[at], c.offs[at])), offs: c.offs.with(at, 0), lent: -1 };
  }
  if (c.offs[at] == 0) {
    return c;
  }
  return __Conns { ...c, bufs: c.bufs.with(at, __bytes_from(c.bufs[at], c.offs[at])), offs: c.offs.with(at, 0) };
}

function __pipeline_burst(): i32 {
  return 32;
}

function __serve_next(own c: __Conns, at: i32, opts: Config): own __Conns {
  let now = monotonic_ns();
  let span = opts.keep_alive_idle;
  if (c.bufs[at].len() > c.offs[at]) {
    span = opts.recv_deadline;
  }
  c = __Conns { ...c, deadlines: c.deadlines.with(at, now + span.as_nanos()), due: __sooner(c.due, now + span.as_nanos()), rate_since: c.rate_since.with(at, -1i64) };
  return __body_deadline(c, at, opts, now);
}

function __body_deadline(own c: __Conns, at: i32, opts: Config, now: i64): own __Conns {
  let since = c.rate_since[at];
  let base = c.rate_base[at];
  let buf = c.bufs[at];
  if (since < 0 as i64) {
    if (buf.len() <= c.offs[at] || !http.header_block_complete(buf, c.offs[at], opts.limits)) {
      return c;
    }
    since = now;
    base = buf.len();
  }
  let earned = since + opts.data_rate_grace.as_nanos();
  if (opts.min_data_rate > 0) {
    earned += (buf.len() - base) as i64 * (1000000000 as i64) / (opts.min_data_rate as i64);
  }
  let deadline = c.deadlines[at];
  if (earned > deadline) {
    deadline = earned;
  }
  return __Conns { ...c, deadlines: c.deadlines.with(at, deadline), due: __sooner(c.due, deadline), rate_since: c.rate_since.with(at, since), rate_base: c.rate_base.with(at, base) };
}

function __serve_ready[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config, behind: __Behind, clk: __Clock): (__Conns, boolean) {
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
function __serve_continue[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): __Conns {
  if (c.continued[at]) {
    return __serve_next(c, at, opts);
  }
  c = __Conns { ...c, continued: c.continued.with(at, true), keep: c.keep.with(at, true) };
  buf_push(c.out, "HTTP/1.1 100 Continue\r\n\r\n");
  let (sent_c, sent_how) = __serve_send_out(drv, c, at, opts);
  match (sent_how) {
    Partial => {
      return sent_c;
    },
    Failed => {
      return __serve_close(drv, sent_c, at);
    },
    Whole => {
      return __serve_next(sent_c, at, opts);
    }
  }
  return sent_c;
}

function __serve_refuse[D: async.Driver](drv: D, own c: __Conns, at: i32, status: i32, opts: Config, clk: __Clock): __Conns {
  c = __Conns { ...c, metrics: c.metrics.request("", status, time.DURATION_ZERO) };
  __serve_wire(c.out, "GET", "HTTP/1.1", http.text(status, ""), false, clk);
  let (sent_c, sent_how) = __serve_send_out(drv, __Conns { ...c, keep: c.keep.with(at, false) }, at, opts);
  match (sent_how) {
    Partial => {
      return sent_c;
    },
    Failed => {
      return __serve_close(drv, sent_c, at);
    },
    Whole => {
      return __serve_close(drv, sent_c, at);
    }
  }
  return sent_c;
}

function __serve_respond[D: async.Driver](drv: D, own c: __Conns, at: i32, method: string, version: string, resp: HttpResponse, keep_alive: boolean, opts: Config, answered: i32, behind: __Behind, clk: __Clock): (__Conns, boolean) {
  resp = __with_alt_svc(c, resp);
  c = __serve_count(c, at, method, resp.status);
  c = __stage_background(c, resp);
  let rest = __serve_tail_wire(c, at, method, version, resp, keep_alive, clk);
  c = __Conns { ...c, tails: c.tails.with(at, rest), keep: c.keep.with(at, c.keep[at] && keep_alive && __tail_keeps(rest)) };
  if (__corks(rest, c.keep[at], answered, behind)) {
    return (c, true);
  }
  let sent = __serve_send_out(drv, c, at, opts);
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
  let produced = __serve_produce(drv, c, at, opts);
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
  let ready = __serve_ready(drv, c, at, opts, behind, clk);
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

function __drain_pending[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  if (c.pending[at].len() == 0) {
    return (c, Whole);
  }
  let pending: u8[] = c.pending[at];
  return __serve_send_raw(drv, c, at, pending, opts);
}
```

### `35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...`

```fern
<<35 function __serve_drain[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns,...>>=
function __serve_drain[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config, clk: __Clock): (__Conns, i32) {
  let sent = __drain_pending(drv, c, at, opts);
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
  let produced = __serve_produce(drv, __Conns { ...c, pending: c.pending.with(at, []) }, at, opts);
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
  let parsed = __serve_behind(http.parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last);
  let ready = __serve_ready(drv, __Conns { ...c, pending: c.pending.with(at, []) }, at, opts, parsed.0, clk);
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

function __serve_h2[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, eof: boolean): (__Conns, __Flights) {
  c = __h2_opened(drv, c, at, opts, true);
  let step = __h2_fed(c, at);
  c = __h2_taken(c, at);
  let answered = __h2_more(drv, __h2_answered(drv, __H2Answered { conn: step.conn, conns: c, fls: fls, out: step.output }, step.events, at, handler, opts, stop), at);
  let conn = answered.conn;
  let out = answered.out;
  let flights = answered.fls;
  let closing = conn.closed() || eof;
  c = answered.conns;
  c = __Conns { ...c, framing: c.framing.with(at, Http2(conn)), keep: c.keep.with(at, !closing) };
  return __h2_flushed(drv, c, flights, at, out, opts, closing);
}

function __h2_settled[D: async.Driver](drv: D, own a: __H2Answered, at: i32, opts: Config): (__Conns, __Flights) {
  let conns = a.conns;
  conns = __Conns { ...conns, framing: conns.framing.with(at, Http2(a.conn)) };
  return __h2_flushed(drv, conns, a.fls, at, a.out, opts, a.conn.closed());
}

function __h2_event[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fd: i32, scratch: u8[], handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, clk: __Clock): (__Conns, __Flights) {
  let ev = __serve_event(drv, c, fd, scratch, opts, clk);
  c = ev.0;
  if (ev.1 < 0) {
    return (c, fls);
  }
  let served = __serve_h2(drv, c, fls, ev.1, handler, opts, stop, ev.2);
  if (__flight_at(served.1, fd) < 0) {
    return served;
  }
  let woken = __h2_pulls_woken(drv, served.0, served.1, fd, opts, clk);
  return __h2_after_pulls(drv, woken.0, woken.1, fd, opts);
}

function __h2_pulls_woken[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fd: i32, opts: Config, clk: __Clock): (__Conns, __Flights) {
  let streams: i32[] = [];
  let producers: boolean[] = [];
  for fl in fls.all {
    if (fl.stream > 0 && __flight_pair(fl, fd) >= 0) {
      streams = streams.append(fl.stream);
      producers = producers.append(__is_producer(fl));
    }
  }
  let i = 0;
  while (i < streams.len()) {
    let fi = __flight_on_stream(fls, fd, streams[i], producers[i]);
    if (fi >= 0) {
      let ready = __flight_pair(fls.all[fi], fd);
      let woke = __flight_wake(drv, c, fls, fi, ready, opts, clk);
      c = woke.0;
      fls = woke.1;
    }
    i += 1;
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

function __flight_on_stream(fls: __Flights, fd: i32, stream: i32, producer: boolean): i32 {
  let i: i32 = 0;
  while (i < fls.all.len()) {
    if (fls.all[i].fd == fd && fls.all[i].stream == stream && __is_producer(fls.all[i]) == producer) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}
```

### `38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fd: i32, opts: Config): (__...`

```fern
<<38 function __h2_after_pulls[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fd: i32, opts: Config): (__...>>=
function __h2_after_pulls[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fd: i32, opts: Config): (__Conns, __Flights) {
  let at = __conns_index(c, fd);
  if (at < 0 || c.pulls.len() == 0 && c.producers.len() == 0) {
    return (c, fls);
  }
  match (c.framing[at]) {
    Http2(conn) => {
      let a = __h2_more(drv, __h2_consumed(__H2Answered { conn: conn, conns: c, fls: fls, out: [] }, at), at);
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

function __h2_taken(own c: __Conns, at: i32): own __Conns {
  let lent: i32 = c.lent;
  if (lent == at) {
    lent = 0 - 1;
  }
  return __Conns { ...c, bufs: c.bufs.with(at, []), offs: c.offs.with(at, 0), lent: lent };
}

function __h2_turn_with[D: async.Driver, S](drv: D, own c: __Conns, at: i32, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, stop: __Stop, eof: boolean, mode: i32): (__Conns, S) {
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
  let flushed: (__Conns, __Flights) = __h2_flushed(drv, c, __flights_none(), at, out, opts, conn.closed() || eof);
  return (flushed.0, st);
}

function __h2_step_with[S](c: __Conns, at: i32, conn: h2.Conn, stream: i32, req: HttpRequest, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, stop: __Stop): (__Conns, h2.Conn, S, u8[]) {
  c = __serve_started(c, at);
  let stepped: (S, HttpResponse) = (st, http.text(503, ""));
  if (!__serve_not_ready(req, opts, stop)) {
    c = __with_connection_host(c, at);
    stepped = __serve_step(handler, st, req, c.request_host, opts);
  }
  c = __serve_count(c, at, req.method, stepped.1.status);
  let r: h2.Step = conn.with_date(c.date).respond(stream, __with_alt_svc(c, stepped.1));
  return (c, r.conn, stepped.0, r.output);
}

function __h2_flushed[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, at: i32, out: u8[], opts: Config, closing: boolean): (__Conns, __Flights) {
  if (out.len() > 0) {
    let sent = __serve_send(drv, c, at, out, opts);
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

function __h2_close[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, at: i32): (__Conns, __Flights) {
  let fd = c.fds[at];
  let kept: __Flight[] = [];
  for fl in fls.all {
    if (fl.fd == fd) {
      __flight_cancel(drv, fl);
    } else {
      kept = kept.append(fl);
    }
  }
  return (__serve_close(drv, c, at), __flights_of(kept));
}

function __h2_turn[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop, eof: boolean, mode: i32): (__Conns, __Flights) {
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

function __flight_event[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fd: i32, readiness: i32, scratch: u8[], opts: Config, clk: __Clock, pool: fetch.Idle): (__Conns, __Flights, boolean) {
  if (__upstream_owned(pool, fd)) {
    let upstream = __shared_event(drv, pool, c, fls, fd, readiness, opts);
    return (upstream.0, upstream.1, true);
  }
  let fi = __flight_index(fls, fd);
  if (fi >= 0) {
    let ready = __flight_pair(fls.all[fi], fd);
    let woke = __flight_wake(drv, c, fls, fi, ready, opts, clk);
    let taken = __staged_taken(drv, woke.0, woke.1, opts, pool);
    return (taken.0, taken.1, true);
  }
  let busy = __flight_at(fls, fd);
  if (busy >= 0) {
    let held = __serve_held_event(drv, c, fls, busy, fd, readiness, scratch, opts);
    let taken = __staged_taken(drv, held.0, held.1, opts, pool);
    return (taken.0, taken.1, true);
  }
  return (c, fls, false);
}

function __h2_opened[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config, streamable: boolean): __Conns {
  match (c.framing[at]) {
    Http2(open) => {
      return c;
    },
    _ => {}
  }
  let start = h2.server(__h2_config(opts, streamable && __h2_streaming(opts)));
  c = __Conns { ...c, framing: c.framing.with(at, Http2(start.conn)) };
  return __serve_send(drv, c, at, start.output, opts).0;
}
```

### `40 struct __H2Answered`

```fern
<<40 struct __H2Answered>>=
struct __H2Answered { conn: h2.Conn, conns: __Conns, fls: __Flights, out: u8[] }

struct __H2Body { data: Cell[u8[]], ended: Cell[i32], consumed: Cell[i32], fault: Cell[Option[StreamFault]], done: Cell[boolean] }

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
  let i = 0;
  while (i < c.pulls.len()) {
    if (c.pulls[i].fd == fd && c.pulls[i].stream == stream) {
      return i;
    }
    i += 1;
  }
  return -1;
}

function __producer_at(c: __Conns, fd: i32, stream: i32): i32 {
  let i = 0;
  while (i < c.producers.len()) {
    if (c.producers[i].fd == fd && c.producers[i].stream == stream) {
      return i;
    }
    i += 1;
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
  let i = 0;
  while (i < pulls.len()) {
    if (i != at) {
      out = out.append(pulls[i]);
    }
    i += 1;
  }
  return out;
}

function __producer_removed(own ps: __H2Producer[], at: i32): __H2Producer[] {
  let out: __H2Producer[] = [];
  let i = 0;
  while (i < ps.len()) {
    if (i != at) {
      out = out.append(ps[i]);
    }
    i += 1;
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
    let woke: async.Wake = async.wait_any(set, None);
  }
  return None;
}

function __h2_arrived(own c: __Conns, fd: i32, sid: i32, bytes: u8[]): __Conns {
  let pi: i32 = __pull_at(c, fd, sid);
  if (pi < 0) {
    return c;
  }
  let body: __H2Body = c.pulls[pi].body;
  body.data.set(body.data.get().concat(bytes));
  return c;
}

function __h2_body_ended(own c: __Conns, fd: i32, sid: i32): __Conns {
  let pi: i32 = __pull_at(c, fd, sid);
  if (pi < 0) {
    return c;
  }
  c.pulls[pi].body.ended.set(1);
  return __pull_tidied(c, pi);
}

function __pull_tidied(own c: __Conns, pi: i32): __Conns {
  let body = c.pulls[pi].body;
  if (body.ended.get() != 0 && body.done.get()) {
    return __Conns { ...c, pulls: __pull_removed(c.pulls, pi) };
  }
  return c;
}

function __h2_responded(own a: __H2Answered, at: i32, sid: i32): __H2Answered {
  let pi = __pull_at(a.conns, a.conns.fds[at], sid);
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
function __h2_stream_reset(own c: __Conns, fd: i32, sid: i32): __Conns {
  let pi = __pull_at(c, fd, sid);
  if (pi >= 0) {
    let body = c.pulls[pi].body;
    body.ended.set(2);
    body.fault.set(Some(StreamCutShort));
    c = __Conns { ...c, pulls: __pull_removed(c.pulls, pi) };
  }
  let pr = __producer_at(c, fd, sid);
  if (pr >= 0) {
    c = __Conns { ...c, producers: __producer_removed(c.producers, pr) };
  }
  return c;
}

function __h2_streamed[D: async.Driver](drv: D, own a: __H2Answered, at: i32, sid: i32, req: HttpRequest, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let empty: u8[] = [];
  let no_fault: Option[StreamFault] = None;
  let body = __H2Body { data: cell_new(empty), ended: cell_new(0), consumed: cell_new(0), fault: cell_new(no_fault), done: cell_new(false) };
  let fd = a.conns.fds[at];
  let streamed = HttpRequest { ...req, body: stream.from_source(__h2_body_source(body, fd)) };
  return __h2_request(drv, __H2Answered { ...a, conns: __Conns { ...a.conns, pulls: a.conns.pulls.append(__H2Pull { fd: fd, stream: sid, body: body }) } }, at, sid, streamed, handler, opts, stop);
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

function __h2_respond[D: async.Driver](drv: D, own a: __H2Answered, at: i32, sid: i32, resp: HttpResponse): __H2Answered {
  resp = __with_alt_svc(a.conns, resp);
  let conn = a.conn;
  conn = conn.with_date(a.conns.date);
  let answer = __file_chunks(resp);
  match (answer.body) {
    BodyChunks(producer) => {
      let step = conn.respond_head(sid, answer);
      if (!step.conn.producing(sid)) {
        return __h2_responded(__H2Answered { ...a, conn: step.conn, out: __joined(a.out, step.output) }, at, sid);
      }
      let pr = __H2Producer { fd: a.conns.fds[at], stream: sid, producer: producer, index: 0, trailers: answer.trailers, running: false };
      return __h2_produce(drv, __H2Answered { ...a, conn: step.conn, conns: __Conns { ...a.conns, producers: a.conns.producers.append(pr) }, out: __joined(a.out, step.output) }, at, sid);
    },
    _ => {
      let step = conn.respond(sid, answer);
      return __h2_responded(__H2Answered { ...a, conn: step.conn, out: __joined(a.out, step.output) }, at, sid);
    }
  }
  return a;
}

function __h2_produce[D: async.Driver](drv: D, own a: __H2Answered, at: i32, sid: i32): __H2Answered {
  while (true) {
    let fd = a.conns.fds[at];
    let pi = __producer_at(a.conns, fd, sid);
    if (pi < 0) {
      return a;
    }
    let p = a.conns.producers[pi];
    if (p.running || a.conn.pending(sid) >= __h2_pending_max()) {
      return a;
    }
    let next = p.producer.next;
    let i = p.index;
    let t = async.task_new((): Option[u8[]] => next(i));
    match (async.task_start(t)) {
      Done(chunk) => {
        let stepped = __h2_chunk(a, at, pi, chunk);
        a = stepped.0;
        if (stepped.1) {
          return a;
        }
      },
      Suspended(w) => {
        let running = __H2Producer { ...p, running: true };
        let conns = __Conns { ...a.conns, producers: a.conns.producers.with(pi, running) };
        let none: i32[] = [];
        let fl = __Flight { job: Producer(t), wait: w, fd: fd, set: none, due: -1i64, method: "", version: "", kept: false, behind: Idle, body: None, idle: -1i64, ends: -1i64, stream: sid, since: -1i64, grpc: false, quic: 0 };
        return __H2Answered { ...a, conns: conns, fls: __flights_add(a.fls, __flight_watch(drv, fl)) };
      }
    }
  }
  return a;
}

function __h2_chunk(own a: __H2Answered, at: i32, pi: i32, chunk: Option[u8[]]): (__H2Answered, boolean) {
  let p = a.conns.producers[pi];
  match (chunk) {
    Some(bytes) => {
      let step = a.conn.write(p.stream, bytes);
      let advanced = __H2Producer { ...p, index: p.index + 1, running: false };
      return (__H2Answered { ...a, conn: step.conn, conns: __Conns { ...a.conns, producers: a.conns.producers.with(pi, advanced) }, out: a.out.concat(step.output) }, false);
    },
    None => {
      let step = a.conn.end(p.stream, __trailers_joined(p.trailers, p.producer.trailers()));
      return (__h2_responded(__H2Answered { ...a, conn: step.conn, conns: __Conns { ...a.conns, producers: __producer_removed(a.conns.producers, pi) }, out: a.out.concat(step.output) }, at, p.stream), true);
    }
  }
  return (a, true);
}

function __trailers_joined(a: HeaderMap, b: HeaderMap): HeaderMap {
  let i = 0;
  while (i < b.names.len()) {
    a = a.append(b.names[i], b.values[i]);
    i += 1;
  }
  return a;
}

function __h2_producer_failed(own a: __H2Answered, at: i32, pi: i32): __H2Answered {
  let p: __H2Producer = a.conns.producers[pi];
  let step: h2.Step = a.conn.reset(p.stream, frame.CANCEL);
  return __h2_responded(__H2Answered { ...a, conn: step.conn, conns: __Conns { ...a.conns, producers: __producer_removed(a.conns.producers, pi) }, out: a.out.concat(step.output) }, at, p.stream);
}
```

### `43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fl: __Flight, t: async.Ta...`

```fern
<<43 function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fl: __Flight, t: async.Ta...>>=
function __h2_producer_wake[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fl: __Flight, t: async.Task[Option[u8[]]], wake: async.Wake, opts: Config): (__Conns, __Flights) {
  let at = __conns_index(c, fl.fd);
  let pi = __producer_at(c, fl.fd, fl.stream);
  if (at < 0 || pi < 0) {
    let gone = async.task_resume(t, async.Wake.Cancelled);
    return (c, fls);
  }
  match (c.framing[at]) {
    Http2(conn) => {
      let a = __H2Answered { conn: conn, conns: c, fls: fls, out: [] };
      match (async.task_resume(t, wake)) {
        Done(chunk) => {
          let stepped = __h2_chunk(a, at, pi, chunk);
          a = stepped.0;
          if (!stepped.1) {
            a = __h2_produce(drv, a, at, fl.stream);
          }
          return __h2_settled(drv, a, at, opts);
        },
        Suspended(w) => {
          return (c, __flights_add(fls, __flight_watch(drv, __Flight { ...fl, wait: w })));
        }
      }
    },
    _ => {}
  }
  let dropped = async.task_resume(t, async.Wake.Cancelled);
  return (c, fls);
}

function __h2_consumed(own a: __H2Answered, at: i32): __H2Answered {
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

function __h2_more[D: async.Driver](drv: D, own a: __H2Answered, at: i32): __H2Answered {
  if (a.conns.producers.len() == 0) {
    return a;
  }
  let fd = a.conns.fds[at];
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

function __h2_answered[D: async.Driver](drv: D, own a: __H2Answered, events: h2.Event[], at: i32, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let fd = a.conns.fds[at];
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

function __h2_request[D: async.Driver](drv: D, own a: __H2Answered, at: i32, stream: i32, req: HttpRequest, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): __H2Answered {
  let conns = a.conns;
  conns = __serve_started(conns, at);
  conns = __with_connection_host(conns, at);
  match (__serve_start(handler, conns.request_host, req, "HTTP/2", true, Idle, opts, stop)) {
    HandlerDone(resp) => {
      conns = __serve_count(conns, at, req.method, resp.status);
      return __h2_respond(drv, __H2Answered { ...a, conns: conns }, at, stream, resp);
    },
    HandlerParked(fl) => {
      let parked = __Flight { ...fl, fd: conns.fds[at], stream: stream, since: conns.rate_since[at] };
      return __H2Answered { ...a, conns: conns, fls: __flights_add(a.fls, __flight_watch(drv, parked)) };
    }
  }
  return __H2Answered { ...a, conns: conns };
}

function __h2_answer[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, fl: __Flight, resp: HttpResponse, opts: Config): (__Conns, __Flights) {
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

function __serve_eof[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config, clk: __Clock): __Conns {
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

function __serve_event[D: async.Driver](drv: D, own c: __Conns, fd: i32, scratch: u8[], opts: Config, clk: __Clock): (__Conns, i32, boolean) {
  let at = __conns_index(c, fd);
  if (at < 0) {
    return (c, -1, false);
  }
  if (__in_flight(c, at)) {
    let drained = __serve_drain(drv, c, at, opts, clk);
    if (!__h2_framed(drained.0, fd)) {
      return (drained.0, drained.1, false);
    }
    c = drained.0;
    at = __conns_index(c, fd);
  }
  let had = c.bufs[at].len();
  let backlog = c.backlog[at];
  let read = __serve_read(drv, c, at, scratch, opts);
  if (!read.1 && !backlog && read.0.bufs[at].len() == had) {
    return (read.0, -1, false);
  }
  return (read.0, at, read.1);
}

function __serve_finish[D: async.Driver](drv: D, own c: __Conns, fd: i32, eof: boolean, opts: Config, clk: __Clock): __Conns {
  let at = __conns_index(c, fd);
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
function __serve_send[D: async.Driver](drv: D, own c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns, __Sent) {
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
            tcp_send_bytes(c.socks[at], step.output);
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

function __serve_send_raw[D: async.Driver](drv: D, own c: __Conns, at: i32, payload: u8[], opts: Config): (__Conns, __Sent) {
  match (net.write_rest(c.socks[at], payload)) {
    Ok(rest) => {
      if (rest.len() == 0) {
        return (c, Whole);
      }
      return (__serve_pend(drv, c, at, rest, opts), Partial);
    },
    Err(_) => {
      return (c, Failed);
    }
  }
  return (c, Failed);
}

// The out buffer keeps OUT_HEAD bytes ahead of what is written to it, so a TLS response is sealed as a record in
// the buffer it is taken in: the header goes there, and the content type and the tag go in OUT_TAIL bytes pushed
// behind it.
const OUT_HEAD: string = "\x00\x00\x00\x00\x00";

const OUT_TAIL: string = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00";

function __out_new(): usize {
  let b: usize = buf_new(4096);
  buf_push(b, OUT_HEAD);
  return b;
}

function __out_reset(b: usize): void {
  buf_clear(b);
  buf_push(b, OUT_HEAD);
}

function __serve_send_out[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  match (c.tls[at]) {
    Secured(sess) => {
      buf_push(c.out, OUT_TAIL);
      let framed = buf_take_bytes(c.out);
      buf_push(c.out, OUT_HEAD);
      c = __Conns { ...c, tls: c.tls.with(at, Plain) };
      match (sess.write_framed(framed)) {
        Ok(step) => {
          return __serve_send_raw(drv, __Conns { ...c, tls: c.tls.with(at, Secured(step.session)) }, at, step.output, opts);
        },
        Err(_) => {
          return (c, Failed);
        }
      }
    },
    _ => {}
  }
  let n = tcp_send_buf(c.socks[at], c.out, OUT_HEAD.len());
  if (n >= buf_len(c.out) - OUT_HEAD.len()) {
    __out_reset(c.out);
    return (c, Whole);
  }
  if (n < 0 && n != 0 - __eagain()) {
    __out_reset(c.out);
    return (c, Failed);
  }
  let sent = OUT_HEAD.len();
  if (n > 0) {
    sent += n;
  }
  let rest = __bytes_from(buf_take_bytes(c.out), sent);
  buf_push(c.out, OUT_HEAD);
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
function __serve_pend[D: async.Driver](drv: D, own c: __Conns, at: i32, rest: u8[], opts: Config): __Conns {
  drv.watch(c.fds[at], __pending_interest(c, at));
  let first = c.pending[at].len() == 0;
  return __drain_deadline(__Conns { ...c, pending: c.pending.with(at, rest) }, at, opts, first);
}

function __serve_produce[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): (__Conns, __Sent) {
  if (!__in_flight(c, at)) {
    return (c, Whole);
  }
  if (__tail_parked(c, at)) {
    return (c, Partial);
  }
  let zc = __zc_tail_send(drv, c, at, opts);
  if (zc.2) {
    return (zc.0, zc.1);
  }
  let moved = __file_tail_send(drv, zc.0, at, opts);
  c = moved.0;
  if (moved.2) {
    return (c, moved.1);
  }
  let produced = __tail_piece(drv, c, at);
  c = produced.0;
  if (!produced.2) {
    return (c, Failed);
  }
  if (__tail_parked(c, at)) {
    return (c, Partial);
  }
  let sent = __serve_send(drv, c, at, produced.1, opts);
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
  if (c.zc_bytes <= 0 || method == "HEAD" || http.bodiless(resp.status) || !__zc_conn(c, at)) {
    return None;
  }
  if let BodyBytes(body) = resp.body {
    if (body.len() < c.zc_bytes || !http.fields_ok(resp.headers) || !http.fields_ok(resp.trailers)) {
      return None;
    }
    let date = clk.line;
    if (resp.headers.has("date")) {
      date = "";
    }
    http.serialize_response_head_sized_into(c.out, version, resp, keep_alive, date, body.len());
    return Some(ZcTail(body, 0, -1));
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

function __zc_tail_send[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): (__Conns, __Sent, boolean) {
  if let ZcTail(body, from, slot) = c.tails[at] {
    let (landed, off, copy) = __zc_landed(drv, c, at, from, slot);
    if (off == -115) {
      return (landed, Partial, true);
    }
    if (off >= body.len()) {
      return (__Conns { ...landed, tails: landed.tails.with(at, NoTail) }, Whole, true);
    }
    if (copy) {
      return __zc_copy(drv, landed, at, body, off, opts);
    }
    return __zc_write(drv, landed, at, body, off, opts);
  }
  return (c, Whole, false);
}

function __zc_landed[D: async.Driver](drv: D, own c: __Conns, at: i32, from: i32, slot: i32): (__Conns, i32, boolean) {
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

function __zc_write[D: async.Driver](drv: D, own c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Conns, __Sent, boolean) {
  if (c.zc_bytes > 0) {
    let s = drv.send_zc(c.socks[at], body, off);
    if (s >= 0) {
      drv.watch(c.fds[at], 8);
      c = __drain_deadline(c, at, opts, true);
      return (__Conns { ...c, tails: c.tails.with(at, ZcTail(body, off, s)), zc_held: c.zc_held.append(body), zc_slots: c.zc_slots.append(s), zc_free: c.zc_free.append(false) }, Partial, true);
    }
    if (s == -95) {
      c = __Conns { ...c, zc_bytes: 0 };
    }
  }
  return __zc_copy(drv, c, at, body, off, opts);
}
```

### `48 function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Co...`

```fern
<<48 function __zc_copy[D: async.Driver](drv: D, c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Co...>>=
function __zc_copy[D: async.Driver](drv: D, own c: __Conns, at: i32, body: u8[], off: i32, opts: Config): (__Conns, __Sent, boolean) {
  let sent = __serve_send(drv, __Conns { ...c, tails: c.tails.with(at, NoTail) }, at, __bytes_from(body, off), opts);
  return (sent.0, sent.1, true);
}

function __zc_settled(own c: __Conns, slot: i32): own __Conns {
  let i = 0;
  while (i < c.zc_slots.len()) {
    if (c.zc_slots[i] == slot && !c.zc_free[i]) {
      return __Conns { ...c, zc_free: c.zc_free.with(i, true) };
    }
    i += 1;
  }
  return c;
}

function __zc_reclaim[D: async.Driver](drv: D, own c: __Conns): __Conns {
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

function __file_tail_send[D: async.Driver](drv: D, own c: __Conns, at: i32, opts: Config): (__Conns, __Sent, boolean) {
  match (c.tls[at]) {
    Secured(sess) => {
      return (c, Whole, false);
    },
    _ => {}
  }
  match (c.tails[at]) {
    FileTail(r, left, after) => {
      let want = 65536;
      if (left < want as i64) {
        want = left as i32;
      }
      let n = tcp_sendfile(c.socks[at], r.fd, want);
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
      let rest = left - n as i64;
      if (rest <= 0 as i64) {
        return __tail_range_done(drv, c, at);
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

// Takes the tail out of its slot when its file range is sent, and starts
// what follows the range.
function __tail_range_done[D: async.Driver](drv: D, own c: __Conns, at: i32): (__Conns, __Sent, boolean) {
  let (held, tails) = c.tails.swap_at(at, NoTail);
  let d = __Conns { ...c, tails: tails };
  match (held) {
    FileTail(r, _, after) => {
      return __sent_range(drv, d, at, __after_range(r, after));
    },
    _ => {}
  }
  return (d, Whole, false);
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

function __drain_deadline(own c: __Conns, at: i32, opts: Config, first: boolean): own __Conns {
  let now = monotonic_ns();
  let since = c.send_since[at];
  let base = c.send_base[at];
  let queued = tcp_socket_ctl(c.socks[at], 6, 0);
  if (queued < 0) {
    queued = 0;
  }
  let outstanding = c.pending[at].len() + queued + __tail_left(c, at);
  if (first) {
    since = now;
    base = outstanding;
  }
  let deadline = now + opts.recv_deadline.as_nanos();
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

function __serve_pass_end[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config, stop: __Stop, accepting: boolean, parked: i32): (__Conns, __Stop, boolean, boolean) {
  let now = monotonic_ns();
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

function __serve_stop_step[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config, stop: __Stop, accepting: boolean, now: i64, parked: i32): (__Conns, __Stop, boolean, boolean) {
  if (!__serve_stopping(stop)) {
    return (c, stop, accepting, false);
  }
  if (!stop.closed) {
    if (now < stop.at + opts.shutdown_grace.as_nanos()) {
      return (c, stop, accepting, false);
    }
    if (accepting) {
      drv.unwatch(listener.fd);
    }
    __shut(listener);
    stop = __Stop { ...stop, closed: true };
    accepting = false;
  }
  let past_drain = now >= stop.at + opts.drain_deadline.as_nanos();
  let swept = __stop_sweep(drv, c, stop, past_drain);
  return (swept.0, swept.1, accepting, __stop_done(swept.0, parked, past_drain));
}

function __stop_done(c: __Conns, parked: i32, past_drain: boolean): boolean {
  return c.open == 0 && (parked == 0 || past_drain);
}

function __stop_sweep[D: async.Driver](drv: D, c: __Conns, stop: __Stop, past_drain: boolean): (__Conns, __Stop) {
  let i = 0;
  while (i < c.fds.len()) {
    if (c.fds[i] >= 0) {
      let idle = c.bufs[i].len() == 0 && !__in_flight(c, i) && !c.backlog[i];
      if (idle || past_drain) {
        if (!idle) {
          stop = __Stop { ...stop, aborted: true };
        }
        c = __serve_close(drv, c, i);
      }
    }
    i += 1;
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

function __serve_gate[D: async.Driver](drv: D, listener: Socket, c: __Conns, opts: Config, accepting: boolean, parked: i32): boolean {
  let cap: i32 = opts.max_connections;
  if (cap < 1) {
    cap = 1;
  }
  let full: boolean = c.open >= cap || parked >= opts.max_in_flight;
  if (accepting && full) {
    drv.unwatch(listener.fd);
    return false;
  }
  if (!accepting && !full) {
    return drv.watch(listener.fd, LISTENER_INTEREST) >= 0;
  }
  return accepting;
}

function __serve_expire[D: async.Driver](drv: D, own c: __Conns, opts: Config, now: i64): own __Conns {
  if (c.due < 0 as i64 || c.due > now) {
    return c;
  }
  let due = -1i64;
  let i = 0;
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
    i += 1;
  }
  return __Conns { ...c, due: due };
}
```

### `53 struct __Body`

```fern
<<53 struct __Body>>=
struct __Body { state: Cell[i32], rest: Cell[u8[]], tls: Cell[Option[server.Session]] }

struct __Pull { fd: i32, sock: Socket, left: Cell[i32], dstate: Cell[i32], dheld: Cell[u8[]], dleft: Cell[i32], dbudget: Cell[i32], droom: Cell[i32], pending: Cell[u8[]], since: Cell[i64], got: Cell[i32], continue_owed: Cell[i32], opts: Config, body: __Body, fault: Cell[Option[StreamFault]] }
```

### `54 function __body_source(sock: Socket, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource`

```fern
<<54 function __body_source(sock: Socket, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource>>=
function __body_source(sock: Socket, head: http.HttpHead, initial: u8[], opts: Config, body: __Body): BodySource {
  let no_fault: Option[StreamFault] = None;
  let fault = cell_new(no_fault);
  let owed = 0;
  if (head.expects_continue && initial.len() == 0) {
    owed = 1;
  }
  let empty: u8[] = [];
  let p = __Pull { fd: sock.fd, sock: sock, left: cell_new(head.length), dstate: cell_new(0), dheld: cell_new(empty), dleft: cell_new(0), dbudget: cell_new(opts.limits.header_bytes), droom: cell_new(opts.limits.body), pending: cell_new(initial), since: cell_new(0 as i64), got: cell_new(0), continue_owed: cell_new(owed), opts: opts, body: body, fault: fault };
  return BodySource { next: (): Option[u8[]] => __body_next(p), fault: fault, send: (bytes: u8[]): i32 => __pull_write(p, bytes) };
}

function __pull_write(p: __Pull, bytes: u8[]): i32 {
  let wire = bytes;
  match (p.body.tls.get()) {
    Some(sess) => {
      match (sess.write(bytes)) {
        Ok(step) => {
          p.body.tls.set(Some(step.session));
          wire = step.output;
        },
        Err(_) => {
          return -71;
        }
      }
    },
    None => {}
  }
  let at = 0;
  while (at < wire.len()) {
    let n = tcp_send_bytes(p.sock, __bytes_from(wire, at));
    if (n > 0) {
      at += n;
      continue;
    }
    if (n != 0 - __eagain()) {
      return n;
    }
    let set = [p.fd, 2];
    match (async.wait_any(set, __within_ms(__idle_ms(p.opts)))) {
      Woke(i) => {},
      TimedOut => {
        return -110;
      },
      Cancelled => {
        return -125;
      }
    }
  }
  return bytes.len();
}

// The instant `ms` milliseconds from now on the monotonic clock, which is the clock this loop schedules by.
function __within_ms(ms: i32): Option[Instant] {
  return Some(time.instant_from_nanos(monotonic_ns() + ms as i64 * 1000000i64));
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
    let bytes = __body_take(p);
    if (bytes.len() > 0) {
      let out = __body_decode(p, bytes);
      if (out.len() > 0) {
        return Some(out);
      }
    }
  }
  return None;
}

function __body_take(p: __Pull): u8[] {
  let none: u8[] = [];
  let pending = p.pending.get();
  if (pending.len() > 0) {
    p.pending.set(none);
    return pending;
  }
  while (true) {
    let wait_ms = __body_wait_ms(p);
    if (wait_ms <= 0) {
      return __body_fault(p, StreamTimeout);
    }
    let set = [p.fd, 1];
    match (async.wait_any(set, __within_ms(wait_ms))) {
      Woke(i) => {},
      TimedOut => {
        return __body_fault(p, StreamTimeout);
      },
      Cancelled => {
        return __body_fault(p, StreamCancelled);
      }
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
  let scratch = __alloc_u8(__body_recv_size(p));
  let n = tcp_recv_into(p.sock, scratch);
  if (n > 0) {
    p.got.set(p.got.get() + n);
    let data = __pull_read(p, scratch.take(n));
    if (data.len() > 0 || p.body.state.get() != 0) {
      return Some(data);
    }
    return None;
  }
  if (n != 0 - __eagain()) {
    return Some(__body_fault(p, StreamCutShort));
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
          return __body_fault(p, StreamMalformed);
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
  match (sess.read(raw, clock.now())) {
    Ok(step) => {
      p.body.tls.set(Some(step.session));
      if (step.output.len() > 0) {
        tcp_send_bytes(p.sock, step.output);
      }
      if (step.closed) {
        return None;
      }
      return Some(step.data);
    },
    Err(f) => {
      tcp_send_bytes(p.sock, f.alert);
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
        tcp_send_bytes(p.sock, step.output);
      }
    },
    None => {
      tcp_send_bytes(p.sock, bytes);
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
      return __body_fault(p, __chunk_fault(status));
    }
  }
  return __body_fault(p, StreamMalformed);
}

function __chunk_fault(status: i32): StreamFault {
  if (status == 413) {
    return StreamTooLarge;
  }
  if (status == 431) {
    return StreamFramingTooLarge;
  }
  return StreamMalformed;
}

function __body_fault(p: __Pull, fault: StreamFault): u8[] {
  let none: u8[] = [];
  p.fault.set(Some(fault));
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

function __body_settled[D: async.Driver](drv: D, own c: __Conns, at: i32, body: __Body): __Conns {
  c = __tls_back(__Conns { ...c, framing: c.framing.with(at, Http1) }, at, body);
  let rest = body.rest.get();
  if (rest.len() > 0) {
    c = __Conns { ...c, bufs: c.bufs.with(at, rest.concat(__bytes_from(c.bufs[at], c.offs[at]))), offs: c.offs.with(at, 0) };
  }
  drv.watch(c.fds[at], __read_interest(c, c.tls[at]));
  if (body.state.get() != 1) {
    return __Conns { ...c, keep: c.keep.with(at, false) };
  }
  return c;
}

function __tls_back(own c: __Conns, at: i32, body: __Body): __Conns {
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

function __tls_regained(own c: __Conns, at: i32, fl: __Flight): __Conns {
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

function __serve_unframed[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, at: i32, expecting: boolean, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, count: i32, clk: __Clock, eof: boolean): (__Conns, __Flights, boolean, __Behind, http.HttpFramed) {
  if (!opts.stream_bodies || eof) {
    if (expecting) {
      c = __serve_continue(drv, c, at, opts);
    } else if (!eof) {
      c = __body_deadline(c, at, opts, monotonic_ns());
    }
    let last = c.last;
    return (c, fls, false, Idle, last);
  }
  let last = c.last;
  match (http.parse_request_head_from(c.bufs[at], c.offs[at], opts.limits, last.request)) {
    http.Complete(h) => {
      return __serve_stream(drv, c, fls, at, h, start, opts, stop, count, clk);
    },
    http.Incomplete => {
      return (__body_deadline(c, at, opts, monotonic_ns()), fls, false, Idle, last);
    },
    http.Malformed(status) => {
      return (__serve_refuse(drv, c, at, status, opts, clk), fls, false, Idle, last);
    }
  }
  return (c, fls, false, Idle, last);
}

function __upgrade_head(f: http.HttpFramed): http.HttpHead {
  return http.HttpHead { request: f.request, len: f.len, keep_alive: f.keep_alive, version: f.version, length: 0 - 2, expects_continue: false };
}

function __serve_stream[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, at: i32, h: http.HttpHead, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, count: i32, clk: __Clock): (__Conns, __Flights, boolean, __Behind, http.HttpFramed) {
  let fd = c.fds[at];
  let buf = c.bufs[at];
  let empty: u8[] = [];
  let body = __Body { state: cell_new(0), rest: cell_new(empty), tls: cell_new(None) };
  match (c.tls[at]) {
    Secured(sess) => {
      body.tls.set(Some(sess));
      c = __Conns { ...c, tls: c.tls.with(at, Lent) };
    },
    _ => {}
  }
  let src = __body_source(c.socks[at], h, __bytes_from(buf, c.offs[at] + h.len), opts, body);
  let req = HttpRequest { ...h.request, body: stream.from_source(src) };
  let served = c.served[at] + 1;
  let keep = h.keep_alive && served < opts.keep_alive_requests && !__serve_stopping(stop);
  let due = __backlog_due(c, at);
  let queued = __unqueued(c.queued, c.fds[at], c.backlog[at]);
  c = __Conns { ...c, offs: c.offs.with(at, buf.len()), served: c.served.with(at, served), due: due, queued: queued, backlog: c.backlog.with(at, false), continued: c.continued.with(at, false), keep: c.keep.with(at, keep), framing: c.framing.with(at, Pulling(body)) };
  let f = http.HttpFramed { request: req, len: 0, keep_alive: keep, version: h.version };
  c = __serve_started(c, at);
  c = __with_connection_host(c, at);
  match (start(f, keep, stop, c.request_host)) {
    HandlerDone(resp) => {
      if (__upgrading(resp)) {
        let last = c.last;
        let upgraded = __serve_respond(drv, c, at, h.request.method, h.version, resp, false, opts, count + 1, Idle, clk);
        return (upgraded.0, fls, false, Idle, last);
      }
      c = __body_settled(drv, c, at, body);
      if (h.length == -2) {
        c = __Conns { ...c, keep: c.keep.with(at, keep) };
      }
      let parsed = __serve_behind(http.parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last);
      let kept = c.keep[at];
      let answered = __serve_respond(drv, c, at, h.request.method, h.version, resp, kept, opts, count + 1, parsed.0, clk);
      return (answered.0, fls, answered.1, parsed.0, parsed.1);
    },
    HandlerParked(fl) => {
      let last = c.last;
      let parked = __serve_park(drv, c, fls, at, __flight_watch(drv, __Flight { ...fl, body: Some(body) }), opts);
      return (parked.0, parked.1, false, Idle, last);
    }
  }
  let last = c.last;
  return (c, fls, false, Idle, last);
}
```

### `57 struct __Flight`

```fern
<<57 struct __Flight>>=
struct __Flight { job: __Job, wait: async.Wait, fd: i32, set: i32[], due: i64, method: string, version: string, kept: boolean, behind: __Behind, body: Option[__Body], idle: i64, ends: i64, stream: i32, since: i64, grpc: boolean, quic: i32 }

// keys counts the flights that wait on a key or run on a connection. A spot
// names one such flight, and a lookup trusts it only when the count is 1.
// soonest is never later than the soonest deadline of any flight.
struct __Flights { all: __Flight[], keys: i64[], counts: i32[], spots: i32[], used: i32, soonest: i64 }

const FLIGHT_SHARED: i32 = -2;

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
  let held = __request_host(plat, req, opts);
  plat = held.0;
  let ends = held.1;
  match (async.call_start(handler, req, plat)) {
    Returned(resp) => {
      return HandlerDone(__in_time(resp, ends, time.instant_nanos(plat.instant()), grpc.is_grpc(req)));
    },
    Parked(call, w) => {
      let none: i32[] = [];
      return HandlerParked(__Flight { job: Handler(call), wait: w, fd: 0, set: none, due: -1i64, method: req.method, version: version, kept: kept, behind: behind, body: None, idle: -1i64, ends: ends, stream: 0, since: -1i64, grpc: grpc.is_grpc(req), quic: 0 });
    }
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
  let held = __request_host(plat, req, opts);
  let stepped = handler(st, req, held.0);
  return (stepped.0, __in_time(stepped.1, held.1, time.instant_nanos(held.0.instant()), grpc.is_grpc(req)));
}

function __serve_started(own c: __Conns, at: i32): own __Conns {
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
  let ends: i64 = __request_ends(req, opts, time.instant_nanos(plat.instant()));
  if (ends >= 0 as i64) {
    plat = plat.with_deadline(time.instant_from_nanos(ends));
  }
  if (opts.trace) {
    let host: platform.Host = plat;
    plat = plat.with_trace(trace.continue_from(req.headers, (): i32 => host.random_i32()));
  }
  return (plat, ends);
}

function __serve_park[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, at: i32, fl: __Flight, opts: Config): (__Conns, __Flights) {
  let fd = c.fds[at];
  if (buf_len(c.out) > OUT_HEAD.len()) {
    let sent = __serve_send_out(drv, c, at, opts);
    c = sent.0;
    match (sent.1) {
      Failed => {
        __flight_cancel(drv, fl);
        return (__serve_close(drv, c, at), fls);
      },
      _ => {}
    }
  }
  return (__Conns { ...c, deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()) }, __flights_add(fls, __Flight { ...fl, fd: fd, since: c.rate_since[at] }));
}

function __held_deadline(): i64 {
  let g: i64 = 1000000000 as i64;
  return g * g * 9;
}
```

### `59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fi: i32, fd: i32, readine...`

```fern
<<59 function __serve_held_event[D: async.Driver](drv: D, c: __Conns, fls: __Flights, fi: i32, fd: i32, readine...>>=
function __serve_held_event[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, fi: i32, fd: i32, readiness: i32, scratch: u8[], opts: Config): (__Conns, __Flights) {
  let at = __conns_index(c, fd);
  if (at < 0) {
    return (c, fls);
  }
  if ((readiness & 4) != 0) {
    __flight_cancel(drv, fls.all[fi]);
    return (__serve_close(drv, c, at), __flights_remove(fls, fi));
  }
  if (__body_live(fls.all[fi])) {
    drv.unwatch(fd);
    return (c, fls);
  }
  let read = __serve_read(drv, __tls_regained(c, at, fls.all[fi]), at, scratch, opts);
  c = __serve_compact(read.0, at);
  if (read.1) {
    c = __serve_parked_eof(c, fd);
  }
  return (__Conns { ...c, deadlines: c.deadlines.with(at, __held_deadline()), due: __sooner(c.due, __held_deadline()) }, fls);
}

function __serve_settle[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, fd: i32, eof: boolean, opts: Config, clk: __Clock): __Conns {
  if (eof && __flight_at(fls, fd) >= 0) {
    return __serve_parked_eof(__serve_finish(drv, c, fd, false, opts, clk), fd);
  }
  return __serve_finish(drv, c, fd, eof, opts, clk);
}

function __serve_parked_eof(own c: __Conns, fd: i32): __Conns {
  let at = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  return __Conns { ...c, keep: c.keep.with(at, false) };
}

function __flight_wake[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fi: i32, ready: i32, opts: Config, clk: __Clock): (__Conns, __Flights) {
  let fl = fls.all[fi];
  __flight_unwatch(drv, fl, ready);
  fls = __flights_remove(fls, fi);
  let wake = async.Wake.TimedOut;
  if (ready >= 0) {
    wake = Woke(ready);
  }
  match (fl.job) {
    Handler(call) => {
      return __handler_wake(drv, c, fls, fl, call, wake, opts, clk);
    },
    Producer(t) => {
      if (fl.quic > 0) {
        return __h3_producer_wake(drv, c, fls, fl, t, wake);
      }
      if (fl.stream > 0) {
        return __h2_producer_wake(drv, c, fls, fl, t, wake, opts);
      }
      if (__conns_index(c, fl.fd) < 0) {
        let gone = async.task_resume(t, async.Wake.Cancelled);
        return (c, fls);
      }
      match (async.task_resume(t, wake)) {
        Done(chunk) => {
          return (__producer_done(drv, c, fl.fd, chunk, opts, clk), fls);
        },
        Suspended(w) => {
          return (c, __flights_add(fls, __flight_watch(drv, __Flight { ...fl, wait: w })));
        }
      }
    },
    Background(t) => {
      return (c, __background_wake(drv, fls, fl, t, wake));
    }
  }
  return (c, fls);
}

function __handler_wake[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fl: __Flight, call: async.Call[HttpRequest, platform.Host, HttpResponse], wake: async.Wake, opts: Config, clk: __Clock): (__Conns, __Flights) {
  match (async.call_resume(call, wake)) {
    Done(resp) => {
      return __serve_answer(drv, c, fls, fl, __in_time(resp, fl.ends, monotonic_ns(), fl.grpc), opts, clk);
    },
    Suspended(w) => {
      return (c, __flights_add(fls, __flight_watch(drv, __Flight { ...fl, wait: w })));
    }
  }
  return (c, fls);
}

function __background_wake[D: async.Driver](drv: D, fls: __Flights, fl: __Flight, t: async.Task[i32], wake: async.Wake): __Flights {
  match (async.task_resume(t, wake)) {
    Suspended(w) => {
      return __flights_add(fls, __flight_watch(drv, __Flight { ...fl, wait: w }));
    },
    Done(v) => {}
  }
  return fls;
}

function __serve_answer[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, fl: __Flight, resp: HttpResponse, opts: Config, clk: __Clock): (__Conns, __Flights) {
  if (fl.quic > 0) {
    return __h3_answer(drv, c, fls, fl, resp);
  }
  let at = __conns_index(c, fl.fd);
  if (at < 0) {
    return (c, fls);
  }
  if (fl.stream > 0) {
    return __h2_answer(drv, c, fls, at, fl, resp, opts);
  }
  let behind = fl.behind;
  match (fl.body) {
    Some(b) => {
      if (__upgrading(resp)) {
        return (__serve_respond(drv, c, at, fl.method, fl.version, resp, false, opts, __pipeline_burst(), Idle, clk).0, fls);
      }
      c = __body_settled(drv, c, at, b);
      behind = __serve_behind(http.parse_request_framed_from(c.bufs[at], c.offs[at], opts.limits, c.last), c.last).0;
    },
    None => {}
  }
  let kept = fl.kept && c.keep[at];
  let answered = __serve_respond(drv, c, at, fl.method, fl.version, resp, kept, opts, __pipeline_burst(), behind, clk);
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

function __serve_abandon[D: async.Driver](drv: D, own c: __Conns, fd: i32): __Conns {
  let at: i32 = __conns_index(c, fd);
  if (at < 0) {
    return c;
  }
  return __serve_close(drv, c, at);
}

function __flight_watch[D: async.Driver](drv: D, fl: __Flight): __Flight {
  let w = fl.wait;
  let once = 0;
  if (drv.oneshot()) {
    once = async.ONESHOT;
  }
  let keeps = drv.keeps();
  let i = 0;
  while (i + 1 < w.set.len()) {
    let interest = w.set[i + 1];
    if (!__own_connection(fl, w.set[i]) && interest != fetch.MAIL_INTEREST) {
      if (!__kept_pair(keeps, interest)) {
        let watched = drv.watch(w.set[i], async.interest_bits(interest) | once);
      } else if ((interest & async.WATCHED) == 0) {
        let kept = drv.watch(w.set[i], async.interest_bits(interest));
      }
    }
    i += 2;
  }
  let due = -1i64;
  if let Some(at) = w.until {
    due = time.instant_nanos(at);
  }
  return __Flight { ...fl, set: w.set, due: due };
}

function __flight_unwatch[D: async.Driver](drv: D, fl: __Flight, fired: i32): void {
  let spent = -1;
  if (drv.oneshot()) {
    spent = fired;
  }
  let keeps = drv.keeps();
  let i = 0;
  while (i + 1 < fl.set.len()) {
    if (!__own_connection(fl, fl.set[i]) && fl.set[i + 1] != fetch.MAIL_INTEREST && i / 2 != spent && !__kept_pair(keeps, fl.set[i + 1])) {
      let unwatched = drv.unwatch(fl.set[i]);
    }
    i += 2;
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
  __flight_unwatch(drv, fl, -1);
  match (fl.job) {
    Handler(call) => {
      let st = async.call_resume(call, async.Wake.Cancelled);
    },
    Producer(t) => {
      let st = async.task_resume(t, async.Wake.Cancelled);
    },
    Background(t) => {
      let st = async.task_resume(t, async.Wake.Cancelled);
    }
  }
}

function __flights_cancel[D: async.Driver](drv: D, fls: __Flights): void {
  for fl in fls.all {
    __flight_cancel(drv, fl);
  }
}

function __flight_index(fls: __Flights, fd: i32): i32 {
  let at = __flights_find(fls, __flight_key(0, fd));
  if (at != FLIGHT_SHARED) {
    return at;
  }
  for i in 0..fls.all.len() {
    if (__flight_pair(fls.all[i], fd) >= 0) {
      return i;
    }
  }
  return -1;
}
```

### `61 function __flight_pair(fl: __Flight, fd: i32): i32`

```fern
<<61 function __flight_pair(fl: __Flight, fd: i32): i32>>=
function __flight_pair(fl: __Flight, fd: i32): i32 {
  let i = 0;
  while (i + 1 < fl.set.len()) {
    if (fl.set[i] == fd) {
      return i / 2;
    }
    i += 2;
  }
  return -1;
}

function __flight_at(fls: __Flights, fd: i32): i32 {
  let at = __flights_find(fls, __flight_key(1, fd));
  if (at != FLIGHT_SHARED) {
    return at;
  }
  for i in 0..fls.all.len() {
    if (fls.all[i].fd == fd) {
      return i;
    }
  }
  return -1;
}

function __flights_none(): __Flights {
  return __Flights { all: [], keys: [], counts: [], spots: [], used: 0, soonest: 0 - 1 as i64 };
}

function __flights_of(list: __Flight[]): __Flights {
  let fls: __Flights = __flights_none();
  for fl in list {
    fls = __flights_add(fls, fl);
  }
  return fls;
}

function __flights_add(fls: __Flights, fl: __Flight): __Flights {
  fls = __flights_room(fls, __flight_keys(fl));
  fls = __flights_count_all(fls, fl, fls.all.len());
  return __Flights { ...fls, all: fls.all.append(fl), soonest: __flight_soonest(fls.soonest, fl) };
}

function __flights_idle(fls: __Flights, at: i32, idle: i64): __Flights {
  let fl = __Flight { ...fls.all[at], idle: idle };
  return __Flights { ...fls, all: fls.all.with(at, fl), soonest: __flight_soonest(fls.soonest, fl) };
}

function __flights_remove(fls: __Flights, at: i32): __Flights {
  let last = fls.all.len() - 1;
  fls = __flights_uncount_all(fls, fls.all[at], at);
  if (at != last) {
    let moved = fls.all[last];
    fls = __flights_respot(fls, moved, last, at);
    fls = __Flights { ...fls, all: fls.all.with(at, moved) };
  }
  return __Flights { ...fls, all: fls.all.truncate(last) };
}

function __flight_key(kind: i64, k: i32): i64 {
  return kind << 32 | k as i64 & 4294967295;
}

function __flight_keys(fl: __Flight): i32 {
  return fl.set.len() / 2 + 1;
}

function __key_home(key: i64, mask: i32): i32 {
  let h: u64 = key as u64 * 0x9E3779B97F4A7C15;
  return (h >> 40) as i32 & mask;
}

function __key_slot(keys: i64[], key: i64): i32 {
  let mask: i32 = keys.len() - 1;
  if (mask < 0) {
    return -1;
  }
  let i: i32 = __key_home(key, mask);
  while (keys[i] >= 0) {
    if (keys[i] == key) {
      return i;
    }
    i = i + 1 & mask;
  }
  return -1;
}

function __flights_find(fls: __Flights, key: i64): i32 {
  let s = __key_slot(fls.keys, key);
  if (s < 0) {
    return -1;
  }
  if (fls.counts[s] == 1 && fls.spots[s] >= 0) {
    return fls.spots[s];
  }
  return FLIGHT_SHARED;
}

function __flights_room(fls: __Flights, extra: i32): __Flights {
  let need = (fls.used + extra) * 2;
  if (need <= fls.keys.len()) {
    return fls;
  }
  let cap = 16;
  while (cap < need * 2) {
    cap *= 2;
  }
  let keys: i64[] = [];
  let counts: i32[] = [];
  let spots: i32[] = [];
  while (keys.len() < cap) {
    keys = keys.append(-1);
    counts = counts.append(0);
    spots = spots.append(0);
  }
  let all = fls.all;
  let none: __Flight[] = [];
  let out = __Flights { all: none, keys: keys, counts: counts, spots: spots, used: 0, soonest: fls.soonest };
  for p in 0..all.len() {
    out = __flights_count_all(out, all[p], p);
  }
  return __Flights { ...out, all: all };
}

function __flights_count_all(fls: __Flights, fl: __Flight, at: i32): __Flights {
  let i = 0;
  while (i + 1 < fl.set.len()) {
    fls = __flights_count(fls, __flight_key(0, fl.set[i]), at);
    i += 2;
  }
  return __flights_count(fls, __flight_key(1, fl.fd), at);
}

function __flights_count(fls: __Flights, key: i64, at: i32): __Flights {
  let mask = fls.keys.len() - 1;
  let i = __key_home(key, mask);
  while (fls.keys[i] >= 0) {
    if (fls.keys[i] == key) {
      return __Flights { ...fls, counts: fls.counts.with(i, fls.counts[i] + 1), spots: fls.spots.with(i, at) };
    }
    i = i + 1 & mask;
  }
  return __Flights { ...fls, keys: fls.keys.with(i, key), counts: fls.counts.with(i, 1), spots: fls.spots.with(i, at), used: fls.used + 1 };
}

function __flights_uncount_all(fls: __Flights, fl: __Flight, at: i32): __Flights {
  let i = 0;
  while (i + 1 < fl.set.len()) {
    fls = __flights_uncount(fls, __flight_key(0, fl.set[i]), at);
    i += 2;
  }
  return __flights_uncount(fls, __flight_key(1, fl.fd), at);
}

function __flights_uncount(fls: __Flights, key: i64, at: i32): __Flights {
  let s = __key_slot(fls.keys, key);
  if (fls.counts[s] > 1) {
    let spot = fls.spots[s];
    if (spot == at) {
      spot = -1;
    }
    return __Flights { ...fls, counts: fls.counts.with(s, fls.counts[s] - 1), spots: fls.spots.with(s, spot) };
  }
  return __flights_unkey(fls, s);
}

function __flights_unkey(fls: __Flights, slot: i32): __Flights {
  let mask: i32 = fls.keys.len() - 1;
  let hole: i32 = slot;
  let j: i32 = slot + 1 & mask;
  while (fls.keys[j] >= 0) {
    let home: i32 = __key_home(fls.keys[j], mask);
    if ((j - home & mask) >= (j - hole & mask)) {
      fls = __Flights { ...fls, keys: fls.keys.with(hole, fls.keys[j]), counts: fls.counts.with(hole, fls.counts[j]), spots: fls.spots.with(hole, fls.spots[j]) };
      hole = j;
    }
    j = j + 1 & mask;
  }
  return __Flights { ...fls, keys: fls.keys.with(hole, 0 - 1), counts: fls.counts.with(hole, 0), used: fls.used - 1 };
}

function __flights_respot(fls: __Flights, fl: __Flight, from: i32, to: i32): __Flights {
  let i: i32 = 0;
  while (i + 1 < fl.set.len()) {
    fls = __flights_move_spot(fls, __flight_key(0, fl.set[i]), from, to);
    i = i + 2;
  }
  return __flights_move_spot(fls, __flight_key(1, fl.fd), from, to);
}

function __flights_move_spot(fls: __Flights, key: i64, from: i32, to: i32): __Flights {
  let s = __key_slot(fls.keys, key);
  if (fls.spots[s] != from) {
    return fls;
  }
  return __Flights { ...fls, spots: fls.spots.with(s, to) };
}

function __flights_timeout(fls: __Flights, now: i64): i32 {
  let best: i64 = fls.soonest;
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

function __flight_soonest(best: i64, fl: __Flight): i64 {
  return __sooner(__sooner(__sooner(best, fl.due), fl.idle), fl.ends);
}

function __flights_resoon(fls: __Flights): __Flights {
  let best: i64 = 0 - 1 as i64;
  for fl in fls.all {
    best = __flight_soonest(best, fl);
  }
  return __Flights { ...fls, soonest: best };
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

function __flights_expire[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, opts: Config, clk: __Clock): (__Conns, __Flights) {
  if (fls.soonest < 0 as i64 || fls.soonest > monotonic_ns()) {
    return (c, fls);
  }
  let again = fls.all.len() > 0;
  while (again) {
    again = false;
    let now = monotonic_ns();
    let i = 0;
    while (i < fls.all.len() && !again) {
      if (fls.all[i].ends >= 0 as i64 && fls.all[i].ends <= now) {
        let late = __flight_overdue(drv, c, fls, i, opts, clk);
        c = late.0;
        fls = late.1;
        again = true;
      } else if (fls.all[i].due >= 0 as i64 && fls.all[i].due <= now) {
        let woke = __flight_wake(drv, c, fls, i, -1, opts, clk);
        c = woke.0;
        fls = woke.1;
        again = true;
      } else if (fls.all[i].idle >= 0 as i64 && fls.all[i].idle <= now) {
        let filled = __flight_filler(drv, c, fls, i, opts);
        c = filled.0;
        fls = filled.1;
        again = true;
      }
      i += 1;
    }
  }
  return (c, __flights_resoon(fls));
}

function __flight_overdue[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, i: i32, opts: Config, clk: __Clock): (__Conns, __Flights) {
  let late: __Flight = fls.all[i];
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

### `62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flights, fd: i32, re...`

```fern
<<62 function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, c: __Conns, fls: __Flights, fd: i32, re...>>=
function __shared_event[D: async.Driver](drv: D, pool: fetch.Idle, own c: __Conns, own fls: __Flights, fd: i32, readiness: i32, opts: Config): (__Conns, __Flights) {
  let s: fetch.Shared = fetch.shared_of(pool);
  match (s.event(fd, (readiness & 1) != 0)) {
    Some(dead) => {
      let unwatched: i32 = drv.unwatch(dead);
      s.close(dead);
    },
    None => {}
  }
  return __shared_woken(drv, s, c, fls, opts);
}

function __clock_none(): __Clock {
  return __Clock { sec: 0 as i64, date: "", line: "" };
}

function __shared_woken[D: async.Driver](drv: D, s: fetch.Shared, own c: __Conns, own fls: __Flights, opts: Config): (__Conns, __Flights) {
  for id in s.answered() {
    let fi = __flight_index(fls, id);
    if (fi >= 0) {
      let ready = __flight_pair(fls.all[fi], id);
      let woke = __flight_wake(drv, c, fls, fi, ready, opts, __clock_none());
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

function __shared_turn[D: async.Driver](drv: D, pool: fetch.Idle, own c: __Conns, own fls: __Flights, opts: Config): (__Conns, __Flights) {
  let s: fetch.Shared = fetch.shared_of(pool);
  let idle: Duration = pool.idle;
  let woken: (__Conns, __Flights) = __shared_woken(drv, s, c, fls, opts);
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
  for fd in s.expired(clock.instant(), idle) {
    let unwatched: i32 = drv.unwatch(fd);
    s.close(fd);
  }
  return (c, fls);
}

function __shared_close[D: async.Driver](drv: D, pool: fetch.Idle): void {
  match (pool.shared) {
    Some(s) => {
      for fd in s.sockets() {
        let unwatched: i32 = drv.unwatch(fd);
        s.close(fd);
      }
    },
    None => {}
  }
}

function __init_platform(): platform.Host {
  return platform.host();
}
```

### `63 function __serve_loop(l: __Listen, handler: (HttpRequest, platform.Host)`

```fern
<<63 function __serve_loop(l: __Listen, handler: (HttpRequest, platform.Host)>>=
function __serve_loop(l: __Listen, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, on_stop: (string) => void, snap: Option[Writer]): Result[i32, ListenError] {
  let listener = l.sock;
  let drv = __loop_driver(opts);
  let watched = drv.watch(listener.fd, LISTENER_INTEREST);
  if (watched < 0) {
    drv.close();
    __listen_release(l);
    return Ok(watched);
  }
  let ex = __loop_exporter(drv, l, opts, snap)?;
  let accepting = true;
  let stop = __serve_stop_sources(drv, opts);
  let plat = __host_platform(drv.handle(), opts, true);
  let host_for = (peer: Option[net.SocketAddr]): platform.Host => plat.with_peer(peer);
  let conns = __Conns { open: 0, free: -1, fds: [], socks: [], slots: [], bufs: [], offs: [], pending: [], deadlines: [], due: -1i64, keep: [], served: [], backlog: [], queued: [], continued: [], rate_since: [], rate_base: [], send_since: [], send_base: [], peers: [], request_host: plat, addresses: [], tails: [], tls: [], framing: [], last: http.framed_none(), out: __out_new(), lent: -1, parked: [], later: [], metrics: metrics.server_metrics(), pulls: [], producers: [], tls_reads: __tls_reads(opts), zc_bytes: opts.io_uring_zc_bytes, zc_held: [], zc_slots: [], zc_free: [], quic: __quic_none(), date: "", peer_counts: __PeerCounts { keys: [], counts: [], used: 0 } };
  let flights = __flights_none();
  let scratch = __recv_scratch();
  let clk = __Clock { sec: 0 as i64, date: "", line: "" };
  let events = async.event_room(64);
  let start = (f: http.HttpFramed, kept: boolean, st: __Stop, host: platform.Host): __Started => __started_again(__serve_start(handler, host, f.request, f.version, kept, Idle, opts, st));
  conns = __Conns { ...conns, quic: __quic_open(drv, l, opts) };
  while (true) {
    conns = __h3_drain(conns);
    let now = monotonic_ns();
    events = __with_backlog(conns, drv.wait_into(events, time.millis_timeout(__snapshot_timeout(snap, __stop_timeout(stop, opts, now, __least_timeout(__least_timeout(__conns_timeout(conns, now), __flights_timeout(flights, now)), __quic_timeout(conns.quic, now)))))));
    let ends = async.ready_pairs(events) * 2;
    conns = __zc_reclaim(drv, conns);
    let second = clk.sec;
    clk = __clock_tick(clk);
    if (clk.sec != second) {
      conns = __Conns { ...conns, date: clk.date };
      __snapshot_send(snap, conns.metrics, opts);
    }
    let k = 0;
    while (k < ends) {
      let fd = events[k];
      let readiness = events[k + 1];
      k += 2;
      let sig = __stop_signal(fd);
      if (sig != 0) {
        stop = __stop_begin(stop, sig);
        continue;
      }
      if (__exporter_owns(ex, fd)) {
        ex = __scrape_wake(drv, ex, fd, __exposition_of(conns.metrics, opts), !__serve_stopping(stop));
        continue;
      }
      if (fd == listener.fd) {
        conns = __serve_accept(drv, listener, conns, opts);
        continue;
      }
      if (__quic_owns(conns, fd)) {
        let datagrams = __h3_event(drv, conns, flights, opts, stop, host_for);
        let started = __h3_start_all(drv, datagrams.0, datagrams.1, datagrams.2, handler, opts, stop);
        conns = started.0;
        flights = started.1;
        continue;
      }
      if (__h2_framed(conns, fd)) {
        let turned = __h2_event(drv, conns, flights, fd, scratch, handler, opts, stop, clk);
        conns = turned.0;
        flights = turned.1;
        continue;
      }
      let flown = __flight_event(drv, conns, flights, fd, readiness, scratch, opts, clk, plat.pool);
      conns = flown.0;
      flights = flown.1;
      if (flown.2) {
        continue;
      }
      let ev = __serve_event(drv, conns, fd, scratch, opts, clk);
      conns = ev.0;
      let at = ev.1;
      if (at < 0) {
        __orphan_event(drv, plat.pool, fd);
        continue;
      }
      let mode = __h2_mode(conns, at, opts);
      if (mode != H2_NOT) {
        let served = __h2_turn(drv, conns, flights, at, handler, opts, stop, ev.2, mode);
        conns = served.0;
        flights = served.1;
        continue;
      }
      let burst = __serve_burst(drv, conns, flights, at, ev.2, handler, start, opts, stop, clk);
      conns = burst.0;
      flights = burst.1;
      conns = __serve_settle(drv, conns, flights, fd, ev.2, opts, clk);
      let taken = __staged_taken(drv, conns, flights, opts, plat.pool);
      conns = taken.0;
      flights = taken.1;
    }
    let due = __flights_expire(drv, conns, flights, opts, clk);
    conns = due.0;
    flights = due.1;
    let woken = __staged_taken(drv, conns, flights, opts, plat.pool);
    conns = woken.0;
    flights = woken.1;
    if (__quic_on(conns)) {
      let ticked = __h3_pass(drv, conns, flights, opts, stop);
      let started_late = __h3_start_all(drv, ticked.0, ticked.1, ticked.2, handler, opts, stop);
      conns = started_late.0;
      flights = started_late.1;
    }
    let ended = __serve_pass_end(drv, listener, conns, opts, stop, accepting, flights.all.len());
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
      return Ok(__stop_code(stop));
    }
  }
  if (accepting) {
    drv.unwatch(listener.fd);
  }
  __quic_close(drv, conns.quic);
  __shared_close(drv, plat.pool);
  __exporter_close(drv, ex);
  drv.close();
  buf_free(conns.out);
  return Ok(0);
}
```

### `64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, eof: boolean,...`

```fern
<<64 function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, eof: boolean,...>>=
function __serve_burst[D: async.Driver](drv: D, own c: __Conns, own fls: __Flights, at: i32, eof: boolean, handler: (HttpRequest, platform.Host) => HttpResponse, start: (http.HttpFramed, boolean, __Stop, platform.Host) => __Started, opts: Config, stop: __Stop, clk: __Clock): (__Conns, __Flights) {
  let conns = c;
  let flights = fls;
  let more = true;
  let count = 0;
  let parsed = __serve_behind(http.parse_request_framed_from(conns.bufs[at], conns.offs[at], opts.limits, conns.last), conns.last);
  let behind = parsed.0;
  let framed = parsed.1;
  while (more) {
    let next = Idle;
    let next_framed = framed;
    match (behind) {
      Pipelined => {
        let f = framed;
        if (__upgrade_request(f.request)) {
          let upgraded = __serve_stream(drv, conns, flights, at, __upgrade_head(f), start, opts, stop, count, clk);
          conns = upgraded.0;
          flights = upgraded.1;
          more = false;
          continue;
        }
        let took = __serve_took(conns, at, f, opts, eof, stop);
        conns = __serve_started(took.0, at);
        count += 1;
        conns = __with_connection_host(conns, at);
        match (__serve_start(handler, conns.request_host, f.request, f.version, took.1, took.2, opts, stop)) {
          HandlerDone(resp) => {
            let answered = __serve_respond(drv, conns, at, f.request.method, f.version, resp, took.1, opts, count, took.2, clk);
            conns = answered.0;
            more = answered.1;
            next = took.2;
            next_framed = took.3;
          },
          HandlerParked(fl) => {
            let parked = __serve_park(drv, conns, flights, at, __flight_watch(drv, fl), opts);
            conns = parked.0;
            flights = parked.1;
            more = false;
          }
        }
      },
      Idle | Expecting => {
        let unframed = __serve_unframed(drv, conns, flights, at, __expecting(behind), start, opts, stop, count, clk, eof);
        conns = unframed.0;
        flights = unframed.1;
        more = unframed.2;
        next = unframed.3;
        next_framed = unframed.4;
        if (more) {
          count += 1;
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

### `65 function __serve_loop_with[S](l: __Listen, init: S, handler: (S, HttpRequest, platform.Host)`

```fern
<<65 function __serve_loop_with[S](l: __Listen, init: S, handler: (S, HttpRequest, platform.Host)>>=
function __serve_loop_with[S](l: __Listen, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), opts: Config, on_stop: (string, S) => void, snap: Option[Writer]): Result[i32, ListenError] {
  let listener = l.sock;
  let st = init;
  let drv = __loop_driver(opts);
  let watched = drv.watch(listener.fd, LISTENER_INTEREST);
  if (watched < 0) {
    drv.close();
    __listen_release(l);
    return Ok(watched);
  }
  let ex = __loop_exporter(drv, l, opts, snap)?;
  let accepting = true;
  let stop = __serve_stop_sources(drv, opts);
  let plat = __host_platform(drv.handle(), opts, false);
  let host_for = (peer: Option[net.SocketAddr]): platform.Host => plat.with_peer(peer);
  let conns = __Conns { open: 0, free: -1, fds: [], socks: [], slots: [], bufs: [], offs: [], pending: [], deadlines: [], due: -1i64, keep: [], served: [], backlog: [], queued: [], continued: [], rate_since: [], rate_base: [], send_since: [], send_base: [], peers: [], request_host: plat, addresses: [], tails: [], tls: [], framing: [], last: http.framed_none(), out: __out_new(), lent: -1, parked: [], later: [], metrics: metrics.server_metrics(), pulls: [], producers: [], tls_reads: __tls_reads(opts), zc_bytes: opts.io_uring_zc_bytes, zc_held: [], zc_slots: [], zc_free: [], quic: __quic_none(), date: "", peer_counts: __PeerCounts { keys: [], counts: [], used: 0 } };
  let flights = __flights_none();
  let scratch = __recv_scratch();
  let clk = __Clock { sec: 0 as i64, date: "", line: "" };
  let events = async.event_room(64);
  conns = __Conns { ...conns, quic: __quic_open(drv, l, opts) };
  while (true) {
    conns = __h3_drain(conns);
    let now = monotonic_ns();
    events = __with_backlog(conns, drv.wait_into(events, time.millis_timeout(__snapshot_timeout(snap, __stop_timeout(stop, opts, now, __least_timeout(__least_timeout(__conns_timeout(conns, now), __flights_timeout(flights, now)), __quic_timeout(conns.quic, now)))))));
    let ends = async.ready_pairs(events) * 2;
    conns = __zc_reclaim(drv, conns);
    let second = clk.sec;
    clk = __clock_tick(clk);
    if (clk.sec != second) {
      conns = __Conns { ...conns, date: clk.date };
      __snapshot_send(snap, conns.metrics, opts);
    }
    let k = 0;
    while (k < ends) {
      let fd = events[k];
      let readiness = events[k + 1];
      k += 2;
      let sig = __stop_signal(fd);
      if (sig != 0) {
        stop = __stop_begin(stop, sig);
        continue;
      }
      if (__exporter_owns(ex, fd)) {
        ex = __scrape_wake(drv, ex, fd, __exposition_of(conns.metrics, opts), !__serve_stopping(stop));
        continue;
      }
      if (fd == listener.fd) {
        conns = __serve_accept(drv, listener, conns, opts);
        continue;
      }
      if (__quic_owns(conns, fd)) {
        let datagrams = __h3_event(drv, conns, flights, opts, stop, host_for);
        let h3_served = __h3_start_all_with(drv, datagrams.0, datagrams.1, datagrams.2, st, handler);
        conns = h3_served.0;
        flights = h3_served.1;
        st = h3_served.2;
        continue;
      }
      let flown = __flight_event(drv, conns, flights, fd, readiness, scratch, opts, clk, plat.pool);
      conns = flown.0;
      flights = flown.1;
      if (flown.2) {
        continue;
      }
      let ev = __serve_event(drv, conns, fd, scratch, opts, clk);
      conns = ev.0;
      let at = ev.1;
      if (at < 0) {
        __orphan_event(drv, plat.pool, fd);
        continue;
      }
      let mode = __h2_mode(conns, at, opts);
      if (mode != H2_NOT) {
        let served = __h2_turn_with(drv, conns, at, st, handler, opts, stop, ev.2, mode);
        conns = served.0;
        st = served.1;
        continue;
      }
      let more = true;
      let count = 0;
      let parsed = __serve_behind(http.parse_request_framed_from(conns.bufs[at], conns.offs[at], opts.limits, conns.last), conns.last);
      let behind = parsed.0;
      let framed = parsed.1;
      while (more) {
        let next = Idle;
        let next_framed = framed;
        match (behind) {
          Pipelined => {
            let f = framed;
            let took = __serve_took(conns, at, f, opts, ev.2, stop);
            conns = took.0;
            count += 1;
            conns = __serve_started(conns, at);
            let stepped = (st, http.text(503, ""));
            if (!__serve_not_ready(f.request, opts, stop)) {
              conns = __with_connection_host(conns, at);
              stepped = __serve_step(handler, st, f.request, conns.request_host, opts);
            }
            st = stepped.0;
            let answered = __serve_respond(drv, conns, at, f.request.method, f.version, stepped.1, took.1, opts, count, took.2, clk);
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
      let taken = __staged_taken(drv, conns, flights, opts, plat.pool);
      conns = taken.0;
      flights = taken.1;
    }
    let due = __flights_expire(drv, conns, flights, opts, clk);
    conns = due.0;
    flights = due.1;
    let woken = __staged_taken(drv, conns, flights, opts, plat.pool);
    conns = woken.0;
    flights = woken.1;
    if (__quic_on(conns)) {
      let ticked = __h3_pass(drv, conns, flights, opts, stop);
      let h3_woken = __h3_start_all_with(drv, ticked.0, ticked.1, ticked.2, st, handler);
      conns = h3_woken.0;
      flights = h3_woken.1;
      st = h3_woken.2;
    }
    let ended = __serve_pass_end(drv, listener, conns, opts, stop, accepting, flights.all.len());
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
      return Ok(__stop_code(stop));
    }
  }
  if (accepting) {
    drv.unwatch(listener.fd);
  }
  __quic_close(drv, conns.quic);
  __exporter_close(drv, ex);
  drv.close();
  buf_free(conns.out);
  return Ok(0);
}
```

### `66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)`

```fern
<<66 pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host)>>=
pub function supervise(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse): Result[i32, ListenError] {
  return supervise_shutdown(port, opts, handler, (reason: string): void => {});
}

pub function supervise_shutdown(port: i32, opts: Config, handler: (HttpRequest, platform.Host) => HttpResponse, shutdown: (string) => void): Result[i32, ListenError] {
  return __supervise(port, opts, (l: __Listen, worker_opts: Config, snap: Option[Writer]): Result[i32, ListenError] => __serve_loop(l, handler, worker_opts, shutdown, snap));
}

pub function supervise_with[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse)): Result[i32, ListenError] {
  return supervise_with_shutdown(port, opts, init, handler, (reason: string, state: S): void => {});
}

pub function supervise_with_shutdown[S](port: i32, opts: Config, init: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse), shutdown: (string, S) => void): Result[i32, ListenError] {
  return __supervise(port, opts, (l: __Listen, worker_opts: Config, snap: Option[Writer]): Result[i32, ListenError] => __serve_loop_with(l, init, handler, worker_opts, shutdown, snap));
}
```

### `67 function __supervise(port: i32, opts: Config, worker: (__Listen, Config, Option[Writer])`

```fern
<<67 function __supervise(port: i32, opts: Config, worker: (__Listen, Config, Option[Writer])>>=
function __supervise(port: i32, opts: Config, worker: (__Listen, Config, Option[Writer]) => Result[i32, ListenError]): Result[i32, ListenError] {
  let workers = opts.workers;
  if (workers < 1) {
    workers = cpu_count();
  }
  if (workers < 1) {
    workers = 1;
  }
  let resolved = Config { ...opts, workers: workers };
  let shared = __listen(__adopt(-1), false);
  let pool: i32[] = [];
  if (__per_worker_listeners(resolved)) {
    match (__worker_pool(port, resolved)) {
      Ok(bound) => {
        pool = bound;
      },
      Err(code) => {
        return Err(__listen_error(port, code));
      }
    }
  } else {
    shared = __serve_listener(port, opts);
    if (shared.sock.fd < 0) {
      return Err(__listen_error(port, shared.sock.fd));
    }
  }
  let bound_fd = shared.sock.fd;
  if (pool.len() > 0) {
    bound_fd = pool[0];
  }
  let sup = __Supervisor { pids: [], spawned: [], pipes: [], readers: [], partial: [], snaps: [], quic: [], h3_free: __h3_pool(bound_fd, resolved), tcp: [], tcp_free: pool, backoff_ms: 100 as i64, fork_after: 0 as i64, fast_deaths: 0, stopping: false, worst: 0, crashed: 0 };
  let drv = async.real_driver();
  let chld = drv.watch_signal(17);
  if (chld < 0) {
    drv.close();
    __supervisor_release(shared, sup);
    return Ok(chld);
  }
  drv.watch_signal(15);
  drv.watch_signal(2);
  let ex: Option[metrics.Exporter] = None;
  while (true) {
    let refork_ms = -1;
    if (!sup.stopping && sup.pids.len() < workers) {
      let wait_ms = sup.fork_after - now_unix_ms();
      if (wait_ms <= 0 as i64) {
        let forked = __supervisor_fork(drv, __worker_listen(shared, sup), shared.sock.fd, worker, resolved, sup);
        sup = forked.0;
        match (forked.1) {
          Some(ended) => {
            __supervisor_release(shared, sup);
            return ended;
          },
          None => {}
        }
        if (opts.metrics_port > 0 && !__exporter_up(ex) && sup.pids.len() == 1) {
          match (__exporter(drv, opts, None)) {
            Ok(opened) => {
              ex = opened;
            },
            Err(e) => {
              __supervisor_abandon(drv, sup);
              drv.close();
              __supervisor_release(shared, sup);
              return Err(e);
            }
          }
        }
        continue;
      }
      refork_ms = wait_ms as i32;
    }
    if (sup.stopping && sup.pids.len() == 0) {
      __exporter_close(drv, ex);
      drv.close();
      __supervisor_release(shared, sup);
      return Ok(sup.worst);
    }
    let events = drv.wait(8, time.millis_timeout(refork_ms));
    sup = __supervisor_events(sup, events);
    ex = __supervisor_scrapes(drv, ex, events, sup);
    if (sup.gave_up()) {
      __supervisor_abandon(drv, sup);
      __exporter_close(drv, ex);
      drv.close();
      __supervisor_release(shared, sup);
      return Ok(sup.crashed);
    }
  }
  return Ok(0);
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
  if let Some(r) = sup.readers[i] {
    if let Ok(bytes) = r.read_chunk_bytes(65536) {
      got = bytes;
    }
  }
  if (got.len() == 0) {
    return sup;
  }
  let framed: (string[], string) = metrics.frames(sup.partial[i] + unsafe { string_from_bytes_unchecked(got) });
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
      ex = __scrape_wake(drv, ex, fd, __merged_of(sup.snaps), !sup.stopping && sup.pids.len() > 0);
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

### `68 function __supervisor_fork(drv: async.RealDriver, own: __Listen, listener: i32, worker: (i32, Config, Opt...`

```fern
<<68 function __supervisor_fork(drv: async.RealDriver, own: __Listen, listener: i32, worker: (i32, Config, Opt...>>=
function __supervisor_fork(drv: async.RealDriver, own: __Listen, listener: i32, worker: (__Listen, Config, Option[Writer]) => Result[i32, ListenError], opts: Config, sup: __Supervisor): (__Supervisor, Option[Result[i32, ListenError]]) {
  let snap = __snapshot_pipe(opts);
  let h3_sock = -1;
  if (sup.h3_free.len() > 0) {
    h3_sock = sup.h3_free[0];
  }
  let pid = proc_fork();
  if (pid == -38) {
    eprint("serve: supervision unavailable; serving single-process");
    __snapshot_pipe_close(snap);
    drv.unwatch_signal(17);
    drv.unwatch_signal(15);
    drv.unwatch_signal(2);
    drv.close();
    __pool_close(sup.h3_free, h3_sock);
    let alone = sup;
    if (listener < 0) {
      __pool_close(sup.tcp_free, own.sock.fd);
      alone = __Supervisor { ...sup, tcp_free: [own.sock.fd] };
    }
    return (alone, Some(worker(own, Config { ...opts, workers: 1, h3_sock: h3_sock }, None)));
  }
  if (pid < 0) {
    __snapshot_pipe_close(snap);
    return (sup, Some(Ok(pid)));
  }
  if (pid == 0) {
    drv.close();
    let loop_opts = Config { ...opts, stop_with_parent: true, h3_sock: h3_sock };
    if (listener < 0) {
      loop_opts = Config { ...loop_opts, workers: 1 };
    }
    exit(exit_code(worker(own, loop_opts, __snapshot_writer(snap))));
    return (sup, None);
  }
  let held = -1;
  let free = sup.tcp_free;
  if (listener < 0) {
    held = own.sock.fd;
    free = __without_index_i32(free, 0);
  }
  let read_end = -1;
  let reader: Option[Reader] = None;
  match (snap) {
    Some(q) => {
      q.w.close();
      drv.watch(q.r.fd, 1);
      read_end = q.r.fd;
      reader = Some(q.r);
    },
    None => {}
  }
  return (__Supervisor { ...sup, pids: sup.pids.append(pid), spawned: sup.spawned.append(now_unix_ms()), pipes: sup.pipes.append(read_end), readers: sup.readers.append(reader), partial: sup.partial.append(""), snaps: sup.snaps.append(""), quic: sup.quic.append(h3_sock), h3_free: __without_index_i32(sup.h3_free, 0), tcp: sup.tcp.append(held), tcp_free: free }, None);
}

function __snapshot_pipe(opts: Config): Option[Pipe] {
  if (opts.metrics_port <= 0) {
    return None;
  }
  match (pipe()) {
    Ok(q) => {
      match (q.w.set_nonblocking(true)) {
        Ok(_) => {
          return Some(q);
        },
        Err(e) => {
          q.r.close();
          q.w.close();
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

function __snapshot_writer(own snap: Option[Pipe]): Option[Writer] {
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

function __snapshot_pipe_close(own snap: Option[Pipe]): void {
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

// A scrape renders its body once, when it is answered, not on each wake that writes more of it.
function __exposition_of(m: metrics.ServerMetrics, opts: Config): () => string {
  return (): string => __loop_exposition(m, opts);
}

function __merged_of(snaps: string[]): () => string {
  return (): string => metrics.merge(snaps);
}

function __exporter(drv: async.RealDriver, opts: Config, snap: Option[Writer]): Result[Option[metrics.Exporter], ListenError] {
  if (opts.metrics_port <= 0 || __snapshot_timeout(snap, 0 - 1) >= 0) {
    return Ok(None);
  }
  match (metrics.listen(drv, opts.metrics_port)) {
    Ok(e) => {
      return Ok(Some(e));
    },
    Err(e) => {
      return Err(ListenError { port: opts.metrics_port, error: e });
    }
  }
  return Ok(None);
}

// __loop_exporter closes the loop's driver, and releases its listener,
// when the exporter cannot listen.
function __loop_exporter(drv: async.RealDriver, l: __Listen, opts: Config, snap: Option[Writer]): Result[Option[metrics.Exporter], ListenError] {
  match (__exporter(drv, opts, snap)) {
    Ok(ex) => {
      return Ok(ex);
    },
    Err(e) => {
      drv.close();
      __listen_release(l);
      return Err(e);
    }
  }
  return Ok(None);
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

function __scrape_wake(drv: async.RealDriver, ex: Option[metrics.Exporter], fd: i32, text: () => string, ready: boolean): Option[metrics.Exporter] {
  match (ex) {
    Some(e) => {
      return Some(e.wake(drv, fd, (path: string): HttpResponse => metrics.scrape(path, text, ready)));
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

// A listener per worker on a Linux kernel, where SO_REUSEPORT spreads
// connections: one shared queue hands most of a burst to whichever worker
// wakes first. The supervisor binds them all and keeps them open, so a dead
// worker's queued connections wait in its socket for the replacement.
function __per_worker_listeners(opts: Config): boolean {
  if (env("LISTEN_FDS").is_some()) {
    return false;
  }
  return opts.reuse_port || opts.workers > 1 && (target_os() == "linux" || target_os() == "android");
}

function __worker_pool(port: i32, opts: Config): Result[i32[], i32] {
  let each = Config { ...opts, reuse_port: true };
  let pool: i32[] = [];
  let at = port;
  for i in 0..opts.workers {
    let l = __serve_listener(at, each);
    if (l.sock.fd < 0) {
      __pool_close(pool, -1);
      return Err(l.sock.fd);
    }
    tcp_socket_ctl(l.sock, 8, 0);
    if (i == 0) {
      at = tcp_local_port(l.sock);
    }
    pool = pool.append(l.sock.fd);
  }
  return Ok(pool);
}

function __worker_listen(shared: __Listen, sup: __Supervisor): __Listen {
  if (shared.sock.fd >= 0 || sup.tcp_free.len() == 0) {
    return shared;
  }
  return __Listen { sock: __adopt(sup.tcp_free[0]), inherited: false, h3_key: shared.h3_key };
}

function __supervisor_release(shared: __Listen, sup: __Supervisor): void {
  __listen_release(shared);
  __pool_close(sup.tcp_free, -1);
  __pool_close(sup.tcp, -1);
}
```

### `70 struct __Supervisor`

```fern
<<70 struct __Supervisor>>=
struct __Supervisor { pids: i32[], spawned: i64[], pipes: i32[], readers: Option[Reader][], partial: string[], snaps: string[], quic: i32[], h3_free: i32[], tcp: i32[], tcp_free: i32[], backoff_ms: i64, fork_after: i64, fast_deaths: i32, stopping: boolean, worst: i32, crashed: i32 }
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
    drv.wait(8, Some(time.duration_millis(left)));
    s = __supervisor_reap(s);
  }
  for pid in s.pids {
    match (signal_send(pid, 9)) {
      Ok(_) => {},
      Err(_) => {}
    }
  }
  while (s.pids.len() > 0) {
    drv.wait(8, Some(time.duration_millis(50)));
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
  let i = 0;
  while (i < sup.pids.len()) {
    let code = proc_waitpid_nohang(sup.pids[i]);
    if (code == -1) {
      i += 1;
      continue;
    }
    if (code < 0) {
      code = 0 - code;
    }
    let lived_ms = now_unix_ms() - sup.spawned[i];
    if let Some(r) = sup.readers[i] {
      unsafe {
        r.close_unchecked();
      }
    }
    let freed = sup.h3_free;
    if (sup.quic[i] >= 0) {
      freed = freed.append(sup.quic[i]);
    }
    let listeners = sup.tcp_free;
    if (sup.tcp[i] >= 0) {
      listeners = listeners.append(sup.tcp[i]);
    }
    sup = __Supervisor { ...sup, pids: __without_index_i32(sup.pids, i), spawned: __without_index_i64(sup.spawned, i), pipes: __without_index_i32(sup.pipes, i), readers: __without_index_reader(sup.readers, i), partial: __without_index_str(sup.partial, i), snaps: __without_index_str(sup.snaps, i), quic: __without_index_i32(sup.quic, i), h3_free: freed, tcp: __without_index_i32(sup.tcp, i), tcp_free: listeners };
    if (sup.stopping) {
      if (code > sup.worst) {
        sup = __Supervisor { ...sup, worst: code };
      }
      continue;
    }
    eprint("serve: worker died with exit code " + code.to_string() + "; restarting");
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
    let next = sup.backoff_ms * (2 as i64);
    if (next > 5000 as i64) {
      next = 5000 as i64;
    }
    sup = __Supervisor { ...sup, fork_after: now_unix_ms() + sup.backoff_ms, backoff_ms: next };
  }
  return sup;
}

function __without_index_i32(xs: i32[], at: i32): i32[] {
  let out: i32[] = [];
  let i = 0;
  while (i < xs.len()) {
    if (i != at) {
      out = out.append(xs[i]);
    }
    i += 1;
  }
  return out;
}

function __without_index_reader(xs: Option[Reader][], at: i32): Option[Reader][] {
  let out: Option[Reader][] = [];
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
  let i = 0;
  while (i < xs.len()) {
    if (i != at) {
      out = out.append(xs[i]);
    }
    i += 1;
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
// keys[i] is every connection ID connection i answers to, read again when
// its cids_version moves; paths[i] is the address it is sent to, as the
// socket reported it, and peers[i] the same address parsed.
struct __Quic { sock: Option[Socket], port: i32, cfg: quic.Config, key: u8[], conns: h3q.Conn[], keys: u8[][][], versions: i64[], origs: u8[][], peers: net.SocketAddr[], paths: u8[][], hosts: platform.Host[], serials: i32[], next: i32, producers: __H3Producer[], stopping: boolean, inbox: net.Inbox, out: net.Batch }

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

// __h3_marked turns ECN on where the batch sends TOS bytes and the inbox
// reads them.
function __h3_marked(c: quic.Config): quic.Config {
  if (target_os() == "linux" || target_os() == "android") {
    return c.with_ecn();
  }
  return c;
}

// __h3_slots is the messages one receive may bring. Each slot holds the
// largest datagram, or the largest run the kernel coalesces under UDP_GRO.
function __h3_slots(): i32 {
  return 16;
}

function __quic_none(): __Quic {
  return __Quic { sock: None, port: 0, cfg: quic.config([]), key: [], conns: [], keys: [], versions: [], origs: [], peers: [], paths: [], hosts: [], serials: [], next: 1, producers: [], stopping: false, inbox: net.inbox(0, 0), out: net.batch() };
}

function __quic_socket(port: i32): Socket {
  if (target_os() != "wasi") {
    let both = udp_bind_with(__every_v6(), port, true);
    if (both.fd >= 0 || !net.error_from_host(both.fd).ipv6_unavailable()) {
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
  let port = tcp_local_port(__adopt(listener));
  let pool: i32[] = [];
  let i = 0;
  while (i < opts.workers) {
    let sock = __quic_socket(port);
    if (sock.fd < 0) {
      __pool_close(pool, -1);
      return [];
    }
    pool = pool.append(sock.fd);
    i += 1;
  }
  return pool;
}

function __pool_close(pool: i32[], keep: i32): void {
  let i = 0;
  while (i < pool.len()) {
    if (pool[i] != keep) {
      unsafe {
        __fd_close(pool[i]);
      }
    }
    i += 1;
  }
}

function __quic_open[D: async.Driver](drv: D, l: __Listen, opts: Config): __Quic {
  let Some(t) = opts.tls else {
    return __quic_none();
  };
  if (!opts.h3) {
    return __quic_none();
  }
  let port = tcp_local_port(l.sock);
  let sock = __adopt(opts.h3_sock);
  if (sock.fd < 0) {
    sock = __quic_socket(port);
  }
  if (sock.fd < 0) {
    return __quic_none();
  }
  let nb = net.set_nonblocking(sock, true);
  let gro = net.set_udp_gro(sock, true);
  let marks = net.set_recv_tos(sock, true);
  if (drv.watch(sock.fd, LISTENER_INTEREST) < 0) {
    let closed = net.close(sock);
    return __quic_none();
  }
  return __Quic { ...__quic_none(), sock: Some(sock), port: port, cfg: __h3_marked(h3q.transport(quic.config([]).server_side(t.identity).with_tickets(l.h3_key).with_early_data())), key: l.h3_key, inbox: net.inbox(__h3_slots(), 65536) };
}

function __quic_on(c: __Conns): boolean {
  if let Some(_) = c.quic.sock {
    return true;
  }
  return false;
}

function __quic_owns(c: __Conns, fd: i32): boolean {
  let Some(sock) = c.quic.sock else {
    return false;
  };
  return fd == sock.fd;
}

function __quic_close[D: async.Driver](drv: D, q: __Quic): void {
  let Some(sock) = q.sock else {
    return;
  };
  let pending = q.out.send(sock);
  let now: Instant = clock.instant();
  let i: i32 = 0;
  while (i < q.conns.len()) {
    let closing: h3q.Conn = q.conns[i].close(now).transmit(now);
    __h3_sent(sock, closing.datagrams(), q.paths[i]);
    i = i + 1;
  }
  drv.unwatch(sock.fd);
  __shut(sock);
}

function __quic_timeout(q: __Quic, now: i64): i32 {
  let best: i64 = 0 - 1 as i64;
  for c in q.conns {
    if let Some(d) = c.deadline() {
      best = __sooner(best, time.instant_nanos(d));
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
  return HttpResponse { ...resp, headers: resp.headers.append("alt-svc", "h3=\":" + c.quic.port.to_string() + "\"; ma=86400") };
}
```

### `75 function __h3_sent(sock: Socket, out: u8[][], path: [u8]): void`

```fern
<<75 function __h3_sent(sock: Socket, out: u8[][], path: [u8]): void>>=
// __h3_sent sends a connection's flight to the address at path, as the
// socket reported it, in as few system calls as the host offers.
function __h3_sent(sock: Socket, out: u8[][], path: [u8]): void {
  let r = net.send_many(sock, path, out);
}
```

### `76 struct __H3Req`

```fern
<<76 struct __H3Req>>=
struct __H3Req { serial: i32, id: i64, req: HttpRequest, host: platform.Host }

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

// __h3_event receives every datagram the socket holds, as many messages per
// call as the inbox has slots, until a call brings fewer than that, the
// socket has nothing more, or the burst is spent.
function __h3_event[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, stop: __Stop, host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, __Flights, __H3Burst) {
  let cur = c;
  let fs = fls;
  let burst = __H3Burst { reqs: [], touched: [] };
  let n = 0;
  while (n < __h3_burst()) {
    let Some(sock) = cur.quic.sock else {
      return (cur, fs, burst);
    };
    let q = cur.quic;
    let r = q.inbox.receive(sock);
    cur = __Conns { ...cur, quic: __Quic { ...q, inbox: r.0 } };
    let Ok(got) = r.1 else {
      return (cur, fs, burst);
    };
    let i = 0;
    while (i < got) {
      let m = __h3_message(drv, cur, fs, i, burst, opts, stop, host_for);
      cur = m.0;
      fs = m.1;
      burst = m.2;
      i += 1;
    }
    n += got;
    if (got < cur.quic.inbox.slots) {
      return (cur, fs, burst);
    }
  }
  return (cur, fs, burst);
}

// __h3_message hands each datagram of message i of the inbox to the
// connection it is for.
function __h3_message[D: async.Driver](drv: D, c: __Conns, fls: __Flights, i: i32, burst: __H3Burst, opts: Config, stop: __Stop, host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, __Flights, __H3Burst) {
  let cur = c;
  let fs = fls;
  let b = burst;
  let from = cur.quic.inbox.sender_at(i);
  let k = 0;
  while (k < cur.quic.inbox.segments(i)) {
    let (at, len) = cur.quic.inbox.segment(i, k);
    let r = __h3_datagram(drv, cur, fs, cur.quic.inbox.buf[at:at + len], cur.quic.inbox.froms[from:from + net.SENDER_BYTES], cur.quic.inbox.tos_of(i), b, opts, stop, host_for);
    cur = r.0;
    fs = r.1;
    b = r.2;
    k += 1;
  }
  return (cur, fs, b);
}

function __h3_datagram[D: async.Driver](drv: D, c: __Conns, fls: __Flights, d: [u8], from: [u8], tos: i32, burst: __H3Burst, opts: Config, stop: __Stop, host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, __Flights, __H3Burst) {
  let found = __h3_route(c, d, from, opts, stop, host_for);
  let cur = found.0;
  let i = found.1;
  if (i < 0) {
    return (cur, fls, burst);
  }
  let q = cur.quic;
  let serial = q.serials[i];
  cur = __h3_repathed(__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].receive_from(d, from, tos, clock.instant())) } }, i);
  let taken = __h3_events(cur, i, burst);
  let more = __h3_more(drv, taken.0, fls, serial);
  return (more.0, more.1, __h3_touched(taken.1, serial));
}

// __h3_route finds the connection a datagram is for, accepting a new one
// for an Initial; a short header naming an ID this server issued is
// matched without parsing the header.
function __h3_route(c: __Conns, d: [u8], from: [u8], opts: Config, stop: __Stop, host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, i32) {
  let known = __h3_short_find(c.quic, d);
  if (known >= 0) {
    return (c, known);
  }
  let Ok(h) = packet.parse(d, 0, c.quic.cfg.cid_len) else {
    return (c, -1);
  };
  let i = __h3_find(c.quic, h.dcid);
  if (i >= 0) {
    return (c, i);
  }
  return __h3_accept(c, h, d, from, opts, stop, host_for);
}

function __h3_short_find(q: __Quic, d: [u8]): i32 {
  let n = q.cfg.cid_len;
  if (d.len() < 1 + n || (d[0] as i32 & 128) != 0) {
    return -1;
  }
  let i = 0;
  while (i < q.keys.len()) {
    for key in q.keys[i] {
      if (__names(key, d, n)) {
        return i;
      }
    }
    i += 1;
  }
  return -1;
}

function __names(key: u8[], d: [u8], n: i32): boolean {
  if (key.len() != n) {
    return false;
  }
  let i: i32 = 0;
  while (i < n) {
    if (key[i] != d[1 + i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

// __h3_repathed brings the routing table and the peer address of connection
// i up to date after the connection issued or retired an ID or moved to
// another path.
function __h3_repathed(c: __Conns, i: i32): __Conns {
  let q = c.quic;
  let quic = q.conns[i].quic();
  let out = q;
  if (quic.cids_version() != q.versions[i]) {
    out = __Quic { ...out, keys: out.keys.with(i, quic.local_cids()), versions: out.versions.with(i, quic.cids_version()) };
  }
  let path = quic.path();
  if (path.len() > 0 && !__bytes_same(path, out.paths[i])) {
    out = __Quic { ...out, paths: out.paths.with(i, path), peers: out.peers.with(i, net.sender(path)) };
  }
  return __Conns { ...c, quic: out };
}

function __h3_find(q: __Quic, dcid: u8[]): i32 {
  let i: i32 = 0;
  while (i < q.conns.len()) {
    if (__bytes_same(q.origs[i], dcid)) {
      return i;
    }
    for key in q.keys[i] {
      if (__bytes_same(key, dcid)) {
        return i;
      }
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

function __h3_accept(c: __Conns, h: packet.Header, d: [u8], path: [u8], opts: Config, stop: __Stop, host_for: (Option[net.SocketAddr]) => platform.Host): (__Conns, i32) {
  let q = c.quic;
  let from = net.sender(path);
  if (!__h3_initial(h) || d.len() < packet.MIN_INITIAL_DATAGRAM || q.stopping || __serve_stopping(stop) || q.conns.len() >= opts.max_connections) {
    return (c, -1);
  }
  if (opts.max_connections_per_ip > 0 && __h3_from_ip(q, from.ip) >= opts.max_connections_per_ip) {
    return (c, -1);
  }
  let Ok(odcid) = __h3_admitted(q, h, path, from, opts) else {
    return (c, -1);
  };
  let Ok(made) = __h3_opened(q, h, odcid) else {
    return (c, -1);
  };
  let conn = h3q.server(made, h3.config()).with_max_body(opts.limits.body as i64);
  let grown = __Quic { ...q, conns: q.conns.append(conn), keys: q.keys.append(made.local_cids()), versions: q.versions.append(made.cids_version()), origs: q.origs.append(h.dcid), peers: q.peers.append(from), paths: q.paths.append(__bytes_range(path, 0, path.len())), hosts: q.hosts.append(host_for(Some(from))), serials: q.serials.append(q.next), next: q.next + 1 };
  return (__Conns { ...c, quic: grown }, grown.conns.len() - 1);
}

// __h3_retry_wanted is whether a new client must prove its address with a
// Retry before the server keeps state for it: when h3_retry_above or more
// connections are still in their handshake. Under a flood of Initials from
// spoofed addresses the handshakes never complete, so that count is what
// grows.
function __h3_retry_wanted(q: __Quic, opts: Config): boolean {
  if (opts.h3_retry_above < 0) {
    return false;
  }
  let n = 0;
  for c in q.conns {
    if (c.quic().handshaking()) {
      n += 1;
    }
  }
  return n >= opts.h3_retry_above;
}

function __h3_initial(h: packet.Header): boolean {
  match (h.kind) {
    packet.Initial => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

// __h3_admitted decides whether the server opens a connection for an
// Initial, and answers it when not. Without a token, a client the server
// wants validated first gets a Retry and nothing is kept for it. With one,
// the token must be one a Retry issued for this address and connection ID,
// or the client gets INVALID_TOKEN. Ok carries the connection ID the
// client's first Initial named.
function __h3_admitted(q: __Quic, h: packet.Header, path: [u8], from: net.SocketAddr, opts: Config): Result[u8[], string] {
  let Some(sock) = q.sock else {
    return Err("no socket");
  };
  if (h.token.len() == 0) {
    if (__h3_retry_wanted(q, opts)) {
      __h3_sent(sock, [quic.retry(q.cfg, q.key, random_bytes(quic.retry_entropy(q.cfg)), h, path, clock.now())], path);
      return Err("retry sent");
    }
    return Ok(h.dcid);
  }
  let Some(odcid) = quic.validated(q.key, h, path, clock.now()) else {
    __h3_sent(sock, [quic.invalid_token(h)], path);
    return Err("token refused");
  };
  return Ok(odcid);
}

// __h3_opened opens the connection for an admitted Initial: one that
// carries a token continues the client's first Initial, which named odcid.
function __h3_opened(q: __Quic, h: packet.Header, odcid: u8[]): Result[quic.Conn, string] {
  let entropy = random_bytes(quic.server_entropy(q.cfg));
  if (h.token.len() > 0) {
    return quic.accept_validated(q.cfg, entropy, h, odcid, clock.instant(), clock.now());
  }
  return quic.accept(q.cfg, entropy, h, clock.instant(), clock.now());
}

function __h3_events(c: __Conns, i: i32, burst: __H3Burst): (__Conns, __H3Burst) {
  let q: __Quic = c.quic;
  let evs: h3q.Event[] = q.conns[i].events;
  let producers: __H3Producer[] = q.producers;
  let b: __H3Burst = burst;
  let serial: i32 = q.serials[i];
  for e in evs {
    match (e) {
      h3q.Requested(id, req) => {
        b = __H3Burst { ...b, reqs: b.reqs.append(__H3Req { serial: serial, id: id, req: req, host: q.hosts[i] }) };
      },
      h3q.StreamDropped(id, _) => {
        producers = __h3_producer_dropped(producers, serial, id);
      },
      _ => {}
    }
  }
  return (__Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].events_taken()), producers: producers } }, b);
}
```

### `78 function __h3_start_all[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, burst: __H3Burst, handler...`

```fern
<<78 function __h3_start_all[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, burst: __H3Burst, handler...>>=
function __h3_start_all[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, burst: __H3Burst, handler: (HttpRequest, platform.Host) => HttpResponse, opts: Config, stop: __Stop): (__Conns, __Flights) {
  if (burst.reqs.len() == 0 && burst.touched.len() == 0) {
    return (c, fls);
  }
  let fs = fls;
  for r in burst.reqs {
    let started = __serve_start(handler, r.host, r.req, "HTTP/3", true, Idle, opts, stop);
    let done = __h3_request(drv, c, fs, r.serial, r.id, r.req, started);
    c = done.0;
    fs = done.1;
  }
  return (__h3_flush_all(c, burst.touched), fs);
}

function __h3_start_all_with[D: async.Driver, S](drv: D, own c: __Conns, fls: __Flights, burst: __H3Burst, st: S, handler: (S, HttpRequest, platform.Host) => (S, HttpResponse)): (__Conns, __Flights, S) {
  if (burst.reqs.len() == 0 && burst.touched.len() == 0) {
    return (c, fls, st);
  }
  let fs: __Flights = fls;
  let state: S = st;
  for r in burst.reqs {
    let (next, resp) = handler(state, r.req, r.host);
    state = next;
    let done: (__Conns, __Flights) = __h3_request(drv, c, fs, r.serial, r.id, r.req, HandlerDone(resp));
    c = done.0;
    fs = done.1;
  }
  return (__h3_flush_all(c, burst.touched), fs, state);
}

function __h3_flush_all(own c: __Conns, serials: i32[]): __Conns {
  let cur = c;
  for s in serials {
    cur = __h3_flush(cur, s);
  }
  return cur;
}

function __h3_request[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, serial: i32, id: i64, req: HttpRequest, started: __Started): (__Conns, __Flights) {
  let now = monotonic_ns();
  match (started) {
    HandlerDone(resp) => {
      let counted = __serve_count_from(c, req.method, resp.status, now);
      return __h3_respond(drv, counted, fls, serial, id, req.method, resp);
    },
    HandlerParked(fl) => {
      let parked = __Flight { ...fl, fd: -1, stream: id as i32, quic: serial, since: now };
      return (c, __flights_add(fls, __flight_watch(drv, parked)));
    }
  }
  return (c, fls);
}

function __h3_at(q: __Quic, serial: i32): i32 {
  let i = 0;
  while (i < q.serials.len()) {
    if (q.serials[i] == serial) {
      return i;
    }
    i += 1;
  }
  return -1;
}

function __h3_respond[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, serial: i32, id: i64, method: string, resp: HttpResponse): (__Conns, __Flights) {
  let i = __h3_at(c.quic, serial);
  if (i < 0) {
    return (c, fls);
  }
  let cur = __stage_background(c, resp);
  let q = cur.quic;
  resp = __file_chunks(resp);
  match (resp.body) {
    BodyChunks(producer) => {
      if (method == "HEAD" || http.bodiless(resp.status)) {
        return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date).respond_head(id, resp, -1i64).end(id, HeaderMap { names: [], values: [] })) } }, fls);
      }
      let p = __H3Producer { serial: serial, stream: id, producer: producer, index: 0, trailers: resp.trailers, running: false };
      cur = __Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date).respond_head(id, resp, -1i64)), producers: q.producers.append(p) } };
      return __h3_produce(drv, cur, fls, cur.quic.producers.len() - 1);
    },
    _ => {
      if (method == "HEAD") {
        return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date).respond_head(id, resp, resp.body_bytes().len() as i64).end(id, HeaderMap { names: [], values: [] })) } }, fls);
      }
      return (__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date).respond(id, resp)) } }, fls);
    }
  }
  return (cur, fls);
}

function __h3_produce[D: async.Driver](drv: D, c: __Conns, fls: __Flights, pi: i32): (__Conns, __Flights) {
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
        return (running, __flights_add(fls, __flight_watch(drv, fl)));
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

function __h3_producer_failed(own c: __Conns, pi: i32): __Conns {
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

function __h3_more[D: async.Driver](drv: D, c: __Conns, fls: __Flights, serial: i32): (__Conns, __Flights) {
  let cur: __Conns = c;
  let fs: __Flights = fls;
  let k: i32 = 0;
  while (k < cur.quic.producers.len()) {
    let p: __H3Producer = cur.quic.producers[k];
    let before: i32 = cur.quic.producers.len();
    if (p.serial == serial && !p.running) {
      let r: (__Conns, __Flights) = __h3_produce(drv, cur, fs, k);
      cur = r.0;
      fs = r.1;
    }
    if (cur.quic.producers.len() == before) {
      k = k + 1;
    }
  }
  return (cur, fs);
}

function __h3_producer_wake[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, fl: __Flight, t: async.Task[Option[u8[]]], wake: async.Wake): (__Conns, __Flights) {
  let k: i32 = __h3_producer_at(c.quic, fl.quic, fl.stream as i64);
  if (k < 0) {
    let gone: async.TaskStatus[Option[u8[]]] = async.task_resume(t, async.Wake.Cancelled);
    return (c, fls);
  }
  match (async.task_resume(t, wake)) {
    Done(chunk) => {
      let (chunked, ended) = __h3_chunk(c, k, chunk);
      if (ended) {
        return (__h3_flush(chunked, fl.quic), fls);
      }
      let more: (__Conns, __Flights) = __h3_produce(drv, chunked, fls, k);
      return (__h3_flush(more.0, fl.quic), more.1);
    },
    Suspended(w) => {
      return (c, __flights_add(fls, __flight_watch(drv, __Flight { ...fl, wait: w })));
    }
  }
  return (c, fls);
}

function __h3_answer[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, fl: __Flight, resp: HttpResponse): (__Conns, __Flights) {
  let counted = __serve_count_from(c, fl.method, resp.status, fl.since);
  let r = __h3_respond(drv, counted, fls, fl.quic, fl.stream as i64, fl.method, resp);
  return (__h3_flush(r.0, fl.quic), r.1);
}

function __h3_abandon(own c: __Conns, fl: __Flight): __Conns {
  let i: i32 = __h3_at(c.quic, fl.quic);
  if (i < 0) {
    return c;
  }
  let aborted = c.quic.conns[i].abort(fl.stream as i64, h3.H3_REQUEST_CANCELLED);
  return __h3_flush(__Conns { ...c, quic: __Quic { ...c.quic, conns: c.quic.conns.with(i, aborted) } }, fl.quic);
}

function __h3_flush(own c: __Conns, serial: i32): __Conns {
  let q = c.quic;
  let i = __h3_at(q, serial);
  if (i < 0) {
    return c;
  }
  let built = __Conns { ...c, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].transmit(clock.instant())) } };
  let b = built.quic;
  let flight = b.conns[i].datagrams();
  let mark = b.conns[i].ecn_codepoint();
  return __Conns { ...built, quic: __Quic { ...b, conns: b.conns.with(i, b.conns[i].sent()), out: b.out.add(b.paths[i], flight, mark) } };
}

// __h3_drain sends every flight the pass gathered, in one call, before the
// loop waits again.
function __h3_drain(own c: __Conns): __Conns {
  let q = c.quic;
  if (q.out.len() == 0) {
    return c;
  }
  let Some(sock) = q.sock else {
    return c;
  };
  let r = q.out.send(sock);
  return __Conns { ...c, quic: __Quic { ...q, out: r.0 } };
}
```

### `80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, stop: __Stop): (__Co...`

```fern
<<80 function __h3_pass[D: async.Driver](drv: D, c: __Conns, fls: __Flights, opts: Config, stop: __Stop): (__Co...>>=
function __h3_pass[D: async.Driver](drv: D, own c: __Conns, fls: __Flights, opts: Config, stop: __Stop): (__Conns, __Flights, __H3Burst) {
  let burst = __H3Burst { reqs: [], touched: [] };
  let cur = __h3_goaway(c, stop);
  let now = clock.instant();
  let i = 0;
  while (i < cur.quic.conns.len()) {
    let q = cur.quic;
    let due = false;
    if let Some(d) = q.conns[i].deadline() {
      due = d.cmp(now) <= 0;
    }
    if (due) {
      let serial = q.serials[i];
      cur = __h3_repathed(__Conns { ...cur, quic: __Quic { ...q, conns: q.conns.with(i, q.conns[i].tick(now)) } }, i);
      let before = burst.reqs.len();
      let taken = __h3_events(cur, i, burst);
      cur = __h3_flush(taken.0, serial);
      burst = taken.1;
      if (burst.reqs.len() > before) {
        burst = __h3_touched(burst, serial);
      }
    }
    i += 1;
  }
  return (__h3_forget_done(cur), fls, burst);
}

function __h3_goaway(own c: __Conns, stop: __Stop): __Conns {
  let q = c.quic;
  if (q.stopping || !__serve_stopping(stop)) {
    return c;
  }
  let cur = __Conns { ...c, quic: __Quic { ...q, stopping: true, conns: __each_shutdown(q.conns) } };
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

function __h3_forget_done(own c: __Conns): __Conns {
  if (!__h3_any_done(c.quic.conns)) {
    return c;
  }
  let q: __Quic = c.quic;
  let kept: __Quic = __Quic { ...q, conns: [], keys: [], versions: [], origs: [], peers: [], paths: [], hosts: [], serials: [] };
  let producers: __H3Producer[] = q.producers;
  let i: i32 = 0;
  while (i < q.conns.len()) {
    if (q.conns[i].done()) {
      producers = __h3_producers_of_gone(producers, q.serials[i]);
    } else {
      kept = __Quic { ...kept, conns: kept.conns.append(q.conns[i]), keys: kept.keys.append(q.keys[i]), versions: kept.versions.append(q.versions[i]), origs: kept.origs.append(q.origs[i]), peers: kept.peers.append(q.peers[i]), paths: kept.paths.append(q.paths[i]), hosts: kept.hosts.append(q.hosts[i]), serials: kept.serials.append(q.serials[i]) };
    }
    i = i + 1;
  }
  return __Conns { ...c, quic: __Quic { ...kept, producers: producers } };
}

function __h3_any_done(cs: h3q.Conn[]): boolean {
  for x in cs {
    if (x.done()) {
      return true;
    }
  }
  return false;
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
