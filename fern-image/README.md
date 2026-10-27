# Fern image foundation

This package implements milestones M0, M1, and M2 of the image engine epic.
The core uses Fern without external image libraries.
It supports checked RGB8 and RGBA8 allocation and safe pixel access.
The color module provides sRGB transfer functions, grayscale, channel operations, and alpha compositing.

The resize module provides scalar nearest-neighbor and bilinear filters.
A restricted native PNG codec decodes and encodes noninterlaced RGB8/RGBA8.
Its [sequential row interface](docs/png-rows.md) emits selected rows after a complete filtered-frame decode.
The [PNG provider adapter](docs/png-providers.md) connects fresh decoder sessions to graph rows and cropped tiles.
The [resumable DEFLATE reader](docs/deflate-reader.md) provides bounded output chunks.
The opt-in [incremental PNG interface](docs/incremental-png.md) validates the complete stream, then reconstructs row prefixes without the complete filtered frame.

The [JPEG header parser](docs/jpeg-headers.md) checks a restricted baseline header profile and returns owned tables.
The separate [JPEG coefficient fold](docs/jpeg-coefficients.md) validates a complete restricted baseline entropy scan and emits quantized blocks.
Neither interface decodes JPEG pixels.
The separate [scalar inverse DCT](docs/jpeg-idct.md) reconstructs isolated component blocks from quantized coefficients.
The [restricted grayscale decoder](docs/jpeg-grayscale.md) assembles one-component JPEG samples into packed RGB8 storage.
General color JPEG decoding remains unfinished.
The [restricted JFIF inspector](docs/jpeg-jfif.md) identifies explicit Y or YCbCr sample meaning before RGB assembly.
The [restricted JFIF RGB decoder](docs/jpeg-rgb.md) assembles canonical YCbCr planes with an explicit centered filter.

HTTP and CDN services remain unimplemented.

## Build and test

Use Linux x86-64 with the Fern CPU baseline: x86-64-v3 plus AES-NI.
The scripts require Bash, make, curl, tar, gzip, sha256sum, and grep.
Run these commands from this directory:

```sh
cd ..
make build
cd fern-image
FERN="$PWD/../bin/fern" ./scripts/test.sh
../bin/fern -target x86-64-linux -o build/example examples/pixels.fern
./build/example
```

`make build` uses the pinned bootstrap compiler and builds the local compiler.
The local source includes a formatter fix required by this package.
The downloaded nightly compiler does not contain that fix.
See [the capability audit](docs/fern-capabilities.md) for exact versions and limitations.

To use a compatible compiler, set `FERN` to its absolute path.
The test script type-checks sources and compiles real native executables with heap checks.
It also checks three expected compiler errors.

## API

Import `packages/pixels/pixels.fern` with a relative import.
The package manifest also selects this file as its library entry.
`create` accepts width, height, byte stride, format, and resource limits.
`read_pixel` returns an RGBA pixel value.
`write_pixel` returns a new image value.

Replace the previous image with that value.
Both access functions validate public image values before array access.

See [the example](examples/pixels.fern), [pixel formats](docs/pixel-formats.md), and [memory rules](docs/memory-model.md).
The API remains experimental.
Shared array updates can copy an entire image.

The allocator does not expose a recoverable allocation error through this API.
A per-image limit does not establish a process memory limit.
See [native allocator observations](docs/memory-measurements.md) for measured update and row costs.

## Scope and status

The 310 TAP cases cover compression, pixels, color, resize, PNG, the restricted JPEG slices, and standalone ICC primitives.
JPEG cases cover headers, coefficients, scalar inverse DCT, grayscale, JFIF identification, restricted RGB assembly, and standalone Exif orientation.
They also cover rows, providers, pipelines, regions, caches, RSS parsing, and fusion.
The file I/O probe and pixel example also exit successfully.
The native script checks source executables and additional allocator, cache, fusion, and formatter smoke modes with the sanitizer.
The separate Wasm script runs the same TAP cases and allocator smoke modes through Wasmtime.

The Linux-only RSS workload records its own procfs observations and runs only in the native script.
See [RSS observations](docs/rss-observations.md) for the parent-memory control and measurement limits.
The separate [worker qualification](docs/worker-isolation.md) tests cgroup OOM, deadlines, cancellation, and fresh-process recovery.

