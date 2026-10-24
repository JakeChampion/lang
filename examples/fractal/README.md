# Fractal explorer

The Fern server renders Mandelbrot tiles as PNG images.
The browser uses Leaflet to request tiles through HTTP or a WebSocket.

From the repository root, compile and run the server:

```sh
make build
bin/fern -target x86-64-linux -O -o examples/fractal/server examples/fractal/server.fern
cd examples/fractal
./server
```

Open <http://localhost:8080>.
Use `-target arm64-linux` or `-target arm64-darwin` for those hosts.

`fractal.fern` selects the renderer and applies the palette.
`kernel.fern` contains the SIMD lane scheduler.
`linear.fern` builds reference orbits and linear skips.
`series.fern` builds the shared Taylor prefix.

`server.fern` contains the routes, statistics, and WebSocket protocol.
`std/png`, `std/binary`, and `std/parse` supply the reusable codecs and integer parser.
`source.fern` serves the source browser at `/_src/`.
`web/` contains the browser assets.

`build.sh` creates the container archive for Netlify.
It builds the checkout compiler first, so new FIP contracts do not depend on the nightly release schedule.

## Source browser

Open `/_src/` for the file index or `/_src/server.fern` for a module.
The pages include syntax colors, line anchors such as `#L42`, and links for imports.
Local imports open another source page.
Standard library and compiler imports link to their source in the repository.

The server loads the application modules and tests at startup and prepares their HTML pages.
Requests select those pages from memory.
The container includes the same source files beside the binary.

## FIP boundaries

The renderer allocates an RGB pixel buffer.
The `fip` pixel writer updates that buffer through an `own` parameter.
The direct batch renderer reuses an `EscapeBatch` record through a `fip` function.
Coordinate validation, iteration limits, and palette interpolation also use `fip`.

These kernels need no fresh allocations when their inputs are unique.
Shared inputs can require a copy to preserve the original value.
The tests check both cases.

The complete renderer remains an ordinary function.
The color math methods lack FIP contracts, and PNG compression and response assembly allocate output buffers.
FBIP adds no benefit here because these kernels use only FIP helpers.
See the [application guide](../../docs/FIP-APPLICATION-GUIDE.md) for the contract details.

## Render performance

Four independent `f64x2` streams advance eight pixels.
Completed lanes receive new pixels while the other lanes continue.
Four updates share one escape test. A scalar replay recovers the first escape time and magnitude.
Exact cycle detection and analytic interior tests stop bounded pixels early.
Shallow tiles and tiles with short center orbits use the smaller four-pixel batch renderer.

At zoom 20 and above, nearby pixels can share an orbit prefix.
The renderer computes one reference orbit with double-double arithmetic.
A degree-16 Taylor model and bivariate linear blocks skip updates under error and escape checks.
The direct SIMD kernel finishes each pixel after the prefix.
An unsuitable prefix falls back to direct iteration.

`render_exact` retains the direct `f64` path for comparison.
`render_reference` retains the batch path and its original PNG fixtures.
The default renderer can change boundary pixels because it uses a different numerical calculation.
The zoom limit, iteration limit, and palette remain the same.

Render time still depends on the orbit. No fixed time is guaranteed at every zoom.
The iteration counter measures executed orbit work, including the shared reference and prefix evaluation.
It does not count each skipped update as executed work.
See the [measurements, numerical checks, and CPU reference](../../docs/benchmarks/fractal-acceleration-2026-10-23/README.md).

## Tile delivery

`/api/config` supplies the renderer version and binary size.
Tile URLs use `/tile/{version}/{z}/{x}/{y}`.
The version contains 24 hexadecimal digits from the executable SHA-256 digest.

A renderer change creates different URLs and prevents stale CDN tiles.
A version mismatch returns 404 without a cache policy.
The old tile route redirects to the versioned route.
If the executable is unavailable, the server and browser disable tile caches.

The browser caches at most 256 tiles and 16 MiB of PNG data.
HTTP and WebSocket delivery share this cache and one request scheduler.
The scheduler selects tiles nearest the viewport center first.
It permits two HTTP requests or one WebSocket request at a time.
Duplicate requests share one response, with separate cancellation for each consumer.

