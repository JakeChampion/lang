# Recorded platform operations

This module provides a platform that records operations for tests.

## Model

A mock platform has two parts. Its configuration holds the canned answers, and its call log records each operation with its arguments.
The configuration is immutable. Each `with_*` builder returns a new mock, and the builders take the same argument types as the `platform.Host` builders.
Mocks that a builder derives from one `new()` share one call log.

## Contracts

A test controls environment, configuration, secret, clock, file, and network values without a change to the host process.
An operation without a canned answer returns a fixed value: no environment value, a zero clock, and a refused connection for `http`.
`with_http` takes the complete `Result` of the request, so a test can return any status, headers, and body, or any `fetch.FetchError`.
The call log records each argument verbatim. A secret is recorded by its name, and not by its value.

## Executable example

```fern test name=std-mock-platform-example
import "std/mock_platform";
import "std/platform";
import "std/fetch";
import "std/net";
import "std/option";

function region(plat: platform.Platform): string {
  return plat.env("REGION").unwrap_or("nowhere");
}

function main(): i32 {
  let m: mock_platform.MockPlatform = mock_platform.new().with_env("REGION", "eu-west").with_http("GET", "http://up.test/", Err(fetch.Timeout(fetch.Connecting)));
  if (region(m) != "eu-west") { return 1; }
  match (m.http(fetch.get("http://up.test/"))) {
    Ok(_) => { return 2; },
    Err(e) => {
      if (e.message() != fetch.Timeout(fetch.Connecting).message()) { return 3; }
    }
  }
  m.log("two\nlines");
  if (m.call_count() != 3 || m.calls()[2].args != "two\nlines") { return 4; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct MockPlatform>>

<<4 pub function new(): MockPlatform>>

<<5 impl platform.Platform for MockPlatform>>

<<6 pub function (m: MockPlatform) record(name: string, args: string): void>>

<<7 pub function (m: MockPlatform) materialize(resp: HttpResponse): HttpResponse>>

<<8 function __materialized(m: MockPlatform, resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): H...>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides a platform that records operations for tests.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/platform";
import "std/fetch";
import "std/time";
import "std/net";
import "std/http/trace";
import "std/http";
```

### `3 pub struct MockPlatform`

```fern
<<3 pub struct MockPlatform>>=
pub struct MockPlatform { sink: Cell[MockCall[]], envs: Map[string, string], configs: Map[string, string], secrets: Map[string, string], wall: time.Timestamp, mono: Instant, random: i32, answers: Map[string, Result[HttpResponse, fetch.FetchError]], files: Map[string, MockFile], dirs: Map[string, boolean], peer: Option[net.SocketAddr], deadline: Option[Instant], trace: Option[trace.Context], cancelled: boolean }

pub struct MockCall { name: string, args: string }

pub struct MockFile { content: string, mtime: time.Timestamp }
```

### `4 pub function new(): MockPlatform`

