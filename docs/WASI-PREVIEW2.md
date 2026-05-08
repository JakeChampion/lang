# WASI Preview 2 migration plan

## Goal

Move the WASM backend from WASI Preview 1 (the legacy core-module
`wasi_snapshot_preview1.fd_*` import shape) to **WASI Preview 2** —
Component Model components, native resource types, streams, and the
`wasi:http` interface for edge-function serving.

End state: `lang -target=wasm prog.lang` produces a `.component.wasm`
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

- Add `--wasi-preview2` flag to the `lang` CLI (off by default).
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

### Step 5 — Add `wasi:http` handler target

A new compile mode: `lang -target=wasi-http prog.lang` produces
a component implementing `wasi:http/incoming-handler.handle`.
The lang program declares:

```
function handle(req: HttpRequest): HttpResponse {
    ...
}
```

instead of `main()`. The runtime takes care of the request /
response plumbing.

### Step 6 — Drop preview-1 emission

Once steps 2-5 cover every builtin, remove the preview-1 code
paths and make preview 2 the default.

## External dependencies

- **`wasm-tools`** (Bytecode Alliance). The
  `wasm-tools component new --adapt …` command wraps a core
  module in a Component Model component using the supplied
  adapter. CI installs the latest release.
- **`wasi_snapshot_preview1.command.wasm`** — the preview-1 →
  preview-2 adapter, also from the Bytecode Alliance. Vendored
  in `vendor/wasi-adapter/` (~120 KB).
- **`wasmtime`** ≥ 14 — already installed by CI for our preview-1
  e2e tests. Preview 2 component support is built in.

## Testing strategy

- Steps 1-2 run *both* preview-1 and preview-2 e2e tests for the
  same lang program. The preview-2 path uses
  `wasmtime run prog.component.wasm`; the preview-1 path uses
  `wasmtime run --wasi=preview1 prog.wasm` (existing).
- Step 3+ progressively replaces the preview-1 imports; preview-1
  tests start to be flagged as deprecated and eventually removed
  in step 6.
- Each step's PR includes a smoke test that the new component
  runs end-to-end through `wasmtime`.

## Open questions

- **Adapter sourcing.** Vendoring the adapter binary in-repo keeps
  builds offline-friendly but bloats clones. Alternative: have the
  `lang` CLI download it on first preview-2 build and cache in
  `~/.cache/lang/`. Decision deferred to step 2.
- **Multi-target binary names.** Currently
  `lang -o prog prog.lang` produces a single output. Preview 2 mode
  produces a `.component.wasm` instead — same path, different
  contents, or `.component.wasm` extension forced? Settle in step 2.