An unloaded tile cancels its queued request or signals an active request through `/cancel/{session}/{id}`.
The renderer checks cancellation between rows and before PNG compression.
It yields to the event loop about every four milliseconds.
A one-millisecond poll lets HTTP cancellation reach an active render.
PNG compression completes before another cancellation check can run.
Canceled tiles do not increase the completed tile count.

Explicit cancellation tokens apply to the container that receives them.
Deployments with multiple containers need request affinity for this control route.

The WebSocket route uses `/ws/{version}/{session}`.
Each binary response starts with 32 bytes in network byte order:

| Offset | Field | Type |
| --- | --- | --- |
| 0 | Request ID | u32 |
| 4 | Total render microseconds | u32 |
| 8 | Executed work units | u64 |
| 16 | Completed container tiles | u32 |
| 20 | Compute microseconds | u32 |
| 24 | Color microseconds | u32 |
| 28 | PNG microseconds | u32 |
| 32 | PNG data | bytes |

A canceled request receives a JSON response with its `cancelled` request ID.
HTTP responses expose the same stages through `Server-Timing`.
The client falls back to HTTP after socket failures.
A stale client refreshes its configuration and reloads when the renderer version changes.
The URL hash preserves its view.

## Measurements

The UI shows recent median and p95 latency at the current zoom.
The window contains at most 60 tiles from the last 120 completed tiles.
Latency starts at the tile request and ends after image decode.
Details show median render, compute, color, PNG, queue, and transfer times.
Transfer time estimates the response time after subtraction of server render time.

It includes network and response work, so it is not a direct network measurement.
Cached tiles do not contribute old server times to compute, color, PNG, or render medians.
Local cache hits and CDN cache hits have separate counters.

The palette uses readable numeric tables without heap allocation on reads.
PNG row copies use the shared byte-copy kernel.
At zoom 20 and above, the default renderer selects the fast lossless compressor.
Shallow tiles retain balanced compression to avoid large transfer costs.
`render_exact` and `render_reference` retain the original PNG output.
See the [delivery, PNG, SIMD, and numerical report](../../docs/benchmarks/fractal-delivery-2026-10-24/README.md).

## Errors

Rendering, request parsing, PNG encoding, and binary decoding return `Result` with typed errors.
Decimal parsing checks overflow before arithmetic and accepts the full signed 64-bit range.
Invalid HTTP coordinates return 400. Invalid WebSocket tile requests close with code 1008.

## Checks

```sh
bin/fern -fmt -w examples/fractal
bin/fern -fmt -d examples/fractal
bin/fern -target x86-64-linux -run examples/fractal/fractal_test.fern
bin/fern -target x86-64-linux -run examples/fractal/source_test.fern
bin/fern -target x86-64-linux -run examples/fractal/acceleration_test.fern
node --test examples/fractal/tests/delivery.test.mjs
go test ./internal/testing/e2ecompiler -run 'TestSelfHostFractal|TestSelfHostStaticNumericTables|TestSelfHostPNGFilters' -count=1 -v
```

The Fern suite checks coordinate bounds, binary fields, PNG bytes, and iteration totals.
Its fixtures cover the whole set, interior points, boundary detail, and maximum zoom.
The Go tests run all suites and the private FIP kernels on x86-64, ARM64, and WASI.
The kernel tests check allocation counts from the first call and preserve shared snapshots.
The acceleration tests compare lane refills, partial groups, and deadlines with a scalar oracle.

The browser tests cover priorities, cache limits, cancellation, transport changes, and obsolete renderer versions.
The live HTTP test confirms that cancellation reaches an active render.
The numerical check compares 12,015 points with a 113-bit oracle.
Its points include random samples, analytic boundaries, tile edges, and alternate reference centers.
PNG tests decode all filter and compression pairs with an independent decoder on all three targets.
They also check raw fast DEFLATE streams through empty, short, and 32 KiB window cases.
