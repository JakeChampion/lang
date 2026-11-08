# An HTTP server over std/tcp

A Fern HTTP handler that boots on a kernel needs a server with the shape of `std/serve` over `std/tcp`, and this module is that server: the fifth slice of the network stack of `docs/BARE-METAL-PLAN.md` (#14259). A `Server` owns a `std/tcp` listener and a handler. It frames the requests of every connection with `std/http`, answers each one, and keeps the connection for the next request when the request allows it. A program moves from `std/serve` to this module by one import and the handler type, which has no `platform.Host`: a kernel has no host.

The server frames with `parse_request_framed_from` of `std/http` under the request limits of `std/http`. It answers a request that expects `100 Continue` with that line, once. It refuses a request that `std/http` cannot frame with the status `std/http` names, and it closes the connection, as `std/serve` does. Pipelined requests on one connection get their answers in order.

The server steps. `step` takes the frames the listener has, answers, and sends. A program that polls calls it in a loop. A program that waits on the IRQ line of the device calls it once for each wake, after it acknowledges the device.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct Server>>

<<stepping>>

<<one connection>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module serves HTTP over std/tcp with the shape of std/serve.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/http";
import "std/option";
import "std/tcp";
```

### `pub struct Server`

A connection keeps the bytes the server has not framed yet, whether the pending request got its `100 Continue`, and the requests it answered.

```fern
<<pub struct Server>>=
struct Conn { buf: u8[], continued: boolean, requests: i32 }

pub struct Server { listener: tcp.Listener, limits: http.HttpLimits, conns: Conn[], handler: (HttpRequest) => HttpResponse, answered: i32 }

pub function new(listener: tcp.Listener, handler: (HttpRequest) => HttpResponse): Server {
  let conns: Conn[] = [];
  for i in 0..tcp.CONNECTIONS {
    conns = conns.append(idle());
  }
  return Server { listener: listener, limits: http.limits(), conns: conns, handler: handler, answered: 0 };
}

pub function (s: Server) with_limits(limits: http.HttpLimits): Server {
  return Server { ...s, limits: limits };
}

// The requests the server answered so far, refusals included.
pub function (s: Server) answered(): i32 {
  return s.answered;
}

// The connections that are open.
pub function (s: Server) active(): i32 {
  return s.listener.active();
}

function idle(): Conn {
  return Conn { buf: [], continued: false, requests: 0 };
}
```

### `stepping`

```fern
<<stepping>>=
pub function (s: Server) step(now: i64): Server {
  let (polled, events) = s.listener.poll(now);
  s = Server { ...s, listener: polled };
  for e in events {
    match (e) {
      Accepted(id) => {
        s = Server { ...s, conns: s.conns.with(id, idle()) };
      },
      Data(id, bytes) => {
        s = s.take(id, bytes);
      },
      Finished(id) => {
        s = s.close(id);
      },
      Closed(id) => {
        s = Server { ...s, conns: s.conns.with(id, idle()) };
      }
    }
  }
  return s;
}

function (s: Server) send(id: i32, bytes: u8[]): Server {
  return Server { ...s, listener: s.listener.write(id, bytes) };
}

function (s: Server) close(id: i32): Server {
  return Server { ...s, listener: s.listener.close(id) };
}
```

### `one connection`

`take` frames the bytes a connection received, as many requests as they hold, and answers each one. The bytes of a request the framing has not completed stay with the connection.

```fern
<<one connection>>=
function (s: Server) take(id: i32, bytes: u8[]): Server {
  let c = s.conns[id];
  let buf = concat(c.buf, bytes);
  let from = 0;
  let continued = c.continued;
  let requests = c.requests;
  let open = true;
  while (open) {
    match (http.parse_request_framed_from(buf, from, s.limits, http.framed_none())) {
      Complete(f) => {
        s = s.answer(id, f);
        from += f.len;
        requests += 1;
        continued = false;
        open = f.keep_alive;
      },
      Incomplete => {
        open = false;
      },
      Continue => {
        if (!continued) {
          s = s.send(id, "HTTP/1.1 100 Continue\r\n\r\n".bytes());
          continued = true;
        }
        open = false;
      },
      Malformed(status) => {
        s = s.refuse(id, status);
        from = buf.len();
        open = false;
      }
    }
  }
  return Server { ...s, conns: s.conns.with(id, Conn { buf: slice(buf, from, buf.len()), continued: continued, requests: requests }) };
}

// Answers one framed request, and closes the connection after the answer
// when the request does not keep it.
function (s: Server) answer(id: i32, f: http.HttpFramed): Server {
  let response = s.handler(f.request);
  s = s.send(id, http.serialize_response_to_bytes(f.request.method, f.version, response, f.keep_alive));
  s = Server { ...s, answered: s.answered + 1 };
  if (f.keep_alive) {
    return s;
  }
  return s.close(id);
}

// Refuses the request with the status the framing named, and closes the
// connection after the answer.
function (s: Server) refuse(id: i32, status: i32): Server {
  s = s.send(id, http.serialize_response_to_bytes("GET", "HTTP/1.1", http.text(status, ""), false));
  return Server { ...s, answered: s.answered + 1 }.close(id);
}

function slice(data: u8[], start: i32, end: i32): u8[] {
  let out: u8[] = [];
  for i in start..end {
    out = out.append(data[i]);
  }
  return out;
}

function concat(a: u8[], b: u8[]): u8[] {
  for x in b {
    a = a.append(x);
  }
  return a;
}
```
