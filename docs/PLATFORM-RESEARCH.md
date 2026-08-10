# Platform research — the handler / host seam for edge functions

Cross-language survey of the platform-application boundary:
how user-written handler code talks to its host, what
capabilities flow across the seam, what the request lifecycle
looks like, and how single-handler-source compiles to multiple
deployment targets (WASI components, native HTTP servers,
serverless FaaS).

Companion to `PERFORMANCE-RESEARCH.md`. That doc covered the
compiler-and-runtime perf angle; this one covers the
*architectural* seam between handler code and its execution
environment, which is what determines how much of the language's
performance budget gets spent on plumbing vs real work.

The codebase already ships `function handle(req): HttpResponse`
as the canonical entry point — same source compiles for
`-target=wasm32-wasi-http` (a Component Model `.wasm`) and `-target=arm64-linux`
(a Linux ELF that serves HTTP/1.1 on `$PORT` via the
auto-synthesised `main → tcp_serve(port, handle)`). The
question this doc asks: **does that seam shape generalise as
the language picks up more deployment targets and more
capabilities** (KV, secrets, scheduled triggers, fetch-to-other-
services), or does it ossify in a way that costs us later?

## Framing — what "platform" means here

Borrowed framing from Roc (the source we'll dwell on hardest):

- **Application code** is what the handler author writes. Pure
  in the sense of "no syscalls in the source" — every effect
  goes through a typed capability boundary.
- **Platform code** is what the host ships: the entry point,
  the request parser, the response serialiser, the bindings
  to KV / secrets / fetch / scheduled-triggers, the trap
  handler, the per-request arena.

A platform-application seam has three knobs:

1. **What's the handler's type signature?** What flows in
   (request? capabilities? both?), what flows out (response?
   effect log? both?).
2. **What's the lifecycle?** One handler-invocation per
   incoming request, vs one process per incoming request, vs
   pool of warm handlers? Init-once vs init-per-invocation?
3. **What's the failure model?** Handler traps → host recovers
   how? Handler hangs → host cancels how?

The codebase has tentative answers for all three:

1. `function handle(req: HttpRequest): HttpResponse`. No
   capability bag yet; effects come from globally-imported
   stdlib (`tcp_serve` etc.) plus the auto-prelude.
2. Per-handler-invocation lifecycle. The arena is reset
   between requests (see comment in
   `examples/wasm/echo_handler.fern`).
3. Trap → process exit on native; trap → component instance
   destroyed on wasi-http. No structured recovery yet.

The interesting question is whether to *evolve* these answers
incrementally or pick a target shape now and migrate towards it.
The user has confirmed there's no handler code in the wild to
preserve, so the migration cost is internal-only.

## What we already do well — call out so we don't drift

- **One source, many targets.** `echo_handler.fern` compiles
  to a wasi-http component, a Linux arm64 ELF, and (by
  inference from `BACKEND-PARITY.md`) an arm64-darwin Mach-O.
  This is the *correct* posture: the handler signature is the
  portable contract; the platform is the target-specific
  surrounding code. Don't fragment this.
- **Auto-synthesised `main()`.** When the program defines
  `handle` but not `main`, the checker synthesises
  `main(): i32 { return tcp_serve(__port_from_env("PORT",
  8080), handle); }`. This is the right ergonomic — handler
  code is a half-line; the noise lives in the platform.
- **Per-handler arena, automatically inserted.** Comment in
  `examples/wasm/echo_handler.fern`: "all the strings the
  handler builds get reclaimed at handler-return." Matches
  Roc's Task-based isolation by construction. Strong default.
- **Limits-on-everything in the prelude HTTP parser.**
  `LANGUAGE-DIRECTION.md ▸ TigerStyle ▸ Already aligned`: 8 KiB
  headers, 1 MiB body, returns `None` past either. Right
  posture for hostile inputs from the edge.
- **WASI Preview 2 migration plan exists** (`docs/WASI-PREVIEW2.md`)
  and is broken into 5 incremental shippable steps.
  Component-Model is the right long-term target.

## Single-source deep dives

### Roc — platforms and applications

Sources:
- https://www.roc-lang.org/platforms
- Richard Feldman's RustConf 2022 talk "Outperforming
  Imperative with Pure Functional Languages."
- https://github.com/roc-lang/basic-cli/ (the canonical
  "platform" for CLI tools).
- https://github.com/roc-lang/basic-webserver/.

**The headline idea.** Roc programs are two pieces glued
together:

