# Fern, in action

Six web applications share one native Fern server. The homepage links each application and its source lesson.
The application modules use literate Fern. Named sections explain the code that Fern compiles.

| Application | URL | Server behavior |
| --- | --- | --- |
| Fractal explorer | `/fractal/` | Mandelbrot PNG tiles, perturbation, SIMD, HTTP and WebSocket delivery |
| Rally | `/rally/` | Authoritative game physics, solo opponent, two-player WebSocket rooms |
| Mosaic | `/mosaic/` | Shared pixel boards, atomic POST strokes, resumable SSE journal, PNG export |
| Wave lab | `/wave/` | Sixteen-step sequencer, four oscillators, native PCM WAV synthesis |
| Guestbook | `/notes/` | HTML forms without JavaScript, signed tokens, 303 redirects, Atom feed |
| Pulse | `/pulse/` | Live process memory and CPU, request counters, SSE, Prometheus export |

## Run locally

Build the compiler from the repository root:

```sh
make build
bin/fern -target x86-64-linux -O -o build/showcase examples/showcase/server.fern.md
./build/showcase examples
```

The server listens on port 8080. Use `-target arm64-linux` or `-target arm64-darwin` for those hosts.
The final argument identifies the examples directory. The process also supports an inherited listener through `LISTEN_FDS`.

## Read the lessons

Open `/_src/` on the server. A source page has syntax colors and linked local imports.
A literate source page also has explanations, linked code sections, and executable examples.
The `.fern` source URLs remain aliases for their canonical `.fern.md` documents.

Run the embedded examples:

```sh
for lesson in examples/showcase/*.fern.md; do
  bin/fern -doctest "$lesson"
done
```

Format the documents with the Fern formatter:

```sh
bin/fern -fmt -w examples/showcase/*.fern.md examples/fractal/server.fern.md
```

## Compression

Dynamic HTML and JSON use `std/http/compress` with native Brotli, gzip, and zstd encoders.
Static files and woven lessons use Brotli, gzip, or zstd variants from the build.
The standard static server selects variants through Accept-Encoding quality values.
SSE and WebSocket retain their stream bodies. PNG and WAV responses bypass text compression.

The build uses Node 22.15 or newer for its three platform encoders. Netlify selects Node 24.
Node does not run in the application container. Fern supplies a fast native Brotli encoder and decoder. The build uses a higher compression level for immutable assets.

Prepare compressed assets locally:

```sh
node examples/showcase/prepare.mjs build/showcase-assets build/showcase
```

The output contains the showcase source, web assets, and compressed source lessons.
The script also prepares variants next to the fractal assets. Git ignores these build files.

`Netlify-Original-Accept-Encoding` takes precedence when present. Otherwise the server uses ordinary Accept-Encoding.
The response includes both header names in Vary.
Set `FERN_ORIGINAL_ACCEPT_ENCODING` to change the proxy header name. An empty value disables restoration.
A reverse proxy must preserve the browser value and overwrite that proxy header.

The Netlify site keeps `examples/fractal` as its build base.
Its build script compiles the showcase, prepares assets, and builds the container function archive.

## State and measurements

Each server process owns its rooms and notes. A restart clears them.
Rally keeps at most 16 rooms. Mosaic keeps at most 32 boards and 256 changes per board.

The guestbook keeps 80 notes. Request bodies have a 16 KiB limit.
Multiple instances require process affinity for a shared room. This example does not provide durable storage.

Pulse reports the process that answers the request. Linux procfs supplies RSS, peak RSS, virtual bytes, swap, and CPU time.

Cgroup files supply container memory use and limits when available. Unsupported fields become JSON null.
CPU percent compares process CPU time with elapsed time. One fully active CPU is 100 percent.

Runtime arena bytes describe the high-water mark. Allocation count is cumulative. Neither field means current live heap bytes.
Handler time ends when the handler returns a response. It excludes subsequent stream delivery time.
The recent request list omits query strings and bodies.

The signed guestbook token prevents cross-site form submissions. The anonymous rate limit is a demonstration, not user authentication.
Public rooms allow any visitor with the room URL to participate.

## Validation

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHost(Showcase|ImportedCastTypes|FractalSource|FractalBrowser)'
node --test examples/showcase/tests/prepare.test.mjs examples/fractal/tests/delivery.test.mjs
```

The core gate runs on x86-64 Linux, ARM64 Linux, and WASI with sanitizer and allocation census checks.
The live HTTP gate checks images, audio, form tokens, Atom, SSE replay, process measurements, and compression negotiation.
The asset test decodes each format with the independent Node platform decoder.
The browser script checks multiplayer, shared canvas edits, WAV playback, forms without JavaScript, and mobile widths.
It requires Playwright and a Chromium executable:

```sh
PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs CHROMIUM=/usr/bin/chromium \
  node examples/showcase/tests/browser.mjs http://localhost:8080
```
