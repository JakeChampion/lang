# Create LICM scratch storage only for functions with loops

The LICM pass scanned every function for its maximum slot and allocated scratch storage before it inspected any loop.
The reverse traversal now locates the final loop before the pass creates that state.
A function without loops returns its operation list and local count unchanged.
The pass resumes at that loop without a second traversal of the suffix.
Its maximum-slot scan still includes the complete operation list.

Both stage-2 compilers use the same stage-1 producer and frozen checker subject.
The producer hash is `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The baseline hash is `fb78dd106a460a9732db18e3d45b3f837aec9b132f3fe49488681403df6eea80`.
The candidate hash is `997c5b370b1180893c71e0f4e1971fff022092c344f5d9ba8cf6ec6d7d1ebf5f`.
The checker and standard library come from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 24,977,303,274 | 24,975,255,111 |
| Calls to `__fern_arr_box` | 17,398,916 | 17,397,831 |
| Calls to `ir.cp_max_slot` | 9,199 | 8,078 |

The fixed compile uses 0.008200% fewer instructions, 1,085 fewer boxes and 1,121 fewer slot scans.
This is a small improvement on the named subject.
It does not establish a native latency improvement.
The complete checker output remains byte-identical on x86-64, ARM64 and Wasm.
Each release binary has the same loaded code and data as its counterpart with symbols.

The sanitized differential runs 350 cases on each target before and after the change.
All six runs have identical output, balanced allocations and zero live bytes.
The cases cover empty inputs, straight-line operations, branches, sparse slots, loops, operation metadata and input aliases.
The [raw evidence](../benchmarks/licm-no-loop-2026-10-28/README.md) retains the inputs, profiles, output records, source identities and commands.

The targeted LICM suites pass 38 test nodes, with zero failures and zero skips.
Source, feature, unchanged complexity and full formatter checks pass.
The formatter covers 2,110 inputs.
Full current-head CI and issue #4412 remain incomplete.