- An **application** (the user's `.roc` file) declares which
  platform it targets, exports a single top-level definition
  named `main` (or `mainForHost` in newer Roc), and uses only
  the capabilities the platform provides.
- A **platform** (a `.roc` package, often shipping its own
  `.so`/`.dll`/`.wasm` host code) defines the application's
  type signature, exports the effects (file I/O, HTTP, env,
  time, sleep), and provides the host program that drives
  the application.

The compiler weaves them together: the platform's `host.zig`
or `host.rs` calls into the application's exports, the
application calls back into the platform's effects through
`Task` (a structured-concurrency promise type). All shared
data is owned by exactly one side at any moment, threaded
through a Roc-side `Box`/`List`/refcount-aware allocator.

**Why the application can be pure.** Every effect goes through
`Task A B` — a Promise-shaped value parameterised by success
type `A` and error type `B`. `Task` is the *only* way to do
I/O. The application composes Tasks with `await` /
`Task.attempt` / `Task.loop`; the platform discharges them
by running the actual syscalls. The compiler can verify at
type-checking time which effects a handler uses, because
they show up in `Task` types.

**Why this fits an edge-handler language.** Edge handlers
have a small, well-defined set of effects: read request,
write response, fetch other services, read KV, read secrets,
log, sleep, schedule. Encoding them as typed capabilities
gives:

- Compile-time check that a handler doesn't use a capability
  the platform doesn't support.
- Mock platforms for testing — swap the real KV for an
  in-memory one in unit tests without touching handler code.
- Multiple platforms from one application source —
  `basic-cli` vs `basic-webserver` vs `lambda` differ in
  platform code, not application code.

**Roc's allocator model is also load-bearing.** Application
code uses Roc's RC-aware allocator; the platform can provide
its own (in Roc/basic-cli's case, a bump arena for short-
lived programs). Same shape as this codebase's per-request
arena — Roc's platforms can plug in whatever allocator fits
the platform's lifecycle.

**What translates:**

- **The application-platform split itself.** Today the
  codebase's handler is purely function-typed (`fn(req) →
  resp`). Effects come from globally-imported stdlib.
  Promoting effects to typed capabilities passed as a second
  parameter — `function handle(req: HttpRequest, plat:
  Platform): HttpResponse` — opens the door to:
  - Different platforms for different deployment shapes
    (native HTTP, wasi-http, wasi-cli, lambda-like).
  - Mock platforms for tests.
  - Compile-time effect tracking
    (`LANGUAGE-DIRECTION.md ▸ Algebraic effects` — Roc's
    approach is what to model on).

- **`Task` (or whatever we call it) as the effect carrier.**
  Roc's `Task A B` ≈ Rust's `Result A B` extended with a
  promise abstraction. For pure-AOT / single-threaded
  handlers we don't strictly need the *promise* part — every
  effect is synchronous in WASI Preview 2 with the new
  callback-based polling API anyway. The carrier type can
  start as `Result A B` and grow to a `Task` shape if/when
  cooperative concurrency arrives.

- **Capability descriptors at compile time.** The platform
  declares which capabilities it provides; the checker
  verifies the application stays within them. Sketch:

  ```
  // platform/wasi-http/platform.fern
  pub platform "wasi-http" {
      capabilities {
          fetch: (HttpRequest) -> Task[HttpResponse, FetchError],
          kv:    (string) -> Task[Option[bytes], KvError],
          log:   (LogLevel, string) -> Task[(), Never],
          now:   () -> Task[Instant, Never],
      }
      handler {
          handle: (HttpRequest, Platform) -> HttpResponse,
      }
  }

  // user.fern
  function handle(req: HttpRequest, plat: Platform): HttpResponse {
      var maybe_session = plat.kv.get("session:" + req.cookie).await;
      // …
  }
  ```

  Checker rejects calls to capabilities the platform doesn't
  list. Each capability gets a stable ABI surface (function
  index in WASI Component Model; struct-of-function-pointers
  in native).

**Considered, left:**

- *Pure-functional-application restriction.* Roc enforces
  that the application has no mutation except through Task.
  Our language is imperative-flavoured and the bump arena
  makes most pure-vs-mutable concerns moot — mutation within
  the per-request scope is fine. We can take Roc's seam
  shape without taking its purity.

- *Platform package distribution as `.roc`-files.* That's a
  Roc-specific package-manager call. Our equivalent is a
  build flag (`-target=wasm32-wasi-http` selects a particular
  platform's code-generation path). Lighter, less complex.

### Cloudflare Workers — service bindings and request context

Source:
- https://developers.cloudflare.com/workers/runtime-apis/
- https://developers.cloudflare.com/workers/configuration/bindings/
- *Inside a Worker* posts on the Cloudflare blog (Kenton
  Varda's "Why Cloudflare Workers Don't Use Containers").

**The seam shape:**

```javascript
export default {
    async fetch(request, env, ctx) {
        // env.SESSIONS is a KV namespace handle bound at deploy time
        const cached = await env.SESSIONS.get(sessionKey);
        ctx.waitUntil(logRequest(request));
        return new Response(cached ?? "miss");
    }
};
```

- `request`: standard Web `Request` object.
- `env`: a frozen bag of *bindings*. Each binding is named in
  `wrangler.toml` and bound to a concrete resource at deploy
  time (a KV namespace, an R2 bucket, a secret, another
  Worker as a service binding). Inside the handler, bindings
  are just JS properties of `env`.
- `ctx`: a per-request context object. `ctx.waitUntil(promise)`
  tells the runtime "don't kill the isolate until this
  promise resolves, even after I've returned a Response."
  Used for logging, analytics, eventual-consistency writes.
- Return: a `Response` (sync). Streaming bodies are
  `ReadableStream` instances.

**Why bindings beat globals.** A binding name is
deploy-time-typed: `wrangler.toml` says "KV[sessions]". The
runtime injects the right handle. Result:

- Same handler can deploy to staging (binding points to
  staging KV) and prod (points to prod KV) with zero
  code change.
- Local-dev binds to a sqlite-backed mock — same shape, no
  network.
- Removed bindings fail at deploy time, not at first request.
- Permissions are visible in config: "this Worker uses 3 KVs,
  1 R2 bucket, and binds to two other Workers."

**Service bindings specifically.** A Worker can bind to
another Worker by name (rather than by URL). `env.AUTH.fetch(
new Request(...))` invokes the bound Worker's `fetch` handler
*in-process* — same isolate, no network hop. Cross-Worker
calls go from milliseconds to microseconds.

**The `ctx.waitUntil` shape.** Decouples "the response is
ready" from "we're done with this request." Two phases:
hot-path (must finish before response) and background (must
finish before isolate-kill). For analytics and metrics that
the user shouldn't pay latency for, this is the right shape.

**What translates:**

- **Capability bindings as deploy-time configuration.** The
  capability descriptor sketched in the Roc section above
  needs a *binding* concept. Sketch:

  ```toml
  # fern.toml — per-deployment configuration
  [bindings.sessions]
  kind = "kv"
  namespace = "prod-sessions"

  [bindings.auth]
  kind = "service"
  target = "auth-worker"
  ```

  The compiler generates the per-capability glue against these
  names. Test platforms inject mocks against the same names.
  Removed bindings fail at compile time of the platform glue.

- **`waitUntil`-shape background tasks.** Two-phase
  completion (response-ready vs cleanup-done) shows up in
  WASI Preview 2 as well — `wasi:io/poll` lets the
  application register pollables that the host keeps alive
  after the response writes. The Fern surface could expose
  this as `plat.background(task)` returning unit.

- **Service bindings as the cross-handler-call shape.** If
  the language grows to support multiple handlers in one
  binary (one per `[bindings.*.kind = "service"]`), the
  in-process call shape is much cheaper than network round-
  trip. Roc's platform-effect interface composes here.

**Considered, left:**

- *Isolates as the deploy unit.* That's a runtime engineering
  decision (V8 process model). Our equivalent is a WASI
  component instance, which is similar in shape but baked
  by the host (wasmtime, jco, fastly Lucet). Not a language
  decision.

- *Web Standards (`Request`/`Response`/`fetch`/`Headers`)
  as the API.* JS-specific. Our `HttpRequest`/`HttpResponse`
  structs are simpler and more direct. The interesting bit
  is keeping field names compatible with the Web API where
  it's free — `method`, `url` (or `path`), `headers`, `body`,
  `status` — so handlers feel familiar to people coming
  from Workers / Deno / Bun.

### Fastly Compute@Edge — backends and dictionaries

Source:
- https://developer.fastly.com/learning/compute/
- https://github.com/fastly/compute-sdk-rust
- https://github.com/fastly/Viceroy (local dev runtime).

**Fastly's WASM-based platform predates wasi-http and has
diverged from it in important ways. Worth understanding both
for what to take and what to avoid.**

The handler shape:

```rust
#[fastly::main]
fn main(req: Request) -> Result<Response, Error> {
    let backend = "origin";
    let resp = req.send(backend)?;
    Ok(resp)
}
```

Differences from the wasi-http canonical shape:

- **Top-level `main` with a typed argument**, not a
  separate `handle` export. The macro turns it into the
  ABI-correct entry point.
- **Backends are named, not URLs.** `req.send("origin")`
  invokes whichever HTTP backend is bound to name `origin`
  in the service config. Decouples deployment topology
  from handler code.
- **Dictionaries**: read-only string maps installed at
  deploy time. Used for feature flags, allowlists, A/B
  test config. Cheaper than a KV lookup because they're
  in-VM.
- **Edge state (KV stores, secret stores, etc.)** is
  accessed through host-call seams that look like Rust
  function calls.

**What we take from this.** The named-backend pattern is the
right shape for *outbound* HTTP: handler code says "talk to
the auth service"; deploy-time config says which URL/cert/
backend the name maps to. Compose with Cloudflare-style
bindings: each named backend / KV / secret is a capability
binding.

**Dictionaries (read-only config)** are interesting because
they're cheap to access (no syscall, sometimes mmap'd from
the host's read-only memory). For feature flags, A/B
tickets, allowlists — pattern shows up in nearly every
edge handler. Worth supporting as a first-class binding
kind:

```toml
[bindings.flags]
kind = "config"
contents = { canary_pct = "10", new_router = "false" }
```

**Considered, left:**

- *Compute@Edge-specific bytecode format (.wasm + ABI
  shims).* Fastly's `compute-sdk-rust` ships a particular
  ABI; not WASI Preview 2 compatible. Going pure
  WASI-Preview-2 + Component Model is the lowest-friction
  long-term bet — Fastly is themselves on the path to wasi-
  http compatibility per their public roadmap.

### wasi:http/proxy — the canonical interface

Source: `cmd/fern/wit/deps/http/incoming-handler.wit`,
`cmd/fern/wit/deps/http/types.wit`, the wasi-http
WIT documents at https://github.com/WebAssembly/wasi-http.

The codebase already imports these WIT files. The interface
shape:

```wit
interface incoming-handler {
    use types.{incoming-request, response-outparam};
    handle: func(request: incoming-request, response-out:
        response-outparam);
}
```

Three things to note:

1. **`incoming-request` is a resource type**, not a struct.
   Resources are owned-handles, the Component-Model
   equivalent of file descriptors. The handler is given the
   handle, can read fields from it, but the host owns the
   lifetime. Drops the handle at handler return.

2. **`response-outparam` is a write-only output sink.**
   Instead of returning a Response, the handler *writes*
   the response into a host-provided sink. This lets the
   host start streaming the response back to the client
   *before* the handler finishes — critical for streaming
   bodies, server-sent events, chunked responses.

3. **No `Result` on the handler.** A handler that traps
   produces a 5xx response automatically; the handler
   surface itself can't return errors. Errors propagate
   through the *response-outparam.set* call, which takes a
   `result<outgoing-response, error-code>`.

**Implication for the codebase's `function handle(req):
HttpResponse` shape.** It's *not* aligned with the wasi-http
canonical shape:

- The Fern handler returns a struct, the WIT expects
  write-into-sink.
- The Fern handler has the full `HttpRequest` as a struct
  with `req.body: string` (eagerly read); the WIT exposes
  `incoming-request` as a resource with `consume → input-
  stream` (pull as needed).

The current `internal/codegen/wasmbin/wasi.go` and the
auto-prelude HTTP parser bridge this gap by eagerly reading
the request body, building the response string, then
streaming it out. Works for the in-memory < 1 MiB case;
won't work for streaming responses or for >1 MiB requests.

**What translates:**

- **Move to a streaming-by-default surface, with
  eager-read as an opt-in.** Sketch:

  ```
  function handle(req: HttpRequest, plat: Platform): () {
      var body = req.body.read_all()?;          // eager
      var resp = plat.response(200);
      resp.header("Content-Type", "text/plain");
      resp.write(body);                          // streaming
      resp.finish();
  }
  ```

  vs the current sugar:

  ```
  function handle(req: HttpRequest): HttpResponse {
      return HttpResponse { status: 200, body: "ok" };
  }
  ```

  The sugar layer can desugar into the streaming form for
  small responses; large responses use the streaming form
  directly. Both forms compile to wasi-http's
  response-outparam shape.

- **Request body as a stream resource, not a string.** The
  current `req.body: string` field forces eager read. A
  `Stream[bytes]` type (with `.read_all() → Result[bytes,
  IoError]` and `.read(n) → Result[bytes, IoError]`) covers
  both shapes. Eager-read remains the common case for small
  POSTs; streaming becomes possible for uploads.

**Considered, left:**

- *Resource types as a first-class Fern concept.* WIT
  resources are owned-handles with a drop-on-scope-exit
  protocol. Our bump arena gives us this for free as long
  as the resource's drop is registered as an arena
  cleanup. We don't need to expose "resource type" in the
  surface syntax — the codegen can emit drop-on-arena-
  reset for any platform-provided handle.

### Hyper / Axum / Tower — the Rust HTTP service abstraction

Source:
- https://docs.rs/hyper, https://docs.rs/axum,
  https://docs.rs/tower
- "Inventing the Service Trait" (Lucio Franco).

**The Service trait — Rust's abstraction for "anything that
takes a request and produces a response":**

```rust
pub trait Service<Request> {
    type Response;
    type Error;
    type Future: Future<Output = Result<Self::Response, Self::Error>>;
    fn call(&mut self, req: Request) -> Self::Future;
}
```

Three properties that make this load-bearing:

1. **Composability.** Middleware is just `Service` wrapping
   another `Service`. Logging, tracing, retries, rate
   limiting, auth — all express as wrappers. Composes via
   `tower::ServiceBuilder`.
2. **Generic over request type.** Same trait powers HTTP,
   gRPC, Redis protocol, internal RPC. Tower middleware
   often works across all of them.
3. **Async-aware.** Returns a future; non-blocking by
   construction.

**The Tower middleware ecosystem** layers compression,
auth, rate limits, retries, timeouts, tracing as
`Layer`s — each one a `Service → Service` adapter. Composing
middleware is `ServiceBuilder::new().layer(Timeout::new(...))
.layer(Trace::new(...)).service(my_handler)`.

**Axum on top** specialises Service for HTTP, providing
extractors (`Path`, `Query`, `Json`, `State`) and response
builders. Type-driven routing falls out: `Router::new().route(
"/users/:id", get(get_user))` and the framework knows what
the handler expects.

**What translates:**

- **A `Handler[Req, Resp]` interface (or trait, or struct
  pointer), not just a top-level function.** The single
  top-level `handle` is fine for hello-world; once
  middleware enters the picture, the handler becomes a
  *value* that can be wrapped. Sketch:

  ```
  pub interface Handler[Req, Resp] {
      handle(self, req: Req): Resp;
  }

  function with_logging[Req, Resp](inner: Handler[Req, Resp]):
          Handler[Req, Resp] {
      return LoggingHandler { inner: inner };
  }
  ```

  Compiles down to the same direct function call when
  monomorphised + inlined; no overhead vs the current
  bare-function shape.

- **Extractor pattern (Axum's killer feature).** The handler's
  parameter types tell the framework how to populate them:

  ```
  function get_user(p: Path[i32], q: Query[UserListParams],
                    body: Json[CreateUser]): HttpResponse { … }
  ```

  Path/Query/Json are extractors. The framework calls them in
  turn (each one consumes the request, possibly mutating it)
  and assembles the handler's parameter list. Composes with
  whatever the platform provides.

  This is a substantial type-system feature (requires a way
  to associate "this type has an extract-from-request rule"
  — Rust does it with the `FromRequest` trait). Worth doing
  iff the language picks up trait/interface-style ad-hoc
  polymorphism.

**Considered, left:**

- *Tower's async-everywhere posture.* Right for long-lived
  Rust servers; for cold-start edge handlers we want
  sync-by-default. The async work happens in the platform
  layer (the runtime pumps the WASI event loop); the handler
  code stays straight-line.

### Bun.serve — modern web-standard API

Source:
- https://bun.sh/docs/api/http
- The Bun source (`src/bun.js/api/server.zig`).

**Bun's HTTP server API is the most ergonomic of any
mainstream JS runtime. Notably:**

```typescript
Bun.serve({
    port: 3000,
    fetch(req, server) {
        return new Response("Hello!");
    },
    websocket: {
        open(ws) { ws.subscribe("chat"); },
        message(ws, msg) { server.publish("chat", msg); },
    },
});
```

- `fetch(req, server)` is the per-request handler. `server`
  is a reference to the running server (gives access to
  `publish`, `requestIP`, etc.).
- `websocket` is a *separate* handler set with its own
  callbacks. Two handler kinds in one config.
- `Bun.serve` returns a `Server` object — you can `stop()`,
  `reload()`, `upgrade()` it.

**Multi-handler-kind in one config** is the right design.
Edge handlers want HTTP, but they also want scheduled
triggers (cron), incoming WebSockets, MQ message handlers,
DLQ replay handlers, etc. Bun's pattern of "one server
object, multiple handler-kind keys" generalises.

**What translates:**

- **Multi-handler dispatcher in the language.** Today the
  codebase auto-synthesises `main` from `handle`. The
  generalisation:

  ```
  function fetch(req: HttpRequest, plat: Platform): HttpResponse { … }
  function scheduled(cron: CronEvent, plat: Platform): () { … }
  function alarm(event: AlarmEvent, plat: Platform): () { … }
  ```

  The checker recognises any/all of these and synthesises
  the right multi-entrypoint glue. wasi-http exports go to
  `fetch`; wasi-scheduled exports go to `scheduled`;
  durable-objects-style alarms go to `alarm`. Same source
  file can host all three.

- **Server-object reference for the running server.** Bun
  passes `server` to the handler. Our equivalent is the
  `Platform` parameter from the Roc section — same shape,
  bigger remit.

**Considered, left:**

- *Web Standards (`Request`/`Response`/`fetch`/`Headers`)
  as the API surface.* Bun, Deno, Cloudflare Workers all
  use it; we don't, and shouldn't, because the Web API has
  warts (header case-insensitivity-via-case-preservation,
  `Body` consumption semantics) that we don't need to
  carry forward.

### AWS Lambda — handler signature and init lifecycle

Source:
- https://docs.aws.amazon.com/lambda/latest/dg/welcome.html
- The lambda runtime APIs (Custom Runtime spec).

**Lambda's lifecycle has two phases that are well worth
modelling:**

1. **Init phase.** The container starts; the runtime calls
   the user's *handler module* once (not the handler function).
   Top-level statements run. Used to: open DB connections,
   parse config, load ML models. Costs cold-start time but
   amortises across many invocations.
2. **Invoke phase.** The runtime calls the handler function
   per request. Init-phase state persists between invokes;
   the handler can use it.

For a hot Lambda (many invokes in a row), init runs once;
for a cold Lambda, it runs per cold start. The trick is
making init cheap so cold starts aren't catastrophic.

**Why this matters for the codebase.** Today the auto-
synthesised `main` calls `tcp_serve(port, handle)` for
native binaries. `tcp_serve` is roughly:

```
loop {
    var conn = tcp_accept(socket);
    var arena = arena_save();
    var req = http_parse_request(conn);
    var resp = handle(req);
    write_response(conn, resp);
    arena_restore(arena);
}
```

There's no per-process init phase. Anything the handler
wants to set up once-per-process has to be a top-level
const-expression (computed by `constfold`) or a one-shot
side effect on first invocation (which means a runtime
check on every invocation).

**What translates:**

- **First-class init phase.** Two-phase entry:

  ```
  // Runs once, in the per-process arena (which is
  // permanent for the process's lifetime).
  function init(): InitState {
      var db = pg_connect(__env("DATABASE_URL"));
      var routes = build_router();
      return InitState { db: db, routes: routes };
  }

  // Runs per request, in the per-request arena. Receives
  // init's return value by reference.
  function handle(req: HttpRequest, st: InitState,
                  plat: Platform): HttpResponse {
      var conn = st.db.borrow();
      …
  }
  ```

  The checker enforces: `init`'s returned struct lives in
  the process-permanent allocator, not the per-request
  arena. The compiler arranges to pass it by reference to
  every `handle` invocation.

  Multi-arena: the bump allocator gets two roots — one
  permanent, one per-request — and `init` writes to the
  former. `LANGUAGE-DIRECTION.md ▸ Odin's
  context.allocator + context.temp_allocator` is already
  on the roadmap; this fits straight in.

- **Process-init-time effect tracking.** `init` has a
  different effect row from `handle`: it can read env,
  connect to DBs, load files, but it *cannot* read the
  request. The checker rejects request access in `init`
  (because no request exists yet) and rejects DB-connect in
  `handle` (do it in `init` and reuse).

**Considered, left:**

- *Snapshotting / SnapStart (Lambda's faster-cold-start
  feature).* AWS-specific runtime engineering. Our cold
  start is already millisecond-shaped because we're AOT,
  not JIT.

## Cross-cutting themes

1. **Capabilities are values, not globals.** Roc passes a
   `Platform` to the handler; Cloudflare passes `env`;
   Fastly passes named backends; Lambda passes `context`.
   Globally-imported stdlib for effects (the current shape)
   is the JS / Python / Go pattern; the edge-runtime
   convention has moved to capabilities-as-parameters.

2. **Bindings beat hard-coded names.** Multi-environment
   deploys (staging vs prod), local-dev mocking, and audit
   of "what does this Worker actually do" all benefit from
   capabilities resolved at deploy time, not source time.

3. **Two-phase lifecycle (init, then per-request).** Lambda,
   long-lived servers, Cloudflare Workers (durable objects),
   Roc's basic-webserver. Cheap setup that amortises across
   invocations is universal.

4. **Streaming-by-default, eager-read as sugar.** wasi-http,
   hyper, Bun, fastly. Eager-read string bodies are fine
   for the < 1 MiB case, become wrong fast for uploads,
   server-sent events, h2/h3.

5. **Multi-handler-kind in one source file.** fetch +
   scheduled + alarm + websocket. Bun's pattern; matches
   how WASI is growing (`wasi:http`, `wasi:cli`,
   `wasi:scheduled`).

6. **Trap → host-recoverable.** WASI components, Cloudflare
   isolates, Lambda. Bun + native servers crash the whole
   process. For the edge use case, isolation-per-invocation
   is the right default.

7. **Mock-platforms as a first-class testing tool.** Roc's
   killer feature — same handler, fake platform. Worth
   replicating verbatim.

## Concrete recommendations

Ranked by leverage × cost. Several depend on the
`Platform` parameter (§1) landing first.

### 1. Promote the `Platform` capability bag to a handler parameter

**Cost: 2-3 weeks (parser + checker + auto-synthesis +
codegen across two targets).** **Impact: highest** — gates
several other recommendations.

Today:

```
function handle(req: HttpRequest): HttpResponse { … }
```

After:

```
function handle(req: HttpRequest, plat: Platform): HttpResponse { … }
```

The `Platform` type is generated per-target by the
compiler from the target's capability descriptor. For
wasi-http it includes `fetch`, `kv`, `secrets`, `log`,
`now`; for native (`-target=arm64-linux`) it includes a subset
(no kv unless we provide a sqlite-backed local impl).

The auto-`main` synthesis stays — it constructs the
target's `Platform` instance and threads it through.

**Migration**: old `function handle(req): HttpResponse`
auto-promotes to ignore-the-platform during a grace period;
flip after every example is updated.

### 2. Define a `Platform` descriptor format (TOML or Fern)

**Cost: 1 week.** **Impact: high.**

A per-target file declaring:

- Capabilities provided (each capability has a name, a
  function signature, and a target-specific glue
  implementation).
- Handler kinds expected (fetch, scheduled, alarm,
  startup, etc.).
- Bindings consumed (kv namespaces, service bindings,
  config dictionaries).

Lives at `internal/platforms/<target>/platform.fern` (or
`.toml` — see below). The compiler reads it during
`-target=...` resolution and generates the per-target
`Platform` struct + glue.

Format question: Fern-defined or TOML-defined. Fern-defined
gets us static-checking of the capability signatures
against the target's WIT files; TOML-defined is simpler
but pushes shape errors to runtime. Lean Fern-defined for
the typed-capability win.

### 3. Add `init()` as a recognised entry point

**Cost: 2 weeks.** **Impact: high** for long-lived
processes (native HTTP server), neutral for one-shot
(wasi-http) but harmless.

`function init(): InitState` runs once per process. The
return value lives in the permanent allocator (separate
arena root from the per-request one). The auto-synthesised
`main` calls `init` once, then loops over requests passing
`InitState` by reference to `handle`.

For `wasi-http` (one component instance per request in some
deployments), `init` runs at instance creation. For Bun-
style or native long-lived servers, it runs once at process
start. For Cloudflare-Workers-style warm isolates, it runs
once per isolate.

The checker enforces that `init` is pure of request access
and `handle` doesn't reach for resources `init` should
have set up (TBD; can be a lint to start).

### 4. Stream-by-default response API, with HttpResponse sugar preserved

**Cost: 3 weeks (touches the wasi-http codegen, the native
HTTP server, and the prelude).** **Impact: medium-high** —
gates uploads, server-sent events, large downloads.

Two APIs:

```
// Sugar — current shape. Compiles to streaming form.
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return HttpResponse { status: 200, body: "ok" };
}

