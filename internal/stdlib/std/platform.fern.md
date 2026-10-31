# Handler platform capabilities

This module defines platform capabilities for handlers.

## Model

The Platform trait groups operations that a handler obtains from its environment. A Host value adds connection, request, and client state to those capabilities.

## Contracts

Host configuration carries optional peer, deadline, cookie, access, and trace values. Tests can supply another trait implementation instead of host effects.
`now` reads the wall clock as a `time.Timestamp`. `instant` reads the monotonic clock, and a deadline is an `Instant` on that clock.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub trait Platform>>

<<4 pub function host(): Host>>

<<5 impl Platform for Host>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines platform capabilities for handlers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/fetch";
import "std/http/cookie";
import "std/async";
import "std/time";
import "std/clock";
import "std/net";
import "std/http/trace";
```

### `3 pub trait Platform`

```fern
<<3 pub trait Platform>>=
pub trait Platform {
  function log(self: Self, msg: string): void;

  function now(self: Self): time.Timestamp;

  function instant(self: Self): Instant;

  function env(self: Self, name: string): Option[string];

  function config(self: Self, name: string): Option[string];

  function secret(self: Self, name: string): Option[string];

  function random_i32(self: Self): i32;

  function sleep(self: Self, d: Duration): void;

  function http(self: Self, req: fetch.Request): Result[HttpResponse, fetch.FetchError];

  function stat(self: Self, path: string): Result[FileStat, IoError];

  function peer(self: Self): Option[net.SocketAddr];

  function deadline(self: Self): Option[Instant];

  function cancelled(self: Self): boolean;

  function trace(self: Self): Option[trace.Context];
}

pub struct Host { reactor: i32, pool: fetch.Idle, jar: Option[cookie.Jar], allow: Option[fetch.Allow], peer: Option[net.SocketAddr], deadline: Option[Instant], trace: Option[trace.Context] }
```

### `4 pub function host(): Host`

```fern
<<4 pub function host(): Host>>=
pub function host(): Host {
  return host_on(0);
}

pub function host_on(reactor: i32): Host {
  return Host { reactor: reactor, pool: fetch.idle(), jar: None, allow: None, peer: None, deadline: None, trace: None };
}

pub function (h: Host) with_pool(pool: fetch.Idle): Host {
  return Host { ...h, pool: pool };
}

pub function (h: Host) with_peer(addr: Option[net.SocketAddr]): Host {
  return Host { ...h, peer: addr };
}

pub function (h: Host) with_jar(jar: cookie.Jar): Host {
  return Host { ...h, jar: Some(jar) };
}

pub function (h: Host) with_allow(a: fetch.Allow): Host {
  return Host { ...h, allow: Some(a) };
}

pub function (h: Host) with_deadline(at: Instant): Host {
  return Host { ...h, deadline: Some(at) };
}

pub function (h: Host) with_trace(span: trace.Context): Host {
  return Host { ...h, trace: Some(span) };
}

pub function traced(req: fetch.Request, span: Option[trace.Context], random: () => i32): fetch.Request {
  match (span) {
    Some(c) => {
      if (req.headers.has("traceparent")) {
        return req;
      }
      return fetch.Request { ...req, headers: c.child(random).inject(req.headers) };
    },
    None => {
      return req;
    }
  }
  return req;
}

pub function passed(deadline: Option[Instant], now: Instant): boolean {
  match (deadline) {
    Some(at) => {
      return now.cmp(at) >= 0;
    },
    None => {
      return false;
    }
  }
  return false;
}
```

### `5 impl Platform for Host`

```fern
<<5 impl Platform for Host>>=
impl Platform for Host {
  function log(self: Host, msg: string): void {
    eprint(msg);
  }

  function now(self: Host): time.Timestamp {
    return clock.now();
  }

  function instant(self: Host): Instant {
    return clock.instant();
  }

  function env(self: Host, name: string): Option[string] {
    return env(name);
  }

  function config(self: Host, name: string): Option[string] {
    return config_get(name);
  }

  function secret(self: Host, name: string): Option[string] {
    return config_get(name);
  }

  function random_i32(self: Host): i32 {
    return random_i32();
  }

  function sleep(self: Host, d: Duration): void {
    let none: i32[] = [];
    let woke: async.Wake = async.wait_any(none, Some(clock.instant().add(d)));
  }

  function http(self: Host, req: fetch.Request): Result[HttpResponse, fetch.FetchError] {
    return fetch.send_on(fetch.sockets_with(self.pool), traced(req, self.trace, (): i32 => random_i32()), fetch.Policy { ...fetch.public_policy_via(self.pool.proxy_env()), jar: self.jar, allow: self.allow });
  }

  function stat(self: Host, path: string): Result[FileStat, IoError] {
    return stat(path);
  }

  function peer(self: Host): Option[net.SocketAddr] {
    return self.peer;
  }

  function deadline(self: Host): Option[Instant] {
    return self.deadline;
  }

  function cancelled(self: Host): boolean {
    return async.task_cancelled() || passed(self.deadline, clock.instant());
  }

  function trace(self: Host): Option[trace.Context] {
    return self.trace;
  }
}
```
