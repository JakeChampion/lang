# Search predecessor lists only for phi edges

The liveness solver searches each successor's predecessor list to select its phi operands.
It also performs that search when the successor has no phi instructions.
The current checker profile records 1,767,384 calls to `ssalive.pred_index`.

The solver now searches the predecessor list only when the successor has phi instructions.
The phi operand order and dependency expansion remain unchanged.
The input validator still checks every predecessor edge.
Its 681,696 predecessor searches remain unchanged in the measured compile.

The comparison uses two stage-2 compilers from one stage-1 producer.
The baseline compiler sources match `8bcb344e3beb2ba81d0037e2059b48f8ff2ad8df` byte for byte.
The producer hash is `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The fixed checker and standard library come from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 24,995,670,175 | 24,977,303,274 |
| Calls to `__fern_arr_box` | 17,398,916 | 17,398,916 |
| Calls to `ssalive.pred_index` | 1,767,384 | 1,014,941 |

The fixed compile uses 0.073480% fewer instructions and removes 752,443 unused predecessor searches.
Its allocation count remains unchanged.
The complete emitted output remains byte-identical on x86-64, ARM64 and Wasm.
Each release binary has the same loaded code and data as its counterpart with symbols.
This result does not establish a native latency improvement.

The [raw evidence](../benchmarks/phi-predecessor-scan-2026-10-28/README.md) records the source snapshots, commands, profiles and binary identities.
This profile group uses its own common producer and remains separate from earlier measurements.

The liveness, dependency and ownership suites pass 145 test nodes, with zero failures and zero skips.
The controls cover phi edges, loop anchors, filtered live sets, sparse identifiers and invalid metadata on three targets.
Source, feature, unchanged complexity and full formatter checks pass.
The formatter covers 2,100 inputs.
Full current-head CI, the retained-machine integration and issue #4412 remain incomplete.