```fern
<<4 pub function new(): MockPlatform>>=
pub function new(): MockPlatform {
  let none: MockCall[] = [];
  return MockPlatform { sink: cell_new(none), envs: map_new(1), configs: map_new(1), secrets: map_new(1), wall: time.Timestamp { sec: 0, nsec: 0 }, mono: time.instant_from_nanos(0), random: 0, answers: map_new(1), files: map_new(1), dirs: map_new(1), peer: None, deadline: None, trace: None, cancelled: false };
}

pub function (m: MockPlatform) with_env(name: string, value: string): MockPlatform {
  return MockPlatform { ...m, envs: m.envs.insert(name, value) };
}

pub function (m: MockPlatform) with_config(name: string, value: string): MockPlatform {
  return MockPlatform { ...m, configs: m.configs.insert(name, value) };
}

pub function (m: MockPlatform) with_secret(name: string, value: string): MockPlatform {
  return MockPlatform { ...m, secrets: m.secrets.insert(name, value) };
}

pub function (m: MockPlatform) with_now(t: time.Timestamp): MockPlatform {
  return MockPlatform { ...m, wall: t };
}

pub function (m: MockPlatform) with_instant(at: Instant): MockPlatform {
  return MockPlatform { ...m, mono: at };
}

pub function (m: MockPlatform) with_random(v: i32): MockPlatform {
  return MockPlatform { ...m, random: v };
}

// `outcome` answers each request with this method and URL.
pub function (m: MockPlatform) with_http(method: string, url: string, outcome: Result[HttpResponse, fetch.FetchError]): MockPlatform {
  return MockPlatform { ...m, answers: m.answers.insert(method + " " + url, outcome) };
}

pub function (m: MockPlatform) with_file(path: string, content: string, mtime: time.Timestamp): MockPlatform {
  return MockPlatform { ...m, files: m.files.insert(path, MockFile { content: content, mtime: mtime }) };
}

pub function (m: MockPlatform) with_dir(path: string): MockPlatform {
  return MockPlatform { ...m, dirs: m.dirs.insert(path, true) };
}

pub function (m: MockPlatform) with_peer(addr: Option[net.SocketAddr]): MockPlatform {
  return MockPlatform { ...m, peer: addr };
}

pub function (m: MockPlatform) with_deadline(at: Instant): MockPlatform {
  return MockPlatform { ...m, deadline: Some(at) };
}

pub function (m: MockPlatform) with_trace(span: trace.Context): MockPlatform {
  return MockPlatform { ...m, trace: Some(span) };
}

pub function (m: MockPlatform) with_cancelled(cancelled: boolean): MockPlatform {
  return MockPlatform { ...m, cancelled: cancelled };
}
```

### `5 impl platform.Platform for MockPlatform`

```fern
<<5 impl platform.Platform for MockPlatform>>=
impl platform.Platform for MockPlatform {
  function log(self: MockPlatform, msg: string): void {
    self.record("log", msg);
  }

  function now(self: MockPlatform): time.Timestamp {
    self.record("now", "");
    return self.wall;
  }

  function instant(self: MockPlatform): Instant {
    self.record("instant", "");
    return self.mono;
  }

  function env(self: MockPlatform, name: string): Option[string] {
    self.record("env", name);
    return self.envs.get(name);
  }

  function config(self: MockPlatform, name: string): Option[string] {
    self.record("config", name);
    return self.configs.get(name);
  }

  function secret(self: MockPlatform, name: string): Option[string] {
    self.record("secret", name);
    return self.secrets.get(name);
  }

  function random_i32(self: MockPlatform): i32 {
    self.record("random_i32", "");
    return self.random;
  }

  function sleep(self: MockPlatform, d: Duration): void {
    self.record("sleep", d.as_millis().to_string());
  }

  function http(self: MockPlatform, req: fetch.Request): Result[HttpResponse, fetch.FetchError] {
    let summary: string = req.method + " " + req.url;
    self.record("http", summary);
    match (self.answers.get(summary)) {
      Some(outcome) => {
        return outcome;
      },
      None => {
        return Err(fetch.Connect(net.ConnectionRefused));
      }
    }
    return Err(fetch.Connect(net.ConnectionRefused));
  }

  function stat(self: MockPlatform, path: string): Result[FileStat, IoError] {
    self.record("stat", path);
    match (self.files.get(path)) {
      Some(f) => {
        return Ok(__file_stat(false, f.content.len() as i64, f.mtime));
      },
      None => {}
    }
    if (self.dirs.contains(path)) {
      return Ok(__file_stat(true, 0 as i64, time.Timestamp { sec: 0, nsec: 0 }));
    }
    return Err(NotFound(path));
  }

  function deadline(self: MockPlatform): Option[Instant] {
    self.record("deadline", "");
    return self.deadline;
  }

  function trace(self: MockPlatform): Option[trace.Context] {
    self.record("trace", "");
    return self.trace;
  }

  function cancelled(self: MockPlatform): boolean {
    self.record("cancelled", "");
    return self.cancelled;
  }

  function peer(self: MockPlatform): Option[net.SocketAddr] {
    self.record("peer", "");
    return self.peer;
  }
}
```

