# Create native view maps only for functions with views

Both native emitters allocated a map of all SSA values for every function.
They scanned that map to count frame slots even when the function had no view instructions.
The helpers now create the map at the first view instruction.
Functions without view instructions return an empty map.
Frame slot indices and their order remain unchanged.

Heap-backed views still require the full map.
The emitters look up their frame offset before the view helper discards that offset.
An initial private prototype omitted this map and failed the full checker compile with an array bounds error.
The corrected source creates the map for every view instruction, then assigns slots only to frame-backed views.
The rejected prototype and failure remain in the raw evidence.

Both stage-2 compilers use producer `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The baseline hash is `eb073fa5b9923c66829d1f417ad5a5fd452f89387213f5ec0b85bbedce1ad0af`.
The candidate hash is `7b2c84743f3b1fbfaa87f36a6622c63204258dfab562e6ac48870c5fe13afba0`.
The frozen checker and profile standard library come from `d9e569d5e5e5731f39efb13c73a373ccc9aca38f`.
Both compiler builds use the frozen standard library from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
Each release binary has identical loaded code and data to its counterpart with symbols.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 25,446,967,823 | 25,435,514,391 |
| Calls to `__fern_arr_box` | 17,683,090 | 17,681,143 |

The fixed compile uses 0.045009% fewer instructions and 1,947 fewer boxes.
Complete checker output remains byte-identical on x86-64, ARM64 and Wasm.
This is a small improvement on the named subject, without a native latency or new bootstrap fixpoint claim.
The new frozen subject differs from the prior shape and LICM measurements.
Those groups do not establish a combined improvement across different subjects.

The sanitized differential passes 1,729 cases on each target with identical output, balanced allocations and zero live bytes.
It checks absent views, heap views, frame views, sparse values, block order, slot order and input aliases.
It requires exact maps whenever any view exists.
For functions without views, it compares absent slots as minus one and requires an empty candidate map.
Some helper graphs use unsupported immediate flags and are not valid typed SSA programs.

The targeted emitter suites pass 65 nodes, with zero failures and zero skips.
They cover frame views, string views, composite views, leaf frames, PIE and shared-output refusals.
Source, feature, unchanged complexity and all 2,110 formatter inputs pass.

The [raw evidence](../benchmarks/view-map-empty-2026-10-28/README.md) retains the source snapshots, profiles, rejected prototype, differential inputs and validation records.
Full current-head CI and issue #4412 remain incomplete.
