# Platform capabilities under simulation

This module provides platform capabilities for deterministic simulations.

## Model

The simulated platform combines a virtual driver, a scripted network, and a recorded mock platform. Request policy can add cookies, access limits, deadlines, and trace context.
The configuration is immutable. Each `with_*` builder returns a new platform, and the builders take the same argument types as the `platform.Host` builders.

## Contracts

The clock, randomness, and network come from the simulation. Environment, configuration, secret, file, and peer values come from the mock platform.
Handler tests can share the same capability interface as host execution without real network effects.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct SimPlatform>>

<<4 pub function new(drv: sim.Sim, net: sim_fetch.Net): SimPlatform>>

<<5 impl platform.Platform for SimPlatform>>

<<6 pub function (p: SimPlatform) materialize(resp: HttpResponse): HttpResponse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides platform capabilities for deterministic simulations.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/platform";
import "std/sim";
import "std/sim_fetch";
import "std/fetch";
import "std/time";
import "std/mock_platform";
import "std/net";
import "std/http/cookie";
import "std/async";
import "std/http/trace";
```

### `3 pub struct SimPlatform`

```fern
<<3 pub struct SimPlatform>>=
pub struct SimPlatform { drv: sim.Sim, net: sim_fetch.Net, mock: mock_platform.MockPlatform, jar: Option[cookie.Jar], allow: Option[fetch.Allow], deadline: Option[Instant], trace: Option[trace.Context] }
```

### `4 pub function new(drv: sim.Sim, net: sim_fetch.Net): SimPlatform`

```fern
<<4 pub function new(drv: sim.Sim, net: sim_fetch.Net): SimPlatform>>=
pub function new(drv: sim.Sim, net: sim_fetch.Net): SimPlatform {
  return SimPlatform { drv: drv, net: net, mock: mock_platform.new(), jar: None, allow: None, deadline: None, trace: None };
}

pub function (p: SimPlatform) with_env(name: string, value: string): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_env(name, value) };
}

pub function (p: SimPlatform) with_config(name: string, value: string): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_config(name, value) };
}

pub function (p: SimPlatform) with_secret(name: string, value: string): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_secret(name, value) };
}

pub function (p: SimPlatform) with_file(path: string, content: string, mtime: time.Timestamp): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_file(path, content, mtime) };
}

pub function (p: SimPlatform) with_dir(path: string): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_dir(path) };
}

pub function (p: SimPlatform) with_peer(addr: Option[net.SocketAddr]): SimPlatform {
  return SimPlatform { ...p, mock: p.mock.with_peer(addr) };
}

pub function (p: SimPlatform) with_jar(jar: cookie.Jar): SimPlatform {
  return SimPlatform { ...p, jar: Some(jar) };
}

pub function (p: SimPlatform) with_allow(a: fetch.Allow): SimPlatform {
  return SimPlatform { ...p, allow: Some(a) };
}

pub function (p: SimPlatform) with_deadline(at: Instant): SimPlatform {
  return SimPlatform { ...p, deadline: Some(at) };
}

pub function (p: SimPlatform) with_trace(span: trace.Context): SimPlatform {
  return SimPlatform { ...p, trace: Some(span) };
}
```

### `5 impl platform.Platform for SimPlatform`

```fern
<<5 impl platform.Platform for SimPlatform>>=
impl platform.Platform for SimPlatform {
  function log(self: SimPlatform, msg: string): void {
    self.mock.record("log", msg);
  }

  function now(self: SimPlatform): time.Timestamp {
    self.mock.record("now", "");
    let d: Duration = time.duration_nanos(self.drv.clock_ns.get());
    return time.Timestamp { sec: d.sec, nsec: d.nsec };
  }

  function instant(self: SimPlatform): Instant {
    self.mock.record("instant", "");
    return self.drv.now();
  }

  function env(self: SimPlatform, name: string): Option[string] {
    self.mock.record("env", name);
    return self.mock.envs.get(name);
  }

  function config(self: SimPlatform, name: string): Option[string] {
    self.mock.record("config", name);
    return self.mock.configs.get(name);
  }

  function secret(self: SimPlatform, name: string): Option[string] {
    self.mock.record("secret", name);
    return self.mock.secrets.get(name);
  }

  function random_i32(self: SimPlatform): i32 {
    self.mock.record("random_i32", "");
    return self.drv.random_i32();
  }

  function sleep(self: SimPlatform, d: Duration): void {
    self.mock.record("sleep", d.as_millis().to_string());
    let moved: boolean = self.drv.advance_to(self.drv.now().add(d));
  }

  function http(self: SimPlatform, req: fetch.Request): Result[HttpResponse, fetch.FetchError] {
    self.mock.record("http", req.method + " " + req.url);
    let drv: sim.Sim = self.drv;
    let e: Map[string, string] = self.mock.envs;
    let proxies: fetch.ProxyEnv = fetch.proxy_env_from(e.get("http_proxy"), e.get("https_proxy"), e.get("HTTPS_PROXY"), e.get("no_proxy"), e.get("NO_PROXY"), e.get("REQUEST_METHOD"));
    return fetch.send_on(self.net, platform.traced(req, self.trace, (): i32 => drv.random_i32()), fetch.Policy { ...fetch.public_policy_via(proxies), jar: self.jar, allow: self.allow });
  }

  function stat(self: SimPlatform, path: string): Result[FileStat, IoError] {
    return self.mock.stat(path);
  }

  function peer(self: SimPlatform): Option[net.SocketAddr] {
    return self.mock.peer();
  }

  function deadline(self: SimPlatform): Option[Instant] {
    self.mock.record("deadline", "");
    return self.deadline;
  }

  function trace(self: SimPlatform): Option[trace.Context] {
    self.mock.record("trace", "");
    return self.trace;
  }

  function cancelled(self: SimPlatform): boolean {
    self.mock.record("cancelled", "");
    return async.task_cancelled() || platform.passed(self.deadline, self.drv.now());
  }
}
```

### `6 pub function (p: SimPlatform) materialize(resp: HttpResponse): HttpResponse`

```fern
<<6 pub function (p: SimPlatform) materialize(resp: HttpResponse): HttpResponse>>=
pub function (p: SimPlatform) materialize(resp: HttpResponse): HttpResponse {
  return p.mock.materialize(resp);
}

pub function (p: SimPlatform) calls(): mock_platform.MockCall[] {
  return p.mock.calls();
}

pub function (p: SimPlatform) reset(): void {
  p.mock.reset();
}
```
