# Bulk byte-copy loops

Status: [reference] the `compiler/sembytecopy.fern` pass as shipped, with its
fill and move loops.

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

## Fill and move loops (#15974)

The pass also recognizes two shapes on owned arrays of `u8`, of 4-byte
elements (`i32`, `u32`, `char`, `f32`) and of 8-byte elements (`i64`, `u64`,
`f64`):

```fern
for i in at..at + n { a = a.with(i, v); }

let i = 0;
while (i < n) { a = a.with(at + i, a[from + i]); i = i + 1; }
```

The first shape is a fill. The value `v` is a value from before the loop or a
constant, and the element is an integer or a `char`. The second shape is a
move inside one array. Each read sees the writes before it, as in the loop.
The offsets of both shapes are values from before the loop or constants. The
pass computes a constant of the body before the call.

Each shape becomes a call to `__fern_arr_fill_u8`, `_u32` or `_u64`, or to
`__fern_arr_move_u8`, `_u32` or `_u64`, by the size of the element. The kernel
checks the complete range once. When the receiver is not unique, the kernel
copies it first.

A fill of zero, or a fill of bytes, uses `memset`. A move
copies in parts that are not longer than the distance between the two
ranges. Thus each part reads only elements that hold their final values.
Arrays of two element types of one size share a kernel, so a function that
fills both keeps the loop of the second type. WASM uses `memory.fill` and
`memory.copy`.

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
