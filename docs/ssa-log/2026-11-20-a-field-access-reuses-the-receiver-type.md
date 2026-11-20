# Field access reuses the receiver type

Issues #11534 and #8171.

When the checker annotates a field access, it asks for the type of the
receiver to type the field and to resolve the member name. A Map iterator
can ask for the same type a third time. If the annotation has no known
type, each lookup checks the original receiver expression again.
`annotate_typed` now saves that result for all three uses. It constructs a
union type only when the field names a qualified variant.

## Measurement

Each x86-64 compiler built itself from its own source tree. Both compiled
and linked the same copy of `compiler/checker.fern` and its dependencies
from main at `4d85033d4b87`. Callgrind ran with certification enabled and
the on-disk proof cache disabled. The separate allocation measurement used
the settings from `scripts/selfhost-alloc-bench`, with certification disabled.

| Checker compile | Main | Change | Reduction |
| --- | ---: | ---: | ---: |
| x86-64 instructions | 48,432,673,046 | 48,427,422,917 | 5,250,129 (0.011%) |
| Heap allocations | 55,192,789 | 55,152,715 | 40,074 (0.073%) |

Both allocation runs finished with zero live bytes. For each of x86-64
Linux, ARM64 Linux and wasm32 WASI, both compilers emitted the same
checker executable byte for byte.