// Explicit streaming.
function handle(req: HttpRequest, plat: Platform): () {
    var resp = plat.response(200);
    resp.header("Content-Type", "text/event-stream");
    for line in req.body.lines() {
        resp.write(line + "\n");
        resp.flush();
    }
    resp.finish();
}
```

The compiler chooses the lowered form. Both return-shapes
are valid handler signatures; the checker picks based on
return type.

Request body becomes a `Stream[bytes]` resource handle
with `.read_all()`, `.read(n)`, `.lines()` (UTF-8 line
iterator), `.json[T]()` (JSON decode).

### 5. Multi-handler-kind recognition

**Cost: 1 week per kind (`scheduled`, `alarm`, `websocket`,
`mq_message`).** **Impact: depends on how many kinds we
actually want.**

The auto-`main` synthesiser already recognises `handle`.
Extend it to recognise:

- `function scheduled(event: CronEvent, plat: Platform): ()`
  — wired to `wasi:scheduled/cron-handler` or to a
  native cron driver.
- `function alarm(event: AlarmEvent, plat: Platform): ()`
  — wired to durable-object alarms or a native timer queue.
- `function websocket_open / websocket_message / …`
  — wired to a WS-protocol upgrade path.

The checker generates the right glue per target. A program
defining only some handlers gets only those exports.

### 6. Mock platform for tests

**Cost: 2 weeks.** **Impact: high for test ergonomics; the
test runner is already pure-Fern per
`internal/stdlib/std/test.fern`.**

`MockPlatform` is a `Platform` impl that records every
effect call and returns canned responses:

```
function test_user_fetch() {
    var plat = mock_platform_new();
    plat.kv_set("user:42", "{\"name\":\"Alice\"}");

    var req = http_request_get("/users/42");
    var resp = handle(req, plat);

    assert_eq_i32(resp.status, 200);
    assert_eq_string(plat.calls()[0].name, "kv.get");
}
```

Drops naturally out of the capability-bag-as-parameter
design: tests pass a different `Platform` instance, no
indirection layer needed.

### 7. Named bindings as deploy-time configuration

**Cost: 2 weeks (config parser + checker + codegen).**
**Impact: high for multi-environment deploys.**

A per-deployment `fern.toml` (or per-target
`*.platform.toml`) declares:

```toml
[bindings.sessions]
kind = "kv"
namespace = "prod-sessions"

