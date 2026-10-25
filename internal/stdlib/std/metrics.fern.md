# Prometheus metric values

This module collects and exports counters, gauges, and histograms in Prometheus text format.

## Model

Counters, gauges, and histograms store measurements in cells. A registry groups these values and renders Prometheus text.

## Contracts

Histogram bounds determine bucket counts. Labels require text escapes. Network exporter helpers are separate from metric construction and text output.

## Executable example

```fern test name=std-metrics-example
import "std/metrics";

function main(): i32 {
  let c = metrics.counter("requests_total", "Request count");
  c.inc();
  c.add(2);
  if (c.get() != 3) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Counter>>

<<4 pub function counter(name: string, help: string): Counter>>

<<5 function __escape(s: string, quotes: boolean): string>>

<<6 pub struct Registry>>

<<7 pub function registry(): Registry>>

<<8 function __head(name: string, help: string, kind: string, seen: string[]): string>>

<<9 pub function (h: Histogram) render_samples(): string>>

<<10 function __comma(labels: string): i32>>

<<11 pub struct ServerMetrics>>

<<12 pub function server_metrics(): ServerMetrics>>

<<13 function __seconds(ns: i64): f64>>

<<14 pub function (m: ServerMetrics) render(): string>>

<<15 pub const EOF: string>>

<<16 pub function frames(buf: string): (string[], string)>>

<<17 struct __Merged>>

<<18 pub function merge(parts: string[]): string>>

<<19 function __merge_line(m: __Merged, line: string): __Merged>>

<<20 function __merged_sample(m: __Merged, k: i32): string>>

<<21 pub struct Exporter>>

<<22 pub function listen[D: async.Driver](drv: D, port: i32): Result[Exporter, net.NetError]>>

<<23 function __listen_any(port: i32): i32>>

<<24 pub function (e: Exporter) port(): i32>>

<<25 function __index_of_i32(xs: i32[], x: i32): i32>>

<<26 pub function (e: Exporter) wake[D: async.Driver](drv: D, fd: i32, answer: (string)>>

<<27 function (e: Exporter) accept[D: async.Driver](drv: D): Exporter>>

<<28 pub function (e: Exporter) close[D: async.Driver](drv: D): void>>

<<29 function __without_i32(xs: i32[], at: i32): i32[]>>

<<30 pub function exposition(body: string): HttpResponse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module collects and exports counters, gauges, and histograms in Prometheus text format.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/async";
import "std/http";
import "std/i32";
import "std/i64";
import "std/net";
import "std/f64";
import "std/string";
```

### `3 pub struct Counter`

```fern
<<3 pub struct Counter>>=
pub struct Counter { name: string, help: string, labels: string, value: Cell[i64] }

pub struct Gauge { name: string, help: string, labels: string, value: Cell[i64] }

pub struct Histogram { name: string, help: string, labels: string, bounds: f64[], counts: Cell[i64][], sum: Cell[f64], count: Cell[i64] }
```

### `4 pub function counter(name: string, help: string): Counter`

