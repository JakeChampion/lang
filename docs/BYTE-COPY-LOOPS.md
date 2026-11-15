# Bulk byte-copy loops

Status: [reference] the `compiler/sembytecopy.fern` pass as shipped.

The typed semantic pass in `compiler/sembytecopy.fern` recognizes byte walks
before ownership planning:

```fern
let i = 0;
while (i < src.len()) { a = a.append(src[i]); i = i + 1; }

let i = 0;
while (i < n) { a = a.with(at + i, src[from + i]); i = i + 1; }

let i = 0;
while (i < n) { a = a.append(a[a.len() - dist]); i = i + 1; }

for b in src { a = a.append(b); }
```

The third shape is the LZ77 back-reference copy. Its source is the receiver
itself, so when `dist` is less than `n` the appended bytes repeat the last
`dist` bytes. A read of `a[from + i]` with an invariant `from` is the same
copy. Its distance is the receiver's length before the loop minus `from`.

The counter of the first, second and fourth shapes can start at any
invariant value, as in `let i = lo; while (i < hi) { ... }`. The copy is then
`max(hi - lo, 0)` bytes from `lo` plus the offset. The pass computes the
difference in 64 bits. A difference above the largest `i32` is longer than
any array. The pass then gives the helper that `i32`, and the helper aborts
as the loop does. A back-reference loop must start at 0.

The fourth shape is the for-each loop over the source. Its lowering reads the
element at a hidden index, and increments the index in a step block between
the body and the header. The pass takes that step block as part of the body.

The arrays must be owned `u8[]` values, the source and offsets invariant, and
the body exactly the read, update and increment. Counts may be invariant
values, integer literals or the length of an invariant value. A count can
also be a header computation over invariant values, such as `c.ns[k]`. The
header runs at least once, so the pass computes it once before the call. The pass keeps
the final index, including zero for a negative count. Other reads of the
receiver's evolving phi keep their scalar walk. Additional effects, extra
loop-carried state and other loop shapes also keep it.

The first and fourth shapes also apply to arrays of other element types,
and to a view source. The pass then does not call a byte helper. It writes
a range concat (#15829): an `arr_concat` of the receiver, the source, the
start and the count. The source must not be the
receiver that the loop starts from. An element that is a string view keeps
the loop.

The range concat checks a range with a count above zero against the
source. A range outside the source aborts with the index error of the loop.
A count of zero or less appends nothing. The concat helper increments the
count of each counted element that it copies.

Each replacement runs in the body block of the old loop, and a phi in the
exit joins its result with the receiver. The header keeps its test, so an
empty range makes no call.

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

The back-reference helper aborts when `dist` is less than 1 or more than the
receiver's length. It copies in chunks that double in size. Each chunk reads
only bytes that already hold their final values.

`ssarc` pushes the private runtime calls in reverse parameter order. Their
physical ownership signatures consume the receiver at push slot 2, 3 or 4.
Native entry shims and WASM parameter declarations use that same convention.
The runtime need and symbol inventories include all three parent helpers.

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
