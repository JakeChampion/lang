# Counted native string-view descriptors

A heap string slice previously carried an immortal reference count even
though its descriptor needed to be freed. Retaining that descriptor did
nothing, while releasing a view could free it. That prevented sharing the
descriptor through the ordinary retain/release protocol.

Native heap slice descriptors now start with a count of one and a `BORR`
marker in the upper header word. The final release frees the descriptor
alone. Its data still belongs to the source string. Frame descriptors keep
their immortal header, and existing lifetime analysis continues to keep
the source bytes alive. The marker is distinct from the fused-string marker
required for in-place growth, so a unique descriptor does not permit changing
borrowed data.

Both native backends use this layout. The legacy view-ownership helper
retains a counted descriptor; when it encounters an older immortal heap
descriptor, it creates a counted descriptor over the same borrowed data.
WebAssembly's existing copying slice representation is unchanged.

The shared per-module cache key includes a runtime-layout discriminator.
A cached function that produces an immortal descriptor cannot safely be
mixed with callers expecting a counted descriptor, even when its source
and lowered operations are unchanged.

## Validation

On the integration with main `e7e6a51c2`:

- The pinned bootstrap reaches identical stage-2 and stage-3 compilers:
  12,114,849 bytes, SHA-256
  `08fcdc58bb1e9c1ab80ff8f5bf56dbada76c283ab9423a3d36522ef8fb789f5d`.
- The actual stage-2 regression retains and releases a descriptor without
  allocating, verifies its count and contents, and reads the source again.
  Twenty rounds finish with 21 allocations, 21 frees and zero live bytes.
  The parent fails the positive-reference-count assertion as expected.
- An old compiler and old driver source populate a two-module object cache.
  The candidate misses both old entries, emits the same output as a clean
  build, and hits both new entries on the next run.
- Array-view identity and lending tests, native cache tests, and the broader
  view/lifetime suite pass. The full unit suite and all lint gates pass.

## Size

The comparison baseline contains the same main integration and array-view
identity prerequisite, with only the counted-descriptor and cache-key changes
removed. Both compiler sources are built by the final stage-2 compiler.

| Artifact | Before | After |
| --- | ---: | ---: |
| Compiler executable | 12,114,817 bytes | 12,114,849 bytes |
| Compiler code | 10,402,724 bytes | 10,404,156 bytes |
| Compiler unwind data | 582,556 bytes | 582,604 bytes |
| Compiler data | 949,016 bytes | 949,528 bytes |
| Native slice fixture | 33,201 bytes | 33,201 bytes |
| Fixture code | 4,164 bytes | 4,192 bytes |

The compiler adds the marker-emission and release branches, their assembly
text, and the cache discriminator. Its segment sizes are unchanged. The
fixture's 28 extra code bytes implement the counted header and borrowed-data
release check; its unwind and data sections are unchanged. Both versions
pass the same content checks and finish with 21 allocations, 21 frees and
zero live bytes. No timing improvement is claimed and no size baseline changed.

## Remaining D8 work

This is a prerequisite for D8, not its completion. The 20-case conversion
probe is unchanged from the parent: 19 cases allocate once, and the WASM
case that constructs a local slice inside the measured call allocates twice.
All allocation censuses balance. `as_bytes()` still copies and must be
replaced before claiming allocation-free byte views.

A separate probe currently returns `as_bytes()` of a local dynamically
constructed heap string and runs safely because the conversion copies.
The borrowed representation must reject that local-storage escape under
the existing parameter/static lifetime contract.