[bindings.auth]
kind = "service"
target = "auth-handler"

[bindings.flags]
kind = "config"
inline = { canary_pct = "10" }
```

The compiler reads it, generates a typed binding object
for each entry, and feeds them through to the handler via
`plat.bindings.sessions`, `.auth`, `.flags`. Removed
bindings fail the build; mistyped bindings (kv vs service)
fail the build.

For tests, a `MockPlatform`'s bindings come from a parallel
test-mode config.

### 8. Service-binding cross-handler calls

**Cost: 2 weeks (after binding system).** **Impact: medium.**

When two handlers are bound by name in the same compilation
unit (or the same deployment graph), `plat.bindings.auth.
fetch(req)` lowers to a *direct call* into the bound
handler's body, not a network round-trip. Reuse the
monomorphisation machinery — the binding's target type is
known at compile time.

Cross-binary service bindings (the bound target lives in
another deployment) lower to a host-call seam (wasi-http
fetch with a special URL scheme, or platform-specific RPC).

### 9. Trap-recoverable handler boundary

**Cost: 1 week.** **Impact: medium for native HTTP servers,
already covered by Wasmtime/Lucet for wasi-http.**

If `handle` traps, the host (the auto-synthesised `main`)
catches the trap, logs it, returns 500, and continues
serving. Today a trap kills the process.

Implementation: emit a per-request setjmp / signal handler
trampoline in the auto-synthesised main. On trap, restore
the arena to the per-request boundary, write a generic
500 response, continue accepting.

The Component-Model side gets this for free — wasmtime
already isolates per-instance traps.

### 10. Compile-time effect tracking

**Cost: 4-6 weeks.** **Impact: medium long-term.**

Once the `Platform` parameter is in (Rec §1), the checker
knows which capabilities a handler reaches for (it's
exactly the set of `plat.*` accesses + transitive callees'
access sets). Promote that to a typed effect row on the
function signature:

```
function handle(req: HttpRequest, plat: Platform): HttpResponse
    uses [io.http, io.kv, io.log] { … }