```fern
<<4 pub function counter(name: string, help: string): Counter>>=
pub function counter(name: string, help: string): Counter {
  return Counter { name: name, help: help, labels: "", value: cell_new(0 as i64) };
}

pub function (c: Counter) with_labels(labels: string): Counter {
  return Counter { name: c.name, help: c.help, labels: labels, value: cell_new(0 as i64) };
}

pub function (c: Counter) inc(): void {
  c.value.set(c.value.get() + 1 as i64);
}

pub function (c: Counter) add(n: i64): void {
  c.value.set(c.value.get() + n);
}

pub function (c: Counter) get(): i64 {
  return c.value.get();
}

pub function gauge(name: string, help: string): Gauge {
  return Gauge { name: name, help: help, labels: "", value: cell_new(0 as i64) };
}

pub function (g: Gauge) with_labels(labels: string): Gauge {
  return Gauge { name: g.name, help: g.help, labels: labels, value: cell_new(0 as i64) };
}

pub function (g: Gauge) set(v: i64): void {
  g.value.set(v);
}

pub function (g: Gauge) inc(): void {
  g.value.set(g.value.get() + 1 as i64);
}

pub function (g: Gauge) dec(): void {
  g.value.set(g.value.get() - 1 as i64);
}

pub function (g: Gauge) get(): i64 {
  return g.value.get();
}

pub function histogram(name: string, help: string, bounds: f64[]): Histogram {
  let counts: Cell[i64][] = [];
  let i: i32 = 0;
  while (i <= bounds.len()) {
    counts = counts.append(cell_new(0 as i64));
    i = i + 1;
  }
  return Histogram { name: name, help: help, labels: "", bounds: bounds, counts: counts, sum: cell_new(0.0), count: cell_new(0 as i64) };
}

pub function (h: Histogram) with_labels(labels: string): Histogram {
  return Histogram { ...histogram(h.name, h.help, h.bounds), labels: labels };
}

pub function duration_buckets(): f64[] {
  return [0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0];
}

pub function (h: Histogram) observe(x: f64): void {
  let i: i32 = 0;
  while (i < h.bounds.len() && x > h.bounds[i]) {
    i = i + 1;
  }
  let c: Cell[i64] = h.counts[i];
  c.set(c.get() + 1 as i64);
  h.sum.set(h.sum.get() + x);
  h.count.set(h.count.get() + 1 as i64);
}

pub function (h: Histogram) count(): i64 {
  return h.count.get();
}

pub function (h: Histogram) sum(): f64 {
  return h.sum.get();
}

pub function label(name: string, value: string): string {
  return name + "=\"" + __escape(value, true) + "\"";
}

pub function labels(parts: string[]): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < parts.len()) {
    if (i > 0) {
      out = out + ",";
    }
    out = out + parts[i];
    i = i + 1;
  }
  return out;
}
```

### `5 function __escape(s: string, quotes: boolean): string`

```fern
<<5 function __escape(s: string, quotes: boolean): string>>=
function __escape(s: string, quotes: boolean): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < s.len()) {
    let c: i32 = s[i] as i32;
    if (c == 92) {
      out = out + "\\\\";
    } else if (c == 10) {
      out = out + "\\n";
    } else if (c == 34 && quotes) {
      out = out + "\\\"";
    } else {
      out = out + s.slice_snap(i, i + 1);
    }
    i = i + 1;
  }
  return out;
}
```

### `6 pub struct Registry`

```fern
<<6 pub struct Registry>>=
pub struct Registry { counters: Counter[], gauges: Gauge[], histograms: Histogram[] }
```

### `7 pub function registry(): Registry`

```fern
<<7 pub function registry(): Registry>>=
pub function registry(): Registry {
  return Registry { counters: [], gauges: [], histograms: [] };
}

pub function (r: Registry) with_counter(c: Counter): Registry {
  return Registry { ...r, counters: r.counters.append(c) };
}

pub function (r: Registry) with_gauge(g: Gauge): Registry {
  return Registry { ...r, gauges: r.gauges.append(g) };
}

pub function (r: Registry) with_histogram(h: Histogram): Registry {
  return Registry { ...r, histograms: r.histograms.append(h) };
}

pub function (r: Registry) render(): string {
  let out: string = "";
  let seen: string[] = [];
  let i: i32 = 0;
  while (i < r.counters.len()) {
    let c: Counter = r.counters[i];
    out = out + __head(c.name, c.help, "counter", seen) + __sample(c.name, c.labels, c.value.get().to_string());
    seen = seen.append(c.name);
    i = i + 1;
  }
  i = 0;
  while (i < r.gauges.len()) {
    let g: Gauge = r.gauges[i];
    out = out + __head(g.name, g.help, "gauge", seen) + __sample(g.name, g.labels, g.value.get().to_string());
    seen = seen.append(g.name);
    i = i + 1;
  }
  i = 0;
  while (i < r.histograms.len()) {
    let h: Histogram = r.histograms[i];
    out = out + __head(h.name, h.help, "histogram", seen) + h.render_samples();
    seen = seen.append(h.name);
    i = i + 1;
  }
  return out;
}
```

