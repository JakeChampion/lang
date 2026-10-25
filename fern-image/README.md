# Fern image foundation

This package implements milestones M0, M1, and M2 of the image engine epic.
The core uses Fern without external image libraries.
It supports checked RGB8 and RGBA8 allocation and safe pixel access.
The color module provides sRGB transfer functions, grayscale, channel operations, and alpha compositing.

The resize module provides scalar nearest-neighbor and bilinear filters.
It does not decode files, serve HTTP, or implement a CDN.

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

## Scope and status

Twenty-one TAP cases pass across the essential probe, pixel, color, and resize suites.
The file I/O probe and pixel example also exit successfully.
All seven executables report zero live bytes under the sanitizer.

Three negative probes confirm compiler restrictions.
The formatter regression also passes before and after the format check.
A tiny synthetic resize baseline exists. Image quality comparisons remain unmeasured.
See [the roadmap](docs/roadmap.md) for proposed issues.

The parent repository does not contain a license file at the audited commit.
This package does not assign a new license to that repository.

See [color semantics](docs/color.md) for numerical contracts and test tolerances.

See [resize semantics](docs/resize.md) for coordinate and alpha rules.
