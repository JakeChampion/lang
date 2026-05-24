# Stdlib design research — HTTP, JSON, date/time, I/O

Companion to `STDLIB-ROADMAP.md`. That doc surveys *breadth*
across 7 reference languages and prioritises missing functions
("add `string.pad_start`", "add `Set` type"). This one surveys
*depth* across the four subdomains the codebase's positioning
makes load-bearing — HTTP, JSON, date/time, I/O. Each one is
the place where bad early decisions ossify into permanent
language-shaped warts (Go's `time.Time` zone bugs, Python's
`datetime` non-aware-vs-aware split, JavaScript's `Date`,
Java's pre-JSR-310 `Calendar`).

The user has stated readiness for breaking changes. For
these four areas specifically, breaking changes *now* are
~free; breaking changes *after the first non-trivial handler
ships* are expensive even with a single user, because handler
code spreads across CI scripts, deployment artifacts, and
documentation examples that all need updating in lockstep.

So: this doc is about getting the *shape* of each subdomain
right before code accretes against the wrong defaults.

## Framing — why these four subdomains specifically

The codebase's positioning is:

- Small fast-startup CLI tools.
- Short-lived edge HTTP handlers.

Profile the actual work a CLI tool or a handler does:

```
parse arguments                  → string handling (covered, OK)
read config file                 → I/O + JSON / TOML
hash a request body             → bytes + crypto (later)
fetch from upstream service      → HTTP (outbound, partially)
parse upstream JSON response     → JSON (have json_parse)
join data with timestamps        → date/time (don't have)
build response body              → string + JSON
write response to client         → HTTP (outbound response, have)
log structured records           → I/O + JSON + date/time
```

Three of the four — JSON, date/time, I/O — show up on
practically every line. HTTP is target-specific but is the
*reason for being* for the handler shape.

Getting any of these four wrong is the kind of error that
shows up not as a feature gap ("we should add X") but as a
*usage-pattern allergy* — every program ends up working
around the same misshape, until eventually the language
acquires a reputation for the workaround instead of for
the solution.

## What we already do well — call out so we don't drift

- **`HttpRequest` / `HttpResponse` as plain struct types**
  (`internal/stdlib/std/http.fern`). Not a class with
  methods, not a stream-of-events. Plain fields:
  `method`, `path`, `body`, `headers`. Right shape for the
  90% case; the streaming case wraps it.
- **`http_parse_request` is bounded** (8 KiB headers, 1 MiB
  body). TigerStyle-aligned.
- **Conservative HTTP subset**: HTTP/1.1, Content-Length,
  Connection: close, no keep-alive, no chunked transfer
  encoding (per the stdlib's documented scope). Doing the
  ambitious version *later* is cheaper than maintaining
  three half-finished versions now.
- **JSON encoder + parser already in stdlib** (`json_encode`
  / `json_parse`). Means downstream features like
  `wasi:http`-content-negotiation work today.
- **`format(fmt, args[])` minimal sprintf** is already
  shipped (per the recent commit in `STDLIB-ROADMAP.md`'s
  notes). Right floor; iteration upward via Rec §16.
- **Reader / writer abstractions exist**
  (`open_reader`/`writer`/`appender`, `read_line`,
  `read_chunk`). Today's shape isn't general-purpose
  streaming, but the *primitives* are aligned —
  `read_chunk` is a pull-based step, not callback-based.
- **`arena_save` / `arena_restore` exposed at the stdlib
  surface** for `tcp_serve`'s per-request reset. The arena
  is not a hidden contract — handler authors can see it
  and (in principle) save / restore it themselves.

## Single-domain deep dives

### HTTP — the cross-cutting question of "what's a Request?"

Sources:
- https://docs.rs/hyper (Rust)
- https://docs.rs/reqwest (Rust outbound)
- https://github.com/oven-sh/bun source (`src/bun.js/api/server.zig`)
- https://expressjs.com/, Node's `http` module
- Fastly/Cloudflare/Vercel SDK shapes
- The Fetch standard (https://fetch.spec.whatwg.org/)

**Three competing shapes for `Request` exist:**

1. **Eager-data struct.** Today's shape. `HttpRequest { method,
   path, headers, body }` — everything pre-parsed into fields,
   body buffered as a `string`. *Easy to use, breaks on
   streaming, breaks on large bodies, breaks on binary bodies
   (because `body: string` implies UTF-8).*

2. **Stream-of-bytes wrapper.** `HttpRequest` is a *handle*
   over an underlying byte stream. Headers are read on
   construction; body is a `Stream[bytes]` that the user
   reads lazily. *Streaming-correct; binary-correct;
   one-extra-method-call for the common case of "give me the
   body as a string."*

3. **Web Standard Fetch Request.** A bag of getters with
   `body: ReadableStream`, `bodyUsed: boolean`,
   consumer methods (`text()`, `json()`, `arrayBuffer()`).
   *Familiar to web developers; pulls in WHATWG-shaped
   warts (header case-insensitivity, body consumption
   semantics, `.then()` everywhere).*

**Recommendation: option 2** (stream-of-bytes wrapper) with
sugar for option-1 patterns. Why:

- The streaming case isn't optional once handlers do
  uploads, server-sent events, h2/h3. Designing it in is
  cheaper than retrofitting.
- Binary bodies (form uploads, image proxying, gzip-
  encoded responses) need bytes, not string. Today's
  string-typed body works because handlers haven't hit
  this yet.
- The 90% "small request, eager parse" case is one method
  call away: `req.body.read_all_string()` → `string`.

**Concrete shape:**

```
struct HttpRequest {
    method:  string                        // "GET", "POST", …
    path:    string                        // "/users/42?x=1"
    headers: HeaderMap                     // see below
    body:    Stream[bytes]                 // lazy
}

// Most-common-case sugar:
pub function (r: HttpRequest).body_string(): Result[string, IoError] {
    return r.body.read_all_string();
}
pub function (r: HttpRequest).body_json[T](): Result[T, JsonError] {
    return json_parse[T](r.body.read_all());
}
```

**Header map.** Today's `headers` is a `Map[string, string]`.
Two problems:

- HTTP allows duplicate header names (`Set-Cookie`, `Accept`,
  `Via`). A `Map[string, string]` overwrites; lossy.
- HTTP header names are case-insensitive but case-preserving
  by convention. `Content-Type` and `content-type` are the
  same header, but proxies preserve case.

Best-in-class is `HeaderMap` from `hyper`:

```
struct HeaderMap {
    // Stored case-normalised (lowercase) for lookup.
    // Insertion order preserved for iteration.
    entries: [(name, value)],
    index: Map[string, [i32]],   // name → indices of entries
}
```

Wins: multi-valued lookup (`r.headers.get_all("Accept")`),
case-insensitive lookup, ordered iteration.

**Outbound HTTP — `plat.fetch(...)` shape.** Once Rec §1 of
`PLATFORM-RESEARCH.md` lands (Platform parameter), outbound
HTTP becomes:

```
var resp = plat.fetch(HttpRequest {
    method: "POST",
    path:   "https://auth/login",
    headers: headers!{ "content-type": "application/json" },
    body:    Stream.from_string(json_encode(payload)),
}).await?;
```

Same `HttpRequest` / `HttpResponse` types both inbound and
outbound. Streaming uniformly.

#### What translates from each source

- **hyper / reqwest**: HeaderMap design (Rec §2). Service
  trait shape (`PLATFORM-RESEARCH.md ▸ Hyper/Axum/Tower`).
- **Bun**: server-object reference passed to handler, the
  multi-handler-kind pattern (also covered in
  `PLATFORM-RESEARCH.md`).
- **Fastly / Cloudflare**: named backends instead of URLs
  (PLATFORM-RESEARCH again).
- **Express / Node**: how *not* to do it. Body parsing as a
  middleware concern (`express.json()`, `express.urlencoded()`)
  splits one logical action across three files. Push body-
  parsing into the body-reader's methods (`req.body.json()`)
  instead.

#### Considered, left

- *WHATWG Fetch surface* (`new Request(url, init)`,
  `await req.json()`, `bodyUsed`). Familiar to web devs;
  imports warts we don't need. Field-name compatibility
  where free (`method`, `headers`, `body`) — actual API
  shape is ours.
- *Per-method functions* (`http_get(url)`, `http_post(url,
  body)`). Convenient but composes badly with timeouts,
  headers, retries. Single `fetch(request)` is the right
  primary; sugar layer on top.

### JSON — parsing strategies and surface shape

Sources:
- https://github.com/simdjson/simdjson (the SIMD reference)
- https://docs.rs/serde_json (the surface design reference)
- https://docs.rs/sonic-rs (Rust's "fast JSON" alternative)
- https://yyjson.ikkz.fun/ (yyjson — pure C, fast)
- https://stedolan.github.io/jq/ (the query language)
- https://docs.python.org/3/library/json.html (the
  pragmatic default)

#### Parser strategies

The codebase has `json_parse` (returns some `Value`-shaped
type) and `json_encode`. The question for serious workloads
is whether to grow it into a full design with multiple
parsing modes.

Modes used in production:

- **Eager DOM parse.** `json_parse(s) → Value`. `Value` is
  a sum type with arms for null / bool / num / string /
  array / object. Easy to use, allocates a tree, fine for
  small documents. Today's mode.

- **Schema-directed parse.** `json_parse[T](s) → Result[T,
  Err]` where `T` is a known struct type. The parser
  walks the bytes once, populates fields directly into
  the target struct, never builds the intermediate `Value`.
  Serde's `from_str::<T>`. Faster + type-safer +
  allocates less.

- **Streaming / event-based.** Like a SAX parser: emits
  events (`StartObject`, `Key("x")`, `Number(42)`,
  `EndObject`) the user consumes lazily. Used when the
  document is too big to fit in memory or when the user
  only wants one field deep in a 100MB array.

- **SIMD-accelerated.** simdjson, yyjson. Parse the
  entire document in two passes using SIMD instructions
  for delimiter scanning + structural validation. 5-10×
  faster than recursive-descent for large docs. Cold-
  start cost: a few microseconds for setup.

**Recommendation: ship schema-directed parse as the second
mode after the DOM mode.** Streaming and SIMD can wait.

**Why schema-directed is high-value:**

- Most edge handlers receive JSON request bodies of *known
  shape*. `json_parse[CreateUser](req.body)` is what the
  code wants to write, not `var v = json_parse(req.body);
  match v { Object(m) => ... }`.
- Errors are better. "Expected `name: string`, found number
  at offset 42" beats "type assertion failed."
- Allocates less. Direct write into the target struct
  avoids the intermediate `Value` tree.
- It's a checker / IR feature, not a runtime feature —
  schema-directed parsing leverages the existing
  monomorphisation pass. Each `json_parse[T]` instantiation
  generates type-specific bytecode.

**Concrete shape:**

```
// DOM mode (today).
var v = json_parse(req.body);
match v {
    Object(m) => …,
    _ => return http_response_bad_request(),
}

// Schema mode (new):
struct CreateUser { name: string, age: i32 }
var u = json_parse[CreateUser](req.body)?;  // Result return
// u.name and u.age are typed and validated.
```

The checker resolves `[CreateUser]` via monomorphisation,
generates a per-struct decode function that walks the JSON
bytes and populates the struct fields by name.

#### JSON encoder

Symmetric: `json_encode(value)` works for any type
implementing a `to_json()` trait/protocol, *or* falls
back to a struct-walking generic. The fallback works for
any plain struct with primitives.

Today's `json_encode` probably already does this for
plain structs. The question: does it round-trip with
`json_parse[T]`?

**Recommendation: round-trip property as a regression
test.** For every struct type used as a JSON payload,
assert `json_parse[T](json_encode(t)) == t`. Add to the
test runner.

#### Query language

Some handlers want "give me `.user.id` from this JSON";
others want jq-shape queries (`.users[] | select(.active)`).

**Recommendation: defer.** The schema-directed parse covers
the typed-struct case. jq-shape queries are an outside-the-
language tool ("call out to `jq` from a script"); building
it into the language only makes sense if substantial
handler code wants it. Not yet.

**JSON-Pointer (RFC 6901) is a different story.** `path:
"/user/id"` referring into a JSON document. Small (one
file of parse + lookup), widely interoperable, used in
patch operations (RFC 6902 JSON-Patch), service-bindings
config, etc. Worth adding as a small helper:
`json_get(doc, "/user/id") → Option[Value]`.

#### What translates from each source

- **simdjson**: not now; revisit when handler corpus has
  evidence of large-doc parsing as the bottleneck.
- **serde**: the `from_str::<T>` shape is the model for
  `json_parse[T]`.
- **yyjson**: tiny + fast C library; useful reference for
  "how compact can a non-SIMD parser be?" (~3k LOC).
- **jq**: not in the language; helper tool.
- **Python json**: pragmatic shape; the *default* mode
  should be the easy one (DOM), with opt-in modes for
  speed.

#### Considered, left

- *Streaming / SAX-shaped parser.* High-engineering, low-
  immediate-need for handler-shape workloads. Body sizes
  are bounded (1 MiB).
- *SIMD-accelerated parser.* See above.
- *jq-shape query language inside lang.* Too much surface
  for the value.

### Date/time — the part every language gets wrong

Sources:
- https://docs.rs/jiff (BurntSushi, 2024 — the most
  thought-through recent attempt)
- https://hinnant.github.io/date_v2.html (Howard Hinnant's
  date library, became `std::chrono` in C++20)
- https://nodatime.org/ (Jon Skeet's .NET library —
  the gold standard before jiff)
- https://www.threeten.org/threetenbp/ (JSR-310, the
  Java 8 redesign)
- https://tc39.es/proposal-temporal/docs/ (TC39
  Temporal — JS's not-yet-shipped redesign)
- https://pkg.go.dev/time (Go's `time` — the cautionary
  tale)

#### What every bad date/time library has in common

Five specific mistakes that show up over and over:

1. **One type for "instant" and "wall clock time."** They're
   different. An instant is "2026-05-19T14:30:00 UTC"
   (a unique point in physical time). A wall-clock time is
   "2026-05-19T14:30:00" — *needs a time zone to become an
   instant*, may not even refer to a valid instant
   (spring-forward DST gap).

2. **Time zone as a string ID baked into the value.** Stops
   working when the IANA tzdb updates. Right shape:
   timezone is a *value*, *separate* from the timestamp,
   with a deliberate "as-of" semantic for which tzdb
   version it refers to.

3. **Mixing absolute time with calendar arithmetic.** "Add 1
   month to 2026-01-31" — what's the answer? Depends on
   whether you want calendar arithmetic (Feb 28 / 29) or
   absolute arithmetic (31 days later). They should be
   different APIs.

4. **Floor division on negative numbers.** "What date is 1
   second before 1970-01-01T00:00:00 UTC?" → 1969-12-31T...
   Easy to get backward.

5. **Leap seconds.** Either acknowledge them or explicitly
   ignore them. Don't pretend by giving wrong answers
   silently.

Go's `time.Time` makes mistakes 1, 2, 3. Python's `datetime`
makes 1 and 2. JavaScript's `Date` makes everything possible.
jiff and NodaTime got it right.

#### jiff — what right looks like in 2024

jiff is BurntSushi's Rust library that synthesises the
NodaTime + Temporal + chrono lessons. Key design choices:

- **Six core types, each one a different *meaning*:**
  - `Timestamp` — instant, UTC, second + nanosecond.
  - `civil::Date` — `(year, month, day)`. No time, no zone.
  - `civil::Time` — `(hour, minute, second, nanosecond)`.
    No date, no zone.
  - `civil::DateTime` — pair of the above. No zone.
  - `Zoned` — `(Timestamp, TimeZone)`. The fully-qualified
    "an instant interpreted in a specific zone."
  - `Span` — calendar duration (years, months, days,
    hours, …); not the same as `Duration` (a fixed
    second + nanosecond difference).

- **Span vs Duration are different types.** `Span::days(1)`
  is calendar-flavoured ("the same wall-clock time
  tomorrow"); `Duration::hours(24)` is absolute
  ("exactly 86400 seconds later"). These are different
  things; they fall back to the same value only in the
  no-DST-shift case.

- **Conversions are explicit.** `Date → Timestamp` requires
  a time zone. `Zoned → Date` discards the zone. No
  implicit coercion in any direction.

- **Parsing / formatting is one big module.** `jiff::fmt`
  has strptime/strftime-shaped patterns plus RFC 3339,
  RFC 2822, RFC 9557 (the timestamp+tz+calendar profile
  for JSON).

- **Time zones are looked up from system tzdb at runtime.**
  Compile-time-baked tzdb is an opt-in via a feature flag.
  Means deploying a binary to a host with newer tz data
  uses the new data.

- **Arithmetic returns `Result`** when the result is
  ambiguous (Feb 30, DST gap, year overflow). Doesn't
  default-to-something. Doesn't return wrong answer.

#### NodaTime — the .NET reference

Same six-type discipline as jiff (predates it by a
decade — jiff is openly inspired by NodaTime). Adds:

- **Calendar abstraction.** Most users want Gregorian, but
  the type system supports Hebrew, Islamic, ISO-week, etc.
  Calendar is a value carried in the type.

- **Pattern types for parsing / formatting.** A `Pattern<T>`
  is a parser-and-formatter pair for type `T`. Pre-compile
  the pattern once, use many times.

#### Temporal (TC39, JS) — what *almost* shipped

Same shape as jiff + NodaTime. Specific transferable bits:

- **`Temporal.Instant` (the UTC instant) is the canonical
  storage shape.** Always store and transmit instants;
  convert to wall-clock only at the display layer.

- **`Temporal.PlainDate`, `PlainTime`, `PlainDateTime`** —
  same naming idea, "plain" prefix marks "no zone." Worth
  copying the prefix for clarity.

- **`Temporal.Now.instant()` vs `Temporal.Now.zonedDateTime()`**.
  Forced to be explicit about which kind of "now" you want.

#### Go's `time.Time` — the cautionary tale

Single type carrying both wall clock and monotonic clock
(`time.Now()` includes both for elapsed-time calculations;
serialisation drops the monotonic part). Mixing wall + zone
in one struct. `Sub`, `Add`, `AddDate` exist side by side,
the difference confusing. Implicit `Local()` zone if you
forget to specify one — programs that work on the dev's
machine break in UTC production.

Go's mistakes are well-known; not repeating them is mostly
a matter of *picking the jiff/NodaTime shape and sticking
to it*.

#### Recommended shape for this lang

Six types, lowercase ergonomic:

```
struct Instant { sec: i64, nsec: i32 }              // UTC, nanos-precision
struct Date { year: i32, month: i32, day: i32 }    // no time, no zone
struct Time { hour, minute, second, nsec: i32 }    // no date, no zone
struct DateTime { date: Date, time: Time }         // no zone
struct Zoned { instant: Instant, zone: TimeZone }  // fully qualified
struct Span { years, months, weeks, days,
              hours, minutes, seconds, nanos: i32 }  // calendar duration
struct Duration { sec: i64, nsec: i32 }            // absolute interval
```

Construction:

```
var now = Instant.now();
var today = Date.today_utc();
var d = Date { year: 2026, month: 5, day: 19 };
var dt = DateTime { date: d, time: Time { hour: 14, minute: 0, second: 0, nsec: 0 } };
var zoned = dt.in_zone(TimeZone.iana("America/New_York"))?;
```

Arithmetic:

```
var tomorrow = today.add_days(1);             // calendar add
var later = now.add(Duration::seconds(3600)); // absolute add
var diff = end.duration_since(start);         // → Duration
var span = end.span_since(start);             // → Span
```

Parse / format:

```
var ts = Instant.parse_rfc3339("2026-05-19T14:30:00Z")?;
var s = ts.format_rfc3339();
```

Timezone data:

```
var tz = TimeZone.iana("America/New_York")?;    // load from system
var local = Instant.now().in_zone(TimeZone.local()?);
```

#### What translates from each source

- **jiff**: the six-type discipline + Span-vs-Duration split.
  Most-recently-thought-through; minimum-friction port.
- **NodaTime**: pattern types for parsing (defer).
- **Temporal**: "plain" prefix on no-zone types (we use no
  prefix; same effect).
- **Howard Hinnant's date**: implementation pointers for
  Gregorian / Julian / civil-date math. Pure-function
  arithmetic that we can transliterate.
- **Go's time**: cautionary lessons (don't merge instant + zone, don't
  default to Local, don't mix wall + monotonic).

#### Considered, left

- *Non-Gregorian calendar abstraction* (Hebrew, Islamic,
  Japanese eras). Real value to .NET because of cultural
  reach; ~zero for edge-handler workloads. Skip.
- *Leap-second handling.* Two camps: "model them" (correct
  but complex) and "ignore them" (Unix convention). Pick
  the latter, document it.
- *Compile-time-baked tzdb.* Lookup-at-runtime is the right
  default; rebuilds-on-tz-update are not the user's problem.

### I/O — readers, writers, async, buffering

Sources:
- https://doc.rust-lang.org/std/io/ (Rust)
- https://pkg.go.dev/io (Go's `io.Reader` / `io.Writer`)
- https://ziglang.org/documentation/master/std/#std.io
  (Zig)
- https://github.com/WebAssembly/wasi-io (WASI Preview 2
  streams)
- https://tokio.rs/ (Rust async I/O)

#### The Reader / Writer pattern

Universal across modern languages. The trait/interface is
two methods:

```
interface Reader { read(buf: []bytes): Result[i32, IoError] }
interface Writer { write(buf: []bytes): Result[i32, IoError] }
```

Read fills the buffer; returns number of bytes filled.
Write consumes the buffer; returns number of bytes
written.

Today's `open_reader / open_writer / read_chunk` already
maps to this shape — it's just not generalised yet. The
move is to:

- Define `Reader` and `Writer` as interfaces (or as
  unboxed-struct-with-function-fields, whatever the
  language ends up calling them).
- Make every I/O source implement them: files (`open_reader`),
  HTTP bodies (`req.body`), sockets (`tcp_recv`), stdin
  (a singleton).
- Build sugar on top: `BufReader` (line-at-a-time),
  `LineReader`, etc.

The win: code can work over *any* stream without caring
where it came from. A JSON parser that consumes a
`Reader` works for files, sockets, request bodies,
stdin, mock test inputs.

#### Sync vs async — the cold-start consideration

Rust's `io::Read` is sync; `tokio::AsyncRead` is async with
function colouring ("async fn"). Go's `io.Reader` is sync
but the runtime handles goroutine scheduling under the
hood. WASI Preview 2's streams are callback-driven; the
host pumps poll events.

**For cold-start edge handlers, sync-shape I/O at the
language level is correct.** The runtime can implement
sync semantics over WASI's poll-based streams transparently
(via a tiny scheduler loop in the platform glue). User
code is straight-line; the runtime hides the async-ness.

This is the Go pattern. The performance hit vs full async
(no real one for single-handler-per-instance shapes), the
complexity savings (no `async`/`await`, no `.then()`, no
function colouring), and the WASI-native fit make sync-at-
language-level the right default.

If the language later picks up multi-handler / long-lived-
server / multi-fiber concurrency, *that's* where async
considerations enter. Today's profile doesn't need them.

#### Buffering

Three levels:

- **Unbuffered.** `tcp_send(conn, buf)` issues a syscall
  per call. Right for small, one-off operations.
- **Buffered writer.** `BufWriter(w, capacity)` accumulates
  writes in a buffer; flushes when full. Right for many
  small writes (line-based output, response serialisation).
- **Memory writer.** `Writer` that writes into a growing
  `[]bytes`. Right for "build the payload in memory,
  send when done."

Today's `tcp_serve` does the right thing (build the
response string, send it all in one `tcp_send`). The
generalisation is letting handler code do the same with
any writer:

```
var buf = MemoryWriter.new();
json_encode_to(buf, payload);
return HttpResponse { status: 200, body: buf.bytes() };
```

#### Error model

`Result[T, IoError]` for every I/O operation. `IoError` is
a sum type:

```
type IoError = NotFound | PermissionDenied | Closed
             | UnexpectedEof | InvalidData | Other(string)
```

Today's I/O functions probably return some `(value,
error)`-shape or a specific `Option`. Generalising to
`Result[T, IoError]` aligns with the rest of the language's
direction (`LANGUAGE-DIRECTION.md ▸ Strong convergence ▸
Multi-return + early-exit error operator`).

#### What translates from each source

- **Rust `io::Read` / `io::Write`**: interface shape +
  sync-at-language-level posture.
- **Go `io.Reader` / `io.Writer`**: same shape, simpler;
  no associated types. Matches lang's posture better.
- **Zig `std.io.GenericReader`**: vtable-shaped Reader
  (Reader is a function pointer + context pointer pair).
  Useful when interfaces aren't first-class.
- **WASI Preview 2 streams**: the platform-glue
  implementation under sync-shaped lang surface.
- **tokio**: don't take async-shape into the language
  surface. Reasonable for long-lived servers, hostile to
  cold-start.

#### Considered, left

- *async/await in the language surface.* No.
- *io_uring-shaped batch I/O.* WASI Preview 2's streams
  are the abstraction layer; io_uring is below that.
- *Generic monads / effect handlers for I/O.* See
  `LANGUAGE-DIRECTION.md ▸ Algebraic effects`. Worth
  prototyping later; not the I/O design question.

## Cross-cutting themes

1. **Six-type discipline for date/time is universal in
   the post-2010 designs.** Instant + Date + Time +
   DateTime + Zoned + Span/Duration. Don't merge them.

2. **Schema-directed parsing is the modern norm for JSON.**
   Serde, jiff (for date strings), every typed-IO library
   shipped recently. The DOM mode is the fallback, not the
   primary surface.

3. **Stream-of-bytes is the right primary for I/O.** Reader
   / Writer interfaces work everywhere — files, sockets,
   HTTP bodies, in-memory buffers, stdin/stdout. Eager-
   read is sugar on top.

4. **Sync-at-language-level is the right default for cold-
   start.** WASI Preview 2's poll-based streams hide under
   a sync Reader / Writer wrapper. Async surface is a
   separate concurrency design (later doc / decision).

5. **Always explicit about UTC vs zoned.** No "default
   timezone." `Instant.now()` and `Zoned.now(zone)` are
   different methods.

6. **`Result[T, Err]` everywhere.** Match the language's
   stated error-handling shape.

7. **Field-name compatibility with Web Standards where
   it's free, no shape compatibility.** `method`, `headers`,
   `body`, `status` — familiar names cost nothing. WHATWG-
   shaped `Headers.get()`-returns-comma-joined-string,
   `body.text()` consumption semantics, `bodyUsed` — wart
   collection, skip.

## Concrete recommendations

Ordered by leverage × cost. These are *design* decisions; the
implementation cost varies but the design lock-in is what
matters most to do *now*.

### 1. Move `HttpRequest.body` to `Stream[bytes]`

**Cost: 1 week (refactor std/http + tcp_serve).**
**Impact: high. Gates streaming, binary, large bodies.**

`body: string` → `body: Stream[bytes]`. Add convenience
methods on `HttpRequest`:

```
pub function (r: HttpRequest).body_string(): Result[string, IoError]
pub function (r: HttpRequest).body_bytes(): Result[bytes, IoError]
pub function (r: HttpRequest).body_json[T](): Result[T, JsonError]
```

Today's handler code `req.body` becomes `req.body_string()?`.
One-line diff per handler. Migration in lockstep with
PLATFORM-RESEARCH.md ▸ Rec §4 (streaming response API).

### 2. Add a real `HeaderMap` type

**Cost: 1 week.** **Impact: medium-high. Fixes duplicate-
header bug.**

`HeaderMap` with multi-valued lookup, case-insensitive
keys, ordered iteration. Replace today's
`headers: Map[string, string]` in `HttpRequest` /
`HttpResponse`.

API surface kept small: `.get(name) → Option[string]`
(first value), `.get_all(name) → []string` (all values),
`.set(name, value)` (replaces), `.append(name, value)`
(adds without replace), iteration in insertion order.

### 3. Schema-directed JSON parse: `json_parse[T](bytes)`

**Cost: 3 weeks (codegen for generic instantiation +
struct-walker IR).** **Impact: high.**

`json_parse(s) -> Value` stays (DOM mode). Add
`json_parse[T](s) -> Result[T, JsonError]` where the
checker monomorphises per `T`. The compiler generates a
walker for each struct type seen as the type arg:

```
struct CreateUser { name: string, age: i32 }
var u = json_parse[CreateUser](req.body_bytes()?)?;
```

The generated decoder walks the input bytes once,
populates `u.name` and `u.age` by field name, returns
`Err(InvalidJson { expected: "string", got: "number",
at_offset: 42 })` on type mismatch.

Lives at `internal/stdlib/std/json.fern` with the
generator as a small IR pass (similar to
`internal/monomorph/`).

### 4. Six-type date/time module

**Cost: 4-6 weeks (new module + tzdb integration).**
**Impact: high — green-field design decision, lock it in
before code accretes.**

New `std/time` (or `std/datetime`) module with:

- `Instant`, `Date`, `Time`, `DateTime`, `Zoned`, `Span`,
  `Duration` (per the jiff shape above).
- `Instant.now()`, `Date.today_utc()`, `Zoned.now(zone)`,
  `TimeZone.iana(name)`, `TimeZone.local()`.
- Arithmetic: `instant.add(duration)`, `date.add_days(n)`,
  `instant.duration_since(other)`, `date.span_since(other)`.
- Parsing / formatting: RFC 3339, ISO 8601, custom patterns.
- Timezone data: lazy load from `/usr/share/zoneinfo` on
  Linux/Mac; bundle a fallback subset for WASI (US/UTC/EU
  zones cover ~80%; full IANA snapshot is ~600 KB).

Reference impl to study: BurntSushi's jiff is MIT-licensed
and well-commented Rust. The algorithms transcribe almost
directly.

### 5. Reader / Writer interfaces in std/io

**Cost: 2 weeks.** **Impact: medium-high. Enables generic
code over streams.**

Define:

```
interface Reader {
    read(buf: bytes): Result[i32, IoError]
}
interface Writer {
    write(buf: bytes): Result[i32, IoError]
    flush(): Result[(), IoError]
}
```

Today's I/O primitives implement them:

- File readers / writers (`open_reader` / `open_writer`)
- HTTP body (`Stream[bytes]` is a Reader)
- TCP sockets
- `MemoryWriter` (for in-memory building)
- `stdin` / `stdout` / `stderr`

Build `BufReader` / `BufWriter` / `LineReader` /
`CountingReader` on top.

### 6. `Result[T, IoError]` everywhere

**Cost: 1 week + cascading test fixes.** **Impact: medium;
consistency win.**

Audit `internal/stdlib/std/io.fern`. Every public function
returns `Result[T, IoError]`. `IoError` is the canonical
sum type. Aligns with the rest of the language.

### 7. JSON encoder structural-walk fallback

**Cost: 1 week.** **Impact: medium. Round-trip with §3.**

`json_encode(value)` works for any plain struct via a
generic walker (analogous to §3 but in the encoding
direction). Properties:

- Pretty-printing flag (`json_encode_pretty`).
- Optional field handling: `None` → omit key (vs serialise
  as `null` — debatable; pick one, document).
- Round-trip with `json_parse[T]`: assert
  `json_parse[T](json_encode(t)) == t` for every struct
  type appearing in test corpora.

### 8. `MemoryWriter` for in-memory building

**Cost: 2 days.** **Impact: small but composable. Pairs
with §5.**

`MemoryWriter.new()` returns a `Writer` that grows a
`bytes` buffer. `.bytes() → bytes` extracts the result.

Used pervasively once §5 is in: build JSON / HTTP /
log records into memory, send when done.

### 9. JSON-Pointer (RFC 6901) helper

**Cost: 1 day.** **Impact: low; nice-to-have for config
parsing.**

```
pub function json_get(doc: Value, pointer: string): Option[Value]
```

Walks `"/users/0/name"`-shape pointers. ~30 LOC. Useful
for poking values out of arbitrary JSON without a schema.

### 10. Outbound HTTP: `plat.fetch(...)`

**Cost: 1 week after PLATFORM-RESEARCH.md Rec §1.**
**Impact: high. Edge handlers want outbound HTTP.**

`plat.fetch(req: HttpRequest) -> Result[HttpResponse,
FetchError]` issued through the wasi-http outbound
interface or native TCP. Same `HttpRequest` /
`HttpResponse` types as inbound (symmetry).

Errors are typed: `FetchError = ConnectFailed |
TlsError | Timeout | DnsError | …`. Not exception-throwing;
caller decides whether to retry, fall back, propagate.

### 11. Format strings — width / precision / fill specs

**Cost: 1-2 weeks.** **Impact: medium.**

Today's `format(fmt, args[])` walks `{}` placeholders.
Extend to Rust-style:

```
format("{:>5}", n)           // right-align, width 5
format("{:.2}", f)           // 2 decimal places
format("{:0>3}", n)          // zero-pad, width 3
format("{:x}", n)            // hex
format("{:b}", n)            // binary
format("{:?}", v)            // debug repr
```

Lex once into a Pattern; pre-validate width / precision
against argument types in the checker (when format string
is a literal).

### 12. CSV / TSV in std/csv

**Cost: 1 week.** **Impact: low; one-off for data tooling.**

Already in the tree (`std/csv.fern`). Audit + flesh out
along with §5 (use `Reader` / `Writer` interfaces).

### 13. URL parsing — separate from query parsing

**Cost: already mostly done (`std/url.fern`).** **Impact:
low; verify it composes with §1.**

`url_parse(s) → Url` exists. Verify the shape works for
both inbound (parse `req.path`) and outbound (build
`fetch` URLs).

### 14. Defer streaming JSON, SIMD JSON, jq

**Cost: 0 (a deferral).** **Impact: avoids
over-engineering.**

Three high-engineering features that should *not* ship
in the v1 stdlib:

- Streaming / SAX-shape JSON parser. Body sizes are
  bounded; not the bottleneck.
- SIMD-accelerated JSON parser. Add when handler
  profiles show JSON parsing as the bottleneck.
- jq-shape query language. Different tool; outside-the-
  language.

### 15. Sort by comparator + `sort_by`

**Cost: 1 week.** **Impact: medium; already on
`STDLIB-ROADMAP.md`'s list.**

Once generic-function-with-closure-argument fully works
(per `ROADMAP-AND-SELF-HOSTING.md ▸ Major gaps`), add
`arr.sort_by(cmp)`. Today's `sort_i32_asc / desc` are
specialisations; the comparator-driven shape generalises.

### 16. Logging — structured records via std/log

**Cost: 1 week.** **Impact: medium. Edge handlers want
structured logs.**

`log.info("user logged in", attrs!{ user_id: u.id,
ip: req.headers.get_or("x-forwarded-for", "") })`

Emits one JSON-line per log call, including:

- `timestamp` (an `Instant` formatted RFC 3339).
- `level` (debug/info/warn/error).
- `message`.
- All key-value attributes.

Outputs to stderr by default; configurable to any
`Writer`. Compatible with `wasi:logging/logging`
interface.

## Anti-patterns — explicit "do not adopt"

- **`HttpRequest.body: string` long-term.** Wrong shape for
  binary, streaming, large bodies. Rec §1 fixes.

- **`headers: Map[string, string]`.** Loses duplicate
  headers; case-sensitive lookup. Rec §2 fixes.

- **Single date/time type carrying both wall + zone (Go's
  `time.Time`).** The world has consensus this is wrong.
  Don't repeat it.

- **Implicit local timezone in `Date.today()`-style
  functions.** Always require explicit zone OR explicit
  `_utc` suffix. `today_utc()` and `today_in_zone(z)`,
  no `today()`.

- **WHATWG Fetch's `body.text()` consumption semantics**
  (read once, then `bodyUsed: true`). Wart; if you want to
  re-read, you're forced to buffer up front. Our shape:
  `body.read_all()` returns bytes; caller buffers or
  doesn't.

- **async/await at the language surface for I/O.** Sync-
  at-language-level over WASI's poll-shaped streams is
  the right cold-start posture.

- **Streaming-by-default for the *easy* case.** Streaming
  is the underlying truth; eager-read string body is the
  sugar. Don't make every handler write 3 lines to read
  a 200-byte JSON body.

- **DOM JSON as the only mode.** Schema-directed is much
  faster and type-safer for typed-payload workloads.

- **jq inside the language.** External tool. Don't grow
  the surface.

## When to revisit

- **When the first handler hits a binary body** (file
  upload, image proxying, gzip-decoded request). Rec §1
  becomes immediately overdue.

- **When the first handler hits `Set-Cookie` semantics**
  (sessions, login). Rec §2 (HeaderMap) overdue.

- **When the first handler parses or formats a date.**
  Rec §4 overdue.

- **When two callers want to consume the same Reader from
  different places.** Indicates either (a) they should be
  buffering up front, or (b) we need a tee-shape Reader
  helper. Add to std/io.

The single highest-leverage *cheap* recommendation —
land before anything else — is **Rec §1
(`HttpRequest.body: Stream[bytes]`) and Rec §2
(HeaderMap)**. They're the breaking-change pair that's
free now and expensive once handler code has them
hard-coded in the current shapes.

The single highest-leverage *expensive* recommendation
is **Rec §4 (six-type date/time)**. The window for
getting it right is *before* any handler code uses
dates. Once `Instant`-vs-`Date` confusion lands in
even one widely-imported helper, fixing it requires
touching every caller.