```

The checker computes the effect set; the programmer can
declare a subset (for documentation) and the checker
verifies. Composes with the Kyo-flavoured effects work
sketched in `LANGUAGE-DIRECTION.md ▸ Algebraic effects`.

### 11. `waitUntil`-shape background work

**Cost: 2 weeks.** **Impact: low until we have observability
patterns to support.**

`plat.background(task)` registers a task that the runtime
keeps alive after the response is flushed. Used for: write-
back caches, async logging, metrics export. WASI Preview 2's
`pollable` shape is the lowering target.

## Anti-patterns — explicit "do not adopt"

- **Web-standard `Request`/`Response`/`Headers`/`fetch` as
  the canonical API.** Bun, Deno, Workers all use it; we
  don't and shouldn't. The Web API carries warts we don't
  need to inherit (header case-insensitivity preserved as
  case-preservation, body-consumption semantics that
  surface JS Promise vs sync, the `URL` class's quirks).
  Field-name compatibility *where free* is fine; bag-of-
  weird-methods compatibility isn't.

- **JS-style "default export object with method properties"
  (Cloudflare Workers' `export default { fetch, scheduled }`).**
  Top-level function definitions are cleaner. The compiler
  recognises which entry points exist; no `export default`
  ceremony.

- **Async/await as the default function shape.** WASI
  Preview 2's pollable model is callback-based; for our
  AOT cold-start posture, sync-shape handlers compile
  straight-through. Async should be opt-in and confined to
  long-lived servers — even there, structured concurrency
  via Roc-style Task or Kyo-style effects is preferable to
  bare async/await colouring.

- **In-handler global state (top-level `var` shared across
  requests).** The arena model relies on per-request
  isolation. Cross-request state goes through the `init`
  phase (Rec §3); attempts to allocate cross-request from
  inside `handle` should fail-fast at the checker.

- **Sticky-session / state-affinity in the language.**
  Durable-object-shape state (Cloudflare's killer feature)
  is a deployment-runtime concept, not a language feature.
  Expose it as a binding kind (`[bindings.session]
  kind = "durable_object"`) if/when the deployment story
  calls for it; don't bake it into the language semantics.

## When to revisit

When two conditions are true:

1. A second deployment target lands beyond wasi-http +
   arm64 native — e.g. AWS Lambda, Fastly Compute, Vercel
   Edge, or a long-lived Tokio-style native server. The
   *second* target is where the platform-application split
   pays off; one target alone, the split is over-engineering.

2. Either KV or scheduled-triggers shows up in real handler
   code. Capability bindings (Rec §7) are only worth the
   complexity once there's more than one capability beyond
   the request/response itself.

Until those land, the current `function handle(req):
HttpResponse` is the right shape, with the `Platform`
parameter (Rec §1) as the first step taken in advance —
since adding a parameter to every handler later is the
breaking change we can take cheaply now (no handler code
exists in the wild beyond `examples/`).