### `8 function __head(name: string, help: string, kind: string, seen: string[]): string`

```fern
<<8 function __head(name: string, help: string, kind: string, seen: string[]): string>>=
function __head(name: string, help: string, kind: string, seen: string[]): string {
  let i: i32 = 0;
  while (i < seen.len()) {
    if (seen[i] == name) {
      return "";
    }
    i = i + 1;
  }
  return "# HELP " + name + " " + __escape(help, false) + "\n# TYPE " + name + " " + kind + "\n";
}

function __sample(name: string, labels: string, value: string): string {
  if (labels.len() == 0) {
    return name + " " + value + "\n";
  }
  return name + "{" + labels + "} " + value + "\n";
}
```

### `9 pub function (h: Histogram) render_samples(): string`

```fern
<<9 pub function (h: Histogram) render_samples(): string>>=
pub function (h: Histogram) render_samples(): string {
  let out: string = "";
  let running: i64 = 0 as i64;
  let i: i32 = 0;
  while (i <= h.bounds.len()) {
    running = running + h.counts[i].get();
    let le: string = "+Inf";
    if (i < h.bounds.len()) {
      le = h.bounds[i].to_string();
    }
    out = out + __sample(h.name + "_bucket", labels([h.labels, label("le", le)]).drop(__comma(h.labels)), running.to_string());
    i = i + 1;
  }
  return out + __sample(h.name + "_sum", h.labels, h.sum.get().to_string()) + __sample(h.name + "_count", h.labels, h.count.get().to_string());
}
```

### `10 function __comma(labels: string): i32`

```fern
<<10 function __comma(labels: string): i32>>=
function __comma(labels: string): i32 {
  if (labels.len() == 0) {
    return 1;
  }
  return 0;
}
```

### `11 pub struct ServerMetrics`

```fern
<<11 pub struct ServerMetrics>>=
pub struct ServerMetrics { methods: string[], statuses: i32[], durations: Histogram[], accepted: Counter, open: Gauge }
```

### `12 pub function server_metrics(): ServerMetrics`

```fern
<<12 pub function server_metrics(): ServerMetrics>>=
pub function server_metrics(): ServerMetrics {
  return ServerMetrics { methods: [], statuses: [], durations: [], accepted: counter("http_server_connections_accepted_total", "Connections accepted."), open: gauge("http_server_open_connections", "Connections open.") };
}

pub function (m: ServerMetrics) request(method: string, status: i32, ns: i64): ServerMetrics {
  let k: i32 = 0;
  while (k < m.methods.len()) {
    if (m.statuses[k] == status && m.methods[k] == method) {
      m.durations[k].observe(__seconds(ns));
      return m;
    }
    k = k + 1;
  }
  let h: Histogram = histogram("http_server_request_duration_seconds", "Duration of HTTP server requests.", duration_buckets()).with_labels(labels([label("http_request_method", method), label("http_response_status_code", status.to_string())]));
  h.observe(__seconds(ns));
  return ServerMetrics { ...m, methods: m.methods.append(method), statuses: m.statuses.append(status), durations: m.durations.append(h) };
}
```

### `13 function __seconds(ns: i64): f64`

```fern
<<13 function __seconds(ns: i64): f64>>=
function __seconds(ns: i64): f64 {
  if (ns < 0 as i64) {
    return 0.0;
  }
  return ns as f64 / 1000000000.0;
}
```

### `14 pub function (m: ServerMetrics) render(): string`

```fern
<<14 pub function (m: ServerMetrics) render(): string>>=
pub function (m: ServerMetrics) render(): string {
  let r: Registry = registry().with_counter(m.accepted).with_gauge(m.open);
  let k: i32 = 0;
  while (k < m.durations.len()) {
    r = r.with_histogram(m.durations[k]);
    k = k + 1;
  }
  return r.render();
}
```

### `15 pub const EOF: string`

```fern
<<15 pub const EOF: string>>=
pub const EOF: string = "# EOF\n";
```

### `16 pub function frames(buf: string): (string[], string)`

