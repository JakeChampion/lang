# Memory model

An image value owns a reference to a Fern byte array.
Fern manages reference counts and releases storage after its final use.
Array updates return a new value.

Retained aliases preserve their original bytes.
A shared update can copy the complete array.
M0 makes no promise that a pixel update costs constant time.

The constructor checks these invariants before allocation:

1. Width and height exceed zero.
2. Width times channel count fits i32.
3. Stride covers all active bytes in a row.

4. Width times height fits i32 and the caller pixel limit.
5. Stride times height fits i32 and both byte limits.
6. The final row offset plus its active bytes fits the allocation.

The allocation includes all row padding, also for the final row.
It contains exactly stride times height bytes.
The allocator initializes every byte to zero.

The hard per-image ceiling is 67,108,864 bytes.
Default limits also permit at most 16,777,216 pixels.
Positive custom limits can lower those budgets.

Custom pixel limits can exceed the default within the hard byte ceiling.

Public structs do not establish opaque storage.
Callers can construct inconsistent image values.
Every public access repeats layout and storage checks before it calculates an offset.

Coordinates must satisfy zero <= x < width and zero <= y < height.
The offset uses checked multiplication and addition.
Validated row bounds prove that each channel offset remains inside the array.

The API reports validation errors as `Result` values.
It does not convert a process allocation failure into an image error.
One image limit does not bound aggregate memory, retained aliases, allocator overhead, or temporary copies.

A production service requires process budgets and isolation.
Thread safety and cross-thread ownership remain outside this milestone.
