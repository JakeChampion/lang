# Pulse: measure the process that serves the page

Pulse displays measurements from this Fern process. It never generates synthetic memory or request values.
The dashboard uses SSE and falls back to periodic HTTP requests when an event stream fails.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Metric types>>
<<Record HTTP requests>>
<<Read Linux counters>>
<<Sample and cache>>
<<Expose application counters>>
<<Stream and export>>
```

## Metric types

An unavailable measurement uses `Option` and becomes JSON null. Zero remains a real measured value.
All byte fields use bytes. The UI chooses readable units.

```fern
<<Metric types>>=
// Real process measurements. Missing OS fields remain absent, rather than zero.
import "std/http";
import "std/http/sse";
import "std/json";
import "std/parse";
import "std/option";
import "std/result";
import "std/array";
import "std/time";
import "std/async";
import "./common";
import "./mosaic";
import "./rally";
import "../fractal/server" as fractal;

@derive(json.Json)
pub struct Memory {
  rss: Option[i64],
  peak_rss: Option[i64],
  virtual_bytes: Option[i64],
  anonymous: Option[i64],
  file_backed: Option[i64],
  swap: Option[i64],
  threads: Option[i64],
  container_bytes: Option[i64],
  container_limit: Option[i64],
  cpu_percent: Option[f64],
}

@derive(json.Json)
pub struct Request { method: string, path: string, status: i32, elapsed_us: i64, at_ms: i64 }

@derive(json.Json)
pub struct Snapshot {
  instance: string,
  at_ms: i64,
  uptime_ms: i64,
  requests: i64,
  client_errors: i64,
  server_errors: i64,
  allocations_total: i64,
  arena_bytes: i64,
  memory: Memory,
  recent: Request[],
  game_rooms: i32,
  canvas_rooms: i32,
  strokes: i64,
  websocket_messages: i64,
  websocket_connections: i64,
  rendered_tiles: i64,
  active_renders: i64,
}

pub struct State {
  instance: string,
  started: i64,
  requests: Cell[i64],
  client_errors: Cell[i64],
  server_errors: Cell[i64],
  recent: Cell[Request[]],
  sampled: Cell[i64],
  ticks: Cell[Option[i64]],
  memory: Cell[Option[Memory]],
  canvases: mosaic.State,
  games: rally.State,
  fractal: fractal.Stats,
}
```

## Record HTTP requests

The middleware counts responses and keeps only the last 32 requests. It omits query strings and request bodies.
Handler time ends when a handler returns a response. It does not include the lifetime of an SSE or WebSocket stream.

```fern
<<Record HTTP requests>>=
pub function state(canvases: mosaic.State, games: rally.State, fractals: fractal.Stats): State {
  return State {
    instance: fractals.instance, started: now_ns(), requests: cell_new(0 as i64), client_errors: cell_new(0 as i64), server_errors: cell_new(0 as i64),
    recent: cell_new([] as Request[]), sampled: cell_new(0 as i64), ticks: cell_new(None as Option[i64]), memory: cell_new(None as Option[Memory]), canvases: canvases, games: games, fractal: fractals,
  };
}

pub function record(state: State, req: HttpRequest, response: HttpResponse, started: i64): void {
  state.requests.set(state.requests.get() + 1);
  if (response.status >= 400 && response.status < 500) {
    state.client_errors.set(state.client_errors.get() + 1);
  }
  if (response.status >= 500) {
    state.server_errors.set(state.server_errors.get() + 1);
  }
  let recent: Request[] = state.recent.get().append(Request { method: req.method, path: common.path(req).take(120), status: response.status, elapsed_us: (now_ns() - started) / 1000, at_ms: now_unix_ms() });
  if (recent.len() > 32) {
    recent = recent.drop(1);
  }
  state.recent.set(recent);
}
```

## Read Linux counters

Procfs exports memory in KiB. Multiply only after an overflow check.
The process name in `/proc/self/stat` can contain spaces and parentheses. Find the last closing parenthesis first.
Linux exports CPU time at 100 ticks per second. Compare successive samples for CPU percent.

```fern
<<Read Linux counters>>=
pub function status_number(status: string, name: string, multiplier: i64): Option[i64] {
  for line in status.split("\n") {
    let parts: string[] = line.splitn(":", 2);
    if (parts.len() == 2 && parts[0] == name) {
      let value: i64 = parse.int64(parts[1].trim().splitn(" ", 2)[0]).ok()?;
      if (value < 0 || value > 9223372036854775807 / multiplier) {
        return None;
      }
      return Some(value * multiplier);
    }
  }
  return None;
}

// Linux exports user and system CPU time in USER_HZ units of 100 ticks/second.
pub function cpu_ticks(stat: string): Option[i64] {
  let end: Option[i32] = None;
  let at: i32 = 0;
  while (at < stat.len()) {
    if (stat[at] == b')') {
      end = Some(at);
    }
    at = at + 1;
  }
  let close: i32 = end?;
  let fields: string[] = stat.drop(close + 1).trim().split(" ");
  if (fields.len() < 13) {
    return None;
  }
  let user: i64 = parse.int64(fields[11]).ok()?;
  let system: i64 = parse.int64(fields[12]).ok()?;
  if (user < 0 || system < 0 || user > 9223372036854775807 - system) {
    return None;
  }
  return Some(user + system);
}
```

## Sample and cache

Read RSS, peak RSS, virtual bytes, anonymous bytes, file-backed bytes, swap, and thread count.
Read cgroup v2 files first, with v1 fallback. An unlimited or unavailable memory limit stays absent.
Cache OS samples for half a second so many viewers do not multiply procfs work.

```fern
<<Sample and cache>>=
function file_number(primary: string, fallback: string): Option[i64] {
  let text: string = read_file(primary).unwrap_or_else((e: IoError): string => read_file(fallback).unwrap_or(""));
  let value: i64 = parse.int64(text.trim()).ok()?;
  if (value < 0 || value > 1152921504606846976) {
    return None;
  }
  return Some(value);
}

