# WASI Preview 2 migration plan

> **Status (complete).** The migration is done: `fern -target wasm32-wasi` and
> `-target wasm32-wasi-http` compose Component Model components **natively** in
> Go (`internal/wasm/component`), with no `wasm-tools` shell-out and no
> preview-1 adapter. The `-wasi-adapter` flag and the
> `wasm-tools component new --adapt` step have been removed from the
> toolchain. Any mix of the migrated preview-2 imports composes through
> one unified path (`classifyComposeRequest` → `component.Compose`).
> The preview-1 import shape survives only as the bare `-target wasm32-wasi -emit core-module`
> raw-core escape hatch (runnable directly under `wasmtime run`). The e2e
> test infrastructure (`buildComponent`) now composes natively in-process
> too (`component.Compose`), so the suite's only external dependency is
> `wasmtime` to run the components — no `wasm-tools --adapt`, no preview-1
> adapter; a handful of preview-2 tests still shell out to `wasm-tools
> print` purely to inspect the composed output. The sections below are the
> original plan, kept for history.

## Goal

Move the WASM backend from WASI Preview 1 (the legacy core-module
`wasi_snapshot_preview1.fd_*` import shape) to **WASI Preview 2** —
Component Model components, native resource types, streams, and the
`wasi:http` interface for edge-function serving.

End state: `fern -target=wasm32-wasi prog.fern` produces a `.component.wasm`
that runs in any preview-2 host (`wasmtime run`, edge-function
runtimes, etc.) and uses the modern WASI interfaces directly.

## Why

- **Real socket support.** Preview 1's socket story is host-preopen
  only (`wasmtime --tcp-listen=…`); preview 2's `wasi:sockets/tcp`
  lets the guest call `start-listen` and accept connections itself.
- **Streams instead of fd-based I/O.** `wasi:io/streams.read` /
  `.write` are async-shaped, compose with `pollable`, and carry
  proper error types instead of bare errno integers.
- **`wasi:http`.** Components implementing
  `wasi:http/incoming-handler` are the deployment shape for edge
  functions on Cloudflare, Fastly, Cosmonic, etc. — plain
  `function handle(req): Response` instead of `main()` polling a
  socket.
- **Future-proof.** Preview 1 is frozen; new WASI features only ship
  in preview 2 / 0.3.

## Migration steps

Each step is shippable on its own — earlier steps don't break when
later steps land, and tests for the preview-1 path keep running
until step 5.

### Step 1 — Component Model scaffolding

- Add `--wasi-preview2` flag to the `fern` CLI (off by default).
- When the flag is on, post-process the existing preview-1 module
  with `wasm-tools component new --adapt
  wasi_snapshot_preview1=$ADAPTER` to produce a Component Model
  binary. Hosts that expect preview-2 components can now run our
  output unchanged.
- Add `wasm-tools` + the adapter (`wasi_snapshot_preview1.wasm`)
  to CI's tooling install.

This step delivers no new capability — programs still use the
preview-1 imports under the hood — but it lays the wiring for
subsequent steps.

### Step 2 — Migrate `random_bytes` *(this PR)*

Smallest blast radius. Replace the `random_get` import with the
preview-2 equivalent:

```
(import "wasi:random/random@0.2.0" "get-random-bytes"
        (func $rng (param i64 i32)))
```

`list u8` is a component-level type; the canonical-ABI legacy
mangling lowers it to `(param i64 i32)` where the i64 is the
requested length and the i32 is a "return area" pointer where the
host writes a `(ptr, len)` pair. The host calls our exported
`cabi_realloc` to allocate the buffer in our linear memory; we
memcpy the bytes into a length-prefixed + NUL-terminated string
(matching the existing `random_bytes` shape) before returning.

The same lift/lower pattern is reused in later steps. Other
WASI imports (`fd_write`, `fd_read`, args, env, …) still go
through the preview-1 adapter at `wasm-tools component new`
time.

### Step 3 — Migrate stdio + file I/O to streams

Splits across multiple PRs to keep blast radius bounded:

