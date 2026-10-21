# Bulk byte-copy loops

Status: [reference] the `compiler/sembytecopy.fern` pass as shipped.

The typed semantic pass in `compiler/sembytecopy.fern` recognizes zero-based
byte walks before ownership planning:

```fern
let i = 0;
while (i < src.len()) { a = a.append(src[i]); i = i + 1; }

let i = 0;
while (i < n) { a = a.with(at + i, src[from + i]); i = i + 1; }
```

The arrays must be owned `u8[]` values, the source and offsets invariant, and
the body exactly the read, update and increment. Counts may be invariant
values, integer literals or the length of an invariant value. The pass keeps
the final index, including zero for a negative count. A source carried by the
receiver's evolving phi, additional effects, extra loop-carried state and
other loop shapes retain their scalar walk.

Each replacement is an ordinary typed call that consumes the receiver and
borrows the source. The unit planner and independent verifier account for
those arguments normally. A source alias of the receiver keeps its owner
alive across the call, so an overlapping write copies the snapshot rather
than propagating earlier writes through later reads.

The helpers in `compiler/rtbytecopy.fern` check complete ranges once and copy
the bytes. A nonpositive count returns the receiver without checking indices.
A unique write reuses its storage; a unique append reuses spare capacity.
Shared or undersized storage allocates once, copies while the old receiver is
still alive, and releases the consumed receiver afterwards. Native helpers
use Fern ownership lowering over the packed array representation. WASM uses
the corresponding linear-memory headers and `memory.copy`.

`ssarc` pushes the private runtime calls in reverse parameter order. Their
physical ownership signatures consume the receiver at push slot 3 or 4;
native entry shims and WASM parameter declarations use that same convention.
The runtime need and symbol inventories include both parent helpers.

The byte-copy tests cover native helper bodies with reuse enabled and
disabled, all three targets, exact allocation counts, ordinary and sanitized
execution, balanced frees, bounds aborts, zero-trip behavior, observable final
indices, sequential-source and effectful-loop controls, encoded WASI output,
and sparse block identities after inlining. The compiler bootstrap remains a
separate check over its full semantic graphs.

`bench/byte_copy_append.fern` and `bench/byte_copy_with.fern` exercise repeated
16 KiB copies and check the resulting contents. The runtime emits only the
requested kernel family and shares its borrowed clone helper. The UTF-8
ingestion benchmarks reach a copy loop in string padding: their native
assembly-line footprint grows by 128 on x86-64 and 159 on ARM64. Those measured
costs are recorded in the performance baseline; its tolerances are unchanged.