function measure(state: State): Memory {
  let now: i64 = now_ns();
  if (now - state.sampled.get() < 500000000) {
    if let Some(memory) = state.memory.get() {
      return memory;
    }
  }
  let status: string = read_file("/proc/self/status").unwrap_or("");
  let ticks: Option[i64] = cpu_ticks(read_file("/proc/self/stat").unwrap_or(""));
  let cpu: Option[f64] = None;
  if let Some(current) = ticks {
    if let Some(previous) = state.ticks.get() {
      if (now > state.sampled.get() && current >= previous) {
        cpu = Some((current - previous) as f64 * 1000000000.0 / ((now - state.sampled.get()) as f64));
      }
    }
  }
  let memory: Memory = Memory {
    rss: status_number(status, "VmRSS", 1024), peak_rss: status_number(status, "VmHWM", 1024), virtual_bytes: status_number(status, "VmSize", 1024),
    anonymous: status_number(status, "RssAnon", 1024), file_backed: status_number(status, "RssFile", 1024), swap: status_number(status, "VmSwap", 1024), threads: status_number(status, "Threads", 1),
    container_bytes: file_number("/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory/memory.usage_in_bytes"),
    container_limit: file_number("/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"), cpu_percent: cpu,
  };
  state.ticks.set(ticks);
  state.sampled.set(now);
  state.memory.set(Some(memory));
  return memory;
}
```

## Expose application counters

Combine process counters with game peers, canvas revisions, and fractal activity.
Runtime allocation count is cumulative. Arena high-water bytes measure reserved runtime storage, not live object bytes.
RSS includes resident process pages and can differ from both runtime counters.

```fern
<<Expose application counters>>=
pub function snapshot(state: State): Snapshot {
  let strokes: i64 = 0;
  let peers: i64 = state.fractal.sockets.get();
  for room in state.games.rooms.get() {
    for seat in room.seats {
      if (seat != "") {
        peers = peers + 1;
      }
    }
  }
  for board in state.canvases.rooms.get() {
    strokes = strokes + board.revision;
  }
  return Snapshot {
    instance: state.instance, at_ms: now_unix_ms(), uptime_ms: (now_ns() - state.started) / 1000000,
    requests: state.requests.get(), client_errors: state.client_errors.get(), server_errors: state.server_errors.get(),
    allocations_total: __heap_alloc_count(), arena_bytes: __heap_bump_bytes(), memory: measure(state), recent: state.recent.get(),
    game_rooms: state.games.rooms.get().len(), canvas_rooms: state.canvases.rooms.get().len(), strokes: strokes, websocket_messages: state.games.messages.get(), websocket_connections: peers, rendered_tiles: state.fractal.tiles.get(), active_renders: state.fractal.active.get(),
  };
}
```

## Stream and export

Send a metrics event each second. Task cancellation ends the stream.
The Prometheus endpoint exposes gauges only when measurements exist. Counters retain the `_total` suffix.

```fern
<<Stream and export>>=
pub function events(state: State): HttpResponse {
  return sse.respond_with(time.duration_seconds(15), (index: i32): Option[sse.Event] => {
    if (index > 0) {
      let ready: i32 = async.wait_any([], 1000);
    }
    if (async.task_cancelled()) {
      return None;
    }
    let value: Snapshot = snapshot(state);
    return Some(sse.event(value.to_json()).with_name("metrics").with_id(value.at_ms.to_string()).with_retry(1500));
  }).with_header("X-Accel-Buffering", "no");
}

function gauge(name: string, help: string, value: Option[i64]): string {
  match (value) {
    Some(number) => {
      return "# HELP " + name + " " + help + "\n# TYPE " + name + " gauge\n" + name + " " + number.to_string() + "\n";
    },
    None => {
      return "";
    }
  }
}

pub function prometheus(state: State): HttpResponse {
  let value: Snapshot = snapshot(state);
  let text: string = "# HELP fern_http_requests_total HTTP requests since process start\n# TYPE fern_http_requests_total counter\nfern_http_requests_total " + value.requests.to_string() + "\n"
    + "# HELP fern_heap_allocations_total Runtime allocations since process start\n# TYPE fern_heap_allocations_total counter\nfern_heap_allocations_total " + value.allocations_total.to_string() + "\n";
  text = text + gauge("process_resident_memory_bytes", "Resident memory from procfs", value.memory.rss)
    + gauge("process_virtual_memory_bytes", "Virtual address space from procfs", value.memory.virtual_bytes)
    + gauge("fern_heap_arena_high_water_bytes", "Runtime arena high-water mark, not live heap bytes", Some(value.arena_bytes))
    + gauge("fern_container_memory_bytes", "Memory used by this cgroup", value.memory.container_bytes)
    + gauge("fern_container_memory_limit_bytes", "Memory limit of this cgroup", value.memory.container_limit);
  return http.text(200, text).with_content_type("text/plain; version=0.0.4; charset=utf-8").with_header("Cache-Control", "no-store");
}
```

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=metrics-example
<<Metric types>>
<<Read Linux counters>>
function main(): i32 {
  if (status_number("VmRSS: 12 kB\n", "VmRSS", 1024).unwrap_or(0) != 12288) { return 1; }
  if (status_number("", "VmRSS", 1024).is_some()) { return 2; }
  return 0;
}
```
