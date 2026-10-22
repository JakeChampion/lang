# wasm-only examples

Each `.fern` file in this directory is a self-contained program
that targets `wasm32-wasi` (or `wasm32-wasi-http`). The header comment explains
what it does, what features it showcases, and the expected output
(or curl shape, for the HTTP examples).

These are grouped separately from the parent `examples/` directory
because they're written for the WebAssembly Component Model worlds —
the `wasm32-wasi` CLI world (`wasi:cli/run`) and the `wasm32-wasi-http` proxy world
(`wasi:http/incoming-handler`). They compile on the native backends
too (the `handle()`-shaped ones synthesise a serve `main` — see
`native_http_handler.fern`), but their point is the component output.

Both targets emit a **self-contained preview-2 component** — no
external `wasi_snapshot_preview1` adapter to supply.

Build a CLI program:

```
$ fern -target wasm32-wasi -o prog.wasm path/to/example.fern
$ wasmtime run prog.wasm
```

Build a wasi:http handler:

```
$ fern -target wasm32-wasi-http -o handler.wasm path/to/example.fern
$ wasmtime serve handler.wasm
```

## Tour

The parent `examples/` directory holds the cross-target basics
(`hello.fern`, `factorial.fern`, `fizzbuzz.fern`, `array.fern`,
`sum_for.fern`) — those compile under both `wasm32-wasi` and `arm64-linux`.

| File | Target | What it shows |
|---|---|---|
| `wc.fern` | wasm32-wasi | argv, stdin / `Reader` API, file I/O via `open_reader`, `Result + match` for fallible operations |
| `json_pretty.fern` | wasm32-wasi | Recursive `match` over std/json's `JsonValue` enum, document-ordered object members, `s.repeat(n)` for indentation |
| `csv_to_json.fern` | wasm32-wasi | Building `JsonValue` trees by hand, `json.JsonObject` for object construction, the pipe operator |
| `word_freq.fern` | wasm32-wasi | `Map[string, i32]` counting, `m.keys()` / `m.values()` snapshots, `s.to_lower` + manual whitespace tokenisation, the immutable-array update idiom (`arr = arr.with(i, v)`) with a tuple-returning sort |
| `use_chain.fern` | wasm32-wasi | Gleam-style `use` desugaring across fallible Option calls, the closure-factory pattern (`adder(7)` returns a closure) — defunctionalisation + inlining together erase the `call_indirect` from the final wat |
| `shape_area.fern` | wasm32-wasi | Tagged-union enums with mixed payloads, exhaustive `match` with payload destructuring, `match` guards, generic enums (`Result[T, E]`), wide payloads (`Cuboid(f64, f64, f64)` with 8-byte slot layout) |
| `echo_handler.fern` | wasm32-wasi-http | The minimal `function handle(req: HttpRequest, plat: platform.Platform): HttpResponse` shape; the implicit per-request arena reclaims every allocation at handler return |
| `native_http_handler.fern` | wasm32-wasi-http / native | The same `handle()` shape, built for the native HTTP path: the checker synthesises a `serve.supervise(...)` `main`, so it compiles to a standalone ELF/Mach-O that serves HTTP/1.1 on `$PORT` |
| `url_router.fern` | wasm32-wasi-http | `url_parse` returning the auto-injected `Url` struct, `query_parse` collecting multi-valued keys into `Map[string, string[]]`, the pipe operator for response building |

Closures capture outer-scope variables by value on every backend —
scalars (`i32` / `i64` / floats / `boolean`) and pointer-shaped
values (strings / arrays / structs) alike. A reference-typed capture
is read-only inside the closure (reassigning one is rejected, as it
could close a reference cycle); thread updated values back out
through the return instead, or pass them as parameters.
