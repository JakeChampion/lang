---
title: Streaming
description: Request bodies read as they arrive, multipart uploads part by part, responses produced chunk by chunk with trailers, files sent with sendfile, Server-Sent Events and WebSockets.
sidebar:
  order: 6
---

By default the server reads a whole request body before calling the
handler, and sends a whole response body after it returns. Both ends can
stream instead, without the handler blocking its worker: every wait is a
park, so the worker serves other connections in the meantime.

## Request bodies as they arrive

Set `stream_bodies: true` and a handler starts as soon as the request's
header block is in. `req.body` is then a `ByteStream` that pulls bytes from
the connection as you read; reads are values, so each returns the bytes
and the stream advanced past them.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function init(): serve.Config {
    let limits: http.HttpLimits = http.HttpLimits { ...http.http_limits(), body: 1024 * 1024 * 1024 };
    return serve.Config { ...serve.config(), stream_bodies: true, limits: limits };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let s: ByteStream = req.body;
    let total: i32 = 0;
    let lines: i32 = 0;
    let going: boolean = true;
    while (going) {
        let got: (u8[], ByteStream) = s.read_n(64 * 1024);
        s = got.1;
        if (got.0.len() == 0) {
            going = false;
        }
        for b in got.0 {
            if (b == 10) {
                lines = lines + 1;
            }
        }
        total = total + got.0.len();
    }
    if (s.fault() != 0) {
        return http.text(s.fault(), "the body did not arrive\n");
    }
    return http.plain(total.to_string() + " bytes, " + lines.to_string() + " lines\n");
}
```

A body that ends early says why in `s.fault()`: `413` past the body
limit, `408` when it trickled in below `min_data_rate`, `400` when its
framing was malformed. The body limit still applies when streaming, which
is why the example raises it to 1 GiB; memory stays at one chunk however
large the upload. `read_some()` returns whatever has arrived, `read_line()` a line
at a time, and `req.body_string()` or `body_bytes()` still read the whole
thing when that is what you want.

## Uploads

`std/http/multipart` reads `multipart/form-data` one part at a time.
Text fields can be read whole; a file part is read in pieces and never
held in memory unless you ask, and every size is capped by
`multipart.limits()`.

```fern
import "std/http";
import "std/serve";
import "std/http/multipart";
import "std/platform";

function init(): serve.Config {
    return serve.Config { ...serve.config(), stream_bodies: true };
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, multipart.MultipartError] {
    let r: multipart.Reader = multipart.open(req, multipart.limits())?;
    let out: string = "";
    let going: boolean = true;
    while (going) {
        match (r.next()?) {
            NextPart(part, rest) => {
                if (part.filename.len() > 0) {
                    let size: i32 = 0;
                    let cur: multipart.Reader = rest;
                    let done: boolean = false;
                    while (!done) {
                        let piece: (u8[], boolean, multipart.Reader) = cur.read(16 * 1024)?;
                        size = size + piece.0.len();   // write piece.0 somewhere here
                        done = piece.1;
                        cur = piece.2;
                    }
                    out = out + part.name + ": " + part.filename + " (" + part.content_type + ", " + size.to_string() + " bytes)\n";
                    r = cur;
                } else {
                    let got: (u8[], multipart.Reader) = rest.content()?;
                    out = out + part.name + " = " + string_from_bytes_unchecked(got.0) + "\n";
                    r = got.1;
                }
            },
            NoMoreParts => { going = false; }
        }
    }
    return Ok(http.plain(out));
}
```

```bash
curl -F title=holiday -F photo=@beach.png localhost:8080/
# title = holiday
# photo: beach.png (image/png, 48213 bytes)
```

`r.next()` skips whatever of the current part was not read. Errors map to
415 (not multipart), 400 (malformed), 413 (a part or the whole too large)
or 431 (part headers too large). When only the text fields matter,
`multipart.fields(req, limits)` returns them as a map. To send a
multipart body, `multipart.builder().field(…).file(…)` builds one.

## Responses produced chunk by chunk

`http.chunks(status, next)` streams a response the handler produces as
it goes. The server calls `next(0)`, `next(1)`, … and sends each chunk
once the socket has taken the one before, until `next` returns `None`.
HTTP/1.1 clients get chunked transfer coding, HTTP/2 clients DATA
frames.

```fern
import "std/http";
import "std/serve";
import "std/time";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let rows: (i32) => Option[u8[]] = (i: i32): Option[u8[]] => {
        if (i >= 5) {
            return None;
        }
        plat.sleep(time.duration_millis(200 as i64));
        return Some(("row " + i.to_string() + "\n").bytes());
    };
    return http.chunks(200, rows).with_content_type("text/plain");
}
```

A producer that waits — on `plat.sleep` here, or on `plat.http`, or on
reading a streamed request — parks just its connection. `max_in_flight`
bounds how many producers a worker runs at once, and
`response_min_data_rate` drops clients that stop reading.

### Trailers

Some protocols put metadata after the body: a checksum, a final status.
`http.chunks_with_trailers(status, next, trailers)` calls `trailers()`
once the body has ended, so the values can depend on what was sent:

```fern
import "std/http";
import "std/serve";
import "std/headers";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let sent: Cell[i32] = cell_new(0);
    let next: (i32) => Option[u8[]] = (i: i32): Option[u8[]] => {
        if (i >= 3) {
            return None;
        }
        let chunk: u8[] = ("part " + i.to_string() + "\n").bytes();
        sent.set(sent.get() + chunk.len());
        return Some(chunk);
    };
    let trailers: () => HeaderMap = (): HeaderMap => headers.header_map_new().set("x-bytes-sent", sent.get().to_string());
    return http.chunks_with_trailers(200, next, trailers);
}
```

For values known up front, `resp.with_trailer(name, value)` adds a fixed
trailer to any chunked response. Trailers go in the chunked trailer
section over HTTP/1.1 and in a final HEADERS frame over HTTP/2; HTTP/1.0
clients and `wasm32-wasi-http` get none. Request trailers arrive in
`req.trailers`.

## Files

`http.file(path)` answers with a file the server streams as the socket
accepts it — with `sendfile(2)` on a plain or kTLS connection, so the
bytes never pass through the program. A file that cannot be opened is a
404. Set the content type yourself, or use `std/http/static`
([Routing and middleware](../routing-middleware/#static-files)), which
also handles validators and ranges.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.file("/var/data/export.csv")
        .with_content_type("text/csv")
        .with_header("content-disposition", "attachment; filename=export.csv");
}
```