### `6 pub function (m: MockPlatform) record(name: string, args: string): void`

```fern
<<6 pub function (m: MockPlatform) record(name: string, args: string): void>>=
pub function (m: MockPlatform) record(name: string, args: string): void {
  m.sink.set(m.sink.get().append(MockCall { name: name, args: args }));
}

pub function (m: MockPlatform) calls(): MockCall[] {
  return m.sink.get();
}

pub function (m: MockPlatform) call_count(): i32 {
  return m.sink.get().len();
}

// The configuration stays. Only the call log is cleared.
pub function (m: MockPlatform) reset(): void {
  let none: MockCall[] = [];
  m.sink.set(none);
}

pub function (m: MockPlatform) has_call(name: string): boolean {
  match (m.find_call(name)) {
    Some(c) => {
      return true;
    },
    None => {
      return false;
    }
  }
  return false;
}

pub function (m: MockPlatform) find_call(name: string): Option[MockCall] {
  for c in m.sink.get() {
    if (c.name == name) {
      return Some(c);
    }
  }
  return None;
}
```

### `7 pub function (m: MockPlatform) materialize(resp: HttpResponse): HttpResponse`

```fern
<<7 pub function (m: MockPlatform) materialize(resp: HttpResponse): HttpResponse>>=
pub function (m: MockPlatform) materialize(resp: HttpResponse): HttpResponse {
  match (resp.body) {
    BodyFile(path) => {
      match (m.files.get(path)) {
        Some(f) => {
          return HttpResponse { ...resp, body: BodyBytes(f.content.bytes()) };
        },
        None => {
          return http.not_found();
        }
      }
      return http.not_found();
    },
    BodyFileRanges(path, ranges, seams) => {
      return __materialized(m, resp, path, ranges, seams);
    },
    _ => {
      return resp;
    }
  }
  return resp;
}
```

### `8 function __materialized(m: MockPlatform, resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): H...`

```fern
<<8 function __materialized(m: MockPlatform, resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): H...>>=
function __materialized(m: MockPlatform, resp: HttpResponse, path: string, ranges: i64[], seams: u8[][]): HttpResponse {
  let content: string = "";
  match (m.files.get(path)) {
    Some(f) => {
      content = f.content;
    },
    None => {
      return http.not_found();
    }
  }
  match (http.file_ranges_bytes(ranges, seams, (offset: i64, len: i64): Option[u8[]] => __canned_range(content, offset, len))) {
    Some(bs) => {
      return HttpResponse { ...resp, body: BodyBytes(bs) };
    },
    None => {
      return http.not_found();
    }
  }
  return http.not_found();
}

function __canned_range(content: string, offset: i64, len: i64): Option[u8[]] {
  if (offset + len > content.len() as i64) {
    return None;
  }
  return Some((slice_unchecked(content, offset as i32, (offset + len) as i32) + "").bytes());
}

function __file_stat(is_dir: boolean, size: i64, mtime: time.Timestamp): FileStat {
  let z: i64 = 0 as i64;
  let u: u32 = 0 as u32;
  let sec: i64 = mtime.sec;
  let nsec: i64 = mtime.nsec as i64;
  return FileStat { is_file: !is_dir, is_dir: is_dir, size: size, mode: u, nlink: 1 as u32, uid: u, gid: u, dev: z, rdev: z, ino: z, blksize: z, blocks: z, atime: sec, atime_nsec: nsec, mtime: sec, mtime_nsec: nsec, ctime: sec, ctime_nsec: nsec, btime: sec, btime_nsec: nsec };
}
```