Three negative probes confirm compiler restrictions.
The formatter regression also passes before and after the format check.
A tiny synthetic resize baseline exists. Image quality comparisons remain unmeasured.
See [the roadmap](docs/roadmap.md) for proposed issues.

The parent repository does not contain a license file at the audited commit.
This package does not assign a new license to that repository.

See [color semantics](docs/color.md) for numerical contracts and test tolerances.

See [resize semantics](docs/resize.md) for coordinate and alpha rules.

The [native compression audit](docs/compression-audit.md) verifies prerequisites for PNG.
See [PNG compatibility](docs/png.md) before use.
The separate [PNG corpus](docs/png-qualification.md) compares 964 generated cases with an offline Pillow reference.
PNG requires the static-append compiler fix in PR #13208.

The [row pipeline](docs/pipeline-design.md) renders solid sources and nearest-neighbor or bilinear chains on demand.
The [provider contract](docs/providers.md) adds fresh execution sessions, access restrictions, and declared source costs.
It enforces logical row budgets and cooperative cancellation. It is not the complete M4 region engine.

The [region interface](docs/regions.md) plans checked cropped tiles and direct dependency rows.
The [row batch interface](docs/row-cache.md) adds per-call caching with payload and entry limits.
The explicit [cached fusion interface](docs/cached-fusion.md) composes eligible nearest chains without intermediate cached rows.
The [nearest fusion](docs/nearest-fusion.md) optimization removes eligible intermediate rows and preserves coordinate and admission contracts.

The [explicit YCbCr conversion kernel](docs/jpeg-ycbcr.md) converts full-range component samples after chroma reconstruction.
It does not infer color meaning from encoded JPEG data or apply color profiles.

The [component-plane decoder](docs/jpeg-planes.md) reconstructs owned raw JPEG sample planes before chroma reconstruction or color interpretation.

The [centered component-row sampler](docs/jpeg-chroma.md) offers explicit nearest and bilinear filters before color interpretation.

The [Exif metadata reader](docs/jpeg-exif.md) reports primary orientation from a standalone APP1 payload.
The JFIF and RGB entry points retain their strict metadata policy.

The [explicit orientation operation](docs/jpeg-orientation.md) copies decoded RGB8/RGBA8 pixels through the eight Exif mappings.
It does not change encoded JPEG admission or infer a color profile.

The [ICC feasibility decision](docs/icc-feasibility.md) defines a staged native profile path and the qualification gates before color integration.
Complete ICC transforms and metadata-bearing JPEG admission remain unimplemented.
The [restricted ICC inspector](docs/icc-envelope.md) checks standalone v4.4 RGB input/display envelopes with an XYZ PCS.
Its owned metadata does not establish required-tag semantics or transform eligibility.
The [ICC tag readers](docs/icc-tag-data.md) preserve exact XYZ and curve wire values in owned results.
The [scalar ICC curv evaluator](docs/icc-curv-evaluation.md) evaluates identity, positive gamma, and sampled curves with separate numerical qualification.
The [bounded parametric evaluator](docs/icc-parametric-evaluation.md) evaluates all five current ICC functions within an explicit coefficient subset.
The [ICC APP2 assembler](docs/jpeg-icc-app2.md) joins caller-supplied fragment payloads with separate framing, ownership, and logical admission checks.
The [ICC matrix kernel](docs/icc-matrix-evaluation.md) applies signed fixed-point columns to explicit linear RGB with separate exact-rational observations.
The [restricted forward model selector](docs/icc-matrix-model.md) returns owned columns and qualified curve data without LUT fallback or transform eligibility.
The [scalar matrix/TRC composition](docs/icc-matrix-trc.md) returns a linear RGB trace and explicit unclipped relative D50 XYZ.
The [bounded inverse matrix kernel](docs/icc-matrix-solve.md) returns explicit unclipped linear RGB with independent Gaussian reference observations.
The [standalone curve inverses](docs/icc-curve-inverses.md) apply explicit monotonic curve policies with independent mathematical observations.
The [function-zero power inverses](docs/icc-power-inverses.md) qualify the full positive fixed-point exponent domain separately from affine and piecewise inverses.
The [bounded affine inverses](docs/icc-affine-inverses.md) preserve continuous curve policies and the exact range phase within their declared subset.
Typed wire data does not authorize curve evaluation or color transforms.