## Server-Sent Events

`std/http/sse` sits on the producer: give it a function from an index to
the next event, and it streams `text/event-stream` with the right
headers. `respond_with` also writes a keep-alive comment whenever the
stream has been idle for the given time, so proxies do not cut it.

```fern
import "std/http";
import "std/serve";
import "std/time";
import "std/http/sse";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path != "/events") {
        return http.html("<script>new EventSource('/events').onmessage = e => document.body.append(e.data + ' ')</script>");
    }
    // A reconnecting browser says where it got to.
    let from: i32 = sse.last_event_id(req).unwrap_or("-1").parse_int_or(0 - 1) + 1;
    let ticks: (i32) => Option[sse.Event] = (i: i32): Option[sse.Event] => {
        let n: i32 = from + i;
        if (n >= 100) {
            return None;
        }
        plat.sleep(time.duration_seconds(1 as i64));
        return Some(sse.event("tick " + n.to_string()).with_id(n.to_string()));
    };
    return sse.respond_with(time.duration_seconds(15 as i64), ticks);
}
```

An event can carry a name (`with_name`), an id (`with_id`) and a retry
hint (`with_retry`). On the client side, `sse.decoder().feed(bytes)`
parses a stream back into events.

## WebSockets

`std/http/websocket` upgrades a request and runs the connection with
callbacks: `open`, `message`, `drain` (the send buffer emptied) and
`close`. It implements RFC 6455 with permessage-deflate compression,
answers pings itself, and closes with the right code on a protocol
error (1002), invalid UTF-8 (1007) or an oversized message (1009).

```fern
import "std/http";
import "std/serve";
import "std/http/websocket";
import "std/platform";

function on_message(c: websocket.Conn, m: websocket.Message): void {
    match (m) {
        Text(s) => {
            if (s == "bye") {
                c.close(1000, "see you");
                return;
            }
            let r: websocket.SendResult = c.send_text("echo: " + s);
        },
        Binary(b) => {
            let r: websocket.SendResult = c.send_binary(b);
        }
    }
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/ws") {
        let opts: websocket.Options = websocket.Options { ...websocket.options(), compression: true };
        let h: websocket.Handlers = websocket.Handlers {
            ...websocket.handlers(on_message),
            open: (c: websocket.Conn): void => {
                let r: websocket.SendResult = c.send_text("hello from " + c.path);
            },
        };
        return http.respond(websocket.accept(req, opts, h));
    }
    return http.ok("ok\n");
}
```

A request that is not a WebSocket upgrade gets `426 Upgrade Required`
(or 400), from `websocket.UpgradeError`'s `ToResponse`.

Sends inside a callback are batched into one write when the callback
returns. `send_*` reports `Sent`, `Backpressure` once more than
`backpressure` bytes (64 KiB) are queued — slow down until `drain` is
called — or `Dropped` past `max_backpressure` (1 MiB). `max_message`
(1 MiB) caps an incoming message. An idle connection closes after the
server's `keep_alive_idle`.

The WebSocket server is part of the native server loop.

Next: [HTTPS, HTTP/2 and gRPC →](../protocols/)