- **3a — stdio writes.** `print` / `write` / `eprint` / `putchar`
  migrate from preview-1 `fd_write(fd=1|2, …)` to preview-2
  `wasi:cli/stdout.get-stdout` / `get-stderr` +
  `wasi:io/streams.[method]output-stream.blocking-write-and-flush`.
  The handle returned by `get-stdout` / `get-stderr` is cached on
  first use in static memory (resource handles are opaque ints
  where 0 is valid, so the cache uses an init-flag bitfield rather
  than a 0-sentinel).
- **3b — `read_line` / stdin.** `wasi:cli/stdin.get-stdin` +
  `wasi:io/streams.[method]input-stream.blocking-read`.
  `__method_Reader_read_line` dispatches at runtime: fd==0 routes
  through the streams path (so `stdin().read_line()` benefits);
  other Readers stay on preview-1 `fd_read` until step 3c.
- **3c — file I/O via streams (this PR).** Reader/Writer structs
  hold `input-stream` / `output-stream` resource handles end-to-
  end (no more fds). `open_reader` / `open_writer` /
  `open_appender` migrate to
  `wasi:filesystem/preopens.get-directories` (cached on first use)
  +
  `wasi:filesystem/types.descriptor.open-at` +
  `descriptor.read-via-stream` /
  `descriptor.write-via-stream` /
  `descriptor.append-via-stream`. Reader/Writer methods
  (`read_line`, `read_chunk`, `write`, `close`) drop their
  preview-1 fd dispatch and always go through streams. Resource
  drops (`[resource-drop]input-stream` /
  `[resource-drop]output-stream`) replace `fd_close`.

  Follow-up shipped separately: `read_file` / `write_file` now
  delegate to `$open_reader` / `$open_writer` + a chunked
  blocking-read / blocking-write-and-flush loop. With that, no
  user-visible builtin still routes through preview-1
  `path_open` / `fd_read` / `fd_write` (the imports stay declared
  for now since other internal paths still reference them).

The IR's existing `Reader` / `Writer` types stay; only the runtime
helpers change shape. After 3c lands, the only preview-1 imports
left are TCP (handled in step 4) plus args / env (step 5 or 6).

### Step 4 — Migrate sockets to `wasi:sockets/tcp` (shipped)

`tcp_listen(port)` now works guest-side. The pipeline:

1. `wasi:sockets/instance-network.instance-network()` produces the
   network handle (cached at memory[124], init bit 4 in the
   flags byte).
2. `wasi:sockets/tcp-create-socket.create-tcp-socket(ipv4)`
   allocates a `tcp-socket` resource.
3. `tcp-socket.start-bind` (15-i32 lowering: self + `borrow<network>`
   + 12 i32s for the `ip-socket-address` variant + retptr) +
   `finish-bind` set the address; we always emit the IPv4 case
   bound to 0.0.0.0:port.
4. `start-listen` + `finish-listen` flip the socket into listen
   mode.
5. `tcp_accept` calls `tcp-socket.subscribe` + `pollable.block`
   to wait for a connection, then `tcp-socket.accept` returns
   `(tcp-socket, input-stream, output-stream)` — a 16-byte
   canonical-ABI result that needs a dynamically-allocated retptr
   since the static 12-byte slot at memory[92] doesn't fit.

The user-facing builtins keep their preview-1 shape for
backward compatibility — every "fd" the program sees from
`tcp_listen` / `tcp_accept` is now a heap pointer to a 12-byte
struct `(tcp_socket, input_stream, output_stream)`. Listener
structs leave the two stream slots zero. `tcp_recv` /
`tcp_send` / `tcp_close` consume that struct: recv blocks-reads
from the input-stream slot, send chunks through the
output-stream via `$__streams_write`, and close drops streams
first then the parent socket (the canonical-ABI rejects parent
drops with live children).

The `--tcp-listen=…` host flag is no longer needed; the only
host privilege the program needs is `-S inherit-network` so
wasmtime allows `create-tcp-socket` and `start-bind`.

