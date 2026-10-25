---
title: HTTPS, HTTP/2 and gRPC
description: Serving TLS 1.3 from PEM files, HTTP/2 beside HTTP/1.1, kernel TLS and io_uring on Linux, mutual TLS, and a gRPC service over std/http/grpc and std/protobuf.
sidebar:
  order: 7
---

TLS and HTTP/2 are settings on `serve.Config`, not separate servers. The
handler does not change: the same function answers HTTP/1.1, HTTP/2,
plaintext and TLS.

## HTTPS

`std/tls` is a TLS 1.3 implementation in Fern, with X25519 and the
post-quantum hybrid X25519MLKEM768 for key exchange. Load a certificate
chain and key from PEM and hand them to `with_tls`:

```fern
import "std/http";
import "std/serve";
import "std/tls/identity";
import "std/tls/server";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.plain("hello over TLS\n");
}

function main(): i32 {
    let Ok(cert) = read_file("cert.pem") else {
        eprint("no cert.pem");
        return 2;
    };
    let Ok(key) = read_file("key.pem") else {
        eprint("no key.pem");
        return 2;
    };
    match (identity.from_pem(cert, key)) {
        Ok(id) => {
            return serve.supervise(8443, serve.config().with_tls(server.config(id, random_bytes(32))), handle);
        },
        Err(e) => {
            eprint(e.message());
            return 1;
        }
    }
}
```

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout key.pem -out cert.pem -subj /CN=localhost -days 30
curl --cacert cert.pem https://localhost:8443/
```

Keys may be P-256, P-384 or Ed25519, in PKCS#8 or SEC1 form; RSA keys
are not accepted. The handshake counts against `recv_deadline`, so a
client that never completes one is closed when it passes.

`server.config(id, key)` turns on session tickets sealed under `key`
with a two-hour lifetime. A key from `random_bytes(32)` serves one
process; behind a load balancer, give every instance the same key so a
client can resume on any of them, or turn tickets off with
`without_tickets()`.

### Mutual TLS

To require client certificates, give the server the roots to verify
them against:

```fern
let roots: verify.Roots = verify.roots_from_file("clients-ca.pem")?;
let tls: server.Config = server.config(id, random_bytes(32)).with_client_roots(roots);
```

On the client side, `std/tls/client`'s `with_identity(id)` presents a
certificate.

### Kernel TLS

`with_ktls()` hands the connection's record encryption to the Linux
kernel after the handshake. Files then go out with `sendfile(2)` even
over TLS, and sealing moves off the worker. Where the kernel lacks the
`tls` module, and on macOS, the server keeps encrypting in user space.
Setting `FERN_KTLS=1` in the environment does the same without a
rebuild.

## HTTP/2

`with_h2()` serves HTTP/2 beside HTTP/1.1 on the same port. Under TLS,
ALPN offers `h2` and `http/1.1` and the client picks; in plaintext, a
connection that opens with the HTTP/2 preface is served as prior-knowledge
`h2c`.

```fern
import "std/http";
import "std/serve";
import "std/option";
import "std/platform";

function init(): serve.Config {
    return serve.config().with_h2();
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.plain("hello from " + req.headers.get("host").unwrap_or("?") + "\n");
}
```

```bash
curl --http2-prior-knowledge localhost:8080/
```

Combine `with_h2()` and `with_tls(…)` in either order for HTTP/2 over
TLS, which is what browsers speak. Each stream is handled like a
request on its own connection: streams park independently, so a slow
handler does not hold up the others on the connection. A request's
`:authority` arrives as its `host` header.

The HTTP/2 implementation is built against the known attacks on the
protocol: Rapid Reset and MadeYouReset are bounded by a reset budget,
CONTINUATION floods and header-list bombs by header limits, and empty
DATA frames by their own budget. Server push is not offered. Below the
server, `std/http/h2` is a sans-IO state machine — bytes in, events and
bytes out — that the client uses too.

On the client side nothing needs enabling: `fetch` offers `h2` by ALPN
to every `https` origin.

## io_uring

`with_io_uring()` runs a worker's event loop on io_uring instead of
epoll, on Linux 6.1 and later; responses larger than
`io_uring_zc_bytes` (3 KiB) are sent zero-copy. On an older kernel, or
where io_uring is disabled, the server falls back to epoll. `FERN_IO_URING=1`
turns it on from the environment.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function init(): serve.Config {
    return serve.Config { ...serve.config().with_io_uring().with_h2(), reuse_port: true };
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok\n");
}
```

## gRPC

gRPC is HTTP/2 with length-prefixed messages in the body and the call's
status in trailers. `std/http/grpc` handles that wire format and
`std/protobuf` reads and writes protobuf fields by number, so a service
is an ordinary handler. Take the canonical example: a `Greeter` whose
`SayHello` takes `HelloRequest { string name = 1; }` and returns
`HelloReply { string message = 1; }`.

```fern
import "std/http";
import "std/http/grpc";
import "std/protobuf";
import "std/serve";
import "std/platform";

function init(): serve.Config {
    return serve.config().with_h2();
}

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (!grpc.is_grpc(req)) {
        return http.text(415, "gRPC only\n");
    }
    if (req.path == "/helloworld.Greeter/SayHello") {
        return say_hello(req.body_bytes());
    }
    return grpc.error(grpc.UNIMPLEMENTED, "no such method " + req.path);
}

function say_hello(body: u8[]): HttpResponse {
    let Ok(Some(found)) = grpc.next_message(body) else {
        return grpc.error(grpc.INVALID_ARGUMENT, "expected one request message");
    };
    let Ok(fields) = protobuf.decode(found.0) else {
        return grpc.error(grpc.INVALID_ARGUMENT, "malformed HelloRequest");
    };
    let name: string = "stranger";
    if let Some(b) = protobuf.bytes(fields, 1) {
        name = string_from_bytes_unchecked(b);
    }
    let reply: u8[] = protobuf.string_field(1, "Hello, " + name);
    return grpc.response([reply], grpc.OK, "");
}
```

```bash
grpcurl -plaintext -proto helloworld.proto -d '{"name":"fern"}' \
    localhost:8080 helloworld.Greeter/SayHello
# { "message": "Hello, fern" }
```

The pieces:

| Call | Does |
|---|---|
| `grpc.is_grpc(req)` | An HTTP/2 `POST` with `content-type: application/grpc` |
| `grpc.next_message(buffered)` | Splits one length-prefixed message off the front: `Ok(Some((message, rest)))`, `Ok(None)` when incomplete |
| `grpc.response(messages, code, msg)` | A 200 with the framed messages and `grpc-status` / `grpc-message` trailers |
| `grpc.error(code, msg)` | A trailers-only error response |
| `grpc.timeout(req.headers)` | The client's `grpc-timeout` as a `Duration` |
| `grpc.frame(message)` | Frame one message, for streaming responses |
| `protobuf.decode(bytes)` | Every field, by number and wire type |
| `protobuf.varint` / `bytes` / `repeated` | Read a field by number |
| `protobuf.varint_field` / `bytes_field` / `string_field` | Encode one; concatenate to build a message |

Streaming RPCs use the streaming pieces from the [previous
page](../streaming/): with `stream_bodies: true` the handler reads
messages as they arrive, and `http.chunks_with_trailers` sends framed
messages one at a time with the status in the trailers. The standard
library's server passes the official gRPC interoperability suite, run
nightly.

Next: [Running in production →](../production/)
