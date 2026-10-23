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

`fractal.fern` contains the escape iteration, palette, and PNG encoder.
`server.fern` contains the routes, statistics, and WebSocket protocol.
`binary.fern` contains the shared byte helpers.
`source.fern` serves the source browser at `/_src/`.
`web/` contains the browser assets.

`build.sh` creates the container archive for Netlify.
It builds the checkout compiler first, so new FIP contracts do not depend on the nightly release schedule.

## Source browser

Open `/_src/` for the file index or `/_src/server.fern` for a module.
The pages include syntax colors, line anchors such as `#L42`, and links for imports.
Local imports open another source page.
Standard library and compiler imports link to their source in the repository.

The server loads the six listed Fern files at startup and prepares their HTML pages.
Requests select those pages from memory.
The container includes the same source files beside the binary.

## FIP boundaries

The renderer allocates one pixel buffer with space for 256 PNG rows.
Each row contains one filter byte and 256 RGB pixels.
The `fip` pixel writer updates that buffer through an `own` parameter.
The `fip` escape function reuses an `EscapeBatch` record with a fixed iteration limit.
Coordinate validation, iteration limits, and palette interpolation also use `fip`.

These kernels need no fresh allocations when their inputs are unique.
Shared inputs can require a copy to preserve the original value.
The tests check both cases.

The complete renderer remains an ordinary function.
The color math methods lack FIP contracts, and PNG compression and response assembly allocate output buffers.
FBIP adds no benefit here because these kernels use only FIP helpers.
See the [application guide](../../docs/FIP-APPLICATION-GUIDE.md) for the contract details.

## Render performance

Two independent `f64x2` vectors advance four adjacent pixels in parallel.
Each pixel keeps its first escape time and magnitude for the color calculation.
The kernel checks exact orbit equality every 16 steps against snapshots at intervals that double.
A repeated orbit cannot escape later, so the kernel can stop that pixel early.
The equality test preserves boundary detail.

The renderer retains double precision, the zoom-dependent iteration limit, and the palette.
Deep boundary tiles can still need more work than exterior or interior tiles.
The iteration counter includes the steps up to escape, an exact cycle, or the iteration limit for each pixel.
See the [measurements and reproduction script](../../docs/benchmarks/fractal-2026-10-23/README.md).

## Checks

```sh
bin/fern -fmt -w examples/fractal
bin/fern -fmt -d examples/fractal
bin/fern -target x86-64-linux -run examples/fractal/fractal_test.fern
bin/fern -target x86-64-linux -run examples/fractal/source_test.fern
go test ./internal/testing/e2ecompiler -run 'TestSelfHostFractal' -count=1 -v
```

The Fern suite checks coordinate bounds, binary fields, PNG bytes, and iteration totals.
Its fixtures cover the whole set, interior points, boundary detail, and maximum zoom.
The Go tests run both suites and the private FIP kernels on x86-64, ARM64, and WASI.
The kernel tests check allocation counts from the first call and preserve shared snapshots.
