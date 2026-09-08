# Checked slice views on x86-64 SSA

Coverage work for coreutils epic #8278 and the compiler gap tracked in #8822.
This adds six cohesive runtime entry points to the register-allocating x86-64
backend: `__slice_make`, `__slice_range`, `__slice_idx`, `__slice_idx_1`,
`__slice_idx_8`, and `__method_string_as_bytes`.

## Contract and implementation

The implementation follows Fern's existing IR layout and ARM64 SSA contract,
not GNU implementation code. A slice is a 16-byte view header containing a
full-width data pointer at offset 0 and an i32 length at offset 8. Its own
8-byte ownership header contains reference count 1 and payload size 16.
The backing buffer is borrowed; making or dropping a view does not acquire or
release the backing buffer's ownership.

The constructor uses the existing guarded x86-64 SSA bump allocator. A
string's `as_bytes()` loads the length from its single-word representation
and tail-calls the constructor without copying the bytes. The dependency is
registered so a module referring only to `as_bytes()` still receives the
constructor, heap reservation and overflow guard.

Construction checks reject negative, reversed and past-end ranges. Element
indexing checks the view's length rather than an array header, then uses the
1-, 4- or 8-byte stride required by the IR. Arguments are narrowed before
address arithmetic so nonzero upper bits on an i32 index cannot turn an
in-range check into an out-of-range address. Bounds traps retain the SSA
backend's status 134 convention. The shipping backend prints a richer
diagnostic; this patch does not unify those diagnostics.

The new helpers preserve callee-saved registers and maintain stack alignment
for the allocator guard call. They do not introduce an x86-64 freelist: its
existing box-free operation is still a no-op. Repeated views therefore retain
the backend's existing non-reclaiming heap limitation. This is not evidence
that x86-64 SSA is ready to become the default.

## Before and after

Base revision: `c2f7057ea`. The reduced string-byte-view test failed before
implementation in debug and release modes because the compiler had no emitter
for `__method_string_as_bytes` and `__slice_idx_1`. Both modes now pass.

The complete sort compile probe initially reported 17 missing helpers. It now
reports 13, with the four slice-related blockers gone. Remaining blockers are:

```text
__method_Reader_close       __method_Reader_read_chunk
__method_Writer_close       __method_Writer_write
args                       env
exit                       open_reader
open_writer                stat
stderr                     stdin
stdout
```

Sort still does not compile through x86-64 SSA. No speed comparison is valid
for that incomplete binary, and no x86-64 native performance measurement is
available on this ARM64 development host. QEMU results below are correctness
evidence only. The standing GNU 9.4+ and Rust uutils performance baselines
remain unchanged.

## Validation

The new CLI corpus passes on Linux with x86-64 execution under QEMU, in debug
and release modes. Each case is also built through the shipping backend and
checked for the same return value or bounds-trap status. Coverage includes
NUL/high bytes, empty strings, byte/i32/i64/string element strides, full 64-bit
values, nested slices, empty end ranges, repeated view creation with another
view live, and negative/reversed/past-end bounds.

Low-level helper tests pass under QEMU. They check exact header fields,
full-width data-pointer aliasing, reference-count behavior, non-overlapping
allocations, 2/4/8-register configurations, transitive heap discovery,
all element strides, i32 extremes and deliberately nonzero upper argument
bits. Existing helper alignment and callee-saved-register checks pass.
The complete backend package also passes on the ARM64 macOS host, where
native-only x86 execution cases are skipped rather than counted as passes.

`make lint-all` passed on the final source tree. The full repository suite
also passed on this isolated candidate: coreutils in 241.471 s, e2e in
1682.466 s and self-host in 125.639 s. The log is retained locally at
`/private/tmp/lang-coreutils-x86-slices-full.log`. These are test durations,
not performance measurements. Integration with newer main commits requires
fresh targeted checks and CI; the old snapshot's pass is not that evidence.

Reproduce targeted cross checks using `lang-pr-ubuntu-cross:24.04`, mounting
this checkout at `/work`, with the usual Go build/module cache volumes:

```sh
go test ./internal/codegen/x86_64ssa -run 'Test(Slice|RuntimeHelper)' -count=1 -v
go test ./internal/e2e -run '^TestX86_64SSASlices$' -count=1 -v
```

Reproduce the remaining coverage diagnostic, which is expected to exit 1:

```sh
go build -o /tmp/fern-x86-slices ./cmd/fern
/tmp/fern-x86-slices -O -target x86-64-linux -backend ssa -o /tmp/sort-x86-slices coreutils/sort.fern
```