```fern
<<16 pub function frames(buf: string): (string[], string)>>=
pub function frames(buf: string): (string[], string) {
  let out: string[] = [];
  let rest: string = buf;
  while (true) {
    let Some(at) = rest.index_of(EOF) else {
      break;
    };
    out = out.append(rest.take(at));
    rest = rest.drop(at + EOF.len());
  }
  return (out, rest);
}
```

### `17 struct __Merged`

```fern
<<17 struct __Merged>>=
struct __Merged { names: string[], heads: string[], keys: string[], ints: i64[], floats: f64[], is_float: boolean[] }
```

### `18 pub function merge(parts: string[]): string`

```fern
<<18 pub function merge(parts: string[]): string>>=
pub function merge(parts: string[]): string {
  let m: __Merged = __Merged { names: [], heads: [], keys: [], ints: [], floats: [], is_float: [] };
  let i: i32 = 0;
  while (i < parts.len()) {
    let lines: string[] = parts[i].split("\n");
    let j: i32 = 0;
    while (j < lines.len()) {
      m = __merge_line(m, lines[j]);
      j = j + 1;
    }
    i = i + 1;
  }
  return __merged_text(m);
}
```

### `19 function __merge_line(m: __Merged, line: string): __Merged`

```fern
<<19 function __merge_line(m: __Merged, line: string): __Merged>>=
function __merge_line(m: __Merged, line: string): __Merged {
  if (line.len() == 0) {
    return m;
  }
  if (line.starts_with("# ")) {
    return __merge_head(m, line);
  }
  let Some(sp) = line.last_index_of(" ") else {
    return m;
  };
  if (sp == 0) {
    return m;
  }
  return __merge_sample(m, line.take(sp), line.drop(sp + 1));
}

function __merge_head(m: __Merged, line: string): __Merged {
  let words: string[] = line.split(" ");
  if (words.len() < 3 || words[1] != "HELP" && words[1] != "TYPE") {
    return m;
  }
  let name: string = words[2];
  let at: i32 = __index_of_str(m.names, name);
  if (at < 0) {
    return __Merged { ...m, names: m.names.append(name), heads: m.heads.append(line + "\n") };
  }
  if (m.heads[at].contains("# " + words[1] + " ")) {
    return m;
  }
  return __Merged { ...m, heads: m.heads.with(at, m.heads[at] + line + "\n") };
}

function __merge_sample(m: __Merged, key: string, value: string): __Merged {
  let at: i32 = __index_of_str(m.keys, key);
  if (at < 0) {
    m = __Merged { ...m, keys: m.keys.append(key), ints: m.ints.append(0 as i64), floats: m.floats.append(0.0), is_float: m.is_float.append(false) };
    at = m.keys.len() - 1;
  }
  if (__is_integer(value)) {
    return __Merged { ...m, ints: m.ints.with(at, m.ints[at] + __int_of(value)) };
  }
  let f: f64 = 0.0;
  match (f64.parse_f64(value)) {
    Ok(x) => {
      f = x;
    },
    Err(_) => {
      return m;
    }
  }
  return __Merged { ...m, floats: m.floats.with(at, m.floats[at] + f), is_float: m.is_float.with(at, true) };
}

function __is_integer(s: string): boolean {
  if (s.len() == 0) {
    return false;
  }
  let i: i32 = 0;
  if (s[0] as i32 == 45) {
    i = 1;
  }
  while (i < s.len()) {
    let c: i32 = s[i] as i32;
    if (c < 48 || c > 57) {
      return false;
    }
    i = i + 1;
  }
  return i > 0;
}

function __int_of(s: string): i64 {
  let n: i64 = 0 as i64;
  let i: i32 = 0;
  let neg: boolean = s[0] as i32 == 45;
  if (neg) {
    i = 1;
  }
  while (i < s.len()) {
    n = n * (10 as i64) + (s[i] as i32 - 48) as i64;
    i = i + 1;
  }
  if (neg) {
    return 0 as i64 - n;
  }
  return n;
}

function __index_of_str(xs: string[], x: string): i32 {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == x) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __merged_text(m: __Merged): string {
  let out: string = "";
  let placed: boolean[] = [];
  let k: i32 = 0;
  while (k < m.keys.len()) {
    placed = placed.append(false);
    k = k + 1;
  }
  let i: i32 = 0;
  while (i < m.names.len()) {
    out = out + m.heads[i];
    k = 0;
    while (k < m.keys.len()) {
      if (!placed[k] && __family_of(m.keys[k]) == m.names[i]) {
        out = out + __merged_sample(m, k);
        placed = placed.with(k, true);
      }
      k = k + 1;
    }
    i = i + 1;
  }
  k = 0;
  while (k < m.keys.len()) {
    if (!placed[k]) {
      out = out + __merged_sample(m, k);
    }
    k = k + 1;
  }
  return out;
}
```