### Step 5 — Add `wasi:http` handler target (shipped)

A new compile mode: `fern -target wasm32-wasi-http prog.fern` produces
a component implementing `wasi:http/incoming-handler.handle`.
The Fern program declares:

```
function handle(req: HttpRequest): HttpResponse {
    if (req.path == "/hello") {
        return HttpResponse { status: 200, body: "world" };
    }
    return HttpResponse { status: 404, body: "not found" };
}
```

instead of `main()`. The component matches the upstream
`wasi:http/proxy` world shape, so the same artifact runs under
`wasmtime serve`, Fastly Compute, Netlify Edge Functions, and
Unikraft Cloud — anywhere that hosts a wasi:http proxy
component.

Auto-injected struct shape:

- `HttpRequest { method: string, path: string, body: string }` —
  `method` is the canonical HTTP verb ("GET", "POST", ...);
  for `other(s)` cases the wire string is passed through.
- `HttpResponse { status: number, body: string }` — `status`
  is the i32 HTTP status code; `body` is written verbatim.

Headers, query parameters, and trailers are deferred — they
need a `fields`-shaped multi-map at the Fern level, which is
its own design decision (Fern doesn't have a `map` type yet).
For the targeted use cases (Fastly-Compute-style edge handlers
that mostly consume the body, route by path, and emit JSON or
HTML), this surface is enough to ship real programs.

Wrapper pipeline (see `emitHttpHandlerWrapper`):

1. `incoming-request.method()` — variant lowered through a
   `br_table` over the discriminant; static interned strings for
   the canonical 9 verbs, materialise the `other(s)` payload via
   `__bytes_to_lang_string`.
2. `incoming-request.path-with-query()` — `option<string>`; on
   `None` use the empty string.
3. `incoming-request.consume()` → `incoming-body.stream()` →
   bulk `blocking-read(4096)` loop into a doubling accumulator
   (same shape as `read_file`'s preview-2 path), drop the
   stream, finish the body.
4. Drop the `incoming-request`.
5. Build the `HttpRequest` struct, call user `handle`, read
   the returned `HttpResponse`.
6. `[constructor]fields()` (empty headers) →
   `[constructor]outgoing-response(headers)` →
   `set-status-code` → `body()` → `body.write()` to obtain the
   output-stream.
7. `response-outparam.set(out, Ok(response))` —
   **before** writing body bytes, since the host treats the
   `set` call as the "headers sealed, body can flow" cue.
8. `blocking-write-and-flush` the body via the chunked
   `__streams_write` helper; drop the output-stream.
9. We don't call `outgoing-body.finish` — wasmtime ≥ 30,
   Fastly, and Netlify all treat dropping the output-stream as
   the body terminator. Calling finish post-`set` traps with
   "unknown handle index" because the body has already been
   reaped on the host side.

Static layout note: the http handler still emits a no-op
`_start` export. The wasi-preview1 adapter (`command.wasm`)
unconditionally wires `wasi:cli/run.run` to call `_start`, even
though `wasmtime serve` never invokes run; switching to the
proxy variant of the adapter would drop this requirement.

Limitations / follow-ups:

- No `wasi:cli/environment` import (= `args()` / `env()` will
  fail to satisfy at component-new time inside a handler). Add
  to the `http` world if a real handler needs it.
- No headers, no query parsing, no trailers (see above).
- The body accumulator copies on grow rather than chaining —
  fine for typical request sizes; revisit if measurable.

### Step 6 — Drop preview-1 emission (shipped)

`-target wasm32-wasi` now always emits a Component Model component;
the `-wasi-preview2` flag is gone (the option implied it was
optional, but there's no preview-1 path to fall back on
anymore) and `-wasi-adapter PATH` is required so `wasm-tools
component new --adapt …` can wrap our core module's `_start`
into `wasi:cli/run.run`.

What got removed:

- The `EmitOptions.Preview2` field (was a no-op for the public
  API; deleted entirely).
- All `if !g.preview2 { … }` branches in
  `internal/codegen/wasm/wasm.go` plus the `g.preview2` field on
  the generator.
- `emitOpenHelper` (preview-1 `path_open`-based open),
  `emitFdWriteString` / `emitFdWriteNewline` (preview-1 print
  helpers), the preview-1 bodies of `emitReadLineHelper`,
  `emitReadFileHelper`, `emitWriteFileHelper`,
  `emitReaderReadLineMethod`, `emitReaderReadChunkMethod`,
  `emitWriterWriteMethod`, `emitCloseMethod`, and the preview-1
  TCP helpers.
- The `wasi_snapshot_preview1` imports for `fd_write`, `fd_read`,
  `fd_close`, `path_open`, `random_get`, and `sock_accept`.

What stayed on preview-1 (translated by the adapter's
entry-point trampoline):

- `args_sizes_get` / `args_get`.
- `environ_sizes_get` / `environ_get`.
- `proc_exit` — the adapter wraps this into
  `wasi:cli/exit.exit(result)`; non-zero codes are flattened to
  `Err(())`, so the host process sees exit 1 for any non-zero
  code. Documented limitation under preview-2 0.2.0; preserving
  arbitrary integer codes would need
  `wasi:cli/exit.exit-with-code` from 0.2.1+.

A new `int_to_string(n: number): string` builtin landed
alongside this: the WASM e2e harness needs to observe
`main()`'s i32 return value over stdout (components don't
expose `--invoke main` and `wasi:cli/exit` clamps the exit
code), and `_start` formats the value through `int_to_string`
+ `print` when the test harness sets the new
`EmitOptions.PrintMainResult` flag.

Two latent bugs surfaced and got fixed in passing:

- `==` / `!=` between floats was lowering to `i32.eq` / `i32.ne`
  (the checker forgot to set `BinaryExpr.IsFloat` on equality
  ops). Never hit before because the preview-1 path observed
  floats via `wasmtime --invoke main`, never compared them in
  Fern.
- `cabi_realloc` referenced `$__fern_alloc` unconditionally but
  the alloc helper was gated on `needsArrays || needsStructs`.
  Tiny programs that didn't use arrays / structs failed to
  compile with `unknown func $__fern_alloc`. Now `$__fern_alloc`
  is always emitted.

## External dependencies

- **`wasm-tools`** (Bytecode Alliance). The
  `wasm-tools component new --adapt …` command wraps a core
  module in a Component Model component using the supplied
  adapter. CI installs the latest release.
- **`wasi_snapshot_preview1.command.wasm`** — the preview-1 →
  preview-2 adapter, also from the Bytecode Alliance. Vendored
  in `vendor/wasi-adapter/` (~120 KB).
- **`wasmtime`** ≥ 14 — required at runtime; CI installs the
  latest release. Preview 2 component support is built in.

## Testing strategy

- The WASM e2e suite (internal/e2e/wasm_e2e_test.go +
  wasm_preview2_test.go) all goes through the component pipeline:
  `wasm.EmitWithOptions{PrintMainResult: true}` →
  `wasm-tools component embed`/`new --adapt` →
  `wasmtime run`. Tests skip if any of wasm-tools, wasmtime, or
  the adapter (FERN_WASI_ADAPTER) is missing.
- `runWasm`-style helpers parse main's i32 return value off the
  trailing line of stdout — `_start` formats it via
  `int_to_string` + `print` when `PrintMainResult` is set.
- Float-arithmetic tests express the assertion in Fern itself
  (return 1 on match, 0 otherwise) since float values can't ride
  the i32 stdout channel.

## Open questions

- **Adapter sourcing.** Vendoring the adapter binary in-repo keeps
  builds offline-friendly but bloats clones. Alternative: have the
  `fern` CLI download it on first preview-2 build and cache in
  `~/.cache/lang/`. Decision deferred to step 2.
- **Multi-target binary names.** Currently
  `fern -o prog prog.fern` produces a single output. Preview 2 mode
  produces a `.component.wasm` instead — same path, different
  contents, or `.component.wasm` extension forced? Settle in step 2.
