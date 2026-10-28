# Skip shape state for functions without shape constants

The x86 emitter created a shape bitmap for every non-shared function.
It scanned the operands and terminators even when the function had no shape constants.
The helper now creates the bitmap at the first shape constant.
It returns the empty plan immediately when no shape constant exists.
Functions with shape constants retain the original eligibility checks.

Both stage-2 compilers use the same stage-1 producer and frozen checker subject.
The producer hash is `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The baseline hash is `997c5b370b1180893c71e0f4e1971fff022092c344f5d9ba8cf6ec6d7d1ebf5f`.
The candidate hash is `d3836ba198edc88998296255082699171d2d8bb07f18a02cede0b793f15420fb`.
The checker and standard library come from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
Each release binary has the same loaded code and data as its counterpart with symbols.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 24,975,255,111 | 24,972,137,842 |
| Calls to `__fern_arr_box` | 17,397,831 | 17,396,561 |

The fixed compile uses 0.012481% fewer instructions and 1,270 fewer boxes.
This is a small improvement on the named subject.
It does not establish a native latency improvement or a new bootstrap fixpoint.
The complete checker output remains byte-identical on x86-64, ARM64 and Wasm.

A prior prototype also counted live shape candidates and stopped after their last invalidation.
That prototype saved 0.011342% of instructions and the same 1,270 boxes.
The simpler version gives a slightly larger reduction without that counter.
The raw evidence retains both prototypes.

The sanitized differential compares the original helper and both prototypes across 6,048 cases on each target.
All three runs have identical output, balanced allocations and zero live bytes.
The cases cover absent shapes, multiple shapes, operand roles, comparison kinds, terminators, sparse values, shared output and input aliases.
Some helper inputs stress invalid operand indices and are not valid typed SSA graphs.
The differential supplements the real emitter tests.

The targeted emitter suites pass 67 test nodes, with zero failures and zero skips.
The suites cover immediate shapes, per-module shapes, PIE, shared-output refusals and the SSA backend comparison.
Source, feature, unchanged complexity and all 2,110 formatter inputs pass.

The [raw evidence](../benchmarks/shape-empty-2026-10-28/README.md) retains source identities, profiles, commands, differential inputs and validation records.
Full current-head CI and issue #4412 remain incomplete.