### `20 function __merged_sample(m: __Merged, k: i32): string`

```fern
<<20 function __merged_sample(m: __Merged, k: i32): string>>=
function __merged_sample(m: __Merged, k: i32): string {
  if (m.is_float[k]) {
    return m.keys[k] + " " + (m.floats[k] + m.ints[k] as f64).to_string() + "\n";
  }
  return m.keys[k] + " " + m.ints[k].to_string() + "\n";
}

function __family_of(key: string): string {
  let name: string = key;
  if let Some(brace) = key.index_of("{") {
    name = key.take(brace);
  }
  if (name.ends_with("_bucket")) {
    return name.take(name.len() - 7);
  }
  if (name.ends_with("_sum")) {
    return name.take(name.len() - 4);
  }
  if (name.ends_with("_count")) {
    return name.take(name.len() - 6);
  }
  return name;
}
```

### `21 pub struct Exporter`

```fern
<<21 pub struct Exporter>>=
pub struct Exporter { listener: i32, scrapes: i32[], heads: string[] }
```

### `22 pub function listen[D: async.Driver](drv: D, port: i32): Result[Exporter, net.NetError]`

```fern
<<22 pub function listen[D: async.Driver](drv: D, port: i32): Result[Exporter, net.NetError]>>=
pub function listen[D: async.Driver](drv: D, port: i32): Result[Exporter, net.NetError] {
  let fd: i32 = __listen_any(port);
  if (fd < 0) {
    return Err(net.error_from_host(fd));
  }
  tcp_socket_ctl(fd, 3, 1);
  let watched: i32 = drv.watch(fd, 1);
  if (watched < 0) {
    tcp_close(fd);
    return Err(net.error_from_host(watched));
  }
  return Ok(Exporter { listener: fd, scrapes: [], heads: [] });
}
```

### `23 function __listen_any(port: i32): i32`

```fern
<<23 function __listen_any(port: i32): i32>>=
function __listen_any(port: i32): i32 {
  let v6: u8[] = [0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8];
  let both: i32 = tcp_listen_with(v6, port, 16, false);
  if (both >= 0 || !net.error_from_host(both).ipv6_unavailable()) {
    return both;
  }
  let v4: u8[] = [0 as u8, 0 as u8, 0 as u8, 0 as u8];
  return tcp_listen_with(v4, port, 16, false);
}
```

### `24 pub function (e: Exporter) port(): i32`

```fern
<<24 pub function (e: Exporter) port(): i32>>=
pub function (e: Exporter) port(): i32 {
  return tcp_local_port(e.listener);
}

pub function (e: Exporter) owns(fd: i32): boolean {
  return fd == e.listener || __index_of_i32(e.scrapes, fd) >= 0;
}
```

### `25 function __index_of_i32(xs: i32[], x: i32): i32`

