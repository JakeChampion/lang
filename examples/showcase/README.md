# Fern, in action

Eight web applications share one native Fern server. The homepage links each application and its source lesson.
The application modules use literate Fern. Named sections explain the code that Fern compiles.

| Application | URL | Server behavior |
| --- | --- | --- |
| Fractal explorer | `/fractal/` | Mandelbrot PNG tiles, perturbation, SIMD, HTTP and WebSocket delivery |
| Rally | `/rally/` | Authoritative game physics, solo opponent, two-player WebSocket rooms |
| Mosaic | `/mosaic/` | Shared pixel boards, atomic WebSocket strokes, immediate revision push, PNG export |
| Gather | `/gather/` | Shared arena for 512 participants, binary WebSocket snapshots, authoritative collection |
| Swarm | `/swarm/` | Browser Wasm particle field and PCM synthesis, WebGL, Web Audio, worker |
| Wave lab | `/wave/` | Sixteen-step sequencer, four oscillators, native PCM WAV synthesis |
| Guestbook | `/notes/` | HTML forms without JavaScript, signed tokens, 303 redirects, Atom feed |
| Pulse | `/pulse/` | Live process memory and CPU, request counters, SSE, Prometheus export |

## Run locally

Build the compiler from the repository root:

```sh
make build
bin/fern -target x86-64-linux -O -o build/showcase examples/showcase/server.fern.md
bin/fern -target wasm32-wasi -emit core-module -O -o examples/showcase/web/swarm/kernel.wasm examples/showcase/swarm.fern.md
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
Its build script verifies the repository bootstrap pin and builds the current compiler from source.
The current compiler builds the showcase and browser Wasm module. The script then prepares assets and the container function archive.

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
PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs CHROMIUM=/usr/bin/chromium \
  node examples/showcase/tests/interaction.browser.mjs http://localhost:8080
```

## Real-time latency

Rally sends coalesced inputs every 16 milliseconds without a stop-and-wait exchange.
The browser predicts its paddle each animation frame, replays unacknowledged input after a reply, and extrapolates the ball.
The server owns physics and scores. It immediately pushes changes to the other player.

Mosaic displays strokes locally and pipelines bounded WebSocket messages.
The server wakes each room subscriber through a loopback datagram descriptor. Idle subscribers need no timer poll.
Reconnects receive a full snapshot. The HTTP mutation and resumable SSE APIs remain available for other clients.

Both applications report network round-trip time separately from server work. Network distance still contributes to round-trip time.

## Browser Wasm

Swarm uses two scalar Fern exports for particle coordinates and audio samples.
These calls allocate no heap memory. The host reuses its graphics buffer and runs audio synthesis in a worker.
The build compiles the core Wasm module before asset preparation and creates all three compressed variants.

Pause and hidden tabs stop the animation loop. Reduced-motion preferences select pause by default.

## Fullscreen and touch controls

Fractal, Rally, Mosaic, Wave, Swarm, and Pulse have fullscreen controls.
Browsers without the Fullscreen API use an expanded view with a visible exit button.
Canvas coordinates account for any space around the rendered image after a resize.

Touch controls cover paddle movement, paint strokes, palette selection, sequencer notes, and all particle interaction modes.
Swarm retains the last touch position until the visitor selects Free flow or resets the field.
Fractal has touch zoom buttons and a control to hide or show its panels.

## Hundreds of participants

Gather has 512 seats per server process. The native server accepts up to 2048 connections for games and other requests.
Each participant submits an eight-byte destination. Fern validates movement and uses an 8 × 8 grid for seed checks.
A shared clock creates ten snapshots per second. Every connection uses the same immutable packet.

A slow participant cannot own or stop the clock. A blocked writer skips superseded snapshots.

The browser predicts local motion and extrapolates other players for at most 150 milliseconds.
Counts represent actual connections. The demo does not add simulated players to the arena.
Multiple instances need connection affinity. This example does not provide a distributed world or persistent player data.

Run the real-connection load test against a local server:

```sh
node examples/showcase/tests/gather.load.mjs http://localhost:8080 512 10
```

A local x86-64 Linux run used an optimized server and 512 real connections for ten seconds.
Each participant sent twenty inputs per second. All participants received approximately ten snapshots per second.
Input acknowledgement latency was 4.7 ms at the median and 9.2 ms at p95.
Server RSS was 14.2 MiB, an increase of 4.16 MiB from the initial sample.
These loopback measurements exclude Internet latency and hosting limits.
