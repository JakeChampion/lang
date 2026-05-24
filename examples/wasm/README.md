# wasm-only examples

Each `.fern` file in this directory is a self-contained program
that targets `wasm` (or `wasi-http`). The header comment explains
what it does, what features it showcases, and the expected output
(or curl shape, for the HTTP examples).

These are kept separate from the parent `examples/` directory
because they exercise features that aren't yet supported on
every native backend — closures, float→int casts, and the
prelude functions that depend on them. CI builds every
`examples/*.fern` non-recursively under both arm64 and wasm,
so the wasm-only ones live under here.

Build a CLI program:

```
$ fern -target wasm \
       -wasi-adapter path/to/wasi_snapshot_preview1.command.wasm \
       -o prog.wasm path/to/example.fern
$ wasmtime prog.wasm
```

Build a wasi:http handler:

```
$ fern -target wasi-http \
       -wasi-adapter path/to/wasi_snapshot_preview1.command.wasm \
       -o handler.wasm path/to/example.fern
$ wasmtime serve handler.wasm
```

## Tour

The parent `examples/` directory holds the cross-target basics
(`hello.fern`, `factorial.fern`, `fizzbuzz.fern`, `array.fern`,
`sum_for.fern`) — those compile under both `wasm` and `arm64`.

| File | Target | What it shows |
|---|---|---|
| `wc.fern` | wasm | argv, stdin / `Reader` API, file I/O via `open_reader`, `Result + match` for fallible operations |
| `json_pretty.fern` | wasm | Recursive `match` over the auto-injected `JsonValue` enum, `MapIter` for ordered object iteration, `s.repeat(n)` for indentation |
| `csv_to_json.fern` | wasm | Building `JsonValue` trees by hand, `Map[string, JsonValue]` for object construction, the pipe operator |
| `word_freq.fern` | wasm | `Map[string, i32]` insertion-order iteration, `s.to_lower` + manual whitespace tokenisation, in-place array mutation through pointer-typed receivers |
| `use_chain.fern` | wasm | Gleam-style `use` desugaring across fallible Option calls, the closure-factory pattern (`adder(7)` returns a closure) — defunctionalisation + inlining together erase the `call_indirect` from the final wat |
| `shape_area.fern` | wasm | Tagged-union enums with mixed payloads, exhaustive `match` with payload destructuring, `match` guards, generic enums (`Result[T, E]`), wide payloads (`Cuboid(f64, f64, f64)` with 8-byte slot layout) |
| `echo_handler.fern` | wasi-http | The minimal `function handle(req: HttpRequest, plat: Platform): HttpResponse` shape; the implicit per-request arena reclaims every allocation at handler return |
| `todo_api.fern` | wasi-http | Method-tuple routing, `JsonValue` request body validation via nested `match`, structured error responses |
| `url_router.fern` | wasi-http | `url_parse` returning the auto-injected `Url` struct, `query_parse` collecting multi-valued keys into `Map[string, string[]]`, the pipe operator for response building |

Closures' capture support today is scalar-only (`i32` / `i64` /
floats / `boolean`) — arrays / strings / structs can still be
passed by reference via parameters, since they're pointer-typed
at the wasm layer. See `word_freq.fern`'s `swap_pairs` for the
pattern.