```fern
<<25 function __index_of_i32(xs: i32[], x: i32): i32>>=
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

### `26 pub function (e: Exporter) wake[D: async.Driver](drv: D, fd: i32, answer: (string)`

```fern
<<26 pub function (e: Exporter) wake[D: async.Driver](drv: D, fd: i32, answer: (string)>>=
pub function (e: Exporter) wake[D: async.Driver](drv: D, fd: i32, answer: (string) => HttpResponse): Exporter {
  if (fd == e.listener) {
    return e.accept(drv);
  }
  let at: i32 = __index_of_i32(e.scrapes, fd);
  if (at < 0) {
    return e;
  }
  let got: u8[] = [];
  match (net.read_up_to(fd, 4096)) {
    Ok(bytes) => {
      got = bytes;
    },
    Err(err) => {
      if (err.eq(net.WouldBlock)) {
        return e;
      }
      drv.unwatch(fd);
      tcp_close(fd);
      return Exporter { ...e, scrapes: __without_i32(e.scrapes, at), heads: __without_str(e.heads, at) };
    }
  }
  let head: string = e.heads[at] + string_from_bytes_unchecked(got);
  if (got.len() > 0 && !head.contains("\r\n\r\n") && head.len() < 8192) {
    return Exporter { ...e, heads: e.heads.with(at, head) };
  }
  __answer(fd, head, answer);
  drv.unwatch(fd);
  tcp_close(fd);
  return Exporter { ...e, scrapes: __without_i32(e.scrapes, at), heads: __without_str(e.heads, at) };
}
```

### `27 function (e: Exporter) accept[D: async.Driver](drv: D): Exporter`

```fern
<<27 function (e: Exporter) accept[D: async.Driver](drv: D): Exporter>>=
function (e: Exporter) accept[D: async.Driver](drv: D): Exporter {
  let fd: i32 = tcp_accept(e.listener);
  if (fd < 0) {
    return e;
  }
  tcp_socket_ctl(fd, 3, 1);
  if (drv.watch(fd, 1) < 0) {
    tcp_close(fd);
    return e;
  }
  return Exporter { ...e, scrapes: e.scrapes.append(fd), heads: e.heads.append("") };
}

function __answer(fd: i32, head: string, answer: (string) => HttpResponse): void {
  let resp: HttpResponse = http.text(400, "");
  match (__path_of(head)) {
    Some(path) => {
      resp = answer(path);
    },
    None => {}
  }
  let sent: Result[i32, net.NetError] = net.write(fd, http.http_serialize_response_conn(resp, false).bytes());
}

function __path_of(head: string): Option[string] {
  let Some(eol) = head.index_of("\r\n") else {
    return None;
  };
  let words: string[] = head.take(eol).split(" ");
  if (words.len() != 3 || !words[2].starts_with("HTTP/") || !words[1].starts_with("/")) {
    return None;
  }
  if let Some(q) = words[1].index_of("?") {
    return Some(words[1].take(q));
  }
  return Some(words[1]);
}
```

### `28 pub function (e: Exporter) close[D: async.Driver](drv: D): void`

```fern
<<28 pub function (e: Exporter) close[D: async.Driver](drv: D): void>>=
pub function (e: Exporter) close[D: async.Driver](drv: D): void {
  let i: i32 = 0;
  while (i < e.scrapes.len()) {
    drv.unwatch(e.scrapes[i]);
    tcp_close(e.scrapes[i]);
    i = i + 1;
  }
  drv.unwatch(e.listener);
  tcp_close(e.listener);
}
```

### `29 function __without_i32(xs: i32[], at: i32): i32[]`

```fern
<<29 function __without_i32(xs: i32[], at: i32): i32[]>>=
function __without_i32(xs: i32[], at: i32): i32[] {
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

function __without_str(xs: string[], at: i32): string[] {
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
```

### `30 pub function exposition(body: string): HttpResponse`

```fern
<<30 pub function exposition(body: string): HttpResponse>>=
pub function exposition(body: string): HttpResponse {
  return http.text(200, body).with_header("Content-Type", "text/plain; version=0.0.4; charset=utf-8").with_header("Cache-Control", "no-store");
}

pub function scrape(path: string, body: () => string, ready: boolean): HttpResponse {
  if (path == "/metrics") {
    return exposition(body());
  }
  if (path == "/healthz") {
    return http.text(200, "ok\n").with_header("Cache-Control", "no-store");
  }
  if (path == "/readyz") {
    if (ready) {
      return http.text(200, "ready\n").with_header("Cache-Control", "no-store");
    }
    return http.text(503, "stopping\n").with_header("Cache-Control", "no-store");
  }
  return http.text(404, "");
}
```
