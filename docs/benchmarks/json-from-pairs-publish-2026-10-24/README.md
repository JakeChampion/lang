# JSON pairs and stage-zero validation

The source commit is `68032dccf9796927460c1476087cf0206e5aadde`.
The base is `76623a46dcb027602363addcadbbfbc340f66929`.
This change resolves #13052.

`json.from(pairs)` constructs a `JsonObject` from key-value pairs.
A repeated key retains its first position and its last value.
The implementation delegates to `object_of`, with the same index behavior.
Tests cover empty input, duplicate keys, twenty indexed keys, and an entries round trip.
The JSON object suite passes all nine tests.

The previous published compiler rejects the new tuple-array constructor with E064.
It also rejects the current test library's `Map.contains` call with E043.
The retained failure log records both conditions.
The new lock comes verbatim from the immutable `stage0-20261024-5e63712` release.
Bootstrap workflow run `40054078777` validated and published all three hosts.
The downloaded x86-64 compiler matches the released lock checksum.

Two matrices pass eleven commands each.
One uses the current compiler and one uses the released stage-zero compiler.
Each matrix covers both checkers, both interpreters, four target builds, and three execution targets.
The Go oracle source differs only in the lock file from the final source commit.
Its compiler and library sources are identical.

All six execution censuses have equal allocation and free counts and zero live bytes.
Native and ARM64 runs each have 866 allocations.
WASI runs each have 958 allocations.
Darwin execution requires the macOS CI runner.

The new published pin builds three identical compiler stages from this source.
Their SHA-256 is `75678f8c6ae21e5e6e17c85ad621f0444fc3c4a6d955e991733c4e57b1694204`.
The changed Fern files pass the canonical format check.

`artifacts.json` records the evidence hashes.
The log archive contains all 44 matrix stdout and stderr files.
`change.patch.gz` contains the complete change from the base.
The proof recorder verifies that the patch reverse-applies to the source commit.
